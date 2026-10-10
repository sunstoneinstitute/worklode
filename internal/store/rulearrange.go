package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// editableSpec is the text a rule arrangement writes and the table holding
// its arrangement: a draft spec's own body and doc_rules, or an accepted
// spec's candidate revision and doc_revision_rules (WL-REQ-1297). anchors is
// where an entry's anchor in that text is read: the derived one
// (doc_entries) or the candidate's own.
type editableSpec struct {
	id       int64
	text     string
	table    string
	anchors  string
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
		return editableSpec{id: docID, text: d.body, table: "doc_rules", anchors: "doc_entries"}, nil
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
	return editableSpec{id: docID, text: candidate, table: "doc_revision_rules", anchors: "doc_revision_rules", revising: true}, nil
}

// ruleSection is the section of d that holds rule ruleID (ref): the heading
// naming it, else the one at the anchor the arrangement holds it at. ok
// reports whether the spec arranges the rule at all.
func (e editableSpec) ruleSection(tx *sql.Tx, d *designdoc.Document, ruleID int64, ref string) (sec *designdoc.Section, ok bool, err error) {
	var anchor sql.NullString
	err = tx.QueryRow(`SELECT anchor FROM `+e.anchors+` WHERE doc_id = $1 AND rule_id = $2`, e.id, ruleID).Scan(&anchor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read arrangement of rule %d in doc %d: %w", ruleID, e.id, err)
	}
	for _, s := range d.Sections {
		if s.Anchor != "" && namedRef(s) == ref {
			return s, true, nil
		}
	}
	return d.SectionByAnchor(anchor.String), true, nil
}

// renumber rewrites d's anchors to the ones each section's place derives
// (WL-REQ-165). A candidate's arrangement follows its text's anchors.
func (e editableSpec) renumber(tx *sql.Tx, d *designdoc.Document) error {
	var olds, news []string
	for _, s := range d.Sections {
		if s.Anchor != "" {
			olds = append(olds, s.Anchor)
		}
	}
	d.DeriveAnchors()
	if !e.revising {
		return nil
	}
	for _, s := range d.Sections {
		if s.Anchor != "" {
			news = append(news, s.Anchor)
		}
	}
	if _, err := tx.Exec(
		`UPDATE doc_revision_rules r SET anchor = m.new
		   FROM unnest($2::text[], $3::text[]) AS m(old, new)
		  WHERE r.doc_id = $1 AND r.anchor = m.old`, e.id, olds, news); err != nil {
		return fmt.Errorf("renumber the revision of doc %d: %w", e.id, err)
	}
	return nil
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

// ArrangeRule places an existing rule in a spec (WL-REQ-1297): its
// newest accepted version's heading and body (a first draft's, when none is
// accepted, §19.4) are written into the spec's editable text at the chosen
// position, its heading naming the rule, and the spec's arrangement holds
// the rule there. Its number and anchor, and those of every entry after it,
// are derived from the new arrangement (WL-REQ-165). A draft spec is
// written in place; an accepted one through its candidate revision, and the
// arrangement lands with it. Returns the anchor the rule landed at.
func ArrangeRule(tx *sql.Tx, now time.Time, docID int64, in model.ArrangeRuleInput, actorID string, eventID int64) (string, error) {
	ruleID, err := ruleByRefString(tx, in.Rule)
	if err != nil {
		return "", err
	}
	ref := ruleRefOf(tx, ruleID)
	var version int
	var heading, body string
	if err := tx.QueryRow(
		`SELECT v.version, v.heading, v.body FROM rules r
		   JOIN rule_versions v ON v.rule_id = r.id
		    AND v.version = CASE WHEN r.status = 'draft' AND r.version > 1 THEN r.version - 1 ELSE r.version END
		  WHERE r.id = $1`, ruleID).Scan(&version, &heading, &body); err != nil {
		return "", fmt.Errorf("read rule %d: %w", ruleID, err)
	}
	e, err := openEditableSpec(tx, now, docID, actorID, eventID)
	if err != nil {
		return "", err
	}
	parsed, err := designdoc.Parse([]byte(e.text))
	if err != nil {
		return "", fmt.Errorf("parse doc %d: %w", docID, err)
	}
	if _, arranged, err := e.ruleSection(tx, parsed, ruleID, ref); err != nil {
		return "", err
	} else if arranged {
		return "", fmt.Errorf("doc %d already arranges %s: %w", docID, ref, ErrInvalidInput)
	}
	idx, level, err := placeRule(tx, e, parsed, in)
	if err != nil {
		return "", err
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
	// Any numbered anchor will do until renumber derives the real one.
	sec := designdoc.NewSection(level, "", heading, "sec-0", body)
	sec.Ref = ref
	parsed.Sections = slices.Insert(parsed.Sections, idx, sec)
	if err := e.renumber(tx, parsed); err != nil {
		return "", err
	}

	var pin func(*sql.Tx) error
	if e.revising {
		pin = func(tx *sql.Tx) error {
			_, err := tx.Exec(
				`INSERT INTO doc_revision_rules (doc_id, position, rule_id, rule_version, depth, anchor)
				 SELECT $1, coalesce(max(position) + 1, 0), $2, $3, $4, $5 FROM doc_revision_rules WHERE doc_id = $1`,
				docID, ruleID, version, level, sec.Anchor)
			if err != nil {
				return fmt.Errorf("arrange rule %d in doc %d: %w", ruleID, docID, err)
			}
			return nil
		}
	}
	return sec.Anchor, e.write(tx, now, string(parsed.Bytes()), eventID, pin)
}

// UnarrangeRule removes a rule from a spec without withdrawing it
// (WL-REQ-1297): its section leaves the spec's editable text and its
// arrangement row goes, and the entries after it are renumbered. A rule
// left in no spec is a standalone rule. An entry with deeper entries under
// it is refused, naming them, since they would fall under whatever
// precedes it. On an accepted spec it lands with the candidate revision,
// which mints a review (reviewUnarranged).
func UnarrangeRule(tx *sql.Tx, now time.Time, docID int64, rule, actorID string, eventID int64) error {
	ruleID, err := ruleByRefString(tx, rule)
	if err != nil {
		return err
	}
	ref := ruleRefOf(tx, ruleID)
	e, err := openEditableSpec(tx, now, docID, actorID, eventID)
	if err != nil {
		return err
	}
	parsed, err := designdoc.Parse([]byte(e.text))
	if err != nil {
		return fmt.Errorf("parse doc %d: %w", docID, err)
	}
	sec, arranged, err := e.ruleSection(tx, parsed, ruleID, ref)
	if err != nil {
		return err
	}
	if !arranged {
		return fmt.Errorf("doc %d does not arrange %s: %w", docID, ref, ErrNotFound)
	}
	if sec == nil {
		return fmt.Errorf("%s: its section is not in the text of doc %d: %w", ref, docID, ErrInvalidInput)
	}
	end := subtreeEnd(parsed, sec)
	var deeper []string
	for _, s := range parsed.Sections[sec.Index+1 : end] {
		if s.Anchor == "" {
			continue
		}
		if r := namedRef(s); r != "" {
			deeper = append(deeper, r)
		} else {
			deeper = append(deeper, s.Anchor)
		}
	}
	if len(deeper) > 0 {
		return fmt.Errorf("%s has %s under it in doc %d; unarrange or move those first: %w",
			ref, strings.Join(deeper, ", "), docID, ErrInvalidInput)
	}
	parsed.Sections = slices.Delete(parsed.Sections, sec.Index, end)
	for i, s := range parsed.Sections {
		s.Index = i
	}
	if e.revising {
		if _, err := tx.Exec(`DELETE FROM doc_revision_rules WHERE doc_id = $1 AND rule_id = $2`, docID, ruleID); err != nil {
			return fmt.Errorf("unarrange rule %d from the revision of doc %d: %w", ruleID, docID, err)
		}
	}
	if err := e.renumber(tx, parsed); err != nil {
		return err
	}
	return e.write(tx, now, string(parsed.Bytes()), eventID, nil)
}

// endLine adds a newline to text that does not end in one.
func endLine(text string) string {
	if text == "" || strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

// placeRule decides where an arranged rule goes in d: the index to insert its
// section at and its heading level. After puts it after the named section's
// subtree at the same depth, Under as the named section's last child, and
// neither at the end at the top level.
func placeRule(tx *sql.Tx, e editableSpec, d *designdoc.Document, in model.ArrangeRuleInput) (idx, level int, err error) {
	if in.After != "" && in.Under != "" {
		return 0, 0, fmt.Errorf("name --after or --under, not both: %w", ErrInvalidInput)
	}
	ref := in.After + in.Under
	if ref == "" {
		return len(d.Sections), 2, nil
	}
	target, err := sectionOf(tx, e, d, ref)
	if err != nil {
		return 0, 0, err
	}
	if in.Under != "" {
		return subtreeEnd(d, target), target.Level + 1, nil
	}
	return subtreeEnd(d, target), target.Level, nil
}

// sectionOf finds the section a position names: a rule ref the spec
// arranges, or a section anchor of its current text.
func sectionOf(tx *sql.Tx, e editableSpec, d *designdoc.Document, ref string) (*designdoc.Section, error) {
	if strings.HasPrefix(ref, "sec-") {
		if sec := d.SectionByAnchor(ref); sec != nil {
			return sec, nil
		}
		return nil, fmt.Errorf("doc %d has no section %s: %w", e.id, ref, ErrInvalidInput)
	}
	ruleID, err := ruleByRefString(tx, ref)
	if err != nil {
		return nil, err
	}
	sec, arranged, err := e.ruleSection(tx, d, ruleID, ruleRefOf(tx, ruleID))
	if err != nil {
		return nil, err
	}
	if !arranged || sec == nil {
		return nil, fmt.Errorf("doc %d does not arrange %s: %w", e.id, ref, ErrInvalidInput)
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
