package designdoc

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Finding is one §8.3 mechanical-substantive rule firing on one section, or,
// for new-dependency, on the document as a whole.
type Finding struct {
	Rule   string // new-dependency | ns-term | surface-token | acceptance-criteria
	Anchor string // "" for a document-level finding (new-dependency)
	Detail string // what changed, for the refusal message
}

// nsTermPattern is the ns-term rule's token shape, fixed by WL-PLAN-109's
// "Decisions this plan executes".
var nsTermPattern = regexp.MustCompile(`\bwlc?:[A-Za-z][A-Za-z0-9_]*\b`)

// inlineCodePattern matches a single-backtick inline code span. The corpus
// does not use multi-backtick spans; surface-token is deliberately coarse
// (see codeSurfaces), so a fancier CommonMark-correct scanner buys nothing.
var inlineCodePattern = regexp.MustCompile("`[^`\n]+`")

// acceptanceHeadingPattern is the §8.3 acceptance-criteria trigger.
var acceptanceHeadingPattern = regexp.MustCompile(`(?i)acceptance criteria|definition of done`)

// ChangedAnchors returns the anchors whose section text differs between old
// and new, in document order: new's order first (so an added section's
// position is where it appears there), then any anchor present only in old
// (a removed section), in old's order. A section present in only one of them
// counts as changed.
func ChangedAnchors(old, new *Document) []string {
	oldSecs := anchoredSections(old)
	newSecs := anchoredSections(new)

	var out []string
	seen := make(map[string]bool)
	for _, sec := range new.Sections {
		if sec.Anchor == "" || seen[sec.Anchor] {
			continue
		}
		seen[sec.Anchor] = true
		oldSec, ok := oldSecs[sec.Anchor]
		if !ok || effectiveContent(oldSec) != effectiveContent(sec) {
			out = append(out, sec.Anchor)
		}
	}
	for _, sec := range old.Sections {
		if sec.Anchor == "" || seen[sec.Anchor] {
			continue
		}
		seen[sec.Anchor] = true
		if _, ok := newSecs[sec.Anchor]; !ok {
			out = append(out, sec.Anchor)
		}
	}
	return out
}

// MechanicalFindings runs the text-level §8.3 rules over the changed
// sections between old and new: new-dependency (a document-level frontmatter
// check) plus, per changed anchor, ns-term, surface-token and
// acceptance-criteria. The referrer rule is a corpus query and lives in the
// store (WL-PLAN-109 Task 3), not here.
func MechanicalFindings(old, new *Document) []Finding {
	var findings []Finding

	if f := newDependencyFinding(old, new); f != nil {
		findings = append(findings, *f)
	}

	oldSecs := anchoredSections(old)
	newSecs := anchoredSections(new)
	for _, anchor := range ChangedAnchors(old, new) {
		oldText := sectionText(oldSecs, anchor)
		newText := sectionText(newSecs, anchor)

		if f := nsTermFinding(anchor, oldText, newText); f != nil {
			findings = append(findings, *f)
		}
		if f := surfaceTokenFinding(anchor, oldText, newText); f != nil {
			findings = append(findings, *f)
		}
		if acceptanceHeadingPattern.MatchString(sectionTitle(oldSecs, newSecs, anchor)) {
			findings = append(findings, Finding{
				Rule:   "acceptance-criteria",
				Anchor: anchor,
				Detail: fmt.Sprintf("%s: acceptance-criteria/definition-of-done section changed", anchor),
			})
		}
	}

	return findings
}

// sectionText is effectiveContent for the section anchored anchor in secs,
// or "" when it holds none — the section did not exist on that side of the
// edit.
func sectionText(secs map[string]*Section, anchor string) string {
	s, ok := secs[anchor]
	if !ok {
		return ""
	}
	return effectiveContent(s)
}

// sectionTitle is the title of anchor's section, preferring new (a reworded
// or added section) and falling back to old (a removed one).
func sectionTitle(oldSecs, newSecs map[string]*Section, anchor string) string {
	if s, ok := newSecs[anchor]; ok {
		return s.Title
	}
	if s, ok := oldSecs[anchor]; ok {
		return s.Title
	}
	return ""
}

// nsTermFinding fires when the set of wl:/wlc: tokens in oldText and newText
// differs.
func nsTermFinding(anchor, oldText, newText string) *Finding {
	if slices.Equal(nsTermSet(oldText), nsTermSet(newText)) {
		return nil
	}
	return &Finding{
		Rule:   "ns-term",
		Anchor: anchor,
		Detail: fmt.Sprintf("%s: wl:/wlc: tokens changed", anchor),
	}
}

// nsTermSet returns the distinct wl:/wlc: tokens in text, sorted.
func nsTermSet(text string) []string {
	set := make(map[string]bool)
	for _, m := range nsTermPattern.FindAllString(text, -1) {
		set[m] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// surfaceTokenFinding fires when oldText and newText's code surfaces
// (codeSurfaces) differ.
func surfaceTokenFinding(anchor, oldText, newText string) *Finding {
	if slices.Equal(codeSurfaces(oldText), codeSurfaces(newText)) {
		return nil
	}
	return &Finding{
		Rule:   "surface-token",
		Anchor: anchor,
		Detail: fmt.Sprintf("%s: code span or fenced-block line changed", anchor),
	}
}

// codeSurfaces returns text's inline code spans and fenced-code-block
// lines, in order — the §8.3 surface-token rule's deliberately coarse unit.
// Schema, DDL, API routes, CLI flags, event names, IRIs and enum values all
// live in code formatting in this corpus, and a false positive here routes
// an edit to review rather than refusing or silently landing it, which is
// the side §8.3 says to fail toward. Fences are recognised the way
// scanHeadings recognises them, so the two never disagree about what counts
// as fenced.
func codeSurfaces(text string) []string {
	var out []string
	var fence string
	for _, line := range strings.Split(text, "\n") {
		stripped := strings.TrimLeft(line, " \t")
		if fence != "" {
			if strings.HasPrefix(stripped, fence) {
				fence = ""
			} else {
				out = append(out, line)
			}
			continue
		}
		if strings.HasPrefix(stripped, "```") || strings.HasPrefix(stripped, "~~~") {
			fence = stripped[:3]
			continue
		}
		out = append(out, inlineCodePattern.FindAllString(line, -1)...)
	}
	return out
}

// newDependencyFinding fires when new's frontmatter requires list gains an
// entry not present in old's. Prose-level dependency detection is not
// mechanical (WL-PLAN-109 Decisions): that is the fixer's judged call.
func newDependencyFinding(old, new *Document) *Finding {
	oldReq := requiresSet(old)
	var added []string
	for _, r := range requiresOf(new) {
		if !oldReq[r] {
			added = append(added, r)
		}
	}
	if len(added) == 0 {
		return nil
	}
	return &Finding{
		Rule:   "new-dependency",
		Detail: fmt.Sprintf("requires gained: %s", strings.Join(added, ", ")),
	}
}

func requiresOf(d *Document) []string {
	if d.Frontmatter == nil {
		return nil
	}
	return d.Frontmatter.Requires
}

func requiresSet(d *Document) map[string]bool {
	set := make(map[string]bool)
	for _, r := range requiresOf(d) {
		set[r] = true
	}
	return set
}
