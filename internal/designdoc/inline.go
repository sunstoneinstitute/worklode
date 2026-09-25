// The consolidated view (WL-84): render a document with every in-force
// amendment folded into the section it acts on, so a reader sees a spec's
// current state without chasing the reference chain turn by turn. It backs
// both `lode show --inline` and the cockpit's document page (WL-716), which
// is why it lives here rather than in internal/cmd.
//
// A section's rule folds in the rules that amend it (WL-SPEC-77 §4),
// attributed to the amending rule (**[amending WL-RULE-12 (WL-SPEC-45#sec-2)]:**<br>)
// so borrowed text is never mistakable for the document's own, and
// transitively, depth-capped so a mutually-amending defect cannot hang. An
// accepted or superseded amending rule is in force, a draft one is pending
// and listed but never folded, and a withdrawn one is left out. Inlined
// headings are flattened to bold lines so borrowed text cannot reshape the
// outline of the document it lands in.

package designdoc

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// inlineMaxDepth caps transitive expansion; a chain this deep is a corpus
// defect, not a rendering requirement (inlinespec.py uses the same bound).
const inlineMaxDepth = 8

// Inliner folds a document's rule amendments into its body. fetchRule loads
// a rule by ref, memoized — the same amending rule can act on many sections.
type Inliner struct {
	fetchRule func(string) (*model.Rule, error)
	rules     map[string]*model.Rule
}

func NewInliner(fetchRule func(string) (*model.Rule, error)) *Inliner {
	return &Inliner{fetchRule: fetchRule, rules: map[string]*model.Rule{}}
}

func (in *Inliner) rule(ref string) (*model.Rule, error) {
	if r, ok := in.rules[ref]; ok {
		return r, nil
	}
	r, err := in.fetchRule(ref)
	if err != nil {
		return nil, err
	}
	in.rules[ref] = r
	return r, nil
}

// amendersOf is the rules with an amends edge onto r, in edge order.
func amendersOf(r *model.Rule) []string {
	var out []string
	for _, e := range r.Edges {
		if e.Type == "amends" && e.To == r.Ref {
			out = append(out, e.From)
		}
	}
	return out
}

// ruleCite names an amending rule and where it is arranged:
// "WL-RULE-12 (WL-SPEC-45#sec-2)".
func ruleCite(r *model.Rule) string {
	if len(r.ArrangedIn) == 0 {
		return r.Ref
	}
	return fmt.Sprintf("%s (%s#%s)", r.Ref, r.ArrangedIn[0].DocRef, r.ArrangedIn[0].Anchor)
}

// RuleAmendments renders the in-force amendments of r as inlined blocks and
// the pending ones as references, transitively.
func (in *Inliner) RuleAmendments(r *model.Rule) (blocks, pending []string, err error) {
	return in.ruleBlocks(r.Ref, amendersOf(r), map[string]bool{}, 0)
}

// ruleBlocks renders the amendments by amenders of the rule root. seen keys
// are rule refs, so a mutually-amending pair stops instead of looping.
func (in *Inliner) ruleBlocks(root string, amenders []string, seen map[string]bool, depth int) (blocks, pending []string, err error) {
	if depth >= inlineMaxDepth {
		return nil, nil, nil
	}
	seen[root] = true
	for _, ref := range amenders {
		if seen[ref] {
			continue
		}
		a, err := in.rule(ref)
		if err != nil {
			return nil, nil, fmt.Errorf("fetch %s: %w", ref, err)
		}
		switch {
		case a.Status == "withdrawn":
			continue
		case !effectiveStatus(a.Status):
			pending = append(pending, "amendment by "+ruleCite(a))
			continue
		}
		nested, nestedPending, err := in.ruleBlocks(a.Ref, amendersOf(a), seen, depth+1)
		if err != nil {
			return nil, nil, err
		}
		pending = append(pending, nestedPending...)
		parts := []string{"**" + a.Heading + "**"}
		if body := strings.TrimSpace(flattenHeadings(a.Body)); body != "" {
			parts = append(parts, body)
		}
		parts = append(parts, nested...)
		blocks = append(blocks, fmt.Sprintf("**[amending %s]:**<br>\n\n%s", ruleCite(a), strings.Join(parts, "\n\n")))
	}
	return blocks, pending, nil
}

// effectiveStatus is when an amendment takes effect: once the amending rule
// is accepted (a later supersession does not un-say what it changed).
func effectiveStatus(status string) bool {
	return status == "accepted" || status == "superseded"
}

var headingLine = regexp.MustCompile(`(?m)^#+[ \t]+(.*?)(?:[ \t]*\{#[^}]*\})?[ \t]*$`)

// flattenHeadings turns an inlined subtree's headings into bold lines, so
// the borrowed text cannot reshape the outline it lands in (026 §3.2).
func flattenHeadings(text string) string {
	return headingLine.ReplaceAllString(text, "**$1**")
}

// Consolidate renders the whole consolidated view: preamble and every
// section with its amendments folded in. section, when non-empty, narrows
// the output to that section's subtree — each nested section still carries
// its own folds.
func (in *Inliner) Consolidate(d *model.DocDetail, section string) (string, error) {
	parsed, err := Parse([]byte(d.Body))
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", d.Slug, err)
	}

	var b strings.Builder

	if section == "" {
		fmt.Fprintf(&b, "<!-- Consolidated view of %s: effective amendments and supersessions folded in (lode show --inline). Not a source document. -->\n\n", d.Slug)
		if p := strings.TrimSpace(parsed.Preamble); p != "" {
			b.WriteString(p + "\n\n")
		}
	}

	// The rule at each amended anchor, and the rules amending it.
	sectionRule, amenders := map[string]string{}, map[string][]string{}
	for _, a := range d.Amendments {
		sectionRule[a.Anchor] = a.Rule
		amenders[a.Anchor] = append(amenders[a.Anchor], a.By)
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
		var blocks, pending []string
		if by := amenders[sec.Anchor]; len(by) > 0 {
			if blocks, pending, err = in.ruleBlocks(sectionRule[sec.Anchor], by, map[string]bool{}, 0); err != nil {
				return "", err
			}
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
