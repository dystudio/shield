package supervisor

import (
	"fmt"
	"github.com/pborman/uuid"
	"github.com/starkandwayne/shield/db"
	"github.com/starkandwayne/shield/timespec"
	"strings"
	"time"
)

type JobRepresentation struct {
	UUID  uuid.UUID
	Tspec string
	Error error
}
type JobFailedError struct {
	FailedJobs []JobRepresentation
}

func (e JobFailedError) Error() string {
	var jobList []string
	for _, j := range e.FailedJobs {
		jobList = append(jobList, j.UUID.String())
	}
	return fmt.Sprintf("the following job(s) failed: %s", strings.Join(jobList, ", "))
}

func (s *Supervisor) GetAllJobs() ([]*Job, error) {
	l := []*Job{}
	result, err := s.Database.Query(`
		SELECT j.uuid, j.paused,
		       t.plugin, t.endpoint,
		       s.plugin, s.endpoint,
		       sc.timespec, r.expiry
		FROM jobs j
			INNER JOIN targets   t    ON  t.uuid = j.target_uuid
			INNER JOIN stores    s    ON  s.uuid = j.store_uuid
			INNER JOIN schedules sc   ON sc.uuid = j.schedule_uuid
			INNER JOIN retention r    ON  r.uuid = j.retention_uuid
	`)
	if err != nil {
		return l, err
	}
	e := JobFailedError{}
	for result.Next() {
		j := &Job{Target: &PluginConfig{}, Store: &PluginConfig{}}
		var id, tspec string
		var expiry int
		//var paused bool
		err = result.Scan(&id, &j.Paused,
			&j.Target.Plugin, &j.Target.Endpoint,
			&j.Store.Plugin, &j.Store.Endpoint,
			&tspec, &expiry)
		j.UUID = uuid.Parse(id)
		if err != nil {
			e.FailedJobs = append(e.FailedJobs, JobRepresentation{j.UUID, tspec, err})
		}
		j.Spec, err = timespec.Parse(tspec)
		if err != nil {
			e.FailedJobs = append(e.FailedJobs, JobRepresentation{j.UUID, tspec, err})
		}
		l = append(l, j)
	}
	if len(e.FailedJobs) == 0 {
		return l, nil
	}
	return l, e
}

type Supervisor struct {
	tick    chan int          /* scheduler will send a message at regular intervals */
	resync  chan int          /* api goroutine will send here when the db changes significantly (i.e. new job, updated target, etc.) */
	workers chan Task         /* workers read from this channel to get tasks */
	updates chan WorkerUpdate /* workers write updates to this channel */

	Database *db.DB

	runq []*Task
	jobq []*Job

	nextWorker uint
}

func NewSupervisor(database *db.DB, resyncc chan int) *Supervisor {
	s := &Supervisor{
		tick:    make(chan int),
		resync:  resyncc,
		workers: make(chan Task),
		updates: make(chan WorkerUpdate),
		runq:    make([]*Task, 0),
		jobq:    make([]*Job, 0),

		Database: database,
	}

	if err := s.Resync(); err != nil {
		fmt.Printf("errors encountered while retrieving initial jobs list from database\n")
		if e, ok := err.(JobFailedError); ok {
			for _, fail := range e.FailedJobs {
				fmt.Printf("  - job %s (%s) failed: %s\n", fail.UUID, fail.Tspec, fail.Error)
			}
		} else {
			fmt.Printf("general error: %s\n", err)
		}
		return nil
	}
	if DEV_MODE_SCHEDULING {
		for _, job := range s.jobq {
			job.NextRun = time.Now()
		}
	}

	return s
}

func (s *Supervisor) Resync() error {
	jobq, err := s.GetAllJobs()
	if err != nil {
		return err
	}

	// calculate the initial run of each job
	for _, job := range jobq {
		err := job.Reschedule()
		if err != nil {
			fmt.Printf("error encountered while determining next run of %s (%s): %s\n",
				job.UUID.String(), job.Spec.String(), err)
		} else {
			fmt.Printf("initial run of %s (%s) is at %s\n",
				job.UUID.String(), job.Spec.String(), job.NextRun)
		}
	}

	s.jobq = jobq
	return nil
}

func (s *Supervisor) CheckSchedule() {
	for _, job := range s.jobq {
		if !job.Runnable() {
			continue
		}

		fmt.Printf("scheduling execution of job %s\n", job.UUID.String())
		task := job.Task()
		id, err := s.Database.CreateTask(
			"system", // owner
			"backup",
			"ARGS", // FIXME: need real args
			job.UUID,
		)
		if err != nil {
			fmt.Printf("job -> task conversion / database update failed: %s\n", err)
			continue
		}

		task.UUID = id
		s.runq = append(s.runq, task)

		err = job.Reschedule()
		if err != nil {
			fmt.Printf("error encountered while determining next run of %s (%s): %s\n",
				job.UUID.String(), job.Spec.String(), err)
		} else {
			fmt.Printf("next run of %s (%s) is at %s\n",
				job.UUID.String(), job.Spec.String(), job.NextRun)
		}
	}
}

func (s *Supervisor) Run() {
	// multiplex between workers and supervisor
	for {
		select {
		case <-s.resync:
			if err := s.Resync(); err != nil {
				fmt.Printf("resync error: %s\n", err)
			}

		case <-s.tick:
			s.CheckSchedule()

		case u := <-s.updates:
			if u.Op == STOPPED {
				fmt.Printf("  %s: job stopped at %s\n", u.Task, u.StoppedAt.String())
				if err := s.Database.CompleteTask(u.Task, u.StoppedAt); err != nil {
					fmt.Printf("  %s: !! failed to update database - %s\n", u.Task, err)
				}

			} else if u.Op == OUTPUT {
				fmt.Printf("  %s> %s\n", u.Task, u.Output)
				if err := s.Database.UpdateTaskLog(u.Task, u.Output); err != nil {
					fmt.Printf("  %s: !! failed to update database - %s\n", u.Task, err)
				}

			} else if u.Op == RESTORE_KEY {
				fmt.Printf("  %s: restore key is %s\n", u.Task, u.Output)
				if err := s.Database.CreateTaskArchive(u.Task, u.Output, time.Now()); err != nil {
					fmt.Printf("  %s: !! failed to update database - %s\n", u.Task, err)
				}

			} else {
				fmt.Printf("  %s: !! unrecognized op type\n", u.Task)
			}

		default:
			if len(s.runq) > 0 {
				select {
				case s.workers <- *s.runq[0]:
					s.Database.StartTask(s.runq[0].UUID, time.Now())
					fmt.Printf("sent a task to a worker\n")
					s.runq = s.runq[1:]
				default:
				}
			}
		}
	}
}

func scheduler(c chan int) {
	for {
		time.Sleep(time.Millisecond * 200)
		c <- 1
	}
}

func (s *Supervisor) SpawnScheduler() {
	go scheduler(s.tick)
}