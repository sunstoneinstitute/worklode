package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/secrets"
)

// opRunFunc executes the materialization step: ONE `op run` resolving every
// reference in envFile under a single 1Password authorization, with
// `lode secrets pack` as the child. Swapped in tests.
var opRunFunc = runOpPack

// opLookPathFunc locates the op binary. Swapped in tests so they never
// depend on the 1Password CLI actually being installed in the environment
// running the suite (it is not part of this repo's CI image).
var opLookPathFunc = func() (string, error) { return exec.LookPath("op") }

func runOpPack(dir, envFile, taskID string, names, declined []string, stdout, stderr io.Writer) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate lode binary: %w", err)
	}
	args := []string{"run", "--env-file", envFile, "--",
		self, "secrets", "pack", "--task", taskID, "--names", strings.Join(names, ",")}
	if len(declined) > 0 {
		args = append(args, "--declined", strings.Join(declined, ","))
	}
	cmd := exec.Command("op", args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// runSecretsCeremony is the spec-017 claim-time ceremony: fetch the catalog,
// resolve declared ∪ baseline names, take one consent for the non-baseline
// set, materialize under one op authorization, and record a names-only
// event. It NEVER fails the claim — every failure degrades to a stderr
// warning, and a needed-but-unavailable secret later becomes a block signal,
// not a prompt. All output goes to stderr so --json stdout stays clean.
func runSecretsCeremony(ctx context.Context, cmd *cobra.Command, c *cli.Client, taskID, dir string, declared []string) {
	errw := cmd.ErrOrStderr()

	resp, _, err := c.SecretsCatalog(ctx)
	if err != nil {
		// A server without the catalog feature would otherwise warn on every
		// claim; only tasks that declared names need to hear about it.
		if len(declared) > 0 {
			fmt.Fprintf(errw, "secrets: catalog unavailable (%v) — credentialed steps will block\n", err)
		}
		return
	}
	catalog := &secrets.Catalog{}
	for _, e := range resp.Secrets {
		catalog.Entries = append(catalog.Entries, secrets.Entry{
			Name: e.Name, Ref: e.Ref, Description: e.Description, Baseline: e.Baseline,
		})
	}

	baseline, consentSet, missing := catalog.Resolve(declared)
	for _, name := range missing {
		fmt.Fprintf(errw, "secrets: %s is declared but not in the catalog — add it via the deployment repo\n", name)
	}
	if len(baseline)+len(consentSet) == 0 {
		return
	}

	var declined []string
	consented := consentSet
	if len(consentSet) > 0 && !consentToSecrets(cmd, consentSet) {
		declined = entryNames(consentSet)
		consented = nil
	}

	pack := append(append([]secrets.Entry{}, baseline...), consented...)
	if len(pack) == 0 {
		if err := secrets.SaveManifest(secrets.Manifest{Task: taskID, Declined: declined}); err != nil {
			fmt.Fprintf(errw, "secrets: record declined names: %v\n", err)
		}
		fmt.Fprintf(errw, "secrets: declined %s — credentialed steps will block\n", strings.Join(declined, ", "))
		return
	}

	if _, err := opLookPathFunc(); err != nil {
		fmt.Fprintln(errw, "secrets: 1Password CLI not found — install `op`, sign in, then `lode resume` to materialize")
		return
	}

	envFile := filepath.Join(dir, ".worklode", "secrets.env")
	if err := secrets.WriteEnvFile(envFile, pack); err != nil {
		fmt.Fprintf(errw, "secrets: %v\n", err)
		return
	}
	excludeSecretsEnv(dir)

	names := entryNames(pack)
	if err := opRunFunc(dir, envFile, taskID, names, declined, cmd.OutOrStdout(), errw); err != nil {
		fmt.Fprintf(errw, "secrets: materialization failed: %v — signed in to op? `lode resume` re-runs the ceremony\n", err)
		return
	}
	if err := c.RecordSecretsMaterialized(ctx, taskID, names); err != nil {
		fmt.Fprintf(errw, "secrets: record materialization event: %v\n", err)
	}
	fmt.Fprintf(errw, "secrets: materialized %s\n", strings.Join(names, ", "))
	if len(declined) > 0 {
		fmt.Fprintf(errw, "secrets: declined %s\n", strings.Join(declined, ", "))
	}
}

// consentToSecrets shows the non-baseline set and takes one yes/no for it.
// Without a terminal (agent-run `lode next`, --json pipelines) the answer is
// "no": the claim still succeeds, and `lode resume` in a terminal — where the
// operator is present by definition — re-runs the ceremony.
func consentToSecrets(cmd *cobra.Command, entries []secrets.Entry) bool {
	errw := cmd.ErrOrStderr()
	fmt.Fprintln(errw, "This task declares secrets:")
	for _, e := range entries {
		fmt.Fprintf(errw, "  %-28s %s\n", e.Name, e.Description)
	}
	if f, ok := cmd.InOrStdin().(*os.File); ok && !term.IsTerminal(int(f.Fd())) {
		fmt.Fprintln(errw, "secrets: no terminal for consent — declined; `lode resume` in a terminal to materialize")
		return false
	}
	fmt.Fprint(errw, "Materialize into the OS keystore for unattended use? [y/N] ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// secretsSatisfied reports whether a resume can skip the ceremony: a manifest
// exists, every materialized name is still in the keystore, and every
// declared name was either materialized or explicitly declined.
func secretsSatisfied(taskID string, declared []string) bool {
	m, ok := secrets.LoadManifest(taskID)
	if !ok {
		return len(declared) == 0
	}
	for _, n := range m.Materialized {
		if _, err := secrets.Fetch(taskID, n); err != nil {
			return false
		}
	}
	for _, n := range declared {
		if !slices.Contains(m.Materialized, n) && !slices.Contains(m.Declined, n) {
			return false
		}
	}
	return true
}

// excludeSecretsEnv adds .worklode/secrets.env to the repo's local ignore
// file (info/exclude in the common git dir) so the refs-only template is
// never committed. Best-effort: any failure is silent.
func excludeSecretsEnv(dir string) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return
	}
	exclude := filepath.Join(strings.TrimSpace(string(out)), "info", "exclude")
	const line = ".worklode/secrets.env"
	if data, err := os.ReadFile(exclude); err == nil && strings.Contains(string(data), line) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// entryNames projects entries to their names.
func entryNames(entries []secrets.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}
