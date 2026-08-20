package api

// docs_internal_test.go exercises docSelectorFrom directly: it is a pure
// function of a query string, so its validation is covered here without a
// live store, alongside the store-backed round trips in docs_test.go.

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDocSelectorFromValid: the three derived selectors accept no filter, or
// one that restates what they imply.
func TestDocSelectorFromValid(t *testing.T) {
	for name, query := range map[string]string{
		"needs_planning alone":                     "needs_planning=true",
		"needs_planning restates kind and status":  "needs_planning=true&kind=spec&status=accepted",
		"needs_execution alone":                    "needs_execution=true",
		"needs_execution restates kind and status": "needs_execution=true&kind=plan&status=accepted",
		"bare_superseded alone":                    "bare_superseded=true",
		"bare_superseded with kind=spec":           "bare_superseded=true&kind=spec",
		"bare_superseded with kind=adr":            "bare_superseded=true&kind=adr",
		"bare_superseded restates status":          "bare_superseded=true&status=superseded",
		"no selector at all":                       "kind=plan&status=draft",
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/v1/docs?"+query, nil)
			if _, err := docSelectorFrom(r); err != nil {
				t.Fatalf("docSelectorFrom(%q) = %v, want no error", query, err)
			}
		})
	}
}

// TestDocSelectorFromConflicts: a selector implies a status and a kind (or,
// for bare_superseded, one of two kinds); a contradicting restatement is
// refused rather than silently answered with an empty list. More than one
// derived selector at once is refused the same way, whichever pair.
func TestDocSelectorFromConflicts(t *testing.T) {
	for name, c := range map[string]struct{ query, want string }{
		"needs_planning and needs_execution":  {"needs_planning=true&needs_execution=true", "disjoint"},
		"needs_planning with draft":           {"needs_planning=true&status=draft", "status=accepted"},
		"needs_planning with plan kind":       {"needs_planning=true&kind=plan", "kind=spec"},
		"needs_execution with draft":          {"needs_execution=true&status=draft", "status=accepted"},
		"needs_execution with spec kind":      {"needs_execution=true&kind=spec", "kind=plan"},
		"bare_superseded with draft":          {"bare_superseded=true&status=draft", "status=superseded"},
		"bare_superseded with plan kind":      {"bare_superseded=true&kind=plan", "kind=spec or adr"},
		"bare_superseded and needs_planning":  {"bare_superseded=true&needs_planning=true", "mutually exclusive"},
		"bare_superseded and needs_execution": {"bare_superseded=true&needs_execution=true", "mutually exclusive"},
		"unparseable bare_superseded":         {"bare_superseded=maybe", "bare_superseded"},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/v1/docs?"+c.query, nil)
			_, err := docSelectorFrom(r)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("docSelectorFrom(%q) = %v, want it to mention %q", c.query, err, c.want)
			}
		})
	}
}
