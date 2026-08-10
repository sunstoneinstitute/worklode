package designdoc

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// newResolveCorpus writes a small fake corpus: a decoy that shares 014's
// leading digits under zero-padding, and one kind: adr document so the
// shorthand's kind check has something to enforce against.
func newResolveCorpus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, frontmatter string) {
		content := frontmatter + "# Title\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("004-execution.md", "---\nstatus: draft\n---\n")
	write("014-design-documents-as-graph-objects.md", "---\nstatus: draft\n---\n")
	write("0140-decoy.md", "---\nstatus: draft\n---\n")
	write("017-some-adr.md", "---\nstatus: draft\nkind: adr\n---\n")
	return dir
}

func TestResolveRefForms(t *testing.T) {
	dir := newResolveCorpus(t)
	specPath := filepath.Join(dir, "014-design-documents-as-graph-objects.md")
	want, err := filepath.Abs(specPath)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		ref  string
	}{
		{"path", specPath},
		{"bare filename", "014-design-documents-as-graph-objects.md"},
		{"number no padding", "14"},
		{"number padded", "014"},
		{"number with filename text", "014-design-documents"},
		{"shorthand", "WL-SPEC-14"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveRef(dir, "WL", tt.ref)
			if err != nil {
				t.Fatalf("ResolveRef(%q): %v", tt.ref, err)
			}
			if got.Path != want {
				t.Errorf("Path = %q, want %q", got.Path, want)
			}
			if got.Section != "" {
				t.Errorf("Section = %q, want empty", got.Section)
			}
		})
	}
}

func TestResolveRefZeroPaddingDoesNotCollide(t *testing.T) {
	dir := newResolveCorpus(t)

	got, err := ResolveRef(dir, "WL", "WL-SPEC-4")
	if err != nil {
		t.Fatalf("WL-SPEC-4: %v", err)
	}
	want, err := filepath.Abs(filepath.Join(dir, "004-execution.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != want {
		t.Errorf("WL-SPEC-4 Path = %q, want %q (zero-padding must normalise)", got.Path, want)
	}

	got, err = ResolveRef(dir, "WL", "014")
	if err != nil {
		t.Fatalf("014: %v", err)
	}
	if filepath.Base(got.Path) != "014-design-documents-as-graph-objects.md" {
		t.Errorf("014 Path = %q, must not match the 0140 decoy", got.Path)
	}
}

func TestResolveRefFragment(t *testing.T) {
	dir := newResolveCorpus(t)

	got, err := ResolveRef(dir, "WL", "WL-SPEC-14#sec-2.1")
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if got.Section != "sec-2.1" {
		t.Errorf("Section = %q, want sec-2.1", got.Section)
	}
	if filepath.Base(got.Path) != "014-design-documents-as-graph-objects.md" {
		t.Errorf("Path = %q", got.Path)
	}
}

func TestResolveRefAmbiguous(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"004-execution.md", "004-other.md"} {
		content := []byte("---\nstatus: draft\n---\n# Title\n")
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	_, err := ResolveRef(dir, "WL", "4")
	var ambErr *AmbiguousRefError
	if !errors.As(err, &ambErr) {
		t.Fatalf("err = %v, want *AmbiguousRefError", err)
	}
	if ambErr.Ref != "4" {
		t.Errorf("Ref = %q, want 4", ambErr.Ref)
	}
	want := []string{"004-execution.md", "004-other.md"}
	if !reflect.DeepEqual(ambErr.Candidates, want) {
		t.Errorf("Candidates = %v, want %v", ambErr.Candidates, want)
	}
}

func TestResolveRefUnresolvedForeignKey(t *testing.T) {
	dir := newResolveCorpus(t)

	_, err := ResolveRef(dir, "WL", "CMS-SPEC-4")
	var unresolved *UnresolvedError
	if !errors.As(err, &unresolved) {
		t.Fatalf("err = %v, want *UnresolvedError", err)
	}
	if unresolved.Key != "CMS" {
		t.Errorf("Key = %q, want CMS", unresolved.Key)
	}
	if got, want := err.Error(), "unresolved: project CMS not known here"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestResolveRefUnresolvedWhenProjectKeyEmpty(t *testing.T) {
	dir := newResolveCorpus(t)

	_, err := ResolveRef(dir, "", "WL-SPEC-14")
	var unresolved *UnresolvedError
	if !errors.As(err, &unresolved) {
		t.Fatalf("err = %v, want *UnresolvedError", err)
	}
	if unresolved.Key != "WL" {
		t.Errorf("Key = %q, want WL", unresolved.Key)
	}
}

func TestResolveRefTier1Miss(t *testing.T) {
	dir := newResolveCorpus(t)

	_, err := ResolveRef(dir, "WL", "WL-SPEC-99")
	if err == nil {
		t.Fatal("want an error")
	}
	var amb *AmbiguousRefError
	var unresolved *UnresolvedError
	var mismatch *KindMismatchError
	if errors.As(err, &amb) || errors.As(err, &unresolved) || errors.As(err, &mismatch) || errors.Is(err, ErrNoSpec) {
		t.Fatalf("err = %v (%T), want a plain tier-1-miss error", err, err)
	}
}

func TestResolveRefKindMismatch(t *testing.T) {
	dir := newResolveCorpus(t)

	_, err := ResolveRef(dir, "WL", "WL-ADR-14")
	var mismatch *KindMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("WL-ADR-14: err = %v, want *KindMismatchError", err)
	}
	if mismatch.Want != "adr" || mismatch.Got != "spec" {
		t.Errorf("WL-ADR-14 mismatch = %+v, want {adr spec}", mismatch)
	}

	got, err := ResolveRef(dir, "WL", "WL-ADR-17")
	if err != nil {
		t.Fatalf("WL-ADR-17: %v", err)
	}
	if filepath.Base(got.Path) != "017-some-adr.md" {
		t.Errorf("WL-ADR-17 Path = %q", got.Path)
	}

	_, err = ResolveRef(dir, "WL", "WL-SPEC-17")
	if !errors.As(err, &mismatch) {
		t.Fatalf("WL-SPEC-17: err = %v, want *KindMismatchError", err)
	}
	if mismatch.Want != "spec" || mismatch.Got != "adr" {
		t.Errorf("WL-SPEC-17 mismatch = %+v, want {spec adr}", mismatch)
	}
}

func TestKindMismatchErrorMessage(t *testing.T) {
	adrPath := filepath.Join("docs", "specs", "007-drift-and-overview.md")

	err := &KindMismatchError{Path: adrPath, Want: "adr", Got: "spec"}
	want := adrPath + ": ref names an ADR, document is a spec"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	err = &KindMismatchError{Path: adrPath, Want: "spec", Got: "adr"}
	want = adrPath + ": ref names a spec, document is an ADR"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestResolveRefNoSpec(t *testing.T) {
	dir := newResolveCorpus(t)

	for _, ref := range []string{"NO-SPEC", "WL-SPEC-0"} {
		_, err := ResolveRef(dir, "WL", ref)
		if !errors.Is(err, ErrNoSpec) {
			t.Errorf("ref %q: err = %v, want ErrNoSpec", ref, err)
		}
	}
}

func TestFindCorpus(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worklode"), 0o755); err != nil {
		t.Fatalf("mkdir .worklode: %v", err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	want, err := filepath.Abs(filepath.Join(root, "docs", "specs"))
	if err != nil {
		t.Fatal(err)
	}
	if got := FindCorpus(nested); got != want {
		t.Errorf("FindCorpus(nested) = %q, want %q", got, want)
	}
}

func TestFindCorpusNoRepoRoot(t *testing.T) {
	dir := t.TempDir()
	if got := FindCorpus(dir); got != "" {
		t.Errorf("FindCorpus = %q, want empty", got)
	}
}

func TestFindRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worklode"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := FindRepoRoot(nested); got != root {
		t.Errorf("FindRepoRoot(%q) = %q, want %q", nested, got, root)
	}
	if got := FindRepoRoot(t.TempDir()); got != "" {
		t.Errorf("FindRepoRoot outside a repo = %q, want \"\"", got)
	}
	// FindCorpus stays the conventional-default wrapper.
	if got, want := FindCorpus(nested), filepath.Join(root, "docs", "specs"); got != want {
		t.Errorf("FindCorpus = %q, want %q", got, want)
	}
}
