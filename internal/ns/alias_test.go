package ns_test

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

func TestNormalizeTaskKind(t *testing.T) {
	cases := []struct {
		kind        string
		wantKind    string
		wantAliased bool
	}{
		{"spec", "design", true},
		{"design", "design", false},
		{"bug", "bug", false},
		{"", "", false},
		{"nonsense", "nonsense", false},
	}
	for _, tc := range cases {
		gotKind, gotAliased := ns.NormalizeTaskKind(tc.kind)
		if gotKind != tc.wantKind || gotAliased != tc.wantAliased {
			t.Errorf("NormalizeTaskKind(%q) = (%q, %v), want (%q, %v)",
				tc.kind, gotKind, gotAliased, tc.wantKind, tc.wantAliased)
		}
	}
}

// TestDeprecatedTaskKindsIsAliasTableNotSecondKindList keeps
// DeprecatedTaskKinds from drifting into a second kind list: every key must
// be a retired spelling (absent from ns.TaskKinds) and every value must be a
// current one (present in ns.TaskKinds).
func TestDeprecatedTaskKindsIsAliasTableNotSecondKindList(t *testing.T) {
	current := ns.Set(ns.TaskKinds)
	for alias, kind := range ns.DeprecatedTaskKinds {
		if current[alias] {
			t.Errorf("alias key %q is a current task kind; DeprecatedTaskKinds must only map retired spellings", alias)
		}
		if !current[kind] {
			t.Errorf("alias value %q for %q is not a current task kind", kind, alias)
		}
	}
}
