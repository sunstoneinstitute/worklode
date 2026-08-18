// Package secrets implements the client-side half of spec 017 (task-declared
// secrets): the org catalog format, the OS keystore holding materialized
// values, the names-only manifest, and the op-run env-file template. The
// package never logs, serializes, or persists a secret value — values exist
// only in process environments and the OS keystore.
package secrets

import "regexp"

// nameRE is the spec-017 secret-name grammar: env-var style, org-unique.
var nameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// ValidName reports whether s is a well-formed secret name. Everything that
// stores or transmits secret names (task field, event payload, catalog keys)
// gates on this, which is what keeps values and op:// refs out of those
// channels by construction.
func ValidName(s string) bool { return nameRE.MatchString(s) }
