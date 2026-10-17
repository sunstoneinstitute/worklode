package api

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestSpokenRef(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, key, want string }{
		{"spec seventy-seven", "WL", "WL-SPEC-77"},
		{"Spec Seventy Seven", "wl", "WL-SPEC-77"},
		{"task five twelve", "WL", "WL-512"},
		{"task five one two", "WL", "WL-512"},
		{"five twelve", "WL", "WL-512"},
		{"512", "WL", "WL-512"},
		{"requirement 1300", "WL", "WL-REQ-1300"},
		{"requirement one thousand three hundred", "WL", "WL-REQ-1300"},
		{"requirement thirteen hundred", "WL", "WL-REQ-1300"},
		{"rule twelve hundred and five", "WL", "WL-RULE-1205"},
		{"plan number three", "WL", "WL-PLAN-3"},
		{"spec three oh five", "WL", "WL-SPEC-305"},
		{"spec seventy seven section three point two", "WL", "WL-SPEC-77#sec-3.2"},
		{"spec 77 section 4.1a", "WL", "WL-SPEC-77#sec-4.1a"},
		{"dp spec nine", "WL", "DP-SPEC-9"},
		{"wl-spec-77", "", "WL-SPEC-77"},
		{"WL-SPEC-77#sec-3", "", "WL-SPEC-77#sec-3"},
		{"wl-12", "", "WL-12"},
		{"spec one hundred twenty one", "WL", "WL-SPEC-121"},
		{"task one twenty", "WL", "WL-120"},
		// Not readable as a ref: passed through for the API's resolver.
		{"design-documents-in-the-backbone", "WL", "design-documents-in-the-backbone"},
		{"spec seventy", "", "spec seventy"},
		{"spec banana", "WL", "spec banana"},
		{"task five section two", "WL", "task five section two"},
		{"", "WL", ""},
		// A kind word leads, so a hyphen does not turn it into a task id.
		{"spec-77", "WL", "WL-SPEC-77"},
		{"Plan-3", "WL", "WL-PLAN-3"},
		{"req-9", "DP", "DP-REQ-9"},
		{"task-12", "WL", "WL-12"},
		// <PREFIX>-<n> is a task only when PREFIX is a project key; a slug
		// passes through as it was written.
		{"rollout-2", "WL", "rollout-2"},
		{"Rollout-Plan-2", "WL", "Rollout-Plan-2"},
		{"dp-7", "WL", "DP-7"},
		{"ops-7", "OPS", "OPS-7"},
		{"zz spec nine", "WL", "zz spec nine"},
	} {
		if got := spokenRef(c.in, c.key, []string{"WL", "DP"}); got != c.want {
			t.Errorf("spokenRef(%q, %q) = %q, want %q", c.in, c.key, got, c.want)
		}
	}
}

func TestSpokenNumber(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"seventy seven", "77", true},
		{"nine thousand nine hundred ninety nine", "9999", true},
		{"two thousand and five", "2005", true},
		{"twelve thirty four", "1234", true},
		{"one two three four", "1234", true},
		{"hundred", "", false},
		{"thousand thousand", "", false},
		{"and five", "", false},
		{"seven fish", "", false},
	} {
		got, ok := spokenNumber(strings.Fields(c.in))
		if got != c.want || ok != c.ok {
			t.Errorf("spokenNumber(%q) = %q %v, want %q %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestMatchProjects(t *testing.T) {
	t.Parallel()
	projects := []model.Project{
		{ID: "worklode", Key: "WL", Name: "Worklode"},
		{ID: "data", Key: "DP", Name: "Data Platform"},
		{ID: "web", Key: "WEB", Name: "Website"},
	}
	for _, c := range []struct {
		spoken, want string
		clear        bool
	}{
		{"work load", "WL", true},
		{"worklowed", "WL", true},
		{"W L", "WL", true},
		{"the data platform", "DP", true},
		{"web site", "WEB", true},
	} {
		cands, clear := matchProjects(c.spoken, projects)
		if len(cands) == 0 || cands[0].Key != c.want || clear != c.clear {
			t.Errorf("matchProjects(%q) = %+v clear=%v, want %s first, clear=%v", c.spoken, cands, clear, c.want, c.clear)
		}
	}
	if cands, _ := matchProjects("kubernetes", projects); len(cands) != 0 {
		t.Errorf("matchProjects(kubernetes) = %+v, want none", cands)
	}
	if got := phoneticCode("workload"); got != phoneticCode("worklode") {
		t.Errorf("phoneticCode(workload) = %q, phoneticCode(worklode) = %q; want equal", got, phoneticCode("worklode"))
	}
}

func TestMCPSessions(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	ss := newMCPSessions(time.Hour, 2)
	ss.now = func() time.Time { return now }
	ss.set("s1", "alice", "wl")
	if got := ss.get("s1", "alice"); got != "wl" {
		t.Errorf("get = %q, want wl", got)
	}
	if got := ss.get("s1", "bob"); got != "" {
		t.Errorf("another actor read the session's project: %q", got)
	}
	if got := ss.get("", "alice"); got != "" {
		t.Errorf("no session read a project: %q", got)
	}
	now = now.Add(30 * time.Minute)
	ss.set("s2", "alice", "dp")
	now = now.Add(31 * time.Minute)
	if got := ss.get("s1", "alice"); got != "" {
		t.Errorf("an expired session kept its project: %q", got)
	}
	ss.set("s3", "alice", "web")
	if len(ss.m) > 2 {
		t.Errorf("sessions grew past the cap: %d", len(ss.m))
	}
	if ss.get("s3", "alice") != "web" || ss.get("s2", "alice") != "dp" {
		t.Errorf("the cap dropped a live session: %+v", ss.m)
	}
}

func TestMCPMetrics(t *testing.T) {
	t.Parallel()
	var nilMetrics *mcpMetrics
	nilMetrics.call("show", "ok") // nil-safe

	reg := prometheus.NewRegistry()
	m := newMCPMetrics(reg)
	m.call("show", "not_found")
	if got := testutil.ToFloat64(m.calls.WithLabelValues("show", "not_found")); got != 1 {
		t.Errorf("show/not_found = %v, want 1", got)
	}
	if n := testutil.CollectAndCount(m.calls); n != len(mcpToolNames)*len(mcpOutcomes) {
		t.Errorf("series = %d, want every tool and outcome pre-initialised (%d)", n, len(mcpToolNames)*len(mcpOutcomes))
	}
}

func TestMCPPage(t *testing.T) {
	t.Parallel()
	items := make([]int, 23)
	page, next, err := mcpPage(items, "")
	if err != nil || len(page) != 10 || next != "10" {
		t.Errorf("first page = %d %q %v", len(page), next, err)
	}
	page, next, _ = mcpPage(items, "20")
	if len(page) != 3 || next != "" {
		t.Errorf("last page = %d %q", len(page), next)
	}
	if _, _, err := mcpPage(items, "x"); err == nil {
		t.Error("a bad cursor was accepted")
	}
}

func TestLookupKindAlias(t *testing.T) {
	t.Parallel()
	if a, err := lookupKindAlias(" Bugs "); err != nil || a.Entity != "task" || a.Kind != "bug" {
		t.Errorf("lookupKindAlias(Bugs) = %+v %v", a, err)
	}
	if _, err := lookupKindAlias("plam"); err == nil || !strings.Contains(err.Error(), "plan (a plan doc)") {
		t.Errorf("lookupKindAlias(plam) = %v", err)
	}
	if k, err := aliasKind("specs", "task"); err == nil {
		t.Errorf("aliasKind(specs, task) = %q, want refused", k)
	}
}
