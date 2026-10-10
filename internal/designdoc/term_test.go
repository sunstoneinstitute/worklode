package designdoc

import "testing"

func TestTermSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Lease":              "lease",
		"Edge Agent":         "edge-agent",
		"  `lode` CLI (v2)!": "lode-cli-v2",
		"Årsak & virkning":   "årsak-virkning",
		"---":                "",
	} {
		if got := TermSlug(in); got != want {
			t.Errorf("TermSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
