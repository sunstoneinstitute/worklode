package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

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
// Acceptance is the owner's act: a rule with no owner, or another owner, is
// ErrForbidden. A rule whose newest version is not a draft is
// ErrBadTransition.
func (s *Store) AcceptRule(ctx context.Context, projectKey string, number int64, actor string) (r *model.Rule, err error) {
	defer func() { s.metrics.ruleOp("accept", err) }()
	ref := designdoc.FormatRuleRef(projectKey, number, "")
	extID, err := randomExternalID()
	if err != nil {
		return nil, err
	}
	payload, err := EventPayload(map[string]any{"actor": actor, "rule": ref})
	if err != nil {
		return nil, err
	}
	_, _, err = s.RecordEvent(ctx, "cli", extID, "rule.accepted", payload, func(tx *sql.Tx, _ int64) error {
		id, err := RuleIDByRef(tx, projectKey, number)
		if err != nil {
			return err
		}
		var status string
		var owner sql.NullString
		if err := tx.QueryRow(`SELECT status, owner FROM rules WHERE id = $1 FOR NO KEY UPDATE`, id).Scan(&status, &owner); err != nil {
			return fmt.Errorf("read rule %s: %w", ref, err)
		}
		if !owner.Valid {
			return fmt.Errorf("rule %s has no owner to accept it: %w", ref, ErrForbidden)
		}
		if actor == "" || owner.String != actor {
			return fmt.Errorf("rule %s is owned by %s, not %s: %w", ref, owner.String, actor, ErrForbidden)
		}
		if status != "draft" {
			return fmt.Errorf("rule %s is %s; only a draft version is accepted: %w", ref, status, ErrBadTransition)
		}
		_, err = tx.Exec(`UPDATE rules SET status = 'accepted', updated_at = now() WHERE id = $1`, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetRule(ctx, projectKey, number)
}
