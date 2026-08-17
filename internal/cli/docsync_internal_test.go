package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// docOfSize builds a DocUpsert whose encoded JSON is at least n bytes.
func docOfSize(ordinal string, n int) DocUpsert {
	return DocUpsert{
		Kind: "spec", Ordinal: ordinal, Status: "accepted", Title: "T",
		Body:        strings.Repeat("x", n),
		Frontmatter: json.RawMessage(`{"status":"accepted"}`),
	}
}

func encodedSize(t *testing.T, docs []DocUpsert) int {
	t.Helper()
	total := 0
	for _, d := range docs {
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		total += len(b)
	}
	return total
}

func TestBatchDocsSplitsOnByteBudget(t *testing.T) {
	budget := 1000
	// Three docs of ~400 encoded bytes each: two fit, the third starts a
	// second batch. The split is on bytes, not on a document count.
	docs := []DocUpsert{docOfSize("1", 300), docOfSize("2", 300), docOfSize("3", 300)}
	batches, err := batchDocs(docs, budget)
	if err != nil {
		t.Fatalf("batchDocs: %v", err)
	}
	if len(batches) != 2 {
		t.Fatalf("batches = %d, want 2 (%v)", len(batches), batchOrdinals(batches))
	}
	for i, b := range batches {
		if n := encodedSize(t, b); n > budget {
			t.Errorf("batch %d encodes to %d bytes, over the %d budget", i, n, budget)
		}
	}
	if got := batchOrdinals(batches); fmt.Sprint(got) != "[[1 2] [3]]" {
		t.Errorf("batches = %v, want [[1 2] [3]]", got)
	}
}

func TestBatchDocsOversizedDocGoesOutAlone(t *testing.T) {
	budget := 1000
	docs := []DocUpsert{docOfSize("1", 100), docOfSize("2", 5000), docOfSize("3", 100)}
	batches, err := batchDocs(docs, budget)
	if err != nil {
		t.Fatalf("batchDocs: %v", err)
	}
	if got := batchOrdinals(batches); fmt.Sprint(got) != "[[1] [2] [3]]" {
		t.Fatalf("batches = %v, want the oversized doc alone: [[1] [2] [3]]", got)
	}
}

func TestBatchDocsEmptyCorpusStillSendsOneRequest(t *testing.T) {
	batches, err := batchDocs(nil, 1000)
	if err != nil {
		t.Fatalf("batchDocs: %v", err)
	}
	if len(batches) != 1 || len(batches[0]) != 0 {
		t.Fatalf("batches = %v, want one empty batch", batches)
	}
}

// batchOrdinals renders the ordinals of each batch for assertions.
func batchOrdinals(batches [][]DocUpsert) [][]string {
	out := make([][]string, 0, len(batches))
	for _, b := range batches {
		ords := make([]string, 0, len(b))
		for _, d := range b {
			ords = append(ords, d.Ordinal)
		}
		out = append(out, ords)
	}
	return out
}

// fakeSender records the requests it is handed and answers each with a
// report that marks every document "added".
type fakeSender struct {
	reqs   []DocSyncInput
	failAt int // 1-based request number to fail on; 0 never fails
	err    error
}

func (f *fakeSender) send(_ context.Context, in DocSyncInput) (DocSyncReport, error) {
	f.reqs = append(f.reqs, in)
	if f.failAt != 0 && len(f.reqs) == f.failAt {
		return DocSyncReport{}, f.err
	}
	rep := DocSyncReport{DryRun: in.DryRun}
	for _, d := range in.Docs {
		rep.Added++
		rep.Results = append(rep.Results, DocSyncResult{
			ID: "WL-SPEC-" + d.Ordinal, Kind: d.Kind, Outcome: "added"})
	}
	return rep, nil
}

func TestSyncDocsBatchedSendsEveryDocExactlyOnce(t *testing.T) {
	docs := []DocUpsert{}
	for i := 1; i <= 9; i++ {
		docs = append(docs, docOfSize(fmt.Sprint(i), 300))
	}
	f := &fakeSender{}
	in := DocSyncInput{Project: "wl", SourceBranch: "main", Dirty: true, Force: true, Docs: docs}
	rep, err := syncDocsBatched(context.Background(), in, 1000, f.send)
	if err != nil {
		t.Fatalf("syncDocsBatched: %v", err)
	}
	if len(f.reqs) < 2 {
		t.Fatalf("requests = %d, want the corpus split across several", len(f.reqs))
	}
	seen := map[string]int{}
	for _, r := range f.reqs {
		if r.Project != "wl" || r.SourceBranch != "main" || !r.Dirty || !r.Force {
			t.Errorf("batch lost the envelope: %+v", r)
		}
		for _, d := range r.Docs {
			seen[d.Ordinal]++
		}
	}
	if len(seen) != len(docs) {
		t.Errorf("saw %d distinct docs, want %d", len(seen), len(docs))
	}
	for ord, n := range seen {
		if n != 1 {
			t.Errorf("doc %s sent %d times, want exactly 1", ord, n)
		}
	}
	if rep.Added != len(docs) || len(rep.Results) != len(docs) {
		t.Errorf("merged report = %+v, want %d added across %d results",
			rep, len(docs), len(docs))
	}
}

func TestSyncDocsBatchedFailureNamesWhatDidNotSync(t *testing.T) {
	docs := []DocUpsert{}
	for i := 1; i <= 6; i++ {
		docs = append(docs, docOfSize(fmt.Sprint(i), 300))
	}
	boom := errors.New("server error (413): too large")
	f := &fakeSender{failAt: 2, err: boom}
	rep, err := syncDocsBatched(context.Background(), DocSyncInput{Project: "wl", Docs: docs}, 1000, f.send)
	if err == nil {
		t.Fatal("syncDocsBatched succeeded; want the batch failure surfaced")
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want it to wrap the transport error", err)
	}
	var pe *DocSyncPartialError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %T, want *DocSyncPartialError", err)
	}
	// Batch 1 (docs 1-2) landed; everything from batch 2 on did not.
	if pe.Synced != len(rep.Results) || pe.Synced == 0 {
		t.Errorf("Synced = %d, partial report has %d results", pe.Synced, len(rep.Results))
	}
	if len(pe.NotSynced) != len(docs)-pe.Synced {
		t.Fatalf("NotSynced = %v, want the %d docs that never left", pe.NotSynced, len(docs)-pe.Synced)
	}
	for _, want := range []string{"spec/3", "spec/6"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "spec/1") {
		t.Errorf("error %q names a document that did sync", err.Error())
	}
	if len(f.reqs) != 2 {
		t.Errorf("requests = %d, want the sync to stop at the failing batch", len(f.reqs))
	}
}

func TestDocSyncPartialErrorTruncatesLongLists(t *testing.T) {
	e := &DocSyncPartialError{Err: errors.New("boom"), Synced: 1}
	for i := 0; i < 25; i++ {
		e.NotSynced = append(e.NotSynced, fmt.Sprintf("plan/%d-1", i))
	}
	msg := e.Error()
	if !strings.Contains(msg, "and 15 more") {
		t.Errorf("message = %q, want the list truncated with a count", msg)
	}
	if !strings.Contains(msg, "plan/0-1") {
		t.Errorf("message = %q, want the first names spelled out", msg)
	}
}

// TestSyncDocsSplitsRealCorpusOverTheWire drives the actual client against a
// server, with a corpus several times syncBatchBytes: every request body must
// stay under the 1 MiB nginx/server cap that WL-83 hit.
func TestSyncDocsSplitsRealCorpusOverTheWire(t *testing.T) {
	var sizes []int
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sizes = append(sizes, len(b))
		var req struct {
			Docs []DocUpsert `json:"docs"`
		}
		if err := json.Unmarshal(b, &req); err != nil {
			t.Errorf("decode batch: %v", err)
		}
		rep := DocSyncReport{Results: []DocSyncResult{}}
		for _, d := range req.Docs {
			got = append(got, d.ref())
			rep.Updated++
			rep.Results = append(rep.Results, DocSyncResult{
				ID: "WL-SPEC-" + d.Ordinal, Kind: d.Kind, Outcome: "updated"})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rep)
	}))
	defer srv.Close()

	var docs []DocUpsert
	for i := 1; i <= 20; i++ {
		docs = append(docs, docOfSize(fmt.Sprint(i), 200<<10))
	}
	c := NewClient(Config{ServerURL: srv.URL, Token: "wl_" + strings.Repeat("a", 40)})
	rep, raw, err := c.SyncDocs(context.Background(), DocSyncInput{Project: "wl", Docs: docs})
	if err != nil {
		t.Fatalf("SyncDocs: %v", err)
	}
	if len(sizes) < 4 {
		t.Errorf("requests = %d, want the ~4 MiB corpus split into several", len(sizes))
	}
	for i, n := range sizes {
		if n >= 1<<20 {
			t.Errorf("request %d body = %d bytes, at or over the 1 MiB cap", i, n)
		}
	}
	if len(got) != len(docs) || rep.Updated != len(docs) {
		t.Errorf("sent %d docs, report has %d updated, want %d", len(got), rep.Updated, len(docs))
	}
	var round DocSyncReport
	if err := json.Unmarshal(raw, &round); err != nil || len(round.Results) != len(docs) {
		t.Errorf("raw report = %s (err %v), want the merged report", raw, err)
	}
}

func TestSyncBatchBytesLeavesHeadroomUnderOneMiB(t *testing.T) {
	if syncBatchBytes >= 1<<20 {
		t.Fatalf("syncBatchBytes = %d, want real headroom under the 1 MiB nginx/server cap", syncBatchBytes)
	}
}
