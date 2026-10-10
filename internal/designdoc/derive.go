package designdoc

import (
	"regexp"
	"strconv"
)

// Entry is one arrangement entry's place in a spec (WL-REQ-165): its heading
// depth and, for an unnumbered entry, the slug that is its anchor.
type Entry struct {
	Depth int
	Slug  string
}

// Derived is an entry's number ("" when unnumbered) and anchor.
type Derived struct {
	Number, Anchor string
}

// numberedAnchor is an anchor that carries a section number, "sec-2.1a".
// Any other anchor is a slug.
var numberedAnchor = regexp.MustCompile(`^sec-\d+(\.\d+)*[a-z]?$`)

// IsSlug reports whether anchor names an unnumbered entry rather than a
// section number.
func IsSlug(anchor string) bool {
	return anchor != "" && !numberedAnchor.MatchString(anchor)
}

// DeriveNumbers numbers entries in reading order (WL-REQ-165). An entry's
// parent is the nearest earlier entry of smaller depth; the first numbered
// child is 1, and a top-level entry's number has no prefix. A slugged entry
// is unnumbered, takes no counter slot, and its anchor is its slug. An entry
// under a slugged one has no number and is anchored "<slug>.<n>".
func DeriveNumbers(entries []Entry) []Derived {
	type frame struct {
		depth    int
		d        Derived
		children int
	}
	var stack []frame
	roots := 0
	out := make([]Derived, len(entries))
	for i, e := range entries {
		for len(stack) > 0 && stack[len(stack)-1].depth >= e.Depth {
			stack = stack[:len(stack)-1]
		}
		var d Derived
		switch {
		case e.Slug != "":
			d.Anchor = e.Slug
		case len(stack) == 0:
			roots++
			d.Number = strconv.Itoa(roots)
			d.Anchor = "sec-" + d.Number
		default:
			p := &stack[len(stack)-1]
			p.children++
			n := strconv.Itoa(p.children)
			if p.d.Number != "" {
				d.Number = p.d.Number + "." + n
				d.Anchor = "sec-" + d.Number
			} else {
				d.Anchor = p.d.Anchor + "." + n
			}
		}
		out[i] = d
		stack = append(stack, frame{depth: e.Depth, d: d})
	}
	return out
}

// Entries are d's anchored sections as arrangement entries, in order.
func (d *Document) Entries() []Entry {
	var out []Entry
	for _, s := range d.Sections {
		if s.Anchor == "" {
			continue
		}
		e := Entry{Depth: s.Level}
		if IsSlug(s.Anchor) {
			e.Slug = s.Anchor
		}
		out = append(out, e)
	}
	return out
}

// DeriveAnchors rewrites every anchored section's number and anchor to the
// ones its place derives (WL-REQ-165). A slugged section keeps its anchor.
func (d *Document) DeriveAnchors() {
	derived := DeriveNumbers(d.Entries())
	i := 0
	for _, s := range d.Sections {
		if s.Anchor == "" {
			continue
		}
		s.Number, s.Anchor = derived[i].Number, derived[i].Anchor
		i++
	}
}
