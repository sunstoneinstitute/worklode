package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestConfirmDocTransferSkipsPromptOnNonTerminalStdin: an *os.File stdin
// that is not a terminal (redirected from a file or pipe, as under CI or an
// agent) must proceed without ever reading an answer from it. Asserted
// directly rather than relying on the other tests' implicit coverage (they
// get their non-interactive behavior for free from `go test`'s own
// non-terminal stdin): the file here "answers" n, so if the terminal check
// ever inverted and the code actually read it, this fails on that answer
// instead of hanging — a real file's Read never blocks, unlike an
// interactive terminal or an unclosed pipe would.
func TestConfirmDocTransferSkipsPromptOnNonTerminalStdin(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if _, err := f.WriteString("n\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	cmd.SetIn(f)
	cmd.SetErr(io.Discard)

	docs := []model.Doc{{ID: 1, Kind: "spec", Number: 1, Title: "x"}}
	if !confirmDocTransfer(cmd, docs, "bob", "ada") {
		t.Fatal("confirmDocTransfer on non-terminal stdin = false, want true (proceed without reading the file's \"n\" answer)")
	}
}

// TestDocTransferBothOrNeitherRefused: refs and --from are mutually
// exclusive, and exactly one is required (Task 5's Args validator).
func TestDocTransferBothOrNeitherRefused(t *testing.T) {
	cmd := newDocTransferCmd()
	cmd.SetArgs([]string{"--to", "bob"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("neither refs nor --from: err = %v, want it to name both forms", err)
	}

	cmd = newDocTransferCmd()
	cmd.SetArgs([]string{"some-ref", "--from", "bob", "--to", "ada"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("both refs and --from: err = %v, want it to name both forms", err)
	}
}

// TestDocTransferByRefs: naming documents directly resolves them the way
// every other `lode doc` command does and transfers each.
func TestDocTransferByRefs(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	if err := st.CreateActor(context.Background(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	specFile := writeDocFile(t, docTestBody)
	if _, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
		"--slug", "ref-spec", "--file", specFile); err != nil {
		t.Fatalf("doc new: %v", err)
	}

	out, err := runLode(t, "doc", "transfer", "ref-spec", "--to", "bob")
	if err != nil {
		t.Fatalf("doc transfer: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "moved") {
		t.Fatalf("doc transfer output = %q, want it to report the move", out)
	}

	out, err = runLode(t, "doc", "get", "ref-spec", "--json")
	if err != nil {
		t.Fatalf("doc get: %v\noutput: %s", err, out)
	}
	if got := docJSON(t, out).Owner; got != "bob" {
		t.Errorf("owner after transfer = %q, want bob", got)
	}
}

// TestDocTransferJSONReportsNewOwner: a successful `--json` transfer reports
// the endpoint's response, not the pre-transfer document ListDocs/GetDoc
// resolved it from — otherwise the emitted owner would be stale (round 1
// finding F1). Covers both forms.
func TestDocTransferJSONReportsNewOwner(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	if err := st.CreateActor(context.Background(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	if err := st.CreateActor(context.Background(), "ada", "human", "Ada", false); err != nil {
		t.Fatalf("create actor ada: %v", err)
	}
	specFile := writeDocFile(t, docTestBody)
	if _, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
		"--slug", "ref-json-spec", "--file", specFile); err != nil {
		t.Fatalf("doc new: %v", err)
	}

	// By ref.
	out, err := runLode(t, "doc", "transfer", "ref-json-spec", "--to", "bob", "--json")
	if err != nil {
		t.Fatalf("doc transfer --json: %v\noutput: %s", err, out)
	}
	var results []docTransferResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(results) != 1 || results[0].Doc.Owner != "bob" {
		t.Fatalf("doc transfer --json results = %+v, want one result owned by bob", results)
	}

	// By --from.
	if _, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
		"--slug", "from-json-spec", "--owner", "bob", "--file", specFile); err != nil {
		t.Fatalf("doc new: %v", err)
	}
	// --from's confirmDocTransfer prints its preview to stderr even under
	// --json (it only skips the interactive question); split streams so
	// that preview does not land in the JSON stdout is decoded.
	out, _, err = runLodeOutErr(t, "doc", "transfer", "--from", "bob", "--to", "ada", "--project", "proj", "--json")
	if err != nil {
		t.Fatalf("doc transfer --from --json: %v\noutput: %s", err, out)
	}
	results = nil
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	for _, r := range results {
		if r.Doc.Owner != "ada" {
			t.Errorf("doc transfer --from --json result = %+v, want owner ada", r)
		}
	}
	if len(results) == 0 {
		t.Fatal("doc transfer --from --json results = [], want bob's document(s)")
	}
}

// TestDocTransferFromPromptsOnTTYAndRespectsDecline: the --from form prints
// what it will move and asks first on a real terminal (simulated by feeding
// stdin a non-*os.File reader, which confirmDocTransfer cannot detect as
// non-interactive — the same trick secretsceremony_test.go relies on).
// Declining leaves ownership untouched; accepting moves it.
func TestDocTransferFromPromptsOnTTYAndRespectsDecline(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	if err := st.CreateActor(context.Background(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	if err := st.CreateActor(context.Background(), "ada", "human", "Ada", false); err != nil {
		t.Fatalf("create actor ada: %v", err)
	}
	specFile := writeDocFile(t, docTestBody)
	if _, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
		"--slug", "from-spec", "--owner", "bob", "--file", specFile); err != nil {
		t.Fatalf("doc new: %v", err)
	}

	rootCmd.SetIn(strings.NewReader("n\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	out, err := runLode(t, "doc", "transfer", "--from", "bob", "--to", "ada", "--project", "proj")
	if err != nil {
		t.Fatalf("decline: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "aborted") {
		t.Fatalf("decline output = %q, want it to report the abort", out)
	}
	out, err = runLode(t, "doc", "get", "from-spec", "--json")
	if err != nil {
		t.Fatalf("doc get: %v", err)
	}
	if got := docJSON(t, out).Owner; got != "bob" {
		t.Errorf("owner after a declined transfer = %q, want still bob", got)
	}

	rootCmd.SetIn(strings.NewReader("y\n"))
	out, err = runLode(t, "doc", "transfer", "--from", "bob", "--to", "ada", "--project", "proj")
	if err != nil {
		t.Fatalf("accept: %v\noutput: %s", err, out)
	}
	out, err = runLode(t, "doc", "get", "from-spec", "--json")
	if err != nil {
		t.Fatalf("doc get: %v", err)
	}
	if got := docJSON(t, out).Owner; got != "ada" {
		t.Errorf("owner after an accepted transfer = %q, want ada", got)
	}
}

// TestDocTransferFromEmptyOwnerIsNotAnError: an actor owning nothing in
// scope transfers nothing, and that is success, not a refusal (Task 4's
// empty-list rule extended to the bulk form).
func TestDocTransferFromEmptyOwnerIsNotAnError(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	out, err := runLode(t, "doc", "transfer", "--from", "nobody-owns-anything", "--to", "alice", "--project", "proj")
	if err != nil {
		t.Fatalf("doc transfer --from with no docs: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "nothing to transfer") {
		t.Fatalf("output = %q, want it to say there was nothing to move", out)
	}
}

// TestDocTransferPartialFailureThenRetryFinishes: a failure partway through
// reports which documents moved and which did not and exits non-zero;
// re-running with the same --from finishes the job because the doc that
// already moved has dropped out of the --from filter, leaving only the one
// that failed to retry. The fake server fails only the very first
// POST .../owner call across the whole test, whichever document it lands
// on, simulating one transient failure in an otherwise-working run.
func TestDocTransferPartialFailureThenRetryFinishes(t *testing.T) {
	var ownerCalls int32
	st, c := testServer(t, api.Config{}, func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/owner") {
				if atomic.AddInt32(&ownerCalls, 1) == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					io.WriteString(w, `{"error":"simulated failure"}`)
					return
				}
			}
			h.ServeHTTP(w, r)
		})
	})
	setupProject(t, c)
	if err := st.CreateActor(context.Background(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	if err := st.CreateActor(context.Background(), "ada", "human", "Ada", false); err != nil {
		t.Fatalf("create actor ada: %v", err)
	}
	specFile := writeDocFile(t, docTestBody)
	for _, slug := range []string{"doc-a", "doc-b"} {
		if _, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
			"--slug", slug, "--owner", "bob", "--file", specFile); err != nil {
			t.Fatalf("doc new %s: %v", slug, err)
		}
	}

	out, err := runLode(t, "doc", "transfer", "--from", "bob", "--to", "ada", "--project", "proj")
	if err == nil {
		t.Fatalf("want a non-zero exit on partial failure, got nil; output: %s", out)
	}
	if !strings.Contains(out, "FAILED") || !strings.Contains(out, "moved") {
		t.Fatalf("partial-failure output = %q, want one row moved and one row FAILED", out)
	}

	// Re-run with the same --from bob: the doc that already moved to ada is
	// no longer owned by bob, so it drops out of the filter and isn't
	// retried; the one that failed is still bob's, so it transfers for real
	// and the whole run now succeeds.
	out, err = runLode(t, "doc", "transfer", "--from", "bob", "--to", "ada", "--project", "proj")
	if err != nil {
		t.Fatalf("retry: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "FAILED") {
		t.Fatalf("retry output = %q, want no failures left", out)
	}

	out, err = runLode(t, "doc", "list", "--project", "proj", "--owner", "bob", "--json")
	if err != nil {
		t.Fatalf("doc list --owner bob: %v", err)
	}
	var listed struct {
		Docs []struct{} `json:"docs"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("decode doc list %q: %v", out, err)
	}
	if len(listed.Docs) != 0 {
		t.Fatalf("doc list --owner bob after full transfer = %+v, want none left", listed.Docs)
	}
}
