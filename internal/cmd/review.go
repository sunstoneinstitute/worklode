package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/review/critjson"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// `lode review`: the verbs of the WL-REQ-1775 catalogue. The change arm of
// add, comment, submit and show is in reviewchange.go.

func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Reviews of documents and of task changes: anchored threads, verdicts, author rounds",
	}
	cmd.AddCommand(newReviewAddCmd(), newReviewListCmd(), newReviewShowCmd(), newReviewCommentCmd(),
		newReviewResolveCmd(), newReviewGuideCmd(), newReviewApproveCmd(), newReviewSetCmd(), newReviewSubmitCmd(), newReviewWaitCmd(), newReviewImportCmd(), newReviewExecCmd())
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

// newReviewImportCmd is crit's on_finish_* hook target (WL-REQ-1775,
// WL-REQ-1776): one crit finish on stdin becomes one reviewer round with
// client crit. It reads nothing else from crit.
func newReviewImportCmd() *cobra.Command {
	var reviewArg, anchors string
	cmd := &cobra.Command{
		Use:   "import --review <id> [--anchors <sidecar>] < result.json",
		Short: "Land a crit finish payload as a reviewer round with its open comments as threads",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id, err := parseID(reviewArg, "--review")
			if err != nil {
				return err
			}
			return importCritFinish(cmd, id, anchors)
		},
	}
	cmd.Flags().StringVar(&reviewArg, "review", "", "the review the crit round belongs to (crit's payload names none)")
	cmd.Flags().StringVar(&anchors, "anchors", "", "the sidecar `lode doc show --anchors` wrote, for a document review")
	_ = cmd.MarkFlagRequired("review")
	return cmd
}

// importCritFinish saves the payload as finish.json beside the sidecar (or in
// the temp directory), so a failed import can be re-run from it, then maps it
// with critjson.ToRound and posts the round.
func importCritFinish(cmd *cobra.Command, id int64, anchors string) error {
	payload, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("read the finish payload: %w", err)
	}
	dir := os.TempDir()
	if anchors != "" {
		dir = filepath.Dir(anchors)
	}
	saved := filepath.Join(dir, "finish.json")
	if err := os.WriteFile(saved, payload, 0o600); err != nil {
		return fmt.Errorf("save the finish payload: %w", err)
	}
	var f critjson.Finish
	if err := json.NewDecoder(bytes.NewReader(payload)).Decode(&f); err != nil {
		return fmt.Errorf("decode the finish payload: %w", err)
	}
	ctx := cmd.Context()
	c, err := newAPIClient()
	if err != nil {
		return err
	}
	rev, _, err := c.GetReview(ctx, id)
	if err != nil {
		return err
	}
	opts := critjson.Options{Focused: os.Getenv("LODE_REVIEW_FOCUSED") == "1", Existing: rev.Threads}
	switch {
	case anchors != "":
		b, err := os.ReadFile(anchors)
		if err != nil {
			return err
		}
		var sc designdoc.AnchorSidecar
		if err := json.Unmarshal(b, &sc); err != nil {
			return fmt.Errorf("decode %s: %w", anchors, err)
		}
		d, _, err := c.GetDoc(ctx, sc.Doc)
		if err != nil {
			return err
		}
		if d.Version != sc.Version {
			return fmt.Errorf("doc %d is at version %d, the sidecar maps version %d: re-run lode review exec", sc.Doc, d.Version, sc.Version)
		}
		opts.Anchors, opts.Body = &sc, string(d.Body)
	case rev.Task == "":
		return fmt.Errorf("review %d is of a document: pass --anchors <sidecar>", id)
	default:
		root, err := worktreeRoot()
		if err != nil {
			return err
		}
		common, err := gitexec.Text(root, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return err
		}
		opts.Repo = filepath.Base(filepath.Dir(common))
	}
	in, rep, err := critjson.ToRound(f, opts)
	if err != nil {
		return fmt.Errorf("%w (payload saved at %s)", err, saved)
	}
	for i, th := range in.Threads {
		if th.Anchor.Path == "" {
			continue
		}
		if in.Threads[i].Anchor, err = changeAnchor(th.Anchor, rev.BaseRef, th.Anchor.AnchorText); err != nil {
			return fmt.Errorf("%w (payload saved at %s)", err, saved)
		}
	}
	got, _, err := c.SubmitReviewRound(ctx, id, in)
	if err != nil {
		return fmt.Errorf("%w (fix the comment in crit, or re-run from %s)", err, saved)
	}
	round := 0
	if n := len(got.Rounds); n > 0 {
		round = got.Rounds[n-1].Round
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "review %d round %d (%s): %d threads opened, %d comments matched existing threads, %d resolved skipped\n",
		id, round, in.Verdict, len(in.Threads), len(rep.Matched), rep.Resolved)
	for _, line := range []struct {
		what string
		ids  []string
	}{{"trimmed of heading lines", rep.Trimmed}, {"split at a section", rep.Split}, {"folded into the overall note", rep.Notes}} {
		if len(line.ids) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", line.what, strings.Join(line.ids, "; "))
		}
	}
	return nil
}

// newReviewExecCmd is the interim terminal desk (WL-REQ-1776, WL-REQ-1316):
// crit, unmodified, launched against the review's subject with its finish
// hooks pointing at `lode review import`. Everything it writes lands in one
// temp directory outside the working tree.
func newReviewExecCmd() *cobra.Command {
	var focused bool
	var worktreeDir string
	cmd := &cobra.Command{
		Use:   "exec <review-id> [--focused] [--worktree <dir>]",
		Short: "Review at the crit desk: launch crit with finish hooks that land each finish as a round",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "review")
			if err != nil {
				return err
			}
			return execDesk(cmd, id, focused, worktreeDir)
		},
	}
	cmd.Flags().BoolVar(&focused, "focused", false, "declare that the guide's skims apply; the round lands with focused = true (WL-RULE-1320)")
	cmd.Flags().StringVar(&worktreeDir, "worktree", "", "for a change: the task's worktree, when it is not the working directory")
	return cmd
}

func execDesk(cmd *cobra.Command, id int64, focused bool, worktreeDir string) error {
	ctx := cmd.Context()
	c, cfg, err := newAPIClientWithConfig()
	if err != nil {
		return err
	}
	r, _, err := c.GetReview(ctx, id)
	if err != nil {
		return err
	}
	if r.State == "closed" {
		return fmt.Errorf("review %d is closed", id)
	}
	if r.Doc == 0 && r.Task == "" {
		return fmt.Errorf("review %d is of a %s: the crit desk reviews a document or a change", id, r.Subject)
	}
	me, _, err := c.WhoAmI(ctx)
	if err != nil {
		return err
	}
	critPath, err := exec.LookPath("crit")
	if err != nil {
		return fmt.Errorf("crit is not on PATH: install crit %s (lode doctor checks it)", critjson.PinnedTag())
	}

	// Validate a change subject's worktree before writing anything.
	critDir := ""
	if r.Task != "" {
		if critDir, err = deskWorktree(ctx, c, r.Task, worktreeDir); err != nil {
			return err
		}
	}

	tmp, err := os.MkdirTemp("", fmt.Sprintf("lode-desk-%d-", id))
	if err != nil {
		return err
	}
	// crit compares its symlink-resolved cwd with $HOME to tell project
	// config from global; resolved, the two are one file in document mode
	// and its hooks are not trust-gated.
	if tmp, err = filepath.EvalSymlinks(tmp); err != nil {
		return err
	}
	w := cmd.ErrOrStderr()
	if r.Guide != nil {
		path, err := materializeGuide(r, focused, tmp)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "guide: %s\n", path)
	}

	var critArgs []string
	sidecar := ""
	if r.Doc != 0 {
		d, _, err := c.GetDoc(ctx, r.Doc)
		if err != nil {
			return err
		}
		name := d.Slug
		if name == "" {
			name = fmt.Sprintf("doc-%d", d.ID)
		}
		copyPath := filepath.Join(tmp, name+".md")
		sidecar = filepath.Join(tmp, name+".anchors.json")
		if err := writeAnchored(cmd, d, sidecar, copyPath); err != nil {
			return err
		}
		critDir, critArgs = tmp, []string{copyPath}
	}

	var user []byte
	if home, err := os.UserHomeDir(); err == nil {
		if user, err = os.ReadFile(filepath.Join(home, ".crit.config.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	conf, err := deskConfig(user, id, sidecar, me.ID)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".crit.config.json"), conf, 0o600); err != nil {
		return err
	}

	before := len(r.Rounds)
	crit := exec.CommandContext(ctx, critPath, critArgs...)
	crit.Dir = critDir
	crit.Env = deskEnv(os.Environ(), tmp, cfg, focused)
	crit.Stdin, crit.Stdout, crit.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), w
	runErr := crit.Run()

	// crit keeps a hook's stdout to itself, so the landed rounds are read back.
	reimport := fmt.Sprintf("lode review import --review %d", id)
	if sidecar != "" {
		reimport += " --anchors " + shellQuote(sidecar)
	}
	reimport += " < " + shellQuote(filepath.Join(tmp, "finish.json"))
	after, _, err := c.GetReview(ctx, id)
	switch {
	case err != nil:
		fmt.Fprintf(w, "could not read review %d back: %v\ndesk: %s\n", id, err, tmp)
	case len(after.Rounds) > before:
		fmt.Fprintln(cmd.OutOrStdout()) // crit's own output may end mid-line
		for _, rd := range after.Rounds[before:] {
			fmt.Fprintf(cmd.OutOrStdout(), "review %d round %d (%s) landed from crit\n", id, rd.Round, rd.Verdict)
		}
		fmt.Fprintf(w, "desk: %s\n", tmp)
	default:
		if _, statErr := os.Stat(filepath.Join(tmp, "finish.json")); statErr == nil {
			fmt.Fprintf(w, "crit finished but no round landed; re-run the import:\n  %s\n", reimport)
		} else {
			fmt.Fprintf(w, "crit exited without finishing; nothing landed\n")
		}
		fmt.Fprintf(w, "desk: %s\n", tmp)
	}
	if runErr != nil {
		return fmt.Errorf("crit: %w", runErr)
	}
	return nil
}

// deskWorktree resolves the worktree crit reviews a change in: dir, else the
// working directory, checked out on the task's branch and holding no crit
// project config, which would take the finish hooks over (WL-REQ-1776).
func deskWorktree(ctx context.Context, c *cli.Client, taskID, dir string) (string, error) {
	if dir == "" {
		var err error
		if dir, err = workingDir(); err != nil {
			return "", err
		}
	}
	root, ok := worktree.Root(dir)
	if !ok {
		return "", fmt.Errorf("%s is not inside a git worktree: run from the task's worktree or pass --worktree", dir)
	}
	t, _, err := c.GetTask(ctx, taskID)
	if err != nil {
		return "", err
	}
	if branch, err := gitexec.Text(root, "rev-parse", "--abbrev-ref", "HEAD"); err != nil {
		return "", err
	} else if t.Branch != "" && branch != t.Branch {
		return "", fmt.Errorf("%s is on branch %s, not task %s's branch %s: pass --worktree <dir>", root, branch, taskID, t.Branch)
	}
	for _, p := range []string{".crit.config.json", filepath.Join(".crit", "hooks")} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			return "", fmt.Errorf("%s holds crit project config that would replace the desk's finish hooks: move it aside and re-run", filepath.Join(root, p))
		}
	}
	return root, nil
}

// deskConfig is the desk's crit config: the user's (keys kept as written)
// with author set and hooks replaced by the two finish hooks, both
// `lode review import`. The whole hooks map is replaced, so a user hook keyed
// on a mode (on_finish_unresolved:diff) cannot win over the desk's.
func deskConfig(user []byte, id int64, sidecar, author string) ([]byte, error) {
	conf := map[string]any{}
	if len(bytes.TrimSpace(user)) > 0 {
		if err := json.Unmarshal(user, &conf); err != nil {
			return nil, fmt.Errorf("read ~/.crit.config.json: %w", err)
		}
	}
	hook := fmt.Sprintf("inline:lode review import --review %d", id)
	if sidecar != "" {
		hook += " --anchors " + shellQuote(sidecar)
	}
	conf["author"] = author
	conf["hooks"] = map[string]string{"on_finish_unresolved": hook, "on_finish_approved": hook}
	return json.MarshalIndent(conf, "", "  ")
}

// deskEnv is crit's environment: the caller's, with HOME and TMPDIR set to
// the desk so crit reads the desk's config and import saves finish.json there,
// the lode server and token passed explicitly because the hook's lode cannot
// find the user's config under the desk's HOME, and LODE_REVIEW_FOCUSED set
// only for a focused read.
func deskEnv(environ []string, tmp string, cfg cli.Config, focused bool) []string {
	set := map[string]string{"HOME": tmp, "TMPDIR": tmp, "LODE_SERVER": cfg.ServerURL, "LODE_TOKEN": cfg.Token}
	if focused {
		set["LODE_REVIEW_FOCUSED"] = "1"
	}
	var env []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := set[k]; ok || k == "LODE_REVIEW_FOCUSED" {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range set {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// shellQuote quotes s for sh -c, which crit runs an inline hook through.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
