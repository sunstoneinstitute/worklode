// worklisten.go: `lode work listen` — what an unattended worker loop needs
// beyond the lifecycle pair in lifecycle.go. Block until the backbone holds
// work this worker could claim, so a supervisor can sleep instead of
// spinning on `lode work next`.
//
// `listen` asks the same question `lode work next` asks, through the same endpoint
// with DryRun set, rather than reconstructing "is there ready work" from a
// task list. Ranking, focus, blocked-ness and every other eligibility rule
// live server-side (spec 005); a list filter here would be a second, drifting
// answer to a question the claim path already answers exactly.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// listenRetryFloor is the first backoff after a transport failure. It doubles
// up to the poll interval, so a server that is down briefly costs one short
// retry and a server that is down for hours costs one poll per interval.
const listenRetryFloor = 5 * time.Second

func newListenCmd() *cobra.Command {
	var scope scopeFlags
	var kind string
	var strictFocus bool
	var interval time.Duration
	var once bool
	cmd := &cobra.Command{
		Use:   "listen",
		Short: "Wait until there is work this worker could claim, then report it",
		Long: "Polls the claim path in dry-run mode and reports the task `lode work next` " +
			"would pick, without claiming it. Blocks until there is one.\n\n" +
			"The filter flags are exactly those `lode work next` takes, so one filter " +
			"can drive both a listener and the loop it wakes.\n\n" +
			"Without --once it keeps watching, printing a line each time the pick " +
			"changes — the same pick sitting unclaimed is not reprinted. With " +
			"--once it exits 0 on the first pick, leaving restart to the caller.\n\n" +
			"A dry-run pick is not a reservation: another worker may claim it " +
			"first, so a caller must treat this as \"there was work\", not \"this " +
			"task is yours\".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval <= 0 {
				return fmt.Errorf("--interval must be positive, got %s", interval)
			}
			warnDeprecatedTaskKind(cmd, kind)
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			return runListen(cmd, c, model.ClaimNextInput{
				Project:     sc.Project,
				Kind:        kind,
				StrictFocus: strictFocus,
				DryRun:      true,
			}, interval, once)
		},
	}
	addScopeFlags(cmd, &scope, "watch this project")
	cmd.Flags().StringVar(&kind, "kind", "", "only wake for this kind: feature, bug, chore, design, review, spike, decision")
	cmd.Flags().BoolVar(&strictFocus, "strict-focus", false,
		"restrict the watch to the project's focus concerns only, the way lode work next --strict-focus does")
	cmd.Flags().DurationVar(&interval, "interval", 5*time.Minute, "how often to ask (e.g. 30s, 5m)")
	cmd.Flags().BoolVar(&once, "once", false, "exit 0 on the first pick instead of watching")
	return cmd
}

// runListen polls until a pick appears, printing each new one.
//
// Signals end it as a success: a listener is meant to be stopped, and a
// supervisor restarting one should not read SIGTERM as a failure.
func runListen(cmd *cobra.Command, c *cli.Client, in model.ClaimNextInput, interval time.Duration, once bool) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	out := cmd.OutOrStdout()
	asJSON := jsonOut(cmd)
	enc := json.NewEncoder(out)
	header := false

	// lastID suppresses the repeat: an unclaimed pick is returned by every
	// poll, and reprinting it each interval would bury the transitions a
	// reader is actually watching for. Cleared when the queue empties, so the
	// same task coming back after a lull is reported again.
	lastID := ""
	backoff := listenRetryFloor

	for {
		resp, _, err := c.ClaimNext(ctx, in)
		switch {
		case err == nil:
			backoff = listenRetryFloor
			switch {
			case resp.Task == nil:
				lastID = ""
			case resp.Task.ID != lastID:
				lastID = resp.Task.ID
				if asJSON {
					if err := enc.Encode(resp.Task); err != nil {
						return err
					}
				} else {
					if !header {
						cli.WorkerPickHeader(out)
						header = true
					}
					cli.WorkerPickRow(out, *resp.Task)
				}
				if once {
					return nil
				}
			}
		case ctx.Err() != nil:
			return nil
		case fatalListenErr(err):
			return err
		default:
			// Transport failures and server-side 5xx are what a long-lived
			// listener exists to ride out, so they warn and retry rather than
			// ending the watch.
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v; retrying in %s\n", err, backoff)
			if !sleepCtx(ctx, backoff) {
				return nil
			}
			if backoff *= 2; backoff > interval {
				backoff = interval
			}
			continue
		}
		if !sleepCtx(ctx, interval) {
			return nil
		}
	}
}

// fatalListenErr reports whether err is the caller's fault rather than a blip
// worth retrying. A rejected token or an unknown project will still be
// rejected on the next poll, so retrying forever would just hide it.
func fatalListenErr(err error) bool {
	var ce *cli.ClientError
	if !errors.As(err, &ce) {
		return false
	}
	return ce.Status >= 400 && ce.Status < 500
}

// sleepCtx waits for d, reporting false when the context ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
