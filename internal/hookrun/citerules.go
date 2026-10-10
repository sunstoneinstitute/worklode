package hookrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/gate"
	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// citeRulesContext is the instruction session-start and subagent-start
// inject in every checkout (WL-REQ-1791).
const citeRulesContext = "Cite a rule by its ref (WL-REQ-<n>, WL-RULE-<n>) in code comments, " +
	"commit messages, pull request bodies and docs, never by a spec section number: section " +
	"numbers change when a spec's arrangement does. Read a section's rule ref with " +
	"`lode show <doc>#sec-N`. The commit-msg hook refuses a message that cites a section."

// scissors is the line `git commit -v` puts above the diff it appends;
// git drops everything below it, so the check does too.
const scissors = "------------------------ >8 ------------------------"

// refuseSectionCitations is the commit-msg citation check (WL-REQ-1791): the
// one hook that fails its event (WL-REQ-1722). It runs in every checkout,
// before the trailer stamp, and reports whether it refused the message.
// Anything it cannot read is a warning, never a refusal.
func refuseSectionCitations(ctx context.Context, opts Options, dir string) bool {
	if len(opts.Args) == 0 {
		return false
	}
	root, ok := worktree.Root(dir)
	if !ok {
		return false
	}
	msgFile := opts.Args[0]
	if !filepath.IsAbs(msgFile) {
		msgFile = filepath.Join(root, msgFile)
	}
	raw, err := os.ReadFile(msgFile) //nolint:gosec // path comes from git, via the hook argv
	if err != nil {
		warn(opts, "read commit message %s: %v (citations not checked)", msgFile, err)
		return false
	}
	text := string(raw)
	if i := strings.Index(text, scissors); i >= 0 {
		text = text[:i]
	}
	cmd := gitexec.Cmd(root, "stripspace", "--strip-comments")
	cmd.Stdin = strings.NewReader(text)
	out, err := cmd.Output()
	if err != nil {
		warn(opts, "git stripspace: %v (citations not checked)", err)
		return false
	}
	cites := gate.Citations(string(out))
	if len(cites) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, backboneTimeout)
	defer cancel()
	resolve := func(designdoc.SectionRef) string { return "" }
	if c, err := opts.client(); err == nil {
		resolve = func(s designdoc.SectionRef) string { return c.SectionRule(ctx, s) }
	}
	warn(opts, "commit refused: cite rules by ref, never by spec section (WL-REQ-1791):\n%s",
		strings.TrimRight(gate.Describe(cites, resolve), "\n"))
	return true
}
