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

func TestSPAHandles(t *testing.T) {
	saved := spaRoutes
	spaRoutes = []string{"/projects", "/projects/$projectId/tasks"}
	t.Cleanup(func() { spaRoutes = saved })
	for path, want := range map[string]bool{
		"/projects": true, "/projects/": false, "/projects/p1/tasks": true,
		"/projects//tasks": false, "/projects/p1": false, "/work": false,
	} {
		if got := spaHandles(path); got != want {
			t.Errorf("spaHandles(%q) = %v, want %v", path, got, want)
		}
	}
}
