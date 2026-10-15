package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestSPARoutesMatchRouter checks spaRoutes against the checked-in
// web/app/src/routes.json, never a build output, so go test needs no Node.
func TestSPARoutesMatchRouter(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "web", "app", "src", "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fromApp []string
	if err := json.Unmarshal(raw, &fromApp); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fromApp, spaRoutes) {
		t.Fatalf("web/app/src/routes.json = %v, spaRoutes = %v: add the page to both", fromApp, spaRoutes)
	}
}

func TestMuxPattern(t *testing.T) {
	for route, want := range map[string]string{
		"/projects":                  "/projects",
		"/projects/$projectId/tasks": "/projects/{projectId}/tasks",
	} {
		if got := muxPattern(route); got != want {
			t.Errorf("muxPattern(%q) = %q, want %q", route, got, want)
		}
	}
}
