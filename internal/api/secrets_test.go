package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
)

// newSecretsTestServer is newTestServer with a secrets catalog on disk.
//
// WebOpen mirrors newTestServer deliberately rather than incidentally: it is
// the instance shape where anonymous access is most permissive, so a 401 from
// the catalog route proves /api/v1 requires a bearer token even there, instead
// of passing because nothing on the instance was open in the first place.
func newSecretsTestServer(t *testing.T, catalogTOML string) (http.Handler, string) {
	t.Helper()
	st := newTestStore(t)
	token := seedActor(t, st, "alice", "human", "Alice", true)
	path := filepath.Join(t.TempDir(), "catalog.toml")
	if err := os.WriteFile(path, []byte(catalogTOML), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	h, _, err := api.NewServer(st, api.Config{SecretsCatalogPath: path, WebOpen: true})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return h, token
}

const testCatalog = `[GITHUB_TOKEN]
ref = "op://Employee/GitHub agent token/credential"
description = "GitHub credential"
baseline = true

[KUBECONFIG_HZDEV]
ref = "op://Infrastructure/hzdev kubeconfig/kubeconfig"
description = "hzdev cluster access"
`

func TestSecretsCatalogRequiresAuth(t *testing.T) {
	h, _ := newSecretsTestServer(t, testCatalog)
	rec := doReq(t, h, http.MethodGet, "/api/v1/secrets/catalog", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d; want 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "op://") {
		t.Fatal("unauthenticated response leaked an op:// ref")
	}
}

func TestSecretsCatalogServed(t *testing.T) {
	h, token := newSecretsTestServer(t, testCatalog)
	rec := doReq(t, h, http.MethodGet, "/api/v1/secrets/catalog", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Secrets []struct {
			Name     string `json:"name"`
			Ref      string `json:"ref"`
			Baseline bool   `json:"baseline"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Secrets) != 2 || resp.Secrets[0].Name != "GITHUB_TOKEN" ||
		!resp.Secrets[0].Baseline || resp.Secrets[1].Ref == "" {
		t.Fatalf("catalog = %+v; want both entries with refs", resp.Secrets)
	}
}

func TestSecretsCatalogUnconfigured(t *testing.T) {
	_, h, token := newTestServer(t) // no SecretsCatalogPath
	rec := doReq(t, h, http.MethodGet, "/api/v1/secrets/catalog", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unconfigured catalog: %d; want 404", rec.Code)
	}
}

// The catalog volume is an optional Secret projected per environment, so a
// configured path with no file behind it is a normal state — an environment
// that has not provisioned the catalog item — and must read as 404, not as a
// server fault.
func TestSecretsCatalogMissingFile(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "alice", "human", "Alice", true); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, "alice", "test token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	path := filepath.Join(t.TempDir(), "catalog.toml") // never written
	h, _, err := api.NewServer(st, api.Config{SecretsCatalogPath: path})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	rec := doReq(t, h, http.MethodGet, "/api/v1/secrets/catalog", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing catalog file: %d %s; want 404", rec.Code, rec.Body.String())
	}
}

func TestSecretsMaterializedEvent(t *testing.T) {
	st, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects", token,
		map[string]string{"id": "secevt", "name": "SecEvt", "key": "SV"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project": "secevt", "title": "t", "priority": "medium", "kind": "chore",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task: %d %s", rec.Code, rec.Body.String())
	}
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode: %v", err)
	}

	names := []string{"GITHUB_TOKEN", "KUBECONFIG_HZDEV", "OPENALEX_API_KEY"}
	rec = doReq(t, h, http.MethodPost, "/api/v1/tasks/"+task.ID+"/secrets-materialized",
		token, map[string]any{"names": names})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("record: %d %s; want 204", rec.Code, rec.Body.String())
	}

	// The audit trail carries the names and nothing else (acceptance 3):
	// no op:// refs, no values, in the state log attributed to the event.
	logs, err := st.StateLogForEntity(context.Background(), "task", task.ID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	var found string
	for _, l := range logs {
		if strings.Contains(l.Change, "secrets_materialized") {
			found = l.Change
		}
	}
	if found == "" {
		t.Fatal("no secrets_materialized state-log entry")
	}
	for _, n := range names {
		if !strings.Contains(found, n) {
			t.Fatalf("state log %q missing name %s", found, n)
		}
	}
	if strings.Contains(found, "op://") {
		t.Fatalf("state log leaked an op:// ref: %q", found)
	}
}

func TestSecretsMaterializedRejectsNonNames(t *testing.T) {
	st, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects", token,
		map[string]string{"id": "secrej", "name": "SecRej", "key": "SR"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project": "secrej", "title": "t", "priority": "medium", "kind": "chore",
	})
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, bad := range [][]string{
		{},
		{"op://Employee/x/y"},
		{"a-value-not-a-name"},
	} {
		rec = doReq(t, h, http.MethodPost, "/api/v1/tasks/"+task.ID+"/secrets-materialized",
			token, map[string]any{"names": bad})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("names %v: %d; want 422", bad, rec.Code)
		}
	}
	// The rejection is the redaction guarantee: a rejected payload leaves no
	// trace, so an op:// ref or a raw value can never reach the event log.
	logs, err := st.StateLogForEntity(context.Background(), "task", task.ID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	for _, l := range logs {
		if strings.Contains(l.Change, "secrets_materialized") ||
			strings.Contains(l.Change, "op://") {
			t.Fatalf("rejected payload reached the state log: %q", l.Change)
		}
	}

	rec = doReq(t, h, http.MethodPost, "/api/v1/tasks/NOPE-1/secrets-materialized",
		token, map[string]any{"names": []string{"GITHUB_TOKEN"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown task: %d; want 404", rec.Code)
	}
}
