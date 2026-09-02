package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
			if len(resp.Secrets) > 0 {
				fmt.Fprintln(out, "\n* = baseline: packed for every task, no per-task declaration needed")
			}
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
			taskID, root, err := resolveWorktreeTask(layout, ".", "")
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
			entry := func(name string) (secrets.ManifestEntry, bool) {
				for _, e := range m.Entries {
					if e.Name == name {
						return e, true
					}
				}
				return secrets.ManifestEntry{}, false
			}
			state := func(name string) string {
				e, ok := entry(name)
				switch {
				case slices.Contains(m.Declined, name):
					return "declined"
				case !ok || !slices.Contains(m.Materialized, name):
					return "unmaterialized"
				}
				for _, item := range e.Items {
					if !inKeystore(item) {
						return "missing from keystore: " + item
					}
				}
				return "materialized"
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "task: %s\n", taskID)
			// A templated entry's detail is what an operator needs to tell
			// "not materialized" from "materialized but the rendered file is
			// gone" — the second is self-healing, the first is not.
			detail := func(name string) {
				e, ok := entry(name)
				if !ok || !e.Templated() {
					return
				}
				rendered := "rendered file absent (the next exec re-renders it)"
				if _, err := os.Stat(filepath.Join(secrets.RenderedDir(root), e.Name)); err == nil {
					rendered = "rendered"
				}
				fmt.Fprintf(out, "    %-26s %s → %s\n", strings.Join(e.Items, " "), e.EnvName(), rendered)
			}
			for _, name := range brief.Task.Secrets {
				fmt.Fprintf(out, "  %-28s %s (declared)\n", name, state(name))
				detail(name)
			}
			for _, name := range m.Materialized {
				if !slices.Contains(brief.Task.Secrets, name) {
					fmt.Fprintf(out, "  %-28s %s (baseline)\n", name, state(name))
					detail(name)
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
			id, root := taskID, ""
			if id == "" {
				layout, err := layoutFrom(".")
				if err != nil {
					return err
				}
				id, root, err = resolveWorktreeTask(layout, ".", "lode secrets purge --task <id>")
				if err != nil {
					return err
				}
			}
			names, err := secrets.PurgeTask(id)
			if err != nil {
				return err
			}
			// Bound to a worktree, drop the whole rendered directory: a file
			// a worktree move stranded before any exec re-recorded its path
			// is not in the manifest for PurgeTask to have unlinked.
			if root != "" {
				if err := os.RemoveAll(secrets.RenderedDir(root)); err != nil {
					return err
				}
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
// op resolves every ITEM=op://ref line into this process's environment under
// one 1Password authorization; pack moves each value into the OS keystore and
// exits. Values never touch disk or the shell.
//
// The plan file is the ceremony's pending manifest — entry structure, item
// names, exported names, template text, declined names. It is a file rather
// than flags because a template is multi-kilobyte multi-line text; it holds
// no value and no resolved reference.
func newSecretsPackCmd() *cobra.Command {
	var taskID, planPath string
	cmd := &cobra.Command{
		Use:    "pack",
		Hidden: true,
		Short:  "Internal: write op-run-resolved env values into the OS keystore",
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if taskID == "" || planPath == "" {
				return errors.New("--task and --plan are required")
			}
			data, err := os.ReadFile(planPath)
			if err != nil {
				return fmt.Errorf("read pack plan: %w", err)
			}
			var plan secrets.Manifest
			if err := json.Unmarshal(data, &plan); err != nil {
				return fmt.Errorf("decode pack plan: %w", err)
			}
			if plan.Task != taskID {
				return fmt.Errorf("pack plan is for %s, not %s", plan.Task, taskID)
			}
			if len(plan.Entries) == 0 {
				return errors.New("pack plan names no entries")
			}
			// The ceremony builds the plan, but pack is the last gate before
			// a name becomes a keystore item and, later, an assignment in an
			// exec child's environment.
			for _, n := range slices.Concat(plan.Materialized, plan.Declined, plan.AllItems()) {
				if !secrets.ValidName(n) {
					return fmt.Errorf("invalid secret name %q", n)
				}
			}
			items := plan.AllItems()
			var missing []string
			for _, n := range items {
				if os.Getenv(n) == "" {
					missing = append(missing, n)
				}
			}
			if len(missing) > 0 {
				return fmt.Errorf("not resolved in environment (op run did not supply): %s",
					strings.Join(missing, ", "))
			}
			for _, n := range items {
				if err := secrets.Put(taskID, n, os.Getenv(n)); err != nil {
					return err
				}
			}
			// A re-run after the declaration narrowed (spec 017 §3,
			// re-materialization) replaces the manifest wholesale, and the
			// manifest is the only authority purge has — keyring cannot
			// enumerate. Drop the dropped items now or they outlive the
			// worktree with no way to reach them.
			if prev, ok := secrets.LoadManifest(taskID); ok {
				stale := prev.AllItems()
				if len(stale) == 0 {
					stale = prev.Materialized // a pre-042 manifest
				}
				for _, n := range stale {
					if !slices.Contains(items, n) {
						if err := secrets.Del(taskID, n); err != nil {
							return err
						}
					}
				}
			}
			plan.At = time.Now().UTC()
			if err := secrets.SaveManifest(plan); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "packed %d secrets for %s\n", len(plan.Materialized), taskID)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "task id")
	cmd.Flags().StringVar(&planPath, "plan", "", "path to the ceremony's pending manifest")
	return cmd
}

// execFn wraps syscall.Exec so tests can capture the argv/env instead of
// replacing the test process.
var execFn = syscall.Exec

func newSecretsExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [--] <command> [args...]",
		Short: "Run a command with the bound task's materialized secrets in its environment",
		Long: "Resolves the task from the wt/<id>-<slug> worktree guard, reads that task's " +
			"items from the OS keystore, injects them as environment variables, and execs. " +
			"Values exist only in the child process. The injected set is exactly the task's " +
			"materialized names — not the catalog, not the operator's secrets. Inherited " +
			"credential-shaped variables (AWS_*, ANTHROPIC_API_KEY, *TOKEN, *SECRET, …) are " +
			"stripped; the shell plumbing (PATH, HOME, locale) is kept.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := layoutFrom(".")
			if err != nil {
				return err
			}
			taskID, root, err := resolveWorktreeTask(layout, ".", "")
			if err != nil {
				return err
			}
			m, ok := secrets.LoadManifest(taskID)
			if !ok || len(m.Entries) == 0 {
				return fmt.Errorf("no secrets materialized for %s; `lode worktree resume` runs the ceremony", taskID)
			}
			// Two entries exporting one name would silently pick a winner in
			// the child, so it is a failure naming both (spec 042 §5) —
			// checked before any keystore read.
			exported := map[string]string{}
			for _, e := range m.Entries {
				if other, dup := exported[e.EnvName()]; dup {
					return fmt.Errorf("entries %s and %s both export %s; fix the task's declaration or the catalog",
						other, e.Name, e.EnvName())
				}
				exported[e.EnvName()] = e.Name
			}

			// The ceremony excludes .worklode/secrets/ when it writes the env
			// file, but exec is what puts plaintext there — so it re-asserts
			// the exclusion rather than trusting a worktree whose exclude
			// file predates this spec. Best-effort and idempotent.
			if slices.ContainsFunc(m.Entries, secrets.ManifestEntry.Templated) {
				excludeSecretsPaths(root)
			}
			injected := make([]string, 0, len(m.Entries))
			for i := range m.Entries {
				e := &m.Entries[i]
				values := make(map[string]string, len(e.Items))
				for _, item := range e.Items {
					v, err := secrets.Fetch(taskID, item)
					if err != nil {
						return fmt.Errorf("secret %s is not in the keystore — do not retry or "+
							"work around; `lode worktree block` with reason missing-secret", item)
					}
					placeholder, _ := secrets.ItemPlaceholder(e.Name, item)
					values[placeholder] = v
				}
				if !e.Templated() {
					// A plain entry's one item is the entry name itself, so
					// ItemPlaceholder left the value keyed by that name.
					injected = append(injected, e.EnvName()+"="+values[e.Name])
					continue
				}
				// Re-rendered every exec: it costs microseconds and makes the
				// file self-healing after deletion, a worktree move, or
				// re-materialization, so there is no staleness to track.
				path, err := secrets.RenderEntry(root, *e, values)
				if err != nil {
					return err
				}
				e.Rendered = path
				injected = append(injected, e.EnvName()+"="+path)
			}
			// The recorded path is what purge unlinks, so it is saved before
			// the exec that replaces this process.
			if err := secrets.SaveManifest(m); err != nil {
				return err
			}
			bin, err := exec.LookPath(args[0])
			if err != nil {
				return err
			}
			strip := slices.Concat(m.Materialized, slices.Collect(maps.Keys(exported)))
			return execFn(bin, args, secrets.ChildEnv(os.Environ(), strip, injected))
		},
	}
	// The wrapped command's flags are its own: without this, cobra claims
	// `lode secrets exec kubectl get pods -n foo`'s -n and fails.
	cmd.Flags().SetInterspersed(false)
	return cmd
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
