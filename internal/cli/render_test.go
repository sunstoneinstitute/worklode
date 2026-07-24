package cli

import (
	"strings"
	"testing"
)

func TestProjectTableShowsKey(t *testing.T) {
	var b strings.Builder
	ProjectTable(&b, []Project{{ID: "worklode", Name: "Worklode", Key: "WL", Repos: []string{"a/b"}}})
	out := b.String()
	if !strings.Contains(out, "KEY") || !strings.Contains(out, "WL") {
		t.Fatalf("ProjectTable output missing KEY/WL:\n%s", out)
	}
}
