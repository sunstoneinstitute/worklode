// `lode show --inline` (WL-84): render a document with every effective
// amendment and supersession folded into the section it acts on, so an agent
// reads a spec's current state without chasing the reference chain turn by
// turn. This is 026 §3.2's consolidated view computed over backbone
// documents — the same rendering scripts/inlinespec.py produces for the git
// corpus, driven by doc_edges instead of frontmatter:
//
//   - a section an effective claim acts on keeps its own text and gains the
//     acting section's text beneath it, led by an attribution marker
//     (**[amending spec 45 §2]:**<br>) so borrowed text is never mistakable
//     for the document's own;
//   - inlining is transitive: an amendment that is itself amended is
//     expanded, depth-capped so a mutually-amending defect cannot hang;
//   - a claim from a document that is not yet effective (a draft's proposal)
//     is listed as pending and never folded, so nothing unsettled reads as
//     design;
//   - a document-scoped claim (no section on either end) is a banner
//     reference at the top, never inlined text;
//   - inlined headings are flattened to bold lines so borrowed text cannot
//     reshape the outline of the document it lands in.

package cmd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// inlineMaxDepth caps transitive expansion; a chain this deep is a corpus
// defect, not a rendering requirement (inlinespec.py uses the same bound).
const inlineMaxDepth = 8

// docInliner folds a document's inbound claims into its body. fetch loads a
// document's detail by id (memoized — the same acting document is typically
// named by many sections).
type docInliner struct {
	fetch func(int64) (*model.DocDetail, error)
	cache map[int64]*model.DocDetail
}

func newDocInliner(fetch func(int64) (*model.DocDetail, error)) *docInliner {
	return &docInliner{fetch: fetch, cache: map[int64]*model.DocDetail{}}
}

func (in *docInliner) detail(id int64) (*model.DocDetail, error) {
	if d, ok := in.cache[id]; ok {
		return d, nil
	}
	d, err := in.fetch(id)
	if err != nil {
		return nil, err
	}
	in.cache[id] = d
	return d, nil
}

// effectiveStatus is when a claim takes effect: once the claiming document is
// accepted (a later supersession does not un-say what it changed).
func effectiveStatus(status string) bool {
	return status == "accepted" || status == "superseded"
}

// claimRef names an acting section for a reader: "spec 45 §2", "adr 48 §3",
// or the slug when the document carries no number.
func claimRef(e model.DocEdge, anchor string) string {
	name := e.ToSlug
	if e.ToNumber != 0 {
		name = e.ToKind + " " + strconv.Itoa(e.ToNumber)
	} else if e.ToKind != "" {
		name = e.ToKind + " " + name
	}
	if anchor != "" {
		name += " §" + strings.TrimPrefix(anchor, "sec-")
	}
	return name
}

// actingVerb maps an inbound edge type to its attribution verb; anything
// else is not a claim this rendering folds.
func actingVerb(edgeType string) string {
	switch edgeType {
	case "amendedBy":
		return "amending"
	case "isReplacedBy":
		return "superseding"
	}
	return ""
}

var headingLine = regexp.MustCompile(`(?m)^#+[ \t]+(.*?)(?:[ \t]*\{#[^}]*\})?[ \t]*$`)

// flattenHeadings turns an inlined subtree's headings into bold lines, so
// the borrowed text cannot reshape the outline it lands in (026 §3.2).
func flattenHeadings(text string) string {
	return headingLine.ReplaceAllString(text, "**$1**")
}

// sectionClaims returns d's inbound section-scoped claims landing on anchor,
// in edge order.
func sectionClaims(d *model.DocDetail, anchor string) []model.DocEdge {
	var out []model.DocEdge
	for _, e := range d.EdgesIn {
		if actingVerb(e.Type) == "" || e.FromAnchor != anchor || e.ToAnchor == "" || e.ToDoc == 0 {
			continue
		}
		out = append(out, e)
	}
	return out
}

// blocksFor renders the inlined blocks and pending notes for one section of
// one document, transitively expanded. seen keys are (doc id, anchor).
func (in *docInliner) blocksFor(d *model.DocDetail, anchor string, seen map[string]bool, depth int) (blocks, pending []string, err error) {
	key := strconv.FormatInt(d.ID, 10) + "#" + anchor
	if depth >= inlineMaxDepth || seen[key] {
		return nil, nil, nil
	}
	seen[key] = true
	for _, e := range sectionClaims(d, anchor) {
		acting, err := in.detail(e.ToDoc)
		if err != nil {
			return nil, nil, fmt.Errorf("fetch %s: %w", claimRef(e, e.ToAnchor), err)
		}
		if !effectiveStatus(acting.Status) {
			pending = append(pending, claimRef(e, e.ToAnchor))
			continue
		}
		parsed, err := designdoc.Parse([]byte(acting.Body))
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", acting.Slug, err)
		}
		text, ok := parsed.Subtree(e.ToAnchor)
		if !ok {
			// The acting section vanished from its own document — surface
			// the claim as pending-shaped rather than dropping it silently.
			pending = append(pending, claimRef(e, e.ToAnchor)+" (section not found)")
			continue
		}
		nested, nestedPending, err := in.blocksFor(acting, e.ToAnchor, seen, depth+1)
		if err != nil {
			return nil, nil, err
		}
		pending = append(pending, nestedPending...)
		parts := append([]string{strings.TrimSpace(flattenHeadings(text))}, nested...)
		blocks = append(blocks, fmt.Sprintf("**[%s %s]:**<br>\n\n%s",
			actingVerb(e.Type), claimRef(e, e.ToAnchor), strings.Join(parts, "\n\n")))
	}
	return blocks, pending, nil
}

// consolidateDoc renders the whole consolidated view: banner, preamble, and
// every section with its claims folded in. section, when non-empty, narrows
// the output to that section's subtree — each nested section still carries
// its own folds.
func (in *docInliner) consolidateDoc(d *model.DocDetail, section string) (string, error) {
	parsed, err := designdoc.Parse([]byte(d.Body))
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", d.Slug, err)
	}

	var b strings.Builder

	// Document-scoped claims: banner references, never inlined text.
	var banner []string
	for _, e := range d.EdgesIn {
		if actingVerb(e.Type) == "" || e.FromAnchor != "" || e.ToDoc == 0 {
			continue
		}
		banner = append(banner, fmt.Sprintf("%s by %s", strings.TrimSuffix(actingVerb(e.Type), "ing")+"ed", claimRef(e, e.ToAnchor)))
	}

	if section == "" {
		fmt.Fprintf(&b, "<!-- Consolidated view of %s: effective amendments and supersessions folded in (lode show --inline). Not a source document. -->\n\n", d.Slug)
		for _, line := range banner {
			fmt.Fprintf(&b, "> %s\n", line)
		}
		if len(banner) > 0 {
			b.WriteString("\n")
		}
		if p := strings.TrimSpace(parsed.Preamble); p != "" {
			b.WriteString(p + "\n\n")
		}
	}

	inSubtree := section == ""
	var subtreeLevel int
	for _, sec := range parsed.Sections {
		if section != "" {
			if sec.Anchor == section {
				inSubtree = true
				subtreeLevel = sec.Level
			} else if inSubtree && sec.Level <= subtreeLevel {
				inSubtree = false
			}
			if !inSubtree {
				continue
			}
		}
		blocks, pending, err := in.blocksFor(d, sec.Anchor, map[string]bool{}, 0)
		if err != nil {
			return "", err
		}
		// Heading plus the section's own body only — Source() would carry
		// the whole subtree and duplicate every nested section this loop
		// visits on its own.
		b.WriteString(strings.TrimRight(sec.HeadingAndBody(), "\n"))
		b.WriteString("\n")
		for _, p := range pending {
			fmt.Fprintf(&b, "\n> Pending %s (not yet effective)\n", p)
		}
		for _, block := range blocks {
			b.WriteString("\n" + block + "\n")
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}
