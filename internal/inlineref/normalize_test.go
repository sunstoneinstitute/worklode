package inlineref

import (
	"strings"
	"testing"
)

var testKeys = map[string]bool{"WL": true}

// resolveFake resolves a hit the way the store would, from a fixed table.
func resolveFake(h Hit) *Target {
	switch {
	case h.Infix == "" && h.Number == 7:
		return &Target{Ref: "WL-7"}
	case h.Infix == "SPEC" && h.Number == 77 && h.Anchor != "":
		if h.Anchor == "sec-4" {
			return &Target{Ref: "WL-REQ-12"}
		}
		return &Target{Ref: "WL-SPEC-77", DocAnchor: true}
	case h.Infix == "ADR":
		return &Target{Ref: "WL-SPEC-77"}
	case h.Infix == "SPEC" && h.Anchor != "":
		return &Target{Ref: "WL-REQ-848"}
	case h.Infix == "SPEC":
		return &Target{Ref: "WL-SPEC-77"}
	case h.Infix == "RULE" || h.Infix == "REQ":
		return &Target{Ref: "WL-REQ-12"}
	}
	return nil
}

func normalize(text string) string {
	hits := Scan(text, testKeys)
	ts := make([]*Target, len(hits))
	for i, h := range hits {
		ts[i] = resolveFake(h)
	}
	return Normalize(text, hits, ts)
}

func TestNormalize(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"bare url", "see https://lode.example/tasks/WL-7 now", "see WL-7 now"},
		{"link with ref text", "see [WL-7](https://lode.example/tasks/WL-7) now", "see WL-7 now"},
		{"link with other text", "see [the task](https://lode.example/tasks/WL-7) now", "see [the task](WL-7) now"},
		{"bare path", "see /tasks/WL-7 now", "see /tasks/WL-7 now"},
		{"link to path", "see [WL-7](/tasks/WL-7) now", "see [WL-7](/tasks/WL-7) now"},
		{"link text with scheme, path target", "see [http://x](/WL-7) now", "see [http://x](/WL-7) now"},
		{"section with rule", "see WL-SPEC-77#sec-4 now", "see WL-REQ-12 now"},
		{"section without rule", "see WL-SPEC-77#sec-9 now", "see WL-SPEC-77#sec-9 now"},
		{"rule fragment is plain", "see WL-SPEC-1#rule-12 now", "see WL-SPEC-1#rule-12 now"},
		{"wrong infix", "see WL-RULE-12 now", "see WL-REQ-12 now"},
		{"in code", "see `/tasks/WL-7` now", "see `/tasks/WL-7` now"},
		{"in code across lines", "see `a\n/tasks/WL-7` now", "see `a\n/tasks/WL-7` now"},
		{"fenced", "```\n/tasks/WL-7\n```", "```\n/tasks/WL-7\n```"},
		{"quoted", `the form "https://lode.example/tasks/WL-7" and https://lode.example/tasks/WL-7`, `the form "https://lode.example/tasks/WL-7" and WL-7`},
		{"range of full refs", "see WL-SPEC-77#sec-4–§5 now", "see WL-SPEC-77#sec-4–§5 now"},
		{"list of full refs", "see WL-SPEC-77#sec-4, §5 now", "see WL-SPEC-77#sec-4, §5 now"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalize(c.in); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestNormalizeLeavesCorpusExamples: the rewrites the WL-1243 dry run showed
// to change meaning are left as written (WL-REQ-1854).
func TestNormalizeLeavesCorpusExamples(t *testing.T) {
	for name, text := range map[string]string{
		"keyword":         "see spec 77 now",
		"keyword section": "see spec 77 §4 now",
		"adr":             "per ADR 7 here",
		"WL-REQ-1809":     `number ("spec 74") is read as a spec of the rendering page's project, and`,
		"WL-REQ-1650":     "it names the Glossary, and a citation of spec 002 written before September 2026 means",
		"WL-PLAN-4":       "Exact values from spec 039 §3–§4 — quoted",
		"WL-PLAN-2 range": "grows the loop (spec 040 §7–§8); `serve`",
		"WL-PLAN-10 list": "Rendering and lifetime (spec 042 §4, §4.1, §5)",
		"WL-PLAN-137":     "- [ ] Commit: `Review button, disabled until spec 059 is built (WL-SPEC-66\n      §3.3)`.",
		"WL-PLAN-79":      `011 §1 (the "Corrected against spec 004 §2" self-correction)`, // spec-gate:ignore
	} {
		t.Run(name, func(t *testing.T) {
			if got := normalize(text); got != text {
				t.Errorf("rewritten:\n got %q\nwant %q", got, text)
			}
			hits := Scan(text, testKeys)
			if len(hits) == 0 {
				t.Fatal("no hits: left-alone text must still yield edges")
			}
			for _, h := range hits {
				if resolveFake(h) == nil {
					t.Errorf("%q does not resolve", h.Raw)
				}
			}
		})
	}
}

// TestNormalizeLeavesUrlPaths: a URL path with no scheme and host is a short
// form. The lines are the first lines of WL-1135 and WL-891 (WL-REQ-1854).
func TestNormalizeLeavesUrlPaths(t *testing.T) {
	for name, text := range map[string]string{
		"WL-1135": "GET /WL-MILE-2 answered `{\"error\":\"ref \\\"WL-MILE-2\\\" names no document in the backbone\"}`: refShortcut tried rule refs, tasks, then documents, never milestones or deliverables. The infixes were hardcoded in a dozen regexps and switches, so a new one had to be added everywhere by hand.",
		"WL-891":  "GET /{ref} (WL-721) resolves tasks and documents only, so a rule ref pasted at the root 404s: /WL-RULE-1 and its alias /WL-CL-1 both answer \"names no document in the backbone\", while /rules/WL-RULE-1 redirects. Fix: refShortcut hands a rule ref to ruleRefRedirect before the task lookup. Test: TestRefShortcut gains WL-RULE-1 and WL-CL-1 cases.",
	} {
		t.Run(name, func(t *testing.T) {
			hits := Scan(text, testKeys)
			ts := make([]*Target, len(hits))
			urls := 0
			for i, h := range hits {
				if h.Form == FormURL {
					ts[i] = &Target{Ref: "WL-X-9"}
					urls++
				}
			}
			if urls == 0 {
				t.Fatal("no URL-path hits: the paths must still yield edges")
			}
			if got := Normalize(text, hits, ts); got != text {
				t.Errorf("rewritten:\n got %q\nwant %q", got, text)
			}
		})
	}
}

func TestNormalizeAllowedForms(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"url with section", "see https://lode.example/WL-SPEC-77#sec-4 now", "see WL-REQ-12 now"},
		{"full section ref next to a keyword", "WL-SPEC-77#sec-4 (spec 77 §4)", "WL-REQ-12 (spec 77 §4)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalize(c.in); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestNormalizeUnresolvedUntouched(t *testing.T) {
	text := "see /tasks/WL-7 now"
	if got := Normalize(text, Scan(text, testKeys), []*Target{nil}); got != text {
		t.Errorf("got %q", got)
	}
}

func TestVerify(t *testing.T) {
	old := "a https://lode.example/tasks/WL-7 b\nc WL-RULE-12 d"
	ok := normalize(old)
	if err := Verify(old, ok, testKeys, resolveFake); err != nil {
		t.Errorf("pure normalization rejected: %v", err)
	}
	for name, bad := range map[string]string{
		"line count":    ok + "\nextra",
		"outside byte":  strings.Replace(ok, "a ", "A ", 1),
		"target change": strings.Replace(ok, "WL-7", "WL-8", 1),
	} {
		if err := Verify(old, bad, testKeys, resolveFake); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestVerifyRefusesLiteralRewrites: Verify accepts only what Normalize would
// rewrite, so a rewrite of a short form, quoted text or code is refused.
func TestVerifyRefusesLiteralRewrites(t *testing.T) {
	for name, c := range map[string][2]string{
		"keyword": {"see spec 77 now", "see WL-SPEC-77 now"},
		"quoted":  {`the form "https://lode.example/tasks/WL-7"`, `the form "WL-7"`},
		"code":    {"see `a\nhttps://lode.example/tasks/WL-7` now", "see `a\nWL-7` now"},
		"path":    {"see /tasks/WL-7 now", "see WL-7 now"},
	} {
		if err := Verify(c[0], c[1], testKeys, resolveFake); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
