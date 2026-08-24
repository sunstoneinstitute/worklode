package disttest

import (
	"os/exec"
	"strings"
	"testing"
)

// 053 §2: lode-hook and lode-statusline are short-lived hot paths. They parse
// their own arguments with the stdlib, so their transitive graphs must stay
// clear of the command tree, the document renderer, and everything the server
// and watcher pull in. Nothing else in the build fails when one of these
// creeps back, hence this guard.

const modulePath = "github.com/sunstoneinstitute/worklode"

var hotPathTargets = []string{
	modulePath + "/cmd/lode-hook",
	modulePath + "/cmd/lode-statusline",
}

// forbidden reports why dep is not allowed in a hot path, or "" if it is.
func forbidden(dep string) string {
	switch {
	case dep == "github.com/spf13/cobra":
		return "the Cobra command tree"
	case strings.HasPrefix(dep, "github.com/yuin/goldmark"):
		return "the Goldmark markdown renderer"
	case dep == modulePath+"/internal/api",
		dep == modulePath+"/internal/store",
		dep == modulePath+"/internal/watch":
		return "a server, store, or watcher package"
	case strings.HasPrefix(dep, "github.com/prometheus/"):
		return "a Prometheus package"
	case strings.HasPrefix(dep, "k8s.io/"):
		return "a Kubernetes package"
	}
	return ""
}

func TestHotPathDependencyBoundaries(t *testing.T) {
	for _, target := range hotPathTargets {
		t.Run(target, func(t *testing.T) {
			out, err := exec.Command("go", "list", "-deps", target).Output()
			if err != nil {
				t.Fatalf("go list -deps %s: %v", target, err)
			}
			deps := strings.Fields(string(out))
			// A renamed or deleted entry point must fail loudly rather than
			// pass an empty dependency list.
			var found bool
			for _, dep := range deps {
				if dep == target {
					found = true
				}
				if why := forbidden(dep); why != "" {
					t.Errorf("%s must not depend on %s (%s)", target, dep, why)
				}
			}
			if !found {
				t.Fatalf("go list -deps %s did not report the target itself; is the entry point still there?", target)
			}
		})
	}
}
