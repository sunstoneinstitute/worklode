package secrets

import "testing"

func TestValidName(t *testing.T) {
	valid := []string{"GITHUB_TOKEN", "KUBECONFIG_HZDEV", "A", "X1_Y2"}
	for _, n := range valid {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false; want true", n)
		}
	}
	invalid := []string{"", "github_token", "1TOKEN", "_TOKEN", "GITHUB-TOKEN",
		"GITHUB TOKEN", "op://Employee/x", "A=B"}
	for _, n := range invalid {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true; want false", n)
		}
	}
}
