package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// newRuleCmd is the design-rule entity group (12-spec-refactoring-design-tree.md
// S14, S35). `lode rule show` and `lode show WL-RULE-<n>` read one rule.
func newRuleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule",
		Short: "Design rules: show or list them, edit one, list its versions, link or unlink it to another",
	}
	cmd.AddCommand(newRuleShowCmd(), newRuleListCmd(), newRuleEditCmd(), newRuleVersionsCmd(), newRuleLinkCmd(), newRuleUnlinkCmd(), newRuleSetCmd(), newRuleSupersedeCmd())
	return cmd
}

func init() {
	rootCmd.AddCommand(newRuleCmd())
}

func newRuleShowCmd() *cobra.Command {
	var version int
	cmd := &cobra.Command{
		Use:               "show <ref>",
		ValidArgsFunction: ruleRefAt(0),
		Short:             "Show a rule: its status, version, placements and text",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRuleShow(cmd, args[0], version, cmd.Flags().Changed("version"))
		},
	}
	cmd.Flags().IntVar(&version, "version", 0, "show one version of the rule")
	return cmd
}

func newRuleListCmd() *cobra.Command {
	var scope scopeFlags
	var doc, status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List rules, or one document's rules in arrangement order",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			// --doc already names its project, so the default scope would
			// only hide a document from another project.
			var project string
			if doc == "" || cmd.Flags().Changed("project") || cmd.Flags().Changed("repo") {
				sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
				if err != nil {
					return err
				}
				project = sc.Project
			}
			rules, raw, err := c.ListRules(cmd.Context(), cli.RuleListFilter{
				Project: project, Doc: doc, Status: status,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.RulesTable(cmd.OutOrStdout(), rules)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "filter by project id")
	cmd.Flags().StringVar(&doc, "doc", "", "only the rules a document arranges, in order, e.g. WL-SPEC-73")
	cmd.Flags().StringVar(&status, "status", "", "filter by status: draft, accepted, superseded, withdrawn")
	cmd.RegisterFlagCompletionFunc("doc", docRefAt(0))
	completeFlagValues(cmd, "status", []string{"draft", "accepted", "superseded", "withdrawn"})
	return cmd
}

func newRuleEditCmd() *cobra.Command {
	var file, heading string
	cmd := &cobra.Command{
		Use:   "edit <ref>",
		Short: "Replace a rule's body (and heading) from a file; the document is regenerated around it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := readBodyFile(cmd, file)
			if err != nil {
				return err
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			// h resolves locally rather than reassigning the closure-captured
			// heading flag variable: RunE can run more than once in a
			// process (tests), and mutating heading would leak the first
			// run's resolved value into the second.
			h := heading
			if h == "" {
				current, _, err := c.GetRule(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				h = current.Heading
			}
			rule, raw, err := c.EditRule(cmd.Context(), args[0], model.EditRuleInput{Heading: strings.TrimSpace(h), Body: body})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.RuleRender(cmd.OutOrStdout(), rule)
			// An edit of an accepted document goes to its candidate
			// revision, so the rule read back is still the old text and
			// the render above looks like nothing happened (S13, S35). Say
			// where the change is waiting.
			if rule.Status == "accepted" && strings.TrimSpace(rule.Body) != strings.TrimSpace(body) && len(rule.ArrangedIn) == 1 {
				ref := rule.ArrangedIn[0].DocRef
				fmt.Fprintf(cmd.OutOrStdout(), "\nSaved to the candidate revision of %s; it lands when you run lode doc revise %s --accept.\n", ref, ref)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", `file holding the new body, the text under the heading ("-" for stdin) (required)`)
	cmd.Flags().StringVar(&heading, "heading", "", "new heading text; unchanged when omitted")
	return cmd
}

// edgeTypeFlagTypes maps each edge-type flag to its wl: type, walked in this
// order by edgeTypeFlags's resolver.
var edgeTypeFlagTypes = []struct{ flag, typ string }{
	{"refines", "refines"},
	{"constrains", "constrains"},
	{"conflicts-with", "conflictsWith"},
	{"references", "references"},
	{"derived-from", "wasDerivedFrom"},
}

// edgeTypeFlags binds the five edge-type flags and returns a resolver that
// yields the wl: type and target of whichever one the caller set.
// MarkFlagsOneRequired and MarkFlagsMutuallyExclusive guarantee exactly one
// is set before RunE runs, so the resolver itself cannot fail. It checks
// Changed rather than a non-empty value, so `--refines ""` still resolves to
// "refines" instead of silently falling through to another type.
func edgeTypeFlags(cmd *cobra.Command) func() (typ, target string) {
	vals := map[string]*string{}
	for _, e := range edgeTypeFlagTypes {
		v := cmd.Flags().String(e.flag, "", edgeFlagUsage[e.flag])
		vals[e.flag] = v
	}
	cmd.MarkFlagsOneRequired("refines", "constrains", "conflicts-with", "references", "derived-from")
	cmd.MarkFlagsMutuallyExclusive("refines", "constrains", "conflicts-with", "references", "derived-from")
	return func() (string, string) {
		for _, e := range edgeTypeFlagTypes {
			if cmd.Flags().Changed(e.flag) {
				return e.typ, *vals[e.flag]
			}
		}
		return "", ""
	}
}

// edgeFlagUsage holds each edge-type flag's help text, keyed the same as
// edgeTypeFlagTypes.
var edgeFlagUsage = map[string]string{
	"refines":        "rule this one narrows or details",
	"constrains":     "rule this one must hold alongside",
	"conflicts-with": "rule this one is in recorded tension with",
	"references":     "rule this one points at (a derived edge is written for you when the text names it)",
	"derived-from":   "rule this one was derived from, e.g. by a split (supersededBy is written by lode rule supersede, not this flag)",
}

func newRuleLinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link <ref>",
		Short: "Relate a rule to another: --refines, --constrains, --conflicts-with, --references or --derived-from <ref>",
		Args:  cobra.ExactArgs(1),
	}
	typ := edgeTypeFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		c, err := newAPIClient()
		if err != nil {
			return err
		}
		t, to := typ()
		raw, err := c.LinkRules(cmd.Context(), args[0], model.RuleEdgeInput{Type: t, To: to})
		if err != nil {
			return err
		}
		if jsonOut(cmd) {
			printRaw(cmd, raw)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s\n", args[0], t, to)
		return nil
	}
	return cmd
}

func newRuleUnlinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unlink <ref>",
		Short: "Remove a relation written with rule link",
		Args:  cobra.ExactArgs(1),
	}
	typ := edgeTypeFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		c, err := newAPIClient()
		if err != nil {
			return err
		}
		t, to := typ()
		raw, err := c.UnlinkRules(cmd.Context(), args[0], model.RuleEdgeInput{Type: t, To: to})
		if err != nil {
			return err
		}
		if jsonOut(cmd) {
			printRaw(cmd, raw)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s no longer %s %s\n", args[0], t, to)
		return nil
	}
	return cmd
}

// newRuleSetCmd is `lode rule set`: owner and tags (S15). Each field
// takes its own positional shape (owner one actor, tags any number), so the
// field is a subcommand rather than a leading argument the way `project set`
// groups its fields (WL-489).
func newRuleSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set a rule's owner or tags",
	}
	cmd.AddCommand(newRuleSetOwnerCmd(), newRuleSetTagsCmd())
	return cmd
}

// newRuleSetOwnerCmd is `lode rule set owner <ref> <actor>`. "" clears
// the owner. "-" is deliberately not a second spelling for that: everywhere
// else in this tree "-" means "read from stdin" (task.go, doc.go), and
// reusing it here for "clear" would give it two contradictory meanings.
func newRuleSetOwnerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "owner <ref> <actor>",
		Short: `Set a rule's owner; "" clears it`,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			owner := args[1]
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			rule, raw, err := c.SetRuleMeta(cmd.Context(), args[0], model.RuleMetaInput{Owner: &owner})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.RuleRender(cmd.OutOrStdout(), rule)
			return nil
		},
	}
}

// newRuleSetTagsCmd is `lode rule set tags <ref> [tag ...]`. Naming no
// tags clears the list: Tags is always a present, non-nil pointer to a
// possibly-empty slice, so an omitted list still replaces whatever was set
// rather than being read as "leave it alone".
func newRuleSetTagsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tags <ref> [tag ...]",
		Short: "Replace a rule's tags; naming none clears them",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tags := append([]string{}, args[1:]...)
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			rule, raw, err := c.SetRuleMeta(cmd.Context(), args[0], model.RuleMetaInput{Tags: &tags})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.RuleRender(cmd.OutOrStdout(), rule)
			return nil
		},
	}
}

func newRuleVersionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "versions <ref>",
		Short: "List a rule's versions, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			vs, raw, err := c.ListRuleVersions(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.RuleVersionsTable(cmd.OutOrStdout(), vs)
			return nil
		},
	}
}

// newRuleSupersedeCmd is `lode rule supersede --map <file>` (S24): the
// refactor primitive. The map decides what applying it changes; this command
// only reads and parses it and hands the entries to the store through the
// API, the way every other write in this package defers the decision to the
// server.
func newRuleSupersedeCmd() *cobra.Command {
	var mapFile string
	var dryRun bool
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:   "supersede",
		Short: "Apply a refactor map: withdraw old rules and link each to its successors (S24)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if mapFile == "" {
				return errors.New(`no map: pass --map <file>, or "-" to read it from stdin`)
			}
			content, err := resolveBody("", mapFile, cmd.InOrStdin())
			if err != nil {
				return err
			}
			entries, err := parseSupersedeMap(content)
			if err != nil {
				return err
			}
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			if sc.Project == "" {
				return errNoProject
			}
			res, raw, err := c.SupersedeRules(cmd.Context(), sc.Project, model.SupersedeInput{Entries: entries, DryRun: dryRun})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.SupersedeRender(cmd.OutOrStdout(), res)
			return nil
		},
	}
	cmd.Flags().StringVar(&mapFile, "map", "", `file holding the refactor map, one entry per line ("-" for stdin) (required)`)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve and report what would change, without writing")
	addScopeFlags(cmd, &scope, "project id")
	return cmd
}

// parseSupersedeMap parses a refactor map: one entry per line, "<old> ->
// <new> [<new> ...]", or "<old> ->" to withdraw with no successor. A line
// whose first non-blank character is "#" is a comment (a ref may itself
// carry a "#" fragment, so only a leading "#" is treated as one); blank
// lines are ignored. A line with no "->" is an error naming its 1-based line
// number.
func parseSupersedeMap(content string) ([]model.SupersedeEntry, error) {
	var entries []model.SupersedeEntry
	for i, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		old, rest, ok := strings.Cut(line, "->")
		if !ok {
			return nil, fmt.Errorf("line %d: no \"->\": %q", i+1, raw)
		}
		old = strings.TrimSpace(old)
		if old == "" {
			return nil, fmt.Errorf("line %d: nothing before \"->\": %q", i+1, raw)
		}
		news := strings.Fields(rest)
		if len(news) == 0 {
			news = nil
		}
		entries = append(entries, model.SupersedeEntry{Old: old, New: news})
	}
	return entries, nil
}
