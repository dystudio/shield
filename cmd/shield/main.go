// Jamie: This contains the go source code that will become shield.

package main

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
)

var (
	//== Root Command for Shield

	ShieldCmd = &cobra.Command{
		Use: "shield",
		Long: `Shield - Protect your data with confidence

Shield allows you to schedule backups of all your data sources, set retention
policies, monitor and control your backup tasks, and restore that data should
the need arise.`,
	}

	//== Base Verbs

	createCmd  = &cobra.Command{Use: "create", Short: "Create a new {{children}}"}
	listCmd    = &cobra.Command{Use: "list", Short: "List all the {{children}}"}
	showCmd    = &cobra.Command{Use: "show", Short: "Show details for the specified {{children}}"}
	deleteCmd  = &cobra.Command{Use: "delete", Short: "Delete the specified {{children}}"}
	updateCmd  = &cobra.Command{Use: "update", Short: "Update the specified {{children}}"}
	editCmd    = &cobra.Command{Use: "edit", Short: "Edit the specified {{children}}"}
	pauseCmd   = &cobra.Command{Use: "pause", Short: "Pause the specified {{children}}"}
	unpauseCmd = &cobra.Command{Use: "unpause", Short: "Continue the specified paused {{children}}"}
	pausedCmd  = &cobra.Command{Use: "paused", Short: "Check if the specified {{children}} is paused"}
	runCmd     = &cobra.Command{Use: "run", Short: "Run the specified {{children}}"}
	cancelCmd  = &cobra.Command{Use: "cancel", Short: "Cancel the specified running {{children}}"}
	restoreCmd = &cobra.Command{Use: "restore", Short: "Restore the specified {{children}}"}

	CfgFile, ShieldServer string
	Verbose, ShieldSSL    bool
	ShieldPort            int
)

//--------------------------

func main() {
	viper.SetConfigType("yaml") // To support lnguyen development

	//ShieldCmd.PersistentFlags().StringVar(&CfgFile, "shield_config", "shield_config.yml", "config file (default is shield_config.yaml)")
	ShieldCmd.PersistentFlags().BoolVarP(&Verbose, "verbose", "v", false, "verbose output")

	ShieldCmd.PersistentFlags().StringVar(&ShieldServer, "host", "localhost", "hostname of Shield server (default: localhost)")
	ShieldCmd.PersistentFlags().IntVar(&ShieldPort, "port", 8080, "port of Shield server (default: 8080)")
	ShieldCmd.PersistentFlags().BoolVar(&ShieldSSL, "ssl", false, "enable SSL")

	viper.BindPFlag("Verbose", ShieldCmd.PersistentFlags().Lookup("verbose"))
	viper.BindPFlag("ShieldServer", ShieldCmd.PersistentFlags().Lookup("host"))
	viper.BindPFlag("ShieldPort", ShieldCmd.PersistentFlags().Lookup("port"))
	viper.BindPFlag("ShieldSSL", ShieldCmd.PersistentFlags().Lookup("ssl"))

	addSubCommandWithHelp(ShieldCmd, createCmd, listCmd, showCmd, deleteCmd, updateCmd, editCmd, pauseCmd, unpauseCmd, pausedCmd, runCmd, cancelCmd, restoreCmd)
	ShieldCmd.Execute()

	if Verbose {
		fmt.Println("Config: ", CfgFile, "SSL:", ShieldSSL, "Host:", ShieldServer, "Port:", ShieldPort)
	}
}

func debug(cmd *cobra.Command, args []string) {

	// Trace back through the cmd chain to assemble the full command
	var cmd_list = make([]string, 0)
	ptr := cmd
	for {
		cmd_list = append([]string{ptr.Use}, cmd_list...)
		if ptr.Parent() != nil {
			ptr = ptr.Parent()
		} else {
			break
		}
	}

	fmt.Print("Command: ")
	fmt.Print(strings.Join(cmd_list, " "))
	fmt.Printf(" Argv [%s]\n", args)
}

func addSubCommandWithHelp(tgtCmd *cobra.Command, subCmds ...*cobra.Command) {
	tgtCmd.AddCommand(subCmds...)

	for _, subCmd := range subCmds {
		var children = make([]string, 0)
		var sentence string

		for _, childCmd := range subCmd.Commands() {
			// TODO: if subCommand children have further children, assume compound command and add it
			children = append(children, childCmd.Use)
		}

		if len(children) > 0 {
			if len(children) == 1 {
				sentence = children[0]
			} else {
				sentence = strings.Join(children[0:(len(children)-1)], ", ") + " or " + children[len(children)-1]
			}
			subCmd.Short = strings.Replace(subCmd.Short, "{{children}}", sentence, -1)
		}
	}
}

func invokeEditor(content string) string {

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}

	tmpDir := os.TempDir()
	tmpFile, tmpFileErr := ioutil.TempFile(tmpDir, "tempFilePrefix")
	if tmpFileErr != nil {
		fmt.Fprintln(os.Stderr, "ERROR: Could not create temporary editor file:\n", tmpFileErr)
	}
	if content != "" {
		err := ioutil.WriteFile(tmpFile.Name(), []byte(content), 600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ERROR: Could not write initial content to editor file:\n", err)
			os.Exit(1)
		}
	}

	path, err := exec.LookPath(editor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Could not find editor `%s` in path:\n%s", editor, err)
		os.Exit(1)
	}
	fmt.Printf("%s is available at %s\nCalling it with file %s \n", editor, path, tmpFile.Name())

	cmd := exec.Command(path, tmpFile.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Start()
	if err != nil {
		fmt.Printf("Start failed: %s", err)
	}
	fmt.Printf("Waiting for editor to finish.\n")
	err = cmd.Wait()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Editor `%s` exited with error:\n%s", editor, err)
		os.Exit(1)
	}

	new_content, err := ioutil.ReadFile(tmpFile.Name())

	return string(new_content)
}

func parseTristateOptions(cmd *cobra.Command, trueFlag, falseFlag string) string {

	trueFlagSet, _ := cmd.Flags().GetBool(trueFlag)
	falseFlagSet, _ := cmd.Flags().GetBool(falseFlag)

	// Validate Request
	tristate := ""
	if trueFlagSet {
		if falseFlagSet {
			fmt.Fprintf(os.Stderr, "\nERROR: Cannot specify --%s and --%s at the same time\n\n", trueFlag, falseFlag)
			os.Exit(1)
		}
		tristate = "t"
	}
	if falseFlagSet {
		tristate = "f"
	}
	return tristate
}