// lode reconcile: repair task and spec activity the ingestion path missed
// (spec 013). Operator command; the server does the work.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func newReconcileCmd() *cobra.Command {
	var repo, task, since string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Repair what webhook ingestion missed",
		Args:  cobra.NoArgs,
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
			verb := "repaired"
			if resp.DryRun {
				verb = "would repair"
			}
			cmd.Printf("run %s\n", resp.RunID)
			if resp.Replay != nil {
				cmd.Printf("replay: %s %d of %d candidate event(s), %d still unmapped\n",
					verb, resp.Replay.Replayed, resp.Replay.Candidates, resp.Replay.StillUnmapped)
				for _, e := range resp.Replay.Errors {
					cmd.Printf("  error: %s\n", e)
				}
			}
			switch {
			case resp.PollSkipped != "":
				cmd.Printf("poll: skipped (%s)\n", resp.PollSkipped)
			case resp.Poll != nil:
				cmd.Printf("poll: %v\n", resp.Poll)
			}
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
