package otlp

import (
	"sync"

	"golang.org/x/time/rate"
)

// The per-actor budget for POST /otlp/v1/logs (WL-863): 120 batches a
// minute, bursting 30. The only other bound on task_activity growth is the
// seven-day purge (spec 071 §2), so a looping exporter would otherwise grow
// the table and burn decode CPU unchecked. A Claude Code session exports on
// a 5s schedule, so the sustained rate carries roughly ten concurrent
// sessions per actor and the burst absorbs a reconnect.
//
// maxKeys bounds the bucket map itself, which is the same unbounded-growth
// problem one level down.
const (
	limitPerMinute = 120
	limitBurst     = 30
	maxKeys        = 4096
)

// ingestRate is limitPerMinute expressed per second, what rate.Limiter takes.
var ingestRate = rate.Limit(limitPerMinute) / 60

// Limiter is a token bucket per actor over the OTLP ingest route. A nil
// *Limiter allows everything, like the other types in this package.
//
// ponytail: per-process, so N replicas allow N times the budget and a
// restart refills every bucket. That is still a bound; move the count into
// Postgres only if one pod's budget stops being the limit that matters.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*rate.Limiter
}

// NewLimiter builds an empty Limiter.
func NewLimiter() *Limiter {
	return &Limiter{buckets: make(map[string]*rate.Limiter)}
}

// Allow reports whether key may send one more batch now.
func (l *Limiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxKeys {
			l.evict()
		}
		b = rate.NewLimiter(ingestRate, limitBurst)
		l.buckets[key] = b
	}
	l.mu.Unlock()
	return b.Allow()
}

// evict drops every bucket that has refilled to full, which is every key
// idle for at least limitBurst/ingestRate seconds. The caller holds the lock.
//
// ponytail: an O(maxKeys) sweep, and it clears the map outright in the case
// where nothing is idle — which hands every current caller a fresh burst.
// Both are cheap and rare at 4096 keys; an LRU is the upgrade if a real
// deployment ever sits at the cap.
func (l *Limiter) evict() {
	for k, b := range l.buckets {
		if b.Tokens() >= float64(limitBurst) {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) >= maxKeys {
		clear(l.buckets)
	}
}
