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
)

func newEventCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "event",
		Short: "The ordered event log: tail, subscriber status, admin seek",
	}
	cmd.AddCommand(newEventTailCmd(), newEventSubscribersCmd(), newEventSeekCmd())
	return cmd
}

func init() { rootCmd.AddCommand(newEventCmd()) }

func newEventTailCmd() *cobra.Command {
	var typ string
	var since time.Duration
	var limit int
	var follow bool
	cmd := &cobra.Command{
		Use:   "tail",
		Short: "List recent events, newest last (--follow to keep watching)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			f := cli.EventListFilter{Type: typ, Limit: limit}
			if since > 0 {
				f.Since = time.Now().Add(-since)
			}
			resp, raw, err := c.ListEvents(cmd.Context(), f)
			if err != nil {
				return err
			}
			if !follow {
				if jsonOut(cmd) {
					printRaw(cmd, raw)
					return nil
				}
				cli.EventTable(cmd.OutOrStdout(), resp.Events)
				return nil
			}
			return followEvents(cmd, c, typ, resp.Events)
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "filter to this event type")
	cmd.Flags().DurationVar(&since, "since", 0, "only events received within this duration (e.g. 2h)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max events returned (default/cap 200)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false,
		"after printing the backlog, keep the connection open and print events as they arrive (admin only)")
	return cmd
}

// followEvents is `tail -f`: print the bounded backlog the one-shot call
// already fetched, then stream from the last id printed. Handing the stream
// its own cursor is what closes the gap — the server's own default would
// start at the head it sees now, which is not necessarily where this page
// ended, and events landing in between would be lost.
func followEvents(cmd *cobra.Command, c *cli.Client, typ string, backlog []cli.Event) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	out := cmd.OutOrStdout()
	asJSON := jsonOut(cmd)
	enc := json.NewEncoder(out)

	// NDJSON under --json: a stream has no closing bracket, so one object per
	// line is the only form a reader can consume incrementally.
	print := func(e cli.Event) error { cli.EventStreamRow(out, e); return nil }
	if asJSON {
		print = func(e cli.Event) error { return enc.Encode(e) }
	} else {
		cli.EventStreamHeader(out)
	}

	var after int64
	for _, e := range backlog {
		if err := print(e); err != nil {
			return err
		}
		after = e.ID
	}

	err := c.StreamEvents(ctx, cli.EventStreamFilter{Type: typ, After: after}, print)
	// Ctrl-C is how a follow ends, so it is a success.
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func newEventSubscribersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subscribers",
		Short: "Show subscriber offsets, lag, and lock holder",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.EventSubscribers(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.EventSubscriberTable(cmd.OutOrStdout(), resp.Subscribers)
			return nil
		},
	}
}

func newEventSeekCmd() *cobra.Command {
	var to int64
	cmd := &cobra.Command{
		Use:   "seek <name>",
		Short: "Move a subscriber's offsets (admin; a replay relies on handler idempotency)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			status, raw, err := c.SeekEventSubscriber(cmd.Context(), args[0], to)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.EventSubscriberTable(cmd.OutOrStdout(), []cli.EventSubscriberStatus{status})
			fmt.Fprintln(cmd.ErrOrStderr(),
				"warning: seeking backward replays events between the new offset and the old one — safe only because subscriber handlers are idempotent")
			return nil
		},
	}
	cmd.Flags().Int64Var(&to, "to", 0, "offset to seek to")
	cmd.MarkFlagRequired("to")
	return cmd
}
