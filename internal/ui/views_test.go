package ui

import (
	"testing"
	"time"
)

func TestHomeActivity(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"zero time", time.Time{}, "No activity yet"},
		{"fixed timestamp", time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC), "Last activity 2026-08-14 09:30"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := homeActivity(c.in); got != c.want {
				t.Errorf("homeActivity(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
