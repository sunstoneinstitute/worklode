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
