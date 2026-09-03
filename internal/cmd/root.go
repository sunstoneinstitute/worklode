// Package cmd defines the lode command-line interface.
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/buildinfo"
	"github.com/sunstoneinstitute/worklode/internal/cli"
)

var rootCmd = &cobra.Command{
	Use:     "lode",
	Short:   "lode is the Sunstone Institute work tracker",
	Version: buildinfo.Version,
	// SilenceUsage/SilenceErrors: main.go already prints the error returned
	// by Execute() and exits 1. Without these, cobra additionally prints
	// "Error: ..." itself and dumps a full usage block for every runtime
	// error (e.g. a 404 from the server), which drowns the one line that
	// actually matters.
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

// Help headings for the top-level command list. Cobra only splits that list
// into headings once a group exists, so declaring the shortcut group means
// declaring the ordinary one too — otherwise every non-shortcut command lands
// under cobra's "Additional Commands:" fallback.
const (
	commandGroupID  = "commands"
	shortcutGroupID = "shortcuts"
)

// shortcut is one of the top-level aliases 061 §1 L9 fixes: a nested command
// that also sits at the root because it runs many times per session. This
// table is the only place a top-level alias is declared — no cobra `Aliases`
// anywhere — and L9 closes the list at four entries: `next` (work next),
// `status` (work status), `board` (task board), `overview` (project
// overview). Adding a fifth requires amending 061 §1 L9. They are permanent
// API, not compatibility aliases.
type shortcut struct {
	// target is the real command's path below the root, e.g. task board.
	target []string
	// build constructs a second instance of that command: cobra sets a
	// command's parent in AddCommand, so one pointer cannot sit under two.
	build func() *cobra.Command
	// reason is why L9 grants this command a shortcut.
	reason string
}

var shortcuts = []shortcut{
	// The board is the first thing read on entering a project and re-read
	// after every state change.
	{target: []string{"task", "board"}, build: newBoardCmd, reason: "read many times per session"},
	// next is how an agent enters Worklode mode: run once per task claimed.
	{target: []string{"work", "next"}, build: newNextCmd, reason: "run many times per session"},
	// status is the standing "where am I" check, run constantly during work.
	{target: []string{"work", "status"}, build: newStatusCmd, reason: "run many times per session"},
}

func init() {
	rootCmd.PersistentFlags().Bool("json", false, "print the raw JSON response instead of a table")

	rootCmd.AddGroup(
		&cobra.Group{ID: commandGroupID, Title: "Available Commands:"},
		&cobra.Group{ID: shortcutGroupID, Title: "Shortcuts:"},
	)
	rootCmd.SetHelpCommandGroupID(commandGroupID)
	rootCmd.SetCompletionCommandGroupID(commandGroupID)
	for _, s := range shortcuts {
		cmd := s.build()
		cmd.GroupID = shortcutGroupID
		rootCmd.AddCommand(cmd)
	}
}

// groupTopLevel files every ordinary top-level command under the "Available
// Commands:" heading. Registration is spread across per-file init() funcs
// whose relative order is not fixed, so this runs once all of them have.
func groupTopLevel() {
	for _, c := range rootCmd.Commands() {
		if c.GroupID == "" {
			c.GroupID = commandGroupID
		}
	}
}

// rejectStrayGroupArgs makes every command group refuse an argument it has no
// subcommand for.
//
// cobra's default (legacyArgs) errors on an unknown first argument for the
// root command only; under any other parent it accepts anything, and a parent
// with no Run falls through to printing help and exiting 0. That turned a
// renamed subcommand into a silent success for callers this repo cannot see —
// `lode task ready $ID && echo done` printed "done" and published nothing
// (WL-480).
//
// Both assignments are needed. cobra returns flag.ErrHelp from its
// !Runnable() check *before* it calls ValidateArgs, so Args alone is never
// consulted on a group; RunE is what makes the group runnable far enough to
// reach its own validation, and it prints the help a bare `lode task` still
// owes. A parent that is already runnable is left alone: `lode project crew
// <project>` has subcommands and takes a real positional argument.
//
// Set here rather than on each constructor so a new group cannot forget it.
func rejectStrayGroupArgs(c *cobra.Command) {
	if c.HasSubCommands() && !c.Runnable() {
		// RunE is the half that matters: a non-runnable parent returns
		// flag.ErrHelp before ValidateArgs is ever reached, so an Args
		// validator on it is dead code. cobra's own `completion` command
		// proves it — it ships with Args: NoArgs and still exits 0 on a
		// stray argument. Only supply Args when the command has none, so a
		// parent that declares its own validator keeps it.
		if c.Args == nil {
			c.Args = cobra.NoArgs
		}
		c.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	}
	for _, sub := range c.Commands() {
		rejectStrayGroupArgs(sub)
	}
}

// Execute runs the root command.
func Execute() error {
	groupTopLevel()
	// cobra builds `completion` and its four shell subcommands lazily inside
	// Execute, so they have to be materialised first or the walk never sees
	// them and `lode completion bogus` keeps exiting 0.
	rootCmd.InitDefaultCompletionCmd()
	rejectStrayGroupArgs(rootCmd)
	return rootCmd.Execute()
}

// jsonOut reports whether --json was passed to cmd (or an ancestor of it).
func jsonOut(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

// newAPIClient loads the client config (LODE_SERVER/LODE_TOKEN env vars override
// a repo-local .worklode/config.toml, which overrides
// ~/.config/worklode/config.toml) and returns a ready-to-use Client, or an error
// telling the user how to configure the server URL.
func newAPIClient() (*cli.Client, error) {
	c, _, err := newAPIClientWithConfig()
	return c, err
}

// newAPIClientWithConfig is newAPIClient plus the config it was built from,
// for commands that also read config values such as current_project.
func newAPIClientWithConfig() (*cli.Client, cli.Config, error) {
	cfg, err := cli.LoadConfig()
	if err != nil {
		return nil, cli.Config{}, err
	}
	if cfg.ServerURL == "" {
		return nil, cli.Config{}, errors.New(`server URL not set: set LODE_SERVER, or add server = "https://..." to ~/.config/worklode/config.toml`)
	}
	return cli.NewClient(cfg), cfg, nil
}

// printJSON writes v as the command's --json output. Used by the commands
// whose JSON shape is assembled client-side rather than passed through from
// the server.
func printJSON(cmd *cobra.Command, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	printRaw(cmd, b)
	return nil
}

// printRaw writes a raw JSON response body to cmd's stdout, adding a
// trailing newline if the body doesn't already end with one. Used by every
// command's --json path. A nil/empty raw (e.g. a 204 response) prints nothing.
func printRaw(cmd *cobra.Command, raw []byte) {
	if len(raw) == 0 {
		return
	}
	out := cmd.OutOrStdout()
	out.Write(raw)
	if raw[len(raw)-1] != '\n' {
		fmt.Fprintln(out)
	}
}
