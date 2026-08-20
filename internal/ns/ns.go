// This is the package's hand-written half — gen.go carries the enums and the
// package doc. What lives here is the two shapes every caller of an enum
// needs, so that deriving a gate from a generated list stays shorter than
// re-typing the list.

package ns

import "strings"

// Set turns a generated enum into the lookup map a validation gate wants.
func Set(values []string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

// OrList renders an enum the way this codebase's 422 bodies name a closed set
// — "draft, accepted, or superseded" — so a generated message reads like the
// hand-written ones it replaces.
func OrList(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " or " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + ", or " + values[len(values)-1]
	}
}
