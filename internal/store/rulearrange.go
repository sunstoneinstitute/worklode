package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// editableSpec is the text a rule arrangement writes and the table holding
// its arrangement: a draft spec's own body and doc_rules, or an accepted
// spec's candidate revision and doc_revision_rules (WL-SPEC-77 §19.3).
type editableSpec struct {
	id       int64
	text     string
	table    string
	revising bool
}

// openEditableSpec locks spec docID and returns its editable text, opening
// the candidate revision of an accepted spec when none is open. Only a spec
// arranges rules.
func openEditableSpec(tx *sql.Tx, now time.Time, docID int64, actorID string, eventID int64) (editableSpec, error) {
	d, err := lockDoc(tx, docID)
	if err != nil {
		return editableSpec{}, err
	}
	if d.kind != "spec" {
		return editableSpec{}, fmt.Errorf("doc %d is a %s: only a spec arranges rules (WL-SPEC-77 §4): %w", docID, d.kind, ErrInvalidInput)
	}
	if d.status == "draft" {
		return editableSpec{id: docID, text: d.body, table: "doc_rules"}, nil
	}
	var candidate string
	err = tx.QueryRow(`SELECT body FROM doc_revisions WHERE doc_id = $1 FOR UPDATE`, docID).Scan(&candidate)
	if errors.Is(err, sql.ErrNoRows) {
		if err := ReviseDoc(tx, now, docID, actorID, eventID); err != nil {
			return editableSpec{}, err
		}
		candidate = d.body
	} else if err != nil {
		return editableSpec{}, fmt.Errorf("load revision of doc %d: %w", docID, err)
	}
	return editableSpec{id: docID, text: candidate, table: "doc_revision_rules", revising: true}, nil
}

// anchorOf is the anchor the spec's editable arrangement holds ruleID at, or
// "" when it does not arrange the rule.
func (e editableSpec) anchorOf(tx *sql.Tx, ruleID int64) (string, error) {
	var anchor string
	err := tx.QueryRow(`SELECT anchor FROM `+e.table+` WHERE doc_id = $1 AND rule_id = $2`, e.id, ruleID).Scan(&anchor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read arrangement of rule %d in doc %d: %w", ruleID, e.id, err)
	}
	return anchor, nil
}

// write stores the spec's new editable text: in place on a draft, so
// syncRules rebuilds doc_rules, or into the candidate revision.
// pin adds the arrangement row the text's new section must match.
func (e editableSpec) write(tx *sql.Tx, now time.Time, text string, eventID int64, pin func(*sql.Tx) error) error {
	if !e.revising {
		_, err := updateDocBodyPinned(tx, now, e.id, text, 0, eventID, pin)
		return err
	}
	if pin != nil {
		if err := pin(tx); err != nil {
			return err
		}
	}
	return UpdateRevision(tx, now, e.id, text, eventID)
}

// ruleByRefString resolves a rule ref such as WL-REQ-12 to its row id.
func ruleByRefString(tx *sql.Tx, ref string) (int64, error) {
	r, ok := designdoc.ParseRuleRef(ref)
	if !ok {
		return 0, fmt.Errorf("%q is not a rule ref like WL-REQ-12: %w", ref, ErrInvalidInput)
	}
	return RuleIDByRef(tx, r.Key, r.Number)
}

// ArrangeRule places an existing rule in a spec (WL-SPEC-77 §19.3): its
// newest version's heading and body are written into the spec's editable
// text at the chosen position and anchor, and the spec's arrangement holds
// the rule there. A draft spec is written in place; an accepted one through
// its candidate revision, and the arrangement lands with it. Returns the
// anchor used.
func ArrangeRule(tx *sql.Tx, now time.Time, docID int64, in model.ArrangeRuleInput, actorID string, eventID int64) (string, error) {
	ruleID, err := ruleByRefString(tx, in.Rule)
	if err != nil {
		return "", err
	}
	var version int
	var heading, body string
	if err := tx.QueryRow(
		`SELECT r.version, v.heading, v.body FROM rules r
		   JOIN rule_versions v ON v.rule_id = r.id AND v.version = r.version
		  WHERE r.id = $1`, ruleID).Scan(&version, &heading, &body); err != nil {
		return "", fmt.Errorf("read rule %d: %w", ruleID, err)
	}
	e, err := openEditableSpec(tx, now, docID, actorID, eventID)
	if err != nil {
		return "", err
	}
	if a, err := e.anchorOf(tx, ruleID); err != nil {
		return "", err
	} else if a != "" {
		return "", fmt.Errorf("doc %d already arranges %s at %s: %w", docID, in.Rule, a, ErrInvalidInput)
	}
	parsed, err := designdoc.Parse([]byte(e.text))
	if err != nil {
		return "", fmt.Errorf("parse doc %d: %w", docID, err)
	}
	idx, level, anchor, err := placeRule(tx, e, parsed, in)
	if err != nil {
		return "", err
	}
	number := strings.TrimPrefix(anchor, "sec-")
	if !sectionNumber.MatchString(number) {
		number = ""
	} else if strings.Count(number, ".") != level-2 {
		return "", fmt.Errorf("anchor %s does not fit depth %d at that position: %w", anchor, level-1, ErrInvalidInput)
	}

	// The text before the new heading must end its line, and so must the
	// rule's body when a heading follows it.
	if idx == 0 {
		parsed.Preamble = endLine(parsed.Preamble)
	} else {
		parsed.Sections[idx-1].Body = endLine(parsed.Sections[idx-1].Body)
	}
	if idx < len(parsed.Sections) {
		body = endLine(body)
	}
	parsed.Sections = slices.Insert(parsed.Sections, idx, designdoc.NewSection(level, number, heading, anchor, body))

	pin := func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO `+e.table+` (doc_id, position, rule_id, rule_version, depth, anchor)
			 SELECT $1, coalesce(max(position) + 1, 0), $2, $3, $4, $5 FROM `+e.table+` WHERE doc_id = $1`,
			docID, ruleID, version, level, anchor)
		if err != nil {
			return fmt.Errorf("arrange rule %d in doc %d: %w", ruleID, docID, err)
		}
		return nil
	}
	return anchor, e.write(tx, now, string(parsed.Bytes()), eventID, pin)
}

// UnarrangeRule removes a rule from a spec without withdrawing it
// (WL-SPEC-77 §19.3): its section leaves the spec's editable text and its
// arrangement row goes. A rule left in no spec is a standalone rule. A rule
// with anchored sections under it is refused, since they would fall under
// whatever precedes it.
func UnarrangeRule(tx *sql.Tx, now time.Time, docID int64, rule, actorID string, eventID int64) error {
	ruleID, err := ruleByRefString(tx, rule)
	if err != nil {
		return err
	}
	e, err := openEditableSpec(tx, now, docID, actorID, eventID)
	if err != nil {
		return err
	}
	anchor, err := e.anchorOf(tx, ruleID)
	if err != nil {
		return err
	}
	if anchor == "" {
		return fmt.Errorf("doc %d does not arrange %s: %w", docID, rule, ErrNotFound)
	}
	parsed, err := designdoc.Parse([]byte(e.text))
	if err != nil {
		return fmt.Errorf("parse doc %d: %w", docID, err)
	}
	sec := parsed.SectionByAnchor(anchor)
	if sec == nil {
		return fmt.Errorf("%s: its section %s is not in the text of doc %d: %w", rule, anchor, docID, ErrInvalidInput)
	}
	end := sec.Index + 1
	for end < len(parsed.Sections) && parsed.Sections[end].Level > sec.Level {
		if parsed.Sections[end].Anchor != "" {
			return fmt.Errorf("%s has %s under it in doc %d; unarrange or move that first: %w",
				rule, parsed.Sections[end].Anchor, docID, ErrInvalidInput)
		}
		end++
	}
	parsed.Sections = slices.Delete(parsed.Sections, sec.Index, end)
	if e.revising {
		if _, err := tx.Exec(`DELETE FROM doc_revision_rules WHERE doc_id = $1 AND rule_id = $2`, docID, ruleID); err != nil {
			return fmt.Errorf("unarrange rule %d from the revision of doc %d: %w", ruleID, docID, err)
		}
	}
	return e.write(tx, now, string(parsed.Bytes()), eventID, nil)
}

// sectionNumber is a section number the heading parser reads: "2", "2.1a".
var sectionNumber = regexp.MustCompile(`^\d+(\.\d+)*[a-z]?$`)

// endLine adds a newline to text that does not end in one.
func endLine(text string) string {
	if text == "" || strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

// placeRule decides where an arranged rule goes in d: the index to insert its
// section at, its heading level and its anchor. After puts it after the
// named section's subtree at the same depth, Under as the named section's
// last child, and neither at the end at the top level. The default anchor is
// the next free number there (WL-SPEC-77 §4): the next integer at the end of
// a sibling list, a letter suffix between two siblings.
func placeRule(tx *sql.Tx, e editableSpec, d *designdoc.Document, in model.ArrangeRuleInput) (idx, level int, anchor string, err error) {
	if in.After != "" && in.Under != "" {
		return 0, 0, "", fmt.Errorf("name --after or --under, not both: %w", ErrInvalidInput)
	}
	var target *designdoc.Section
	if ref := in.After + in.Under; ref != "" {
		if target, err = sectionOf(tx, e, d, ref); err != nil {
			return 0, 0, "", err
		}
	}
	var siblings []*designdoc.Section
	switch {
	case target == nil:
		idx, level = len(d.Sections), 2
		for _, s := range d.Sections {
			if s.Parent == nil {
				siblings = append(siblings, s)
			}
		}
	case in.Under != "":
		idx, level, siblings = subtreeEnd(d, target), target.Level+1, target.Children
	default:
		idx, level = subtreeEnd(d, target), target.Level
		if target.Parent != nil {
			siblings = target.Parent.Children
		} else {
			for _, s := range d.Sections {
				if s.Parent == nil {
					siblings = append(siblings, s)
				}
			}
		}
	}
	if in.Anchor != "" {
		anchor = in.Anchor
	} else if anchor, err = defaultAnchor(target, in.Under != "", siblings); err != nil {
		return 0, 0, "", err
	}
	if !strings.HasPrefix(anchor, "sec-") || strings.ContainsAny(anchor, " {}#") {
		return 0, 0, "", fmt.Errorf("anchor %q must look like sec-2.1a: %w", anchor, ErrInvalidInput)
	}
	if d.SectionByAnchor(anchor) != nil {
		return 0, 0, "", fmt.Errorf("anchor %s is taken in doc %d; pass --anchor: %w", anchor, e.id, ErrInvalidInput)
	}
	return idx, level, anchor, nil
}

// sectionOf finds the section a position names: a rule ref the spec
// arranges, or a section anchor.
func sectionOf(tx *sql.Tx, e editableSpec, d *designdoc.Document, ref string) (*designdoc.Section, error) {
	anchor := ref
	if !strings.HasPrefix(ref, "sec-") {
		ruleID, err := ruleByRefString(tx, ref)
		if err != nil {
			return nil, err
		}
		if anchor, err = e.anchorOf(tx, ruleID); err != nil {
			return nil, err
		}
		if anchor == "" {
			return nil, fmt.Errorf("doc %d does not arrange %s: %w", e.id, ref, ErrInvalidInput)
		}
	}
	sec := d.SectionByAnchor(anchor)
	if sec == nil {
		return nil, fmt.Errorf("doc %d has no section %s: %w", e.id, anchor, ErrInvalidInput)
	}
	return sec, nil
}

// subtreeEnd is the index just past sec and every section nested under it.
func subtreeEnd(d *designdoc.Document, sec *designdoc.Section) int {
	end := sec.Index + 1
	for end < len(d.Sections) && d.Sections[end].Level > sec.Level {
		end++
	}
	return end
}

// defaultAnchor numbers a new section placed after target (under=false),
// as target's last child (under=true), or last at the top level (target
// nil). siblings are the sections at the new section's depth.
// ponytail: one letter suffix only, since the heading parser reads one; a
// second insert between the same two siblings needs --anchor.
func defaultAnchor(target *designdoc.Section, under bool, siblings []*designdoc.Section) (string, error) {
	var anchored []*designdoc.Section
	for _, s := range siblings {
		if s.Anchor != "" {
			anchored = append(anchored, s)
		}
	}
	numberOf := func(s *designdoc.Section) (string, error) {
		if s.Number == "" {
			return "", fmt.Errorf("section %s has no number to count from; pass --anchor: %w", s.Anchor, ErrInvalidInput)
		}
		return s.Number, nil
	}
	if target == nil || under {
		if len(anchored) == 0 {
			if target == nil {
				return "sec-1", nil
			}
			n, err := numberOf(target)
			return "sec-" + n + ".1", err
		}
		n, err := numberOf(anchored[len(anchored)-1])
		if err != nil {
			return "", err
		}
		return "sec-" + nextNumber(n), nil
	}
	n, err := numberOf(target)
	if err != nil {
		return "", err
	}
	if anchored[len(anchored)-1] == target {
		return "sec-" + nextNumber(n), nil
	}
	if last := n[len(n)-1]; last >= 'a' && last < 'z' {
		return "sec-" + n[:len(n)-1] + string(last+1), nil
	} else if last == 'z' {
		return "", fmt.Errorf("no letter left after %s; pass --anchor: %w", n, ErrInvalidInput)
	}
	return "sec-" + n + "a", nil
}

// nextNumber increments a section number's last component and drops its
// letter suffix: 2.1a becomes 2.2.
func nextNumber(n string) string {
	head, last := "", n
	if i := strings.LastIndex(n, "."); i >= 0 {
		head, last = n[:i+1], n[i+1:]
	}
	last = strings.TrimRight(last, "abcdefghijklmnopqrstuvwxyz")
	v, _ := strconv.Atoi(last)
	return head + strconv.Itoa(v+1)
}
