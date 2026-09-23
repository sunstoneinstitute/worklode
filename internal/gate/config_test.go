package gate

import (
	"strings"
	"testing"
)

func TestParseGateTable(t *testing.T) {
	data := []byte(`current_project = "worklode"
project_key = "WL"

[gate]
paths = ["internal/cmd/**", "ns/*.ttl"]
regex = ["^internal/store/(tasks|claim)\\.go$"]
`)
	cfg, ok, err := Parse(data)
	if err != nil || !ok {
		t.Fatalf("Parse: ok=%v err=%v", ok, err)
	}
	if cfg.Trailer != "Spec:" {
		t.Errorf("Trailer defaults to Spec:, got %q", cfg.Trailer)
	}
	if len(cfg.Paths) != 2 || len(cfg.Regex) != 1 {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestParseNoGateTable(t *testing.T) {
	_, ok, err := Parse([]byte(`current_project = "worklode"` + "\n"))
	if err != nil || ok {
		t.Fatalf("no table: ok=%v err=%v, want false nil", ok, err)
	}
}

func TestParseEmptyGateTableIsAnError(t *testing.T) {
	if _, _, err := Parse([]byte("[gate]\ntrailer = \"Spec:\"\n")); err == nil {
		t.Fatal("a [gate] table naming no paths and no regex must be refused")
	}
	if _, _, err := Parse([]byte("[gate]\nregex = [\"(\"]\n")); err == nil {
		t.Fatal("an invalid regex must be refused at parse time")
	}
}

func TestParseTrailerNeedsAColon(t *testing.T) {
	// "Spec" without the colon matches no line, so the gate would refuse
	// every guarded change.
	_, _, err := Parse([]byte("[gate]\npaths = [\"internal/cmd/**\"]\ntrailer = \"Spec\"\n"))
	if err == nil {
		t.Fatal("a trailer key without a colon must be refused")
	}
	if !strings.Contains(err.Error(), "colon") {
		t.Errorf("error should name the colon, got %v", err)
	}
}

func TestGuardsMatch(t *testing.T) {
	cfg := Config{
		Paths: []string{"internal/cmd/**", "ns/*.ttl", "**/router.go", "deploy/base/**"},
		Regex: []string{`^internal/store/(tasks|claim)\.go$`},
	}
	g, err := cfg.Guards()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"internal/cmd/gate.go":            true,
		"internal/cmd/sub/deep.go":        true,
		"internal/cmdx/gate.go":           false,
		"ns/concept.ttl":                  true,
		"ns/sub/concept.ttl":              false,
		"internal/api/router.go":          true,
		"router.go":                       true,
		"deploy/base/migrations/0085.sql": true,
		"internal/store/tasks.go":         true,
		"internal/store/docs.go":          false,
		"README.md":                       false,
	}
	for p, want := range cases {
		if got := g.Match(p); got != want {
			t.Errorf("Match(%q) = %v, want %v", p, got, want)
		}
	}
}
