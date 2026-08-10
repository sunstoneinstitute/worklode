//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestDocSyncRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})

	if _, _, err := admin.CreateProject(ctx, cli.CreateProjectInput{
		ID: "docsync", Name: "Doc Sync", Key: "DS"}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	spec := cli.DocUpsert{
		Kind: "spec", Ordinal: "34", Status: "accepted",
		Title:       "Spec 034 — Design-doc sync",
		Body:        "---\nstatus: accepted\n---\n# Spec 034 — Design-doc sync\n\n## 1. Scope {#sec-1}\n\nBody.\n",
		Frontmatter: json.RawMessage(`{"status":"accepted"}`),
		Sections:    []cli.DocSection{{Anchor: "sec-1", Heading: "Scope", Depth: 2}},
	}
	plan := cli.DocUpsert{
		Kind: "plan", Ordinal: "34-1", Status: "draft", Title: "Part 1",
		Body:        "---\nstatus: draft\nimplements: docs/specs/034-design-doc-sync.md\n---\n# Part 1\n",
		Frontmatter: json.RawMessage(`{"status":"draft"}`),
		Edges:       []cli.DocEdge{{Rel: "implements", Target: "docs/specs/034-design-doc-sync.md"}},
	}
	input := cli.DocSyncInput{Project: "docsync", SourceBranch: "main", Docs: []cli.DocUpsert{spec, plan}}

	// First sync: everything added (034 §12.2).
	rep, _, err := admin.SyncDocs(ctx, input)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if rep.Added != 2 || rep.Updated != 0 || rep.Unchanged != 0 {
		t.Fatalf("first sync = %+v, want 2 added", rep)
	}

	// Second sync: no changes (034 §12.2).
	if rep, _, err = admin.SyncDocs(ctx, input); err != nil || rep.Unchanged != 2 {
		t.Fatalf("second sync = %+v, %v; want 2 unchanged", rep, err)
	}

	// Dry run of a change: reported, not written (034 §12.4).
	changed := input
	changed.DryRun = true
	changed.Docs = append([]cli.DocUpsert{}, spec, plan)
	changed.Docs[0].Body += "\nmore\n"
	if rep, _, err = admin.SyncDocs(ctx, changed); err != nil || !rep.DryRun || rep.Updated != 1 {
		t.Fatalf("dry run = %+v, %v; want 1 updated", rep, err)
	}
	d, _, err := admin.GetDoc(ctx, "DS-SPEC-34")
	if err != nil || d.Version != 1 {
		t.Fatalf("after dry run: version = %d, %v; want 1 (nothing written)", d.Version, err)
	}

	// Forced sync records provenance (034 §12.3's server half).
	forced := input
	forced.SourceBranch, forced.Dirty, forced.Force = "feature-x", true, true
	if _, _, err = admin.SyncDocs(ctx, forced); err != nil {
		t.Fatalf("forced sync: %v", err)
	}
	if d, _, err = admin.GetDoc(ctx, "DS-SPEC-34"); err != nil ||
		d.SourceBranch != "feature-x" || !d.SourceDirty {
		t.Fatalf("forced provenance = %q/%v, %v; want feature-x/true", d.SourceBranch, d.SourceDirty, err)
	}

	// List returns the derived ids from the store (034 §12.5).
	list, _, err := admin.ListDocs(ctx, "docsync", "", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids := map[string]bool{}
	for _, doc := range list.Docs {
		ids[doc.ID] = true
	}
	if !ids["DS-SPEC-34"] || !ids["DS-PLAN-34-1"] {
		t.Fatalf("list ids = %v, want DS-SPEC-34 and DS-PLAN-34-1", ids)
	}
}
