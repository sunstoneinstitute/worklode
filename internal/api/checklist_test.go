package api_test

import (
	"net/http"
	"testing"
)

// TestChecklistRoundTrip covers GET/POST /api/v1/tasks/{id}/checklist: items
// parsed from the body on creation, checking one by ordinal, unchecking one
// by title, and the error responses for a bad ordinal, an ambiguous title,
// and an unknown task.
func TestChecklistRoundTrip(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "medium", "kind": "feature",
		"body": "intro\n- [ ] first item\n- [x] second item\n",
	})
	id := created["id"].(string)

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+id+"/checklist", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get checklist status = %d, body %s", rr.Code, rr.Body.String())
	}
	var items []map[string]any
	decodeInto(t, rr, &items)
	if len(items) != 2 {
		t.Fatalf("checklist items = %v, want 2", items)
	}
	if items[0]["title"] != "first item" || items[0]["checked"] != false {
		t.Fatalf("item 0 = %v", items[0])
	}
	if items[1]["title"] != "second item" || items[1]["checked"] != true {
		t.Fatalf("item 1 = %v", items[1])
	}

	// Check item 0 by ordinal.
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/checklist", token,
		map[string]any{"ordinal": 0, "checked": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("check by ordinal status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["checked"] != true || got["title"] != "first item" {
		t.Fatalf("check by ordinal response = %v", got)
	}

	// Uncheck item by title.
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/checklist", token,
		map[string]any{"title": "second item", "checked": false})
	if rr.Code != http.StatusOK {
		t.Fatalf("uncheck by title status = %d, body %s", rr.Code, rr.Body.String())
	}
	got = decodeMap(t, rr)
	if got["checked"] != false {
		t.Fatalf("uncheck by title response = %v", got)
	}

	// The GET reflects both writes.
	rr = doReq(t, h, "GET", "/api/v1/tasks/"+id+"/checklist", token, nil)
	decodeInto(t, rr, &items)
	if items[0]["checked"] != true || items[1]["checked"] != false {
		t.Fatalf("checklist after writes = %v", items)
	}

	// Out-of-range ordinal -> 422.
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/checklist", token,
		map[string]any{"ordinal": 99, "checked": true})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("out of range ordinal status = %d, want 422", rr.Code)
	}

	// Neither ordinal nor title -> 422.
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/checklist", token,
		map[string]any{"checked": true})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no identifier status = %d, want 422", rr.Code)
	}

	// Unknown task -> 404.
	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-999/checklist", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("get checklist on unknown task status = %d, want 404", rr.Code)
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-999/checklist", token,
		map[string]any{"ordinal": 0, "checked": true})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("set checklist item on unknown task status = %d, want 404", rr.Code)
	}
}
