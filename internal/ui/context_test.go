package ui

import "testing"

func TestInitials(t *testing.T) {
	for in, want := range map[string]string{
		"Stig Bakken":    "SB",
		"alice":          "A",
		"ada king lovel": "AK",
		"  ":             "?",
		"":               "?",
	} {
		if got := initials(in); got != want {
			t.Errorf("initials(%q) = %q, want %q", in, got, want)
		}
	}
}
