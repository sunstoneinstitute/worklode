package mcp

import "testing"

func TestParseRef(t *testing.T) {
	keys := []string{"WL", "DS"}
	for in, want := range map[string]string{
		"spec seventy-seven":            "WL-SPEC-77",
		"task 512":                      "WL-512",
		"spec-77":                       "WL-SPEC-77",
		"WL-7":                          "WL-7",
		"wl-7":                          "WL-7",
		"ds-9":                          "DS-9",
		"XX-7":                          "XX-7",
		"rollout-2":                     "rollout-2",
		"requirement twelve":            "WL-REQ-12",
		"req 12":                        "WL-REQ-12",
		"plan one hundred fifty-eight":  "WL-PLAN-158",
		"rule nine hundred ninety-nine": "WL-RULE-999",
		"rule one thousand":             "rule one thousand",
		"spec":                          "spec",
		"WL-SPEC-77":                    "WL-SPEC-77",
		"  task 5 ":                     "WL-5",
	} {
		if got := parseRef(in, "WL", keys); got != want {
			t.Errorf("parseRef(%q) = %q, want %q", in, got, want)
		}
	}
}
