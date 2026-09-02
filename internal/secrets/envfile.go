package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WriteEnvFile renders entries in `op run` env-file format —
// ITEM=op://vault/item/field, one line per keystore item, sorted by item
// name. A plain entry contributes one line under its own name; a templated
// entry contributes one per credential, under NAME__PLACEHOLDER (spec 042
// §3). References only, never values: the file is the portable packing
// manifest (spec 017 v1.5 re-uses it verbatim for remote executors). 0600
// because vault/item names are mildly sensitive.
func WriteEnvFile(path string, entries []Entry) error {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.Templated() {
			lines = append(lines, e.Name+"="+e.Ref)
			continue
		}
		for _, c := range e.Creds {
			lines = append(lines, ItemName(e.Name, c.Placeholder)+"="+c.Ref)
		}
	}
	sort.Strings(lines)
	var b strings.Builder
	for _, l := range lines {
		fmt.Fprintln(&b, l)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
