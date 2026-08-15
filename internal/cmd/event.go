package cmd

import (
	"fmt"
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
	cmd := &cobra.Command{
		Use:   "tail",
		Short: "List recent events, newest last",
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
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.EventTable(cmd.OutOrStdout(), resp.Events)
			return nil
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "filter to this event type")
	cmd.Flags().DurationVar(&since, "since", 0, "only events received within this duration (e.g. 2h)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max events returned (default/cap 200)")
	return cmd
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
