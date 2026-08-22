package api_test

// deleted_test.go covers the cockpit's Deleted destination (deleted.go): the
// page that lists spec 044's tombstones with the justification each delete
// carried, and the two Restore buttons that undelete one.
//
// The tombstone write itself is covered by softdelete_test.go and the store;
// what is checked here is that the page shows what the tombstone stores, that
// Restore goes through the same undelete the JSON API calls, and that a
// refused restore comes back as the page with a reason rather than as an
// error page.

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// deleteBoth tombstones the fixture's task and document with a justification,
// which is what a prod instance demands and a dev instance stores all the
// same (044 §3).
func deleteBoth(t *testing.T, f *deleteFixture, justification string) {
	t.Helper()
	body := model.DeleteInput{Justification: justification}
	if rr := doReq(t, f.h, "DELETE", f.taskPath(""), f.token, body); rr.Code != http.StatusOK {
		t.Fatalf("delete task status = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, f.h, "DELETE", f.docPath(""), f.token, body); rr.Code != http.StatusOK {
		t.Fatalf("delete doc status = %d, body %s", rr.Code, rr.Body.String())
	}
}

// TestDeletedPageShowsTombstones is the reason the page exists: on a prod
// instance a delete is refused without a justification precisely so someone
// can review it later, and before this page the only way to read it was the
// CLI or the API.
func TestDeletedPageShowsTombstones(t *testing.T) {
	f := newDeleteFixture(t, api.InstanceProd)
	const why = "Imported twice; this is the copy nobody worked."
	deleteBoth(t, f, why)

	rr := doReq(t, f.h, "GET", "/projects/proj/deleted", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	assertOneAriaCurrent(t, body)
	main := mainContent(t, body)
	bodyContains(t, main,
		f.task, "Doomed", // the tombstoned task
		"doomed-spec", // the tombstoned document
		why,           // the justification both deletes carried
		"alice",       // who deleted them
		`action="/projects/proj/deleted/tasks/restore"`,
		`action="/projects/proj/deleted/docs/restore"`,
		`<input type="hidden" name="task" value="`+f.task+`"`,
		`<input type="hidden" name="doc" value="`+strconv.FormatInt(f.doc, 10)+`"`,
	)
	// The destination is reachable from the project navigation, or nobody
	// finds it.
	bodyContains(t, body, `href="/projects/proj/deleted"`, ">Deleted<")

	if rr := doReq(t, f.h, "GET", "/projects/nosuch/deleted", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", rr.Code)
	}
}

// TestDeletedPageEmptyIsHonest: a project with nothing deleted says so per
// list rather than rendering an empty table or a fabricated row.
func TestDeletedPageEmptyIsHonest(t *testing.T) {
	f := newDeleteFixture(t, api.InstanceDev)

	main := mainContent(t, doReq(t, f.h, "GET", "/projects/proj/deleted", "", nil).Body.String())
	bodyContains(t, main, "No deleted tasks in this project.", "No deleted documents in this project.")
	if strings.Contains(main, "rowform") {
		t.Errorf("the empty Deleted page renders a Restore form:\n%s", main)
	}
}

// TestDeletedPageRestoresTask checks the Restore button is the same undelete
// the CLI calls: the task leaves the page, comes back into the live list, and
// the caller is 303'd so a reload never restores twice.
func TestDeletedPageRestoresTask(t *testing.T) {
	f := newDeleteFixture(t, api.InstanceDev)
	deleteBoth(t, f, "noise")

	rr := doForm(t, f.h, "/projects/proj/deleted/tasks/restore",
		url.Values{"task": {f.task}}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("restore status = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	if got, want := rr.Header().Get("Location"), "/projects/proj/deleted"; got != want {
		t.Errorf("redirect = %q, want %q", got, want)
	}

	main := mainContent(t, doReq(t, f.h, "GET", "/projects/proj/deleted", "", nil).Body.String())
	if strings.Contains(main, f.task) {
		t.Errorf("restored task %s is still on the Deleted page:\n%s", f.task, main)
	}
	bodyContains(t, main, "No deleted tasks in this project.", "doomed-spec")

	if ids := listTaskIDs(t, f.h, f.token, ""); len(ids) != 1 || ids[0] != f.task {
		t.Errorf("live tasks after restore = %v, want [%s]", ids, f.task)
	}
}

// TestDeletedPageRestoresDoc is the document half. The two are separate
// routes because they carry separate permissions (044 §5).
func TestDeletedPageRestoresDoc(t *testing.T) {
	f := newDeleteFixture(t, api.InstanceDev)
	deleteBoth(t, f, "noise")

	rr := doForm(t, f.h, "/projects/proj/deleted/docs/restore",
		url.Values{"doc": {strconv.FormatInt(f.doc, 10)}}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("restore status = %d, want 303; body %s", rr.Code, rr.Body.String())
	}

	main := mainContent(t, doReq(t, f.h, "GET", "/projects/proj/deleted", "", nil).Body.String())
	if strings.Contains(main, "doomed-spec") {
		t.Errorf("restored document is still on the Deleted page:\n%s", main)
	}
	// The task was deleted too and is untouched: restoring is per row, and
	// the document route does not cascade into anything else.
	bodyContains(t, main, "No deleted documents in this project.", f.task)
}

// TestRestoreRefusalRerendersPage: a row that is gone, is no longer deleted,
// or was never named belongs back on the list with the reason — re-reading
// the list is what answers it — not on an error page.
func TestRestoreRefusalRerendersPage(t *testing.T) {
	f := newDeleteFixture(t, api.InstanceDev)
	deleteBoth(t, f, "noise")

	cases := []struct {
		name string
		path string
		form url.Values
		want string
	}{
		{"unknown task", "/projects/proj/deleted/tasks/restore",
			url.Values{"task": {"WL-999"}}, "There is no WL-999 to restore."},
		{"no task named", "/projects/proj/deleted/tasks/restore",
			url.Values{}, "That restore named no task."},
		{"unparseable doc", "/projects/proj/deleted/docs/restore",
			url.Values{"doc": {"nonsense"}}, "That restore named no document."},
		{"unknown doc", "/projects/proj/deleted/docs/restore",
			url.Values{"doc": {"999999"}}, "There is no 999999 to restore."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := doForm(t, f.h, c.path, c.form, nil)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			assertShell(t, body)
			// The refusal is the page again, tombstones and all, with one
			// message — not a bare error document.
			bodyContains(t, body, c.want, f.task, "doomed-spec")
		})
	}
}

// TestRestoreAlreadyRestoredIsRefused: two people with the page open, one
// restores first. The second gets the reason and a list that no longer shows
// the row, rather than a 500.
func TestRestoreAlreadyRestoredIsRefused(t *testing.T) {
	f := newDeleteFixture(t, api.InstanceDev)
	deleteBoth(t, f, "noise")
	form := url.Values{"task": {f.task}}

	if rr := doForm(t, f.h, "/projects/proj/deleted/tasks/restore", form, nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("first restore status = %d, want 303", rr.Code)
	}
	rr := doForm(t, f.h, "/projects/proj/deleted/tasks/restore", form, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second restore status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), f.task+" is not deleted. It may already have been restored.")
}

// TestRestoreRefusesCrossOrigin: the Restore buttons are state-changing form
// POSTs and carry the same same-origin lock every other cockpit form does,
// which is the one that still holds on an instance serving no session cookie.
func TestRestoreRefusesCrossOrigin(t *testing.T) {
	f := newDeleteFixture(t, api.InstanceDev)
	deleteBoth(t, f, "noise")

	for _, path := range []string{
		"/projects/proj/deleted/tasks/restore",
		"/projects/proj/deleted/docs/restore",
	} {
		rr := doForm(t, f.h, path, url.Values{"task": {f.task}},
			map[string]string{"Sec-Fetch-Site": "cross-site"})
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s cross-origin status = %d, want 403", path, rr.Code)
		}
	}
	// Nothing was restored.
	bodyContains(t, mainContent(t, doReq(t, f.h, "GET", "/projects/proj/deleted", "", nil).Body.String()),
		f.task, "doomed-spec")
}

// TestRestoreMetrics: a restore from the cockpit lands in both the delete
// counter spec 044 §6 defines — under the same op="undelete" the API path
// records — and the form-submission counter every cockpit write records.
func TestRestoreMetrics(t *testing.T) {
	f := newDeleteFixture(t, api.InstanceDev)
	deleteBoth(t, f, "noise")

	doForm(t, f.h, "/projects/proj/deleted/tasks/restore", url.Values{"task": {f.task}}, nil)
	doForm(t, f.h, "/projects/proj/deleted/docs/restore",
		url.Values{"doc": {strconv.FormatInt(f.doc, 10)}}, nil)
	doForm(t, f.h, "/projects/proj/deleted/tasks/restore", url.Values{"task": {"WL-999"}}, nil)

	body := doReq(t, f.admin, "GET", "/metrics", "", nil).Body.String()
	bodyContains(t, body,
		`worklode_deletes_total{entity="task",op="undelete",outcome="ok"} 1`,
		`worklode_deletes_total{entity="doc",op="undelete",outcome="ok"} 1`,
		`worklode_deletes_total{entity="task",op="undelete",outcome="not_found"} 1`,
		`worklode_web_form_submissions_total{form="task_restore",outcome="created"} 1`,
		`worklode_web_form_submissions_total{form="doc_restore",outcome="created"} 1`,
		`worklode_web_form_submissions_total{form="task_restore",outcome="not_found"} 1`,
	)
}
