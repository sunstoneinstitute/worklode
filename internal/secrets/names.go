// Package secrets implements the client-side half of spec 017 (task-declared
// secrets): the org catalog format, the OS keystore holding materialized
// values, the names-only manifest, and the op-run env-file template. The
// package never logs, serializes, or persists a secret value — values exist
// only in process environments and the OS keystore.
package secrets

import (
	"regexp"
	"strings"
)

// nameRE is the spec-017 secret-name grammar: env-var style, org-unique.
var nameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// loaderPrefixes are the dynamic-loader namespaces (glibc, dyld). Every
// variable in them exists to alter how code is loaded, so the whole namespace
// is denied rather than an enumeration that new loader releases outrun.
var loaderPrefixes = []string{"LD_", "DYLD_"}

// loaderNames are the loader-sensitive variables outside those namespaces:
// shell command resolution and startup files, glibc's non-LD_ module paths,
// and language-runtime module paths and startup hooks. ADR 047 §3 holds the
// list and the reason each entry is on it; the boundary is "decides what code
// gets loaded", not "tunes behaviour". `_JAVA_OPTIONS` needs no entry — the
// grammar requires a leading letter, so it never validated.
var loaderNames = map[string]bool{
	"PATH": true, "CDPATH": true, "IFS": true, "ENV": true, "BASH_ENV": true,
	"GCONV_PATH": true, "LOCPATH": true, "NLSPATH": true,
	"PYTHONPATH": true, "PYTHONHOME": true, "PYTHONSTARTUP": true,
	"NODE_OPTIONS": true, "NODE_PATH": true,
	"PERL5LIB": true, "PERLLIB": true, "PERL5OPT": true,
	"RUBYLIB": true, "RUBYOPT": true,
	"CLASSPATH": true, "JAVA_TOOL_OPTIONS": true, "JDK_JAVA_OPTIONS": true,
}

// LoaderSensitive reports whether s names a variable that redirects how a
// process loads or resolves code. `lode secret exec` assigns every
// materialized name into the child environment (017 §4), so such a name would
// make the secret's value code the child loads rather than a credential it
// holds. Exact and case-sensitive: the grammar admits upper-case only, so
// case variants are already rejected a step earlier.
func LoaderSensitive(s string) bool {
	for _, p := range loaderPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return loaderNames[s]
}

// ValidName reports whether s is a well-formed secret name: it matches the
// grammar and is not loader-sensitive (ADR 047 §2). Everything that stores or
// transmits secret names (task field, event payload, catalog keys, keystore
// items, the manifest) gates on this one function, which is what keeps values
// and op:// refs out of those channels by construction — and what keeps the
// deny-list from being three lists that drift.
func ValidName(s string) bool { return nameRE.MatchString(s) && !LoaderSensitive(s) }

// taskIDRE is the task-id grammar, anchored. internal/worktree has the same
// pattern unanchored, because there it extracts an id from a directory name
// rather than validating one; borrowing it here would accept "../WL-1/..".
// This package needs the validating form and is a leaf, so it carries its own
// rather than making internal/worktree export an extractor for a guard.
var taskIDRE = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

// ValidTaskID reports whether s is a well-formed task id. The manifest path
// and the keystore service name are both built from it, so a traversing or
// empty id must never reach either.
func ValidTaskID(s string) bool { return taskIDRE.MatchString(s) }
