package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestGetProjectGraph(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Graphed", "priority": "low", "kind": "chore",
	})
	unlinked := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 1, Slug: "001-thing",
		Body: "---\nstatus: draft\n---\n\n# Thing\n\n## 0. A {#sec-0}\n\nA.\n",
	})
	_ = unlinked // unlinked: must be absent

	rr := doReq(t, h, "GET", "/api/v1/projects/proj/graph", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var g model.ProjectGraph
	if err := json.Unmarshal(rr.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if g.Project != "proj" || len(g.Tasks) != 1 || g.Tasks[0].ID != task["id"] {
		t.Errorf("graph = %+v", g)
	}
	if len(g.Docs) != 0 {
		t.Errorf("unlinked doc must be absent, got %+v", g.Docs)
	}
	if g.DocEdges == nil || g.Links == nil || g.TaskEdges == nil {
		t.Errorf("slices must serialize as [] not null: %s", rr.Body.String())
	}

	linked := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 2, Slug: "002-written",
		GeneratedByTask: task["id"].(string),
		Body:            "---\nstatus: draft\n---\n\n# Written\n\n## 0. A {#sec-0}\n\nA.\n",
	})

	rr = doReq(t, h, "GET", "/api/v1/projects/proj/graph", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	g = model.ProjectGraph{}
	if err := json.Unmarshal(rr.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Docs) != 1 || g.Docs[0].ID != linked.ID {
		t.Fatalf("docs = %+v, want just %d", g.Docs, linked.ID)
	}
	if g.Docs[0].Ref == "" || !strings.HasPrefix(g.Docs[0].Ref, "WL") {
		t.Errorf("Ref = %q, want prefixed WL (createProject's key)", g.Docs[0].Ref)
	}
	if g.Docs[0].ProjectKey == "" {
		t.Errorf("ProjectKey not stamped")
	}
	if g.Docs[0].Body != "" {
		t.Errorf("Body must be cleared, got %q", g.Docs[0].Body)
	}

	if rr := doReq(t, h, "GET", "/api/v1/projects/nope/graph", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown project: status = %d", rr.Code)
	}
}
