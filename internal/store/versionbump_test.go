package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestOnlyBumpFunctionsWriteVersions holds every write path to versions.go: a
// version that moves anywhere else moves without its snapshot (WL-SPEC-77 §3).
// insertRule's version-1 rule_versions insert is creation, not a bump, and
// is not matched.
func TestOnlyBumpFunctionsWriteVersions(t *testing.T) {
	snapshotInsert := regexp.MustCompile(`(?i)INSERT INTO (doc_versions|doc_edge_versions|doc_rule_versions|rule_edge_versions)\b`)
	versionUpdate := regexp.MustCompile(`(?is)UPDATE (docs|rules) SET (.*?)\bWHERE\b`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "versions.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := snapshotInsert.Find(src); m != nil {
			t.Errorf("%s: %q outside versions.go; snapshot through bumpDocVersion or bumpRuleVersion", f, m)
		}
		for _, m := range versionUpdate.FindAllSubmatch(src, -1) {
			if regexp.MustCompile(`(?i)\bversion\s*=`).Match(m[2]) {
				t.Errorf("%s: %q moves a version outside versions.go", f, m[0])
			}
		}
	}
}
