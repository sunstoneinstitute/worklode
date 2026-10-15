package gate

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/gitexec"
)

// Run is the CI check of WL-REQ-8 over a repo: the repo root is found from
// dir, the config read, the diff and the messages taken from git. The
// returned text is the verdict line; a non-nil error is the refusal.
// resolve names the rule a cited section resolves to and declare prints a
// passing declaration; either may be nil, as in CI, which runs offline.
func Run(dir, base, head, bodyFile string, resolve func(designdoc.SectionRef) string, declare func(Declaration) string) (string, error) {
	root, ok := gitexec.Line(dir, "rev-parse", "--show-toplevel")
	if !ok {
		return "", errors.New("gate: not inside a git repository")
	}
	cfg, enabled, err := Load(root)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	if !enabled {
		return "gate: no [gate] table in .worklode/config.toml, nothing to check\n", nil
	}
	if head == "" {
		head = "HEAD"
	}
	// --no-renames: with rename detection on, moving a file out of a guarded
	// path prints only the destination and the gate never sees the departure.
	diff, err := gitexec.Text(root, "diff", "--name-only", "--no-renames", base+"..."+head)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	var in Input
	for _, p := range strings.Split(diff, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			in.Changed = append(in.Changed, p)
		}
	}
	var cites []Citation
	if bodyFile != "" {
		body, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", fmt.Errorf("gate: %w", err)
		}
		in.Texts = append(in.Texts, string(body))
		for _, c := range Citations(string(body)) {
			c.Where = "pull request body"
			cites = append(cites, c)
		}
	}
	added, err := gitexec.Text(root, "diff", "--no-renames", "--no-color", "--no-ext-diff", base+"..."+head)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	postImage := func(file string) (string, bool) {
		b, err := gitexec.Bytes(root, "show", head+":"+file)
		return string(b), err == nil
	}
	inDiff, err := cfg.AddedCitations(added, postImage)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	cites = append(cites, inDiff...)
	// One NUL after each message keeps a multi-paragraph body whole; newest
	// first so the final commit's trailer wins when several carry one.
	log, err := gitexec.Text(root, "log", "--format=%B%x00", base+".."+head)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	for _, m := range strings.Split(log, "\x00") {
		if m = strings.TrimSpace(m); m != "" {
			in.Texts = append(in.Texts, m)
		}
	}
	in.ResolveSection = resolve
	v, err := Check(cfg, in)
	if len(cites) > 0 {
		// WL-REQ-1791: reported alongside a trailer refusal, not instead of it.
		err = errors.Join(err, fmt.Errorf("cite rules by ref, never by spec section (WL-REQ-1791):\n%s",
			strings.TrimRight(Describe(cites, resolve), "\n")))
	}
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	if len(v.Guarded) == 0 {
		return "gate: no guarded path changed\n", nil
	}
	if declare == nil {
		declare = Declaration.Spelled
	}
	return fmt.Sprintf("gate: %s covers %s\n", cfg.Trailer+" "+declare(*v.Declaration), strings.Join(v.Guarded, ", ")), nil
}

// Spelled prints the declaration with a cited rule as the trailer spells it,
// the qualifier following, for a caller that cannot ask the server its kind.
func (d Declaration) Spelled() string {
	if d.Rule == nil {
		return d.String()
	}
	ref := strings.Fields(d.Line)[1] // the word after the trailer key
	if d.Qualifier != "" {
		ref += " " + d.Qualifier
	}
	return ref
}
