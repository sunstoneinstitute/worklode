package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

const runTestToken = "wl_0123456789abcdef0123456789abcdef01234567"

// fakeAPI is an httptest server that answers the project and task list
// routes and records every request it sees.
type fakeAPI struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []string // "METHOD path?query"
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.got = append(f.got, r.Method+" "+r.URL.RequestURI())
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+runTestToken {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects":
			io.WriteString(w, `{"projects":[{"id":"worklode","name":"Worklode","key":"WL","repos":[],"focus":[]},`+
				`{"id":"other","name":"Other","key":"OT","repos":[],"focus":[]}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tasks":
			io.WriteString(w, `{"tasks":[]}`)
		default:
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) client() *cli.Client {
	return cli.NewClient(cli.Config{ServerURL: f.srv.URL, Token: runTestToken})
}

func (f *fakeAPI) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

// unreachableConfig points every machine-side source of config somewhere a
// run must not reach: if Run read it, the command would fail to connect.
func unreachableConfig(t *testing.T) {
	t.Helper()
	t.Setenv("LODE_SERVER", "http://127.0.0.1:1")
	t.Setenv("LODE_TOKEN", "wl_ffffffffffffffffffffffffffffffffffffffff")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "must-not-be-read")
}

// captureStdout swaps os.Stdout for a pipe while fn runs and returns what was
// written to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() { os.Stdout = orig }()
	fn()
	w.Close()
	os.Stdout = orig
	return <-done
}

func TestRunUsesInjectedClient(t *testing.T) {
	unreachableConfig(t)
	f := newFakeAPI(t)

	var res RunResult
	leaked := captureStdout(t, func() {
		res = Run(context.Background(), RunOptions{
			Args:    []string{"task", "list", "--json"},
			Client:  f.client(),
			Project: "WL",
		})
	})
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("Run: code %d err %v stderr %q", res.ExitCode, res.Err, res.Stderr)
	}
	if strings.TrimSpace(string(res.Stdout)) != `{"tasks":[]}` {
		t.Errorf("stdout = %q", res.Stdout)
	}
	if leaked != "" {
		t.Errorf("Run wrote to the process stdout: %q", leaked)
	}
	reqs := f.requests()
	want := "GET /api/v1/tasks?project=worklode&"
	found := false
	for _, r := range reqs {
		if strings.HasPrefix(r, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("requests %v: want %q (the injected project resolved to its id)", reqs, want)
	}
}

func TestRunHumanOutputGoesToBuffer(t *testing.T) {
	unreachableConfig(t)
	f := newFakeAPI(t)
	leaked := captureStdout(t, func() {
		res := Run(context.Background(), RunOptions{Args: []string{"--help"}, Client: f.client()})
		if res.ExitCode != 0 || !strings.Contains(string(res.Stdout), "Available Commands:") {
			t.Errorf("help: code %d stdout %q", res.ExitCode, res.Stdout)
		}
	})
	if leaked != "" {
		t.Errorf("Run wrote to the process stdout: %q", leaked)
	}
}

func TestRunProjectFlag(t *testing.T) {
	unreachableConfig(t)
	cases := []struct {
		name     string
		project  string
		args     []string
		mismatch bool
	}{
		{"same project by key", "WL", []string{"task", "list", "--json", "--project", "WL"}, false},
		{"same project by id", "WL", []string{"task", "list", "--json", "--project", "worklode"}, false},
		{"same project by name", "worklode", []string{"task", "list", "--json", "--project", "Worklode"}, false},
		{"another project", "WL", []string{"task", "list", "--json", "--project", "other"}, true},
		{"every project", "WL", []string{"task", "list", "--json", "--project="}, true},
		{"flag without a handle", "", []string{"task", "list", "--json", "--project", "worklode"}, true},
		{"project show of another", "WL", []string{"project", "show", "--project", "OT"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t)
			res := Run(context.Background(), RunOptions{Args: tc.args, Client: f.client(), Project: tc.project})
			if got := errors.Is(res.Err, ErrProjectMismatch); got != tc.mismatch {
				t.Fatalf("errors.Is(%v, ErrProjectMismatch) = %v, want %v", res.Err, got, tc.mismatch)
			}
			if tc.mismatch {
				var pm *ProjectMismatchError
				if !errors.As(res.Err, &pm) || pm.Requested != strings.TrimPrefix(tc.args[len(tc.args)-1], "--project=") {
					t.Errorf("error %#v does not name the requested project", res.Err)
				}
				if res.ExitCode != 1 {
					t.Errorf("exit code %d, want 1", res.ExitCode)
				}
				for _, r := range f.requests() {
					if strings.HasPrefix(r, "GET /api/v1/tasks") {
						t.Errorf("a refused run still listed tasks: %s", r)
					}
				}
			}
		})
	}
}

func TestRunProjectRequired(t *testing.T) {
	unreachableConfig(t)
	f := newFakeAPI(t)
	res := Run(context.Background(), RunOptions{
		Args:   []string{"task", "add", "--title", "x"},
		Client: f.client(),
	})
	if !errors.Is(res.Err, ErrProjectRequired) {
		t.Fatalf("err = %v, want ErrProjectRequired", res.Err)
	}
	if res.ExitCode != 1 {
		t.Errorf("exit code %d, want 1", res.ExitCode)
	}
	for _, r := range f.requests() {
		if strings.HasPrefix(r, "POST ") {
			t.Errorf("a run with no project still wrote: %s", r)
		}
	}
}

func TestRunExitCodes(t *testing.T) {
	unreachableConfig(t)
	f := newFakeAPI(t)
	ok := Run(context.Background(), RunOptions{Args: []string{"task", "list", "--json"}, Client: f.client()})
	if ok.ExitCode != 0 || ok.Err != nil {
		t.Errorf("success: code %d err %v", ok.ExitCode, ok.Err)
	}
	bad := Run(context.Background(), RunOptions{Args: []string{"task", "bogus"}, Client: f.client()})
	if bad.ExitCode != 1 || bad.Err == nil {
		t.Errorf("unknown command: code %d err %v", bad.ExitCode, bad.Err)
	}
	if !strings.Contains(string(bad.Stderr), bad.Err.Error()) {
		t.Errorf("stderr %q does not carry the error %q", bad.Stderr, bad.Err)
	}
	none := Run(context.Background(), RunOptions{Args: []string{"task", "list"}})
	if none.ExitCode != 1 || none.Err == nil {
		t.Errorf("no client: code %d err %v", none.ExitCode, none.Err)
	}
}

func TestRunRefusesMachineState(t *testing.T) {
	unreachableConfig(t)
	f := newFakeAPI(t)
	for _, args := range [][]string{{"login"}, {"logout"}, {"project", "resolve", "--refresh"}} {
		res := Run(context.Background(), RunOptions{Args: args, Client: f.client(), Project: "WL"})
		if args[0] == "project" {
			// resolve reports the caller's project, never the server's checkout.
			if res.Err != nil || !strings.Contains(string(res.Stdout), "worklode") {
				t.Errorf("project resolve: err %v stdout %q", res.Err, res.Stdout)
			}
			continue
		}
		if res.Err == nil {
			t.Errorf("lode %s ran in process", strings.Join(args, " "))
		}
	}
}

func TestInjectedRunIsNotInteractive(t *testing.T) {
	ctx := context.WithValue(context.Background(), injectedKey{}, &injectedEnv{})
	cmd := &cobra.Command{Use: "x"}
	cmd.SetContext(ctx)

	orig := pagerFn
	t.Cleanup(func() { pagerFn = orig })
	pagerFn = func(bool) (io.Writer, func()) {
		t.Error("an in-process run asked for a pager")
		return nil, func() {}
	}
	withPager(cmd, true)()

	if v := processEnv(cmd, "PATH"); v != "" {
		t.Errorf("processEnv read the server's environment: %q", v)
	}
	if _, err := workingDir(ctx); !errors.Is(err, errNoWorkingDir) {
		t.Errorf("workingDir in process = %v, want errNoWorkingDir", err)
	}
	if currentTaskID(ctx) != "" || currentSessionID(ctx) != "" {
		t.Error("provenance read from the server's working directory")
	}
}

func TestRunReadsNoServerFile(t *testing.T) {
	unreachableConfig(t)
	f := newFakeAPI(t)
	res := Run(context.Background(), RunOptions{
		Args:    []string{"task", "add", "--title", "x", "--body-file", "/etc/hostname"},
		Client:  f.client(),
		Project: "WL",
	})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "reads only stdin") {
		t.Fatalf("err = %v, want the in-process file refusal", res.Err)
	}
	body, err := readFileOrStdin("-", inProcessStdin{strings.NewReader("from stdin")})
	if err != nil || string(body) != "from stdin" {
		t.Errorf("stdin read = %q, %v", body, err)
	}
}
