package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// decisionKeyRe is the shape a Decision.Key must take: lowercase letters,
// digits and hyphens.
var decisionKeyRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// decisionResponseTypes are the six response types §10.1 defines.
var decisionResponseTypes = []string{
	"single_select",
	"multi_select",
	"single_select_notes",
	"pick_or_freetext",
	"yes_no",
	"freetext",
}

// decisionTypesWithOptions are the response types that pose a set of
// options; yes_no and freetext answer without one.
var decisionTypesWithOptions = []string{
	"single_select",
	"multi_select",
	"single_select_notes",
	"pick_or_freetext",
}

// ValidateDecisionSpec checks the shape of a decision as it is posed (025
// §10.1): a routable key, a response_type the store recognizes with the
// options it needs and none it doesn't, and no answer yet. It does not
// check that the key is unique within the task — the database's UNIQUE
// constraint on (task, key) is the backstop for that.
func ValidateDecisionSpec(d model.Decision) error {
	if d.Key == "" {
		return fmt.Errorf("decision: key is required: %w", ErrInvalidInput)
	}
	if !decisionKeyRe.MatchString(d.Key) {
		return fmt.Errorf("decision %q: key must match [a-z0-9-]+: %w", d.Key, ErrInvalidInput)
	}
	if d.Question == "" {
		return fmt.Errorf("decision %q: question is required: %w", d.Key, ErrInvalidInput)
	}
	if !slices.Contains(decisionResponseTypes, d.ResponseType) {
		return fmt.Errorf("decision %q: response_type %q is not one of %v: %w",
			d.Key, d.ResponseType, decisionResponseTypes, ErrInvalidInput)
	}

	wantsOptions := slices.Contains(decisionTypesWithOptions, d.ResponseType)
	switch {
	case wantsOptions && len(d.Options) == 0:
		return fmt.Errorf("decision %q: options is required for %s: %w", d.Key, d.ResponseType, ErrInvalidInput)
	case !wantsOptions && len(d.Options) != 0:
		return fmt.Errorf("decision %q: options is not used by %s: %w", d.Key, d.ResponseType, ErrInvalidInput)
	}

	seenLabels := make(map[string]bool, len(d.Options))
	for _, opt := range d.Options {
		if opt.Label == "" {
			return fmt.Errorf("decision %q: an option has no label: %w", d.Key, ErrInvalidInput)
		}
		if seenLabels[opt.Label] {
			return fmt.Errorf("decision %q: option label %q is declared twice: %w", d.Key, opt.Label, ErrInvalidInput)
		}
		seenLabels[opt.Label] = true
	}

	if d.ResponseType != "multi_select" {
		if d.MinPicks != nil {
			return fmt.Errorf("decision %q: min_picks is only used by multi_select: %w", d.Key, ErrInvalidInput)
		}
		if d.MaxPicks != nil {
			return fmt.Errorf("decision %q: max_picks is only used by multi_select: %w", d.Key, ErrInvalidInput)
		}
	} else {
		if d.MinPicks != nil && *d.MinPicks < 1 {
			return fmt.Errorf("decision %q: min_picks must be at least 1: %w", d.Key, ErrInvalidInput)
		}
		if d.MaxPicks != nil {
			if *d.MaxPicks < 1 {
				return fmt.Errorf("decision %q: max_picks must be at least 1: %w", d.Key, ErrInvalidInput)
			}
			if *d.MaxPicks > len(d.Options) {
				return fmt.Errorf("decision %q: max_picks must be at most len(options): %w", d.Key, ErrInvalidInput)
			}
		}
		if d.MinPicks != nil && d.MaxPicks != nil && *d.MinPicks > *d.MaxPicks {
			return fmt.Errorf("decision %q: min_picks must be at most max_picks: %w", d.Key, ErrInvalidInput)
		}
	}

	if d.Answer != nil {
		return fmt.Errorf("decision %q: answer must be absent when posing: %w", d.Key, ErrInvalidInput)
	}
	if d.DecidedAt != nil {
		return fmt.Errorf("decision %q: decided_at must be absent when posing: %w", d.Key, ErrInvalidInput)
	}

	return nil
}

// validateAnswer checks a submitted answer against the decision's spec
// (025 §10.1): the fields its response_type uses hold values that satisfy
// its rule, and every field it doesn't use is empty. An answer that
// smuggles an unused field is refused, so what is stored is exactly what
// the type defines.
func validateAnswer(d model.Decision, a model.DecisionAnswer) error {
	switch d.ResponseType {
	case "single_select":
		if err := requireEmptyAnswerFields(d, a, "notes", "freetext", "value"); err != nil {
			return err
		}
		return requireSinglePick(d, a.Picked)

	case "multi_select":
		if err := requireEmptyAnswerFields(d, a, "notes", "freetext", "value"); err != nil {
			return err
		}
		return requireMultiPick(d, a.Picked)

	case "single_select_notes":
		if err := requireEmptyAnswerFields(d, a, "freetext", "value"); err != nil {
			return err
		}
		if a.Notes == "" {
			return fmt.Errorf("decision %q: notes is required for single_select_notes: %w", d.Key, ErrInvalidInput)
		}
		return requireSinglePick(d, a.Picked)

	case "pick_or_freetext":
		if err := requireEmptyAnswerFields(d, a, "notes", "value"); err != nil {
			return err
		}
		hasPick := len(a.Picked) > 0
		hasFreetext := a.Freetext != ""
		if hasPick == hasFreetext {
			return fmt.Errorf("decision %q: exactly one of picked or freetext is required for pick_or_freetext: %w",
				d.Key, ErrInvalidInput)
		}
		if hasPick {
			return requireSinglePick(d, a.Picked)
		}
		return nil

	case "yes_no":
		if err := requireEmptyAnswerFields(d, a, "picked", "notes", "freetext"); err != nil {
			return err
		}
		if a.Value != "yes" && a.Value != "no" && a.Value != "unsure" {
			return fmt.Errorf("decision %q: value must be yes, no or unsure: %w", d.Key, ErrInvalidInput)
		}
		return nil

	case "freetext":
		if err := requireEmptyAnswerFields(d, a, "picked", "notes", "value"); err != nil {
			return err
		}
		if a.Freetext == "" {
			return fmt.Errorf("decision %q: freetext is required for freetext: %w", d.Key, ErrInvalidInput)
		}
		return nil

	default:
		return fmt.Errorf("decision %q: response_type %q is not one of %v: %w",
			d.Key, d.ResponseType, decisionResponseTypes, ErrInvalidInput)
	}
}

// requireEmptyAnswerFields refuses an answer that sets any of the named
// DecisionAnswer fields ("picked", "notes", "freetext", "value") — fields
// the calling response_type does not define.
func requireEmptyAnswerFields(d model.Decision, a model.DecisionAnswer, fields ...string) error {
	for _, f := range fields {
		var set bool
		switch f {
		case "picked":
			set = len(a.Picked) != 0
		case "notes":
			set = a.Notes != ""
		case "freetext":
			set = a.Freetext != ""
		case "value":
			set = a.Value != ""
		}
		if set {
			return fmt.Errorf("decision %q: %s is not used by %s: %w", d.Key, f, d.ResponseType, ErrInvalidInput)
		}
	}
	return nil
}

// requireSinglePick refuses an answer whose picked does not name exactly
// one of d's offered option labels.
func requireSinglePick(d model.Decision, picked []string) error {
	if len(picked) != 1 {
		return fmt.Errorf("decision %q: picked must name exactly one option: %w", d.Key, ErrInvalidInput)
	}
	if !hasOptionLabel(d.Options, picked[0]) {
		return fmt.Errorf("decision %q: picked %q is not an offered option: %w", d.Key, picked[0], ErrInvalidInput)
	}
	return nil
}

// requireMultiPick refuses an answer whose picked repeats a label, names
// one that isn't offered, or falls outside [min_picks, max_picks] (default
// 1 and len(options)).
func requireMultiPick(d model.Decision, picked []string) error {
	min := 1
	if d.MinPicks != nil {
		min = *d.MinPicks
	}
	max := len(d.Options)
	if d.MaxPicks != nil {
		max = *d.MaxPicks
	}
	seen := make(map[string]bool, len(picked))
	for _, p := range picked {
		if !hasOptionLabel(d.Options, p) {
			return fmt.Errorf("decision %q: picked %q is not an offered option: %w", d.Key, p, ErrInvalidInput)
		}
		if seen[p] {
			return fmt.Errorf("decision %q: picked %q is repeated: %w", d.Key, p, ErrInvalidInput)
		}
		seen[p] = true
	}
	if len(picked) < min || len(picked) > max {
		return fmt.Errorf("decision %q: picked must name between %d and %d options: %w", d.Key, min, max, ErrInvalidInput)
	}
	return nil
}

func hasOptionLabel(opts []model.DecisionOption, label string) bool {
	for _, o := range opts {
		if o.Label == label {
			return true
		}
	}
	return false
}

// decisionColumns is the read list every decision scan uses, in the order
// scanDecision expects.
const decisionColumns = `id, task_id, key, position, "group", question, context,
	response_type, options, min_picks, max_picks, answer, COALESCE(decided_by, ''), decided_at`

func scanDecision(row rowScanner) (model.Decision, error) {
	var d model.Decision
	var options, answer []byte
	if err := row.Scan(&d.ID, &d.Task, &d.Key, &d.Position, &d.Group, &d.Question, &d.Context,
		&d.ResponseType, &options, &d.MinPicks, &d.MaxPicks, &answer, &d.DecidedBy, &d.DecidedAt); err != nil {
		return model.Decision{}, err
	}
	if len(options) > 0 {
		if err := json.Unmarshal(options, &d.Options); err != nil {
			return model.Decision{}, fmt.Errorf("decode options of %s/%s: %w", d.Task, d.Key, err)
		}
	}
	if len(answer) > 0 {
		d.Answer = &model.DecisionAnswer{}
		if err := json.Unmarshal(answer, d.Answer); err != nil {
			return model.Decision{}, fmt.Errorf("decode answer of %s/%s: %w", d.Task, d.Key, err)
		}
	}
	if d.DecidedAt != nil {
		utc := d.DecidedAt.UTC()
		d.DecidedAt = &utc
	}
	return d, nil
}

// ListDecisions returns the questions posed on a task, in authored order.
func (s *Store) ListDecisions(ctx context.Context, taskID string) ([]model.Decision, error) {
	var out []model.Decision
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = listDecisions(tx, taskID)
		return err
	})
	return out, err
}

func listDecisions(tx *sql.Tx, taskID string) ([]model.Decision, error) {
	rows, err := tx.Query(
		`SELECT `+decisionColumns+` FROM decisions WHERE task_id = $1 ORDER BY position, id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list decisions of %s: %w", taskID, err)
	}
	return collectRows(rows, "list decisions of "+taskID, scanDecision)
}

// InsertDecision writes one posed question against taskID, at the end of
// that task's order. The spec is validated first, so an invalid row never
// reaches the database, and a key already used on the task comes back as
// ErrDecisionExists rather than a raw constraint error.
//
// Exported because the create path (POST /api/v1/tasks with a decisions
// list) runs it inside the transaction that inserts the task, so a task and
// the questions posed on it commit together.
func InsertDecision(tx *sql.Tx, taskID string, d model.Decision) (*model.Decision, error) {
	d.Task = taskID
	if err := ValidateDecisionSpec(d); err != nil {
		return nil, err
	}
	var options any
	if len(d.Options) > 0 {
		b, err := json.Marshal(d.Options)
		if err != nil {
			return nil, fmt.Errorf("encode options of %s/%s: %w", taskID, d.Key, err)
		}
		options = b
	}
	row := tx.QueryRow(
		`INSERT INTO decisions (task_id, key, position, "group", question, context,
		                        response_type, options, min_picks, max_picks)
		 VALUES ($1, $2,
		         COALESCE((SELECT max(position) FROM decisions WHERE task_id = $1), 0) + 1,
		         $3, $4, $5, $6, $7, $8, $9)
		 RETURNING `+decisionColumns,
		taskID, d.Key, d.Group, d.Question, d.Context, d.ResponseType, options, d.MinPicks, d.MaxPicks)
	out, err := scanDecision(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("task %s already poses %q: %w", taskID, d.Key, ErrDecisionExists)
		}
		return nil, fmt.Errorf("pose decision %s/%s: %w", taskID, d.Key, err)
	}
	return &out, nil
}

// AddDecision poses one question on an existing task (025 §10.1), recorded
// as a "decision.posed" cli event. Any kind of task but a rally may carry
// rows.
//
// Errors: ErrNotFound if the task does not exist or is soft-deleted, or if
// actorID does not name an actor; ErrInvalidInput for a spec violation;
// ErrDecisionExists if the key is already used on the task.
func (s *Store) AddDecision(ctx context.Context, taskID, actorID string, in model.DecisionInput) (*model.Decision, error) {
	extID, err := randomExternalID()
	if err != nil {
		return nil, err
	}
	payload, err := EventPayload(map[string]any{"task": taskID, "actor": actorID, "key": in.Key})
	if err != nil {
		return nil, err
	}

	var posed *model.Decision
	_, _, err = s.RecordEvent(ctx, "cli", extID, "decision.posed", payload,
		func(tx *sql.Tx, eventID int64) error {
			if err := requireLiveTask(tx, taskID); err != nil {
				return err
			}
			if err := requireActor(tx, actorID); err != nil {
				return err
			}
			posed, err = InsertDecision(tx, taskID, applyDecisionInput(model.Decision{}, in, true))
			return err
		})
	s.metrics.decision("pose", decisionOutcome(err))
	if err != nil {
		return nil, err
	}
	return posed, nil
}

// EditDecision rewords, regroups or re-parents one unanswered question,
// recorded as a "decision.edited" cli event. An answered row is immutable
// (025 §10.1) and comes back as ErrBadTransition. A re-parent (in.Task set)
// moves the row to the end of the target task's order.
//
// Errors: ErrNotFound for an unknown task, row or actor; ErrInvalidInput if
// the merged row would violate the spec, or if the re-parent target is
// closed; ErrBadTransition if the row is answered; ErrDecisionExists if the
// key is already used on the target task.
func (s *Store) EditDecision(ctx context.Context, taskID, key, actorID string, in model.DecisionInput) (*model.Decision, error) {
	extID, err := randomExternalID()
	if err != nil {
		return nil, err
	}
	payload, err := EventPayload(map[string]any{"task": taskID, "actor": actorID, "key": key})
	if err != nil {
		return nil, err
	}

	var edited *model.Decision
	_, _, err = s.RecordEvent(ctx, "cli", extID, "decision.edited", payload,
		func(tx *sql.Tx, eventID int64) error {
			if err := requireActor(tx, actorID); err != nil {
				return err
			}
			cur, err := lockDecision(tx, taskID, key)
			if err != nil {
				return err
			}
			if cur.Answer != nil || cur.DecidedAt != nil {
				return fmt.Errorf("decision %s/%s: an answered row is immutable; pose a new one: %w",
					taskID, key, ErrBadTransition)
			}
			target := taskID
			if in.Task != "" && in.Task != taskID {
				if err := requireOpenTask(tx, in.Task); err != nil {
					return err
				}
				target = in.Task
			}
			next := applyDecisionInput(cur, in, false)
			if err := ValidateDecisionSpec(specOnly(next)); err != nil {
				return err
			}
			edited, err = updateDecision(tx, cur.ID, target, next, target != taskID)
			return err
		})
	s.metrics.decision("edit", decisionOutcome(err))
	if err != nil {
		return nil, err
	}
	return edited, nil
}

// applyDecisionInput folds in over base. pose=true builds a fresh row from
// the input alone; pose=false is the edit merge described on
// model.DecisionInput: response_type, options and the two pick bounds move
// as one group, replaced only when the input names a response_type.
func applyDecisionInput(base model.Decision, in model.DecisionInput, pose bool) model.Decision {
	out := base
	if in.Key != "" {
		out.Key = in.Key
	}
	if in.Question != "" {
		out.Question = in.Question
	}
	if in.Group != nil {
		out.Group = *in.Group
	}
	if in.Context != nil {
		out.Context = *in.Context
	}
	if pose || in.ResponseType != "" {
		out.ResponseType = in.ResponseType
		out.Options = in.Options
		out.MinPicks = in.MinPicks
		out.MaxPicks = in.MaxPicks
	}
	return out
}

// specOnly strips the fields ValidateDecisionSpec requires to be absent at
// pose time, so an edit is checked against the same rules without its own
// stored id and position tripping them.
func specOnly(d model.Decision) model.Decision {
	d.Answer = nil
	d.DecidedAt = nil
	return d
}

// lockDecision reads one row FOR UPDATE, so the answered check and the
// update that follows it cannot straddle a concurrent answer.
func lockDecision(tx *sql.Tx, taskID, key string) (model.Decision, error) {
	row := tx.QueryRow(
		`SELECT `+decisionColumns+` FROM decisions WHERE task_id = $1 AND key = $2 FOR UPDATE`,
		taskID, key)
	d, err := scanDecision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return d, fmt.Errorf("decision %s/%s: %w", taskID, key, ErrNotFound)
	}
	if err != nil {
		return d, fmt.Errorf("read decision %s/%s: %w", taskID, key, err)
	}
	return d, nil
}

// updateDecision writes the merged row back. reposition is set when the row
// moved to another task, where its old position means nothing.
func updateDecision(tx *sql.Tx, id int64, taskID string, d model.Decision, reposition bool) (*model.Decision, error) {
	var options any
	if len(d.Options) > 0 {
		b, err := json.Marshal(d.Options)
		if err != nil {
			return nil, fmt.Errorf("encode options of %s/%s: %w", taskID, d.Key, err)
		}
		options = b
	}
	position := "position"
	if reposition {
		position = `COALESCE((SELECT max(position) FROM decisions WHERE task_id = $1), 0) + 1`
	}
	row := tx.QueryRow(
		`UPDATE decisions SET task_id = $1, key = $2, "group" = $3, question = $4, context = $5,
		        response_type = $6, options = $7, min_picks = $8, max_picks = $9,
		        position = `+position+`
		  WHERE id = $10
		 RETURNING `+decisionColumns,
		taskID, d.Key, d.Group, d.Question, d.Context, d.ResponseType, options, d.MinPicks, d.MaxPicks, id)
	out, err := scanDecision(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("task %s already poses %q: %w", taskID, d.Key, ErrDecisionExists)
		}
		return nil, fmt.Errorf("edit decision %s/%s: %w", taskID, d.Key, err)
	}
	return &out, nil
}

// requireLiveTask refuses an unknown or soft-deleted task, the same
// tombstone rule Claim uses (044 §4).
func requireLiveTask(tx *sql.Tx, taskID string) error {
	var kind string
	err := tx.QueryRow(`SELECT kind FROM tasks WHERE id = $1 AND deleted_at IS NULL`, taskID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("check task %s: %w", taskID, err)
	}
	return rejectRallyDecision(taskID, kind)
}

// rejectRallyDecision refuses a decision row on a rally. A rally carries no
// content of its own — its 'blocks' edges are the whole of it — so a question
// posed there is a question nobody reads. Pose it on the member it is about.
func rejectRallyDecision(taskID, kind string) error {
	if kind == "rally" {
		return fmt.Errorf("task %s is a rally and cannot carry decisions: %w", taskID, ErrInvalidInput)
	}
	return nil
}

// requireOpenTask additionally refuses a task in a terminal state: a
// question moved onto a closed task would never be answered. The rally rule
// applies to a re-parent the same way it does to a pose.
func requireOpenTask(tx *sql.Tx, taskID string) error {
	var state, kind string
	err := tx.QueryRow(
		`SELECT state, kind FROM tasks WHERE id = $1 AND deleted_at IS NULL`, taskID).Scan(&state, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("check task %s: %w", taskID, err)
	}
	if deliveredStateSet[state] {
		return fmt.Errorf("task %s is %s: cannot pose a decision on it: %w", taskID, state, ErrInvalidInput)
	}
	return rejectRallyDecision(taskID, kind)
}
