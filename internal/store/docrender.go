package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// docrender.go assembles a spec's text on read (WL-REQ-1299): each
// arranged entry shows the heading and body of the rule version its
// arrangement row holds, and every span outside the entries is the template
// text docs.body stores. docs.body keeps each entry's heading line as the
// placeholder for its position and no rule text (specTemplate); a body
// written before that still carries a copy, which the renderer replaces the
// same way. An entry with no placeholder is inserted before the next entry
// that has one.

// arrangedText is the text one arrangement entry shows: a rule version's
// heading and body, or a spec heading's text with no body.
type arrangedText struct {
	heading  string
	body     string
	rule     bool
	position int
	depth    int
}

// arrangedTexts reads the entries of table (doc_rules, or doc_rule_versions
// narrowed to one version by where) with the text each shows, keyed by
// document id and anchor.
func arrangedTexts(ctx context.Context, q rowQueryer, table, where string, args ...any) (map[int64]map[string]arrangedText, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT x.doc_id, x.anchor, coalesce(x.heading, v.heading, ''), coalesce(v.body, ''), x.rule_id IS NOT NULL,
		        x.position, x.depth
		   FROM `+table+` x
		   LEFT JOIN rule_versions v ON v.rule_id = x.rule_id AND v.version = x.rule_version
		  WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("read arranged text from %s: %w", table, err)
	}
	defer rows.Close()
	out := map[int64]map[string]arrangedText{}
	for rows.Next() {
		var id int64
		var anchor string
		var t arrangedText
		if err := rows.Scan(&id, &anchor, &t.heading, &t.body, &t.rule, &t.position, &t.depth); err != nil {
			return nil, fmt.Errorf("scan arranged text from %s: %w", table, err)
		}
		if out[id] == nil {
			out[id] = map[string]arrangedText{}
		}
		out[id][anchor] = t
	}
	return out, rows.Err()
}

// renderBody is body with each anchored section's heading and text replaced
// by what its arrangement entry shows. A body that does not parse, or that
// already agrees with every entry, is returned unchanged, so a document with
// no arrangement renders byte for byte as stored.
func renderBody(body string, texts map[string]arrangedText) string {
	if len(texts) == 0 {
		return body
	}
	doc, err := designdoc.Parse([]byte(body))
	if err != nil {
		return body
	}
	changed := insertMissingEntries(doc, texts)
	last := len(doc.Sections) - 1
	for _, sec := range doc.Sections {
		t, ok := texts[sec.Anchor]
		if sec.Anchor == "" || !ok {
			continue
		}
		if sec.Title != t.heading {
			sec.Title, changed = t.heading, true
		}
		// An empty stored body is the template's placeholder, filled even
		// for a rule with no text so the blank lines it was written with
		// come back.
		if t.rule && (sec.Body == "" || !sameRuleBody(sec.Body, t.body)) {
			sec.Body, changed = t.body, true
			if strings.Trim(t.body, "\r\n") != "" {
				sec.Body = sectionBody(t.body, sec.Index == last)
			}
		}
	}
	if !changed {
		return body
	}
	return string(doc.Bytes())
}

// insertMissingEntries adds a section for each entry whose anchor doc has no
// heading for, before the next entry in position order that has one, or at
// the end. It reports whether it added any.
func insertMissingEntries(doc *designdoc.Document, texts map[string]arrangedText) bool {
	anchors := make([]string, 0, len(texts))
	for a := range texts {
		anchors = append(anchors, a)
	}
	slices.SortFunc(anchors, func(a, b string) int { return texts[a].position - texts[b].position })
	var missing []string
	added := false
	insert := func(at int) {
		for i, a := range missing {
			t := texts[a]
			number := strings.TrimPrefix(a, "sec-")
			if !sectionNumber.MatchString(number) {
				number = ""
			}
			doc.Sections = slices.Insert(doc.Sections, at+i, designdoc.NewSection(t.depth, number, t.heading, a, ""))
		}
		added = added || len(missing) > 0
		missing = missing[:0]
	}
	for _, a := range anchors {
		sec := doc.SectionByAnchor(a)
		if sec == nil {
			missing = append(missing, a)
			continue
		}
		insert(slices.Index(doc.Sections, sec))
	}
	if len(missing) > 0 {
		if n := len(doc.Sections); n > 0 {
			doc.Sections[n-1].Body = endLine(doc.Sections[n-1].Body)
		} else {
			doc.Preamble = endLine(doc.Preamble)
		}
		insert(len(doc.Sections))
	}
	for i, sec := range doc.Sections {
		sec.Index = i
	}
	return added
}

// specTemplate is body with the text of every section anchored at one of
// rules removed and its heading line kept as the entry's placeholder: the
// template docs.body holds for a spec (WL-REQ-1295). The migration that
// introduced it carries the same cut in SQL (spec_template). A body that
// does not parse is returned unchanged.
func specTemplate(body string, rules map[string]bool) string {
	doc, err := designdoc.Parse([]byte(body))
	if err != nil {
		return body
	}
	var b strings.Builder
	b.WriteString(doc.Preamble)
	for _, sec := range doc.Sections {
		b.WriteString(sec.Heading())
		if sec.Anchor == "" || !rules[sec.Anchor] {
			b.WriteString(sec.Body)
		}
	}
	return b.String()
}

// storeSpecTemplate cuts the rule text out of spec docID's stored body once
// syncRules has written its arrangement from that text.
func storeSpecTemplate(tx *sql.Tx, docID int64) error {
	var body string
	if err := tx.QueryRow(`SELECT body FROM docs WHERE id = $1`, docID).Scan(&body); err != nil {
		return fmt.Errorf("read doc %d body: %w", docID, err)
	}
	rows, err := tx.Query(`SELECT anchor FROM doc_rules WHERE doc_id = $1 AND rule_id IS NOT NULL`, docID)
	if err != nil {
		return fmt.Errorf("read rule anchors of doc %d: %w", docID, err)
	}
	anchors, err := scanColumn[string](rows, fmt.Sprintf("rule anchors of doc %d", docID))
	if err != nil {
		return err
	}
	rules := make(map[string]bool, len(anchors))
	for _, a := range anchors {
		rules[a] = true
	}
	if t := specTemplate(body, rules); t != body {
		if _, err := tx.Exec(`UPDATE docs SET body = $2 WHERE id = $1`, docID, t); err != nil {
			return fmt.Errorf("store template of doc %d: %w", docID, err)
		}
	}
	return nil
}

// sameRuleBody compares two rule bodies ignoring the blank lines around
// them, which depend on where the rule sits in a document (sectionBody).
func sameRuleBody(a, b string) bool {
	return strings.Trim(a, "\r\n") == strings.Trim(b, "\r\n")
}

// renderDocs replaces each document's stored body with its rendered text,
// reading every arrangement in one query. Plans have no arrangement and are
// left as stored.
func renderDocs(ctx context.Context, q rowQueryer, docs ...*model.Doc) error {
	ids := make([]int64, 0, len(docs))
	for _, d := range docs {
		if d.Kind != "plan" {
			ids = append(ids, d.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	texts, err := arrangedTexts(ctx, q, "doc_rules", "x.doc_id = ANY($1)", ids)
	if err != nil {
		return err
	}
	for _, d := range docs {
		d.Body = renderBody(d.Body, texts[d.ID])
	}
	return nil
}

// renderDocList is renderDocs over a slice of documents.
func renderDocList(ctx context.Context, q rowQueryer, docs []model.Doc) error {
	ptrs := make([]*model.Doc, len(docs))
	for i := range docs {
		ptrs[i] = &docs[i]
	}
	return renderDocs(ctx, q, ptrs...)
}
