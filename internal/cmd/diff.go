package cmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

type diffJSON struct {
	Ref  string       `json:"ref"`
	From int          `json:"from"`
	To   int          `json:"to"`
	Ops  []diffOpJSON `json:"ops"`
}

type diffOpJSON struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// newDiffCmd builds `lode diff`, a word-level diff between two versions of a
// rule, spec or plan.
//
// It is one of the three cross-entity readers WL-REQ-305 L7 puts at the top
// level (with `lode show` and `lode search`): it takes a reference of any of
// three kinds, so no `lode <entity> <verb>` spelling (L1) is true of it.
func newDiffCmd() *cobra.Command {
	var from, to int
	cmd := &cobra.Command{
		Use:   "diff <ref>",
		Short: "Show what changed between two versions of a rule, spec or plan",
		Long: "Word-level diff between two versions of a rule (WL-REQ-<n>, WL-RULE-<n>),\n" +
			"a spec or a plan. Deleted words show as [-old-] and inserted words as\n" +
			"{+new+}, or in red and green on a terminal.\n\n" +
			"With no flags the newest version is compared with the one before it.\n" +
			"--to picks the newer side, --from the older one; each defaults to 0,\n" +
			"which means pick for me. A rule's text is its heading as a '# ' line,\n" +
			"a blank line and its body. A document's text is its body.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: docRefAt(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			var load func(*cobra.Command, *cli.Client, string, int, int) (cli.Diff, error)
			switch classify(args[0]).Kind {
			case targetRule:
				load = loadRuleDiff
			case targetDoc:
				load = loadDocDiff
			default:
				return fmt.Errorf("lode diff takes a rule (WL-REQ-<n>, WL-RULE-<n>), a spec or a plan; %s is not one", args[0])
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			d, err := load(cmd, c, args[0], from, to)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				out := diffJSON{Ref: d.Ref, From: d.From, To: d.To, Ops: make([]diffOpJSON, 0, len(d.Ops))}
				for _, op := range d.Ops {
					out.Ops = append(out.Ops, diffOpJSON{Op: op.Kind, Text: op.Text})
				}
				return printJSON(cmd, out)
			}
			cli.DiffRender(cmd.OutOrStdout(), d)
			return nil
		},
	}
	cmd.Flags().IntVar(&from, "from", 0, "older version to compare (default: the one before --to)")
	cmd.Flags().IntVar(&to, "to", 0, "newer version to compare (default: the newest)")
	return cmd
}

func init() { rootCmd.AddCommand(newDiffCmd()) }

// pickDiffVersions chooses the pair to compare from versions, sorted
// ascending. A zero from or to means pick for me.
func pickDiffVersions(ref string, versions []int, from, to int) (int, int, error) {
	have := make([]string, len(versions))
	for i, v := range versions {
		have[i] = fmt.Sprintf("v%d", v)
	}
	exist := ref + " has " + strings.Join(have, ", ")
	if len(versions) == 0 {
		return 0, 0, fmt.Errorf("%s has no versions", ref)
	}
	if len(versions) < 2 {
		return 0, 0, fmt.Errorf("%s has one version (v%d); there is nothing to diff", ref, versions[0])
	}
	if to == 0 {
		to = versions[len(versions)-1]
	} else if !slices.Contains(versions, to) {
		return 0, 0, fmt.Errorf("--to %d is not a version; %s", to, exist)
	}
	if from == 0 {
		i := slices.Index(versions, to)
		if i == 0 {
			return 0, 0, fmt.Errorf("--to %d is the oldest version, so nothing is older; %s", to, exist)
		}
		return versions[i-1], to, nil
	}
	if !slices.Contains(versions, from) {
		return 0, 0, fmt.Errorf("--from %d is not a version; %s", from, exist)
	}
	if from >= to {
		return 0, 0, fmt.Errorf("--from %d is not older than --to %d; %s", from, to, exist)
	}
	return from, to, nil
}

func loadRuleDiff(cmd *cobra.Command, c *cli.Client, ref string, from, to int) (cli.Diff, error) {
	ctx := cmd.Context()
	list, _, err := c.ListRuleVersions(ctx, ref)
	if err != nil {
		return cli.Diff{}, err
	}
	nums := make([]int, len(list))
	for i, v := range list {
		nums[i] = v.Version
	}
	slices.Sort(nums)
	from, to, err = pickDiffVersions(ref, nums, from, to)
	if err != nil {
		return cli.Diff{}, err
	}
	a, _, err := c.GetRuleVersion(ctx, ref, from)
	if err != nil {
		return cli.Diff{}, err
	}
	b, _, err := c.GetRuleVersion(ctx, ref, to)
	if err != nil {
		return cli.Diff{}, err
	}
	text := func(heading, body string) string { return "# " + heading + "\n\n" + body }
	return cli.Diff{
		Ref: b.Ref, From: from, To: to,
		Ops: cli.WordDiff(text(string(a.Heading), string(a.Body)), text(string(b.Heading), string(b.Body))),
	}, nil
}

func loadDocDiff(cmd *cobra.Command, c *cli.Client, ref string, from, to int) (cli.Diff, error) {
	ctx := cmd.Context()
	id, err := resolveDocID(ctx, c, ref)
	if err != nil {
		return cli.Diff{}, err
	}
	list, _, err := c.ListDocVersions(ctx, id)
	if err != nil {
		return cli.Diff{}, err
	}
	nums := make([]int, len(list))
	for i, v := range list {
		nums[i] = v.Version
	}
	slices.Sort(nums)
	from, to, err = pickDiffVersions(ref, nums, from, to)
	if err != nil {
		return cli.Diff{}, err
	}
	a, _, err := c.GetDocVersion(ctx, id, from)
	if err != nil {
		return cli.Diff{}, err
	}
	b, _, err := c.GetDocVersion(ctx, id, to)
	if err != nil {
		return cli.Diff{}, err
	}
	// The header names the document as the caller typed it: the resolve
	// route's response carries no project key, so it cannot rebuild the ref.
	return cli.Diff{Ref: ref, From: from, To: to, Ops: cli.WordDiff(a.Body, b.Body)}, nil
}
