package mdrender

import (
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestEvictsToStayUnderEntryBound: caching untrusted input is only safe if
// the cache is not itself an unbounded sink. These use the unexported
// constructor because the bounds are the invariant under test, not a knob
// callers should be able to move.
func TestEvictsToStayUnderEntryBound(t *testing.T) {
	m := NewMetrics(prometheus.NewRegistry())
	c := newCache(maxCacheBytes, 4, m)
	for i := 0; i < 20; i++ {
		c.Body(fmt.Sprintf("body %d\n", i))
	}
	if got := c.lru.Len(); got != 4 {
		t.Fatalf("cache holds %d entries, want 4", got)
	}
	if got := len(c.index); got != 4 {
		t.Fatalf("index holds %d entries, want 4 — it is leaking evicted keys", got)
	}
	if got := testutil.ToFloat64(m.Evictions()); got != 16 {
		t.Fatalf("recorded %v evictions, want 16", got)
	}
}

func TestEvictsToStayUnderByteBound(t *testing.T) {
	m := NewMetrics(prometheus.NewRegistry())
	// Each body renders to a <p> a little over 1 KiB, so 8 KiB holds a
	// handful and the rest must be evicted.
	c := newCache(8<<10, maxCacheEntries, m)
	for i := 0; i < 64; i++ {
		c.Body(fmt.Sprintf("%04d", i) + strings.Repeat("x", 1<<10))
	}
	if c.bytes > 8<<10 {
		t.Fatalf("cache holds %d bytes, over the 8192 bound", c.bytes)
	}
	if got := testutil.ToFloat64(m.Bytes()); got != float64(c.bytes) {
		t.Fatalf("gauge reports %v bytes, cache holds %d", got, c.bytes)
	}
	if c.lru.Len() != len(c.index) {
		t.Fatalf("list holds %d entries, index %d", c.lru.Len(), len(c.index))
	}
}

// TestEvictsLeastRecentlyUsed: a body being read right now must outlive one
// nobody has looked at, or a busy task page evicts itself.
func TestEvictsLeastRecentlyUsed(t *testing.T) {
	c := newCache(maxCacheBytes, 2, nil)
	c.Body("a")
	c.Body("b")
	c.Body("a") // a is now the most recently used
	c.Body("c") // evicts b

	if _, ok := c.get(keyOf("a")); !ok {
		t.Fatal("a was evicted despite being the most recently used")
	}
	if _, ok := c.get(keyOf("c")); !ok {
		t.Fatal("c was evicted immediately after insertion")
	}
	if _, ok := c.get(keyOf("b")); ok {
		t.Fatal("b survived; eviction is not least-recently-used")
	}
}

// TestNilMetricsIsSafe: newCache(_, _, nil) is what the LRU tests above use,
// and (*Cache).Body must not panic without a registry behind it.
func TestNilMetricsIsSafe(t *testing.T) {
	c := newCache(maxCacheBytes, maxCacheEntries, nil)
	c.Body("# hi\n")
	c.Body("# hi\n")
	c.Body(strings.Repeat("x", (64<<10)+1))
}
