// Package refkind is the one registry of the infixes in a <KEY>-<INFIX>-<n>
// reference ("WL-SPEC-77", "WL-REQ-12", "WL-MILE-2") and the kind of entity
// each one names. Every pattern, switch and redirect that reads a typed
// reference derives from All, so a new infix is added here once and every
// reader learns it. Task ids (<KEY>-<n>) carry no infix and are not here.
package refkind

import "strings"

// Target is the kind of entity an infix names.
type Target string

const (
	Doc         Target = "doc"
	Rule        Target = "rule"
	Milestone   Target = "milestone"
	Deliverable Target = "deliverable"
)

// Targets lists every Target, for tests that must cover each one.
var Targets = []Target{Doc, Rule, Milestone, Deliverable}

// The infixes. Rules share one counter, so REQ and RULE both resolve a rule
// by its number (WL-REQ-1642). The store keys a project's
// rule, milestone and deliverable counters by RULE, MILE and DEL.
const (
	SPEC = "SPEC"
	ADR  = "ADR"
	PLAN = "PLAN"
	REQ  = "REQ"
	RULE = "RULE"
	MILE = "MILE"
	DEL  = "DEL"
)

// Kind is one registered infix.
type Kind struct {
	Infix  string
	Target Target
}

// All is the registry.
var All = []Kind{
	{SPEC, Doc}, {ADR, Doc}, {PLAN, Doc},
	{REQ, Rule}, {RULE, Rule},
	{MILE, Milestone}, {DEL, Deliverable},
}

// KeyPattern is a project key's regexp (projects_key_format).
const KeyPattern = `[A-Z][A-Z0-9]{1,9}`

// Lookup returns the Target an infix names.
func Lookup(infix string) (Target, bool) {
	for _, k := range All {
		if k.Infix == infix {
			return k.Target, true
		}
	}
	return "", false
}

// Alternation is the regexp alternation of every infix naming one of
// targets, e.g. "SPEC|ADR|PLAN" for Doc. No group: the caller adds one.
func Alternation(targets ...Target) string {
	var out []string
	for _, k := range All {
		for _, t := range targets {
			if k.Target == t {
				out = append(out, k.Infix)
			}
		}
	}
	return strings.Join(out, "|")
}
