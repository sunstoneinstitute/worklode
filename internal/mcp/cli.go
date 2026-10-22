package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// The lode tool runs one command of the lode CLI in process (WL-REQ-1862).
// Its description and schema stay short: a client loads them every turn, and
// `--help` teaches the commands on demand.
const (
	lodeToolDesc = "Run a lode CLI command on the Worklode server and return what it prints. " +
		"args are the shell words after `lode`; use --help to learn the commands. " +
		"stdin feeds file flags given `-`; project is the handle from project_use."
	lodeToolSchema = `{"type":"object","properties":{` +
		`"args":{"type":"array","items":{"type":"string"}},` +
		`"stdin":{"type":"string"},` +
		`"project":{"type":"string"}},"required":["args"]}`
)

type lodeArgs struct {
	Args    []string `json:"args"`
	Stdin   string   `json:"stdin"`
	Project string   `json:"project"`
}

func (s *server) addCLITools() {
	s.addTool("lode", lodeToolDesc, lodeToolSchema, s.lode)
}

// lode runs the command with the caller's in-process client, in the project
// of the call's handle. Its result is exempt from the other tools' shape: the
// content is the command's stdout, its stderr and its exit code, in that
// order, and a non-zero exit is an error result. Project failures answer the
// same confirm_project and project_required answers as the other tools.
// Every call is also counted by command path (WL-REQ-1865).
func (s *server) lode(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (_ *mcp.CallToolResult, err error) {
	command, outcome := "unknown", outcomeError
	defer func() { s.cfg.Metrics.cliCommand(command, outcome) }()

	a, err := argsOf[lodeArgs](req)
	if err != nil {
		outcome, _ = classify(err)
		return nil, err
	}
	if s.cfg.CLI.Run == nil {
		return nil, errors.New("The lode tool is not available on this server.")
	}
	if s.cfg.CLI.Command != nil {
		command = s.cfg.CLI.Command(a.Args)
	}

	key := ""
	if a.Project != "" {
		if key, err = s.requireProject(ctx, c, a.Project, ""); err != nil {
			outcome, _ = classify(err)
			return nil, err
		}
	}

	stdout, stderr, code, runErr := s.cfg.CLI.Run(ctx, a.Args, strings.NewReader(a.Stdin), c, key)
	res := &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: string(stdout)},
		&mcp.TextContent{Text: string(stderr)},
		&mcp.TextContent{Text: fmt.Sprintf("exit code: %d", code)},
	}}
	if code == 0 && runErr == nil {
		outcome = outcomeOK
		return res, nil
	}
	err = s.cliError(ctx, c, command, key, runErr, res)
	outcome, _ = classify(err)
	return nil, err
}

// cliError maps a failed run to its outcome. The project failures answer
// as the other tools do (WL-REQ-1829); every other failure answers with the
// command's own output, res.
func (s *server) cliError(ctx context.Context, c *cli.Client, command, key string, runErr error, res *mcp.CallToolResult) error {
	var (
		pm  *cli.ProjectMismatchError
		lce *cli.LocalCommandError
		cre *cli.ConfirmationRequiredError
		ce  *cli.ClientError
	)
	switch {
	case errors.Is(runErr, cli.ErrProjectRequired):
		ps, err := listProjects(ctx, c)
		if err != nil {
			return err
		}
		return confirmError(outcomeProjectRequired,
			fmt.Sprintf("lode %s needs a Worklode project confirmed with project_use first; pass its handle as project.", command),
			projectCandidates(ps))
	case errors.As(runErr, &pm), errors.Is(runErr, cli.ErrProjectNotFound):
		ps, err := listProjects(ctx, c)
		if err != nil {
			return err
		}
		requested := key
		if pm != nil {
			requested = pm.Requested
		}
		cands := projectCandidates(ps)
		if requested != "" {
			cands = matchProjects(requested, ps)
		}
		return confirmError(outcomeConfirmProject,
			runErr.Error()+". The command acts in the project of its handle; confirm another project with project_use and pass its handle.",
			cands)
	case errors.As(runErr, &lce), errors.As(runErr, &cre):
		return &toolError{outcome: outcomeRefused, msg: runErr.Error(), res: res}
	case errors.Is(runErr, cli.ErrLocalPath):
		return &toolError{outcome: outcomeInvalid, msg: runErr.Error(), res: res}
	case errors.As(runErr, &ce):
		return &toolError{outcome: statusOutcome(ce.Status), msg: runErr.Error(), res: res}
	}
	msg := "command failed"
	if runErr != nil {
		msg = runErr.Error()
	}
	return &toolError{outcome: outcomeError, msg: msg, res: res}
}
