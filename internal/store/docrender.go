package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// docrender.go assembles a spec's text on read (WL-REQ-1299): each
// arranged entry shows the heading and body of the rule version its
// arrangement row holds, numbered and anchored from its place (WL-REQ-165),
// and every span outside the entries is the template text docs.body stores.
// docs.body keeps one anchored heading line per entry as its placeholder,
// in position order, and no rule text (specTemplate); a body written before
// that still carries a copy, which the renderer replaces the same way. An
// entry with no placeholder goes at the end.

// arrangedText is the text one arrangement entry shows: a rule version's
// heading and body, or a spec heading's text with no body, with what places
// it: depth and slug.
type arrangedText struct {
	heading string
	body    string
	rule    bool
	ref     string
	depth   int
	slug    string
}

// arrangedTexts reads the entries of table (doc_rules, or doc_rule_versions
// narrowed to one version by where) with the text each shows, per document
// id in position order.
func arrangedTexts(ctx context.Context, q rowQueryer, table, where string, args ...any) (map[int64][]arrangedText, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT x.doc_id, coalesce(x.heading, v.heading, ''), coalesce(v.body, ''), x.rule_id IS NOT NULL,
		        coalesce(`+ruleRefSQL("p", "r")+`, ''), x.depth, coalesce(x.slug, '')
		   FROM `+table+` x
		   LEFT JOIN rule_versions v ON v.rule_id = x.rule_id AND v.version = x.rule_version
		   LEFT JOIN rules r ON r.id = x.rule_id
		   LEFT JOIN projects p ON p.id = r.project_id
		  WHERE `+where+`
		  ORDER BY x.doc_id, x.position`, args...)
	if err != nil {
		return nil, fmt.Errorf("read arranged text from %s: %w", table, err)
	}
	defer rows.Close()
	out := map[int64][]arrangedText{}
	for rows.Next() {
		var id int64
		var t arrangedText
		if err := rows.Scan(&id, &t.heading, &t.body, &t.rule, &t.ref, &t.depth, &t.slug); err != nil {
			return nil, fmt.Errorf("scan arranged text from %s: %w", table, err)
		}
		out[id] = append(out[id], t)
	}
	return out, rows.Err()
}

// deriveTexts is the number and anchor each entry derives (WL-REQ-165).
func deriveTexts(texts []arrangedText) []designdoc.Derived {
	entries := make([]designdoc.Entry, len(texts))
	for i, t := range texts {
		entries[i] = designdoc.Entry{Depth: t.depth, Slug: t.slug}
	}
	return designdoc.DeriveNumbers(entries)
}

// renderBody is body with the k-th anchored heading and its text replaced
// by what the k-th entry shows: heading, derived number and anchor, the
// rule's ref, and its body. A body that does not parse, or that already
// agrees with every entry, is returned unchanged, so a document with no
// arrangement renders byte for byte as stored.
func renderBody(body string, texts []arrangedText) string {
	if len(texts) == 0 {
		return body
	}
	doc, err := designdoc.Parse([]byte(body))
	if err != nil {
		return body
	}
	derived := deriveTexts(texts)
	var placeholders []*designdoc.Section
	for _, sec := range doc.Sections {
		if sec.Anchor != "" {
			placeholders = append(placeholders, sec)
		}
	}
	if len(texts) > len(placeholders) {
		if n := len(doc.Sections); n > 0 {
			doc.Sections[n-1].Body = endLine(doc.Sections[n-1].Body)
		} else {
			doc.Preamble = endLine(doc.Preamble)
		}
		for _, t := range texts[len(placeholders):] {
			sec := designdoc.NewSection(t.depth, "", "", "", "")
			sec.Index = len(doc.Sections)
			doc.Sections = append(doc.Sections, sec)
			placeholders = append(placeholders, sec)
		}
	}
	last := len(doc.Sections) - 1
	for i, t := range texts {
		sec := placeholders[i]
		sec.Level, sec.Number, sec.Anchor, sec.Title = t.depth, derived[i].Number, derived[i].Anchor, t.heading
		sec.Ref = ""
		if t.rule {
			sec.Ref = t.ref
		}
		// An empty stored body is the template's placeholder, filled even
		// for a rule with no text so the blank lines it was written with
		// come back.
		if t.rule && (sec.Body == "" || !sameRuleBody(sec.Body, t.body)) {
			sec.Body = t.body
			if strings.Trim(t.body, "\r\n") != "" {
				sec.Body = sectionBody(t.body, sec.Index == last)
			}
		}
	}
	return string(doc.Bytes())
}

// specTemplate is body with the text of the k-th anchored section removed
// when rules[k] holds, its heading line kept as the entry's placeholder: the
// template docs.body holds for a spec (WL-REQ-1295). The migration that
// introduced it carries the same cut in SQL (spec_template). A body that
// does not parse is returned unchanged.
func specTemplate(body string, rules []bool) string {
	doc, err := designdoc.Parse([]byte(body))
	if err != nil {
		return body
	}
	var b strings.Builder
	b.WriteString(doc.Preamble)
	k := 0
	for _, sec := range doc.Sections {
		b.WriteString(sec.Heading())
		rule := false
		if sec.Anchor != "" {
			rule = k < len(rules) && rules[k]
			k++
		}
		if !rule {
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
	rows, err := tx.Query(`SELECT rule_id IS NOT NULL FROM doc_rules WHERE doc_id = $1 ORDER BY position`, docID)
	if err != nil {
		return fmt.Errorf("read entries of doc %d: %w", docID, err)
	}
	rules, err := scanColumn[bool](rows, fmt.Sprintf("entries of doc %d", docID))
	if err != nil {
		return err
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
