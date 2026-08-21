package secrets

import (
	"slices"
	"strings"
)

// keepNames are the shell-plumbing variables the child keeps whatever else
// matches below: without them a child cannot find binaries, a home directory,
// a temp directory or a locale. They are checked before the deny rules, so a
// deny pattern can never grow far enough to break the plumbing by accident.
// `SSH_AUTH_SOCK` is deliberately here rather than denied — the Linux keystore
// (017 §3) is encrypted to a key held in ssh-agent, and git push over ssh
// needs it, so stripping it would break `lode` itself in the child.
var keepNames = map[string]bool{
	"PATH": true, "HOME": true, "SHELL": true, "USER": true, "LOGNAME": true,
	"PWD": true, "OLDPWD": true, "TMPDIR": true, "TERM": true, "TERMINFO": true,
	"LANG": true, "LANGUAGE": true, "TZ": true, "COLUMNS": true, "LINES": true,
	"SSH_AUTH_SOCK": true, "SSH_AGENT_PID": true,
}

// keepPrefixes are namespaces kept whole for the same reason: locale settings
// and the XDG base directories the child's tools resolve their own state from.
var keepPrefixes = []string{"LC_", "XDG_"}

// denyPrefixes are namespaces that exist to carry an identity, and whose
// non-credential members select which ambient credential is used (`AWS_PROFILE`
// picks a key pair out of `~/.aws`). A task that needs one of these declares it
// (017 §2) and gets the materialized value injected; inheriting the operator's
// is the least-privilege failure ADR 050 removes.
var denyPrefixes = []string{
	"AWS_", "AZURE_", "GCP_", "CLOUDSDK_",
	"ANTHROPIC_", "OPENAI_",
	"VAULT_", "OP_",
}

// denyNames are credential-bearing variables whose names carry none of the
// tokens below: each points at a file of ambient credentials.
var denyNames = map[string]bool{
	"KUBECONFIG": true, "NETRC": true, "DOCKER_CONFIG": true, "PGPASSFILE": true,
}

// denyTokens are the credential-shaped substrings. Substring rather than
// suffix matching, because the shape appears in every position
// (`TOKEN_FOR_X`, `X_TOKEN`, `GH_TOKEN_2`).
var denyTokens = []string{
	"TOKEN", "SECRET", "PASSWORD", "PASSWD", "PASSPHRASE",
	"CREDENTIAL", "AUTH", "APIKEY",
}

// CredentialShaped reports whether an inherited environment variable name
// looks like a credential and must not reach a `lode secrets exec` child.
// Case-insensitive: the secret-name grammar is upper-case only (017 §1), but
// an inherited name is whatever the operator's shell exported.
//
// The rule is deny-by-shape, not allow-by-list (ADR 050 §2): the child keeps
// the operator's environment minus anything credential-shaped. It is therefore
// best-effort — a credential in a variable named for none of these shapes
// still passes — and defence in depth over the positive rule that 017 §4
// already states, never a substitute for it.
func CredentialShaped(name string) bool {
	n := strings.ToUpper(name)
	if keepNames[n] {
		return false
	}
	for _, p := range keepPrefixes {
		if strings.HasPrefix(n, p) {
			return false
		}
	}
	if denyNames[n] {
		return true
	}
	for _, p := range denyPrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	for _, t := range denyTokens {
		if strings.Contains(n, t) {
			return true
		}
	}
	// A bare `_KEY` tail is credential-shaped (`SIGNING_KEY`, `API_KEY`);
	// "KEY" alone is not, or `KEYCLOAK_URL` and `KEYBOARD_LAYOUT` would go.
	return strings.HasSuffix(n, "_KEY") || strings.Contains(n, "_KEY_")
}

// ChildEnv returns the environment for a `lode secrets exec` child: parent
// with every assignment to one of names stripped, then every credential-shaped
// inherited assignment stripped, then injected appended.
//
// Stripping names is what makes injected authoritative: execve keeps duplicate
// entries and getenv returns the first, so appending alone would hand the child
// the operator's ambient value instead of the task's (017 §4: "not the
// operator's shell environment"). injected is appended after both passes, so a
// materialized name that is itself credential-shaped — most of them are — is
// unaffected by the deny rules.
func ChildEnv(parent, names, injected []string) []string {
	out := make([]string, 0, len(parent)+len(injected))
	for _, kv := range parent {
		if k, _, ok := strings.Cut(kv, "="); ok && (slices.Contains(names, k) || CredentialShaped(k)) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, injected...)
}
