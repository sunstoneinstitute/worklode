package store

import (
	"context"
	"database/sql"
	"errors"
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
	// ruleID and ruleVersion name the rule version shown, 0 for a heading.
	ruleID      int64
	ruleVersion int
}

// arrangedTexts reads the entries of table (doc_rules, or doc_rule_versions
// narrowed to one version by where) with the text each shows, per document
// id in position order.
func arrangedTexts(ctx context.Context, q rowQueryer, table, where string, args ...any) (map[int64][]arrangedText, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT x.doc_id, coalesce(x.heading, v.heading, ''), coalesce(v.body, ''), x.rule_id IS NOT NULL,
		        coalesce(`+ruleRefSQL("p", "r")+`, ''), x.depth, coalesce(x.slug, ''),
		        coalesce(x.rule_id, 0), coalesce(x.rule_version, 0)
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
		if err := rows.Scan(&id, &t.heading, &t.body, &t.rule, &t.ref, &t.depth, &t.slug, &t.ruleID, &t.ruleVersion); err != nil {
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
	rendered, _ := renderSpans(body, texts)
	return rendered
}

// renderedSpan is a byte range [Start, End) of a rendered spec and where its
// bytes come from (WL-REQ-1317): the body of rule version Rule/RuleVersion
// from byte Offset, or with Rule 0 docs.body from byte Offset. Offset -1
// marks derived text, a heading line or the blank lines around a rule body,
// which belongs to neither. Anchor is the section the span sits in.
type renderedSpan struct {
	Start, End  int
	Rule        int64
	RuleVersion int
	Offset      int
	Anchor      string
}

// renderSpans is renderBody's text with the span map that tiles it.
func renderSpans(body string, texts []arrangedText) (string, []renderedSpan) {
	whole := []renderedSpan{{End: len(body), Offset: 0}}
	if len(texts) == 0 || body == "" {
		return body, whole[:min(1, len(body))]
	}
	doc, err := designdoc.Parse([]byte(body))
	if err != nil {
		return body, whole
	}
	// Where each parsed section's heading and body sit in docs.body; an
	// unedited Document's Bytes is its source, frontmatter first.
	type orig struct{ head, body, bodyLen int }
	origs := map[*designdoc.Section]orig{}
	pos := len(body)
	for _, sec := range doc.Sections {
		pos -= len(sec.HeadingAndBody())
	}
	prefixLen, origPreamble := pos, len(doc.Preamble)
	for _, sec := range doc.Sections {
		h := len(sec.Heading())
		origs[sec] = orig{head: pos, body: pos + h, bodyLen: len(sec.Body)}
		pos += h + len(sec.Body)
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
	entry := map[*designdoc.Section]int{}
	last := len(doc.Sections) - 1
	for i, t := range texts {
		sec := placeholders[i]
		entry[sec] = i
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
	rendered := string(doc.Bytes())

	var spans []renderedSpan
	at, anchor := 0, ""
	add := func(n int, rule int64, ruleVersion, offset int) {
		if n <= 0 {
			return
		}
		sp := renderedSpan{Start: at, End: at + n, Rule: rule, RuleVersion: ruleVersion, Offset: offset, Anchor: anchor}
		at += n
		if k := len(spans) - 1; k >= 0 {
			p := &spans[k]
			if p.Rule == rule && p.RuleVersion == ruleVersion &&
				((offset < 0 && p.Offset < 0) || (offset >= 0 && p.Offset >= 0 && p.Offset+p.End-p.Start == offset)) {
				p.End = sp.End
				return
			}
		}
		spans = append(spans, sp)
	}
	// template is n rendered bytes copied from docs.body at off, of which
	// only the first have are there (endLine may have added one).
	template := func(n, off, have int) {
		add(min(n, have), 0, 0, off)
		add(n-min(n, have), 0, 0, -1)
	}
	template(prefixLen-origPreamble+len(doc.Preamble), 0, prefixLen)
	for _, sec := range doc.Sections {
		o, parsed := origs[sec]
		i, placed := entry[sec]
		if placed {
			anchor = sec.Anchor
		}
		h := len(sec.Heading())
		if placed || !parsed {
			add(h, 0, 0, -1)
		} else {
			template(h, o.head, h)
		}
		if !placed || !texts[i].rule {
			template(len(sec.Body), o.body, o.bodyLen)
			continue
		}
		// The rule's text sits in the rendered body with its surrounding
		// line breaks re-cut (sectionBody); the bytes both share are the
		// rule's, the rest derived.
		t, r := texts[i], sec.Body
		trim := strings.Trim(t.body, "\r\n")
		if trim == "" {
			add(len(r), 0, 0, -1)
			continue
		}
		rs := len(r) - len(strings.TrimLeft(r, "\r\n"))
		ts := len(t.body) - len(strings.TrimLeft(t.body, "\r\n"))
		re, te := rs+len(trim), ts+len(trim)
		for rs > 0 && ts > 0 && r[rs-1] == t.body[ts-1] {
			rs, ts = rs-1, ts-1
		}
		for re < len(r) && te < len(t.body) && r[re] == t.body[te] {
			re, te = re+1, te+1
		}
		add(rs, 0, 0, -1)
		add(re-rs, t.ruleID, t.ruleVersion, ts)
		add(len(r)-re, 0, 0, -1)
	}
	return rendered, spans
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
		d.Body = model.Markdown(renderBody(string(d.Body), texts[d.ID]))
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

// mapRenderedRange maps [start, end) of spec docID's rendered body at its
// current version onto the rule text or template text it came from
// (WL-REQ-1317): a range in one rule's text is an anchor on that rule
// version, a range in template text an anchor on docs.body. A range on
// another version, over derived text, or across two spans is refused.
func mapRenderedRange(tx *sql.Tx, docID int64, version, start, end int) (model.ReviewAnchor, error) {
	var body string
	var current int
	err := tx.QueryRow(`SELECT body, version FROM docs WHERE id = $1`, docID).Scan(&body, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ReviewAnchor{}, fmt.Errorf("doc %d: %w", docID, ErrNotFound)
	}
	if err != nil {
		return model.ReviewAnchor{}, fmt.Errorf("read doc %d: %w", docID, err)
	}
	if version != current {
		return model.ReviewAnchor{}, fmt.Errorf("doc %d is at version %d; open threads on the current version: %w", docID, current, ErrInvalidInput)
	}
	texts, err := arrangedTexts(context.Background(), tx, "doc_rules", "x.doc_id = $1", docID)
	if err != nil {
		return model.ReviewAnchor{}, err
	}
	rendered, spans := renderSpans(body, texts[docID])
	if start < 0 || start >= end || end > len(rendered) {
		return model.ReviewAnchor{}, fmt.Errorf("byte range [%d,%d) is not inside a %d-byte text: %w", start, end, len(rendered), ErrInvalidInput)
	}
	k := 0
	for spans[k].End <= start {
		k++
	}
	sp := spans[k]
	if end > sp.End || sp.Offset < 0 {
		last := sp
		for _, s := range spans[k:] {
			if s.Start < end {
				last = s
			}
		}
		if last == sp {
			return model.ReviewAnchor{}, fmt.Errorf("byte range [%d,%d) covers %s: %w", start, end, describeSpan(sp), ErrInvalidInput)
		}
		return model.ReviewAnchor{}, fmt.Errorf("byte range [%d,%d) crosses from %s into %s: %w",
			start, end, describeSpan(sp), describeSpan(last), ErrInvalidInput)
	}
	a := model.ReviewAnchor{ByteStart: start - sp.Start + sp.Offset, ByteEnd: end - sp.Start + sp.Offset}
	if sp.Rule != 0 {
		a.Rule, a.Version = sp.Rule, sp.RuleVersion
	} else {
		a.Doc, a.Version = docID, current
	}
	return a, nil
}

// describeSpan names a span for an error: what text it is and its section.
func describeSpan(sp renderedSpan) string {
	what := "the template text"
	switch {
	case sp.Rule != 0:
		what = "the rule text"
	case sp.Offset < 0:
		what = "derived text (a heading line or the blank lines around a rule)"
	}
	if sp.Anchor == "" {
		return what + " before the first section"
	}
	return what + " at " + sp.Anchor
}
