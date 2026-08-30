package api_test

// softdelete_test.go covers spec 044's delete and undelete over the HTTP
// surface: the tombstone the response carries, the justification rule that
// keys off LODE_INSTANCE_ENV (044 §3), and the ?deleted= list switch (044 §5).
// The tombstone write itself is the store's (internal/store/softdelete_test.go);
// what is checked here is the one rule this layer owns and the status code
// each store refusal reaches the caller as.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// deleteFixture is a booted server on one instance environment with a project,
// a task and a document to delete.
type deleteFixture struct {
	st    *store.Store
	h     http.Handler
	admin http.Handler
	token string
	task  string
	doc   int64
}

// newDeleteFixture boots a server on the named instance environment and seeds
// the one task and one document every case below deletes.
func newDeleteFixture(t *testing.T, instanceEnv string) *deleteFixture {
	t.Helper()
	st := newTestStore(t)
	token := seedActor(t, st, "alice", "human", "Alice", true)
	h, admin, err := api.NewServer(st, api.Config{WebOpen: true, InstanceEnv: instanceEnv})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	createProject(t, st, "proj")
	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Doomed", "priority": "medium", "kind": "feature",
	})
	doc := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 44, Slug: "doomed-spec", Body: docSpecBody,
	})
	return &deleteFixture{st: st, h: h, admin: admin, token: token, task: created["id"].(string), doc: doc.ID}
}

func (f *deleteFixture) taskPath(suffix string) string { return "/api/v1/tasks/" + f.task + suffix }
func (f *deleteFixture) docPath(suffix string) string {
	return "/api/v1/docs/" + strconv.FormatInt(f.doc, 10) + suffix
}

// taskTombstone reads the tombstone off a task response, failing when there is
// none.
func taskTombstone(t *testing.T, rr *httptest.ResponseRecorder) model.Tombstone {
	t.Helper()
	var got model.Task
	decodeInto(t, rr, &got)
	if got.Tombstone == nil {
		t.Fatalf("task %s carries no tombstone: %s", got.ID, rr.Body.String())
	}
	return *got.Tombstone
}

// docTombstone is taskTombstone for a document.
func docTombstone(t *testing.T, rr *httptest.ResponseRecorder) model.Tombstone {
	t.Helper()
	var got model.Doc
	decodeInto(t, rr, &got)
	if got.Tombstone == nil {
		t.Fatalf("doc %d carries no tombstone: %s", got.ID, rr.Body.String())
	}
	return *got.Tombstone
}

// blankJustifications are the three ways a delete arrives with no reason: no
// body at all (a bodyless DELETE is a legal request, not a malformed one), an
// empty field, and whitespace — which is not a reason either.
var blankJustifications = map[string]any{
	"absent body": nil,
	"empty":       model.DeleteInput{},
	"whitespace":  model.DeleteInput{Justification: "   \t "},
}

// A prod instance refuses a justification-less delete with 422 and names the
// instance environment, because the request is well-formed and the same server
// configured the other way would have taken it (044 §5).
func TestDeleteProdRequiresJustification(t *testing.T) {
	t.Parallel()
	f := newDeleteFixture(t, api.InstanceProd)

	for name, body := range blankJustifications {
		for entity, path := range map[string]string{"task": f.taskPath(""), "doc": f.docPath("")} {
			t.Run(entity+"/"+name, func(t *testing.T) {
				rr := doReq(t, f.h, "DELETE", path, f.token, body)
				if rr.Code != http.StatusUnprocessableEntity {
					t.Fatalf("status = %d, want 422, body %s", rr.Code, rr.Body.String())
				}
				got, _ := decodeMap(t, rr)["error"].(string)
				if !strings.Contains(got, "LODE_INSTANCE_ENV=prod") {
					t.Fatalf("error = %q, want it to name the instance environment", got)
				}
			})
		}
	}

	// Nothing was tombstoned by the refusals.
	if rr := doReq(t, f.h, "GET", f.taskPath(""), f.token, nil); strings.Contains(rr.Body.String(), "tombstone") {
		t.Fatalf("refused delete left a tombstone: %s", rr.Body.String())
	}
}

// A real justification is accepted, stored trimmed, and comes back on the
// response together with the actor that deleted the row.
func TestDeleteProdWithJustification(t *testing.T) {
	t.Parallel()
	f := newDeleteFixture(t, api.InstanceProd)

	rr := doReq(t, f.h, "DELETE", f.taskPath(""), f.token,
		model.DeleteInput{Justification: "  filed twice by the importer  "})
	if rr.Code != http.StatusOK {
		t.Fatalf("delete task status = %d, body %s", rr.Code, rr.Body.String())
	}
	ts := taskTombstone(t, rr)
	if ts.Justification != "filed twice by the importer" {
		t.Fatalf("task justification = %q, want it trimmed and stored verbatim", ts.Justification)
	}
	if ts.DeletedBy != "alice" {
		t.Fatalf("task deleted_by = %q, want alice", ts.DeletedBy)
	}
	if ts.DeletedAt.IsZero() {
		t.Fatal("task deleted_at is zero")
	}

	rr = doReq(t, f.h, "DELETE", f.docPath(""), f.token,
		model.DeleteInput{Justification: "duplicate import"})
	if rr.Code != http.StatusOK {
		t.Fatalf("delete doc status = %d, body %s", rr.Code, rr.Body.String())
	}
	if ts := docTombstone(t, rr); ts.Justification != "duplicate import" || ts.DeletedBy != "alice" {
		t.Fatalf("doc tombstone = %+v, want justification and deleted_by set", ts)
	}
}

// A dev instance takes a bodyless delete and tombstones with an empty
// justification: the environment gates the demand, not the mechanism (044 §3).
func TestDeleteDevWithoutJustification(t *testing.T) {
	t.Parallel()
	f := newDeleteFixture(t, api.InstanceDev)

	rr := doReq(t, f.h, "DELETE", f.taskPath(""), f.token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete task status = %d, want 200, body %s", rr.Code, rr.Body.String())
	}
	if ts := taskTombstone(t, rr); ts.Justification != "" || ts.DeletedBy != "alice" {
		t.Fatalf("task tombstone = %+v, want empty justification and deleted_by=alice", ts)
	}

	rr = doReq(t, f.h, "DELETE", f.docPath(""), f.token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete doc status = %d, want 200, body %s", rr.Code, rr.Body.String())
	}
	if ts := docTombstone(t, rr); ts.Justification != "" {
		t.Fatalf("doc justification = %q, want empty", ts.Justification)
	}
}

// A justification given on a dev instance is stored exactly as one given on
// prod (044 §3).
func TestDeleteDevStoresGivenJustification(t *testing.T) {
	t.Parallel()
	f := newDeleteFixture(t, api.InstanceDev)

	rr := doReq(t, f.h, "DELETE", f.taskPath(""), f.token,
		model.DeleteInput{Justification: "seeded by the fixture"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := taskTombstone(t, rr).Justification; got != "seeded by the fixture" {
		t.Fatalf("justification = %q, want it stored", got)
	}
}

// Undelete round-trips on both instances and carries no body and no
// justification: only the first half of the pair is worth making someone stop
// and type (044 §3).
func TestUndeleteRoundTrip(t *testing.T) {
	t.Parallel()
	for _, env := range []string{api.InstanceDev, api.InstanceProd} {
		t.Run(env, func(t *testing.T) {
			f := newDeleteFixture(t, env)
			body := any(nil)
			if env == api.InstanceProd {
				body = model.DeleteInput{Justification: "noise"}
			}
			if rr := doReq(t, f.h, "DELETE", f.taskPath(""), f.token, body); rr.Code != http.StatusOK {
				t.Fatalf("delete task status = %d, body %s", rr.Code, rr.Body.String())
			}
			if rr := doReq(t, f.h, "DELETE", f.docPath(""), f.token, body); rr.Code != http.StatusOK {
				t.Fatalf("delete doc status = %d, body %s", rr.Code, rr.Body.String())
			}

			rr := doReq(t, f.h, "POST", f.taskPath("/undelete"), f.token, nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("undelete task status = %d, body %s", rr.Code, rr.Body.String())
			}
			var task model.Task
			decodeInto(t, rr, &task)
			if task.Tombstone != nil {
				t.Fatalf("undeleted task still carries a tombstone: %+v", task.Tombstone)
			}

			rr = doReq(t, f.h, "POST", f.docPath("/undelete"), f.token, nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("undelete doc status = %d, body %s", rr.Code, rr.Body.String())
			}
			var doc model.Doc
			decodeInto(t, rr, &doc)
			if doc.Tombstone != nil {
				t.Fatalf("undeleted doc still carries a tombstone: %+v", doc.Tombstone)
			}

			// Both are back in their lists.
			if ids := listTaskIDs(t, f.h, f.token, ""); len(ids) != 1 {
				t.Fatalf("live tasks after undelete = %v, want the one task", ids)
			}
		})
	}
}

// Deleting an already-deleted row is the store's ErrInvalidInput, which is a
// 422 here — not a silent success, because the tombstone it would overwrite
// names someone else (044 §2).
func TestDeleteTwiceIsRejected(t *testing.T) {
	t.Parallel()
	f := newDeleteFixture(t, api.InstanceDev)

	if rr := doReq(t, f.h, "DELETE", f.taskPath(""), f.token, nil); rr.Code != http.StatusOK {
		t.Fatalf("first delete status = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, f.h, "DELETE", f.taskPath(""), f.token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second delete status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, f.h, "DELETE", f.docPath(""), f.token, nil); rr.Code != http.StatusOK {
		t.Fatalf("first doc delete status = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, f.h, "DELETE", f.docPath(""), f.token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second doc delete status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
	// Undeleting a live row is refused the same way.
	if rr := doReq(t, f.h, "POST", f.taskPath("/undelete"), f.token, nil); rr.Code != http.StatusOK {
		t.Fatalf("undelete status = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, f.h, "POST", f.taskPath("/undelete"), f.token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second undelete status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
}

func TestDeleteUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()
	f := newDeleteFixture(t, api.InstanceDev)

	for name, path := range map[string]string{
		"task":          "/api/v1/tasks/WL-999",
		"doc":           "/api/v1/docs/99999",
		"task undelete": "/api/v1/tasks/WL-999/undelete",
		"doc undelete":  "/api/v1/docs/99999/undelete",
	} {
		method := "DELETE"
		if strings.HasSuffix(path, "/undelete") {
			method = "POST"
		}
		t.Run(name, func(t *testing.T) {
			rr := doReq(t, f.h, method, path, f.token, nil)
			if rr.Code != http.StatusNotFound {
				t.Fatalf("%s %s = %d, want 404, body %s", method, path, rr.Code, rr.Body.String())
			}
		})
	}
}

// listTaskIDs returns the ids GET /api/v1/tasks answers with for the given
// query string (leading "?" included, or empty).
func listTaskIDs(t *testing.T, h http.Handler, token, query string) []string {
	t.Helper()
	rr := doReq(t, h, "GET", "/api/v1/tasks"+query, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list tasks%s status = %d, body %s", query, rr.Code, rr.Body.String())
	}
	var resp model.TaskListResponse
	decodeInto(t, rr, &resp)
	ids := make([]string, 0, len(resp.Tasks))
	for _, task := range resp.Tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

// listDocIDs is listTaskIDs for documents.
func listDocIDs(t *testing.T, h http.Handler, token, query string) []int64 {
	t.Helper()
	rr := doReq(t, h, "GET", "/api/v1/docs"+query, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list docs%s status = %d, body %s", query, rr.Code, rr.Body.String())
	}
	var resp model.DocListResponse
	decodeInto(t, rr, &resp)
	ids := make([]int64, 0, len(resp.Docs))
	for _, d := range resp.Docs {
		ids = append(ids, d.ID)
	}
	return ids
}

// ?deleted=true is a switch, not an addition: it lists the tombstoned rows
// instead of the live ones (044 §5).
func TestListDeletedIsASwitch(t *testing.T) {
	t.Parallel()
	f := newDeleteFixture(t, api.InstanceDev)
	// A second task and document that stay live, so "hides the deleted one"
	// is distinguishable from "returns nothing".
	survivor := createTaskViaAPI(t, f.h, f.token, map[string]any{
		"project": "proj", "title": "Survivor", "priority": "low", "kind": "chore",
	})["id"].(string)
	survivingDoc := createDocViaAPI(t, f.h, f.token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 45, Slug: "surviving-spec", Body: docSpecBody,
	}).ID

	if rr := doReq(t, f.h, "DELETE", f.taskPath(""), f.token, nil); rr.Code != http.StatusOK {
		t.Fatalf("delete task status = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, f.h, "DELETE", f.docPath(""), f.token, nil); rr.Code != http.StatusOK {
		t.Fatalf("delete doc status = %d, body %s", rr.Code, rr.Body.String())
	}

	if got := listTaskIDs(t, f.h, f.token, ""); len(got) != 1 || got[0] != survivor {
		t.Fatalf("live tasks = %v, want only %s", got, survivor)
	}
	if got := listTaskIDs(t, f.h, f.token, "?deleted=true"); len(got) != 1 || got[0] != f.task {
		t.Fatalf("deleted tasks = %v, want only %s", got, f.task)
	}
	if got := listDocIDs(t, f.h, f.token, ""); len(got) != 1 || got[0] != survivingDoc {
		t.Fatalf("live docs = %v, want only %d", got, survivingDoc)
	}
	if got := listDocIDs(t, f.h, f.token, "?deleted=true"); len(got) != 1 || got[0] != f.doc {
		t.Fatalf("deleted docs = %v, want only %d", got, f.doc)
	}

	// The tombstoned task still resolves by id, tombstone and all (044 §4).
	rr := doReq(t, f.h, "GET", f.taskPath(""), f.token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get deleted task status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"tombstone"`) {
		t.Fatalf("get deleted task carries no tombstone: %s", rr.Body.String())
	}
}

// A non-boolean ?deleted= is named rather than read as off, the stance every
// other boolean query parameter takes.
func TestListDeletedRejectsNonBoolean(t *testing.T) {
	t.Parallel()
	f := newDeleteFixture(t, api.InstanceDev)

	if rr := doReq(t, f.h, "GET", "/api/v1/tasks?deleted=maybe", f.token, nil); rr.Code != http.StatusBadRequest {
		t.Fatalf("tasks?deleted=maybe = %d, want 400, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, f.h, "GET", "/api/v1/docs?deleted=maybe", f.token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("docs?deleted=maybe = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
}

// All four routes are behind the bearer-token guard. The router's own boot
// check already refuses a route the table does not name; this is the other
// half — that the table entry is actually enforced on the wire.
func TestDeleteRoutesRequireAuth(t *testing.T) {
	t.Parallel()
	f := newDeleteFixture(t, api.InstanceDev)

	for _, tc := range []struct{ method, path string }{
		{"DELETE", f.taskPath("")},
		{"POST", f.taskPath("/undelete")},
		{"DELETE", f.docPath("")},
		{"POST", f.docPath("/undelete")},
	} {
		rr := doReq(t, f.h, tc.method, tc.path, "", nil)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s unauthenticated = %d, want 401", tc.method, tc.path, rr.Code)
		}
	}
}

// Deleting a leased task closes the lease in the same transaction (044 §2):
// a hidden task cannot be worked, and the sweeper should not be left tending a
// row nothing can see.
func TestDeleteTaskClosesLease(t *testing.T) {
	t.Parallel()
	f := newDeleteFixture(t, api.InstanceDev)

	rr := doReq(t, f.h, "POST", f.taskPath("/claim"), f.token, map[string]any{"worktree": "wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, f.h, "DELETE", f.taskPath(""), f.token, nil); rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body %s", rr.Code, rr.Body.String())
	}
	if _, err := f.st.ActiveLease(t.Context(), f.task); err == nil {
		t.Fatal("lease survived the delete")
	}
}

// worklode_deletes_total is pre-initialised across every entity/op/outcome
// combination and counts the prod refusal as an outcome of the delete op
// rather than as an absence of one (044 §6).
func TestDeleteMetrics(t *testing.T) {
	t.Parallel()
	f := newDeleteFixture(t, api.InstanceProd)

	doReq(t, f.h, "DELETE", f.taskPath(""), f.token, nil)                                           // justification_required
	doReq(t, f.h, "DELETE", "/api/v1/tasks/WL-999", f.token, model.DeleteInput{Justification: "x"}) // not_found
	doReq(t, f.h, "DELETE", f.taskPath(""), f.token, model.DeleteInput{Justification: "noise"})     // ok
	doReq(t, f.h, "POST", f.taskPath("/undelete"), f.token, nil)                                    // ok
	doReq(t, f.h, "DELETE", f.docPath(""), f.token, model.DeleteInput{Justification: "noise"})      // ok

	body := doReq(t, f.admin, "GET", "/metrics", "", nil).Body.String()
	for _, want := range []string{
		`worklode_deletes_total{entity="task",op="delete",outcome="justification_required"} 1`,
		`worklode_deletes_total{entity="task",op="delete",outcome="not_found"} 1`,
		`worklode_deletes_total{entity="task",op="delete",outcome="ok"} 1`,
		`worklode_deletes_total{entity="task",op="undelete",outcome="ok"} 1`,
		`worklode_deletes_total{entity="doc",op="delete",outcome="ok"} 1`,
		// Pre-initialised: an instance where nobody has undeleted a document
		// reads as a flat zero, not as no-data.
		`worklode_deletes_total{entity="doc",op="undelete",outcome="error"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %s", want)
		}
	}
	// Undelete asks for no justification on either instance (044 §3), so this
	// series can never move; pre-initialising it would publish a permanently
	// flat zero that reads as "no undelete was ever refused" rather than as
	// "an undelete cannot be refused for that reason".
	for _, entity := range []string{"task", "doc"} {
		unreachable := `worklode_deletes_total{entity="` + entity +
			`",op="undelete",outcome="justification_required"}`
		if strings.Contains(body, unreachable) {
			t.Fatalf("metrics publish the unreachable series %s", unreachable)
		}
	}
}

func TestParseInstanceEnv(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"":     api.InstanceProd, // 039 §3: prod is the default nobody has to write down
		"dev":  api.InstanceDev,
		"prod": api.InstanceProd,
	} {
		got, err := api.ParseInstanceEnv(in)
		if err != nil {
			t.Fatalf("ParseInstanceEnv(%q) err = %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseInstanceEnv(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"staging", "PROD", "development", "1"} {
		if _, err := api.ParseInstanceEnv(in); err == nil {
			t.Fatalf("ParseInstanceEnv(%q) accepted an unrecognised value", in)
		}
	}
}

// NewServer re-checks the environment, so an embedder that builds a Config in
// Go cannot get an unset or bogus value past the boot (039 §3).
func TestNewServerRejectsBadInstanceEnv(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	_, _, err := api.NewServer(st, api.Config{InstanceEnv: "staging"})
	if err == nil {
		t.Fatal("expected error for an unrecognised LODE_INSTANCE_ENV")
	}
	if !strings.Contains(err.Error(), "LODE_INSTANCE_ENV") {
		t.Fatalf("boot refusal = %v, want it to name the env var", err)
	}
	// An unset value is the prod default, so a Config that says nothing boots
	// and demands a justification.
	if _, _, err := api.NewServer(newTestStore(t), api.Config{}); err != nil {
		t.Fatalf("empty InstanceEnv must default to prod, got %v", err)
	}
}
