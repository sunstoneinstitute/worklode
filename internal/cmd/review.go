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

// `lode review`: the verbs of the WL-REQ-1775 catalogue. The change arm of
// add, comment, submit and show is in reviewchange.go.

func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Reviews of documents and of task changes: anchored threads, verdicts, author rounds",
	}
	cmd.AddCommand(newReviewAddCmd(), newReviewListCmd(), newReviewShowCmd(), newReviewCommentCmd(),
		newReviewResolveCmd(), newReviewGuideCmd(), newReviewApproveCmd(), newReviewSetCmd(), newReviewSubmitCmd(), newReviewWaitCmd())
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
	var baseRef, reason string
	var allowReady bool
	cmd := &cobra.Command{
		Use:               "add <doc-ref|task-id> [--base-ref <sha>] [--reason <text>] [--allow-ready]",
		ValidArgsFunction: docRefAt(0),
		Short:             "Open a review of a document or of a task's change",
		Long: `Open a review of a document (reviewers from 'lode doc set reviewers') or of a
task's change (reviewers from 'lode task set reviewers').

For a task, --base-ref is the branch head sha, default HEAD of the current
worktree. The lease holder must give --reason, one line saying why. A ready
task is refused unless --allow-ready, which moves it to in_review.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if taskID.MatchString(args[0]) {
				return openChangeReview(cmd, args[0], baseRef, reason, allowReady)
			}
			if baseRef != "" || reason != "" || allowReady {
				return errors.New("--base-ref, --reason and --allow-ready apply to a task's change review only")
			}
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
	cmd.Flags().StringVar(&baseRef, "base-ref", "", "task: the branch head sha to review (default HEAD of the worktree)")
	cmd.Flags().StringVar(&reason, "reason", "", "task: why this change needs a reviewer; required of the lease holder")
	cmd.Flags().BoolVar(&allowReady, "allow-ready", false, "task: open over a ready task, moving it to in_review")
	return cmd
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
			if r.Task != "" && !jsonOut(cmd) {
				printReviewDiff(cmd, r)
			}
			return nil
		},
	}
}

func newReviewCommentCmd() *cobra.Command {
	var quote, intent, body, thread, anchor, anchorText string
	cmd := &cobra.Command{
		Use:   "comment <review-id> (--quote <text> | --anchor <a> --intent <intent> | --thread <thread-id>) --body <text>",
		Short: "Open a thread on quoted text of the subject, or reply to a thread",
		Long: `Open a thread anchored to text of the review's subject, or reply to one.

  --quote must occur exactly once in the document's current text (for a spec,
  its rendered body); the thread anchors to that byte range (WL-REQ-1317).
  --intent is question, change_request or note.
  --thread replies to an existing thread instead.

On a task's change review, --anchor <repo>:<path>:<additions|deletions>:<line>
opens a thread on that line. The line's text is read from the working tree, or
from the base_ref commit; --anchor-text supplies it when there is no checkout.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "review")
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" {
				return errors.New("a comment needs --body")
			}
			if n := count(quote != "", thread != "", anchor != ""); n != 1 {
				return errors.New("pass --quote or --anchor to open a thread or --thread to reply, one of the three")
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
			if r.Task != "" {
				if anchor == "" {
					return fmt.Errorf("review %d is of a change; anchor a thread with --anchor <repo>:<path>:<additions|deletions>:<line>", id)
				}
				return commentOnChange(cmd, c, r, anchor, anchorText, intent, body)
			}
			if anchor != "" {
				return errors.New("--anchor is for a task's change review; quote the document text with --quote")
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
	cmd.Flags().StringVar(&anchor, "anchor", "", "change review: <repo>:<path>:<additions|deletions>:<line>")
	cmd.Flags().StringVar(&anchorText, "anchor-text", "", "change review: the line's text, when there is no checkout")
	cmd.MarkFlagsMutuallyExclusive("quote", "thread", "anchor")
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
func submitVerdict(cmd *cobra.Command, idArg, verdict string, focused bool) error {
	id, err := parseID(idArg, "review")
	if err != nil {
		return err
	}
	c, err := newAPIClient()
	if err != nil {
		return err
	}
	r, raw, err := c.SubmitReviewRound(cmd.Context(), id, model.ReviewRoundInput{Verdict: verdict, Client: "cli", Focused: focused})
	if err != nil {
		return err
	}
	printReview(cmd, r, raw)
	return nil
}

func newReviewApproveCmd() *cobra.Command {
	var focused bool
	cmd := &cobra.Command{
		Use:   "approve <review-id> [--focused]",
		Short: "Submit a reviewer round with verdict approved",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return submitVerdict(cmd, args[0], "approved", focused)
		},
	}
	cmd.Flags().BoolVar(&focused, "focused", false, "the round was read with the guide's skims applied")
	return cmd
}

func newReviewGuideCmd() *cobra.Command {
	var focused bool
	cmd := &cobra.Command{
		Use:   "guide <review-id> [--focused]",
		Short: "Show the review's guide; --focused applies its skims",
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
			g, raw, err := c.GetReviewGuide(cmd.Context(), id, focused)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ReviewGuideRender(cmd.OutOrStdout(), g)
			return nil
		},
	}
	cmd.Flags().BoolVar(&focused, "focused", false, "apply the guide's skims")
	return cmd
}

func newReviewSetCmd() *cobra.Command {
	var focused bool
	var file, overview string
	cmd := &cobra.Command{
		Use:   "set <field> [<value>] <review-id>",
		Short: "Set one field of a review, e.g. `lode review set verdict changes_requested 12`",
		Long: `Set one named field of a review. The review id is the last argument.

  verdict  changes_requested: submit a reviewer round with that verdict
             lode review set verdict changes_requested 12 [--focused]
  guide    author: propose the guide for the current round, with the diff
           attached; --file is a JSON array of guide files, --overview the text
             lode review set guide 12 --file guide.json --overview "…"`,
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case args[0] == "verdict" && len(args) == 3:
				return submitVerdict(cmd, args[2], args[1], focused)
			case args[0] == "guide" && len(args) == 2:
				return proposeGuide(cmd, args[1], file, overview)
			}
			return fmt.Errorf("unknown field %q: the settable fields are verdict <value> <review-id> and guide <review-id>", args[0])
		},
	}
	cmd.Flags().BoolVar(&focused, "focused", false, "verdict: the round was read with the guide's skims applied")
	cmd.Flags().StringVar(&file, "file", "", "guide: a JSON file holding an array of guide files")
	cmd.Flags().StringVar(&overview, "overview", "", "guide: the overview text")
	return cmd
}

func newReviewSubmitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "submit <review-id>",
		Short: "Author: submit a new round at the document's current version, or the change's HEAD with threads re-anchored",
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
			if r.Task != "" {
				return submitChange(cmd, c, r)
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

// count returns how many of bs are true.
func count(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}
