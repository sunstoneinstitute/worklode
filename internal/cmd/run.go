package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// RunOptions is one in-process run of the command tree (WL-REQ-1862). The
// caller supplies everything the binary would otherwise take from this
// machine: the API client stands in for config files, LODE_* variables and
// the keychain, and Project stands in for current_project and the git remote.
type RunOptions struct {
	// Args are the command-line arguments without the program name.
	Args []string
	// Stdin is what the command reads as standard input. Nil reads as empty.
	Stdin io.Reader
	// Client calls the API. Required.
	Client *cli.Client
	// Project names the run's current project by key, id or name. "" means
	// none: a command that needs a project fails with ErrProjectRequired.
	Project string
}

// RunResult is what an in-process run produced.
type RunResult struct {
	Stdout, Stderr []byte
	// ExitCode is the status the binary would have exited with.
	ExitCode int
	// Err is the error the command returned, nil on success.
	Err error
}

// ErrProjectRequired matches (errors.Is) the error of a command that needs a
// project when none is set.
var ErrProjectRequired = errors.New("no project")

// ErrProjectMismatch matches (errors.Is) the error of an in-process run whose
// --project or --repo names a project other than the run's own.
var ErrProjectMismatch = errors.New("project mismatch")

// ProjectMismatchError is the ErrProjectMismatch a run returns. Requested is
// the flag's value; Current is the run's project, "" when it has none.
type ProjectMismatchError struct {
	Current, Requested string
}

func (e *ProjectMismatchError) Error() string {
	if e.Current == "" {
		return fmt.Sprintf("--project %s: this run has no current project, and a flag cannot choose one", e.Requested)
	}
	return fmt.Sprintf("--project %s names a different project than the current one, %s", e.Requested, e.Current)
}

func (e *ProjectMismatchError) Is(target error) bool { return target == ErrProjectMismatch }

// projectRequiredError keeps a command's own wording while matching
// ErrProjectRequired.
type projectRequiredError struct{ msg string }

func (e projectRequiredError) Error() string        { return e.msg }
func (e projectRequiredError) Is(target error) bool { return target == ErrProjectRequired }

// Run executes one command in process with the environment o injects, and
// never reads this machine's config, LODE_* variables, keychain, working
// directory or terminal. Output goes to the result's buffers.
func Run(ctx context.Context, o RunOptions) RunResult {
	if o.Client == nil {
		err := errors.New("cmd.Run: no API client")
		return RunResult{ExitCode: 1, Err: err, Stderr: []byte(err.Error() + "\n")}
	}
	args := o.Args
	if args == nil {
		// cobra reads os.Args when no args were set.
		args = []string{}
	}
	stdin := o.Stdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	var stdout, stderr bytes.Buffer
	root := newRootCmd()
	root.SetArgs(args)
	root.SetIn(inProcessStdin{stdin})
	root.SetOut(&stdout)
	root.SetErr(&stderr)

	env := &injectedEnv{client: o.Client.WithoutLocalCache(), project: o.Project}
	err := root.ExecuteContext(context.WithValue(ctx, injectedKey{}, env))
	res := RunResult{Err: err}
	if err != nil {
		code, ok := ExitCode(err)
		if !ok {
			code = 1
			fmt.Fprintln(&stderr, err)
		}
		res.ExitCode = code
	}
	res.Stdout, res.Stderr = stdout.Bytes(), stderr.Bytes()
	return res
}

// inProcessStdin marks an in-process run's stdin, so readFileOrStdin, which
// sees only the reader, can refuse a path on the server's disk.
type inProcessStdin struct{ io.Reader }

type injectedKey struct{}

// injectedEnv is the environment an in-process run carries on its context.
type injectedEnv struct {
	client  *cli.Client
	project string

	once     sync.Once
	resolved model.Project
	err      error
}

// injected returns the run's injected environment, or nil when cmd belongs to
// the binary (or is nil).
func injected(cmd *cobra.Command) *injectedEnv {
	if cmd == nil {
		return nil
	}
	return injectedFromContext(cmd.Context())
}

func injectedFromContext(ctx context.Context) *injectedEnv {
	if ctx == nil {
		return nil
	}
	env, _ := ctx.Value(injectedKey{}).(*injectedEnv)
	return env
}

// projectFor resolves the run's project through the API once.
func (e *injectedEnv) projectFor(ctx context.Context) (model.Project, error) {
	e.once.Do(func() {
		if e.project == "" {
			return
		}
		resp, _, err := e.client.ListProjects(ctx)
		if err != nil {
			e.err = fmt.Errorf("resolve project %s: %w", e.project, err)
			return
		}
		p, ok := matchProject(e.project, resp.Projects)
		if !ok {
			e.err = fmt.Errorf("project not found: %s", e.project)
			return
		}
		e.resolved = p
	})
	return e.resolved, e.err
}

// config is the cli.Config an in-process run stands in for the machine's:
// the client's server and the run's project.
func (e *injectedEnv) config(ctx context.Context) (cli.Config, error) {
	p, err := e.projectFor(ctx)
	if err != nil {
		return cli.Config{}, err
	}
	return cli.Config{ServerURL: e.client.ServerURL(), CurrentProject: p.ID, ProjectKey: p.Key}, nil
}

// scope is the run's current scope: its project, or none. Nothing is
// discovered from a working directory or git remote.
func (e *injectedEnv) scope(ctx context.Context) cli.Scope {
	p, err := e.projectFor(ctx)
	if err != nil || p.ID == "" {
		return cli.Scope{Source: cli.ScopeNone}
	}
	return cli.Scope{Project: p.ID, Key: p.Key, Source: cli.ScopeInjected}
}

// checkFlag refuses a --project/--repo that names a project other than the
// run's. requested is the flag's value as typed; p, when non-nil, is the
// project the flag resolved to.
func (e *injectedEnv) checkFlag(ctx context.Context, requested string, p *model.Project) (cli.Scope, error) {
	cur, err := e.projectFor(ctx)
	if err != nil {
		return cli.Scope{}, err
	}
	mismatch := &ProjectMismatchError{Current: e.project, Requested: requested}
	if cur.ID == "" {
		return cli.Scope{}, mismatch
	}
	same := false
	if p != nil {
		same = p.ID == cur.ID
	} else {
		_, same = matchProject(requested, []model.Project{cur})
	}
	if !same {
		return cli.Scope{}, mismatch
	}
	return cli.Scope{Project: cur.ID, Key: cur.Key, Source: cli.ScopeFlag}, nil
}

// matchProject finds the project ref names, by id, key or name. The id
// matches exactly; key and name ignore case.
func matchProject(ref string, ps []model.Project) (model.Project, bool) {
	for _, p := range ps {
		if p.ID == ref {
			return p, true
		}
	}
	for _, p := range ps {
		if strings.EqualFold(p.Key, ref) || strings.EqualFold(p.Name, ref) {
			return p, true
		}
	}
	return model.Project{}, false
}

// loadConfig is cli.LoadConfig, or the injected config in an in-process run.
func loadConfig(cmd *cobra.Command) (cli.Config, error) {
	if env := injected(cmd); env != nil {
		return env.config(cmd.Context())
	}
	return cli.LoadConfig()
}

// processEnv reads an environment variable of the process running the
// command. In-process runs share the server's environment, which belongs to
// no caller, so they read nothing.
func processEnv(cmd *cobra.Command, name string) string {
	if injected(cmd) != nil {
		return ""
	}
	return os.Getenv(name)
}

// refuseInProcess is the error a command that acts on this machine (its
// keychain, browser or checkout) returns in an in-process run, and nil in the
// binary.
func refuseInProcess(cmd *cobra.Command) error {
	if injected(cmd) == nil {
		return nil
	}
	return fmt.Errorf("lode %s acts on the machine it runs on and cannot run in process", cmd.Name())
}
