package cli

import "testing"

func TestParseConfigSkipsTables(t *testing.T) {
	cfg, err := parseConfig("current_project = \"worklode\"\n\n[gate]\npaths = [\"internal/cmd/**\"]\ntrailer = \"Spec:\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CurrentProject != "worklode" {
		t.Errorf("CurrentProject = %q", cfg.CurrentProject)
	}
}

func TestWithParams(t *testing.T) {
	type probe struct {
		Project string   `query:"project,omitempty"`
		State   []string `query:"state,omitempty"`
		Tree    bool     `query:"tree,omitempty"`
		Limit   int      `query:"limit,omitempty"`
	}
	got := withParams("/x", probe{Project: "wl", State: []string{"a", "b"}, Tree: true, Limit: 5})
	if want := "/x?limit=5&project=wl&state=a&state=b&tree=true"; got != want {
		t.Errorf("withParams = %q, want %q", got, want)
	}
	if got := withParams("/x", probe{}); got != "/x" {
		t.Errorf("zero value: withParams = %q, want /x", got)
	}
}
