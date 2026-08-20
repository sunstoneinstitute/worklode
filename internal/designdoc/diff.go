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
// sections only — an anchorless heading is content within its nearest
// anchored ancestor (025 §6.1) and never participates. On a duplicate
// anchor within one document, the first occurrence wins; a duplicate is a
// lint-grade defect a different check owns, not this diff.
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
			if strings.TrimSpace(accSec.Body) != strings.TrimSpace(candSec.Body) {
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
// there is nothing to diff against — a first accept, and `lode doc anchors`.
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
