package designdoc

import (
	"errors"
	"testing"
)

func TestSplitFragment(t *testing.T) {
	tests := []struct {
		ref, base, section string
	}{
		{"WL-SPEC-14", "WL-SPEC-14", ""},
		{"WL-SPEC-14#sec-2.1", "WL-SPEC-14", "sec-2.1"},
		{"docs/specs/014-x.md#sec-3", "docs/specs/014-x.md", "sec-3"},
		{"#sec-1", "", "sec-1"},
	}
	for _, tt := range tests {
		base, section := SplitFragment(tt.ref)
		if base != tt.base || section != tt.section {
			t.Errorf("SplitFragment(%q) = (%q, %q), want (%q, %q)", tt.ref, base, section, tt.base, tt.section)
		}
	}
}

func TestParseShorthand(t *testing.T) {
	tests := []struct {
		base string
		want Shorthand
		ok   bool
	}{
		{"WL-SPEC-25", Shorthand{Key: "WL", Type: "SPEC", Number: 25}, true},
		{"WL-ADR-3", Shorthand{Key: "WL", Type: "ADR", Number: 3}, true},
		{"WL-SPEC-0", Shorthand{Key: "WL", Type: "SPEC", Number: 0}, true},
		{"CMS1-SPEC-7", Shorthand{Key: "CMS1", Type: "SPEC", Number: 7}, true},
		{"NO-SPEC", Shorthand{}, false},                                    // the sentinel is not a shorthand
		{"wl-spec-25", Shorthand{}, false},                                 // the key is upper case
		{"WL-PLAN-1", Shorthand{Key: "WL", Type: "PLAN", Number: 1}, true}, // 029 §4
		{"W-SPEC-1", Shorthand{}, false},                                   // key is at least two characters
		{"WL-SPEC-25#sec-2", Shorthand{}, false},                           // fragment must be split off first
		{"025-documents", Shorthand{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseShorthand(tt.base)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseShorthand(%q) = (%+v, %v), want (%+v, %v)", tt.base, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseShorthandKind(t *testing.T) {
	for _, tt := range []struct{ typ, kind string }{{"SPEC", "spec"}, {"ADR", "adr"}} {
		if got := (Shorthand{Type: tt.typ}).Kind(); got != tt.kind {
			t.Errorf("Shorthand{Type: %q}.Kind() = %q, want %q", tt.typ, got, tt.kind)
		}
	}
}

func TestParseNumberForm(t *testing.T) {
	tests := []struct {
		base string
		want NumberForm
		ok   bool
	}{
		{"14", NumberForm{Number: 14}, true},
		{"014", NumberForm{Number: 14}, true},
		{"0", NumberForm{Number: 0}, true},
		{"014-design-documents", NumberForm{Number: 14, Rest: "-design-documents"}, true},
		{"docs/specs/014-x.md", NumberForm{}, false}, // a path is form 1
		{"WL-SPEC-14", NumberForm{}, false},
		{"", NumberForm{}, false},
		{"14#sec-2", NumberForm{}, false}, // fragment must be split off first
		{"99999999999999999999", NumberForm{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseNumberForm(tt.base)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseNumberForm(%q) = (%+v, %v), want (%+v, %v)", tt.base, got, ok, tt.want, tt.ok)
		}
	}
}

func TestKindMismatchErrorMessage(t *testing.T) {
	err := &KindMismatchError{Doc: "007-drift-and-overview", Want: "adr", Got: "spec"}
	want := "007-drift-and-overview: ref names an ADR, document is a spec"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	err = &KindMismatchError{Doc: "007-drift-and-overview", Want: "spec", Got: "adr"}
	want = "007-drift-and-overview: ref names a spec, document is an ADR"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestUnresolvedErrorMessage(t *testing.T) {
	err := &UnresolvedError{Key: "CMS"}
	if got, want := err.Error(), "unresolved: project CMS not known here"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestAmbiguousRefErrorMessage(t *testing.T) {
	err := &AmbiguousRefError{Ref: "4", Candidates: []string{"004-execution", "004-other"}}
	want := "ambiguous ref \"4\":\n004-execution\n004-other"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestNoSpecError(t *testing.T) {
	err := NoSpecError("WL-SPEC-0")
	if !errors.Is(err, ErrNoSpec) {
		t.Fatalf("err = %v, want it to wrap ErrNoSpec", err)
	}
	if got := err.Error(); got != "WL-SPEC-0 is the no-governing-spec sentinel (026 §4.3), not a document: no governing spec" {
		t.Errorf("Error() = %q", got)
	}
}
