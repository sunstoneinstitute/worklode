package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// AddRule creates a rule arranged in no document, at draft version 1,
// owned by actor and numbered from the project's RULE counter (WL-SPEC-77
// §19.2). Its references edges are derived from its text as on any version
// write. One rule.added event records it.
func (s *Store) AddRule(ctx context.Context, in model.AddRuleInput, actor string) (r *model.Rule, err error) {
	defer func() { s.metrics.ruleOp("add", err) }()
	in.Heading = strings.TrimSpace(in.Heading)
	if in.Heading == "" {
		return nil, fmt.Errorf("rule heading is required: %w", ErrInvalidInput)
	}
	if in.Kind == "" {
		in.Kind = designdoc.RuleKindRequirement
	}
	if !slices.Contains(designdoc.RuleKinds, in.Kind) {
		return nil, fmt.Errorf("rule kind %q: must be one of %s: %w", in.Kind, strings.Join(designdoc.RuleKinds, ", "), ErrInvalidInput)
	}
	extID, err := randomExternalID()
	if err != nil {
		return nil, err
	}
	payload, err := EventPayload(map[string]any{"actor": actor, "project": in.Project, "heading": in.Heading, "kind": in.Kind, "tags": in.Tags})
	if err != nil {
		return nil, err
	}
	var key string
	var number int64
	_, _, err = s.RecordEvent(ctx, "cli", extID, "rule.added", payload, func(tx *sql.Tx, eventID int64) error {
		if err := tx.QueryRow(`SELECT key FROM projects WHERE id = $1`, in.Project).Scan(&key); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("project %q: %w", in.Project, ErrNotFound)
		} else if err != nil {
			return fmt.Errorf("read project %q: %w", in.Project, err)
		}
		body := sectionBody(in.Body, true)
		id, _, err := insertRule(tx, in.Project, in.Heading, body)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(
			`UPDATE rules SET owner = nullif($2, ''), tags = $3::text[], kind = $4 WHERE id = $1 RETURNING number`,
			id, actor, nonNil(in.Tags), in.Kind).Scan(&number); err != nil {
			return fmt.Errorf("set meta of rule %d: %w", id, err)
		}
		if err := deriveReferences(tx, in.Project, id, in.Heading+"\n"+body); err != nil {
			return err
		}
		return MergeEventPayload(tx, eventID, map[string]any{"rule": designdoc.FormatRuleRef(key, number, in.Kind)})
	})
	if err != nil {
		return nil, err
	}
	return s.GetRule(ctx, key, number)
}

// AcceptRule accepts a rule's newest draft version (WL-SPEC-77 §19.2).
// Acceptance is the owner's act. A rule with no owner of its own is owned by
// the owner of the lowest-id spec arranging it. No owner at all, or another
// owner, is ErrForbidden. A rule whose newest version is not a draft is
// ErrBadTransition.
//
// Every spec arranging the rule moves to the accepted version, an accepted
// one through a version bump (§19.4). Section 10's gates then apply to the
// version: when it trips a mechanical check (a wl: term, code surface or
// acceptance criteria changed, or the rule has a referrer), or substantive
// says its author judged it so, a review task is minted for the rule owner
// and the reviewers of every bumped spec, those reviewers owe an approval of
// the spec's new version, and every accepted plan covering the rule is
// marked stale. A first version amends nothing and passes no gate.
func (s *Store) AcceptRule(ctx context.Context, projectKey string, number int64, substantive bool, actor string) (r *model.Rule, err error) {
	defer func() { s.metrics.ruleOp("accept", err) }()
	ref := designdoc.FormatRuleRef(projectKey, number, "")
	extID, err := randomExternalID()
	if err != nil {
		return nil, err
	}
	payload, err := EventPayload(map[string]any{"actor": actor, "rule": ref, "substantive": substantive})
	if err != nil {
		return nil, err
	}
	now := s.Now()
	_, _, err = s.RecordEvent(ctx, "cli", extID, "rule.accepted", payload, func(tx *sql.Tx, eventID int64) error {
		id, err := RuleIDByRef(tx, projectKey, number)
		if err != nil {
			return err
		}
		var status, project string
		var owner sql.NullString
		var version int
		// A rule with no owner of its own is owned by the owner of the
		// lowest-id spec arranging it, resolved here and never stored.
		if err := tx.QueryRow(`
			SELECT r.status, coalesce(r.owner, (
			         SELECT d.owner FROM doc_rules dr JOIN docs d ON d.id = dr.doc_id
			          WHERE dr.rule_id = r.id AND d.kind = 'spec' AND d.owner IS NOT NULL
			          ORDER BY d.id LIMIT 1)),
			       r.version, r.project_id
			  FROM rules r WHERE r.id = $1 FOR NO KEY UPDATE OF r`, id).
			Scan(&status, &owner, &version, &project); err != nil {
			return fmt.Errorf("read rule %s: %w", ref, err)
		}
		if !owner.Valid {
			return fmt.Errorf("rule %s has no owner and no arranging spec with one; set one with lode rule set owner %s <actor>: %w", ref, ref, ErrForbidden)
		}
		if actor == "" || owner.String != actor {
			return fmt.Errorf("rule %s is owned by %s, not %s: %w", ref, owner.String, actor, ErrForbidden)
		}
		if status != "draft" {
			return fmt.Errorf("rule %s is %s; only a draft version is accepted: %w", ref, status, ErrBadTransition)
		}
		if _, err := tx.Exec(`UPDATE rules SET status = 'accepted', updated_at = now() WHERE id = $1`, id); err != nil {
			return fmt.Errorf("accept rule %s: %w", ref, err)
		}
		bumped, err := moveSpecsToAcceptedRules(tx, []int64{id}, 0)
		if err != nil {
			return err
		}
		if version == 1 {
			return nil
		}
		gate, err := ruleGate(tx, id, version, substantive)
		if err != nil || gate == "" {
			return err
		}
		return reviewRuleVersion(tx, now, ruleVersionReview{
			id: id, project: project, ref: ruleRefOf(tx, id), version: version, owner: owner.String,
			gate: gate, actor: actor, bumped: bumped,
		}, eventID)
	})
	if err != nil {
		return nil, err
	}
	return s.GetRule(ctx, projectKey, number)
}

// ruleGate is WL-SPEC-77 §10's substantive test applied to version of rule
// id against the version before it (§19.4): the first mechanical check it
// trips, "referrer" when anything refers to the rule, "judged" when only the
// caller's judgment holds, "" for a non-substantive version.
func ruleGate(tx *sql.Tx, id int64, version int, judged bool) (string, error) {
	rows, err := tx.Query(
		`SELECT version, heading, body FROM rule_versions WHERE rule_id = $1 AND version IN ($2, $2 - 1) ORDER BY version`,
		id, version)
	if err != nil {
		return "", fmt.Errorf("read rule %d versions: %w", id, err)
	}
	docs := map[int]*designdoc.Document{}
	for rows.Next() {
		var v int
		var heading, body string
		if err := rows.Scan(&v, &heading, &body); err != nil {
			rows.Close()
			return "", fmt.Errorf("scan rule %d version: %w", id, err)
		}
		doc, err := designdoc.Parse([]byte("## " + heading + " {#rule}\n" + body))
		if err != nil {
			rows.Close()
			return "", fmt.Errorf("parse rule %d v%d: %w", id, v, err)
		}
		docs[v] = doc
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("read rule %d versions: %w", id, err)
	}
	if docs[version-1] != nil && docs[version] != nil {
		if f := designdoc.MechanicalFindings(docs[version-1], docs[version]); len(f) > 0 {
			return f[0].Rule, nil
		}
	}
	var referred bool
	if err := tx.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM rule_edges e WHERE e.to_rule = $1 AND e.type IN ('amends', 'supersedes'))
		     OR EXISTS (SELECT 1 FROM tasks t
		                  JOIN docs p ON p.id = t.plan_doc AND p.status = 'accepted' AND p.deleted_at IS NULL
		                  JOIN covered_rules c ON c.plan_id = p.id AND c.rule_id = $1
		                 WHERE t.deleted_at IS NULL AND t.state IN (`+claimedOpenStates+`))
		     OR EXISTS (SELECT 1 FROM doc_rules dr
		                  JOIN doc_edges e ON e.to_doc = dr.doc_id AND e.to_anchor = dr.anchor AND e.type IN ('requires', 'covers')
		                  JOIN docs d ON d.id = e.from_doc AND d.kind <> 'plan' AND d.status = 'accepted' AND d.deleted_at IS NULL
		                 WHERE dr.rule_id = $1)`, id).Scan(&referred); err != nil {
		return "", fmt.Errorf("referrers of rule %d: %w", id, err)
	}
	switch {
	case referred:
		return "referrer", nil
	case judged:
		return "judged", nil
	}
	return "", nil
}

// ruleVersionReview is what reviewRuleVersion needs about the accepted
// version: bumped maps each accepted spec moved to it to its new version.
type ruleVersionReview struct {
	id      int64
	project string
	ref     string
	version int
	owner   string
	gate    string
	actor   string
	bumped  map[int64]int
}

// reviewRuleVersion performs WL-SPEC-77 §10's consequences of a substantive
// rule version (§19.4): one review task for the rule owner and the bumped
// specs' reviewers, an awaiting approval for each reviewer on the spec's new
// version, and every accepted plan covering the rule marked stale.
func reviewRuleVersion(tx *sql.Tx, now time.Time, in ruleVersionReview, eventID int64) error {
	specs := slices.Sorted(maps.Keys(in.bumped))
	reviewers := []string{in.owner}
	for _, spec := range specs {
		rs, err := docReviewers(tx, spec)
		if err != nil {
			return err
		}
		if len(rs) == 0 {
			continue
		}
		if err := RequestDocApproval(tx, now, spec, in.bumped[spec]); err != nil {
			return err
		}
		for _, r := range rs {
			if !slices.Contains(reviewers, r) {
				reviewers = append(reviewers, r)
			}
		}
	}
	var about int64
	if len(specs) > 0 {
		about = specs[0]
	}
	task, err := CreateTask(tx, now, TaskInput{
		ProjectID: in.project,
		Kind:      "review",
		Priority:  "medium",
		Title:     fmt.Sprintf("Review %s v%d", in.ref, in.version),
		Body: fmt.Sprintf("Version %d of %s was accepted and is substantive (%s, WL-SPEC-77 §10).\n\nReviewers: %s.\n",
			in.version, in.ref, in.gate, strings.Join(reviewers, ", ")),
		AboutDoc:  about,
		CreatedBy: in.actor,
	}, eventID)
	if err != nil {
		return err
	}
	plans, err := acceptedPlansCovering(tx, []int64{in.id})
	if err != nil {
		return err
	}
	stale, err := MarkPlansStale(tx, now, plans, "amended", in.ref, nil, eventID)
	if err != nil {
		return err
	}
	return MergeEventPayload(tx, eventID, map[string]any{
		"gate": in.gate, "review_task": task.ID, "stale_plans": stale,
	})
}
