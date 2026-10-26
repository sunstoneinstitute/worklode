package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestRecordClassification: the row is inserted and the event carries it as
// payload (WL-REQ-1853).
func TestRecordClassification(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()
	c := model.RefClassification{
		FromKind: model.NodeKind("task"), FromID: "P1-1", Field: "body", Line: 3,
		LineText: "blocked by WL-5", ToKind: model.NodeKind("task"), ToID: "P1-5",
		Classifier: "cue", ClassifierVersion: 1, Predicate: "blockedBy", Confidence: 0.9,
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	var id, eventID int64
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		eventID = seedEvent(t, tx)
		var err error
		id, err = recordClassification(tx, c, eventID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var outcome, predicate string
	if err := s.db.QueryRowContext(ctx,
		`SELECT outcome, predicate FROM ref_classifications WHERE id = $1`, id).Scan(&outcome, &predicate); err != nil {
		t.Fatal(err)
	}
	if outcome != "kept" || predicate != "blockedBy" {
		t.Errorf("row = %q %q", outcome, predicate)
	}
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT payload FROM events WHERE id = $1`, eventID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p["@type"] != "wl:ReferenceClassified" || p["id"] != float64(id) || p["predicate"] != "blockedBy" {
		t.Errorf("payload = %s", raw)
	}
}
