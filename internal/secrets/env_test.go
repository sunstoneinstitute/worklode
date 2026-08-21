package secrets

import (
	"slices"
	"strings"
	"testing"
)

func TestCredentialShaped(t *testing.T) {
	denied := []string{
		// The names ADR 050 §1 names outright.
		"ANTHROPIC_API_KEY", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN", "GOOGLE_APPLICATION_CREDENTIALS",
		// Namespaces whose non-credential members select a credential.
		"AWS_PROFILE", "AZURE_CLIENT_ID", "OP_SERVICE_ACCOUNT_TOKEN",
		"VAULT_ADDR", "OPENAI_API_KEY", "ANTHROPIC_BASE_URL",
		// Shape, wherever it sits in the name.
		"GITHUB_TOKEN", "GH_TOKEN", "TOKEN_FOR_REGISTRY", "NPM_TOKEN",
		"STRIPE_SECRET_KEY", "PGPASSWORD", "SSH_PASSPHRASE",
		"DOCKER_AUTH_CONFIG", "SLACK_APIKEY", "GIT_SIGNING_KEY",
		// Files of ambient credentials.
		"KUBECONFIG", "NETRC", "DOCKER_CONFIG", "PGPASSFILE",
		// Whatever case the operator's shell exported it in.
		"aws_secret_access_key", "anthropic_api_key",
	}
	for _, n := range denied {
		if !CredentialShaped(n) {
			t.Errorf("CredentialShaped(%q) = false; want it stripped", n)
		}
	}

	kept := []string{
		// Plumbing the child needs to function.
		"PATH", "HOME", "SHELL", "USER", "PWD", "TMPDIR", "TERM",
		"LANG", "LC_ALL", "LC_TIME", "TZ", "XDG_RUNTIME_DIR",
		"SSH_AUTH_SOCK", "SSH_AGENT_PID",
		// Ordinary configuration that happens to sit near the patterns.
		"KEYCLOAK_URL", "GITHUB_REPOSITORY", "GOPATH", "EDITOR", "COLORTERM",
	}
	for _, n := range kept {
		if CredentialShaped(n) {
			t.Errorf("CredentialShaped(%q) = true; want it inherited", n)
		}
	}
}

// TestChildEnvStripsAmbientCredentials is the acceptance criterion (017 §4, as
// amended by ADR 050): a child of `lode secrets exec` sees its materialized
// names plus the shell plumbing, and never the operator's ambient credentials.
func TestChildEnvStripsAmbientCredentials(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/op",
		"LANG=en_US.UTF-8",
		"ANTHROPIC_API_KEY=test-value",
		"AWS_ACCESS_KEY_ID=AKIAOPERATOR",
		"GOOGLE_APPLICATION_CREDENTIALS=/home/op/gcp.json",
	}
	got := ChildEnv(parent, []string{"A_TOKEN"}, []string{"A_TOKEN=from-keystore"})

	for _, want := range []string{"PATH=/usr/bin:/bin", "HOME=/home/op", "LANG=en_US.UTF-8", "A_TOKEN=from-keystore"} {
		if !slices.Contains(got, want) {
			t.Errorf("child env is missing %q: %v", want, got)
		}
	}
	for _, kv := range got {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "ANTHROPIC_API_KEY", "AWS_ACCESS_KEY_ID", "GOOGLE_APPLICATION_CREDENTIALS":
			t.Errorf("ambient credential %q reached the child: %q", k, kv)
		}
	}
	// Not just absent by name: the value must be gone from the environment
	// under any name.
	for _, kv := range got {
		if strings.Contains(kv, "test-value") || strings.Contains(kv, "AKIAOPERATOR") {
			t.Errorf("ambient credential value survived in %q", kv)
		}
	}
}

// TestChildEnvInjectsCredentialShapedNames: nearly every materialized name is
// credential-shaped by construction, so the deny pass must apply to inherited
// assignments only.
func TestChildEnvInjectsCredentialShapedNames(t *testing.T) {
	parent := []string{"PATH=/bin", "GITHUB_TOKEN=operators-own", "AWS_PROFILE=personal"}
	names := []string{"GITHUB_TOKEN", "AWS_PROFILE"}
	injected := []string{"GITHUB_TOKEN=from-keystore", "AWS_PROFILE=task-profile"}

	got := ChildEnv(parent, names, injected)
	for _, want := range injected {
		if !slices.Contains(got, want) {
			t.Errorf("materialized assignment %q did not reach the child: %v", want, got)
		}
	}
	// One entry per name, and it is the task's: execve keeps duplicates and
	// getenv returns the first, so a surviving ambient entry would win.
	if n := count(got, "GITHUB_TOKEN"); n != 1 {
		t.Errorf("GITHUB_TOKEN entries = %d; want exactly 1", n)
	}
	if slices.Contains(got, "GITHUB_TOKEN=operators-own") {
		t.Error("the operator's ambient GITHUB_TOKEN survived alongside the task's")
	}
}

// TestChildEnvKeepsMalformedEntries: an entry with no '=' names nothing, so it
// cannot be judged by name. Passing it through matches execve's own tolerance.
func TestChildEnvKeepsMalformedEntries(t *testing.T) {
	got := ChildEnv([]string{"MALFORMED", "PATH=/bin"}, nil, nil)
	if !slices.Contains(got, "MALFORMED") {
		t.Errorf("child env = %v; want the malformed entry passed through", got)
	}
}

func count(env []string, name string) int {
	n := 0
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && k == name {
			n++
		}
	}
	return n
}
