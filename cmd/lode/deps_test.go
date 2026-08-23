package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestUserCLIDoesNotLinkExtractedOperatorImplementations(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "github.com/sunstoneinstitute/worklode/internal/api" ||
			dep == "github.com/sunstoneinstitute/worklode/internal/projector" ||
			dep == "github.com/sunstoneinstitute/worklode/internal/serverapp" ||
			dep == "github.com/sunstoneinstitute/worklode/internal/watchapp" ||
			dep == "github.com/sunstoneinstitute/worklode/internal/migrateapp" ||
			strings.HasPrefix(dep, "k8s.io/client-go/") {
			t.Errorf("lode must not link operator dependency %s", dep)
		}
	}

	out, err = exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, "github.com/sunstoneinstitute/worklode/internal/cmd").Output()
	if err != nil {
		t.Fatalf("go list internal/cmd imports: %v", err)
	}
	for _, imported := range strings.Fields(string(out)) {
		if imported == "github.com/sunstoneinstitute/worklode/internal/api" ||
			imported == "github.com/sunstoneinstitute/worklode/internal/store" ||
			imported == "github.com/sunstoneinstitute/worklode/internal/projector" ||
			strings.HasPrefix(imported, "k8s.io/client-go/") {
			t.Errorf("internal/cmd must not directly import operator dependency %s", imported)
		}
	}
}
