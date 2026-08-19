package designdoc

import "fmt"

// LintAnchors reports the two anchor defects that make a section
// unaddressable: two headings claiming one anchor, and an anchor that
// disagrees with its heading number. secfmt.py writes the anchor as
// "sec-<number>", so the number is the anchor and a disagreement means one of
// them is a typo.
//
// It returns every finding rather than stopping at the first — `lode doc
// anchors` is a pre-accept lint whose caller wants the whole list in one pass.
// The store keeps the stricter reading: any finding refuses the write
// (parseDocBody, internal/store/docs.go).
//
// Findings are human-readable and name the heading rather than a line number:
// the parser yields no line numbers, and the heading text locates the defect.
func LintAnchors(d *Document) []string {
	var out []string
	seen := map[string]string{}
	for _, sec := range d.Sections {
		if sec.Anchor == "" {
			continue
		}
		if sec.Number != "" && sec.Anchor != "sec-"+sec.Number {
			out = append(out, fmt.Sprintf("heading %q is numbered %s but anchored #%s",
				sec.Title, sec.Number, sec.Anchor))
		}
		if prev, dup := seen[sec.Anchor]; dup {
			out = append(out, fmt.Sprintf("anchor #%s is claimed by both %q and %q",
				sec.Anchor, prev, sec.Title))
			continue
		}
		seen[sec.Anchor] = sec.Title
	}
	return out
}
