package gate

import (
	"errors"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

func TestParseLineForms(t *testing.T) {
	ok := map[string]func(Declaration) bool{
		"Spec: WL-RULE-456":         func(d Declaration) bool { return d.Rule != nil && d.Rule.Number == 456 && d.Qualifier == "" },
		"Spec: WL-RULE-456 amended": func(d Declaration) bool { return d.Rule != nil && d.Qualifier == "amended" },
		"Spec: WL-RULE-456 fix":     func(d Declaration) bool { return d.Rule != nil && d.Qualifier == "fix" },
		"Spec: WL-REQ-456":          func(d Declaration) bool { return d.Rule != nil && d.Rule.Number == 456 },
		"Spec: none refactor":       func(d Declaration) bool { return d.None == "refactor" && d.Rule == nil },
		"  Spec:   none   tests  ":  func(d Declaration) bool { return d.None == "tests" },
	}
	for line, check := range ok {
		d, matched, err := ParseLine("Spec:", line)
		if err != nil || !matched {
			t.Errorf("ParseLine(%q): matched=%v err=%v", line, matched, err)
			continue
		}
		if !check(d) {
			t.Errorf("ParseLine(%q) = %+v", line, d)
		}
	}
	bad := []string{
		"Spec: none fix",        // WL-REQ-7: a fix with nothing to cite is refused
		"Spec: none",            // no reason
		"Spec: none later",      // not in the closed list
		"Spec: WL-SPEC-4",       // a document is not a rule
		"Spec: WL-SPEC-4 sec-5", // a section value is refused (WL-REQ-7)
		"Spec: WL-SPEC-4#sec-5 amended",
		"Spec: WL-RULE-456 sometime", // unknown qualifier
		"Spec: 456",
		"Spec:",
	}
	for _, line := range bad {
		if _, matched, err := ParseLine("Spec:", line); !matched || err == nil {
			t.Errorf("ParseLine(%q) should match the key and fail, got matched=%v err=%v", line, matched, err)
		}
	}
	if _, matched, _ := ParseLine("Spec:", "Specification: WL-RULE-1"); matched {
		t.Error("a longer word starting with the key is not the trailer")
	}
}

func TestFindTakesTheFirstTrailer(t *testing.T) {
	text := "Adds a thing.\n\nSpec: WL-RULE-12\nWorklode-Task: WL-99\nSpec: WL-RULE-13\n"
	d, err := Find("Spec:", text)
	if err != nil || d.Rule == nil || d.Rule.Number != 12 {
		t.Fatalf("Find = %+v, %v", d, err)
	}
	if _, err := Find("Spec:", "no trailer here\n"); !errors.Is(err, ErrNoTrailer) {
		t.Errorf("Find on text without a trailer = %v, want ErrNoTrailer", err)
	}
	if _, err := Find("Spec:", "Spec: none later\n"); err == nil || errors.Is(err, ErrNoTrailer) {
		t.Errorf("a malformed trailer is an error, not ErrNoTrailer: %v", err)
	}
}

func TestCheck(t *testing.T) {
	cfg := Config{Paths: []string{"internal/cmd/**"}, Trailer: "Spec:"}
	v, err := Check(cfg, Input{Changed: []string{"README.md", "docs/x.md"}})
	if err != nil || len(v.Guarded) != 0 {
		t.Fatalf("no guarded path: %+v %v", v, err)
	}
	_, err = Check(cfg, Input{Changed: []string{"internal/cmd/gate.go"}, Texts: []string{"body\n", "a commit\n"}})
	if err == nil || !strings.Contains(err.Error(), "internal/cmd/gate.go") || !strings.Contains(err.Error(), "Spec:") {
		t.Fatalf("guarded path without trailer must name the path and the key: %v", err)
	}
	v, err = Check(cfg, Input{Changed: []string{"internal/cmd/gate.go"}, Texts: []string{"body\n", "Feat\n\nSpec: WL-RULE-3\n"}})
	if err != nil || v.Declaration == nil || v.Declaration.Rule == nil {
		t.Fatalf("trailer in a commit passes: %+v %v", v, err)
	}
	_, err = Check(cfg, Input{Changed: []string{"internal/cmd/gate.go"}, Texts: []string{"Spec: none fix\n"}})
	if err == nil {
		t.Fatal("a malformed trailer fails the check")
	}
}

// TestCheckTrailerForms runs the three trailer forms through Check: a rule
// ref passes, none <reason> passes, a section fails naming the rule it
// resolves to, or `lode show <doc>#sec-N` when it cannot be resolved
// (WL-REQ-7).
func TestCheckTrailerForms(t *testing.T) {
	cfg := Config{Paths: []string{"internal/**"}, Trailer: "Spec:"}
	changed := []string{"internal/x.go"}
	v, err := Check(cfg, Input{Changed: changed, Texts: []string{"Spec: WL-REQ-7\n"}})
	if err != nil || v.Declaration.String() != "WL-RULE-7" {
		t.Fatalf("rule ref: %+v %v", v.Declaration, err)
	}
	v, err = Check(cfg, Input{Changed: changed, Texts: []string{"Spec: none refactor\n"}})
	if err != nil || v.Declaration.None != "refactor" {
		t.Fatalf("none reason: %+v %v", v.Declaration, err)
	}
	_, err = Check(cfg, Input{Changed: changed, Texts: []string{"Spec: WL-SPEC-72 sec-4\n"}})
	var se *SectionError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "lode show WL-SPEC-72#sec-4") {
		t.Fatalf("unresolved section must name lode show <doc>#sec-N: %v", err)
	}
	resolve := func(s designdoc.SectionRef) string {
		if s.Shorthand.Number == 72 && s.Anchor == "sec-4" {
			return "WL-REQ-7"
		}
		return ""
	}
	_, err = Check(cfg, Input{Changed: changed, Texts: []string{"Spec: WL-SPEC-72#sec-4 amended\n"}, ResolveSection: resolve})
	if err == nil || !strings.Contains(err.Error(), "Spec: WL-REQ-7") || strings.Contains(err.Error(), "lode show") {
		t.Fatalf("resolved section must name its rule ref: %v", err)
	}
}
