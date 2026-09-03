package designdoc

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// DepthLimit is the 025 §6.1 addressability limit. Server-configurable is
// deferred with the rest of the 014 admin surface; 3 is its default.
const DepthLimit = 3

// SectionDiff compares an accepted document with a candidate revision
// (025 §6, enforced at accept time by the server per 025 §5). Removed,
// Renumbered and TooDeep are violations; Changed is the last_revised_in
// input; Added is informational. All five slices hold anchors, sorted for
// deterministic output.
type SectionDiff struct {
	Added, Removed, Renumbered, Changed, TooDeep []string

	// renumbers maps an anchor in Renumbered to its accepted and candidate
	// section numbers, so Violations can name both.
	renumbers map[string][2]string
}

// CompareSections diffs accepted against candidate over their anchored
// sections only. An anchorless heading is never a node of its own: it is
// content within its nearest anchored ancestor (025 §6.1) and is diffed as
// part of that ancestor — see effectiveContent. An anchorless heading with no
// anchored ancestor at all belongs to no section and so is diffed by nobody.
// On a duplicate anchor within one document, the first occurrence wins; a
// duplicate is a lint-grade defect a different check owns, not this diff.
//
// depthLimit governs TooDeep, which is DepthViolations' rule over candidate
// alone — accepted plays no part in it.
//
// Both passes walk anchors in sorted order, so every result slice is stable.
func CompareSections(accepted, candidate *Document, depthLimit int) SectionDiff {
	acc := anchoredSections(accepted)
	cand := anchoredSections(candidate)

	diff := SectionDiff{renumbers: map[string][2]string{}}

	for _, anchor := range slices.Sorted(maps.Keys(acc)) {
		if _, ok := cand[anchor]; !ok {
			diff.Removed = append(diff.Removed, anchor)
		}
	}

	for _, anchor := range slices.Sorted(maps.Keys(cand)) {
		candSec := cand[anchor]
		accSec, ok := acc[anchor]
		if !ok {
			diff.Added = append(diff.Added, anchor)
		} else {
			if accSec.Number != candSec.Number {
				diff.Renumbered = append(diff.Renumbered, anchor)
				diff.renumbers[anchor] = [2]string{accSec.Number, candSec.Number}
			}
			if effectiveContent(accSec) != effectiveContent(candSec) {
				diff.Changed = append(diff.Changed, anchor)
			}
		}
	}

	diff.TooDeep = tooDeepAnchors(candidate, depthLimit)

	return diff
}

// DepthViolations reports the 025 §6.1 depth rule over one document: an
// anchored section deeper than limit is unaddressable content masquerading as
// a node. It needs no prior version, which makes it the whole gate wherever
// there is nothing to diff against — a first accept, and `lode doc lint <file>`.
// Strings are the same ones SectionDiff.Violations() would emit for TooDeep.
func DepthViolations(d *Document, limit int) []string {
	var out []string
	for _, anchor := range tooDeepAnchors(d, limit) {
		out = append(out, tooDeepViolation(anchor))
	}
	return out
}

// tooDeepAnchors returns d's anchored sections deeper than limit, sorted, so
// callers get stable output; on a duplicate anchor the first occurrence wins.
func tooDeepAnchors(d *Document, limit int) []string {
	secs := anchoredSections(d)
	var out []string
	for _, anchor := range slices.Sorted(maps.Keys(secs)) {
		if secs[anchor].Level > limit {
			out = append(out, anchor)
		}
	}
	return out
}

// tooDeepViolation is the one wording of the depth finding, shared by
// DepthViolations and SectionDiff.Violations().
func tooDeepViolation(anchor string) string {
	return fmt.Sprintf("%s: exceeds the configured section depth limit (025 §6.1)", anchor)
}

// Violations returns human-readable strings naming each offending anchor —
// they surface verbatim in an HTTP 422 body and a CLI error, so each must be
// actionable on its own. A renumber names both the accepted and candidate
// section numbers.
func (d SectionDiff) Violations() []string {
	var out []string
	for _, anchor := range d.Removed {
		out = append(out, fmt.Sprintf(
			"%s: section removed; accepted anchors are append-only (025 §6 rule 1)", anchor))
	}
	for _, anchor := range d.Renumbered {
		nums := d.renumbers[anchor]
		out = append(out, fmt.Sprintf(
			"%s: renumbered from %q to %q; accepted anchors are immutable (025 §6 rule 3)",
			anchor, nums[0], nums[1]))
	}
	for _, anchor := range d.TooDeep {
		out = append(out, tooDeepViolation(anchor))
	}
	return out
}

// effectiveContent is the text 025 §6.1 counts as s's own: its Body plus the
// heading and body of every anchorless descendant. Section.Body stops at the
// next heading of any level, so comparing bodies alone misses an edit confined
// to an anchorless subheading and leaves claims against s falsely fresh.
//
// An anchored descendant is a node in its own right, so the walk stops there.
// An anchorless descendant's heading does participate — renaming "####
// Tie-breaking" changes the section holding it — while rewording an *anchored*
// heading is not a change (025 §3), which is why s's own heading is excluded.
//
// Pieces are whitespace-trimmed and newline-joined, so the result is as
// insensitive to surrounding blank lines as the single-Body comparison was.
func effectiveContent(s *Section) string {
	parts := appendUnanchored([]string{strings.TrimSpace(s.Body)}, s)
	return strings.Join(parts, "\n")
}

// appendUnanchored appends s's anchorless descendants' headings and bodies to
// parts in document order, stopping at every anchored section. A heading
// contributes its parsed fields rather than its source line, so reformatting
// one is not a content change — over-stamping last_revised_in mass-invalidates
// valid claims, which 025 §6 rule 5 forbids as squarely as under-stamping.
func appendUnanchored(parts []string, s *Section) []string {
	for _, child := range s.Children {
		if child.Anchor != "" {
			continue
		}
		parts = append(parts,
			fmt.Sprintf("%d %s %s", child.Level, child.Number, child.Title),
			strings.TrimSpace(child.Body))
		parts = appendUnanchored(parts, child)
	}
	return parts
}

// anchoredSections indexes d's anchored sections by anchor. Anchorless
// headings are skipped; on a duplicate anchor the first occurrence wins.
func anchoredSections(d *Document) map[string]*Section {
	out := make(map[string]*Section)
	for _, sec := range d.Sections {
		if sec.Anchor == "" {
			continue
		}
		if _, dup := out[sec.Anchor]; dup {
			continue
		}
		out[sec.Anchor] = sec
	}
	return out
}
