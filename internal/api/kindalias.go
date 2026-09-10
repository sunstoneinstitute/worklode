package api

import (
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// kindAliasSurfaces are the entry points that normalise a task kind, and the
// only values the surface label takes. Keeping them listed here bounds the
// label and lets initMetrics pre-initialise every series to zero, which is
// what makes "nothing has sent the alias" readable as a flat zero rather than
// as no-data.
var kindAliasSurfaces = []string{"create", "list", "claim_next", "promote", "web_form", "edit"}

// normalizeTaskKind applies ns.DeprecatedTaskKinds to a caller-supplied kind
// and counts the alias use. Normalising in the handler rather than the CLI is
// deliberate: the web form, the plugin and `lode inbox import` all reach the
// same gate. surface must be one of kindAliasSurfaces.
func (s *server) normalizeTaskKind(kind, surface string) string {
	current, aliased := ns.NormalizeTaskKind(kind)
	if !aliased {
		return kind
	}
	if s.kindAliasUses != nil {
		s.kindAliasUses.WithLabelValues(kind, surface).Inc()
	}
	return current
}

// normalizeTaskKindList applies normalizeTaskKind to each element of a
// comma-separated kind filter (025 §8.8) and returns the normalised list,
// rejoined. ok is false when an element is not a task kind, and bad names
// that element, so the caller's 422 can point at it rather than at the whole
// list. An empty filter stays empty and is ok: it means "any kind".
//
// An empty element is one of the rejections, not a value to drop. A trailing
// comma that silently emptied the list would turn a tier filter into "claim
// anything", which is the leak §8.8 exists to close.
func (s *server) normalizeTaskKindList(list, surface string) (normalized, bad string, ok bool) {
	if strings.TrimSpace(list) == "" {
		return "", "", true
	}
	parts := strings.Split(list, ",")
	for i, p := range parts {
		k := s.normalizeTaskKind(strings.TrimSpace(p), surface)
		if !validKinds[k] {
			return "", k, false
		}
		parts[i] = k
	}
	return strings.Join(parts, ","), "", true
}
