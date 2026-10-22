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
		{"bare url", "see /tasks/WL-7 now", "see WL-7 now"},
		{"link with ref text", "see [WL-7](/tasks/WL-7) now", "see WL-7 now"},
		{"link with other text", "see [the task](/tasks/WL-7) now", "see [the task](WL-7) now"},
		{"section with rule", "see WL-SPEC-77#sec-4 now", "see WL-REQ-12 now"},
		{"section without rule", "see WL-SPEC-77#sec-9 now", "see WL-SPEC-77#sec-9 now"},
		{"wrong infix", "see WL-RULE-12 now", "see WL-REQ-12 now"},
		{"keyword", "see spec 77 now", "see WL-SPEC-77 now"},
		{"in code", "see `/tasks/WL-7` now", "see `/tasks/WL-7` now"},
		{"fenced", "```\n/tasks/WL-7\n```", "```\n/tasks/WL-7\n```"},
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
	old := "a /tasks/WL-7 b\nc WL-RULE-12 d"
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
