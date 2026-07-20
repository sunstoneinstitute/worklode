package hooks_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sunstoneinstitute/work-tracker/internal/api"
	"github.com/sunstoneinstitute/work-tracker/internal/hooks"
	"github.com/sunstoneinstitute/work-tracker/internal/store"
)

const fluxTestSecret = "test-flux-secret"

// fluxSeededSHA is the source_sha shared by the kustomization_succeeded.json
// and kustomization_failed.json fixtures, so tests can seed a matching
// artifact and assert it gets linked.
const fluxSeededSHA = "abc1230000000000000000000000000000000000"

// fluxEnv is a webhook test fixture: a real store, the Flux handler with a
// single-cluster map (prod-1 -> prod), and the db path for raw SQL
// assertions.
type fluxEnv struct {
	st     *store.Store
	h      http.Handler
	dbPath string
}

func newFluxEnv(t *testing.T) *fluxEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "wt.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(store.MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	return &fluxEnv{
		st:     st,
		h:      hooks.NewFluxHandler(st, fluxTestSecret, map[string]string{"prod-1": "prod"}, nil),
		dbPath: dbPath,
	}
}

// seedArtifact inserts an artifact with the given source_sha and returns its
// id.
func (e *fluxEnv) seedArtifact(t *testing.T, sha string) int64 {
	t.Helper()
	var id int64
	_, _, err := e.st.RecordEvent(t.Context(), "github", "seed-"+sha, "release.published", nil,
		func(tx *sql.Tx, _ int64) error {
			var err error
			id, err = store.CreateArtifact(tx, store.Artifact{
				Kind:      "docker_image",
				Name:      "sunstoneinstitute/demo",
				Version:   "v1.2.3",
				Repo:      "sunstoneinstitute/demo",
				SourceSHA: sha,
			})
			return err
		})
	if err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	return id
}

func fluxSign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(fluxTestSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func fluxFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "flux", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// fluxDeliver posts a signed Flux event to the handler. fixtureFile "" means
// body is used verbatim; otherwise the named testdata file is the body.
func fluxDeliver(t *testing.T, h http.Handler, fixtureFile string) *httptest.ResponseRecorder {
	t.Helper()
	return fluxDeliverBody(t, h, fluxFixture(t, fixtureFile))
}

func fluxDeliverBody(t *testing.T, h http.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/hooks/flux", bytes.NewReader(body))
	req.Header.Set("X-Signature", fluxSign(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func fluxStatus(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	return m["status"]
}

func (e *fluxEnv) rawQueryRow(t *testing.T, dest []any, query string, args ...any) bool {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+e.dbPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer db.Close()
	err = db.QueryRow(query, args...).Scan(dest...)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("raw query %q: %v", query, err)
	}
	return true
}

func (e *fluxEnv) rawQueryInt(t *testing.T, query string, args ...any) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+e.dbPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("raw query %q: %v", query, err)
	}
	return n
}

func (e *fluxEnv) eventCount(t *testing.T) int {
	return e.rawQueryInt(t, `SELECT COUNT(*) FROM events WHERE source = 'flux'`)
}

// deployment reads (status, artifact_id, environment) for one target; ok is
// false if no row exists.
func (e *fluxEnv) deployment(t *testing.T, environment, targetKind, targetName string) (status string, artifactID *int64, ok bool) {
	t.Helper()
	var st string
	var aid sql.NullInt64
	ok = e.rawQueryRow(t, []any{&st, &aid},
		`SELECT status, artifact_id FROM deployments WHERE environment = ? AND target_kind = ? AND target_name = ?`,
		environment, targetKind, targetName)
	if !ok {
		return "", nil, false
	}
	if aid.Valid {
		artifactID = &aid.Int64
	}
	return st, artifactID, true
}

func (e *fluxEnv) runtimeEventCount(t *testing.T, kind string) int {
	return e.rawQueryInt(t, `SELECT COUNT(*) FROM runtime_events WHERE kind = ?`, kind)
}

func TestFluxSignatureRejected(t *testing.T) {
	e := newFluxEnv(t)
	body := fluxFixture(t, "kustomization_succeeded.json")

	t.Run("missing signature", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/hooks/flux", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		e.h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rr.Code)
		}
	})
	t.Run("bad signature", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/hooks/flux", bytes.NewReader(body))
		req.Header.Set("X-Signature", "sha256="+hex.EncodeToString(make([]byte, 32)))
		rr := httptest.NewRecorder()
		e.h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rr.Code)
		}
	})
	if n := e.eventCount(t); n != 0 {
		t.Fatalf("events recorded for rejected deliveries = %d, want 0", n)
	}
}

func TestFluxEmptySecretIs503(t *testing.T) {
	e := newFluxEnv(t)
	h := hooks.NewFluxHandler(e.st, "", map[string]string{"prod-1": "prod"}, nil)
	rr := fluxDeliver(t, h, "kustomization_succeeded.json")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestFluxOversizedBody413(t *testing.T) {
	e := newFluxEnv(t)
	body := bytes.Repeat([]byte("a"), 5<<20+1)
	rr := fluxDeliverBody(t, e.h, body)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
	if n := e.eventCount(t); n != 0 {
		t.Fatalf("events recorded for oversized body = %d, want 0", n)
	}
}

func TestFluxIdempotency(t *testing.T) {
	e := newFluxEnv(t)
	rr := fluxDeliver(t, e.h, "kustomization_succeeded.json")
	if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
		t.Fatalf("first delivery: code=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = fluxDeliver(t, e.h, "kustomization_succeeded.json")
	if rr.Code != http.StatusOK || fluxStatus(t, rr) != "duplicate" {
		t.Fatalf("second delivery: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := e.eventCount(t); n != 1 {
		t.Fatalf("event rows = %d, want 1", n)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM deployments`); n != 1 {
		t.Fatalf("deployment rows = %d, want 1", n)
	}
}

func TestFluxKustomizationSucceededWithArtifact(t *testing.T) {
	e := newFluxEnv(t)
	artifactID := e.seedArtifact(t, fluxSeededSHA)

	rr := fluxDeliver(t, e.h, "kustomization_succeeded.json")
	if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	status, aid, ok := e.deployment(t, "prod", "flux_kustomization", "flux-system/demo")
	if !ok {
		t.Fatalf("no deployment row for flux-system/demo")
	}
	if status != "deployed" {
		t.Fatalf("status = %q, want deployed", status)
	}
	if aid == nil || *aid != artifactID {
		t.Fatalf("artifact_id = %v, want %d", aid, artifactID)
	}
}

func TestFluxKustomizationSucceededNoArtifactMatch(t *testing.T) {
	e := newFluxEnv(t)

	rr := fluxDeliver(t, e.h, "kustomization_succeeded.json")
	if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	status, aid, ok := e.deployment(t, "prod", "flux_kustomization", "flux-system/demo")
	if !ok {
		t.Fatalf("no deployment row for flux-system/demo")
	}
	if status != "deployed" {
		t.Fatalf("status = %q, want deployed", status)
	}
	if aid != nil {
		t.Fatalf("artifact_id = %v, want nil (no seeded artifact)", *aid)
	}
}

func TestFluxKustomizationFailedRecordsFailureAndRuntimeEvent(t *testing.T) {
	e := newFluxEnv(t)
	artifactID := e.seedArtifact(t, fluxSeededSHA)

	rr := fluxDeliver(t, e.h, "kustomization_failed.json")
	if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	status, aid, ok := e.deployment(t, "prod", "flux_kustomization", "flux-system/demo")
	if !ok {
		t.Fatalf("no deployment row for flux-system/demo")
	}
	if status != "failed" {
		t.Fatalf("status = %q, want failed", status)
	}
	if aid == nil || *aid != artifactID {
		t.Fatalf("artifact_id = %v, want %d", aid, artifactID)
	}
	if n := e.runtimeEventCount(t, "flux_failure"); n != 1 {
		t.Fatalf("flux_failure runtime events = %d, want 1", n)
	}

	var message, workload, cluster string
	ok = e.rawQueryRow(t, []any{&message, &workload, &cluster},
		`SELECT message, workload, cluster FROM runtime_events WHERE kind = 'flux_failure'`)
	if !ok {
		t.Fatalf("no flux_failure row")
	}
	if message != "Health check failed after 5m0s" || workload != "flux-system/demo" || cluster != "prod-1" {
		t.Fatalf("flux_failure row: message=%q workload=%q cluster=%q", message, workload, cluster)
	}
}

func TestFluxRecoveryAfterFailure(t *testing.T) {
	e := newFluxEnv(t)

	rr := fluxDeliver(t, e.h, "kustomization_failed.json")
	if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
		t.Fatalf("failed delivery: code=%d body=%s", rr.Code, rr.Body.String())
	}
	status, _, ok := e.deployment(t, "prod", "flux_kustomization", "flux-system/demo")
	if !ok || status != "failed" {
		t.Fatalf("precondition: status = %q ok=%v, want failed", status, ok)
	}

	// A different body (later timestamp) than kustomization_succeeded.json,
	// so it hashes to a different idempotency key.
	recoveryBody := []byte(`{
		"involvedObject": {"kind": "Kustomization", "namespace": "flux-system", "name": "demo"},
		"severity": "info",
		"timestamp": "2026-07-19T10:15:00Z",
		"message": "Applied revision: main@sha1:abc1230000000000000000000000000000000000",
		"reason": "ReconciliationSucceeded",
		"metadata": {
			"revision": "main@sha1:abc1230000000000000000000000000000000000",
			"cluster": "prod-1"
		}
	}`)
	rr = fluxDeliverBody(t, e.h, recoveryBody)
	if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
		t.Fatalf("recovery delivery: code=%d body=%s", rr.Code, rr.Body.String())
	}

	status, _, ok = e.deployment(t, "prod", "flux_kustomization", "flux-system/demo")
	if !ok || status != "deployed" {
		t.Fatalf("status after recovery = %q ok=%v, want deployed", status, ok)
	}
	if n := e.runtimeEventCount(t, "flux_recovery"); n != 1 {
		t.Fatalf("flux_recovery runtime events = %d, want 1", n)
	}
	if n := e.runtimeEventCount(t, "flux_failure"); n != 1 {
		t.Fatalf("flux_failure runtime events = %d, want 1 (unchanged)", n)
	}
}

func TestFluxHelmReleaseUsesFluxKustomizationTargetKind(t *testing.T) {
	e := newFluxEnv(t)

	rr := fluxDeliver(t, e.h, "helmrelease_succeeded.json")
	if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	status, aid, ok := e.deployment(t, "prod", "flux_kustomization", "flux-system/demo-helm")
	if !ok {
		t.Fatalf("no deployment row for flux-system/demo-helm")
	}
	if status != "deployed" {
		t.Fatalf("status = %q, want deployed", status)
	}
	if aid != nil {
		t.Fatalf("artifact_id = %v, want nil (sha not seeded)", *aid)
	}
}

func TestFluxUnknownKindIgnored(t *testing.T) {
	e := newFluxEnv(t)

	rr := fluxDeliver(t, e.h, "unknown_kind.json")
	if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ignored" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM deployments`); n != 0 {
		t.Fatalf("deployment rows = %d, want 0", n)
	}
	if n := e.eventCount(t); n != 1 {
		t.Fatalf("event rows = %d, want 1 (still recorded)", n)
	}

	var typ string
	body := fluxFixture(t, "unknown_kind.json")
	sum := sha256.Sum256(body)
	extID := hex.EncodeToString(sum[:])
	ok := e.rawQueryRow(t, []any{&typ}, `SELECT type FROM events WHERE source = 'flux' AND external_id = ?`, extID)
	if !ok {
		t.Fatalf("no event row for unknown-kind delivery")
	}
	if typ != "flux.GitRepository.GitOperationSucceeded.ignored" {
		t.Fatalf("event type = %q, want flux.GitRepository.GitOperationSucceeded.ignored", typ)
	}
}

func TestFluxOtherReasonSetsReconciling(t *testing.T) {
	e := newFluxEnv(t)
	body := []byte(`{
		"involvedObject": {"kind": "Kustomization", "namespace": "flux-system", "name": "demo"},
		"severity": "info",
		"timestamp": "2026-07-19T10:00:00Z",
		"message": "Reconciliation in progress",
		"reason": "Progressing",
		"metadata": {"cluster": "prod-1"}
	}`)
	rr := fluxDeliverBody(t, e.h, body)
	if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	status, _, ok := e.deployment(t, "prod", "flux_kustomization", "flux-system/demo")
	if !ok || status != "reconciling" {
		t.Fatalf("status = %q ok=%v, want reconciling", status, ok)
	}
}

func TestFluxClusterEnvResolution(t *testing.T) {
	mkBody := func(cluster string) []byte {
		meta := `"revision": "main@sha1:abc1230000000000000000000000000000000000"`
		if cluster != "" {
			meta += `, "cluster": "` + cluster + `"`
		}
		return []byte(`{
			"involvedObject": {"kind": "Kustomization", "namespace": "flux-system", "name": "demo"},
			"severity": "info",
			"timestamp": "2026-07-19T10:00:00Z",
			"message": "Applied revision",
			"reason": "ReconciliationSucceeded",
			"metadata": {` + meta + `}
		}`)
	}

	t.Run("mapped cluster", func(t *testing.T) {
		e := newFluxEnv(t) // clusterEnv: {"prod-1": "prod"}
		rr := fluxDeliverBody(t, e.h, mkBody("prod-1"))
		if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
			t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
		}
		if _, _, ok := e.deployment(t, "prod", "flux_kustomization", "flux-system/demo"); !ok {
			t.Fatalf("no deployment row in environment prod")
		}
	})

	t.Run("no cluster, single-entry map", func(t *testing.T) {
		e := newFluxEnv(t) // clusterEnv: {"prod-1": "prod"}
		rr := fluxDeliverBody(t, e.h, mkBody(""))
		if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
			t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
		}
		if _, _, ok := e.deployment(t, "prod", "flux_kustomization", "flux-system/demo"); !ok {
			t.Fatalf("no deployment row in environment prod (single-entry fallback)")
		}
	})

	t.Run("unmapped cluster defaults to dev", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "wt.db")
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		if err := st.Migrate(store.MigrationsDirForTests()); err != nil {
			t.Fatalf("migrate store: %v", err)
		}
		t.Cleanup(func() { st.Close() })
		h := hooks.NewFluxHandler(st, fluxTestSecret, map[string]string{"prod-1": "prod"}, nil)
		e := &fluxEnv{st: st, h: h, dbPath: dbPath}

		rr := fluxDeliverBody(t, e.h, mkBody("staging-9"))
		if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
			t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
		}
		if _, _, ok := e.deployment(t, "dev", "flux_kustomization", "flux-system/demo"); !ok {
			t.Fatalf("no deployment row in environment dev (unmapped cluster fallback)")
		}
	})
}

// TestFluxMountedOnServer proves server.go routes POST /hooks/flux to the
// handler without bearer auth (the HMAC is the auth).
func TestFluxMountedOnServer(t *testing.T) {
	e := newFluxEnv(t)
	h, err := api.NewServer(e.st, api.Config{
		FluxWebhookSecret: fluxTestSecret,
		ClusterEnvMap:     map[string]string{"prod-1": "prod"},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	rr := fluxDeliver(t, h, "kustomization_succeeded.json")
	if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}
