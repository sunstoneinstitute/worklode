// lode doctor: client-side setup diagnosis (spec 013). Runs entirely
// locally, needs no privileges, and stays useful with the server
// unreachable. Each failing check names its fix; any failure exits non-zero
// so hooks and CI can gate on it.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/githooks"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// doctorCheck is one pass/fail line of the report. Fix is set only on
// failure. Skipped checks (e.g. the worktree check outside a worktree, or a
// server-side check with the server unreachable) count as neither.
type doctorCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail"`
	Fix     string `json:"fix,omitempty"`
}

func pass(name, detail string) doctorCheck { return doctorCheck{Name: name, OK: true, Detail: detail} }
func fail(name, detail, fix string) doctorCheck {
	return doctorCheck{Name: name, Detail: detail, Fix: fix}
}
func skip(name, detail string) doctorCheck {
	return doctorCheck{Name: name, OK: true, Skipped: true, Detail: detail}
}

// doctorReport is the --json form of `lode doctor`'s whole run. Named
// deliberately, not an anonymous struct literal at the marshal call site:
// this package's ADR 036 rule (internal/model/rule_test.go's modeNamed for
// internal/cmd) flags anonymous json-tagged struct literals even though
// named ones are fine for a --json stdout contract that crosses no HTTP
// boundary (see CLAUDE.md's Architecture section).
type doctorReport struct {
	OK     bool          `json:"ok"`
	Checks []doctorCheck `json:"checks"`
}

// runDoctorChecks runs the spec's six checks in order from dir. Later checks
// still run when earlier ones fail, degrading to skips where they cannot be
// evaluated, so one run reports everything wrong at once.
func runDoctorChecks(ctx context.Context, dir string) []doctorCheck {
	var checks []doctorCheck

	// 1. Config file found — which one, and where the walk-up located it.
	userPath, userFound, repoPath, repoFound := cli.ConfigOrigins(dir)
	switch {
	case repoFound:
		checks = append(checks, pass("config", "repo config "+repoPath))
	case userFound:
		checks = append(checks, pass("config", "user config "+userPath))
	default:
		checks = append(checks, fail("config",
			"no config file found (looked for a repo-local .worklode/.lode config above "+dir+" and "+userPath+")",
			"run `lode login <server-url>` or create "+userPath+" with server = \"https://...\""))
	}

	cfg, cfgErr := cli.LoadConfig()
	if cfgErr != nil {
		checks = append(checks, fail("config-load", cfgErr.Error(), "fix the config file reported above"))
		return checks
	}

	// 2. server set and reachable / 3. token present and accepted — one
	// whoami round trip answers both: a transport error is "unreachable", a
	// 401 is "token rejected", 200 is both green.
	var c *cli.Client
	serverReachable := false
	switch {
	case cfg.ServerURL == "":
		checks = append(checks, fail("server", "server URL not set",
			"set LODE_SERVER or add server = \"https://...\" to the config file"))
	default:
		c = cli.NewClient(cfg)
		who, _, whoErr := c.WhoAmI(ctx)
		var ce *cli.ClientError
		switch {
		case whoErr == nil:
			serverReachable = true
			checks = append(checks, pass("server", cfg.ServerURL+" reachable"))
			if cfg.Token == "" {
				// 200 with no token cannot happen; guard anyway.
				checks = append(checks, fail("token", "no token configured", "run `lode login`"))
			} else {
				checks = append(checks, pass("token", "accepted; you are "+who.ID+" ("+who.Kind+")"))
			}
		case errors.As(whoErr, &ce):
			serverReachable = true
			checks = append(checks, pass("server", cfg.ServerURL+" reachable"))
			if cfg.Token == "" {
				checks = append(checks, fail("token",
					"no token in the OS keychain or LODE_TOKEN", "run `lode login`"))
			} else {
				checks = append(checks, fail("token",
					fmt.Sprintf("server rejected the token (%d)", ce.Status), "run `lode login` to mint a fresh token"))
			}
		default:
			checks = append(checks, fail("server", cfg.ServerURL+" unreachable: "+whoErr.Error(),
				"check the server URL and your network; set LODE_SERVER to override"))
			checks = append(checks, skip("token", "not checked (server unreachable)"))
		}
	}

	// 4. current_project set, and the project exists.
	switch {
	case cfg.CurrentProject == "":
		checks = append(checks, fail("current_project", "not set",
			"add current_project = \"<project-id>\" to .worklode/config.toml (or the user config)"))
	case !serverReachable:
		checks = append(checks, skip("current_project", cfg.CurrentProject+" (existence not checked: server unreachable)"))
	default:
		if _, err := c.GetProject(ctx, cfg.CurrentProject); err != nil {
			checks = append(checks, fail("current_project",
				"project "+cfg.CurrentProject+" not found on the server",
				"fix current_project in the config, or create the project with `lode project add`"))
		} else {
			checks = append(checks, pass("current_project", cfg.CurrentProject))
		}
	}

	// 5. Git hooks installed in this repo.
	switch hooksDir, installed, err := githooks.Installed(dir); {
	case err != nil:
		checks = append(checks, skip("hooks", "not in a git repository"))
	case installed:
		checks = append(checks, pass("hooks", "pre-commit installed in "+hooksDir))
	default:
		checks = append(checks, fail("hooks", "worklode pre-commit hook not installed",
			"run `lode install` in this repo"))
	}

	// 6. Inside a task worktree: does it map to a task with a live lease.
	// layoutFrom/worktree.Root/Layout.TaskID is this repo's established way
	// to resolve "am I in a task worktree, and which task" (see
	// resolveWorktreeTask in lifecycle.go) — it is used here directly rather
	// than via resolveWorktreeTask because that helper returns an error for
	// "not in a worktree", where doctor wants a skip, not a failure.
	root, inRepo := worktree.Root(dir)
	taskID, isTaskWT := "", false
	if inRepo {
		if l, err := layoutFrom(root); err == nil {
			taskID, isTaskWT = l.TaskID(root)
		}
	}
	switch {
	case !isTaskWT:
		checks = append(checks, skip("worktree", "not inside a task worktree"))
	case !serverReachable:
		checks = append(checks, skip("worktree", taskID+" (lease not checked: server unreachable)"))
	default:
		detail, _, err := c.GetTask(ctx, taskID)
		switch {
		case err != nil:
			checks = append(checks, fail("worktree", "worktree names task "+taskID+", which the server does not know",
				"remove the stale worktree, or create/claim the task"))
		case detail.Lease == nil:
			checks = append(checks, fail("worktree", "task "+taskID+" has no live lease",
				"run `lode claim "+taskID+"` from this worktree"))
		default:
			checks = append(checks, pass("worktree", taskID+" leased until "+detail.Lease.ExpiresAt.Format("2006-01-02 15:04")))
		}
	}

	return checks
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose this machine's lode setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			checks := runDoctorChecks(cmd.Context(), dir)

			failed := 0
			for _, c := range checks {
				if !c.OK {
					failed++
				}
			}
			if jsonOut(cmd) {
				b, err := json.MarshalIndent(doctorReport{OK: failed == 0, Checks: checks}, "", "  ")
				if err != nil {
					return err
				}
				cmd.Println(string(b))
			} else {
				for _, c := range checks {
					mark := "ok  "
					switch {
					case c.Skipped:
						mark = "skip"
					case !c.OK:
						mark = "FAIL"
					}
					cmd.Printf("%s  %-16s %s\n", mark, c.Name, c.Detail)
					if c.Fix != "" {
						cmd.Printf("      fix: %s\n", c.Fix)
					}
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d check(s) failed", failed)
			}
			return nil
		},
	}
}

func init() {
	rootCmd.AddCommand(newDoctorCmd())
}
