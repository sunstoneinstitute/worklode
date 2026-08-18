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
	ctx := context.Background()
	if err := st.CreateActor(ctx, "alice", "human", "Alice", true); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, "alice", "test token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
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
