package derive_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
)

// fakeRepoReader serves manifests and PR file lists from maps, counting
// FileAt calls per repo so tests can assert the manifest is fetched at most
// once per repo regardless of how many PRs land in it.
type fakeRepoReader struct {
	manifests   map[string]string   // repo → components.yaml
	files       map[string][]string // "repo#number" → changed paths
	fileAtCalls map[string]int
}

func (f *fakeRepoReader) FileAt(_ context.Context, repo, path string) ([]byte, error) {
	if path != ".worklode/components.yaml" {
		return nil, errors.New("unexpected path " + path)
	}
	if f.fileAtCalls == nil {
		f.fileAtCalls = map[string]int{}
	}
	f.fileAtCalls[repo]++
	m, ok := f.manifests[repo]
	if !ok {
		return nil, derive.ErrNotFound
	}
	return []byte(m), nil
}

func (f *fakeRepoReader) PRFiles(_ context.Context, repo string, number int64) ([]string, error) {
	return f.files[repoNum(repo, number)], nil
}

func repoNum(repo string, n int64) string { return fmt.Sprintf("%s#%d", repo, n) }

func TestPRAffectsTriples(t *testing.T) {
	prs := []derive.PRRef{
		{Repo: "sunstoneinstitute/research-stack", Number: 1, TaskID: "WL-7"},
		{Repo: "sunstoneinstitute/unmapped", Number: 2, TaskID: "WL-8"},
	}
	rr := &fakeRepoReader{
		manifests: map[string]string{"sunstoneinstitute/research-stack": importsManifest},
		files: map[string][]string{
			repoNum("sunstoneinstitute/research-stack", 1): {
				"internal/ingest/x.go", "internal/graph/y.go", "README.md",
			},
		},
	}
	doc, skipped, err := derive.PRAffectsTriples(context.Background(), prs, rr)
	if err != nil {
		t.Fatalf("PRAffectsTriples: %v", err)
	}
	got := string(doc)
	want := "<https://worklode.io/ns/id/task/WL-7> <https://worklode.io/ns/ontology#affects> " +
		"<https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/graphsrv> .\n" +
		"<https://worklode.io/ns/id/task/WL-7> <https://worklode.io/ns/ontology#affects> " +
		"<https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest> .\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant exactly:\n%s", got, want)
	}
	if strings.Contains(got, "WL-8") {
		t.Errorf("PR in a manifest-less repo produced triples:\n%s", got)
	}
	if len(skipped) != 1 || skipped[0] != "sunstoneinstitute/unmapped" {
		t.Fatalf("skipped = %v; want the manifest-less repo reported", skipped)
	}
}

// TestPRAffectsTriplesSkipsManifestlessRepoOnce guards the loop's
// skipped-repo cache: a second PR in a repo already known to lack a
// manifest must not re-fetch it or double-report the repo as skipped.
func TestPRAffectsTriplesSkipsManifestlessRepoOnce(t *testing.T) {
	prs := []derive.PRRef{
		{Repo: "sunstoneinstitute/unmapped", Number: 1, TaskID: "WL-1"},
		{Repo: "sunstoneinstitute/unmapped", Number: 2, TaskID: "WL-2"},
	}
	rr := &fakeRepoReader{manifests: map[string]string{}}
	doc, skipped, err := derive.PRAffectsTriples(context.Background(), prs, rr)
	if err != nil {
		t.Fatalf("PRAffectsTriples: %v", err)
	}
	if len(doc) != 0 {
		t.Fatalf("doc = %q, want empty", doc)
	}
	if len(skipped) != 1 || skipped[0] != "sunstoneinstitute/unmapped" {
		t.Fatalf("skipped = %v, want the repo reported exactly once", skipped)
	}
	if got := rr.fileAtCalls["sunstoneinstitute/unmapped"]; got != 1 {
		t.Fatalf("FileAt calls for the manifest-less repo = %d, want 1", got)
	}
}

// TestPRAffectsTriplesSortsSkippedRepos pins the sort on skippedRepos: the
// set is built by ranging a map, so with two or more entries the report is
// order-dependent unless it is sorted. The PRs are fed in reverse
// alphabetical order so an unsorted result would come out wrong at least
// sometimes.
func TestPRAffectsTriplesSortsSkippedRepos(t *testing.T) {
	prs := []derive.PRRef{
		{Repo: "sunstoneinstitute/zeta", Number: 1, TaskID: "WL-1"},
		{Repo: "sunstoneinstitute/alpha", Number: 2, TaskID: "WL-2"},
		{Repo: "sunstoneinstitute/mid", Number: 3, TaskID: "WL-3"},
	}
	rr := &fakeRepoReader{manifests: map[string]string{}}
	_, skipped, err := derive.PRAffectsTriples(context.Background(), prs, rr)
	if err != nil {
		t.Fatalf("PRAffectsTriples: %v", err)
	}
	want := []string{"sunstoneinstitute/alpha", "sunstoneinstitute/mid", "sunstoneinstitute/zeta"}
	if !slices.Equal(skipped, want) {
		t.Fatalf("skipped = %v, want %v in sorted order", skipped, want)
	}
}

// TestPRAffectsTriplesFetchesManifestOnce guards the loop's manifest cache:
// a second PR in an already-fetched repo must reuse the cached manifest
// rather than fetching it again.
func TestPRAffectsTriplesFetchesManifestOnce(t *testing.T) {
	prs := []derive.PRRef{
		{Repo: "sunstoneinstitute/research-stack", Number: 1, TaskID: "WL-7"},
		{Repo: "sunstoneinstitute/research-stack", Number: 2, TaskID: "WL-9"},
	}
	rr := &fakeRepoReader{
		manifests: map[string]string{"sunstoneinstitute/research-stack": importsManifest},
		files: map[string][]string{
			repoNum("sunstoneinstitute/research-stack", 1): {"internal/ingest/x.go"},
			repoNum("sunstoneinstitute/research-stack", 2): {"internal/graph/y.go"},
		},
	}
	doc, _, err := derive.PRAffectsTriples(context.Background(), prs, rr)
	if err != nil {
		t.Fatalf("PRAffectsTriples: %v", err)
	}
	got := string(doc)
	if !strings.Contains(got, "WL-7") || !strings.Contains(got, "WL-9") {
		t.Fatalf("doc missing an expected task edge:\n%s", got)
	}
	if calls := rr.fileAtCalls["sunstoneinstitute/research-stack"]; calls != 1 {
		t.Fatalf("FileAt calls for the manifested repo = %d, want 1 (cached for the second PR)", calls)
	}
}
