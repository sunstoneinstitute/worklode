//go:build e2e

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// --- the stub embedding provider ----------------------------------------
//
// Spec 040 §2.3's default deployment is an OpenAI-compatible sidecar, so the
// test stands one up rather than injecting a Go provider: the server reaches
// it over HTTP through internal/embed exactly as it reaches a real one. It is
// environment, like the Postgres this suite already needs — not a store write.
//
// The vectors are a hashed bag of words: every token lands in one of the 768
// buckets and the vector is L2-normalised, so cosine similarity is real token
// overlap rather than a constant. That is enough to rank, and it is
// deterministic, which is what makes the ranking assertions below stable.

// The width is the contract (§2.2): the server refuses a provider whose Dim()
// disagrees, and the vector(768) typmod refuses the row.
const stubEmbedDim = store.IndexDim

// stubVector is the deterministic 768-wide embedding of one text. Tokens
// split on non-alphanumerics, matching what Postgres' `simple` configuration
// does with an identifier: child_of is the two tokens child and of, in both
// arms.
func stubVector(text string) []float32 {
	vec := make([]float32, stubEmbedDim)
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, tok := range fields {
		h := fnv.New32a()
		h.Write([]byte(tok))
		vec[h.Sum32()%stubEmbedDim]++
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		// A zero-norm vector is refused by the store (cosine distance against
		// it is undefined), and an empty chunk is a legal thing to embed.
		vec[0] = 1
		return vec
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] = float32(float64(vec[i]) / norm)
	}
	return vec
}

// stubEmbeddings serves POST /v1/embeddings the way a text-embeddings-inference
// sidecar does.
func stubEmbeddings(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		type item struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		out := struct {
			Data []item `json:"data"`
		}{Data: make([]item, len(req.Input))}
		for i, in := range req.Input {
			out.Data[i] = item{Index: i, Embedding: stubVector(in)}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- the corpus -----------------------------------------------------------

// searchSpecBody is the fixture spec 040 §0 argues from, written out as a real
// document. sec-1 is what a semantic query has to find; sec-2 and sec-3 are
// the ranking inversion: sec-2 is prose *about* parents and children and wins
// the dense arm on the query `child_of`, while sec-3 is the section that
// actually defines the identifier and only the lexical arm ranks it first.
//
// sec-2 deliberately never puts "of" directly after "child": under the
// `simple` configuration `child_of` is the phrase child <-> of, so prose that
// happened to read "a child of a parent" would match it and the fixture would
// stop demonstrating anything (§6.2, §13.6).
const searchSpecBody = `---
status: draft
---

# Hierarchy and leases

How a worktree lease is held and how tasks are nested.

## 1. Worktree leases {#sec-1}

Leases and worktree pruning. A worktree lease is held by one agent.
Pruning a worktree releases the leases that worktree holds, and pruning is
how leases end.

## 2. Parent and child tasks {#sec-2}

A parent task of a project has a child task. The child task of a parent is a
task of that parent. Each parent of a project may hold a child task, and each
child task of a parent belongs to exactly one parent task of one project.

## 3. Hierarchy edges {#sec-3}

The child_of edge records which parent a task hangs under. Writing child_of
places a task in the hierarchy, and child_of is the only edge that describes
containment.
`

// The lease task, shared by both scenarios so the lexical assertions in the
// no-provider one are made against the same words the indexed one carries.
const (
	leaseTaskTitle = "Pruning a worktree must release the leases it holds"
	leaseTaskBody  = "Pruning a worktree releases the leases bound to that worktree. Leases that outlive pruning block the task."
)

// searchSkillMD is the SKILL.md the stub skill source ships. Skills are the
// third indexed kind and the only one with no create endpoint: they arrive
// through skill sync, which this test drives through POST /api/v1/skills/sync.
const searchSkillMD = `---
name: worktree-leases
description: How a worktree lease interacts with pruning
---

A lease is released when its worktree is pruned. Prune a worktree only after
the agent holding the lease has stopped, or the next claim races the one that
is still running.
`

// skillTarball builds the GitHub-shaped tarball skill sync unpacks: entries
// under a single "<owner>-<repo>-<sha>/" root, gzipped.
func skillTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	name := "acme-org-skills-e2e/skills/worktree-leases/SKILL.md"
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o644, Size: int64(len(searchSkillMD)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(searchSkillMD)); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// --- helpers --------------------------------------------------------------

// searchOrFail runs one search and fails the test on error.
func searchOrFail(t *testing.T, c *cli.Client, f cli.SearchFilter) model.SearchResponse {
	t.Helper()
	resp, _, err := c.Search(context.Background(), f)
	if err != nil {
		t.Fatalf("search %+v: %v", f, err)
	}
	return resp
}

// kindsOf lists the distinct subject kinds a result set covers.
func kindsOf(hits []model.SearchHit) map[string]bool {
	got := map[string]bool{}
	for _, h := range hits {
		got[h.Kind] = true
	}
	return got
}

// renderHits formats a result set for a failure message: address, fused
// score, and the two arm ranks that produced it.
func renderHits(hits []model.SearchHit) string {
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "\n  %-6s %-12s %-8s score=%.5f dense=%d lexical=%d %q",
			h.Kind, h.TaskID+h.Title, h.Anchor, h.Score, h.DenseRank, h.LexicalRank, h.Excerpt)
	}
	return b.String()
}

// eventually polls cond until it holds or the deadline passes, failing with
// msg. Convergence is a background loop (§7), so every assertion about the
// index is an assertion about where the loop has got to.
func eventually(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

// scrapeMetrics fetches the admin listener's /metrics and returns the body.
func scrapeMetrics(t *testing.T, admin http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics: status = %d", rec.Code)
	}
	return rec.Body.String()
}

// metricSum adds up every sample of one metric family, whatever its labels.
// The name has to match exactly up to the label set — worklode_index_chunks
// and worklode_index_chunks_without_vector are two families, and a prefix
// test would fold the second into the first.
func metricSum(t *testing.T, body, name string) float64 {
	t.Helper()
	var total float64
	for _, line := range strings.Split(body, "\n") {
		rest, ok := strings.CutPrefix(line, name)
		if !ok || (!strings.HasPrefix(rest, "{") && !strings.HasPrefix(rest, " ")) {
			continue
		}
		// "<name>{labels} <value>" or "<name> <value>": the value is the last
		// space-separated field.
		i := strings.LastIndex(line, " ")
		if i < 0 {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(line[i+1:]), 64)
		if err != nil {
			t.Fatalf("parse %q from /metrics line %q: %v", name, line, err)
		}
		total += v
	}
	return total
}

// --- the journey ----------------------------------------------------------

// TestSearchJourney drives spec 040 end to end over public surfaces only: a
// document, two tasks and a skill enter the corpus through the API and the
// skill-sync endpoint, the background convergence loop indexes them against a
// stub embeddings sidecar, and GET /api/v1/search answers over both arms.
//
// It discharges §13.1 (all three kinds reachable by one semantic query),
// §13.2 (the identifier query, and the inversion that proves the lexical arm
// is what fixes it), §13.4 (a doc hit's frozen anchor resolves, and both arm
// ranks come back) and §13.10 (a second pass over an unchanged corpus
// re-embeds nothing).
func TestSearchJourney(t *testing.T) {
	ctx := context.Background()

	embedSrv := stubEmbeddings(t)
	reg := prometheus.NewRegistry()
	st := store.OpenTestStore(t)

	// The convergence loop and the doc-lifecycle subscriber run until this is
	// cancelled; both must stop before the database goes away, so the cleanup
	// registered here runs before OpenTestStore's drop (cleanups are LIFO).
	loopCtx, cancelLoop := context.WithCancel(context.Background())
	t.Cleanup(cancelLoop)

	handler, adminHandler, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		Metrics:        reg,
		// The default deployment's shape (§2.3): an OpenAI-compatible
		// endpoint, no provider code of its own.
		EmbeddingURL:   embedSrv.URL + "/v1/embeddings",
		EmbeddingModel: "e2e-stub",
		// Skill sync is the only public surface that creates a skill. The
		// fetch is stubbed the way the embeddings endpoint is: the upstream
		// repo is environment, everything downstream of it is the real path.
		SkillSources: "acme/org-skills@main:skills/*",
		SkillFetchForTest: func(ctx context.Context, repo, ref string) ([]byte, error) {
			return skillTarball(t), nil
		},
		// A background context is what starts the convergence loop at all;
		// the interval keeps a pass close behind each write.
		BackgroundCtx: loopCtx,
		IndexInterval: 100 * time.Millisecond,
		EventPoll:     50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "search", Name: "Search E2E", Key: "SRCH",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// 1. A document with frozen section anchors (§1, §4.2).
	doc, _, err := admin.CreateDoc(ctx, model.CreateDocInput{
		Project: "search", Kind: "spec", Number: 1, Slug: "hierarchy-and-leases",
		Body: searchSpecBody,
	})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}

	// 2. Two tasks. The first is what the semantic query must find; the
	// second exists to give the dense arm a second strong candidate on
	// `child_of`, so the section that defines the identifier is not first
	// there (§0's inversion).
	leaseTask, _, err := admin.CreateTask(ctx, model.CreateTaskInput{
		Project: "search", Kind: "chore", Priority: "medium",
		Title: leaseTaskTitle, Body: leaseTaskBody,
	})
	if err != nil {
		t.Fatalf("create lease task: %v", err)
	}
	if _, _, err := admin.CreateTask(ctx, model.CreateTaskInput{
		Project: "search", Kind: "feature", Priority: "low",
		Title: "Order the child task list of a parent task",
		Body:  "Each child task of a parent task of a project keeps its place in the list of tasks of that parent.",
	}); err != nil {
		t.Fatalf("create hierarchy task: %v", err)
	}

	// 3. A skill, through the sync endpoint. A boot sync fired off the same
	// stub already; that one holds the mutex for a moment, and a 409 says so
	// rather than that anything failed.
	eventually(t, "skill sync to run", func() bool {
		_, _, err := admin.SyncSkills(ctx)
		return err == nil
	})

	// 4. Convergence. Nothing below writes chunk rows: the loop does, on its
	// own, from the rows the API wrote.
	semantic := "how do leases interact with worktree pruning"
	var semanticResp model.SearchResponse
	eventually(t, "the corpus to index and answer a semantic query across all three kinds", func() bool {
		semanticResp = searchOrFail(t, admin, cli.SearchFilter{Query: semantic, Limit: 20})
		got := kindsOf(semanticResp.Hits)
		return got["doc"] && got["task"] && got["skill"]
	})
	semanticHits := semanticResp.Hits

	// §13.1: one semantic query reaches a document, a task and a skill, and
	// the instance answers as a configured one rather than a degraded one.
	if semanticResp.Provider != "openai-compatible" {
		t.Fatalf("provider = %q, want openai-compatible: the stub sidecar is not wired in", semanticResp.Provider)
	}

	// §13.2: the identifier query. The lexical arm ranks the section that
	// literally contains child_of first; the dense arm does not, and the
	// fused ranking follows the lexical arm.
	hybrid := searchOrFail(t, admin, cli.SearchFilter{Query: "child_of", Limit: 20})
	if len(hybrid.Hits) == 0 {
		t.Fatal("hybrid search for child_of returned nothing")
	}
	top := hybrid.Hits[0]
	if top.Kind != "doc" || top.DocID != doc.ID || top.Anchor != "sec-3" {
		t.Fatalf("hybrid child_of ranks %s %s first, want the doc section that defines it (sec-3):%s",
			top.Kind, top.Anchor, renderHits(hybrid.Hits))
	}

	// The same query with the lexical arm switched off must NOT rank it
	// first. This is §0's measured inversion, kept as a regression test: if
	// someone weakens the lexical arm, hybrid stops being better than dense
	// and this fails rather than quietly degrading.
	dense := searchOrFail(t, admin, cli.SearchFilter{Query: "child_of", Mode: model.SearchDense, Limit: 20})
	if len(dense.Hits) == 0 {
		t.Fatal("dense search for child_of returned nothing: the fixture no longer exercises the inversion")
	}
	if d := dense.Hits[0]; d.Kind == "doc" && d.DocID == doc.ID && d.Anchor == "sec-3" {
		t.Fatalf("dense-only child_of already ranks sec-3 first, so the test no longer proves the lexical arm is what fixes it:%s",
			renderHits(dense.Hits))
	}
	// §13.4 (second half): the response carries both arm ranks, and here they
	// are the whole explanation of the result — the winner is the subject the
	// lexical arm ranked first and the dense arm did not, which is what
	// fusion is for. A fused score alone would make this result merely
	// pleasing; the two ranks make it checkable.
	if top.DenseRank <= 1 || top.LexicalRank != 1 {
		t.Fatalf("fused winner has dense_rank=%d lexical_rank=%d, want lexical 1 and dense below 1:%s",
			top.DenseRank, top.LexicalRank, renderHits(hybrid.Hits))
	}

	// §13.6, observed from outside: `child_of` must not match the prose in
	// sec-2, which reads "the child task of a parent". Under `english` it
	// would; under `simple` the query is the phrase child <-> of.
	lexical := searchOrFail(t, admin, cli.SearchFilter{Query: "child_of", Mode: model.SearchLexical, Limit: 20})
	for _, h := range lexical.Hits {
		if h.Kind == "doc" && h.DocID == doc.ID && h.Anchor == "sec-2" {
			t.Fatalf("the lexical arm matched prose about children on the query child_of; the text search configuration is not `simple`:%s",
				renderHits(lexical.Hits))
		}
	}

	// §13.4: a doc hit's anchor is a frozen address, and it resolves — the
	// same resolution `lode doc show --section` performs, over the same
	// public endpoint it reads the body from.
	var docHit *model.SearchHit
	for i := range semanticHits {
		if semanticHits[i].Kind == "doc" {
			docHit = &semanticHits[i]
			break
		}
	}
	if docHit == nil || docHit.Anchor == "" {
		t.Fatalf("no doc hit with an anchor to resolve:%s", renderHits(semanticHits))
	}
	detail, _, err := admin.GetDoc(ctx, docHit.DocID)
	if err != nil {
		t.Fatalf("get doc %d: %v", docHit.DocID, err)
	}
	if findDocSection(detail.Sections, docHit.Anchor) == nil {
		t.Fatalf("hit anchor %q is not one of the document's sections: %+v", docHit.Anchor, detail.Sections)
	}
	parsed, err := designdoc.Parse([]byte(detail.Body))
	if err != nil {
		t.Fatalf("parse doc body: %v", err)
	}
	section, ok := parsed.Subtree(docHit.Anchor)
	if !ok || strings.TrimSpace(section) == "" {
		t.Fatalf("--section %s does not resolve in the document body", docHit.Anchor)
	}
	if !strings.Contains(section, "{#"+docHit.Anchor+"}") {
		t.Fatalf("--section %s resolved to the wrong subtree:\n%s", docHit.Anchor, section)
	}

	// A task hit addresses its task: the semantic query found the lease task
	// specifically, and the id it reports reads back. (The doc side of the
	// same property is the anchor above.)
	foundLeaseTask := false
	for _, h := range semanticHits {
		if h.Kind != "task" || h.TaskID != leaseTask.ID {
			continue
		}
		foundLeaseTask = true
		if _, _, err := admin.GetTask(ctx, h.TaskID); err != nil {
			t.Fatalf("task hit %s does not read back: %v", h.TaskID, err)
		}
	}
	if !foundLeaseTask {
		t.Fatalf("the semantic query did not reach the lease task %s:%s", leaseTask.ID, renderHits(semanticHits))
	}

	// §13.10: convergence is idempotent. Wait for a pass that leaves nothing
	// stale, note how many subjects have been re-indexed, then let several
	// more passes run: an unchanged corpus must re-embed nothing and keep the
	// stale gauge at zero.
	var reembedBefore, passesBefore float64
	eventually(t, "a convergence pass that leaves nothing stale", func() bool {
		body := scrapeMetrics(t, adminHandler)
		if metricSum(t, body, "worklode_index_subjects_stale") != 0 {
			return false
		}
		// Read the counters from the same scrape that saw a quiet index, so
		// the baseline cannot include work from a pass still running.
		reembedBefore = metricSum(t, body, "worklode_index_reembed_total")
		passesBefore = metricSum(t, body, "worklode_index_convergence_duration_seconds_count")
		return reembedBefore > 0
	})
	eventually(t, "three more convergence passes", func() bool {
		body := scrapeMetrics(t, adminHandler)
		return metricSum(t, body, "worklode_index_convergence_duration_seconds_count") >= passesBefore+3
	})
	after := scrapeMetrics(t, adminHandler)
	if got := metricSum(t, after, "worklode_index_reembed_total"); got != reembedBefore {
		t.Fatalf("worklode_index_reembed_total moved from %v to %v over an unchanged corpus: convergence is re-embedding work it already did",
			reembedBefore, got)
	}
	if got := metricSum(t, after, "worklode_index_subjects_stale"); got != 0 {
		t.Fatalf("worklode_index_subjects_stale = %v after a settled corpus, want 0", got)
	}
	if got := metricSum(t, after, "worklode_index_chunks_without_vector"); got != 0 {
		t.Fatalf("worklode_index_chunks_without_vector = %v with a provider configured, want 0", got)
	}
}

// TestSearchWithoutProvider is §13.8: an instance with no embedding provider
// configured is not a degraded-to-nothing instance. It indexes the same
// corpus, answers with the lexical arm, and says so — provider "none", mode
// "lexical" — while returning real hits.
func TestSearchWithoutProvider(t *testing.T) {
	ctx := context.Background()

	reg := prometheus.NewRegistry()
	st := store.OpenTestStore(t)
	loopCtx, cancelLoop := context.WithCancel(context.Background())
	t.Cleanup(cancelLoop)

	handler, adminHandler, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		Metrics:        reg,
		// No EmbeddingURL: this is the whole point of the scenario.
		BackgroundCtx: loopCtx,
		IndexInterval: 100 * time.Millisecond,
		EventPoll:     50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "nolexical", Name: "No Provider", Key: "NOPR",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.CreateDoc(ctx, model.CreateDocInput{
		Project: "nolexical", Kind: "spec", Number: 1, Slug: "hierarchy-and-leases",
		Body: searchSpecBody,
	}); err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, _, err := admin.CreateTask(ctx, model.CreateTaskInput{
		Project: "nolexical", Kind: "chore", Priority: "medium",
		Title: leaseTaskTitle, Body: leaseTaskBody,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Real results, from a corpus indexed with no vectors at all.
	var resp model.SearchResponse
	eventually(t, "lexical-only search to answer over both subject kinds", func() bool {
		resp = searchOrFail(t, admin, cli.SearchFilter{Query: "worktree pruning leases", Limit: 20})
		got := kindsOf(resp.Hits)
		return got["doc"] && got["task"]
	})
	if resp.Provider != "none" {
		t.Fatalf("provider = %q, want none", resp.Provider)
	}
	if resp.Mode != model.SearchLexical {
		t.Fatalf("mode = %q, want lexical", resp.Mode)
	}
	for _, h := range resp.Hits {
		if h.DenseRank != 0 {
			t.Fatalf("hit carries a dense rank on an instance with no provider:%s", renderHits(resp.Hits))
		}
		if h.LexicalRank == 0 {
			t.Fatalf("hit carries no lexical rank, so it came from nowhere:%s", renderHits(resp.Hits))
		}
	}

	// Asking for hybrid, or even for dense, still answers lexically rather
	// than erroring: the response reports what it actually did (§11).
	forced := searchOrFail(t, admin, cli.SearchFilter{
		Query: "worktree pruning leases", Mode: model.SearchDense, Limit: 20})
	if forced.Mode != model.SearchLexical || forced.Provider != "none" || len(forced.Hits) == 0 {
		t.Fatalf("mode=dense with no provider: mode=%q provider=%q hits=%d, want a lexical answer with real hits",
			forced.Mode, forced.Provider, len(forced.Hits))
	}

	// The index converges and then goes quiet: with no provider, a chunk row
	// carrying no vector is finished work, not a subject to retry forever
	// (§8). Every row lacks a vector and nothing is stale.
	eventually(t, "the index to converge with no vectors", func() bool {
		body := scrapeMetrics(t, adminHandler)
		return metricSum(t, body, "worklode_index_chunks") > 0 &&
			metricSum(t, body, "worklode_index_subjects_stale") == 0
	})
	body := scrapeMetrics(t, adminHandler)
	chunks := metricSum(t, body, "worklode_index_chunks")
	if noVec := metricSum(t, body, "worklode_index_chunks_without_vector"); noVec != chunks {
		t.Fatalf("worklode_index_chunks_without_vector = %v of %v chunks, want all of them on an instance with no provider",
			noVec, chunks)
	}
}
