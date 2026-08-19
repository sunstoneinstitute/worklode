package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/secrets"
)

// This file implements `lode secrets`: the runtime surface of spec 017.
// Values pass through exactly two places here — the pack command's inherited
// environment (as the child of `op run`) and the exec command's child
// environment. Neither is ever written, logged, or echoed.

func init() {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Task-declared secrets: catalog, status, exec, purge (spec 017)",
	}
	cmd.AddCommand(newSecretsCatalogCmd(), newSecretsStatusCmd(), newSecretsExecCmd(),
		newSecretsPurgeCmd(), newSecretsPackCmd())
	rootCmd.AddCommand(cmd)
}

func newSecretsCatalogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "catalog",
		Short: "List the org secrets catalog: names, baseline flag, descriptions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.SecretsCatalog(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			out := cmd.OutOrStdout()
			for _, e := range resp.Secrets {
				marker := " "
				if e.Baseline {
					marker = "*"
				}
				fmt.Fprintf(out, "%s %-28s %s\n", marker, e.Name, e.Description)
			}
			fmt.Fprintln(out, "\n* = baseline: packed for every task, no per-task declaration needed")
			return nil
		},
	}
}

func newSecretsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show declared vs materialized secret names for the bound task (names only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			layout, err := layoutFrom(".")
			if err != nil {
				return err
			}
			taskID, _, err := resolveWorktreeTask(layout, ".", "")
			if err != nil {
				return err
			}
			brief, _, err := c.Brief(cmd.Context(), taskID)
			if err != nil {
				return err
			}
			m, _ := secrets.LoadManifest(taskID)

			inKeystore := func(name string) bool {
				_, err := secrets.Fetch(taskID, name)
				return err == nil
			}
			state := func(name string) string {
				switch {
				case contains(m.Declined, name):
					return "declined"
				case contains(m.Materialized, name) && inKeystore(name):
					return "materialized"
				case contains(m.Materialized, name):
					return "missing from keystore"
				default:
					return "unmaterialized"
				}
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "task: %s\n", taskID)
			for _, name := range brief.Task.Secrets {
				fmt.Fprintf(out, "  %-28s %s (declared)\n", name, state(name))
			}
			for _, name := range m.Materialized {
				if !contains(brief.Task.Secrets, name) {
					fmt.Fprintf(out, "  %-28s %s (baseline)\n", name, state(name))
				}
			}
			if len(brief.Task.Secrets) == 0 && len(m.Materialized) == 0 && len(m.Declined) == 0 {
				fmt.Fprintln(out, "  no secrets declared or materialized")
			}
			return nil
		},
	}
}

func newSecretsPurgeCmd() *cobra.Command {
	var taskID string
	cmd := &cobra.Command{
		Use:   "purge [--task <id>]",
		Short: "Remove the task's keystore items (invoked by release hooks)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := taskID
			if id == "" {
				layout, err := layoutFrom(".")
				if err != nil {
					return err
				}
				id, _, err = resolveWorktreeTask(layout, ".", "lode secrets purge --task <id>")
				if err != nil {
					return err
				}
			}
			names, err := secrets.PurgeTask(id)
			if err != nil {
				return err
			}
			if len(names) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "no secrets stored for %s\n", id)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "purged %s for %s\n", strings.Join(names, ", "), id)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "task id (default: the current worktree's task)")
	return cmd
}

// newSecretsPackCmd is the internal child of the ceremony's single `op run`:
// op resolves every NAME=op://ref line into this process's environment under
// one 1Password authorization; pack moves each value into the OS keystore and
// exits. Values never touch disk or the shell.
func newSecretsPackCmd() *cobra.Command {
	var taskID, namesCSV, declinedCSV string
	cmd := &cobra.Command{
		Use:    "pack",
		Hidden: true,
		Short:  "Internal: write op-run-resolved env values into the OS keystore",
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			names := splitNames(namesCSV)
			if taskID == "" || len(names) == 0 {
				return errors.New("--task and --names are required")
			}
			var missing []string
			for _, n := range names {
				if os.Getenv(n) == "" {
					missing = append(missing, n)
				}
			}
			if len(missing) > 0 {
				return fmt.Errorf("not resolved in environment (op run did not supply): %s",
					strings.Join(missing, ", "))
			}
			for _, n := range names {
				if err := secrets.Put(taskID, n, os.Getenv(n)); err != nil {
					return err
				}
			}
			if err := secrets.SaveManifest(secrets.Manifest{
				Task: taskID, Materialized: names, Declined: splitNames(declinedCSV),
				At: time.Now().UTC(),
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "packed %d secrets for %s\n", len(names), taskID)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "task id")
	cmd.Flags().StringVar(&namesCSV, "names", "", "comma-separated names to pack")
	cmd.Flags().StringVar(&declinedCSV, "declined", "", "comma-separated names the operator declined")
	return cmd
}

// execFn wraps syscall.Exec so tests can capture the argv/env instead of
// replacing the test process.
var execFn = syscall.Exec

func newSecretsExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec [--] <command> [args...]",
		Short: "Run a command with the bound task's materialized secrets in its environment",
		Long: "Resolves the task from the wt/<id>-<slug> worktree guard, reads that task's " +
			"items from the OS keystore, injects them as environment variables, and execs. " +
			"Values exist only in the child process. The injected set is exactly the task's " +
			"materialized names — not the catalog, not the operator's secrets.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := layoutFrom(".")
			if err != nil {
				return err
			}
			taskID, _, err := resolveWorktreeTask(layout, ".", "")
			if err != nil {
				return err
			}
			m, ok := secrets.LoadManifest(taskID)
			if !ok || len(m.Materialized) == 0 {
				return fmt.Errorf("no secrets materialized for %s; `lode resume` runs the ceremony", taskID)
			}
			env := os.Environ()
			for _, name := range m.Materialized {
				v, err := secrets.Fetch(taskID, name)
				if err != nil {
					return fmt.Errorf("secret %s is not in the keystore — do not retry or "+
						"work around; `lode block` with reason missing-secret: %s", name, name)
				}
				env = append(env, name+"="+v)
			}
			bin, err := exec.LookPath(args[0])
			if err != nil {
				return err
			}
			return execFn(bin, args, env)
		},
	}
}

// splitNames splits a comma-separated name list, dropping empties.
func splitNames(csv string) []string {
	var out []string
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
