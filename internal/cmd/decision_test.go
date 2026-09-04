package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const posedDecisionJSON = `{"id":1,"task":"WL-7","key":"x-distribution","position":1,` +
	`"group":"scope","question":"Do we ship X?","context":"budget is fixed",` +
	`"response_type":"single_select","options":[{"label":"yes"},{"label":"no","description":"defer"}]}`

// TestDecisionAddPostsToTask covers `lode decision add`: the task is the
// positional argument, every field flag rides along, --option is repeatable
// and splits label from description, --type defaults to single_select once
// options are named, and the confirmation renders the posed row.
func TestDecisionAddPostsToTask(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, posedDecisionJSON)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	cmd := newDecisionAddCmd()
	cmd.SetArgs([]string{"WL-7",
		"--key", "x-distribution", "--question", "Do we ship X?",
		"--context", "budget is fixed", "--group", "scope",
		"--option", "yes", "--option", "no:defer"})
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("decision add: %v", err)
	}
	if gotPath != "/api/v1/tasks/WL-7/decisions" {
		t.Errorf("path = %q, want /api/v1/tasks/WL-7/decisions", gotPath)
	}
	for _, want := range []string{
		`"key":"x-distribution"`, `"question":"Do we ship X?"`, `"context":"budget is fixed"`,
		`"group":"scope"`, `"response_type":"single_select"`,
		`{"label":"yes"}`, `{"label":"no","description":"defer"}`,
	} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("request body missing %s:\n%s", want, gotBody)
		}
	}
	for _, want := range []string{"WL-7/x-distribution", "Do we ship X?", "single_select", "defer", "budget is fixed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

// TestDecisionAddDefaultsToFreetext: with no --option and no --type, the
// question takes free text.
func TestDecisionAddDefaultsToFreetext(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, posedDecisionJSON)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	cmd := newDecisionAddCmd()
	cmd.SetArgs([]string{"WL-7", "--key", "k", "--question", "When?"})
	cmd.SetOut(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("decision add: %v", err)
	}
	if !strings.Contains(gotBody, `"response_type":"freetext"`) {
		t.Errorf("request body = %q, want freetext", gotBody)
	}
	if strings.Contains(gotBody, `"group"`) || strings.Contains(gotBody, `"min_picks"`) {
		t.Errorf("request body sends flags that were never set: %q", gotBody)
	}
}

// TestDecisionEditPatchesRow covers `lode decision edit`: the <task>/<key>
// address splits, --task re-parents, and unset flags stay out of the body so
// the edit changes only what was named.
func TestDecisionEditPatchesRow(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, posedDecisionJSON)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	cmd := newDecisionEditCmd()
	cmd.SetArgs([]string{"WL-7/x-distribution", "--question", "Do we ship X in October?", "--task", "WL-9"})
	cmd.SetOut(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("decision edit: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/v1/tasks/WL-7/decisions/x-distribution" {
		t.Errorf("request = %s %s, want PATCH /api/v1/tasks/WL-7/decisions/x-distribution", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"task":"WL-9"`) || !strings.Contains(gotBody, `"question":"Do we ship X in October?"`) {
		t.Errorf("request body = %q", gotBody)
	}
	if strings.Contains(gotBody, `"response_type"`) {
		t.Errorf("edit sent a response_type it was never given: %q", gotBody)
	}
}

// TestDecisionEditRejectsBareTask: an address with no key names a task, not
// a question, and the error says how to write one.
func TestDecisionEditRejectsBareTask(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := newDecisionEditCmd()
	cmd.SetArgs([]string{"WL-7", "--question", "q"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "<task>/<key>") {
		t.Fatalf("decision edit WL-7 err = %v, want the address hint", err)
	}
}
