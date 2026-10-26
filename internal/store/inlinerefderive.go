package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/inlineref"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// DeriveTargets lists the ids of the nodes of p.Kind in project that p
// selects (WL-REQ-1860): the named refs, else the kind's own filters, else
// every node under All. Selecting nothing is ErrInvalidInput. A superseded or
// withdrawn doc or rule is skipped when only All selects it, because no
// normalization writes one. A positive Limit returns one page of the selection
// in id order after p.After, and next is the After of the following page,
// empty on the last.
func (s *Store) DeriveTargets(ctx context.Context, project string, p model.RefDeriveParams) (ids []string, next string, err error) {
	ids, err = s.deriveSelection(ctx, project, p)
	if err != nil || (p.Limit <= 0 && p.After == "") {
		return ids, "", err
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	start, found := slices.BinarySearch(ids, p.After)
	if found {
		start++
	}
	ids = ids[start:]
	if p.Limit > 0 && len(ids) > p.Limit {
		ids = ids[:p.Limit]
		next = ids[len(ids)-1]
	}
	return ids, next, nil
}

// deriveSelection lists the ids DeriveTargets pages.
func (s *Store) deriveSelection(ctx context.Context, project string, p model.RefDeriveParams) ([]string, error) {
	switch p.Kind {
	case "doc", "task", "rule":
	default:
		return nil, fmt.Errorf("derive %q: only doc, task or rule: %w", p.Kind, ErrInvalidInput)
	}
	filtered := len(p.Status) > 0 || p.DocKind != "" || p.TaskKind != ""
	if len(p.Refs) == 0 && !filtered && !p.All {
		return nil, fmt.Errorf("derive %s: name refs or give --status, --kind or --all: %w", p.Kind, ErrInvalidInput)
	}
	var ids []string
	if len(p.Refs) > 0 {
		for _, ref := range p.Refs {
			id, err := s.deriveRefID(ctx, project, p.Kind, ref)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	statuses := p.Status
	if len(statuses) == 0 {
		statuses = []string{""}
	}
	for _, st := range statuses {
		switch p.Kind {
		case "task":
			f := TaskFilter{Project: project, Kind: p.TaskKind, OmitBody: true}
			if st != "" {
				f.States = []string{st}
			}
			ts, err := s.ListTasks(ctx, f)
			if err != nil {
				return nil, err
			}
			for _, t := range ts {
				ids = append(ids, t.ID)
			}
		case "doc":
			ds, err := s.ListDocs(ctx, DocFilter{Project: project, Kind: p.DocKind, Status: st})
			if err != nil {
				return nil, err
			}
			for _, d := range ds {
				if st != "" || !terminalStatus(d.Status) {
					ids = append(ids, strconv.FormatInt(d.ID, 10))
				}
			}
		case "rule":
			rs, err := s.ListRules(ctx, RuleFilter{Project: project, Status: st})
			if err != nil {
				return nil, err
			}
			for _, r := range rs {
				if st != "" || !terminalStatus(r.Status) {
					ids = append(ids, strconv.FormatInt(r.ID, 10))
				}
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func terminalStatus(s string) bool { return s == "withdrawn" || s == "superseded" }

// deriveRefID resolves one named ref of kind in project to a node id.
func (s *Store) deriveRefID(ctx context.Context, project string, kind model.NodeKind, ref string) (string, error) {
	switch kind {
	case "task":
		t, err := s.GetTask(ctx, ref)
		if err != nil {
			return "", err
		}
		if t.Project != project {
			return "", fmt.Errorf("task %s is not in project %s: %w", ref, project, ErrNotFound)
		}
		return t.ID, nil
	case "doc":
		d, err := s.ResolveDocRef(ctx, ref)
		if err != nil {
			return "", err
		}
		if d.Project != project {
			return "", fmt.Errorf("doc %s is not in project %s: %w", ref, project, ErrNotFound)
		}
		return strconv.FormatInt(d.ID, 10), nil
	}
	rr, ok := designdoc.ParseRuleRef(ref)
	if !ok {
		return "", fmt.Errorf("rule ref %q must look like WL-REQ-12: %w", ref, ErrInvalidInput)
	}
	r, err := s.GetRule(ctx, rr.Key, rr.Number)
	if err != nil {
		return "", err
	}
	if r.Project != project {
		return "", fmt.Errorf("rule %s is not in project %s: %w", ref, project, ErrNotFound)
	}
	return strconv.FormatInt(r.ID, 10), nil
}

// field is one Markdown field of a node: its name, the version it is at (nil
// when unversioned) and its text.
type field struct {
	name    string
	version *int
	text    string
}

// nodeFields reads the Markdown fields derive extracts from a doc, rule or
// task, and the node's display ref.
func nodeFields(tx *sql.Tx, project string, kind model.NodeKind, id string) (ref string, fields []field, err error) {
	switch kind {
	case "task":
		var title, body string
		err = tx.QueryRow(`SELECT title, coalesce(body, '') FROM tasks WHERE id = $1 AND project_id = $2`, id, project).Scan(&title, &body)
		return id, []field{{"title", nil, title}, {"body", nil, body}}, notFoundIfNoRows(err, "task "+id)
	case "doc":
		var title, body string
		var v int
		err = tx.QueryRow(`SELECT coalesce(`+docRefSQL("p", "d")+`, d.slug), d.title, d.body, d.version
		  FROM docs d JOIN projects p ON p.id = d.project_id WHERE d.id = $1::bigint AND d.project_id = $2`, id, project).
			Scan(&ref, &title, &body, &v)
		return ref, []field{{"title", nil, title}, {"body", &v, body}}, notFoundIfNoRows(err, "doc "+id)
	}
	var heading, body string
	var v int
	err = tx.QueryRow(`SELECT `+ruleRefSQL("p", "r")+`, rv.heading, rv.body, r.version
	  FROM rules r JOIN projects p ON p.id = r.project_id
	  JOIN rule_versions rv ON rv.rule_id = r.id AND rv.version = r.version
	 WHERE r.id = $1::bigint AND r.project_id = $2`, id, project).Scan(&ref, &heading, &body, &v)
	return ref, []field{{"heading", &v, heading}, {"body", &v, body}}, notFoundIfNoRows(err, "rule "+id)
}

func notFoundIfNoRows(err error, what string) error {
	if err == sql.ErrNoRows {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	return err
}

// DeriveNode normalizes one doc, rule or task of project (WL-REQ-1854), then
// re-extracts its inline references (WL-REQ-1851) and reports what changed.
// The caller commits or rolls back the transaction, which is how a dry run
// reports without writing.
func DeriveNode(tx *sql.Tx, now time.Time, project string, kind model.NodeKind, id string, eventID int64) (model.RefDeriveNode, error) {
	ctx := context.Background()
	node := model.RefDeriveNode{Added: []model.InlineRef{}, Removed: []model.InlineRef{}, Unresolved: []string{}}
	ref, _, err := nodeFields(tx, project, kind, id)
	node.Ref = ref
	if err != nil {
		return node, err
	}
	before, err := listInlineRefs(ctx, tx, kind, id, "out")
	if err != nil {
		return node, err
	}
	if _, node.Diff, err = NormalizeNode(tx, now, project, kind, id, eventID); err != nil {
		return node, err
	}
	node.Rewrites = strings.Count(node.Diff, "\n+")
	_, fields, err := nodeFields(tx, project, kind, id)
	if err != nil {
		return node, err
	}
	keys, err := projectKeys(tx)
	if err != nil {
		return node, err
	}
	var key string
	if err := tx.QueryRow(`SELECT key FROM projects WHERE id = $1`, project).Scan(&key); err != nil {
		return node, fmt.Errorf("read key of project %q: %w", project, err)
	}
	for _, f := range fields {
		if err := deriveInlineRefs(tx, project, kind, id, f.name, f.version, f.text, f.text, eventID); err != nil {
			return node, err
		}
		_, unresolved, err := resolveHits(tx, project, kind, id, inlineref.Scan(f.text, keys), nil)
		if err != nil {
			return node, err
		}
		for _, h := range unresolved {
			if r := hitRef(h, key); !slices.Contains(node.Unresolved, r) {
				node.Unresolved = append(node.Unresolved, r)
			}
		}
	}
	if err := pruneClassifiedEdges(tx, kind, id, fields[len(fields)-1].version); err != nil {
		return node, err
	}
	after, err := listInlineRefs(ctx, tx, kind, id, "out")
	if err != nil {
		return node, err
	}
	edge := func(r model.InlineRef) string { return r.Field + "|" + string(r.ToKind) + "|" + r.ToID }
	node.Added = refsMissingFrom(after, before, edge)
	node.Removed = refsMissingFrom(before, after, edge)
	return node, nil
}

// refsMissingFrom lists the rows of a whose key b does not have; never nil.
func refsMissingFrom(a, b []model.InlineRef, key func(model.InlineRef) string) []model.InlineRef {
	have := map[string]bool{}
	for _, r := range b {
		have[key(r)] = true
	}
	out := []model.InlineRef{}
	for _, r := range a {
		if !have[key(r)] {
			out = append(out, r)
		}
	}
	return out
}
