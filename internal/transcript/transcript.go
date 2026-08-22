// Package transcript turns a coding agent's session transcript into the token
// buckets Worklode bills from.
//
// It reads Claude Code's JSONL transcript, whose path arrives on the
// `transcript_path` field of the SessionEnd and Stop hook payloads. Every
// assistant entry carries the vendor's own `usage` block, so the numbers here
// are reported rather than estimated — nothing re-tokenizes anything.
//
// Three properties of the format drive the whole implementation:
//
//   - An assistant message is written once per content block, so the same
//     usage block appears on several consecutive lines. Summing lines
//     double- or triple-counts; entries are deduplicated by message id.
//   - The prompt is split across four separately priced classes, and cache
//     writes are further split by TTL. Collapsing them loses most of the bill.
//   - A transcript is cumulative and one session can work several worktrees in
//     sequence, so entries are filtered by the working directory they ran in.
package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxLine bounds one JSONL line. Transcript lines carry whole tool results and
// file contents, so the 64KiB scanner default is far too small; a line beyond
// this is skipped rather than treated as the end of the file.
const maxLine = 32 << 20

// Usage is one turn's tokens, split the way they are billed. Mirrors
// store.TokenCounts; kept separate so this package stays free of the store.
type Usage struct {
	// Input is the uncached remainder of the prompt, NOT the prompt size.
	Input int64
	// CacheWrite5m and CacheWrite1h are prefix written to cache at each TTL,
	// billed above base input and at different rates from each other.
	CacheWrite5m int64
	CacheWrite1h int64
	// CacheRead is prefix served from cache, billed at a fraction of input.
	// In a long agentic session it dwarfs every other class.
	CacheRead int64
	// Output is generated tokens. Never cached — last turn's output re-enters
	// the next prompt and is billed as CacheRead from then on.
	Output int64
}

// Total is every billed token in the bucket.
func (u Usage) Total() int64 {
	return u.Input + u.CacheWrite5m + u.CacheWrite1h + u.CacheRead + u.Output
}

func (u *Usage) add(other Usage) {
	u.Input += other.Input
	u.CacheWrite5m += other.CacheWrite5m
	u.CacheWrite1h += other.CacheWrite1h
	u.CacheRead += other.CacheRead
	u.Output += other.Output
}

// Bucket is the usage one model accumulated on one UTC day at one billing
// speed — the granularity cost can be computed at, since rates vary by all
// three.
type Bucket struct {
	Day   time.Time
	Model string
	// Speed is "fast" for fast-mode turns, "standard" otherwise. Fast mode is
	// a separate, more expensive SKU.
	Speed string
	// Cwd is the working directory the entries in this bucket recorded, raw
	// as the transcript wrote it (not normalized). A later consumer uses it
	// to classify spend by directory; Parse itself does nothing with it
	// beyond carrying it through.
	Cwd   string
	Usage Usage
}

// Options tunes which entries count.
type Options struct {
	// Root, when set, keeps only entries whose working directory is inside
	// it. This is how a session that moves between worktrees is attributed:
	// each lease bills the turns that actually ran in its worktree, so the
	// same transcript reported against two leases is not counted twice.
	//
	// Entries that record no working directory are kept — older transcripts
	// omit the field, and dropping them would silently lose their cost.
	Root string
}

// entry is the subset of a transcript line this package reads.
type entry struct {
	Type      string    `json:"type"`
	Cwd       string    `json:"cwd"`
	Timestamp time.Time `json:"timestamp"`
	RequestID string    `json:"requestId"`
	UUID      string    `json:"uuid"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreation            *struct {
				Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
				Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
			Speed string `json:"speed"`
		} `json:"usage"`
	} `json:"message"`
}

// ParseFile reads the transcript at path. A missing file is not an error: a
// hook can fire for a session whose transcript was never written, and losing
// cost for it must not fail the hook.
func ParseFile(path string, opts Options) ([]Bucket, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open transcript %s: %w", path, err)
	}
	defer f.Close()
	return Parse(f, opts)
}

// Parse reads JSONL from r and returns the billed buckets, ordered by day then
// model then speed.
//
// Unparseable lines are skipped rather than failing the parse. A transcript is
// appended to while the session runs, so the last line can legitimately be a
// partial write, and one bad line should not cost a session its whole bill.
func Parse(r io.Reader, opts Options) ([]Bucket, error) {
	root := opts.Root
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		root = filepath.Clean(root)
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)

	seen := map[string]bool{}
	byKey := map[key]*Usage{}
	var order []key

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		u := e.Message.Usage
		if u == nil || e.Message.Model == "" {
			continue // not a billed turn
		}
		if !inRoot(root, e.Cwd) {
			continue
		}
		// One assistant message spans several lines, one per content block,
		// each repeating the same usage. Count it once. Sidechain (subagent)
		// turns have their own message ids and are counted — they are billed,
		// and often on a cheaper model than the main loop.
		if id := dedupeID(e); id != "" {
			if seen[id] {
				continue
			}
			seen[id] = true
		}

		k := key{
			day:   e.Timestamp.UTC().Truncate(24 * time.Hour),
			model: e.Message.Model,
			speed: normalizeSpeed(u.Speed),
			cwd:   e.Cwd,
		}
		acc, ok := byKey[k]
		if !ok {
			acc = &Usage{}
			byKey[k] = acc
			order = append(order, k)
		}

		write5m, write1h := u.CacheCreationInputTokens, int64(0)
		if c := u.CacheCreation; c != nil {
			write5m, write1h = c.Ephemeral5m, c.Ephemeral1h
			// Defensive: if the breakdown accounts for less than the headline
			// figure, the remainder is still billed. Attribute it the same way
			// a missing breakdown is (see below) rather than dropping it.
			if rest := u.CacheCreationInputTokens - (write5m + write1h); rest > 0 {
				write5m += rest
			}
		}
		// With no breakdown at all, the whole cache write is attributed to the
		// 5-minute TTL: that is the vendor's default when a request does not
		// ask for the 1-hour TTL. It is an assumption, and the only one in
		// this package — every current transcript carries the breakdown.

		acc.add(Usage{
			Input:        u.InputTokens,
			CacheWrite5m: write5m,
			CacheWrite1h: write1h,
			CacheRead:    u.CacheReadInputTokens,
			Output:       u.OutputTokens,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read transcript: %w", err)
	}

	buckets := make([]Bucket, 0, len(order))
	for _, k := range order {
		if byKey[k].Total() == 0 {
			continue
		}
		buckets = append(buckets, Bucket{
			Day: k.day, Model: k.model, Speed: k.speed, Cwd: k.cwd, Usage: *byKey[k],
		})
	}
	sortBuckets(buckets)
	return buckets, nil
}

type key struct {
	day   time.Time
	model string
	speed string
	cwd   string
}

// dedupeID picks the most specific identity available for a turn. The message
// id is the vendor's own and is exactly one per billed response; requestId and
// uuid are fallbacks for entries that predate it.
func dedupeID(e entry) string {
	switch {
	case e.Message.ID != "":
		return e.Message.ID
	case e.RequestID != "":
		return e.RequestID
	default:
		return e.UUID
	}
}

func normalizeSpeed(speed string) string {
	if speed == "fast" {
		return "fast"
	}
	return "standard"
}

// inRoot reports whether an entry recorded in cwd belongs to root.
func inRoot(root, cwd string) bool {
	if root == "" || cwd == "" {
		return true
	}
	cwd = filepath.Clean(cwd)
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	return cwd == root || strings.HasPrefix(cwd, root+string(filepath.Separator))
}

func sortBuckets(buckets []Bucket) {
	sort.Slice(buckets, func(i, j int) bool {
		a, b := buckets[i], buckets[j]
		if !a.Day.Equal(b.Day) {
			return a.Day.Before(b.Day)
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		if a.Speed != b.Speed {
			return a.Speed < b.Speed
		}
		return a.Cwd < b.Cwd
	})
}
