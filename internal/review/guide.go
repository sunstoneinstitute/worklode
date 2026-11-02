package review

import (
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Refusal names a file whose proposed skim was refused and the rule that fired.
type Refusal struct{ Path, Rule string }

var (
	lockfiles = map[string]bool{"go.sum": true, "package-lock.json": true, "pnpm-lock.yaml": true,
		"yarn.lock": true, "Cargo.lock": true, "poetry.lock": true, "uv.lock": true, "Gemfile.lock": true}
	genHeader  = regexp.MustCompile(`Code generated .* DO NOT EDIT`)
	importLine = regexp.MustCompile(`^(import|(\w+|\.|_)?\s*"[^"]*")$`)
	spaces     = regexp.MustCompile(`\s+`)
	hunkNew    = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)
)

func hasSegment(p string, segs ...string) bool {
	for _, s := range strings.Split(path.Dir(p), "/") {
		for _, want := range segs {
			if s == want {
				return true
			}
		}
	}
	return false
}

// changed returns the added and removed lines (without markers) of the hunks.
func changed(hunks []string) (added, removed []string) {
	for _, h := range hunks {
		for _, l := range strings.Split(h, "\n") {
			switch {
			case strings.HasPrefix(l, "+"):
				added = append(added, l[1:])
			case strings.HasPrefix(l, "-"):
				removed = append(removed, l[1:])
			}
		}
	}
	return
}

// Mechanical classifies one file's change by path and hunks against the
// mechanical set of WL-RULE-1321. rule names the predicate that matched, or
// "non-mechanical".
func Mechanical(p string, hunks []string) (ok bool, rule string) {
	switch {
	case lockfiles[path.Base(p)]:
		return true, "lockfile"
	case strings.HasSuffix(p, "_templ.go"), strings.HasSuffix(p, ".pb.go"), hasSegment(p, "gen", "generated"),
		generatedHeader(hunks):
		return true, "generated"
	case hasSegment(p, "vendor", "third_party", "node_modules"):
		return true, "vendored"
	case hasSegment(p, "__snapshots__", "testdata") && !strings.HasSuffix(p, ".go"):
		return true, "snapshot"
	}
	added, removed := changed(hunks)
	if len(added)+len(removed) == 0 {
		return false, "non-mechanical"
	}
	imports := true
	for _, l := range append(append([]string{}, added...), removed...) {
		if l = strings.TrimSpace(l); l != "" && l != "(" && l != ")" && !importLine.MatchString(l) {
			imports = false
			break
		}
	}
	if imports {
		return true, "import-only"
	}
	norm := func(ls []string) string { return spaces.ReplaceAllString(strings.Join(ls, " "), " ") }
	if strings.TrimSpace(norm(added)) == strings.TrimSpace(norm(removed)) {
		return true, "format-only"
	}
	return false, "non-mechanical"
}

// generatedHeader reports a "Code generated ... DO NOT EDIT" line among the
// first 5 lines the first hunk shows.
func generatedHeader(hunks []string) bool {
	if len(hunks) == 0 {
		return false
	}
	lines := strings.Split(hunks[0], "\n")
	if len(lines) > 0 {
		lines = lines[1:] // @@ header
	}
	if len(lines) > 5 {
		lines = lines[:5]
	}
	for _, l := range lines {
		if len(l) > 0 && genHeader.MatchString(l[1:]) {
			return true
		}
	}
	return false
}

// SplitDiff splits a unified diff into hunks per file path (the new path, or
// the old one for a deletion). Each hunk starts at its "@@" line.
func SplitDiff(diff string) map[string][]string {
	out := map[string][]string{}
	var cur string
	var hunk []string
	var oldPath string
	flush := func() {
		if cur != "" && len(hunk) > 0 {
			out[cur] = append(out[cur], strings.Join(hunk, "\n"))
		}
		hunk = nil
	}
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			flush()
			cur, oldPath = "", ""
		case len(hunk) == 0 && strings.HasPrefix(l, "--- "):
			oldPath = l[4:]
		case len(hunk) == 0 && strings.HasPrefix(l, "+++ "):
			cur = strings.TrimPrefix(l[4:], "b/")
			if cur == "/dev/null" {
				cur = strings.TrimPrefix(oldPath, "a/")
			}
		case strings.HasPrefix(l, "@@"):
			flush()
			hunk = []string{l}
		case len(hunk) > 0:
			hunk = append(hunk, l)
		}
	}
	flush()
	return out
}

// wholeFile spans line 1 to the last new-side line the hunks reach.
func wholeFile(hunks []string) model.GuideSpan {
	to := 1
	for _, h := range hunks {
		if m := hunkNew.FindStringSubmatch(h); m != nil {
			start, _ := strconv.Atoi(m[1])
			n := 1
			if m[2] != "" {
				n, _ = strconv.Atoi(m[2])
			}
			to = max(to, min(start+n, math.MaxInt32))
		}
	}
	return model.GuideSpan{From: 1, To: to, DecidedBy: "policy"}
}

// Compose applies policy to the author's proposal: refuses skims on edited
// files and adds policy skims on mechanical ones. Skims are kept only when
// focused.
func Compose(p model.GuideProposal, focused bool) (model.ReviewGuide, []Refusal) {
	hunks := SplitDiff(p.Diff)
	g := model.ReviewGuide{Overview: p.Overview}
	var refused []Refusal
	for _, f := range p.Files {
		hs := hunks[f.Path]
		ok, rule := Mechanical(f.Path, hs)
		skims := f.Skims
		f.Skims = nil
		switch {
		case ok && len(skims) > 0:
			for _, s := range skims {
				s.DecidedBy = "author"
				f.Skims = append(f.Skims, s)
			}
		case ok:
			f.Skims = []model.GuideSpan{wholeFile(hs)}
		case len(skims) > 0:
			if len(hs) == 0 {
				rule = "no-diff"
			}
			refused = append(refused, Refusal{f.Path, rule})
		}
		if !focused {
			f.Skims = nil
		}
		g.Files = append(g.Files, f)
	}
	// Mechanical files the author did not list still get a policy skim.
	listed := map[string]bool{}
	for _, f := range p.Files {
		listed[f.Path] = true
	}
	paths := make([]string, 0, len(hunks))
	for fp := range hunks {
		paths = append(paths, fp)
	}
	sort.Strings(paths)
	for _, fp := range paths {
		if ok, _ := Mechanical(fp, hunks[fp]); ok && !listed[fp] && focused {
			g.Files = append(g.Files, model.GuideFile{Path: fp, Skims: []model.GuideSpan{wholeFile(hunks[fp])}})
		}
	}
	return g, refused
}
