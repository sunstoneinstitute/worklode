package designdoc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ResolvedRef is a corpus document a ref resolved to.
type ResolvedRef struct {
	Path    string // absolute path to the document
	Section string // "sec-2.1" when the ref carried a #sec- fragment, else ""
}

// FindRepoRoot walks up from dir to the nearest directory containing a
// ".worklode" directory — the repo root the corpus config is relative to
// (spec 034 §2). Returns "" when no repo root is found.
func FindRepoRoot(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if st, err := os.Stat(filepath.Join(d, ".worklode")); err == nil && st.IsDir() {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// FindCorpus returns the conventional spec corpus, docs/specs under the repo
// root — the default a repo without a spec_corpus key gets (034 §2). Returns
// "" when no repo root is found.
func FindCorpus(dir string) string {
	root := FindRepoRoot(dir)
	if root == "" {
		return ""
	}
	return filepath.Join(root, "docs", "specs")
}

// AmbiguousRefError reports a ref that matched more than one corpus document.
// Error lists every candidate filename, one per line, so a caller printing it
// as-is gives the reader enough to disambiguate.
type AmbiguousRefError struct {
	Ref        string
	Candidates []string
}

func (e *AmbiguousRefError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ambiguous ref %q:", e.Ref)
	for _, c := range e.Candidates {
		b.WriteByte('\n')
		b.WriteString(c)
	}
	return b.String()
}

// UnresolvedError reports a shorthand ref naming a project this checkout has
// no way to reach (026 §4.2 tier 3). It is deliberately not a defect: nothing
// in the referring repository can repair it.
type UnresolvedError struct {
	Key string
}

func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("unresolved: project %s not known here", e.Key)
}

// KindMismatchError reports a shorthand ref whose <TYPE> token names one
// document kind (spec or adr) while the target's frontmatter declares the
// other (026 §4.2).
type KindMismatchError struct {
	Path string
	Want string // the kind the ref's <TYPE> token asked for
	Got  string // the kind the document's frontmatter declares
}

func (e *KindMismatchError) Error() string {
	return fmt.Sprintf("%s: ref names %s, document is %s", e.Path, kindArticle(e.Want), kindArticle(e.Got))
}

// kindArticle renders a checkKind kind ("adr" or "spec") with its article and
// display casing: "an ADR" or "a spec".
func kindArticle(kind string) string {
	if kind == "adr" {
		return "an ADR"
	}
	return "a " + kind
}

// ErrNoSpec is returned for the NO-SPEC sentinel ref, or its equivalent
// <KEY>-SPEC-0 (026 §4.2a): the ref explicitly means "no governing spec",
// never a document, so the tier table of §4.2 never runs.
var ErrNoSpec = errors.New("no governing spec")

// noSpecError wraps ErrNoSpec with the ref that triggered it, so a caller
// printing the error as-is gets a self-explanatory message rather than the
// bare sentinel text. errors.Is(err, ErrNoSpec) still holds through the
// wrap.
func noSpecError(ref string) error {
	return fmt.Errorf("%s is the no-governing-spec sentinel (026 §4.2a), not a document: %w", ref, ErrNoSpec)
}

// numberFormPattern recognizes ref form 2 (026 §3): a bare spec number, with
// or without zero-padding, optionally followed by more of the filename
// ("14", "014", "014-design-documents").
var numberFormPattern = regexp.MustCompile(`^(\d+)(-.*)?$`)

// leadingNumberPattern extracts a corpus filename's leading spec number
// ("014-design-documents...md" -> "014").
var leadingNumberPattern = regexp.MustCompile(`^(\d+)-`)

// shorthandPattern is 014 §11.3's <KEY>-<TYPE>-<n> grammar, fragment already
// split off by splitFragment.
var shorthandPattern = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-(SPEC|ADR)-(\d+)$`)

// ResolveRef resolves ref against the corpus at corpusDir. projectKey is
// the current repo's key ("WL"), or "" when unknown.
//
// Ref forms are tried in the order spec 026 §3 documents: a path, then a spec
// number or filename prefix, then 014 §11.3's <KEY>-<TYPE>-<n> shorthand. A
// path that names no existing file falls through to the later forms rather
// than failing outright, which is how a bare filename like
// "014-foo.md" is matched inside the corpus.
func ResolveRef(corpusDir, projectKey, ref string) (ResolvedRef, error) {
	base, section := splitFragment(ref)

	// Form 1: path.
	if looksLikePath(base) {
		if p, ok := resolveExistingPath(corpusDir, base); ok {
			return ResolvedRef{Path: p, Section: section}, nil
		}
	}

	files, err := corpusFilenames(corpusDir)
	if err != nil {
		return ResolvedRef{}, err
	}

	// Form 2: spec number / filename prefix.
	if m := numberFormPattern.FindStringSubmatch(base); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return ResolvedRef{}, fmt.Errorf("ref %q: %w", ref, err)
		}
		if n == 0 {
			return ResolvedRef{}, noSpecError(ref)
		}
		matches := matchByNumber(files, n)
		if strings.Contains(base, "-") {
			matches = append(matches, matchByPrefix(files, base)...)
		}
		return finish(corpusDir, ref, matches, section)
	}

	// Form 3: shorthand.
	if base == "NO-SPEC" {
		return ResolvedRef{}, noSpecError(ref)
	}
	if m := shorthandPattern.FindStringSubmatch(base); m != nil {
		key, typ, numStr := m[1], m[2], m[3]
		num, err := strconv.Atoi(numStr)
		if err != nil {
			return ResolvedRef{}, fmt.Errorf("ref %q: %w", ref, err)
		}
		if typ == "SPEC" && num == 0 {
			return ResolvedRef{}, noSpecError(ref)
		}
		if projectKey == "" || key != projectKey {
			return ResolvedRef{}, &UnresolvedError{Key: key}
		}
		resolved, err := finish(corpusDir, ref, matchByNumber(files, num), section)
		if err != nil {
			return ResolvedRef{}, err
		}
		if err := CheckKind(resolved.Path, typ); err != nil {
			return ResolvedRef{}, err
		}
		return resolved, nil
	}

	return ResolvedRef{}, fmt.Errorf("ref %q not found in corpus %s", ref, corpusDir)
}

// splitFragment separates a trailing "#sec-..." fragment from ref, per 026
// §4's "narrows any of them to an anchor". base is ref with the fragment (and
// its '#') removed; section is the fragment with the '#' stripped, or "" when
// ref carried none.
func splitFragment(ref string) (base, section string) {
	base, section, found := strings.Cut(ref, "#")
	if !found {
		return ref, ""
	}
	return base, section
}

// looksLikePath reports whether base is shaped like ref form 1: it names a
// directory somewhere (contains '/') or names a markdown file directly.
func looksLikePath(base string) bool {
	return strings.Contains(base, "/") || strings.HasSuffix(base, ".md")
}

// resolveExistingPath checks base as given, then relative to corpusDir, and
// returns the first that names an existing file.
func resolveExistingPath(corpusDir, base string) (string, bool) {
	for _, candidate := range []string{base, filepath.Join(corpusDir, base)} {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(candidate); err == nil {
				return abs, true
			}
		}
	}
	return "", false
}

// corpusFilenames lists the corpus's document filenames (docs/specs/*.md),
// sorted for a deterministic AmbiguousRefError.
func corpusFilenames(corpusDir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(corpusDir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("list corpus %s: %w", corpusDir, err)
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = filepath.Base(m)
	}
	sort.Strings(names)
	return names, nil
}

// leadingNumber parses a corpus filename's leading spec number, ignoring any
// zero-padding — "0140-decoy.md" is 140, distinct from "014-x.md"'s 14.
func leadingNumber(filename string) (int, bool) {
	m := leadingNumberPattern.FindStringSubmatch(filename)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// matchByNumber returns every corpus filename whose leading number equals n.
func matchByNumber(files []string, n int) []string {
	var matches []string
	for _, f := range files {
		if ln, ok := leadingNumber(f); ok && ln == n {
			matches = append(matches, f)
		}
	}
	return matches
}

// matchByPrefix returns every corpus filename that starts with ref.
func matchByPrefix(files []string, ref string) []string {
	var matches []string
	for _, f := range files {
		if strings.HasPrefix(f, ref) {
			matches = append(matches, f)
		}
	}
	return matches
}

// finish turns a form's candidate filenames into a result: none is a
// tier-1 miss (a plain, named error), more than one is ambiguous, and
// exactly one resolves.
func finish(corpusDir, ref string, matches []string, section string) (ResolvedRef, error) {
	matches = uniqueSorted(matches)
	switch len(matches) {
	case 0:
		return ResolvedRef{}, fmt.Errorf("ref %q not found in corpus %s", ref, corpusDir)
	case 1:
		abs, err := filepath.Abs(filepath.Join(corpusDir, matches[0]))
		if err != nil {
			abs = filepath.Join(corpusDir, matches[0])
		}
		return ResolvedRef{Path: abs, Section: section}, nil
	default:
		return ResolvedRef{}, &AmbiguousRefError{Ref: ref, Candidates: matches}
	}
}

// uniqueSorted dedupes and sorts in, so a match found through more than one
// criterion (leading number and filename prefix) is not reported twice.
func uniqueSorted(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// CheckKind enforces 014 §11.3's <TYPE> token ("SPEC" or "ADR") against the
// target document's frontmatter: a document is an ADR iff its frontmatter
// carries kind: adr (026 §4.2), no other file needing the key. ResolveRef's
// own shorthand form (below) calls this; it is also exported so a caller
// that resolved a ref through a form ResolveRef never kind-checks — the
// bare-number form, form 2, which a <KEY>-less --spec/--adr flag falls back
// to — can still enforce the kind it asked for, without a second
// implementation of the mismatch check.
func CheckKind(path, typ string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	doc, err := Parse(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	isADR := doc.Frontmatter != nil && doc.Frontmatter.Kind == "adr"
	switch {
	case typ == "ADR" && !isADR:
		return &KindMismatchError{Path: path, Want: "adr", Got: "spec"}
	case typ == "SPEC" && isADR:
		return &KindMismatchError{Path: path, Want: "spec", Got: "adr"}
	}
	return nil
}
