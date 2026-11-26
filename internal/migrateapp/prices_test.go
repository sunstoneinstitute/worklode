package migrateapp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

const feedJSON = `{
 "sample_spec": {"input_cost_per_token": 0},
 "claude-new-9": {"litellm_provider": "anthropic", "mode": "chat",
   "input_cost_per_token": 4e-06, "output_cost_per_token": 2e-05,
   "cache_creation_input_token_cost": 5e-06, "cache_creation_input_token_cost_above_1hr": 8e-06,
   "cache_read_input_token_cost": 2e-07, "provider_specific_entry": {"fast": 2}},
 "gpt-9": {"litellm_provider": "openai", "mode": "responses",
   "input_cost_per_token": 1.25e-06, "output_cost_per_token": 1e-05, "cache_read_input_token_cost": 1.25e-07},
 "bedrock/claude-new-9": {"litellm_provider": "bedrock", "mode": "chat",
   "input_cost_per_token": 1e-06, "output_cost_per_token": 1e-06},
 "text-embedding-9": {"litellm_provider": "openai", "mode": "embedding",
   "input_cost_per_token": 1e-08, "output_cost_per_token": 0},
 "claude-opus-5": {"litellm_provider": "anthropic", "mode": "chat",
   "input_cost_per_token": 9e-06, "output_cost_per_token": 2.5e-05}
}`

func TestParseLiteLLM(t *testing.T) {
	got, err := parseLiteLLM(strings.NewReader(feedJSON))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][5]int64{
		"claude-new-9/standard":  {4e6, 5e6, 8e6, 2e5, 2e7},
		"claude-new-9/fast":      {8e6, 10e6, 16e6, 4e5, 4e7},
		"gpt-9/standard":         {1.25e6, 1.25e6, 1.25e6, 1.25e5, 1e7}, // no cache-write surcharge
		"claude-opus-5/standard": {9e6, 9e6, 9e6, 9e6, 2.5e7},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for _, p := range got {
		key := p.Model + "/" + p.Speed
		r := [5]int64{p.InputMicros, p.CacheWrite5mMicros, p.CacheWrite1hMicros, p.CacheReadMicros, p.OutputMicros}
		if r != want[key] {
			t.Errorf("%s = %v, want %v", key, r, want[key])
		}
	}
}

func TestSyncPricesAddsOnlyMissing(t *testing.T) {
	s := store.OpenTestStore(t)
	ctx := context.Background()
	feed, err := parseLiteLLM(strings.NewReader(feedJSON))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := syncPrices(ctx, s, feed, time.Now(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "prices synced: 3 added, 1 changed") {
		t.Errorf("output = %q", out.String())
	}
	// The seeded claude-opus-5 rate disagrees with the feed: it changes from
	// today, and yesterday keeps the old rate.
	p, err := s.ModelPriceFor(ctx, "claude-opus-5", store.SpeedStandard, time.Now())
	if err != nil || p == nil || p.InputMicros != 9_000_000 {
		t.Fatalf("today's rate: %+v, %v", p, err)
	}
	p, err = s.ModelPriceFor(ctx, "claude-opus-5", store.SpeedStandard, time.Now().AddDate(0, 0, -1))
	if err != nil || p == nil || p.InputMicros != 5_000_000 {
		t.Fatalf("yesterday's rate: %+v, %v", p, err)
	}
	p, err = s.ModelPriceFor(ctx, "claude-new-9", store.SpeedFast, time.Now())
	if err != nil || p == nil || p.OutputMicros != 40_000_000 {
		t.Fatalf("new fast rate: %+v, %v", p, err)
	}
	out.Reset()
	if err := syncPrices(ctx, s, feed, time.Now(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "prices synced: 0 added, 0 changed") {
		t.Errorf("second run output = %q", out.String())
	}
}
