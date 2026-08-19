package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WriteEnvFile renders entries in `op run` env-file format —
// NAME=op://vault/item/field, one per line, sorted by name. References only,
// never values: the file is the portable packing manifest (spec 017 v1.5
// re-uses it verbatim for remote executors). 0600 because vault/item names
// are mildly sensitive.
func WriteEnvFile(path string, entries []Entry) error {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	for _, e := range sorted {
		fmt.Fprintf(&b, "%s=%s\n", e.Name, e.Ref)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
