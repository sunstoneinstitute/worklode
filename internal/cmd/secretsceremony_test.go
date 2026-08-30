package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
  {"name":"KUBECONFIG_HZDEV","ref":"op://Infrastructure/hzdev kubeconfig/kubeconfig","description":"hzdev","baseline":false},
  {"name":"OPENALEX_API_KEY","ref":"op://Infrastructure/openalex/key","description":"openalex","baseline":false}
]}`

// ceremonyFixture is ceremonyFixtureWithCatalog against the standard catalog.
func ceremonyFixture(t *testing.T, catalogStatus int, stdin string) (*cli.Client, *cobra.Command, *bytes.Buffer, *bytes.Buffer, *[]string) {
	t.Helper()
	return ceremonyFixtureWithCatalog(t, catalogStatus, ceremonyCatalogJSON, stdin)
}

// ceremonyFixtureWithCatalog returns a client against a stub server (catalog +
// materialized-event recording), a cobra command with buffered IO, that
// command's stdout and stderr buffers, and the recorded names.
//
// stdout is a real buffer rather than io.Discard because the ceremony's
// contract is that it leaves it untouched: `lode worktree next --json` marshals its
// document to the same stream.
func ceremonyFixtureWithCatalog(t *testing.T, catalogStatus int, catalogJSON, stdin string) (*cli.Client, *cobra.Command, *bytes.Buffer, *bytes.Buffer, *[]string) {
	t.Helper()
	var recorded []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/secrets/catalog":
			w.WriteHeader(catalogStatus)
			if catalogStatus == http.StatusOK {
				io.WriteString(w, catalogJSON)
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
	outBuf, errBuf := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetErr(errBuf)
	cmd.SetIn(strings.NewReader(stdin))
	return cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_test"}), cmd, outBuf, errBuf, &recorded
}

// fakeOp simulates op run + lode secrets pack: it stores a dummy value per
// name, writes the manifest, and prints pack's own success line to the stdout
// writer it is handed — exactly as the real pack child would.
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
		if err := secrets.SaveManifest(secrets.Manifest{Task: taskID, Materialized: names, Declined: declined}); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "packed %d secrets for %s\n", len(names), taskID)
		return nil
	}
}

func TestCeremonyOneOpRunMaterializesAll(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	c, cmd, outBuf, errBuf, recorded := ceremonyFixture(t, http.StatusOK, "y\n")

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
	// `lode worktree next --json` marshals its document to this stream after the
	// ceremony returns; a single stray line from the pack child breaks every
	// parser downstream.
	if outBuf.Len() != 0 {
		t.Fatalf("ceremony wrote to stdout: %q", outBuf.String())
	}
}

func TestCeremonyDeclineSkipsConsentedSet(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	c, cmd, _, _, recorded := ceremonyFixture(t, http.StatusOK, "n\n")

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
	c, cmd, _, errBuf, _ := ceremonyFixture(t, http.StatusInternalServerError, "")

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

// TestCeremonyAutoDeclineLeavesDeclaredUnsatisfied covers the agent path:
// `/lode:next` runs from a non-tty Bash tool, so consent is auto-declined. A
// persisted decline would satisfy the declared name forever and `lode worktree resume`
// would never retry — so an auto-decline must record nothing.
func TestCeremonyAutoDeclineLeavesDeclaredUnsatisfied(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	c, cmd, _, _, _ := ceremonyFixture(t, http.StatusOK, "")

	// An *os.File that is not a terminal — what an agent-run `lode worktree next` gets.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	w.Close()
	cmd.SetIn(r)

	calls, envFile := 0, ""
	restore := opRunFunc
	opRunFunc = fakeOp(t, &calls, &envFile)
	defer func() { opRunFunc = restore }()
	restoreLookPath := opLookPathFunc
	opLookPathFunc = func() (string, error) { return "op", nil }
	defer func() { opLookPathFunc = restoreLookPath }()

	runSecretsCeremony(context.Background(), cmd, c, "WL-8", dir, []string{"KUBECONFIG_HZDEV"})

	if strings.Contains(envFile, "KUBECONFIG_HZDEV") {
		t.Fatal("unconsented name reached the env file")
	}
	m, _ := secrets.LoadManifest("WL-8")
	if len(m.Declined) != 0 {
		t.Fatalf("auto-decline was persisted as Declined %v", m.Declined)
	}
	if secretsSatisfied("WL-8", []string{"KUBECONFIG_HZDEV"}) {
		t.Fatal("auto-decline satisfied the declared name; `lode worktree resume` would never retry")
	}
}

// TestCeremonyJSONNeverPrompts: a --json caller is a machine. Under a
// pty-wrapped unattended runner stdin passes term.IsTerminal, so the read
// would hang forever with the task already claimed.
func TestCeremonyJSONNeverPrompts(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	// stdin says yes; --json must not even look at it.
	c, cmd, _, _, _ := ceremonyFixture(t, http.StatusOK, "y\n")
	cmd.Flags().Bool("json", true, "")

	calls, envFile := 0, ""
	restore := opRunFunc
	opRunFunc = fakeOp(t, &calls, &envFile)
	defer func() { opRunFunc = restore }()
	restoreLookPath := opLookPathFunc
	opLookPathFunc = func() (string, error) { return "op", nil }
	defer func() { opLookPathFunc = restoreLookPath }()

	runSecretsCeremony(context.Background(), cmd, c, "WL-11", dir, []string{"KUBECONFIG_HZDEV"})

	if strings.Contains(envFile, "KUBECONFIG_HZDEV") {
		t.Fatal("--json consented on the operator's behalf")
	}
	m, _ := secrets.LoadManifest("WL-11")
	if len(m.Declined) != 0 {
		t.Fatalf("--json auto-decline was persisted as Declined %v", m.Declined)
	}
}

// TestSecretsSatisfiedRequiresAManifest: `lode worktree next` packs the baseline set
// whether or not the task declares anything, so "no manifest" is unfinished
// work even for a task with no declarations — resume must run the ceremony.
func TestSecretsSatisfiedRequiresAManifest(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	if secretsSatisfied("WL-10", nil) {
		t.Fatal("no manifest counted as satisfied; resume would never pack the baseline set")
	}
}

// ceremonyNoBaselineCatalogJSON has no baseline entry, so declining the
// consent set leaves nothing to pack — the branch that replaces the manifest
// without running op.
const ceremonyNoBaselineCatalogJSON = `{"secrets":[
  {"name":"KUBECONFIG_HZDEV","ref":"op://Infrastructure/hzdev kubeconfig/kubeconfig","description":"hzdev","baseline":false}
]}`

// TestCeremonyDeclinePurgesPreviouslyMaterialized: the manifest is the only
// record of what the keystore holds, so replacing it with a declined-only one
// must first remove the items the previous one listed. Otherwise they outlive
// the worktree in the OS keychain with no purge path able to name them.
func TestCeremonyDeclinePurgesPreviouslyMaterialized(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	c, cmd, _, _, _ := ceremonyFixtureWithCatalog(t, http.StatusOK, ceremonyNoBaselineCatalogJSON, "n\n")

	if err := secrets.Put("WL-9", "KUBECONFIG_HZDEV", "resolved"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{
		Task: "WL-9", Materialized: []string{"KUBECONFIG_HZDEV"},
	}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	calls, envFile := 0, ""
	restore := opRunFunc
	opRunFunc = fakeOp(t, &calls, &envFile)
	defer func() { opRunFunc = restore }()
	restoreLookPath := opLookPathFunc
	opLookPathFunc = func() (string, error) { return "op", nil }
	defer func() { opLookPathFunc = restoreLookPath }()

	runSecretsCeremony(context.Background(), cmd, c, "WL-9", dir, []string{"KUBECONFIG_HZDEV"})

	if calls != 0 {
		t.Fatalf("op run called %d times; nothing was left to pack", calls)
	}
	if _, err := secrets.Fetch("WL-9", "KUBECONFIG_HZDEV"); err == nil {
		t.Fatal("declining orphaned the previously materialized keystore item")
	}
	m, ok := secrets.LoadManifest("WL-9")
	if !ok || !slices.Contains(m.Declined, "KUBECONFIG_HZDEV") {
		t.Fatalf("manifest = %+v; want KUBECONFIG_HZDEV declined", m)
	}
	if len(m.Materialized) != 0 {
		t.Fatalf("manifest still lists materialized names: %v", m.Materialized)
	}
	if m.At.IsZero() {
		t.Fatal("declined manifest left At at the zero time")
	}
}
