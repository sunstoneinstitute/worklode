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
