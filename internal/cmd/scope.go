package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// scopeFlags holds the values of the --project/--repo pair a command
// registers with addScopeFlags.
type scopeFlags struct {
	project string
	repo    string
}

// addScopeFlags registers the --project/--repo pair on cmd. projectHelp
// describes what the project narrows ("filter by project id", "project id").
func addScopeFlags(cmd *cobra.Command, f *scopeFlags, projectHelp string) {
	cmd.Flags().StringVar(&f.project, "project", "",
		projectHelp+" (default: the current repo's project — from current_project in config, else the git remote); pass --project= for all projects")
	cmd.Flags().StringVar(&f.repo, "repo", "",
		"name the project by one of its repos, as owner/name (alternative to --project)")
}

// resolveScope returns the project scope a command should act on: an explicit
// --project/--repo when passed, otherwise the config/git-remote chain in
// cli.ResolveScope. An explicitly empty --project= means "every project" and
// stops the chain.
func resolveScope(ctx context.Context, cmd *cobra.Command, c *cli.Client, cfg cli.Config, f *scopeFlags) (cli.Scope, error) {
	projectSet := cmd.Flags().Changed("project")
	repoSet := cmd.Flags().Changed("repo")

	if projectSet && repoSet {
		return cli.Scope{}, errors.New("--project and --repo name the same thing; pass only one")
	}
	if repoSet {
		p, err := c.ResolveRemote(ctx, f.repo)
		if err != nil {
			return cli.Scope{}, fmt.Errorf("resolve --repo %s: %w", f.repo, err)
		}
		return cli.Scope{Project: p.ID, Key: p.Key, Source: cli.ScopeFlag}, nil
	}
	if projectSet {
		return cli.Scope{Project: f.project, Source: cli.ScopeFlag}, nil
	}

	return currentScope(ctx, c, cfg), nil
}

// errNoProject is what every project-scoped create command returns when the
// resolution chain came up empty.
var errNoProject = errors.New(`no project: pass --project or --repo, set current_project in .worklode/config.toml or ~/.config/worklode/config.toml, or map this repo with "lode project repo add"`)

// bareTaskNumber matches a task number without its project key, as accepted
// by every id-taking command.
var bareTaskNumber = regexp.MustCompile(`^[0-9]+$`)

// resolveTaskID expands a bare task number ("12") to a full task id ("WL-12")
// using the current scope's project key. Anything else — including a full
// id from another project — is returned untouched.
func resolveTaskID(ctx context.Context, arg string, c *cli.Client, cfg cli.Config) (string, error) {
	if !bareTaskNumber.MatchString(arg) {
		return arg, nil
	}
	return resolveTaskIDInScope(ctx, arg, c, currentScope(ctx, c, cfg))
}

// resolveTaskIDPair is resolveTaskID for the two ends of an edge command,
// resolving the current scope at most once — and not at all when neither
// argument is a bare number.
func resolveTaskIDPair(ctx context.Context, a, b string, c *cli.Client, cfg cli.Config) (string, string, error) {
	if !bareTaskNumber.MatchString(a) && !bareTaskNumber.MatchString(b) {
		return a, b, nil
	}
	scope := currentScope(ctx, c, cfg)
	ra, err := resolveTaskIDInScope(ctx, a, c, scope)
	if err != nil {
		return "", "", err
	}
	rb, err := resolveTaskIDInScope(ctx, b, c, scope)
	if err != nil {
		return "", "", err
	}
	return ra, rb, nil
}

// workingDir is os.Getwd with one error wording for the whole package.
func workingDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine working directory: %w", err)
	}
	return wd, nil
}

// currentScope resolves the project the working directory belongs to. An
// unreadable working directory is not fatal — the config/git-remote chain
// simply has one fewer input.
func currentScope(ctx context.Context, c *cli.Client, cfg cli.Config) cli.Scope {
	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}
	return cli.ResolveScope(ctx, c, cfg, wd)
}

// resolveTaskIDInScope is resolveTaskID against an already-resolved scope, for
// commands that take both an id and --project/--repo: the flag must decide
// which project a bare number belongs to.
func resolveTaskIDInScope(ctx context.Context, arg string, c *cli.Client, scope cli.Scope) (string, error) {
	if !bareTaskNumber.MatchString(arg) {
		return arg, nil
	}
	key := scope.Key
	if key == "" {
		key = cli.ProjectKey(ctx, c, scope.Project)
	}
	if key == "" {
		if scope.Project == "" {
			return "", fmt.Errorf("%s is a task number, not a task id, and no current project is set:\npass a full id like WL-%s, or set current_project", arg, arg)
		}
		return "", fmt.Errorf("%s is a task number, and no task-id key could be looked up for project %s:\ncheck that the project exists and that the server is reachable", arg, scope.Project)
	}
	return key + "-" + arg, nil
}
