package store

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"text/template"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// DefaultBranchTemplate is the branch name Worklode hands out when
// LODE_BRANCH_TEMPLATE is unset (spec 008 §3).
const DefaultBranchTemplate = "{{ .id }}-{{ .slug }}"

// The branch template and the correlation pattern derived from it are read by
// webhook handlers concurrently, so both live behind one lock and are only
// ever replaced together.
var (
	branchMu      sync.RWMutex
	branchText    = DefaultBranchTemplate
	branchTmpl    = mustTemplate(DefaultBranchTemplate)
	branchPattern = mustPattern(DefaultBranchTemplate)
)

// branchFields are the template's fields. A field's zero value is never
// meaningful, so every render supplies all of them. There is no bare
// ".project" field: a project id, name, and key are three different things,
// and Task only ever carries an id — projectID is the only project-shaped
// field exposed until the store carries more (human ruling, spec 008 §3.1).
type branchFields struct{ id, slug, projectID, kind string }

func (f branchFields) asMap() map[string]string {
	return map[string]string{"id": f.id, "slug": f.slug, "projectId": f.projectID, "kind": f.kind}
}

// sentinels are substituted for the fields when deriving the correlation
// pattern. validateRef rejects a NUL in the sample render, so for a template
// whose literal text is the same regardless of field values, a sentinel
// cannot collide with a literal. A template that branches on field values
// (e.g. "{{ if .kind }}...{{ end }}") could still emit a raw NUL from this
// render that validateRef never saw; that only makes derivePattern produce a
// pattern that matches nothing, not a collision (spec 008 §4).
var sentinels = branchFields{
	id:        "\x00id\x00",
	slug:      "\x00slug\x00",
	projectID: "\x00projectId\x00",
	kind:      "\x00kind\x00",
}

// sampleFields render a representative branch for validation. The values are
// shaped like real ones: ids are uppercase-alpha + "-" + digits, and
// SlugifyTitle only ever emits [a-z0-9-].
var sampleFields = branchFields{id: "WL-1", slug: "sample-title", projectID: "sample", kind: "feature"}

func parseBranchTemplate(text string) (*template.Template, error) {
	return template.New("branch").Option("missingkey=error").Parse(text)
}

func mustTemplate(text string) *template.Template {
	t, err := parseBranchTemplate(text)
	if err != nil {
		panic("store: default branch template is invalid: " + err.Error())
	}
	return t
}

func mustPattern(text string) *regexp.Regexp {
	re, err := derivePattern(mustTemplate(text))
	if err != nil {
		panic("store: default branch template has no derivable pattern: " + err.Error())
	}
	return re
}

func render(t *template.Template, f branchFields) (string, error) {
	var b strings.Builder
	if err := t.Execute(&b, f.asMap()); err != nil {
		return "", err
	}
	return b.String(), nil
}

// derivePattern builds the branch → task-id pattern from the template itself:
// render with sentinels, quote the literal parts, then swap the sentinels for
// the field patterns (spec 008 §4).
func derivePattern(t *template.Template) (*regexp.Regexp, error) {
	out, err := render(t, sentinels)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(out, sentinels.id) {
		return nil, fmt.Errorf("template does not reference .id, so its branches cannot be correlated to a task")
	}
	pat := regexp.QuoteMeta(out)
	pat = strings.ReplaceAll(pat, regexp.QuoteMeta(sentinels.id), `([A-Z][A-Z0-9]*-[0-9]+)`)
	for _, s := range []string{sentinels.slug, sentinels.projectID, sentinels.kind} {
		pat = strings.ReplaceAll(pat, regexp.QuoteMeta(s), `[^/]*`)
	}
	return regexp.Compile("^" + pat + "$")
}

// refBadChars are the characters git refuses in a ref name.
var refBadChars = regexp.MustCompile(`[\x00-\x20\x7f ~^:?*\[\\]`)

// validateRef reports why ref is not a legal git branch name, or nil.
// Mirrors git check-ref-format, which is not shelled out to because the
// server does not otherwise need a git binary (spec 008 §3.2).
func validateRef(ref string) error {
	switch {
	case ref == "":
		return fmt.Errorf("renders to an empty branch name")
	case refBadChars.MatchString(ref):
		return fmt.Errorf("renders %q, which contains a character git forbids in a ref", ref)
	case strings.Contains(ref, ".."):
		return fmt.Errorf("renders %q, which contains %q", ref, "..")
	case strings.Contains(ref, "@{"):
		return fmt.Errorf("renders %q, which contains %q", ref, "@{")
	case strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/"):
		return fmt.Errorf("renders %q, which starts or ends with %q", ref, "/")
	}
	for _, part := range strings.Split(ref, "/") {
		switch {
		case part == "":
			return fmt.Errorf("renders %q, which has an empty path component", ref)
		case strings.HasPrefix(part, "."):
			return fmt.Errorf("renders %q, whose component %q starts with %q", ref, part, ".")
		case strings.HasSuffix(part, "."):
			return fmt.Errorf("renders %q, whose component %q ends with %q", ref, part, ".")
		case strings.HasSuffix(part, ".lock"):
			return fmt.Errorf("renders %q, whose component %q ends with %q", ref, part, ".lock")
		}
	}
	return nil
}

// SetBranchTemplate configures the branch-name template (LODE_BRANCH_TEMPLATE;
// "" means DefaultBranchTemplate) and the correlation pattern derived from it.
// It validates before installing anything, so a rejected template leaves the
// previous one in place. Called once at server start — a bad template is a
// startup failure, not a per-claim one (spec 008 §3.2).
func SetBranchTemplate(text string) error {
	if text == "" {
		text = DefaultBranchTemplate
	}
	tmpl, err := parseBranchTemplate(text)
	if err != nil {
		return fmt.Errorf("branch template %q: %w", text, err)
	}
	sample, err := render(tmpl, sampleFields)
	if err != nil {
		return fmt.Errorf("branch template %q: %w", text, err)
	}
	if err := validateRef(sample); err != nil {
		return fmt.Errorf("branch template %q: %w", text, err)
	}
	pattern, err := derivePattern(tmpl)
	if err != nil {
		return fmt.Errorf("branch template %q: %w", text, err)
	}
	branchMu.Lock()
	defer branchMu.Unlock()
	branchText, branchTmpl, branchPattern = text, tmpl, pattern
	return nil
}

// BranchTemplate returns the configured branch template.
func BranchTemplate() string {
	branchMu.RLock()
	defer branchMu.RUnlock()
	return branchText
}

// BranchFor returns the git branch for a task. SetBranchTemplate has already
// proved the template renders, and every field is supplied, so the render
// cannot fail here; the fallback exists so a branch name is never empty.
//
// .projectId and .kind go through SlugifyTitle before rendering: unlike .id
// (format-constrained by the task-id scheme) and .slug (already slugified),
// projects.id is free text with no ref-safety guarantee, and a project id
// containing e.g. a space or ".." would otherwise render an illegal branch
// that SetBranchTemplate's sample-render validation cannot see coming, since
// it validates a sample, not the real value (spec 008 §3.1).
func BranchFor(t *model.Task) string {
	branchMu.RLock()
	tmpl := branchTmpl
	branchMu.RUnlock()
	f := branchFields{id: t.ID, slug: SlugifyTitle(t.Title), projectID: SlugifyTitle(t.Project), kind: SlugifyTitle(t.Kind)}
	out, err := render(tmpl, f)
	if err != nil || out == "" {
		return t.ID + "-" + SlugifyTitle(t.Title)
	}
	return out
}

// TaskIDFromRef extracts a task id from a branch name, using the pattern
// derived from the configured template. It returns "" if ref does not match —
// including when the id part is lowercase, since task-id prefixes are always
// uppercase (e.g. WL-, SW-). A shape match is not proof the task exists;
// callers gate on taskExists before writing a binding.
func TaskIDFromRef(ref string) string {
	branchMu.RLock()
	defer branchMu.RUnlock()
	m := branchPattern.FindStringSubmatch(ref)
	if m == nil {
		return ""
	}
	return m[1]
}
