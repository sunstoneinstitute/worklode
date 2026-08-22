package transcript

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// utcDay is the bucket key Parse derives from any timestamp on that UTC date.
func utcDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func parseString(t *testing.T, jsonl string, opts Options) []Bucket {
	t.Helper()
	got, err := Parse(strings.NewReader(jsonl), opts)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return got
}

func assertBuckets(t *testing.T, got, want []Bucket) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("bucket count: got %d (%+v), want %d (%+v)", len(got), got, len(want), want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("bucket %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		jsonl string
		opts  Options
		want  []Bucket
		// wantTotalInput, when set, asserts that Input summed across every
		// returned bucket still equals this value — the invariant spec 052 §3
		// promises for an existing Options{Root: root} caller: cwd only
		// splits a merged bucket into finer-grained rows, it never changes
		// the total tokens that caller sees.
		wantTotalInput *int64
	}{
		{
			// Claude Code writes one line per content block, each repeating the
			// whole usage block. Summing lines would double- and triple-count.
			name: "one message id counts once, two ids count twice",
			jsonl: `
{"type":"assistant","timestamp":"2026-07-19T10:00:00Z","message":{"id":"msg_1","model":"claude-opus-5","usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":5}}}
{"type":"assistant","timestamp":"2026-07-19T10:00:00Z","message":{"id":"msg_1","model":"claude-opus-5","usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":5}}}
{"type":"assistant","timestamp":"2026-07-19T10:00:00Z","message":{"id":"msg_1","model":"claude-opus-5","usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":5}}}
{"type":"assistant","timestamp":"2026-07-19T10:01:00Z","message":{"id":"msg_2","model":"claude-opus-5","usage":{"input_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":7}}}
{"type":"assistant","timestamp":"2026-07-19T10:01:00Z","message":{"id":"msg_2","model":"claude-opus-5","usage":{"input_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":7}}}
`,
			want: []Bucket{{
				Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "standard",
				Usage: Usage{Input: 30, CacheRead: 100, Output: 12},
			}},
		},
		{
			// The two TTLs are separate SKUs (1.25x and 2x base input), so the
			// split must survive parsing.
			name: "cache creation splits by TTL",
			jsonl: `
{"type":"assistant","timestamp":"2026-07-19T10:00:00Z","message":{"id":"msg_1","model":"claude-opus-5","usage":{"input_tokens":1,"cache_creation_input_tokens":1000,"cache_read_input_tokens":0,"output_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":700,"ephemeral_1h_input_tokens":300}}}}
`,
			want: []Bucket{{
				Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "standard",
				Usage: Usage{Input: 1, CacheWrite5m: 700, CacheWrite1h: 300, Output: 2},
			}},
		},
		{
			// No breakdown means the vendor default TTL, which is 5 minutes.
			name: "missing breakdown lands wholly on the 5m TTL",
			jsonl: `
{"type":"assistant","timestamp":"2026-07-19T10:00:00Z","message":{"id":"msg_1","model":"claude-opus-5","usage":{"input_tokens":1,"cache_creation_input_tokens":1000,"cache_read_input_tokens":0,"output_tokens":2}}}
`,
			want: []Bucket{{
				Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "standard",
				Usage: Usage{Input: 1, CacheWrite5m: 1000, Output: 2},
			}},
		},
		{
			// A breakdown that accounts for less than the headline figure still
			// billed the remainder; it must not be dropped.
			name: "breakdown shortfall joins the 5m TTL",
			jsonl: `
{"type":"assistant","timestamp":"2026-07-19T10:00:00Z","message":{"id":"msg_1","model":"claude-opus-5","usage":{"input_tokens":1,"cache_creation_input_tokens":1000,"cache_read_input_tokens":0,"output_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":600,"ephemeral_1h_input_tokens":100}}}}
`,
			want: []Bucket{{
				Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "standard",
				Usage: Usage{Input: 1, CacheWrite5m: 900, CacheWrite1h: 100, Output: 2},
			}},
		},
		{
			// Day, model and speed each price differently, so each splits a
			// bucket; matching on all three merges. The first two entries
			// straddle a UTC midnight by two seconds.
			name: "day, model and speed each split a bucket",
			jsonl: `
{"type":"assistant","timestamp":"2026-07-19T23:59:59Z","message":{"id":"msg_a","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":0}}}
{"type":"assistant","timestamp":"2026-07-20T00:00:01Z","message":{"id":"msg_b","model":"claude-opus-5","usage":{"input_tokens":2,"output_tokens":0}}}
{"type":"assistant","timestamp":"2026-07-20T01:00:00Z","message":{"id":"msg_c","model":"claude-opus-5","usage":{"input_tokens":4,"output_tokens":0,"speed":"fast"}}}
{"type":"assistant","timestamp":"2026-07-20T02:00:00Z","message":{"id":"msg_d","model":"claude-haiku-4-5","usage":{"input_tokens":8,"output_tokens":0}}}
{"type":"assistant","timestamp":"2026-07-20T03:00:00Z","message":{"id":"msg_e","model":"claude-opus-5","usage":{"input_tokens":16,"output_tokens":0}}}
`,
			want: []Bucket{
				{Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "standard", Usage: Usage{Input: 1}},
				{Day: utcDay(2026, 7, 20), Model: "claude-haiku-4-5", Speed: "standard", Usage: Usage{Input: 8}},
				{Day: utcDay(2026, 7, 20), Model: "claude-opus-5", Speed: "fast", Usage: Usage{Input: 4}},
				{Day: utcDay(2026, 7, 20), Model: "claude-opus-5", Speed: "standard", Usage: Usage{Input: 18}},
			},
		},
		{
			// Subagent turns are real spend and carry their own message ids.
			name: "sidechain turns are billed",
			jsonl: `
{"type":"assistant","isSidechain":false,"timestamp":"2026-07-19T10:00:00Z","message":{"id":"msg_main","model":"claude-opus-5","usage":{"input_tokens":10,"output_tokens":1}}}
{"type":"assistant","isSidechain":true,"timestamp":"2026-07-19T10:05:00Z","message":{"id":"msg_sub","model":"claude-opus-5","usage":{"input_tokens":100,"output_tokens":2}}}
`,
			want: []Bucket{{
				Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "standard",
				Usage: Usage{Input: 110, Output: 3},
			}},
		},
		{
			// Root attribution must respect the path separator: /tmp/.worktrees/xyz is a
			// sibling worktree, not a child of /tmp/.worktrees/x. An entry with no cwd is
			// kept, since older transcripts omit the field.
			name: "root keeps the tree, excludes the sibling, keeps cwd-less entries",
			opts: Options{Root: "/tmp/.worktrees/x"},
			jsonl: `
{"type":"assistant","cwd":"/tmp/.worktrees/x","timestamp":"2026-07-19T10:00:00Z","message":{"id":"msg_1","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":0}}}
{"type":"assistant","cwd":"/tmp/.worktrees/x/sub","timestamp":"2026-07-19T10:01:00Z","message":{"id":"msg_2","model":"claude-opus-5","usage":{"input_tokens":2,"output_tokens":0}}}
{"type":"assistant","cwd":"/tmp/.worktrees/xyz","timestamp":"2026-07-19T10:02:00Z","message":{"id":"msg_3","model":"claude-opus-5","usage":{"input_tokens":400,"output_tokens":0}}}
{"type":"assistant","timestamp":"2026-07-19T10:03:00Z","message":{"id":"msg_4","model":"claude-opus-5","usage":{"input_tokens":8,"output_tokens":0}}}
`,
			// cwd is now part of the bucketing key (spec 052 §3), so the three
			// kept entries — three distinct raw cwds, including the cwd-less
			// one — land in three buckets instead of merging into one. The
			// sibling worktree /tmp/.worktrees/xyz is still excluded by Root
			// filtering, same as before.
			want: []Bucket{
				{
					Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "standard",
					Cwd: "", Usage: Usage{Input: 8},
				},
				{
					Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "standard",
					Cwd: "/tmp/.worktrees/x", Usage: Usage{Input: 1},
				},
				{
					Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "standard",
					Cwd: "/tmp/.worktrees/x/sub", Usage: Usage{Input: 2},
				},
			},
			// The invariant spec 052 §3 promises: an existing Options{Root:
			// root} caller's total is unchanged by cwd-splitting — only the
			// sibling worktree's 400 tokens are dropped by Root filtering.
			wantTotalInput: int64Ptr(11),
		},
		{
			// A transcript is appended to while the session runs, so the last
			// line can be a partial write. One bad line must not cost the
			// session its whole bill.
			name: "garbage, blank and truncated lines are skipped",
			jsonl: `
{"type":"assistant","timestamp":"2026-07-19T10:00:00Z","message":{"id":"msg_1","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":0}}}
this is not json

{"type":"assistant","timestamp":"2026-07-19T10:01:00Z","message":{"id":"msg_2","model":"claude-opus-5","usage":{"input_tokens":2,"output_tokens":0}}}
{"type":"assistant","timestamp":"2026-07-19T10:02:00Z","message":{"id":"msg_3","model":"claude-opus-5","usa`,
			want: []Bucket{{
				Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "standard",
				Usage: Usage{Input: 3},
			}},
		},
		{
			// Only entries carrying both a usage block and a model are billed
			// turns; everything else in the transcript is narration.
			name: "entries without usage or without a model are ignored",
			jsonl: `
{"type":"user","timestamp":"2026-07-19T10:00:00Z","message":{"role":"user","content":"hi"}}
{"type":"assistant","timestamp":"2026-07-19T10:01:00Z","message":{"id":"msg_nomodel","usage":{"input_tokens":999,"output_tokens":999}}}
{"type":"assistant","timestamp":"2026-07-19T10:02:00Z","message":{"id":"msg_1","model":"claude-opus-5","usage":{"input_tokens":5,"output_tokens":0}}}
`,
			want: []Bucket{{
				Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "standard",
				Usage: Usage{Input: 5},
			}},
		},
		{
			// A turn that billed nothing produces no bucket.
			name: "all-zero usage produces no bucket",
			jsonl: `
{"type":"assistant","timestamp":"2026-07-19T10:00:00Z","message":{"id":"msg_1","model":"claude-opus-5","usage":{"input_tokens":0,"output_tokens":0}}}
`,
			want: nil,
		},
		{
			name:  "empty input",
			jsonl: "",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseString(t, tt.jsonl, tt.opts)
			assertBuckets(t, got, tt.want)
			if tt.wantTotalInput != nil {
				var total int64
				for _, b := range got {
					total += b.Usage.Input
				}
				if total != *tt.wantTotalInput {
					t.Errorf("total Input across buckets = %d, want %d", total, *tt.wantTotalInput)
				}
			}
		})
	}
}

func int64Ptr(v int64) *int64 { return &v }

// TestParseOrderIsDeterministic pins the output order (day, then model, then
// speed) regardless of the order entries appear in, and across repeated runs —
// the accumulator is a map, so an unsorted result would vary run to run.
func TestParseOrderIsDeterministic(t *testing.T) {
	const jsonl = `
{"type":"assistant","timestamp":"2026-07-20T03:00:00Z","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":0,"speed":"fast"}}}
{"type":"assistant","timestamp":"2026-07-19T03:00:00Z","message":{"id":"m2","model":"claude-sonnet-5","usage":{"input_tokens":2,"output_tokens":0}}}
{"type":"assistant","timestamp":"2026-07-20T04:00:00Z","message":{"id":"m3","model":"claude-opus-5","usage":{"input_tokens":3,"output_tokens":0}}}
{"type":"assistant","timestamp":"2026-07-19T05:00:00Z","message":{"id":"m4","model":"claude-haiku-4-5","usage":{"input_tokens":4,"output_tokens":0}}}
{"type":"assistant","timestamp":"2026-07-20T05:00:00Z","message":{"id":"m5","model":"claude-haiku-4-5","usage":{"input_tokens":5,"output_tokens":0}}}
`
	want := []Bucket{
		{Day: utcDay(2026, 7, 19), Model: "claude-haiku-4-5", Speed: "standard", Usage: Usage{Input: 4}},
		{Day: utcDay(2026, 7, 19), Model: "claude-sonnet-5", Speed: "standard", Usage: Usage{Input: 2}},
		{Day: utcDay(2026, 7, 20), Model: "claude-haiku-4-5", Speed: "standard", Usage: Usage{Input: 5}},
		{Day: utcDay(2026, 7, 20), Model: "claude-opus-5", Speed: "fast", Usage: Usage{Input: 1}},
		{Day: utcDay(2026, 7, 20), Model: "claude-opus-5", Speed: "standard", Usage: Usage{Input: 3}},
	}

	for i := 0; i < 20; i++ {
		assertBuckets(t, parseString(t, jsonl, Options{}), want)
	}
}

func TestParseFileMissingIsNotAnError(t *testing.T) {
	// A hook can fire for a session whose transcript was never written; losing
	// its cost must not fail the hook.
	got, err := ParseFile(filepath.Join(t.TempDir(), "no-such-transcript.jsonl"), Options{})
	if err != nil {
		t.Fatalf("ParseFile on a missing path: got error %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("ParseFile on a missing path: got %+v, want nil", got)
	}
}

// realisticTranscript is a trimmed but shape-faithful Claude Code transcript:
// user turns without usage, assistant messages spread over several content
// blocks, a sidechain turn on a cheaper model, and a fast-mode turn.
const realisticTranscript = `
{"type":"user","cwd":"/tmp/.worktrees/demo","timestamp":"2026-07-19T09:59:00Z","uuid":"u-1","message":{"role":"user","content":"add a test"}}
{"parentUuid":"u-1","type":"assistant","cwd":"/tmp/.worktrees/demo","timestamp":"2026-07-19T10:00:00Z","requestId":"req_a","uuid":"a-1","message":{"id":"msg_a","model":"claude-opus-5","usage":{"input_tokens":4,"cache_creation_input_tokens":1200,"cache_read_input_tokens":0,"output_tokens":150,"cache_creation":{"ephemeral_5m_input_tokens":1200,"ephemeral_1h_input_tokens":0}}}}
{"parentUuid":"u-1","type":"assistant","cwd":"/tmp/.worktrees/demo","timestamp":"2026-07-19T10:00:00Z","requestId":"req_a","uuid":"a-2","message":{"id":"msg_a","model":"claude-opus-5","usage":{"input_tokens":4,"cache_creation_input_tokens":1200,"cache_read_input_tokens":0,"output_tokens":150,"cache_creation":{"ephemeral_5m_input_tokens":1200,"ephemeral_1h_input_tokens":0}}}}
{"type":"user","cwd":"/tmp/.worktrees/demo","timestamp":"2026-07-19T10:00:05Z","uuid":"u-2","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}
{"type":"assistant","cwd":"/tmp/.worktrees/demo","timestamp":"2026-07-19T10:00:30Z","requestId":"req_b","uuid":"a-3","message":{"id":"msg_b","model":"claude-opus-5","usage":{"input_tokens":6,"cache_creation_input_tokens":0,"cache_read_input_tokens":1200,"output_tokens":240,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}}}}
{"type":"assistant","isSidechain":true,"cwd":"/tmp/.worktrees/demo","timestamp":"2026-07-19T10:01:00Z","requestId":"req_d","uuid":"a-4","message":{"id":"msg_d","model":"claude-haiku-4-5","usage":{"input_tokens":3,"cache_creation_input_tokens":900,"cache_read_input_tokens":0,"output_tokens":60,"cache_creation":{"ephemeral_5m_input_tokens":900,"ephemeral_1h_input_tokens":0}}}}
{"type":"assistant","cwd":"/tmp/.worktrees/demo","timestamp":"2026-07-19T10:02:00Z","requestId":"req_c","uuid":"a-5","message":{"id":"msg_c","model":"claude-opus-5","usage":{"input_tokens":11,"cache_creation_input_tokens":8500,"cache_read_input_tokens":1440,"output_tokens":95,"cache_creation":{"ephemeral_5m_input_tokens":500,"ephemeral_1h_input_tokens":8000}}}}
{"type":"assistant","cwd":"/tmp/.worktrees/demo","timestamp":"2026-07-19T10:02:00Z","requestId":"req_c","uuid":"a-6","message":{"id":"msg_c","model":"claude-opus-5","usage":{"input_tokens":11,"cache_creation_input_tokens":8500,"cache_read_input_tokens":1440,"output_tokens":95,"cache_creation":{"ephemeral_5m_input_tokens":500,"ephemeral_1h_input_tokens":8000}}}}
{"type":"assistant","cwd":"/tmp/.worktrees/demo","timestamp":"2026-07-19T10:02:00Z","requestId":"req_c","uuid":"a-7","message":{"id":"msg_c","model":"claude-opus-5","usage":{"input_tokens":11,"cache_creation_input_tokens":8500,"cache_read_input_tokens":1440,"output_tokens":95,"cache_creation":{"ephemeral_5m_input_tokens":500,"ephemeral_1h_input_tokens":8000}}}}
{"type":"assistant","cwd":"/tmp/.worktrees/demo","timestamp":"2026-07-19T10:03:00Z","requestId":"req_e","uuid":"a-8","message":{"id":"msg_e","model":"claude-opus-5","usage":{"input_tokens":2,"cache_creation_input_tokens":0,"cache_read_input_tokens":10235,"output_tokens":30,"speed":"fast","cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}}}}
{"type":"system","cwd":"/tmp/.worktrees/demo","timestamp":"2026-07-19T10:04:00Z","uuid":"s-1","content":"session ended"}
`

// realisticWant is the hand-summed total of realisticTranscript. It is spelled
// out so a refactor cannot quietly change the numbers a bill is computed from.
var realisticWant = []Bucket{
	{
		Day: utcDay(2026, 7, 19), Model: "claude-haiku-4-5", Speed: "standard",
		Cwd:   "/tmp/.worktrees/demo",
		Usage: Usage{Input: 3, CacheWrite5m: 900, Output: 60},
	},
	{
		Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "fast",
		Cwd:   "/tmp/.worktrees/demo",
		Usage: Usage{Input: 2, CacheRead: 10235, Output: 30},
	},
	{
		Day: utcDay(2026, 7, 19), Model: "claude-opus-5", Speed: "standard",
		Cwd:   "/tmp/.worktrees/demo",
		Usage: Usage{Input: 21, CacheWrite5m: 1700, CacheWrite1h: 8000, CacheRead: 2640, Output: 485},
	},
}

func TestParseRealisticTranscript(t *testing.T) {
	assertBuckets(t, parseString(t, realisticTranscript, Options{}), realisticWant)

	// Every billed token, across every bucket.
	var total int64
	for _, b := range parseString(t, realisticTranscript, Options{}) {
		total += b.Usage.Total()
	}
	if want := int64(24076); total != want {
		t.Fatalf("total billed tokens: got %d, want %d", total, want)
	}
}

func TestParseFileReadsRealisticTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(realisticTranscript), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := ParseFile(path, Options{Root: "/tmp/.worktrees/demo"})
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	assertBuckets(t, got, realisticWant)

	// The same transcript reported against a different worktree bills nothing,
	// which is what keeps a two-lease session from being counted twice.
	other, err := ParseFile(path, Options{Root: "/tmp/.worktrees/elsewhere"})
	if err != nil {
		t.Fatalf("ParseFile with a foreign root: %v", err)
	}
	assertBuckets(t, other, nil)
}

func TestParseGroupsByCwd(t *testing.T) {
	lines := []string{
		`{"type":"assistant","cwd":"/repo/.worktrees/WL-1-a","timestamp":"2026-08-01T10:00:00Z","message":{"id":"m1","model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":10}}}`,
		`{"type":"assistant","cwd":"/repo","timestamp":"2026-08-01T10:01:00Z","message":{"id":"m2","model":"claude-sonnet-5","usage":{"input_tokens":200,"output_tokens":20}}}`,
	}
	buckets, err := Parse(strings.NewReader(strings.Join(lines, "\n")), Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("got %d buckets, want 2 (one per cwd): %+v", len(buckets), buckets)
	}
	byCwd := map[string]Bucket{}
	for _, b := range buckets {
		byCwd[b.Cwd] = b
	}
	if got := byCwd["/repo/.worktrees/WL-1-a"].Usage.Input; got != 100 {
		t.Errorf("worktree bucket input = %d, want 100", got)
	}
	if got := byCwd["/repo"].Usage.Input; got != 200 {
		t.Errorf("main-checkout bucket input = %d, want 200", got)
	}
}
