package serverapp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/indexer"
)

func TestRunRequiresDSN(t *testing.T) {
	err := Run(context.Background(), Options{})
	if err == nil || !strings.Contains(err.Error(), "no DSN") {
		t.Fatalf("Run without a DSN = %v, want no DSN error", err)
	}
}

// TestIndexIntervalFromEnv: the convergence interval is an operator knob, so
// a typo has to fail the boot rather than run five-minute passes on an
// instance configured for ten seconds.
func TestIndexIntervalFromEnv(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want time.Duration
		bad  bool
	}{
		{env: "", want: indexer.DefaultInterval},
		{env: "30s", want: 30 * time.Second},
		{env: "2h", want: 2 * time.Hour},
		{env: "5", bad: true},    // no unit
		{env: "soon", bad: true}, // not a duration
		{env: "0s", bad: true},   // a zero interval is a busy loop
		{env: "-1m", bad: true},  // as is a negative one
	} {
		t.Setenv("LODE_INDEX_INTERVAL", tc.env)
		got, err := indexIntervalFromEnv()
		if tc.bad {
			if err == nil {
				t.Errorf("LODE_INDEX_INTERVAL=%q: want an error, got %v", tc.env, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("LODE_INDEX_INTERVAL=%q = %v err=%v, want %v", tc.env, got, err, tc.want)
		}
	}
}
