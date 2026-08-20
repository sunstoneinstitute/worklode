package model_test

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestNormalizeAgent(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"claude-code", "claude-code"},
		{"other", "other"},
		{"pi", "pi"},
		{"codx", "other"},        // typo for codex
		{"Claude-Code", "other"}, // the vocabulary is case-sensitive
		{"some-new-harness", "other"},
		{"", ""}, // nothing to record; the caller applies its own default
	} {
		if got := model.NormalizeAgent(tc.in); got != tc.want {
			t.Errorf("NormalizeAgent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Every id NormalizeAgent can emit must itself be accepted, or normalizing
// would hand the store a value it rejects.
func TestNormalizeAgentIsIdempotent(t *testing.T) {
	for _, a := range append([]string{"codx", ""}, model.KnownAgents...) {
		once := model.NormalizeAgent(a)
		if twice := model.NormalizeAgent(once); twice != once {
			t.Errorf("NormalizeAgent(%q) = %q, but renormalizes to %q", a, once, twice)
		}
		if once != "" && !model.AgentKnown(once) {
			t.Errorf("NormalizeAgent(%q) = %q, which AgentKnown rejects", a, once)
		}
	}
}
