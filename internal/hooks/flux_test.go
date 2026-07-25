package hooks_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const fluxTestSecret = "test-flux-secret"

// fluxSeededSHA is the source_sha shared by the kustomization_succeeded.json
// and kustomization_failed.json fixtures, so tests can seed a matching
// artifact and assert it gets linked.
const fluxSeededSHA = "abc1230000000000000000000000000000000000"

// fluxEnv is a webhook test fixture: a real store and the Flux handler with
// a single-cluster map (prod-1 -> prod). Raw SQL assertions go through the
// store's own connection pool.
type fluxEnv struct {
	st *store.Store
	h  http.Handler
}

func newFluxEnv(t *testing.T) *fluxEnv {
	t.Helper()
	st := store.OpenTestStore(t)

	return &fluxEnv{
		st: st,
		h:  hooks.NewFluxHandler(st, fluxTestSecret, map[string]string{"prod-1": "prod"}, nil),
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
	err := e.st.DBForTests().QueryRow(query, args...).Scan(dest...)
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
	var n int
	if err := e.st.DBForTests().QueryRow(query, args...).Scan(&n); err != nil {
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
		`SELECT status, artifact_id FROM deployments WHERE environment = $1 AND target_kind = $2 AND target_name = $3`,
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
	return e.rawQueryInt(t, `SELECT COUNT(*) FROM runtime_events WHERE kind = $1`, kind)
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
	ok := e.rawQueryRow(t, []any{&typ}, `SELECT type FROM events WHERE source = 'flux' AND external_id = $1`, extID)
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
		st := store.OpenTestStore(t)
		h := hooks.NewFluxHandler(st, fluxTestSecret, map[string]string{"prod-1": "prod"}, nil)
		e := &fluxEnv{st: st, h: h}

		rr := fluxDeliverBody(t, e.h, mkBody("staging-9"))
		if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
			t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
		}
		if _, _, ok := e.deployment(t, "dev", "flux_kustomization", "flux-system/demo"); !ok {
			t.Fatalf("no deployment row in environment dev (unmapped cluster fallback)")
		}
	})
}

// Delivery-confirmation tests. These drive the GitHub and Flux handlers over
// one store: the GitHub side records main commits and the gh watermark, the
// Flux side records the flux watermark, and the resolver gates on both.

// Shas from the github push fixtures: mainMergeSHA is the head of
// push_main_merge.json, markerSHA the single commit in push_main_marker.json
// (which lands later, so it has a higher main_commits id).
const (
	mainMergeSHA = "3333333333333333333333333333333333333333"
	markerSHA    = "4444444444444444444444444444444444444444"
)

// fluxHandlerFor returns a Flux handler sharing e's store, so GitHub and Flux
// events land in the same database.
func fluxHandlerFor(e *env, clusterEnv map[string]string) http.Handler {
	return hooks.NewFluxHandler(e.st, fluxTestSecret, clusterEnv, nil)
}

// fluxBody builds a Kustomization event. Distinct shas yield distinct bodies,
// which is what the handler dedupes on.
func fluxBody(reason, severity, cluster, sha string) []byte {
	return []byte(`{
		"involvedObject": {"kind": "Kustomization", "namespace": "flux-system", "name": "demo"},
		"severity": "` + severity + `",
		"timestamp": "2026-07-19T10:00:00Z",
		"message": "Applied revision: main@sha1:` + sha + `",
		"reason": "` + reason + `",
		"metadata": {"revision": "main@sha1:` + sha + `", "cluster": "` + cluster + `"}
	}`)
}

// fluxDeliverOK posts a Flux body and requires a clean "ok" — an apply that
// errors rolls its whole transaction back, which otherwise looks
// indistinguishable from "the handler correctly did nothing".
func fluxDeliverOK(t *testing.T, h http.Handler, body []byte) {
	t.Helper()
	rr := fluxDeliverBody(t, h, body)
	if rr.Code != http.StatusOK || fluxStatus(t, rr) != "ok" {
		t.Fatalf("flux deliver: code=%d body=%s", rr.Code, rr.Body.String())
	}
}

// deploymentStatusBody builds a successful GitHub deployment_status payload.
func deploymentStatusBody(environment, sha string) []byte {
	return []byte(`{
		"action": "created",
		"deployment_status": {"state": "success"},
		"deployment": {"environment": "` + environment + `", "sha": "` + sha + `"},
		"repository": {"full_name": "sunstoneinstitute/demo", "default_branch": "main"}
	}`)
}

// envDeploy reads the watermark row for demoRepo/environment; ok is false if
// there is none.
func (e *env) envDeploy(t *testing.T, environment string) (gh, flux sql.NullInt64, fluxSeen, ok bool) {
	t.Helper()
	err := e.st.DBForTests().QueryRow(
		`SELECT gh_main_id, flux_main_id, flux_seen FROM env_deploys
		 WHERE repo = $1 AND environment = $2`, demoRepo, environment).
		Scan(&gh, &flux, &fluxSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return gh, flux, false, false
	}
	if err != nil {
		t.Fatalf("env_deploys %s: %v", environment, err)
	}
	return gh, flux, fluxSeen, true
}

// TestFluxSuccessConfirmsFrontier: a Flux reconciliation of a known main
// commit sets the flux watermark and latches flux_seen. The task was already
// deployed_dev on the GitHub signal alone (bootstrap fallback), so the Flux
// event must not move it.
func TestFluxSuccessConfirmsFrontier(t *testing.T) {
	e := newEnv(t)
	fh := fluxHandlerFor(e, map[string]string{"dev-cluster": "dev"})
	taskID := e.seedTask(t) // WL-1
	e.claimTask(t, taskID)

	deliverPushOK(t, e, "d-1", "push_branch.json")
	deliverPushOK(t, e, "d-2", "push_main_merge.json")
	deliverOK(t, e, "deployment_status", "d-3", "deployment_status_success.json")
	if st := e.taskState(t, taskID); st != "deployed_dev" {
		t.Fatalf("task state before flux = %q, want deployed_dev (gh signal alone)", st)
	}
	if _, _, seen, _ := e.envDeploy(t, "dev"); seen {
		t.Fatal("flux_seen before any Flux event = true, want false")
	}

	fluxDeliverOK(t, fh, fluxBody("ReconciliationSucceeded", "info", "dev-cluster", mainMergeSHA))

	head := e.mainCommitID(t, mainMergeSHA)
	gh, flux, seen, ok := e.envDeploy(t, "dev")
	if !ok {
		t.Fatal("no env_deploys row for dev")
	}
	if !seen {
		t.Fatal("flux_seen = false, want true (a correlated Flux revision latches it)")
	}
	if !flux.Valid || flux.Int64 != int64(head) {
		t.Fatalf("flux_main_id = %v, want %d", flux, head)
	}
	if !gh.Valid || gh.Int64 != int64(head) {
		t.Fatalf("gh_main_id = %v, want %d (unchanged)", gh, head)
	}
	if st := e.taskState(t, taskID); st != "deployed_dev" {
		t.Fatalf("task state after flux = %q, want deployed_dev (idempotent)", st)
	}
	// The deployments table keeps its own behaviour alongside the frontier.
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM deployments WHERE environment = 'dev' AND status = 'deployed'`); n != 1 {
		t.Fatalf("deployed deployments rows = %d, want 1", n)
	}
}

// TestFluxGatesAfterFirstSeen: once a repo/env has seen a Flux revision, the
// confirmed frontier is min(gh, flux). A task landed above the flux watermark
// stays merged even though GitHub reported it deployed — and moves as soon as
// Flux catches up.
func TestFluxGatesAfterFirstSeen(t *testing.T) {
	e := newEnv(t)
	fh := fluxHandlerFor(e, map[string]string{"dev-cluster": "dev"})
	taskID := e.seedTask(t) // WL-1
	e.claimTask(t, taskID)

	// Two main commits, both attributed to the task; markerSHA is the newer,
	// so the task's landed commit is the marker one.
	deliverPushOK(t, e, "d-1", "push_main_merge.json")
	deliverPushOK(t, e, "d-2", "push_main_marker.json")
	flent := e.mainCommitID(t, mainMergeSHA)
	newer := e.mainCommitID(t, markerSHA)
	if newer <= flent {
		t.Fatalf("marker main id = %d, want > %d", newer, flent)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state after pushes = %q, want merged", st)
	}

	// Flux confirms the older commit — the watermark tracks the reconciled
	// revision, not the repo's head. No gh watermark yet, so with flux_seen
	// latched nothing is confirmed: the frontier needs both signals.
	fluxDeliverOK(t, fh, fluxBody("ReconciliationSucceeded", "info", "dev-cluster", mainMergeSHA))
	_, flux, seen, ok := e.envDeploy(t, "dev")
	if !ok || !seen || !flux.Valid || flux.Int64 != int64(flent) {
		t.Fatalf("after flux: ok=%v flux_seen=%v flux_main_id=%v, want true/true/%d", ok, seen, flux, flent)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state after flux-only = %q, want merged (no gh signal)", st)
	}

	// GitHub confirms the newer commit. min(gh, flux) is still the older one.
	rr := deliverBody(t, e.h, "deployment_status", "d-3", deploymentStatusBody("dev", markerSHA))
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("deployment_status: code=%d body=%s", rr.Code, rr.Body.String())
	}
	gh, flux, seen, ok := e.envDeploy(t, "dev")
	if !ok || !seen || !gh.Valid || gh.Int64 != int64(newer) || !flux.Valid || flux.Int64 != int64(flent) {
		t.Fatalf("watermarks = gh %v flux %v seen=%v ok=%v, want gh %d flux %d", gh, flux, seen, ok, newer, flent)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state = %q, want merged (gh confirmed %d but flux only %d)", st, newer, flent)
	}

	// Flux catches up: the task advances, proving the gate — not something
	// else — was holding it back.
	fluxDeliverOK(t, fh, fluxBody("ReconciliationSucceeded", "info", "dev-cluster", markerSHA))
	if _, flux, _, _ := e.envDeploy(t, "dev"); !flux.Valid || flux.Int64 != int64(newer) {
		t.Fatalf("flux_main_id after catch-up = %v, want %d", flux, newer)
	}
	if st := e.taskState(t, taskID); st != "deployed_dev" {
		t.Fatalf("task state after flux catch-up = %q, want deployed_dev", st)
	}
}

// TestFluxFailureDoesNotConfirm: only a successful reconciliation is a
// delivery signal. A failure must neither move the watermark nor latch
// flux_seen, which would gate the repo/env on a signal that never arrived.
func TestFluxFailureDoesNotConfirm(t *testing.T) {
	e := newEnv(t)
	fh := fluxHandlerFor(e, map[string]string{"dev-cluster": "dev"})
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	deliverPushOK(t, e, "d-1", "push_main_merge.json")

	fluxDeliverOK(t, fh, fluxBody("ReconciliationFailed", "error", "dev-cluster", mainMergeSHA))

	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM env_deploys`); n != 0 {
		t.Fatalf("env_deploys rows = %d, want 0", n)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state = %q, want merged", st)
	}
	// Existing failure handling is untouched.
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM deployments WHERE environment = 'dev' AND status = 'failed'`); n != 1 {
		t.Fatalf("failed deployments rows = %d, want 1", n)
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM runtime_events WHERE kind = 'flux_failure'`); n != 1 {
		t.Fatalf("flux_failure runtime events = %d, want 1", n)
	}
}

// TestFluxUnknownRevisionDoesNotLatch: a revision we cannot correlate to a
// main commit is a clean no-op. Latching flux_seen on it would gate the
// repo/env forever on a signal we are unable to confirm.
func TestFluxUnknownRevisionDoesNotLatch(t *testing.T) {
	e := newEnv(t)
	fh := fluxHandlerFor(e, map[string]string{"dev-cluster": "dev"})
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	deliverPushOK(t, e, "d-1", "push_main_merge.json")

	const unknownSHA = "9999999999999999999999999999999999999999"
	fluxDeliverOK(t, fh, fluxBody("ReconciliationSucceeded", "info", "dev-cluster", unknownSHA))

	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM env_deploys`); n != 0 {
		t.Fatalf("env_deploys rows = %d, want 0 (revision correlates to nothing)", n)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state = %q, want merged", st)
	}
	// The deployment itself is still recorded.
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM deployments WHERE environment = 'dev' AND status = 'deployed'`); n != 1 {
		t.Fatalf("deployed deployments rows = %d, want 1", n)
	}
}

// TestFluxUnknownEnvironmentSkipsConfirmation: LODE_CLUSTER_ENV_MAP is
// unvalidated operator config, so a cluster can map to something env_deploys
// rejects. The delivery must still succeed and the deployment still be
// recorded.
func TestFluxUnknownEnvironmentSkipsConfirmation(t *testing.T) {
	e := newEnv(t)
	fh := fluxHandlerFor(e, map[string]string{"staging-1": "staging"})
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	deliverPushOK(t, e, "d-1", "push_main_merge.json")

	fluxDeliverOK(t, fh, fluxBody("ReconciliationSucceeded", "info", "staging-1", mainMergeSHA))

	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM env_deploys`); n != 0 {
		t.Fatalf("env_deploys rows = %d, want 0", n)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state = %q, want merged", st)
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM deployments WHERE environment = 'staging'`); n != 1 {
		t.Fatalf("staging deployments rows = %d, want 1", n)
	}
}

// TestFluxMountedOnServer proves server.go routes POST /hooks/flux to the
// handler without bearer auth (the HMAC is the auth).
func TestFluxMountedOnServer(t *testing.T) {
	e := newFluxEnv(t)
	h, _, err := api.NewServer(e.st, api.Config{
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
