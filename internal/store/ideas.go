// ideas.go implements WL-REQ-1799: an idea is private to its author until the
// author shares it, which mints its task in the intake project. Every query
// on the table applies visibleTo with the caller's actor id as $1, so a row
// the caller may not see is ErrNotFound everywhere. Events carry the idea id,
// the actor and (once shared) the task, never the title or body.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// visibleTo is the one visibility predicate (WL-REQ-1799). $1 is the caller's
// actor id, which may be "" and then matches shared ideas only.
const visibleTo = `(i.author = $1 OR i.shared_at IS NOT NULL) AND i.deleted_at IS NULL`

const (
	ideaColumns = `i.id, i.title, i.body, i.author, i.type, i.story_type, i.litmus_test_id,
		COALESCE(to_char(i.shared_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), ''), COALESCE(i.task, ''),
		to_char(i.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), i.pre_research_authorized, i.decision`

	ideaListQuery = `SELECT ` + ideaColumns + ` FROM ideas i WHERE ` + visibleTo +
		` ORDER BY i.created_at DESC, i.id`
	ideaGetQuery = `SELECT ` + ideaColumns + ` FROM ideas i WHERE i.id = $2 AND ` + visibleTo
	// ideaLockAuthoredQuery is the opening read of every author-only act.
	ideaLockAuthoredQuery = `SELECT ` + ideaColumns + ` FROM ideas i
		WHERE i.id = $2 AND i.author = $1 AND ` + visibleTo + ` FOR UPDATE`
	ideaLockQuery   = `SELECT ` + ideaColumns + ` FROM ideas i WHERE i.id = $2 AND ` + visibleTo + ` FOR UPDATE`
	ideaSharedQuery = `SELECT i.shared_at IS NOT NULL FROM ideas i WHERE i.id = $2 AND ` + visibleTo

	ideaUpdateQuery = `UPDATE ideas i SET title = $3, body = $4, type = $5, story_type = $6,
		litmus_test_id = $7, updated_at = $8 WHERE i.id = $2 AND i.author = $1 AND ` + visibleTo
	ideaShareQuery = `UPDATE ideas i SET task = $3, shared_at = $4, updated_at = $4
		WHERE i.id = $2 AND i.author = $1 AND i.shared_at IS NULL AND ` + visibleTo
	ideaDeletePrivateQuery = `DELETE FROM ideas i
		WHERE i.id = $2 AND i.author = $1 AND i.shared_at IS NULL AND ` + visibleTo
	ideaTombstoneQuery = `UPDATE ideas i SET deleted_at = $3, updated_at = $3
		WHERE i.id = $2 AND i.shared_at IS NOT NULL AND ` + visibleTo
	ideaIntakeQuery = `UPDATE ideas i SET pre_research_authorized = $3, decision = $4, updated_at = $5
		WHERE i.id = $2 AND i.shared_at IS NOT NULL AND ` + visibleTo

	// The insert is the one statement without visibleTo: it writes a row
	// whose author is the caller, which the predicate admits by definition.
	ideaInsert = `INSERT INTO ideas (id, author, title, body, type, story_type, litmus_test_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`

	ideaEventSource = "cli"
)

var (
	ideaTypes      = []string{"general", "story-pitch"}
	ideaStoryTypes = []string{"data-story", "short-story", ""}
	ideaDecisions  = []string{"accept", "narrow", "park", "stop", ""}
)

// errAlreadyShared rolls back ShareIdea's event when there is nothing to do.
var errAlreadyShared = errors.New("idea already shared")

func scanIdea(row rowScanner) (*model.Idea, error) {
	var i model.Idea
	if err := row.Scan(&i.ID, &i.Title, &i.Body, &i.Author, &i.Type, &i.StoryType, &i.LitmusTestID,
		&i.Shared, &i.Task, &i.Submitted, &i.Intake.PreResearchAuthorized, &i.Intake.Decision); err != nil {
		return nil, err
	}
	i.Status = "private"
	if i.Task != "" {
		i.Status = "intake"
	}
	return &i, nil
}

// normalizeIdea trims the title, checks the enums, and blanks the story
// fields of an idea that is not a story pitch.
func normalizeIdea(i *model.Idea) error {
	i.Title = strings.TrimSpace(i.Title)
	if i.Title == "" {
		return &FieldError{Field: "title", Msg: "Give your idea a title."}
	}
	if err := validIdeaEnum("type", i.Type, ideaTypes); err != nil {
		return err
	}
	if i.Type != "story-pitch" {
		i.StoryType, i.LitmusTestID = "", ""
	}
	return validIdeaEnum("story_type", i.StoryType, ideaStoryTypes)
}

// validIdeaEnum is a FieldError when v is not one of allowed.
func validIdeaEnum(field, v string, allowed []string) error {
	if slices.Contains(allowed, v) {
		return nil
	}
	return &FieldError{Field: field, Msg: fmt.Sprintf("%q must be one of %s", v, strings.Join(allowed, ", "))}
}

// ListIdeas returns the caller's private ideas and every shared idea, newest first.
func (s *Store) ListIdeas(ctx context.Context, caller string) ([]model.Idea, error) {
	rows, err := s.db.QueryContext(ctx, ideaListQuery, caller)
	if err != nil {
		return nil, fmt.Errorf("list ideas: %w", err)
	}
	out, err := collectRows(rows, "list ideas", byValue(scanIdea))
	return nonNil(out), err
}

// GetIdea returns one idea, ErrNotFound when it is absent or not visible.
func (s *Store) GetIdea(ctx context.Context, caller, id string) (*model.Idea, error) {
	return getIdea(ctx, s.db, caller, id)
}

func getIdea(ctx context.Context, q queryer, caller, id string) (*model.Idea, error) {
	i, err := scanIdea(q.QueryRowContext(ctx, ideaGetQuery, caller, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("idea %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get idea %s: %w", id, err)
	}
	return i, nil
}

// lockAuthored reads the caller's own idea under a row lock. A visible idea
// the caller did not write is ErrForbidden; anything else missing is
// ErrNotFound.
func lockAuthored(ctx context.Context, tx *sql.Tx, caller, id string) (*model.Idea, error) {
	i, err := scanIdea(tx.QueryRowContext(ctx, ideaLockAuthoredQuery, caller, id))
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := getIdea(ctx, tx, caller, id); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("idea %s belongs to its author: %w", id, ErrForbidden)
	}
	if err != nil {
		return nil, fmt.Errorf("lock idea %s: %w", id, err)
	}
	return i, nil
}

// requireShared is ErrNotFound when the caller cannot see the idea and
// ErrNotShared when it is visible but private. Intake-side writes call it
// first (WL-REQ-1799).
func requireShared(ctx context.Context, q queryer, caller, id string) error {
	var shared bool
	err := q.QueryRowContext(ctx, ideaSharedQuery, caller, id).Scan(&shared)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("idea %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("check idea %s shared: %w", id, err)
	}
	if !shared {
		return fmt.Errorf("idea %s: %w", id, ErrNotShared)
	}
	return nil
}

// recordIdeaEvent runs apply inside one event of type typ whose payload is
// {idea, actor} (WL-REQ-1799). apply adds "task" itself when there is one.
func (s *Store) recordIdeaEvent(ctx context.Context, typ, caller, id string, apply func(tx *sql.Tx, now time.Time, eventID int64) error) error {
	return s.recordIdeaEventWith(ctx, typ, caller, id, nil, apply)
}

// recordIdeaEventWith is recordIdeaEvent with extra payload members.
func (s *Store) recordIdeaEventWith(ctx context.Context, typ, caller, id string, extra map[string]string, apply func(tx *sql.Tx, now time.Time, eventID int64) error) error {
	ext, err := randomExternalID()
	if err != nil {
		return err
	}
	members := map[string]string{"idea": id, "actor": caller}
	for k, v := range extra {
		members[k] = v
	}
	payload, err := json.Marshal(members)
	if err != nil {
		return err
	}
	now := s.Now().UTC().Truncate(time.Second)
	_, _, err = s.RecordEvent(ctx, ideaEventSource, ext, typ, payload,
		func(tx *sql.Tx, eventID int64) error { return apply(tx, now, eventID) })
	return err
}

// CreateIdea writes a private idea authored by caller and records idea.created.
func (s *Store) CreateIdea(ctx context.Context, caller string, in model.CreateIdeaInput) (*model.Idea, error) {
	if caller == "" {
		return nil, fmt.Errorf("idea needs an author: %w", ErrInvalidInput)
	}
	i := &model.Idea{Title: in.Title, Body: in.Body, Type: in.Type, StoryType: in.StoryType, LitmusTestID: in.LitmusTestID}
	if err := normalizeIdea(i); err != nil {
		return nil, err
	}
	suffix, err := randomExternalID()
	if err != nil {
		return nil, err
	}
	id := "idea_" + suffix[:12]
	if err := s.recordIdeaEvent(ctx, "idea.created", caller, id, func(tx *sql.Tx, now time.Time, _ int64) error {
		if _, err := tx.ExecContext(ctx, ideaInsert,
			id, caller, i.Title, i.Body, i.Type, i.StoryType, i.LitmusTestID, now); err != nil {
			return fmt.Errorf("insert idea %s: %w", id, err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return s.GetIdea(ctx, caller, id)
}

// PatchIdea applies the non-nil fields of in to the caller's own idea and
// records idea.updated. On a shared idea the intake task takes the new title
// and body in the same transaction.
func (s *Store) PatchIdea(ctx context.Context, caller, id string, in model.PatchIdeaInput) (*model.Idea, error) {
	if err := s.recordIdeaEvent(ctx, "idea.updated", caller, id, func(tx *sql.Tx, now time.Time, eventID int64) error {
		i, err := lockAuthored(ctx, tx, caller, id)
		if err != nil {
			return err
		}
		for _, f := range []struct{ dst, src *string }{
			{&i.Title, in.Title}, {&i.Body, in.Body}, {&i.Type, in.Type},
			{&i.StoryType, in.StoryType}, {&i.LitmusTestID, in.LitmusTestID},
		} {
			if f.src != nil {
				*f.dst = *f.src
			}
		}
		if err := normalizeIdea(i); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, ideaUpdateQuery, caller, id,
			i.Title, i.Body, i.Type, i.StoryType, i.LitmusTestID, now)
		if err != nil {
			return fmt.Errorf("update idea %s: %w", id, err)
		}
		if err := requireOneAffected(res, "update idea "+id, fmt.Errorf("idea %s: %w", id, ErrNotFound)); err != nil {
			return err
		}
		if i.Task == "" {
			return nil
		}
		if err := UpdateTaskFields(tx, now, i.Task, &i.Title, &i.Body,
			nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
			return err
		}
		return AttributeEventToTask(tx, eventID, i.Task)
	}); err != nil {
		return nil, err
	}
	return s.GetIdea(ctx, caller, id)
}

// ShareIdea is the author's one-way share act: it mints a ready design task
// in intakeProject from the idea's title and body, links it, and records
// idea.shared. An idea already shared comes back unchanged with no event.
func (s *Store) ShareIdea(ctx context.Context, caller, id, intakeProject string) (*model.Idea, error) {
	err := s.recordIdeaEvent(ctx, "idea.shared", caller, id, func(tx *sql.Tx, now time.Time, eventID int64) error {
		i, err := lockAuthored(ctx, tx, caller, id)
		if err != nil {
			return err
		}
		if i.Task != "" {
			return errAlreadyShared
		}
		task, err := CreateTask(tx, now, TaskInput{
			ProjectID: intakeProject, Title: i.Title, Body: i.Body,
			Kind: "design", Priority: "medium", CreatedBy: i.Author,
		}, eventID)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, ideaShareQuery, caller, id, task.ID, now)
		if err != nil {
			return fmt.Errorf("share idea %s: %w", id, err)
		}
		if err := requireOneAffected(res, "share idea "+id, fmt.Errorf("idea %s: %w", id, ErrNotFound)); err != nil {
			return err
		}
		return AttributeEventToTask(tx, eventID, task.ID)
	})
	if err != nil && !errors.Is(err, errAlreadyShared) {
		return nil, err
	}
	return s.GetIdea(ctx, caller, id)
}

// DeleteIdea removes a private idea outright (its author only) or, once
// shared, tombstones the idea and its intake task (any caller who can see
// it), and records idea.deleted.
func (s *Store) DeleteIdea(ctx context.Context, caller, id, justification string) error {
	return s.recordIdeaEvent(ctx, "idea.deleted", caller, id, func(tx *sql.Tx, now time.Time, eventID int64) error {
		i, err := scanIdea(tx.QueryRowContext(ctx, ideaLockQuery, caller, id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("idea %s: %w", id, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("lock idea %s: %w", id, err)
		}
		if i.Task == "" {
			res, err := tx.ExecContext(ctx, ideaDeletePrivateQuery, caller, id)
			if err != nil {
				return fmt.Errorf("delete idea %s: %w", id, err)
			}
			return requireOneAffected(res, "delete idea "+id,
				fmt.Errorf("idea %s belongs to its author: %w", id, ErrForbidden))
		}
		if _, err := tx.ExecContext(ctx, ideaTombstoneQuery, caller, id, now); err != nil {
			return fmt.Errorf("tombstone idea %s: %w", id, err)
		}
		// The task may already be tombstoned on its own; the idea still goes.
		var taskDeleted bool
		if err := tx.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL FROM tasks WHERE id = $1`, i.Task).
			Scan(&taskDeleted); err != nil {
			return fmt.Errorf("read task %s: %w", i.Task, err)
		}
		if !taskDeleted {
			if err := DeleteTask(tx, now, i.Task, caller, justification, eventID); err != nil {
				return err
			}
		}
		return AttributeEventToTask(tx, eventID, i.Task)
	})
}

// SetIdeaIntake replaces a shared idea's intake fields and records
// idea.intake_updated. A private idea is ErrNotShared to its author.
func (s *Store) SetIdeaIntake(ctx context.Context, caller, id string, in model.PatchIdeaIntakeInput) (*model.Idea, error) {
	if err := validIdeaEnum("decision", in.Decision, ideaDecisions); err != nil {
		return nil, err
	}
	if err := s.recordIdeaEvent(ctx, "idea.intake_updated", caller, id, func(tx *sql.Tx, now time.Time, _ int64) error {
		if err := requireShared(ctx, tx, caller, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, ideaIntakeQuery, caller, id, in.PreResearchAuthorized, in.Decision, now)
		if err != nil {
			return fmt.Errorf("set intake on idea %s: %w", id, err)
		}
		return requireOneAffected(res, "set intake on idea "+id, fmt.Errorf("idea %s: %w", id, ErrNotFound))
	}); err != nil {
		return nil, err
	}
	return s.GetIdea(ctx, caller, id)
}
