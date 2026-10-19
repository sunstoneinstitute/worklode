// ideafindings.go implements WL-REQ-1800: litmus-test results and
// pre-research runs recorded on a shared idea as idea_findings rows. Every
// call starts with requireShared, so a private idea is ErrNotShared to its
// author and ErrNotFound to anyone else. Events carry the kind, never the
// findings' text.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

const (
	findingKindLitmus      = "litmus"
	findingKindPreResearch = "pre_research"

	findingColumns = `id, to_char(date, 'YYYY-MM-DD'), summary, items`
	findingList    = `SELECT ` + findingColumns + ` FROM idea_findings
		WHERE idea = $1 AND kind = $2 ORDER BY date DESC, id DESC`
	findingLatest = findingList + ` LIMIT 1`
	findingInsert = `INSERT INTO idea_findings (idea, kind, date, summary, items, created_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING ` + findingColumns
)

// scanFinding reads one findingColumns row. A row of either kind scans as
// a LitmusTest; items lands in Recommendations.
func scanFinding(row rowScanner) (*model.LitmusTest, error) {
	var (
		lt  model.LitmusTest
		id  int64
		raw []byte
	)
	if err := row.Scan(&id, &lt.Date, &lt.Summary, &raw); err != nil {
		return nil, err
	}
	lt.ID = strconv.FormatInt(id, 10)
	if err := json.Unmarshal(raw, &lt.Recommendations); err != nil {
		return nil, fmt.Errorf("decode finding %d items: %w", id, err)
	}
	lt.Recommendations = nonNil(lt.Recommendations)
	return &lt, nil
}

// validFindingDate is a FieldError unless d is YYYY-MM-DD.
func validFindingDate(d string) error {
	if _, err := time.Parse(time.DateOnly, d); err != nil {
		return &FieldError{Field: "date", Msg: "Give the date as YYYY-MM-DD."}
	}
	return nil
}

// ListLitmusTests returns a shared idea's litmus tests, newest date first.
func (s *Store) ListLitmusTests(ctx context.Context, caller, ideaID string) ([]model.LitmusTest, error) {
	if err := requireShared(ctx, s.db, caller, ideaID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, findingList, ideaID, findingKindLitmus)
	if err != nil {
		return nil, fmt.Errorf("list litmus tests of %s: %w", ideaID, err)
	}
	out, err := collectRows(rows, "list litmus tests", byValue(scanFinding))
	return nonNil(out), err
}

// AddLitmusTest records one litmus test on a shared idea and records
// idea.finding_recorded.
func (s *Store) AddLitmusTest(ctx context.Context, caller, ideaID string, in model.CreateLitmusTestInput) (*model.LitmusTest, error) {
	return s.addFinding(ctx, caller, ideaID, findingKindLitmus, in.Date, in.Summary, in.Recommendations)
}

// LatestPreResearch returns the findings of a shared idea's newest
// pre-research run, empty when there is none.
func (s *Store) LatestPreResearch(ctx context.Context, caller, ideaID string) (*model.PreResearch, error) {
	if err := requireShared(ctx, s.db, caller, ideaID); err != nil {
		return nil, err
	}
	f, err := scanFinding(s.db.QueryRowContext(ctx, findingLatest, ideaID, findingKindPreResearch))
	if errors.Is(err, sql.ErrNoRows) {
		return &model.PreResearch{Findings: []string{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pre-research of %s: %w", ideaID, err)
	}
	return &model.PreResearch{Findings: f.Recommendations}, nil
}

// PutPreResearch records a new pre-research run on a shared idea, records
// idea.finding_recorded, and returns the newest run's findings.
func (s *Store) PutPreResearch(ctx context.Context, caller, ideaID string, in model.PutPreResearchInput) (*model.PreResearch, error) {
	if _, err := s.addFinding(ctx, caller, ideaID, findingKindPreResearch, in.Date, "", in.Findings); err != nil {
		return nil, err
	}
	return s.LatestPreResearch(ctx, caller, ideaID)
}

// addFinding inserts one idea_findings row inside its event. The open
// subject (caller "") reads findings but cannot record one.
func (s *Store) addFinding(ctx context.Context, caller, ideaID, kind, date, summary string, items []string) (*model.LitmusTest, error) {
	if err := validFindingDate(date); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(nonNil(items))
	if err != nil {
		return nil, err
	}
	var out *model.LitmusTest
	err = s.recordIdeaEventWith(ctx, "idea.finding_recorded", caller, ideaID, map[string]string{"kind": kind},
		func(tx *sql.Tx, now time.Time, _ int64) error {
			if err := requireShared(ctx, tx, caller, ideaID); err != nil {
				return err
			}
			if caller == "" {
				return fmt.Errorf("finding needs an author: %w", ErrInvalidInput)
			}
			f, err := scanFinding(tx.QueryRowContext(ctx, findingInsert,
				ideaID, kind, date, summary, raw, now, caller))
			if err != nil {
				return fmt.Errorf("insert %s finding on %s: %w", kind, ideaID, err)
			}
			out = f
			return nil
		})
	return out, err
}
