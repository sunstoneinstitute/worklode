package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// repairSpec arranges WL-REQ-1..3; WL-REQ-3's body refines WL-REQ-1.
const repairSpec = "---\nstatus: draft\n---\n# T\n\nIntro.\n\n## 1. One {#sec-1}\n\nA.\n\n### 1.1 Sub {#sec-1.1}\n\nB.\n\n## 2. Two {#sec-2}\n\nThis refines WL-REQ-1.\n"

const repairPlan = "---\nstatus: draft\n---\n# %s\n\n%s\n\n## Tasks\n\n### Task 1 — Do it\n\n```yaml\nkind: chore\n```\n\nText.\n"

// seedReferencesRow records that a classifier read from -> to as a plain
// reference, the row a person's typed write turns missed.
func seedReferencesRow(t *testing.T, st *store.Store, fromKind, fromID, toKind, toRef string) {
	t.Helper()
	if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO ref_classifications
			   (from_kind, from_id, field, line, line_text, to_kind, to_id, classifier, classifier_version,
			    model, predicate, confidence, created_at)
			 VALUES ($1, $2, 'body', 1, 'x', $3,
			         CASE WHEN $3 = 'rule' THEN (SELECT r.id::text FROM rules r JOIN projects p ON p.id = r.project_id
			                                     WHERE p.key || '-REQ-' || r.number = $4) ELSE $4 END,
			         'jev', 1, 'm', 'references', 0.9, now())`, fromKind, fromID, toKind, toRef)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// TestRepairsSetOutcomes: each person's typed edge writer and remover records
// its outcome on the classifier's verdict for the pair, by the caller, with a
// wl:ClassificationRepaired event informed by the writer's event, and leaves
// no edge carrying that classification (WL-REQ-1858, WL-REQ-1856).
func TestRepairsSetOutcomes(t *testing.T) {
	t.Parallel()
	to := func(s string) *string { return &s }
	cases := []struct {
		name         string
		body         string // WL-2's body; WL-1 is the target task
		missed       [2]string
		method, path string
		req          any
		outcome      string
		adjustedTo   string
	}{
		{name: "task block", missed: [2]string{"task", "WL-1"}, method: "POST", path: "/api/v1/tasks/WL-2/edges",
			req: model.EdgeInput{To: to("WL-1"), Type: "dependsOn"}, outcome: "missed"},
		{name: "task unblock", body: "Depends on WL-1.\n", method: "DELETE", path: "/api/v1/tasks/WL-2/edges",
			req: model.EdgeInput{To: to("WL-1"), Type: "dependsOn"}, outcome: "rejected"},
		{name: "task parent", body: "Depends on WL-1.\n", method: "POST", path: "/api/v1/tasks/WL-2/edges",
			req: model.EdgeInput{To: to("WL-1"), Type: "childOf"}, outcome: "adjusted", adjustedTo: "childOf"},
		{name: "task duplicate", body: "Depends on WL-1.\n", method: "POST", path: "/api/v1/tasks/WL-2/edges",
			req: model.EdgeInput{To: to("WL-1"), Type: "duplicateOf"}, outcome: "adjusted", adjustedTo: "duplicateOf"},
		{name: "follow-up write", method: "POST", path: "/api/v1/tasks",
			req: map[string]any{"project": "wl", "title": "F", "kind": "feature", "priority": "medium",
				"body": "Depends on WL-1.\n", "follow_up_to": "WL-1"},
			outcome: "adjusted", adjustedTo: "followUpTo"},
		{name: "task govern", missed: [2]string{"rule", "WL-REQ-1"}, method: "POST", path: "/api/v1/tasks/WL-2/governed-by",
			req: map[string]any{"rule": "WL-REQ-1"}, outcome: "missed"},
		{name: "task ungovern", body: "Governed by WL-REQ-1.\n", method: "DELETE", path: "/api/v1/tasks/WL-2/governed-by",
			req: map[string]any{"rule": "WL-REQ-1"}, outcome: "rejected"},
		{name: "rule link", method: "POST", path: "/api/v1/rules/WL-REQ-3/edges",
			req: model.RuleEdgeInput{Type: "needs", To: "WL-REQ-1"}, outcome: "adjusted", adjustedTo: "needs"},
		{name: "rule unlink", method: "DELETE", path: "/api/v1/rules/WL-REQ-3/edges",
			req: model.RuleEdgeInput{Type: "refines", To: "WL-REQ-1"}, outcome: "rejected"},
		{name: "doc link", method: "POST", path: "doc-a",
			req: model.DocEdgeInput{Type: "requires", To: "b"}, outcome: "adjusted", adjustedTo: "requires"},
		{name: "doc unlink", method: "DELETE", path: "doc-a",
			req: model.DocEdgeInput{Type: "blockedBy", To: "b"}, outcome: "rejected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st, h, token := newTestServer(t)
			projID := seedProjectWithKey(t, st, "WL")
			createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: repairSpec})
			b := createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "plan", Slug: "b",
				Body: model.Markdown(fmt.Sprintf(repairPlan, "B", "First."))})
			a := createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "plan", Slug: "a",
				Body: model.Markdown(fmt.Sprintf(repairPlan, "A", fmt.Sprintf("This runs after WL-PLAN-%d.", b.Number)))})
			createTaskViaAPI(t, h, token, map[string]any{"project": projID, "title": "Target", "kind": "feature", "priority": "medium"})
			createTaskViaAPI(t, h, token, map[string]any{"project": projID, "title": "Source", "kind": "feature", "priority": "medium", "body": tc.body})
			if tc.missed[0] != "" {
				seedReferencesRow(t, st, "task", "WL-2", tc.missed[0], tc.missed[1])
			}
			path := tc.path
			if path == "doc-a" {
				path = docPath(a.ID, "/edges")
			}
			if rr := doReq(t, h, tc.method, path, token, tc.req); rr.Code >= 300 {
				t.Fatalf("%s %s: %d %s", tc.method, path, rr.Code, rr.Body)
			}

			var id int64
			var outcome, adjustedTo, by string
			var at sql.NullTime
			var raw []byte
			var stillClassified int
			if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
				if err := tx.QueryRow(`SELECT id, outcome, coalesce(adjusted_to, ''), coalesce(outcome_by, ''), outcome_at
					 FROM ref_classifications WHERE outcome <> 'kept'`).Scan(&id, &outcome, &adjustedTo, &by, &at); err != nil {
					return fmt.Errorf("one repaired row: %w", err)
				}
				if err := tx.QueryRow(`SELECT payload FROM events WHERE type = 'wl:ClassificationRepaired' AND external_id = $1`,
					fmt.Sprintf("ref_classification:%d:%s", id, outcome)).Scan(&raw); err != nil {
					return fmt.Errorf("repair event: %w", err)
				}
				return tx.QueryRow(`SELECT (SELECT count(*) FROM task_edges WHERE classification_id = $1)
					 + (SELECT count(*) FROM task_governed_by WHERE classification_id = $1)
					 + (SELECT count(*) FROM rule_edges WHERE classification_id = $1)
					 + (SELECT count(*) FROM doc_edges WHERE classification_id = $1)`, id).Scan(&stillClassified)
			}); err != nil {
				t.Fatal(err)
			}
			if outcome != tc.outcome || adjustedTo != tc.adjustedTo || by == "" || !at.Valid {
				t.Errorf("row = %s %q by %q at %v, want %s %q by the caller", outcome, adjustedTo, by, at, tc.outcome, tc.adjustedTo)
			}
			var p map[string]any
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			if p["@type"] != "wl:ClassificationRepaired" || p["outcome"] != tc.outcome || p["prov:wasInformedBy"] == nil {
				t.Errorf("event payload = %s", raw)
			}
			if stillClassified != 0 {
				t.Errorf("%d edges still carry the repaired classification", stillClassified)
			}
		})
	}
}

// TestRejectedEdgeStaysRemoved: re-deriving text whose classified edge a
// person removed records nothing new and writes no edge back (WL-REQ-1858).
func TestRejectedEdgeStaysRemoved(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createTaskViaAPI(t, h, token, map[string]any{"project": projID, "title": "Target", "kind": "feature", "priority": "medium"})
	createTaskViaAPI(t, h, token, map[string]any{"project": projID, "title": "Source", "kind": "feature", "priority": "medium", "body": "Depends on WL-1.\n"})
	to := "WL-1"
	if rr := doReq(t, h, "DELETE", "/api/v1/tasks/WL-2/edges", token, model.EdgeInput{To: &to, Type: "dependsOn"}); rr.Code != 204 {
		t.Fatalf("unblock: %d %s", rr.Code, rr.Body)
	}
	if rr := doReq(t, h, "POST", "/api/v1/projects/wl/refs/derive", token, model.RefDeriveParams{Kind: "task", Refs: []string{"WL-2"}}); rr.Code != 200 {
		t.Fatalf("derive: %d %s", rr.Code, rr.Body)
	}
	var edges, rows int
	if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT (SELECT count(*) FROM task_edges WHERE from_task = 'WL-2'), (SELECT count(*) FROM ref_classifications)`).Scan(&edges, &rows)
	}); err != nil {
		t.Fatal(err)
	}
	if edges != 0 || rows != 1 {
		t.Errorf("edges = %d, classifications = %d; want 0, 1", edges, rows)
	}
}

// TestRefStatsRoute: the report counts the selected nodes' classifications,
// and a request that selects nothing is a 400 (WL-REQ-1859).
func TestRefStatsRoute(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createTaskViaAPI(t, h, token, map[string]any{"project": projID, "title": "Target", "kind": "feature", "priority": "medium"})
	createTaskViaAPI(t, h, token, map[string]any{"project": projID, "title": "Source", "kind": "feature", "priority": "medium", "body": "Depends on WL-1.\n"})
	if rr := doReq(t, h, "POST", "/api/v1/projects/wl/refs/derive", token, model.RefDeriveParams{Kind: "task", Refs: []string{"WL-2"}}); rr.Code != 200 {
		t.Fatalf("derive: %d %s", rr.Code, rr.Body)
	}
	rr := doReq(t, h, "GET", "/api/v1/projects/wl/refs/stats?kind=task&ref=WL-2", token, nil)
	if rr.Code != 200 {
		t.Fatalf("stats: %d %s", rr.Code, rr.Body)
	}
	var got model.RefStats
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0].Classifier != "cue" || got.Rows[0].Kept != 1 || got.Rows[0].KeptRate != 1 {
		t.Errorf("rows = %+v", got.Rows)
	}
	if rr := doReq(t, h, "GET", "/api/v1/projects/wl/refs/stats?kind=task", token, nil); rr.Code != 400 {
		t.Errorf("no selection: %d", rr.Code)
	}
}
