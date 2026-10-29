package cmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// `lode review`: the document verbs of the WL-REQ-1775 catalogue. A review of
// a task's change comes with WL-PLAN-133.

func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Reviews of documents: threads anchored to quoted text, verdicts, author rounds",
	}
	cmd.AddCommand(newReviewAddCmd(), newReviewListCmd(), newReviewShowCmd(), newReviewCommentCmd(),
		newReviewResolveCmd(), newReviewApproveCmd(), newReviewSetCmd(), newReviewSubmitCmd(), newReviewWaitCmd())
	return cmd
}

func init() {
	rootCmd.AddCommand(newReviewCmd())
}

// parseID reads a positive integer argument named what.
func parseID(s, what string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%s %q is not a positive integer", what, s)
	}
	return id, nil
}

// printReview prints a review as --json or in full.
func printReview(cmd *cobra.Command, r model.Review, raw []byte) {
	if jsonOut(cmd) {
		printRaw(cmd, raw)
		return
	}
	cli.ReviewRender(cmd.OutOrStdout(), r)
}

func newReviewAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "add <ref>",
		ValidArgsFunction: docRefAt(0),
		Short:             "Open a review of a document; its reviewers come from `lode doc set reviewers`",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			id, err := resolveDocID(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			r, raw, err := c.OpenReview(cmd.Context(), model.OpenReviewInput{Doc: id})
			if err != nil {
				return err
			}
			printReview(cmd, r, raw)
			return nil
		},
	}
}

func newReviewListCmd() *cobra.Command {
	var mine, open bool
	cmd := &cobra.Command{
		Use:   "list [--mine] [--open]",
		Short: "List reviews",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			p := model.ReviewListParams{Open: open}
			if mine {
				me, _, err := c.WhoAmI(cmd.Context())
				if err != nil {
					return err
				}
				p.Reviewer = me.ID
			}
			rows, raw, err := c.ListReviews(cmd.Context(), p)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ReviewTable(cmd.OutOrStdout(), rows)
			return nil
		},
	}
	cmd.Flags().BoolVar(&mine, "mine", false, "only reviews you are a reviewer on")
	cmd.Flags().BoolVar(&open, "open", false, "only reviews not yet closed")
	return cmd
}

func newReviewShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <review-id>",
		Short: "Show a review: reviewers and their verdicts, threads, rounds",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "review")
			if err != nil {
				return err
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			r, raw, err := c.GetReview(cmd.Context(), id)
			if err != nil {
				return err
			}
			printReview(cmd, r, raw)
			return nil
		},
	}
}

func newReviewCommentCmd() *cobra.Command {
	var quote, intent, body, thread string
	cmd := &cobra.Command{
		Use:   "comment <review-id> (--quote <text> --intent <intent> | --thread <thread-id>) --body <text>",
		Short: "Open a thread on quoted text of the subject, or reply to a thread",
		Long: `Open a thread anchored to text of the review's subject, or reply to one.

  --quote must occur exactly once in the document's current text (for a spec,
  its rendered body); the thread anchors to that byte range (WL-REQ-1317).
  --intent is question, change_request or note.
  --thread replies to an existing thread instead.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "review")
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" {
				return errors.New("a comment needs --body")
			}
			if (quote == "") == (thread == "") {
				return errors.New("pass --quote to open a thread or --thread to reply, one of the two")
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if thread != "" {
				tid, err := parseID(thread, "thread")
				if err != nil {
					return err
				}
				m, raw, err := c.ReplyReviewThread(ctx, id, tid, body)
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "replied on thread %d as %s (seq %d)\n", tid, m.Role, m.Seq)
				return nil
			}
			if intent == "" {
				return errors.New("a new thread needs --intent question|change_request|note")
			}
			r, _, err := c.GetReview(ctx, id)
			if err != nil {
				return err
			}
			if r.Doc == 0 {
				return fmt.Errorf("review %d is not of a document; change reviews come with WL-PLAN-133", id)
			}
			d, _, err := c.GetDoc(ctx, r.Doc)
			if err != nil {
				return err
			}
			start, err := cli.FindQuote(string(d.Body), quote, fmt.Sprintf("doc %d v%d", r.Doc, d.Version))
			if err != nil {
				return err
			}
			th, raw, err := c.OpenReviewThread(ctx, id, model.OpenThreadInput{
				Anchor: model.ReviewAnchor{Doc: r.Doc, Version: d.Version, ByteStart: start,
					ByteEnd: start + len(quote), Quote: quote},
				Intent: intent, Body: body,
				RaisedByTask: currentTaskID(), RaisedBySession: currentSessionID(),
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ReviewThreadRender(cmd.OutOrStdout(), th)
			return nil
		},
	}
	cmd.Flags().StringVar(&quote, "quote", "", "the exact text the thread is about")
	cmd.Flags().StringVar(&intent, "intent", "", "question, change_request or note")
	cmd.Flags().StringVar(&body, "body", "", "the comment")
	cmd.Flags().StringVar(&thread, "thread", "", "reply to this thread")
	cmd.MarkFlagsMutuallyExclusive("quote", "thread")
	cmd.MarkFlagsMutuallyExclusive("intent", "thread")
	return cmd
}

func newReviewResolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve <thread-id>",
		Short: "Resolve a thread",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tid, err := parseID(args[0], "thread")
			if err != nil {
				return err
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			th, raw, err := c.ResolveThread(cmd.Context(), tid, "")
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ReviewThreadRender(cmd.OutOrStdout(), th)
			return nil
		},
	}
}

// submitVerdict posts a reviewer round with verdict and no threads
// (WL-REQ-1323 has no verdict route; the verdict is a field of the round).
func submitVerdict(cmd *cobra.Command, idArg, verdict string) error {
	id, err := parseID(idArg, "review")
	if err != nil {
		return err
	}
	c, err := newAPIClient()
	if err != nil {
		return err
	}
	r, raw, err := c.SubmitReviewRound(cmd.Context(), id, model.ReviewRoundInput{Verdict: verdict, Client: "cli"})
	if err != nil {
		return err
	}
	printReview(cmd, r, raw)
	return nil
}

func newReviewApproveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "approve <review-id>",
		Short: "Submit a reviewer round with verdict approved",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return submitVerdict(cmd, args[0], "approved")
		},
	}
}

func newReviewSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <field> <value> <review-id>",
		Short: "Set one field of a review, e.g. `lode review set verdict changes_requested 12`",
		Long: `Set one named field of a review. The review id is the last argument.

  verdict  changes_requested: submit a reviewer round with that verdict
             lode review set verdict changes_requested 12`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "verdict" {
				return fmt.Errorf("unknown field %q: the settable field is verdict", args[0])
			}
			return submitVerdict(cmd, args[2], args[1])
		},
	}
}

func newReviewSubmitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "submit <review-id>",
		Short: "Author: submit a new round at the document's current version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "review")
			if err != nil {
				return err
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			r, _, err := c.GetReview(cmd.Context(), id)
			if err != nil {
				return err
			}
			if r.Doc == 0 {
				return fmt.Errorf("review %d is not of a document; change reviews come with WL-PLAN-133", id)
			}
			d, _, err := c.GetDoc(cmd.Context(), r.Doc)
			if err != nil {
				return err
			}
			r, raw, err := c.SubmitReviewRound(cmd.Context(), id, model.ReviewRoundInput{
				BaseRef: strconv.Itoa(d.Version), Client: "cli",
			})
			if err != nil {
				return err
			}
			printReview(cmd, r, raw)
			return nil
		},
	}
}

func newReviewWaitCmd() *cobra.Command {
	var after int64
	var timeout int
	cmd := &cobra.Command{
		Use:   "wait <review-id> [--after <seq>] [--timeout <s>]",
		Short: "Author: wait for messages after a seq and resolved threads, printed as one JSON envelope",
		Long: `Long-poll a review. Prints one JSON envelope
{"review": id, "after": seq, "threads": [...], "messages": [...]} and exits 0
once there is a message after --after or a thread was resolved. Pass the
envelope's after as --after next time. When --timeout passes with nothing new
it prints nothing and exits 0. A review that is closed or missing exits 1.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "review")
			if err != nil {
				return err
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			r, _, err := c.GetReview(cmd.Context(), id)
			if err != nil {
				return err
			}
			if r.State == "closed" {
				return fmt.Errorf("review %d is closed", id)
			}
			ev, err := c.ReviewEvents(cmd.Context(), id, model.ReviewEventsParams{After: after, Timeout: timeout})
			if err != nil || ev == nil {
				return err
			}
			return printJSON(cmd, map[string]any{
				"review": id, "after": ev.After, "threads": ev.Threads, "messages": ev.Messages,
			})
		},
	}
	cmd.Flags().Int64Var(&after, "after", 0, "the last message seq already seen")
	cmd.Flags().IntVar(&timeout, "timeout", 30, "seconds to wait, at most 60")
	return cmd
}
