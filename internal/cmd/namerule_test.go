package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The closed top-level sets of 061 §1. Each is closed in the spec's own words
// — adding a member requires amending §1 — so each is transcribed here rather
// than derived from anything.
var (
	l1Entities = map[string]bool{ // L1: exactly what the backbone models, singular
		"actor": true, "approval": true, "blob": true, "channel": true,
		"doc": true, "event": true, "graph": true, "inbox": true,
		"project": true, "secret": true, "skill": true, "task": true,
		"token": true,
	}
	l2Machine = map[string]bool{ // L2: acts on this machine or this checkout
		"doctor": true, "install": true, "uninstall": true,
		"login": true, "logout": true,
	}
	l7Readers = map[string]bool{ // L7: the two cross-entity readers
		"show": true, "search": true,
	}
	l8Workflow  = "work"           // L8: the one workflow group
	l9Shortcuts = map[string]bool{ // L9: the four permanent aliases
		"board": true, "next": true, "overview": true, "status": true,
	}
)

// l3Canonical is L3's seven: one verb per operation.
var l3Canonical = map[string]bool{
	"add": true, "show": true, "list": true, "edit": true,
	"set": true, "remove": true, "delete": true,
}

// l3DomainActions is L3's allowlist of verbs that name an act none of the
// seven expresses. Transcribed from 061 §1 L3 in its order; a verb reaching
// the tree without reaching this list is the drift §0 describes.
//
// `pack` is the last entry for a reason worth keeping: `lode secret pack` is
// Hidden, so two hand surveys enumerating from `lode --help` never saw it and
// this test found it instead (061 §1, "Revised again"). The walk below reads
// the built tree and does not skip hidden commands — the law says every
// command is explicable by one of its rules, not every visible one.
var l3DomainActions = map[string]bool{
	"claim": true, "release": true, "renew": true, "submit": true,
	"abandon": true, "reopen": true, "rework": true, "start": true,
	"stop": true, "publish": true, "promote": true, "revoke": true,
	"sync": true, "exec": true, "purge": true, "import": true,
	"install": true, "recommend": true, "resolve": true, "decompose": true,
	"instruct": true, "reconcile": true, "transfer": true, "accept": true,
	"revise": true, "lint": true, "derive": true, "seek": true,
	"tail": true, "gc": true, "link": true, "dismiss": true,
	"serve": true, "listen": true, "next": true, "resume": true,
	"attach": true, "detach": true, "assign": true, "block": true,
	"parent": true, "duplicate": true, "request": true, "pack": true,
}

// nounViews is every L6 named view in the tree, by full command path. A view
// is a noun, so checks 2 and 3 — which read a subcommand's name as a verb —
// must not run over it, and asking "is this verb allowed" is unanswerable
// until this set is known.
//
// L6's own list is illustrative, not exhaustive: it names seventeen of these
// and the tree holds twenty-six. So this is a second normative list, and it
// lives in a test rather than in the spec it enforces — an inversion worth
// fixing. **It should move into 061 (§5, beside the four checks it feeds, or
// L6) the next time that spec is revised**, at which point this comment goes
// with it.
//
// Provenance, since it differs by entry:
//   - Verbatim from L6: task brief, task board, task tree, task blockers,
//     task cost, task frontier, task critical-path, doc todo, doc versions,
//     doc reviewers, secret catalog, event subscribers, graph quarantines,
//     project overview, project health, project focus, project crew.
//   - Named in §2.1–§2.3's rename tables but absent from L6's list:
//     graph drift, graph gaps, task timeline, work status, and project repo
//     (which §2.2 calls a nested entity group).
//   - **Mentioned nowhere in 061:** graph triples, secret status,
//     task checklist, task skills. These four are the entries this test
//     legislates outright, and they are the reason the list belongs in the
//     spec rather than here.
var nounViews = map[string]bool{
	"lode doc reviewers":      true,
	"lode doc todo":           true,
	"lode doc versions":       true,
	"lode event subscribers":  true,
	"lode graph drift":        true,
	"lode graph gaps":         true,
	"lode graph quarantines":  true,
	"lode graph triples":      true,
	"lode project crew":       true,
	"lode project focus":      true,
	"lode project health":     true,
	"lode project overview":   true,
	"lode project repo":       true,
	"lode secret catalog":     true,
	"lode secret status":      true,
	"lode task blockers":      true,
	"lode task board":         true,
	"lode task brief":         true,
	"lode task checklist":     true,
	"lode task cost":          true,
	"lode task critical-path": true,
	"lode task frontier":      true,
	"lode task skills":        true,
	"lode task timeline":      true,
	"lode task tree":          true,
	"lode work status":        true,
}

// hyphenatedVerbs is L4's exception list: a verb that keeps its hyphen because
// no single word says it. Keyed on the forward verb's path — L5 inverses ride
// on their forward verb's entry, so `task unfollow-up` needs no line of its
// own. Every entry carries its reason, which is what makes adding one a
// decision someone can see in review.
var hyphenatedVerbs = map[string]string{
	"lode task follow-up": "names the wl:followUpOf edge, which has no single-word verb",
}

// TestNameRule is 061 §5: the naming law of §1 as a test. It walks the live
// cobra tree and fails on the four things a test can see — an unclassifiable
// top-level command, a verb outside L3, a hyphenated verb outside the
// allowlist, and pointless depth. It does not check L4's "verbs are imperative
// verbs" or L6's "views are nouns"; a test cannot tell an adjective from a
// verb, which is why `internal/cmd/CLAUDE.md` states the law for review to
// read.
//
// The tree is the source of truth, not the source files: a command's name is a
// runtime fact, and TestCommandReference and TestEntityArgsComplete already
// walk it for the same reason.
func TestNameRule(t *testing.T) {
	seenTop := map[string]bool{}
	seenView := map[string]bool{}
	seenHyphen := map[string]bool{}

	var walk func(c *cobra.Command, depth int)
	walk = func(c *cobra.Command, depth int) {
		subs := subcommands(c)
		// Check 4: a parent below the top level with exactly one child. L1
		// fixes the shape of a top-level entity group, so `lode actor` with
		// one subcommand is correct; `lode graph projection status` was not.
		if depth >= 2 && len(subs) == 1 {
			t.Errorf("%q is below the top level and has exactly one subcommand, %q: 061 §5 "+
				"rejects the depth — fold the child into its parent the way "+
				"`lode graph projection status` became `lode graph quarantines`",
				c.CommandPath(), subs[0].Name())
		}
		for _, sub := range subs {
			name, path := sub.Name(), sub.CommandPath()
			switch {
			case depth == 0:
				// Check 1: every top-level command classifies under one rule.
				seenTop[name] = true
				if !l1Entities[name] && !l2Machine[name] && !l7Readers[name] &&
					name != l8Workflow && !l9Shortcuts[name] {
					t.Errorf("top-level %q matches no rule in 061 §1: it must be an L1 entity, "+
						"an L2 machine command, one of the two L7 cross-entity readers, the L8 "+
						"workflow group, or an L9 shortcut. Move it under its entity, or amend "+
						"061 §1 to open the closed set it belongs in", path)
				}
			case nounViews[path]:
				seenView[path] = true // L6: a noun, so not this rule's business
			case c.Name() == "set":
				// L4: under `set` the field "is an argument, not part of its
				// name". `project set focus-note` is a field, never a verb.
			case strings.Contains(name, "-"):
				// Check 3.
				fwd := c.CommandPath() + " " + forwardVerb(name)
				if _, ok := hyphenatedVerbs[fwd]; ok {
					seenHyphen[fwd] = true
				} else {
					t.Errorf("%q is a hyphenated verb, which L4 forbids (061 §1): rename it to "+
						"one word, or add a hyphenatedVerbs entry giving the reason no single "+
						"word says it", path)
				}
			default:
				// Check 2.
				if !allowedVerb(name) {
					t.Errorf("%q uses the verb %q, which is neither one of L3's seven (add, "+
						"show, list, edit, set, remove, delete) nor in L3's domain-action "+
						"allowlist (061 §1): rename it to the canonical verb for the "+
						"operation, or amend L3 if it names an act none of the seven "+
						"expresses", path, name)
				}
			}
			walk(sub, depth+1)
		}
	}
	walk(rootCmd, 0)

	// Staleness. A closed set or an allowlist naming something the tree no
	// longer has means a rename landed without its rule following it.
	for _, set := range []struct {
		rule    string
		members map[string]bool
	}{
		{"L1 entity", l1Entities},
		{"L2 machine", l2Machine},
		{"L7 cross-entity reader", l7Readers},
		{"L9 shortcut", l9Shortcuts},
		{"L8 workflow group", map[string]bool{l8Workflow: true}},
	} {
		for name := range set.members {
			if !seenTop[name] {
				t.Errorf("061 §1 names %q as an %s, but there is no such top-level command: a "+
					"rename was missed, or the closed set needs amending", name, set.rule)
			}
		}
	}
	for path := range nounViews {
		if !seenView[path] {
			t.Errorf("nounViews names %q, which is not a command: a rename was missed, so the "+
				"view is now being read as a verb under some other path", path)
		}
	}
	for path, reason := range hyphenatedVerbs {
		if !seenHyphen[path] {
			t.Errorf("hyphenatedVerbs names %q (%s), which is not a hyphenated command: the "+
				"command was renamed, so remove the entry", path, reason)
		}
	}
}

// subcommands returns c's children less cobra's own generated ones: `help`,
// `completion`, and the `__complete` RPC the shell scripts call. Cobra adds
// the last two lazily on the first Execute, so which of them are attached
// depends on what ran before this test — none of them are ours to name.
// Hidden children of ours stay in: a hidden child is still depth, so check 4
// counts it.
func subcommands(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range c.Commands() {
		name := sub.Name()
		if name == "help" || name == "completion" || strings.HasPrefix(name, "__") {
			continue
		}
		out = append(out, sub)
	}
	return out
}

// forwardVerb strips an L5 inverse back to the verb it inverts, so
// `unfollow-up` resolves to `follow-up`.
func forwardVerb(name string) string {
	if rest, ok := strings.CutPrefix(name, "un"); ok && rest != "" {
		return rest
	}
	return name
}

// allowedVerb applies L3 and L5: one of the seven, or a named domain action,
// or the `un-` inverse of something that is.
func allowedVerb(name string) bool {
	if l3Canonical[name] || l3DomainActions[name] {
		return true
	}
	if fwd := forwardVerb(name); fwd != name {
		return allowedVerb(fwd)
	}
	return false
}
