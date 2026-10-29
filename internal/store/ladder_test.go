package store

import (
	"context"
	"testing"
)

func TestRecordGapKeysOnReason(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	gap := GapInput{TaskID: "WL-1", Doc: "WL-PLAN-1", Anchor: "sec-2", Reason: "the empty case"}
	for i, want := range []bool{true, false} {
		got, err := s.RecordGap(ctx, gap)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("call %d with the same reason: inserted = %v, want %v", i+1, got, want)
		}
	}
	gap.Reason = "the schema misdescribes tables"
	if got, err := s.RecordGap(ctx, gap); err != nil || !got {
		t.Fatalf("a second reason on the same section: inserted = %v, err = %v, want a new event", got, err)
	}
}
