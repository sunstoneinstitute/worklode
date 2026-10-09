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

// ruleClosureEdgesSQL lists the closure edges out of one rule with the
// target's current text, in a stable order: type, then project and number.
var ruleClosureEdgesSQL = `
SELECT e.type, r.id, ` + ruleRefSQL("p", "r") + `, r.kind, v.heading, v.body
  FROM rule_edges e
  JOIN rules r ON r.id = e.to_rule
  JOIN projects p ON p.id = r.project_id
  JOIN rule_versions v ON v.rule_id = r.id AND v.version = r.version
 WHERE e.from_rule = $1 AND e.type IN ('refines', 'needs')
 ORDER BY e.type, p.key, r.number`

// RuleClosure reads a rule's context closure (WL-SPEC-77 §4c): the rule plus
// every rule reachable over refines and needs, breadth-first, each rule once,
// with the edge it was first reached by. references edges are outside it.
// An unknown rule is ErrNotFound.
func (s *Store) RuleClosure(ctx context.Context, projectKey string, number int64) (*model.RuleClosure, error) {
	var rootID int64
	var body string
	root := model.RuleClosureMember{}
	err := s.db.QueryRowContext(ctx,
		`SELECT r.id, `+ruleRefSQL("p", "r")+`, r.kind, v.heading, v.body
		   FROM rules r
		   JOIN projects p ON p.id = r.project_id
		   JOIN rule_versions v ON v.rule_id = r.id AND v.version = r.version
		  WHERE p.key = $1 AND r.number = $2`, projectKey, number,
	).Scan(&rootID, &root.Ref, &root.Kind, &root.Heading, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("rule %s: %w", designdoc.FormatRuleRef(projectKey, number, ""), ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read rule %s: %w", designdoc.FormatRuleRef(projectKey, number, ""), err)
	}
	root.Words = len(strings.Fields(body))
	out := &model.RuleClosure{Rule: root.Ref, Members: []model.RuleClosureMember{root}, Words: root.Words}
	queue := []int64{rootID}
	seen := map[int64]bool{rootID: true}
	// ponytail: one query per member; batch by level if closures grow past
	// tens of rules.
	for i := 0; i < len(queue); i++ {
		type hop struct {
			id int64
			m  model.RuleClosureMember
		}
		rows, err := s.db.QueryContext(ctx, ruleClosureEdgesSQL, queue[i])
		if err != nil {
			return nil, fmt.Errorf("read closure edges of %s: %w", out.Members[i].Ref, err)
		}
		hops, err := collectRows(rows, "read closure edges of "+out.Members[i].Ref, func(r rowScanner) (hop, error) {
			var h hop
			var body string
			err := r.Scan(&h.m.Edge, &h.id, &h.m.Ref, &h.m.Kind, &h.m.Heading, &body)
			h.m.Words = len(strings.Fields(body))
			return h, err
		})
		if err != nil {
			return nil, err
		}
		for _, h := range hops {
			if seen[h.id] {
				continue
			}
			seen[h.id] = true
			h.m.From = out.Members[i].Ref
			queue = append(queue, h.id)
			out.Members = append(out.Members, h.m)
			out.Words += h.m.Words
		}
	}
	s.metrics.ruleClosure(len(out.Members))
	return out, nil
}
