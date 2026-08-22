// lode reconcile: repair task and spec activity the ingestion path missed
// (spec 013). Operator command; the server does the work.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func newReconcileCmd() *cobra.Command {
	var repo, task, since string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Repair what webhook ingestion missed",
		Long: `Repair what webhook ingestion missed, in two engines: replay the
stored *.ignored events, then poll GitHub for missed PR, merge and release
facts. Polling is skipped when the server has no GitHub App configured, and
--task skips the replay (an ignored event's task binding is unknown before
its apply runs, so replay cannot be task-scoped). Each engine reports its own
section, so one being skipped or failing does not hide what the other did.

--since means a different column per engine. For replay it bounds
events.received_at — when the event arrived. For the poll it bounds
tasks.updated_at — when the task last changed. Those diverge in exactly the
case reconcile exists for: a task whose merge was never ingested has a stale
updated_at, so too narrow a --since excludes the tasks most in need of
repair. Widen it (or drop it) when hunting a known ingestion gap.

The poll's "repaired" list reports what the run observed on GitHub, not what
it changed. A task whose PRs were already recorded still appears every run,
with the same PR numbers, so a scheduled --json consumer that alerts on a
non-empty repaired list alerts on steady state. The counters derived from
that list inherit the bias: worklode_reconcile_poll_repairs_total and the
"repaired" outcome of worklode_reconcile_poll_candidates_total. What does
track real work is the "checked" outcome of
worklode_reconcile_poll_commit_checks_total — the commits the run had to ask
GitHub about because nothing was recorded for them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if repo != "" && task != "" {
				return fmt.Errorf("--repo and --task are mutually exclusive")
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.Reconcile(cmd.Context(), model.ReconcileInput{
				Repo: repo, Task: task, Since: since, DryRun: dryRun,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ReconcileRender(cmd.OutOrStdout(), resp)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "bound the run to one repo (owner/name)")
	cmd.Flags().StringVar(&task, "task", "", "bound the run to one task id")
	cmd.Flags().StringVar(&since, "since", "", "RFC 3339 time or Go duration (e.g. 720h), against the server clock")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report repairs without writing")
	return cmd
}

func init() {
	rootCmd.AddCommand(newReconcileCmd())
}
