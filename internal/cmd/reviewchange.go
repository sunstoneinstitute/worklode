package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/review"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// The change arm of `lode review` (WL-REQ-1775, WL-REQ-1326): reviews of a
// task's change, whose threads anchor to lines of the branch. The server never
// sees the repository, so the git reads happen here.

// worktreeRoot is the git worktree root of the working directory.
func worktreeRoot() (string, error) {
	dir, err := workingDir()
	if err != nil {
		return "", err
	}
	root, ok := worktree.Root(dir)
	if !ok {
		return "", errors.New("not inside a git worktree")
	}
	return root, nil
}

// openChangeReview opens a review over a task's change.
func openChangeReview(cmd *cobra.Command, taskID, baseRef, reason string, allowReady bool) error {
	ctx := cmd.Context()
	c, err := newAPIClient()
	if err != nil {
		return err
	}
	if baseRef == "" {
		root, err := worktreeRoot()
		if err != nil {
			return fmt.Errorf("%w: pass --base-ref <sha> to review a branch head from here", err)
		}
		if baseRef, err = gitexec.Text(root, "rev-parse", "HEAD"); err != nil {
			return err
		}
	}
	t, _, err := c.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if reason == "" && t.Lease != nil {
		me, _, err := c.WhoAmI(ctx)
		if err != nil {
			return err
		}
		if t.Lease.ActorID == me.ID {
			return errors.New("the lease holder opens a review with --reason \"<why>\": one line saying why this change needs a reviewer")
		}
	}
	r, raw, err := c.OpenReview(ctx, model.OpenReviewInput{Task: taskID, BaseRef: baseRef, Reason: reason, AllowReady: allowReady})
	if err != nil {
		return err
	}
	if len(t.Reviewers) == 0 && !jsonOut(cmd) {
		fmt.Fprintf(cmd.ErrOrStderr(), "no reviewers were set; defaulted to %s\n", strings.Join(r.Reviewers, ", "))
	}
	printReview(cmd, r, raw)
	return nil
}

// parseChangeAnchor splits <repo>:<path>:<additions|deletions>:<line>. The
// path may itself hold colons: repo is the first field, side and line the
// last two.
func parseChangeAnchor(s string) (model.ReviewAnchor, error) {
	f := strings.Split(s, ":")
	if len(f) < 4 {
		return model.ReviewAnchor{}, fmt.Errorf("--anchor %q is not <repo>:<path>:<additions|deletions>:<line>", s)
	}
	line, err := strconv.Atoi(f[len(f)-1])
	side := f[len(f)-2]
	if err != nil || line < 1 || (side != "additions" && side != "deletions") {
		return model.ReviewAnchor{}, fmt.Errorf("--anchor %q is not <repo>:<path>:<additions|deletions>:<line>", s)
	}
	return model.ReviewAnchor{Repo: f[0], Path: strings.Join(f[1:len(f)-2], ":"), Side: side, Line: line}, nil
}

// fileLines splits file text into lines, dropping the final newline's empty tail.
func fileLines(text string) []string {
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// changeAnchor fills the anchor_text and blob_sha of a change anchor: text from
// the working tree at that line, else from `git show <base_ref>:<path>`;
// anchorText overrides for a reviewer with no checkout (WL-REQ-1317).
func changeAnchor(a model.ReviewAnchor, baseRef, anchorText string) (model.ReviewAnchor, error) {
	root, rootErr := worktreeRoot()
	if rootErr == nil {
		a.BlobSHA, _ = gitexec.Text(root, "rev-parse", baseRef+":"+a.Path)
	}
	a.AnchorText = anchorText
	if a.AnchorText == "" {
		if rootErr != nil {
			return a, fmt.Errorf("%w: pass --anchor-text with the line's text", rootErr)
		}
		text := ""
		if b, err := os.ReadFile(filepath.Join(root, a.Path)); err == nil {
			text = string(b)
		} else if text, err = gitexec.Text(root, "show", baseRef+":"+a.Path); err != nil {
			return a, fmt.Errorf("read %s: neither in the working tree nor at %s: %w", a.Path, baseRef, err)
		}
		lines := fileLines(text)
		if a.Line > len(lines) {
			return a, fmt.Errorf("%s has %d lines, not %d", a.Path, len(lines), a.Line)
		}
		a.AnchorText = lines[a.Line-1]
	}
	if a.BlobSHA == "" {
		return a, fmt.Errorf("cannot read the blob of %s at %s here: run from a checkout that has that commit", a.Path, baseRef)
	}
	return a, nil
}

// commentOnChange opens a thread on a line of a change review.
func commentOnChange(cmd *cobra.Command, c *cli.Client, r model.Review, anchor, anchorText, intent, body string) error {
	a, err := parseChangeAnchor(anchor)
	if err != nil {
		return err
	}
	if a, err = changeAnchor(a, r.BaseRef, anchorText); err != nil {
		return err
	}
	th, raw, err := c.OpenReviewThread(cmd.Context(), r.ID, model.OpenThreadInput{
		Anchor: a, Intent: intent, Body: body, RaisedByTask: currentTaskID(), RaisedBySession: currentSessionID(),
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
}

// submitChange is the author round of a change review: HEAD is the new base,
// and every open thread on an additions line is re-anchored by
// review.ReanchorLine against HEAD, or marked obsolete when it drifted.
// ponytail: deletions threads are left as they were; they anchor into the
// old file, which this round does not change.
func submitChange(cmd *cobra.Command, c *cli.Client, r model.Review) error {
	root, err := worktreeRoot()
	if err != nil {
		return fmt.Errorf("%w: submit a change review from the task's worktree", err)
	}
	head, err := gitexec.Text(root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	var reanchors []model.ThreadReanchor
	var report []func(w io.Writer)
	for _, t := range r.Threads {
		a := t.Anchor
		if t.Status != "open" || a.Side != "additions" {
			continue
		}
		oldText, err := gitexec.Text(root, "show", a.BlobSHA)
		if err != nil {
			return fmt.Errorf("thread %d: read blob %s: %w", t.ID, a.BlobSHA, err)
		}
		newText, newErr := gitexec.Text(root, "show", "HEAD:"+a.Path)
		line, stage, ok := 0, "drifted", false
		if newErr == nil {
			line, stage, ok = review.ReanchorLine(fileLines(oldText), fileLines(newText), a.Line, a.AnchorText)
		}
		ra := model.ThreadReanchor{Thread: t.ID}
		if ok {
			blob, err := gitexec.Text(root, "rev-parse", "HEAD:"+a.Path)
			if err != nil {
				return err
			}
			ra.Anchor = model.ReviewAnchor{Line: line, BlobSHA: blob}
		} else {
			ra.Status = "obsolete"
		}
		reanchors = append(reanchors, ra)
		id, path, from := t.ID, a.Path, a.Line
		report = append(report, func(w io.Writer) { cli.ReanchorRender(w, id, path, from, line, stage, ok) })
	}
	r, raw, err := c.SubmitReviewRound(cmd.Context(), r.ID, model.ReviewRoundInput{BaseRef: head, Client: "cli", Reanchors: reanchors})
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if jsonOut(cmd) {
		w = cmd.ErrOrStderr()
	}
	for _, f := range report {
		f(w)
	}
	printReview(cmd, r, raw)
	return nil
}

// reviewDiff is the raw `git diff <merge-base>..<base_ref>` of a change
// review, read in the working directory's checkout (WL-REQ-1326). The merge
// base is against the default branch.
func reviewDiff(r model.Review) (string, error) {
	root, err := worktreeRoot()
	if err != nil {
		return "", err
	}
	trunk := ""
	if b, err := worktree.DefaultBranch(root); err == nil {
		trunk = "origin/" + b
	}
	for _, cand := range []string{trunk, "main", "master"} {
		if cand == "" {
			continue
		}
		base, err := gitexec.Text(root, "merge-base", cand, r.BaseRef)
		if err != nil {
			continue
		}
		out, err := gitexec.Bytes(root, "diff", base+".."+r.BaseRef)
		return string(out), err
	}
	return "", fmt.Errorf("no merge base of %s with the default branch here", r.BaseRef)
}

// printReviewDiff prints the diff after the threads, or says where it is.
func printReviewDiff(cmd *cobra.Command, r model.Review) {
	w := cmd.OutOrStdout()
	diff, err := reviewDiff(r)
	if err != nil {
		fmt.Fprintf(w, "\ndiff: not available here (%v). In the worktree of %s run: git diff $(git merge-base origin/HEAD %s)..%s\n",
			err, r.Task, r.BaseRef, r.BaseRef)
		return
	}
	fmt.Fprintf(w, "\ndiff:\n%s", diff)
}

// proposeGuide is `lode review set guide`: the author's proposal for the
// current round, with the change's diff attached so the server can check every
// skim against what the author edited (WL-RULE-1320). A document review has no
// diff.
func proposeGuide(cmd *cobra.Command, idArg, file, overview string) error {
	id, err := parseID(idArg, "review")
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
	p := model.GuideProposal{Overview: overview, Files: []model.GuideFile{}}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &p.Files); err != nil {
			return fmt.Errorf("%s: want a JSON array of guide files: %w", file, err)
		}
	}
	if r.Task != "" {
		if p.Diff, err = reviewDiff(r); err != nil {
			return fmt.Errorf("the proposal carries the change's diff: %w", err)
		}
	}
	res, raw, err := c.PutReviewGuide(cmd.Context(), id, p)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		printRaw(cmd, raw)
		return nil
	}
	cli.ReviewGuideRender(cmd.OutOrStdout(), res.Guide)
	cli.GuideRefusalsRender(cmd.OutOrStdout(), res.Refused)
	return nil
}

// materializeGuide writes the review's guide JSON to a temp file outside the
// working tree for crit to be pointed at (WL-REQ-1776) and returns its path.
// Without focused the skims are emptied (WL-RULE-1320), the same split as
// GET .../guide?focused=.
func materializeGuide(r model.Review, focused bool) (string, error) {
	if r.Guide == nil {
		return "", fmt.Errorf("review %d has no guide", r.ID)
	}
	g := *r.Guide
	g.Files = append([]model.GuideFile(nil), g.Files...)
	if !focused {
		for i := range g.Files {
			g.Files[i].Skims = nil
		}
	}
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("lode-review-%d-round-%d.json", r.ID, r.Round))
	return path, os.WriteFile(path, b, 0o644)
}
