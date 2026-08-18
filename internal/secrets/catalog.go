package secrets

import (
	"fmt"
	"strings"
)

// Entry is one catalog secret: a symbolic name mapped to a 1Password
// reference plus policy. The ref addresses a value; it never is one.
type Entry struct {
	Name        string
	Ref         string // op://vault/item/field
	Description string
	Baseline    bool // packed for every task; exempt from the consent prompt
}

// Catalog is the parsed org-wide secrets catalog, in file order.
type Catalog struct {
	Entries []Entry
}

// Get returns the entry for name.
func (c *Catalog) Get(name string) (Entry, bool) {
	for _, e := range c.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Resolve maps a task's declared names to catalog entries: the baseline set
// (every baseline entry, declared or not), the consent set (declared,
// non-baseline), and declared names missing from the catalog. A missing name
// is a warning at the ceremony, never a failure (spec 017 degradation).
func (c *Catalog) Resolve(declared []string) (baseline, consented []Entry, missing []string) {
	for _, e := range c.Entries {
		if e.Baseline {
			baseline = append(baseline, e)
		}
	}
	for _, name := range declared {
		e, ok := c.Get(name)
		switch {
		case !ok:
			missing = append(missing, name)
		case !e.Baseline:
			consented = append(consented, e)
		}
	}
	return baseline, consented, missing
}

// ParseCatalog parses the catalog TOML subset: [NAME] tables with
// ref/description string keys and a baseline bool, full-line and trailing
// comments. A hand-rolled parser, matching the module's existing stance of
// carrying no TOML dependency (see internal/cli config parsing).
func ParseCatalog(data []byte) (*Catalog, error) {
	c := &Catalog{}
	cur := -1
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.Index(line, "]")
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated table header", i+1)
			}
			name := strings.TrimSpace(line[1:end])
			if !ValidName(name) {
				return nil, fmt.Errorf("line %d: invalid secret name %q", i+1, name)
			}
			if _, ok := c.Get(name); ok {
				return nil, fmt.Errorf("line %d: duplicate entry %q", i+1, name)
			}
			c.Entries = append(c.Entries, Entry{Name: name})
			cur = len(c.Entries) - 1
			continue
		}
		if cur < 0 {
			return nil, fmt.Errorf("line %d: key outside a [NAME] table", i+1)
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", i+1)
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "ref", "description":
			s, err := unquote(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: %s: %v", i+1, key, err)
			}
			if key == "ref" {
				c.Entries[cur].Ref = s
			} else {
				c.Entries[cur].Description = s
			}
		case "baseline":
			if j := strings.Index(val, "#"); j >= 0 {
				val = strings.TrimSpace(val[:j])
			}
			switch val {
			case "true":
				c.Entries[cur].Baseline = true
			case "false":
				c.Entries[cur].Baseline = false
			default:
				return nil, fmt.Errorf("line %d: baseline must be true or false", i+1)
			}
		default:
			return nil, fmt.Errorf("line %d: unknown key %q", i+1, key)
		}
	}
	for _, e := range c.Entries {
		if e.Ref == "" {
			return nil, fmt.Errorf("entry %s: ref is required", e.Name)
		}
	}
	return c, nil
}

// unquote extracts a "quoted" value, ignoring anything after the closing
// quote (trailing comments).
func unquote(val string) (string, error) {
	if len(val) < 2 || val[0] != '"' {
		return "", fmt.Errorf(`expected a "quoted" value`)
	}
	end := strings.Index(val[1:], `"`)
	if end < 0 {
		return "", fmt.Errorf("unterminated string")
	}
	return val[1 : 1+end], nil
}
