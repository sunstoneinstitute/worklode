package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/gate"
	"github.com/sunstoneinstitute/worklode/internal/gitexec"
)

// newGateCmd is the design authority gate (11-design-authority-gate.md): the
// check CI runs on a pull request that touches a guarded path.
func newGateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gate",
		Short: "The design authority gate",
	}
	cmd.AddCommand(newGateCheckCmd())
	return cmd
}

func newGateCheckCmd() *cobra.Command {
	var base, head, bodyFile string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Refuse a change to a guarded path that names no design clause",
		Long: `Reads the [gate] table of .worklode/config.toml, diffs --base to --head,
and requires a Spec: trailer on the pull request body (--body-file) or in a
commit message when a guarded path changed. Exit status 1 with the reason
when the trailer is missing or malformed. Without a [gate] table it does
nothing. The trailer is checked for form here; the server resolves the
clause it names (12-spec-refactoring-design-tree.md S50).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			out, err := runGateCheck(dir, base, head, bodyFile)
			if out != "" {
				fmt.Fprint(cmd.OutOrStdout(), out)
			}
			if err != nil {
				cmd.SilenceUsage = true
			}
			return err
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "base revision of the change (the pull request's base sha)")
	cmd.Flags().StringVar(&head, "head", "HEAD", "head revision of the change")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "file holding the pull request body")
	_ = cmd.MarkFlagRequired("base")
	return cmd
}

// runGateCheck is the command without cobra: the repo root is found from
// dir, the config read, the diff and the messages taken from git. The
// returned text is the verdict line for the caller to print; a non-nil error
// is the refusal.
func runGateCheck(dir, base, head, bodyFile string) (string, error) {
	root, ok := gitexec.Line(dir, "rev-parse", "--show-toplevel")
	if !ok {
		return "", errors.New("gate: not inside a git repository")
	}
	cfg, enabled, err := gate.Load(root)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	if !enabled {
		return "gate: no [gate] table in .worklode/config.toml, nothing to check\n", nil
	}
	if head == "" {
		head = "HEAD"
	}
	// --no-renames: with rename detection on, moving a file out of a guarded
	// path prints only the destination and the gate never sees the departure.
	diff, err := gitexec.Text(root, "diff", "--name-only", "--no-renames", base+"..."+head)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	var in gate.Input
	for _, p := range strings.Split(diff, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			in.Changed = append(in.Changed, p)
		}
	}
	if bodyFile != "" {
		body, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", fmt.Errorf("gate: %w", err)
		}
		in.Texts = append(in.Texts, string(body))
	}
	// One NUL after each message keeps a multi-paragraph body whole; newest
	// first so the final commit's trailer wins when several carry one.
	log, err := gitexec.Text(root, "log", "--format=%B%x00", base+".."+head)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	for _, m := range strings.Split(log, "\x00") {
		if m = strings.TrimSpace(m); m != "" {
			in.Texts = append(in.Texts, m)
		}
	}
	v, err := gate.Check(cfg, in)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	if len(v.Guarded) == 0 {
		return "gate: no guarded path changed\n", nil
	}
	return fmt.Sprintf("gate: %s covers %s\n", cfg.Trailer+" "+v.Declaration.String(), strings.Join(v.Guarded, ", ")), nil
}

func init() {
	rootCmd.AddCommand(newGateCmd())
}
