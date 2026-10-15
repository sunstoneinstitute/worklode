package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"

	"github.com/sunstoneinstitute/worklode/internal/gate"
)

// newGateCmd is the design authority gate (WL-REQ-8): the
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
		Short: "Refuse a change to a guarded path that names no design rule",
		Long: `Reads the [gate] table of .worklode/config.toml, diffs --base to --head,
and requires a Spec: trailer on the pull request body (--body-file) or in a
commit message when a guarded path changed. The trailer names a rule
(Spec: WL-REQ-<n>, optionally followed by amended or a reason word) or
says Spec: none <reason>. A section value (WL-SPEC-<n> sec-N) is refused,
naming the rule it resolves to when a server is configured, else the
lode show <doc>#sec-N command that reads it. The pull request body and the
lines the diff adds must cite rules by ref, never by spec section; paths the
[gate] exempt list names are skipped. Exit status 1 with the reason when the
trailer is missing or malformed or a section citation is found. Without a
[gate] table it does nothing. The server resolves the rule a well-formed
trailer names.`,
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

// runGateCheck is the command without cobra, asking the server when one is
// configured.
func runGateCheck(dir, base, head, bodyFile string) (string, error) {
	return gate.Run(dir, base, head, bodyFile, resolveSectionRule, verdictDeclaration)
}

// verdictDeclaration prints a cited rule with the infix of its current kind
// (WL-REQ-1642) when the server answers, and with the trailer's own spelling
// when it does not (as in CI, WL-REQ-8). The qualifier follows either way.
func verdictDeclaration(d gate.Declaration) string {
	if d.Rule == nil {
		return d.String()
	}
	c, err := newAPIClient()
	if err != nil {
		return d.Spelled()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, _, err := c.GetRule(ctx, strings.Fields(d.Line)[1])
	if err != nil {
		return d.Spelled()
	}
	ref := designdoc.FormatRuleRef(d.Rule.Key, d.Rule.Number, r.Kind)
	if d.Qualifier != "" {
		ref += " " + d.Qualifier
	}
	return ref
}

// resolveSectionRule names the rule a section resolves to at the
// document's current version, or "" when no server is configured (as in CI,
// which makes no Worklode API call per WL-REQ-8), the call fails within the
// hook timeout of WL-REQ-1722, or the anchor arranges no single rule.
func resolveSectionRule(sec designdoc.SectionRef) string {
	c, err := newAPIClient()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return c.SectionRule(ctx, sec)
}

func init() {
	rootCmd.AddCommand(newGateCmd())
}
