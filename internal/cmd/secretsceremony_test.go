package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/secrets"
)

const ceremonyCatalogJSON = `{"secrets":[
  {"name":"GITHUB_TOKEN","ref":"op://Employee/GitHub agent token/credential","description":"gh","baseline":true},
  {"name":"KUBECONFIG_HZDEV","ref":"op://Infrastructure/hzdev kubeconfig/kubeconfig","description":"hzdev",	"baseline":false},
  {"name":"OPENALEX_API_KEY","ref":"op://Infrastructure/openalex/key","description":"openalex","baseline":false}
]}`

// ceremonyFixture returns a client against a stub server (catalog +
// materialized-event recording), a cobra command with buffered IO, and the
// recorded-names channel.
func ceremonyFixture(t *testing.T, catalogStatus int, stdin string) (*cli.Client, *cobra.Command, *bytes.Buffer, *[]string) {
	t.Helper()
	var recorded []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/secrets/catalog":
			w.WriteHeader(catalogStatus)
			if catalogStatus == http.StatusOK {
				io.WriteString(w, ceremonyCatalogJSON)
			} else {
				io.WriteString(w, `{"error":"boom"}`)
			}
		case strings.HasSuffix(r.URL.Path, "/secrets-materialized"):
			var req struct {
				Names []string `json:"names"`
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &req)
			recorded = req.Names
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := &cobra.Command{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(errBuf)
	cmd.SetIn(strings.NewReader(stdin))
	return cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_test"}), cmd, errBuf, &recorded
}

// fakeOp simulates op run + lode secrets pack: it stores a dummy value per
// name and writes the manifest, exactly as the real pack child would.
func fakeOp(t *testing.T, calls *int, capturedEnvFile *string) func(dir, envFile, taskID string, names, declined []string, stdout, stderr io.Writer) error {
	return func(dir, envFile, taskID string, names, declined []string, stdout, stderr io.Writer) error {
		*calls++
		data, err := os.ReadFile(envFile)
		if err != nil {
			t.Errorf("read env file: %v", err)
		}
		*capturedEnvFile = string(data)
		for _, n := range names {
			if err := secrets.Put(taskID, n, "resolved-"+n); err != nil {
				return err
			}
		}
		return secrets.SaveManifest(secrets.Manifest{Task: taskID, Materialized: names, Declined: declined})
	}
}

func TestCeremonyOneOpRunMaterializesAll(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	c, cmd, errBuf, recorded := ceremonyFixture(t, http.StatusOK, "y\n")

	calls, envFile := 0, ""
	restore := opRunFunc
	opRunFunc = fakeOp(t, &calls, &envFile)
	defer func() { opRunFunc = restore }()
	restoreLookPath := opLookPathFunc
	opLookPathFunc = func() (string, error) { return "op", nil }
	defer func() { opLookPathFunc = restoreLookPath }()

	runSecretsCeremony(context.Background(), cmd, c, "WL-7", dir,
		[]string{"KUBECONFIG_HZDEV", "OPENALEX_API_KEY", "NOT_IN_CATALOG"})

	if calls != 1 {
		t.Fatalf("op run called %d times; want exactly 1 (one authorization)", calls)
	}
	// One baseline + two consented, resolved to refs — references only.
	for _, want := range []string{
		"GITHUB_TOKEN=op://Employee/GitHub agent token/credential",
		"KUBECONFIG_HZDEV=op://Infrastructure/hzdev kubeconfig/kubeconfig",
		"OPENALEX_API_KEY=op://Infrastructure/openalex/key",
	} {
		if !strings.Contains(envFile, want) {
			t.Errorf("env file missing %q:\n%s", want, envFile)
		}
	}
	if strings.Contains(envFile, "resolved-") {
		t.Fatal("env file contains a value")
	}
	if len(*recorded) != 3 {
		t.Fatalf("recorded names = %v; want the 3 materialized names", *recorded)
	}
	if !strings.Contains(errBuf.String(), "NOT_IN_CATALOG") {
		t.Fatalf("no warning for the unknown name:\n%s", errBuf.String())
	}
	if _, err := secrets.Fetch("WL-7", "GITHUB_TOKEN"); err != nil {
		t.Fatalf("baseline secret not in keystore: %v", err)
	}
}

func TestCeremonyDeclineSkipsConsentedSet(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	c, cmd, _, recorded := ceremonyFixture(t, http.StatusOK, "n\n")

	calls, envFile := 0, ""
	restore := opRunFunc
	opRunFunc = fakeOp(t, &calls, &envFile)
	defer func() { opRunFunc = restore }()
	restoreLookPath := opLookPathFunc
	opLookPathFunc = func() (string, error) { return "op", nil }
	defer func() { opLookPathFunc = restoreLookPath }()

	runSecretsCeremony(context.Background(), cmd, c, "WL-7", dir, []string{"KUBECONFIG_HZDEV"})

	// Baseline still packs (exempt from consent); the declined name does not.
	if calls != 1 {
		t.Fatalf("op run called %d times; want 1 (baseline only)", calls)
	}
	if strings.Contains(envFile, "KUBECONFIG_HZDEV") {
		t.Fatal("declined name reached the env file")
	}
	m, ok := secrets.LoadManifest("WL-7")
	if !ok || !slices.Contains(m.Declined, "KUBECONFIG_HZDEV") {
		t.Fatalf("manifest = %+v; want KUBECONFIG_HZDEV declined", m)
	}
	if slices.Contains(*recorded, "KUBECONFIG_HZDEV") {
		t.Fatal("declined name was recorded as materialized")
	}
}

func TestCeremonyCatalogUnavailableDegrades(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	c, cmd, errBuf, _ := ceremonyFixture(t, http.StatusInternalServerError, "")

	calls, envFile := 0, ""
	restore := opRunFunc
	opRunFunc = fakeOp(t, &calls, &envFile)
	defer func() { opRunFunc = restore }()

	runSecretsCeremony(context.Background(), cmd, c, "WL-7", t.TempDir(), []string{"KUBECONFIG_HZDEV"})
	if calls != 0 {
		t.Fatal("op run called though the catalog was unavailable")
	}
	if !strings.Contains(errBuf.String(), "catalog unavailable") {
		t.Fatalf("no degradation warning:\n%s", errBuf.String())
	}

	// No declared names + no catalog ⇒ silent (no noise on servers without
	// the feature).
	errBuf.Reset()
	runSecretsCeremony(context.Background(), cmd, c, "WL-7", t.TempDir(), nil)
	if errBuf.Len() != 0 {
		t.Fatalf("expected silence, got:\n%s", errBuf.String())
	}
}
