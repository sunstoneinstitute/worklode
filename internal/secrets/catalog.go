package secrets

import (
	"fmt"
	"strings"
)

// Entry is one catalog secret: a symbolic name mapped either to a single
// 1Password reference (plain) or to a plaintext template plus one reference
// per credential (templated, spec 042 §1). A ref addresses a value; it never
// is one.
type Entry struct {
	Name string
	Ref  string // op://vault/item/field — plain entries only

	// Template names a sibling key of the projected catalog Secret holding
	// the template text (ADR 043 §2); TemplateText is that text, which only
	// the server reads off disk and only the wire carries onward. Creds maps
	// each placeholder in the text to its own reference. All three are empty
	// on a plain entry.
	Template     string
	TemplateText string
	Creds        []Cred // file order

	// Env is the environment-variable name the entry is exported under at
	// exec time; empty means the entry name (spec 042 §2).
	Env         string
	Description string
	Baseline    bool // packed for every task; exempt from the consent prompt
}

// Cred is one credential of a templated entry: the placeholder it fills and
// the reference resolving it.
type Cred struct {
	Placeholder string
	Ref         string
}

// Templated reports whether the entry renders a template rather than
// injecting a single value. It keys off the credentials, not the template
// name: the catalog key is a server-side lookup that never crosses the wire,
// so the client would otherwise see every entry as plain.
func (e Entry) Templated() bool { return len(e.Creds) > 0 }

// EnvName is the name the entry is exported under at exec time.
func (e Entry) EnvName() string {
	if e.Env != "" {
		return e.Env
	}
	return e.Name
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
// ref/template/env/description string keys, cred.<PLACEHOLDER> reference
// keys and a baseline bool, full-line and trailing comments. A hand-rolled
// parser, matching the module's existing stance of carrying no TOML
// dependency (see internal/cli config parsing).
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
		// cred.<PLACEHOLDER> is the only dotted key: a strings.Cut, not
		// dotted-key machinery (spec 042 §2).
		if prefix, placeholder, dotted := strings.Cut(key, "."); dotted {
			if prefix != "cred" {
				return nil, fmt.Errorf("line %d: unknown key %q", i+1, key)
			}
			if !ValidName(placeholder) {
				return nil, fmt.Errorf("line %d: invalid placeholder %q", i+1, placeholder)
			}
			s, err := unquote(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: %s: %v", i+1, key, err)
			}
			for _, cr := range c.Entries[cur].Creds {
				if cr.Placeholder == placeholder {
					return nil, fmt.Errorf("line %d: duplicate placeholder %q", i+1, placeholder)
				}
			}
			c.Entries[cur].Creds = append(c.Entries[cur].Creds, Cred{Placeholder: placeholder, Ref: s})
			continue
		}
		switch key {
		case "ref", "description", "template", "env":
			s, err := unquote(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: %s: %v", i+1, key, err)
			}
			switch key {
			case "ref":
				c.Entries[cur].Ref = s
			case "description":
				c.Entries[cur].Description = s
			case "template":
				c.Entries[cur].Template = s
			case "env":
				if !ValidName(s) {
					return nil, fmt.Errorf("line %d: invalid env name %q", i+1, s)
				}
				c.Entries[cur].Env = s
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
	// An item name is what reaches the keystore, the env file and (via
	// pack) an exec child, so the catalog-wide uniqueness check lives here
	// rather than at any one consumer. The name grammar admits "__", so
	// nothing else stops a plain entry named X__Y from colliding with a
	// templated entry's derived item (spec 042 §2).
	seen := map[string]string{}
	for _, e := range c.Entries {
		switch {
		case e.Ref != "" && e.Template != "":
			return nil, fmt.Errorf("entry %s: ref and template are mutually exclusive", e.Name)
		case e.Ref == "" && e.Template == "":
			return nil, fmt.Errorf("entry %s: one of ref or template is required", e.Name)
		case e.Template != "" && len(e.Creds) == 0:
			return nil, fmt.Errorf("entry %s: template needs at least one cred.<PLACEHOLDER>", e.Name)
		case e.Template == "" && len(e.Creds) > 0:
			return nil, fmt.Errorf("entry %s: cred keys need a template", e.Name)
		}
		for _, item := range Items(e) {
			if !ValidName(item) {
				return nil, fmt.Errorf("entry %s: invalid item name %q", e.Name, item)
			}
			if other, dup := seen[item]; dup {
				return nil, fmt.Errorf("entry %s: item name %q collides with entry %s", e.Name, item, other)
			}
			seen[item] = e.Name
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
