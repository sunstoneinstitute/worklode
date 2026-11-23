package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestPickDiffVersions(t *testing.T) {
	cases := []struct {
		name         string
		versions     []int
		from, to     int
		wantF, wantT int
		wantErr      string
	}{
		{"defaults pick newest pair", []int{1, 2, 3, 4}, 0, 0, 3, 4, ""},
		{"to picks the version before it", []int{1, 2, 3, 4}, 0, 3, 2, 3, ""},
		{"explicit pair kept", []int{1, 2, 3, 4}, 1, 4, 1, 4, ""},
		{"from only", []int{1, 2, 3}, 1, 0, 1, 3, ""},
		{"one version", []int{1}, 0, 0, 0, 0, "ZZ-REQ-7 has one version (v1); there is nothing to diff"},
		{"from equals to", []int{1, 2, 3}, 2, 2, 0, 0, "--from 2 is not older than --to 2; ZZ-REQ-7 has v1, v2, v3"},
		{"from after to", []int{1, 2, 3}, 3, 2, 0, 0, "--from 3 is not older than --to 2; ZZ-REQ-7 has v1, v2, v3"},
		{"unknown to", []int{1, 2, 3}, 0, 9, 0, 0, "ZZ-REQ-7 has v1, v2, v3"},
		{"unknown from", []int{1, 2, 4}, 3, 0, 0, 0, "ZZ-REQ-7 has v1, v2, v4"},
		{"to is oldest", []int{1, 2, 3}, 0, 1, 0, 0, "ZZ-REQ-7 has v1, v2, v3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, to, err := pickDiffVersions("ZZ-REQ-7", tc.versions, tc.from, tc.to)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || f != tc.wantF || to != tc.wantT {
				t.Fatalf("got (%d, %d, %v), want (%d, %d)", f, to, err, tc.wantF, tc.wantT)
			}
		})
	}
}

// diffStub serves a rule ZZ-REQ-7 and a document ZZ-SPEC-3 (id 3), and
// records which version numbers were fetched and how many requests arrived.
type diffStub struct {
	ruleFetched, docFetched []int
	requests                int
}

func setupDiff(t *testing.T, s *diffStub, ruleHeadings, ruleBodies, docBodies []string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { s.requests++; http.NotFound(w, r) })
	mux.HandleFunc("GET /api/v1/rules/ZZ-REQ-7/versions", func(w http.ResponseWriter, r *http.Request) {
		s.requests++
		var out []model.RuleVersion
		for i := len(ruleBodies); i >= 1; i-- { // newest first
			out = append(out, model.RuleVersion{Version: i, Heading: ruleHeadings[i-1]})
		}
		writeTestJSON(t, w, out)
	})
	mux.HandleFunc("GET /api/v1/rules/ZZ-REQ-7/versions/{n}", func(w http.ResponseWriter, r *http.Request) {
		s.requests++
		n, _ := strconv.Atoi(r.PathValue("n"))
		s.ruleFetched = append(s.ruleFetched, n)
		writeTestJSON(t, w, model.Rule{Ref: "ZZ-REQ-7", Version: n, Heading: model.Markdown(ruleHeadings[n-1]), Body: model.Markdown(ruleBodies[n-1])})
	})
	mux.HandleFunc("GET /api/v1/docs/resolve", func(w http.ResponseWriter, r *http.Request) {
		s.requests++
		writeTestJSON(t, w, model.Doc{ID: 3, Kind: "spec", Number: 3, ProjectKey: "ZZ"})
	})
	mux.HandleFunc("GET /api/v1/docs/3/versions", func(w http.ResponseWriter, r *http.Request) {
		s.requests++
		var out []model.DocVersionSummary
		for i := len(docBodies); i >= 1; i-- {
			out = append(out, model.DocVersionSummary{Version: i})
		}
		writeTestJSON(t, w, out)
	})
	mux.HandleFunc("GET /api/v1/docs/3/versions/{n}", func(w http.ResponseWriter, r *http.Request) {
		s.requests++
		n, _ := strconv.Atoi(r.PathValue("n"))
		s.docFetched = append(s.docFetched, n)
		writeTestJSON(t, w, model.DocVersion{Doc: 3, Version: n, Title: "Title", Body: docBodies[n-1]})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
}

func threeRuleVersions(t *testing.T, s *diffStub) {
	setupDiff(t, s,
		[]string{"Old head", "Old head", "New head"},
		[]string{"first body", "the old rule", "the new rule"},
		[]string{"spec one", "spec two"})
}

func TestDiffRule(t *testing.T) {
	s := &diffStub{}
	threeRuleVersions(t, s)
	out, err := runLode(t, "diff", "ZZ-REQ-7")
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	if !slices.Equal(s.ruleFetched, []int{2, 3}) {
		t.Errorf("fetched %v, want 2 and 3", s.ruleFetched)
	}
	for _, want := range []string{"ZZ-REQ-7  v2 → v3", "# [-Old-]{+New+} head", "[-old-]{+new+}"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDiffRuleExplicitVersions(t *testing.T) {
	s := &diffStub{}
	threeRuleVersions(t, s)
	out, err := runLode(t, "diff", "ZZ-REQ-7", "--from", "1", "--to", "2")
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	if !slices.Equal(s.ruleFetched, []int{1, 2}) {
		t.Errorf("fetched %v, want 1 and 2", s.ruleFetched)
	}
	if !strings.Contains(out, "ZZ-REQ-7  v1 → v2") {
		t.Errorf("header missing:\n%s", out)
	}
}

func TestDiffDoc(t *testing.T) {
	s := &diffStub{}
	threeRuleVersions(t, s)
	out, err := runLode(t, "diff", "ZZ-SPEC-3")
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	if !slices.Equal(s.docFetched, []int{1, 2}) {
		t.Errorf("fetched %v, want 1 and 2", s.docFetched)
	}
	if !strings.Contains(out, "ZZ-SPEC-3  v1 → v2") {
		t.Errorf("header missing:\n%s", out)
	}
	if !strings.Contains(out, "spec [-one-]{+two+}") || strings.Contains(out, "Title") || strings.Contains(out, "# ") {
		t.Errorf("body should be the text alone:\n%s", out)
	}
}

func TestDiffJSON(t *testing.T) {
	s := &diffStub{}
	threeRuleVersions(t, s)
	out, err := runLode(t, "diff", "ZZ-REQ-7", "--json")
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	var got diffJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got.Ref != "ZZ-REQ-7" || got.From != 2 || got.To != 3 || len(got.Ops) == 0 {
		t.Fatalf("got %+v", got)
	}
	var to strings.Builder
	for _, op := range got.Ops {
		switch op.Op {
		case "equal", "insert":
			to.WriteString(op.Text)
		case "delete":
		default:
			t.Errorf("unknown op %q", op.Op)
		}
	}
	if want := "# New head\n\nthe new rule"; to.String() != want {
		t.Errorf("to text = %q, want %q", to.String(), want)
	}
}

func TestDiffRefusals(t *testing.T) {
	s := &diffStub{}
	setupDiff(t, s, []string{"H"}, []string{"only"}, []string{"a", "b"})
	if _, err := runLode(t, "diff", "ZZ-REQ-7"); err == nil || !strings.Contains(err.Error(), "has one version (v1)") {
		t.Errorf("one version: err = %v", err)
	}
	s2 := &diffStub{}
	threeRuleVersions(t, s2)
	for _, args := range [][]string{
		{"diff", "ZZ-REQ-7", "--from", "2", "--to", "2"},
		{"diff", "ZZ-REQ-7", "--from", "3", "--to", "2"},
	} {
		if _, err := runLode(t, args...); err == nil || !strings.Contains(err.Error(), "ZZ-REQ-7 has v1, v2, v3") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	s3 := &diffStub{}
	threeRuleVersions(t, s3)
	_, err := runLode(t, "diff", "ZZ-12")
	if err == nil || !strings.Contains(err.Error(), "rule") || !strings.Contains(err.Error(), "spec") || !strings.Contains(err.Error(), "plan") {
		t.Errorf("task: err = %v", err)
	}
	if s3.requests != 0 {
		t.Errorf("task refusal reached the server %d times", s3.requests)
	}
}
