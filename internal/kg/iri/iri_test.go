package iri_test

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
)

func TestGrammar(t *testing.T) {
	const base = "https://worklode.io/ns/"
	cases := []struct{ name, got, want string }{
		{"term", iri.Term("Task"), base + "ontology#Task"},
		{"concept", iri.Concept("feature"), base + "concept/feature"},
		{"task", iri.Task("WL-7"), base + "id/task/WL-7"},
		{"project", iri.Project("worklode"), base + "id/project/worklode"},
		{"project graph", iri.ProjectGraph("worklode"), base + "graph/project/worklode"},
		{"agent", iri.Agent("stig"), base + "id/agent/stig"},
		{"component slug with slashes", iri.Component("github.com/sunstoneinstitute/worklode"),
			base + "id/component/github.com/sunstoneinstitute/worklode"},
		{"doc", iri.Doc("spec-worklode-006"), base + "id/doc/spec-worklode-006"},
		{"deliverable", iri.Deliverable("WL-DEL-1"), base + "id/deliverable/WL-DEL-1"},
		{"issue", iri.Issue("github.com", "sunstoneinstitute", "worklode", 42),
			base + "id/issue/github.com/sunstoneinstitute/worklode/42"},
		{"pr", iri.PR("github.com", "sunstoneinstitute", "worklode", 42),
			base + "id/pr/github.com/sunstoneinstitute/worklode/42"},
		{"artifact kind-first", iri.Artifact("docker_image", "ghcr.io/sunstoneinstitute/graph-server", "v1"),
			base + "id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1"},
		{"deployment", iri.Deployment("prod", "flux_kustomization", "graph-server"),
			base + "id/deployment/prod/flux_kustomization/graph-server"},
		{"environment", iri.Environment("prod"), base + "id/environment/prod"},
		{"commit", iri.Commit("github.com", "sunstoneinstitute", "worklode", "a16c2a7"),
			base + "id/commit/github.com/sunstoneinstitute/worklode/a16c2a7"},
		{"declared graph", iri.DeclaredGraph("adr-worklode-0007"),
			base + "graph/declared/adr-worklode-0007"},
		{"observed graph (org-global source)", iri.ObservedGraph("deploy"),
			base + "graph/observed/deploy"},
		{"observed graph (repo-local source)",
			iri.RepoObservedGraph("go-imports", "github.com", "sunstoneinstitute", "worklode"),
			base + "graph/observed/go-imports/github.com/sunstoneinstitute/worklode"},
		{"repo", iri.Repo("github.com", "sunstoneinstitute", "worklode"),
			base + "id/repo/github.com/sunstoneinstitute/worklode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q; want %q", tc.got, tc.want)
			}
		})
	}
}

// The namespace roots are exported untyped constants: downstream packages
// build prefixes from them (drift-3's iri.IDNS + "task/").
const _ = iri.IDNS + "task/"
