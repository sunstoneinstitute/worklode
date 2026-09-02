package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// Template rendering for spec 042: a plaintext template plus one credential
// per placeholder. Substitution is verbatim and single-pass — no escaping, no
// conditionals, no nesting, and no re-expansion of a credential value that
// itself looks like a placeholder.

// segment is one run of a scanned template: literal text, or a placeholder
// name. Splitting once is what makes Render single-pass by construction.
type segment struct {
	text        string
	placeholder bool
}

// scanTemplate splits text into literal and placeholder segments. Any "{{"
// that does not open a well-formed "{{ NAME }}" is an error, so a typo fails
// catalog validation instead of rendering a broken artifact.
func scanTemplate(text string) ([]segment, error) {
	var segs []segment
	for {
		i := strings.Index(text, "{{")
		if i < 0 {
			if text != "" {
				segs = append(segs, segment{text: text})
			}
			return segs, nil
		}
		end := strings.Index(text[i:], "}}")
		if end < 0 {
			return nil, fmt.Errorf("unterminated placeholder at %q", excerpt(text[i:]))
		}
		name := strings.TrimSpace(text[i+2 : i+end])
		if !ValidName(name) {
			return nil, fmt.Errorf("invalid placeholder %q", name)
		}
		if i > 0 {
			segs = append(segs, segment{text: text[:i]})
		}
		segs = append(segs, segment{text: name, placeholder: true})
		text = text[i+end+2:]
	}
}

// excerpt trims a fragment for an error message so a template body never
// lands whole in a log line.
func excerpt(s string) string {
	if len(s) > 24 {
		return s[:24] + "…"
	}
	return s
}

// Placeholders lists the distinct placeholders in a template, in first
// appearance order.
func Placeholders(text string) ([]string, error) {
	segs, err := scanTemplate(text)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range segs {
		if s.placeholder && !slices.Contains(out, s.text) {
			out = append(out, s.text)
		}
	}
	return out, nil
}

// ValidateTemplate checks that a templated entry's placeholder set and cred
// set match exactly, both directions, and that the text is valid UTF-8: it
// crosses two JSON round-trips (the catalog response and the manifest) where
// Go's encoder would silently replace invalid bytes and corrupt "verbatim".
func ValidateTemplate(e Entry, text string) error {
	if !utf8.ValidString(text) {
		return fmt.Errorf("entry %s: template %s is not valid UTF-8", e.Name, e.Template)
	}
	found, err := Placeholders(text)
	if err != nil {
		return fmt.Errorf("entry %s: template %s: %w", e.Name, e.Template, err)
	}
	declared := make([]string, 0, len(e.Creds))
	for _, c := range e.Creds {
		declared = append(declared, c.Placeholder)
	}
	for _, p := range found {
		if !slices.Contains(declared, p) {
			return fmt.Errorf("entry %s: template uses {{ %s }} with no cred.%s key", e.Name, p, p)
		}
	}
	for _, p := range declared {
		if !slices.Contains(found, p) {
			return fmt.Errorf("entry %s: cred.%s is unused by the template", e.Name, p)
		}
	}
	return nil
}

// Render substitutes each placeholder with its value, verbatim. A value that
// itself contains a "{{ … }}" sequence is written literally: the template is
// scanned once, before any substitution (spec 042 §2).
func Render(text string, values map[string]string) (string, error) {
	segs, err := scanTemplate(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, s := range segs {
		if !s.placeholder {
			b.WriteString(s.text)
			continue
		}
		v, ok := values[s.text]
		if !ok {
			return "", fmt.Errorf("no value for placeholder %s", s.text)
		}
		b.WriteString(v)
	}
	return b.String(), nil
}

// RenderedDir is the per-worktree directory holding rendered templates. Git
// ignores it and `lode secrets purge` removes it (spec 042 §4.1).
func RenderedDir(worktreeDir string) string {
	return filepath.Join(worktreeDir, ".worklode", "secrets")
}

// RenderEntry renders a materialized templated entry into
// <worktreeDir>/.worklode/secrets/<NAME> at mode 0600 and returns the
// absolute path. The write goes to a temp file and is renamed, so concurrent
// execs never expose a partial file and the path stays stable.
func RenderEntry(worktreeDir string, e ManifestEntry, values map[string]string) (string, error) {
	if !ValidName(e.Name) {
		return "", fmt.Errorf("invalid secret name %q", e.Name)
	}
	text, err := Render(e.Template, values)
	if err != nil {
		return "", fmt.Errorf("render %s: %w", e.Name, err)
	}
	dir := RenderedDir(worktreeDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, e.Name)
	tmp, err := os.CreateTemp(dir, "."+e.Name+".*") // CreateTemp opens at 0600
	if err != nil {
		return "", fmt.Errorf("create %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // no-op once the rename below succeeds
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	return abs, nil
}
