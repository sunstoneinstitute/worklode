package mdrender

import (
	"container/list"
	"crypto/sha256"
	"html/template"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/singleflight"
)

// Cache memoises Body's output so a body is rendered once per edit rather
// than once per page view.
//
// Rendering an untrusted body is expensive in a way no cap fixes. goldmark's
// inline parser is quadratic on repeated "[x](" — a hostile 64 KiB body,
// exactly what maxBody still admits and exactly what spec 020's inbox import
// can write, measured at 1.584s of CPU — and balance() hands the sanitised
// fragment to html.ParseFragment before maxRendered can bite, building a
// ~1.9M-node DOM and allocating 315 MB for output that is then discarded.
// maxBody and maxRendered bound how far each can grow; neither makes the
// worst admissible body cheap. Paying that per view is the defect. Paying it
// once per distinct body is not, so the cache — not a redesigned balance
// pass — is the fix for both (WL-222).
//
// The key is the body's SHA-256, which is what "per revision" means here:
// tasks carry no revision counter, and the content hash is strictly better
// than the (id, updated_at) pair that would stand in for one. It cannot go
// stale, it collapses identical bodies across tasks into one entry, and it
// keeps this package unaware of what a task is. Hashing 64 KiB costs tens of
// microseconds against the 1.584s it replaces. SHA-256 rather than a fast
// non-cryptographic hash because bodies are attacker-controlled: a forgeable
// key would let one task's HTML be served under another's.
type Cache struct {
	mu      sync.Mutex
	lru     *list.List // front is most recently used; values are *entry
	index   map[[32]byte]*list.Element
	bytes   int
	maxByte int
	maxEnt  int

	// flight coalesces concurrent misses on the same body. Without it the
	// cache would not bound a burst: N simultaneous views of one hostile
	// task would each start their own 1.584s render, which is the cost this
	// type exists to charge only once.
	flight singleflight.Group

	metrics *Metrics
}

type entry struct {
	key  [32]byte
	html template.HTML
}

// maxCacheBytes bounds the rendered HTML held at once. Every cached value is
// at most maxRendered (1 MiB) or, on the escaping fallback, a few times
// maxBody, so the bound is never one entry away from being violated. 8 MiB
// holds a few thousand ordinary bodies.
const maxCacheBytes = 8 << 20

// maxCacheEntries bounds the entry count independently, so a project of tiny
// bodies cannot grow the map without limit while staying under maxCacheBytes.
const maxCacheEntries = 4096

// NewCache returns a cache with the default bounds, reporting on reg.
func NewCache(reg prometheus.Registerer) *Cache {
	return newCache(maxCacheBytes, maxCacheEntries, NewMetrics(reg))
}

func newCache(maxBytes, maxEntries int, m *Metrics) *Cache {
	return &Cache{
		lru:     list.New(),
		index:   make(map[[32]byte]*list.Element),
		maxByte: maxBytes,
		maxEnt:  maxEntries,
		metrics: m,
	}
}

// Body renders an untrusted markdown task body to sanitised HTML, reusing an
// earlier render of the same body when there is one. The safety contract is
// Body's, unchanged: this only decides how often it runs.
//
// Nil-safe. A nil *Cache renders every call, which is what a server built
// directly in a test wants.
func (c *Cache) Body(keys ProjectKeys, body string) template.HTML {
	return c.render(taskFlavour, keys, body)
}

// DocBody is Body for a design-document body: the same cache, keyed so that a
// body reaching both flavours is stored once per flavour rather than served
// under whichever rendered it first.
func (c *Cache) DocBody(keys ProjectKeys, body string) template.HTML {
	return c.render(docFlavour, keys, body)
}

func (c *Cache) render(f flavour, keys ProjectKeys, body string) template.HTML {
	if c == nil {
		html, _ := render(f, keys, body)
		return html
	}

	// Oversize bodies are not cached. They take the escaping fallback, whose
	// cost is linear in the body and whose output is several times the body
	// — up to the API's 1 MiB request cap — so an entry would blow the byte
	// bound to avoid work that was never the expensive kind.
	if len(body) > f.maxBody {
		html, outcome := render(f, keys, body)
		c.metrics.render(f.kind, outcome)
		return html
	}

	key := keyOf(f.kind, keys, body)
	if html, ok := c.get(key); ok {
		c.metrics.lookup(f.kind, "hit")
		return html
	}
	c.metrics.lookup(f.kind, "miss")

	v, _, _ := c.flight.Do(string(key[:]), func() (any, error) {
		// A caller that lost the race to start this flight may have been
		// served from cache in between; re-check so the leader does not
		// re-render what the previous leader just stored.
		if html, ok := c.get(key); ok {
			return html, nil
		}
		html, outcome := render(f, keys, body)
		c.metrics.render(f.kind, outcome)
		c.put(key, html)
		return html, nil
	})
	return v.(template.HTML)
}

// keyOf is the cache key: the body's own content, hashed, under the flavour
// and project-key set that rendered it. See Cache's doc comment for why the
// hash has to be a cryptographic one. The flavour is in the key because the
// two pipelines produce different HTML for the same input — a document body's
// "{#sec-1}" is an anchor, a task body's is text — so sharing an entry would
// serve one page's render on the other.
//
// The project-key set is in the key for the same reason (WL-305): the same
// body renders "COW-7" as a link or as plain text depending on whether COW is
// a live project. Keying on its fingerprint is what lets a new project's
// tasks start linking immediately instead of at the next restart — the entries
// rendered under the old set are simply never looked up again, and age out of
// the LRU. The fingerprint is a fixed-width hex digest, so it cannot run into
// the NUL separator.
func keyOf(kind string, keys ProjectKeys, body string) [32]byte {
	return sha256.Sum256([]byte(kind + "\x00" + keys.fingerprint + "\x00" + body))
}

func (c *Cache) get(key [32]byte) (template.HTML, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.index[key]
	if !ok {
		return "", false
	}
	c.lru.MoveToFront(el)
	return el.Value.(*entry).html, true
}

func (c *Cache) put(key [32]byte, html template.HTML) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.index[key]; ok {
		c.lru.MoveToFront(el)
		return
	}
	c.index[key] = c.lru.PushFront(&entry{key: key, html: html})
	c.bytes += len(html)

	evicted := 0
	for c.lru.Len() > 0 && (c.bytes > c.maxByte || c.lru.Len() > c.maxEnt) {
		oldest := c.lru.Back()
		e := oldest.Value.(*entry)
		c.lru.Remove(oldest)
		delete(c.index, e.key)
		c.bytes -= len(e.html)
		evicted++
	}
	c.metrics.evicted(evicted)
	c.metrics.setBytes(c.bytes)
}
