package mdrender_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/mdrender"
)

func newTestCache(t *testing.T) (*mdrender.Cache, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	return mdrender.NewCache(reg), reg
}

// lookups and renders read one series of the cache's counters. Both carry a
// "kind" label now that the cache serves task and document bodies, so both
// halves have to be matched: matching on the result alone would read whichever
// kind's series the registry happened to yield first.
func lookups(t *testing.T, reg *prometheus.Registry, kind, result string) float64 {
	t.Helper()
	return counter(t, reg, "worklode_mdrender_cache_lookups_total", map[string]string{"kind": kind, "result": result})
}

func renders(t *testing.T, reg *prometheus.Registry, kind, outcome string) float64 {
	t.Helper()
	return counter(t, reg, "worklode_mdrender_renders_total", map[string]string{"kind": kind, "outcome": outcome})
}

func counter(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			match := 0
			for _, l := range m.GetLabel() {
				if labels[l.GetName()] == l.GetValue() {
					match++
				}
			}
			if match == len(labels) {
				return m.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("no %s%v in registry", name, labels)
	return 0
}

// TestCacheRendersOncePerBody is the point of the type: the second view of a
// body performs no render at all. The counters prove it without timing
// anything, so the assertion holds on a loaded CI runner.
func TestCacheRendersOncePerBody(t *testing.T) {
	c, reg := newTestCache(t)
	const body = "# hi\n\n*there* [x](https://example.com)\n"

	first := c.Body(mdrender.ProjectKeys{}, body)
	if got, want := renders(t, reg, "task", "ok"), 1.0; got != want {
		t.Fatalf("first view performed %v renders, want %v", got, want)
	}
	for i := 0; i < 5; i++ {
		if got := c.Body(mdrender.ProjectKeys{}, body); got != first {
			t.Fatalf("view %d differs from the first:\n%s\n%s", i+2, got, first)
		}
	}
	if got := renders(t, reg, "task", "ok"); got != 1 {
		t.Fatalf("six views performed %v renders, want 1", got)
	}
	if got := lookups(t, reg, "task", "hit"); got != 5 {
		t.Fatalf("got %v hits, want 5", got)
	}
	if got := lookups(t, reg, "task", "miss"); got != 1 {
		t.Fatalf("got %v misses, want 1", got)
	}
}

// TestCacheMatchesUncached: caching may not change what is served. Every
// hostile shape the uncached pipeline neutralises must come back identical
// through the cache, on the miss and on the hit alike.
func TestCacheMatchesUncached(t *testing.T) {
	bodies := []string{
		"",
		"plain text",
		"<script>alert(1)</script>",
		"[x](javascript:alert(1))",
		"![](https://evil.example/p.png)",
		"![](/blob/" + validHash + ")",
		"- [x] done\n- [ ] not\n",
		// Both fallback paths: over the parser's nesting limit, and over
		// maxRendered.
		strings.Repeat("> ", 600) + "x",
		amplifier(400, 4800),
		// Over maxBody, which the cache refuses to store at all.
		strings.Repeat("x", (64<<10)+1),
	}
	c, _ := newTestCache(t)
	for i, body := range bodies {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			want := mdrender.Body(mdrender.ProjectKeys{}, body)
			if got := c.Body(mdrender.ProjectKeys{}, body); got != want {
				t.Fatalf("miss differs from uncached:\n got %.300s\nwant %.300s", got, want)
			}
			if got := c.Body(mdrender.ProjectKeys{}, body); got != want {
				t.Fatalf("hit differs from uncached:\n got %.300s\nwant %.300s", got, want)
			}
		})
	}
}

// TestDistinctBodiesDoNotShareAnEntry: the key is a content hash, so the
// cache must not serve one body's HTML for another's.
func TestDistinctBodiesDoNotShareAnEntry(t *testing.T) {
	c, _ := newTestCache(t)
	a, b := c.Body(mdrender.ProjectKeys{}, "alpha"), c.Body(mdrender.ProjectKeys{}, "beta")
	if a == b {
		t.Fatalf("two bodies rendered to the same HTML: %q", a)
	}
	if got := c.Body(mdrender.ProjectKeys{}, "alpha"); got != a {
		t.Fatalf("second view of alpha returned %q, want %q", got, a)
	}
}

// TestFlavoursDoNotShareAnEntry: the two flavours render the same body
// differently — a document's "{#sec-1}" is an anchor, a task's is text — so
// the cache key carries the flavour. Without that, whichever page was viewed
// first would decide what the other served.
func TestFlavoursDoNotShareAnEntry(t *testing.T) {
	c, reg := newTestCache(t)
	const body = "## Heading {#sec-1}\n"

	task, doc := c.Body(mdrender.ProjectKeys{}, body), c.DocBody(mdrender.ProjectKeys{}, body)
	if task == doc {
		t.Fatalf("both flavours returned the same HTML: %q", task)
	}
	if got, want := string(c.Body(mdrender.ProjectKeys{}, body)), string(task); got != want {
		t.Fatalf("second task view returned %q, want %q", got, want)
	}
	if got, want := string(c.DocBody(mdrender.ProjectKeys{}, body)), string(doc); got != want {
		t.Fatalf("second doc view returned %q, want %q", got, want)
	}
	// One render each, then a hit each: the counters are labelled by kind, so
	// a document render cannot be read as a task one.
	for _, kind := range []string{"task", "doc"} {
		if got := renders(t, reg, kind, "ok"); got != 1 {
			t.Fatalf("%s renders = %v, want 1", kind, got)
		}
		if got := lookups(t, reg, kind, "hit"); got != 1 {
			t.Fatalf("%s hits = %v, want 1", kind, got)
		}
	}
}

// TestKeySetIsPartOfTheKey (WL-305): the same body renders differently under
// different project-key sets, so a body cached under one must not be served
// under another. This is what makes the body-keyed cache safe without any
// invalidation when a project is added or removed.
func TestKeySetIsPartOfTheKey(t *testing.T) {
	c, _ := newTestCache(t)
	const body = "Follows WL-129."
	if got := string(c.Body(mdrender.ProjectKeys{}, body)); strings.Contains(got, "/tasks/") {
		t.Fatalf("linked under an empty key set:\n%s", got)
	}
	if got := string(c.Body(mdrender.NewProjectKeys([]string{"WL"}), body)); !strings.Contains(got, "/tasks/WL-129") {
		t.Fatalf("cached render from the empty key set was served under a live one:\n%s", got)
	}
	// And back again, so the reverse direction is pinned too.
	if got := string(c.Body(mdrender.ProjectKeys{}, body)); strings.Contains(got, "/tasks/") {
		t.Fatalf("live-key render was served under the empty key set:\n%s", got)
	}
}

// TestNilCacheRendersDocs: a *server built directly in a test carries no
// cache, and the document page must render anyway.
func TestNilCacheRendersDocs(t *testing.T) {
	var c *mdrender.Cache
	const body = "## Heading {#sec-1}\n"
	if got, want := c.DocBody(mdrender.ProjectKeys{}, body), mdrender.DocBody(mdrender.ProjectKeys{}, body); got != want {
		t.Fatalf("nil cache rendered %q, want %q", got, want)
	}
}

// TestOversizeIsNotCached pins the one deliberate hole: an oversize body
// takes the escaping fallback every time rather than storing an entry several
// times maxBody. Its cost is linear, not the quadratic kind the cache exists
// for.
func TestOversizeIsNotCached(t *testing.T) {
	c, reg := newTestCache(t)
	body := strings.Repeat("x", (64<<10)+1)
	c.Body(mdrender.ProjectKeys{}, body)
	c.Body(mdrender.ProjectKeys{}, body)
	if got := renders(t, reg, "task", "oversize"); got != 2 {
		t.Fatalf("got %v oversize renders, want 2", got)
	}
	if got := lookups(t, reg, "task", "hit") + lookups(t, reg, "task", "miss"); got != 0 {
		t.Fatalf("oversize body reached the cache: %v lookups", got)
	}
}

// TestFallbackIsCached: the fallback paths are the expensive ones — a body
// over maxRendered pays the full goldmark parse and the ParseFragment DOM
// before its bound rejects the result — so they must be cached too.
func TestFallbackIsCached(t *testing.T) {
	c, reg := newTestCache(t)
	body := amplifier(400, 4800)
	first := c.Body(mdrender.ProjectKeys{}, body)
	second := c.Body(mdrender.ProjectKeys{}, body)
	if first != second {
		t.Fatal("fallback output differs between views")
	}
	if got := renders(t, reg, "task", "fallback"); got != 1 {
		t.Fatalf("got %v fallback renders, want 1", got)
	}
}

// TestNilCacheRenders: a *server built directly in a test carries no cache.
func TestNilCacheRenders(t *testing.T) {
	var c *mdrender.Cache
	const body = "# hi\n"
	if got, want := c.Body(mdrender.ProjectKeys{}, body), mdrender.Body(mdrender.ProjectKeys{}, body); got != want {
		t.Fatalf("nil cache rendered %q, want %q", got, want)
	}
}

// TestConcurrentMissesCoalesce: without singleflight the cache would not
// bound a burst — N simultaneous first views of one hostile task would each
// start their own render, which is exactly the cost being charged once.
func TestConcurrentMissesCoalesce(t *testing.T) {
	c, reg := newTestCache(t)
	body := amplifier(400, 4800) // seconds of CPU if it ran 32 times

	var wg sync.WaitGroup
	got := make([]string, 32)
	start := make(chan struct{})
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got[i] = string(c.Body(mdrender.ProjectKeys{}, body))
		}(i)
	}
	close(start)
	wg.Wait()

	for i := range got {
		if got[i] != got[0] {
			t.Fatalf("goroutine %d disagrees with goroutine 0", i)
		}
	}
	// Not 1: a caller can miss, then lose the flight that was already
	// finishing, and start the next one. Bounded is the property that
	// matters; 32 would mean no coalescing at all.
	if n := renders(t, reg, "task", "fallback"); n > 4 {
		t.Fatalf("32 concurrent views performed %v renders; singleflight is not coalescing", n)
	}
}
