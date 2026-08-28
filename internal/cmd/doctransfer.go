package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// docTransferResult is `lode doc transfer --json`'s per-document contract.
// It crosses no HTTP boundary — it is assembled here from N responses to
// Task 3's owner endpoint — so it is named and declared in internal/cmd
// rather than internal/model (ADR 036 §2's modeNamed carve-out for `--json`
// stdout contracts; see internal/model/rule_test.go).
type docTransferResult struct {
	Doc   model.Doc `json:"doc"`
	Error string    `json:"error,omitempty"`
}

// newDocTransferCmd is `lode doc transfer`: POST /api/v1/docs/{id}/owner
// (025 §7.3, added by an earlier task in this series) exposed as a command,
// plus the case the whole feature exists for — reassigning every document a
// departed actor owns — as a client-side loop over the `--owner` list filter
// (also an earlier task). There is no bulk transfer endpoint: the owner
// endpoint's no-op-on-same-owner rule is what makes looping it safe to just
// run again after a partial failure.
//
// Positional refs and --from are mutually exclusive and exactly one is
// required. cobra's MarkFlagsMutuallyExclusive only sees flags, so it cannot
// express "these positional args and that flag disagree" — the Args
// validator below carries that check instead. It runs before RunE (as flag
// parsing does), so both-or-neither is refused before any document moves.
func newDocTransferCmd() *cobra.Command {
	var scope scopeFlags
	var to, from string
	cmd := &cobra.Command{
		Use:   "transfer [ref...] --to <actor>",
		Short: "Transfer document ownership to another actor",
		Long: "Transfer ownership of one or more documents (025 §7.3).\n\n" +
			"Name documents by ref:\n" +
			"  lode doc transfer WL-SPEC-25 --to ada\n\n" +
			"Or move everything one actor owns — the rescue for a departed owner:\n" +
			"  lode doc transfer --from bob --to ada\n\n" +
			"--from is project-scoped like `lode doc list`: the current repo's project\n" +
			"by default, --project= for every project. Transferring a document to its\n" +
			"current owner is a no-op, so a run interrupted partway can simply be\n" +
			"repeated.",
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case from == "" && len(args) == 0:
				return errors.New("lode doc transfer needs document refs, or --from <actor>; pass one or the other")
			case from != "" && len(args) > 0:
				return errors.New("document refs and --from both name what to transfer; pass only one")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}

			var docs []model.Doc
			if from != "" {
				sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
				if err != nil {
					return err
				}
				resp, _, err := c.ListDocs(cmd.Context(), cli.DocListFilter{Project: sc.Project, Owner: from})
				if err != nil {
					return err
				}
				docs = resp.Docs
				if !confirmDocTransfer(cmd, docs, from, to) {
					fmt.Fprintln(cmd.OutOrStdout(), "aborted: no documents transferred")
					return nil
				}
			} else {
				docs = make([]model.Doc, 0, len(args))
				for _, ref := range args {
					id, err := resolveDocID(cmd.Context(), c, ref)
					if err != nil {
						return err
					}
					d, _, err := c.GetDoc(cmd.Context(), id)
					if err != nil {
						return err
					}
					docs = append(docs, d.Doc)
				}
			}

			outcomes := c.TransferDocs(cmd.Context(), docs, to)
			failed := 0
			for _, o := range outcomes {
				if o.Err != "" {
					failed++
				}
			}
			if jsonOut(cmd) {
				results := make([]docTransferResult, len(outcomes))
				for i, o := range outcomes {
					results[i] = docTransferResult{Doc: o.Doc, Error: o.Err}
				}
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(results); err != nil {
					return err
				}
			} else {
				cli.DocTransferTable(cmd.OutOrStdout(), outcomes)
			}
			// Honest partial failure: the table (or JSON) above already named
			// which documents moved and which did not, so this only needs to
			// carry the non-zero exit — a silent partial success is the
			// outcome this whole command exists to avoid.
			if failed > 0 {
				return fmt.Errorf("%d of %d document transfer(s) failed", failed, len(outcomes))
			}
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "project id (with --from)")
	cmd.Flags().StringVar(&to, "to", "", "actor id to receive ownership (required)")
	cmd.Flags().StringVar(&from, "from", "", "transfer every document this actor owns, instead of naming refs")
	cmd.MarkFlagRequired("to")
	return cmd
}

// confirmDocTransfer prints what a --from transfer is about to move and, on a
// real terminal, asks before doing it. Non-interactive stdin (an agent, CI)
// or --json proceeds without asking, following the term.IsTerminal pattern
// consentToSecrets uses (internal/cmd/secretsceremony.go:166) — there is no
// shared confirm helper in this CLI.
func confirmDocTransfer(cmd *cobra.Command, docs []model.Doc, from, to string) bool {
	errw := cmd.ErrOrStderr()
	if len(docs) == 0 {
		fmt.Fprintf(errw, "%s owns no documents in scope; nothing to transfer\n", from)
		return true
	}
	fmt.Fprintf(errw, "This will transfer %d document(s) from %s to %s:\n", len(docs), from, to)
	for _, d := range docs {
		fmt.Fprintf(errw, "  %s  %s\n", cli.DocRef(d), d.Title)
	}
	f, isFile := cmd.InOrStdin().(*os.File)
	if jsonOut(cmd) || (isFile && !term.IsTerminal(int(f.Fd()))) {
		return true
	}
	fmt.Fprint(errw, "Proceed? [y/N] ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil {
		return false // EOF at a real prompt is an answer: no
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
