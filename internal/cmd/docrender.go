package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// normalizeSection turns a --section value into a canonical anchor. It drops
// an optional leading '#' and expands the shortcut form — a bare section
// number like "3" or "4.1a" becomes "sec-3" / "sec-4.1a", so `--section 3` is
// an alias for `--section sec-3`. A value already in "sec-..." form, or empty,
// is returned unchanged.
func normalizeSection(s string) string {
	s = strings.TrimPrefix(s, "#")
	if s == "" || strings.HasPrefix(s, "sec-") {
		return s
	}
	return "sec-" + s
}

// runDocShow renders a SPEC or ADR document, or one of its sections,
// cat-style (spec 026 §3). It backs every SPEC/ADR path through `lode show`
// (show.go): the typed-id dispatch (expectedKind "", since resolveDocRef's
// own <KEY>-<TYPE>-<n> shorthand form already kind-checks), and the
// --spec/--adr/--kind flags via runDocShowByOrdinal (expectedKind "SPEC" or
// "ADR").
//
// Documents come from the backbone, not from disk: the project scope is
// resolved the usual way (config, else the git remote), GET /api/v1/docs
// supplies the candidates resolveDocRef matches the ref against, and GET
// /api/v1/docs/{id} supplies the body — list responses carry none. So this
// needs a reachable server, and works in a checkout with no documents on
// disk at all.
//
// expectedKind, when non-empty, is verified with checkDocKind, the same check
// the shorthand form runs — not a second implementation of the mismatch rule.
// It exists because the other resolution forms (a bare number, notably — the
// fallback runDocShowByOrdinal uses when the project key is unknown) never
// run it themselves: a flag's kind must still be enforced when there is no
// shorthand to route it through.
func runDocShow(cmd *cobra.Command, ref, section, expectedKind string, inline bool) error {
	section = normalizeSection(section)

	c, cfg, err := newAPIClientWithConfig()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	scope := currentScope(ctx, c, cfg)

	list, _, err := c.ListDocs(ctx, cli.DocListFilter{Project: scope.Project})
	if err != nil {
		return err
	}

	doc, refSection, err := resolveDocRefTiers(ctx, c, list.Docs, cfg.ProjectKey, ref)
	if err != nil {
		var unresolved *designdoc.UnresolvedError
		if errors.As(err, &unresolved) {
			// 026 §4.2 tier 3: printed, exit code unaffected.
			return writeUnresolved(cmd, err)
		}
		return err
	}

	if expectedKind != "" {
		if err := checkDocKind(doc, expectedKind); err != nil {
			return err
		}
	}

	if refSection != "" && section != "" && refSection != section {
		return fmt.Errorf("ref %q carries #%s but --section %s was passed; they disagree", ref, refSection, section)
	}
	if section == "" {
		section = refSection
	}

	detail, _, err := c.GetDoc(ctx, doc.ID)
	if err != nil {
		return err
	}

	if inline {
		// The consolidated view (WL-84): every effective claim folded into
		// the section it acts on, transitively; --section narrows to one
		// subtree with its folds intact.
		inliner := newDocInliner(func(id int64) (*model.DocDetail, error) {
			d, _, err := c.GetDoc(ctx, id)
			if err != nil {
				return nil, err
			}
			return &d, nil
		})
		out, err := inliner.consolidateDoc(&detail, section)
		if err != nil {
			return err
		}
		return writeDocShow(cmd, doc, section, []byte(out))
	}

	data := []byte(detail.Body)

	if section == "" {
		return writeDocShow(cmd, doc, "", data)
	}

	parsed, err := designdoc.Parse(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", doc.Slug, err)
	}
	// 026 §3: a section is always its whole subtree. designdoc cuts it from
	// the spans Parse already found, so nothing here re-scans the source.
	text, ok := parsed.Subtree(section)
	if !ok {
		return fmt.Errorf("no section %s in %s", section, doc.Slug)
	}
	return writeDocShow(cmd, doc, section, []byte(text))
}

// resolveDocRefTiers resolves ref through 026 §4.2's tiers 1 and 2: the pure
// grammar against docs with this checkout's own project key, then — when the
// key names another project, or this checkout declares no project_key at all —
// against the docs of the project the backbone says owns that key. It returns
// *designdoc.UnresolvedError only for tier 3, a key no registered project
// carries, so every ref-taking command gets the same answer for the same ref.
//
// A ref carrying no key — a slug, a path, a number form — has no tier 2 of
// its own, and used to stop at the current project's documents while `lode
// doc show` resolved the same string org-wide (WL-358: `lode doc show
// 001-zero-trust-gateway` worked from a worklode checkout and `lode doc todo`
// on the same string did not). It now falls through to the backbone's own
// resolver — the endpoint `lode doc <verb>` already calls — so the four doc
// surfaces accept one grammar over one corpus. Only a not-found falls
// through: an ambiguity or a kind mismatch is an answer about this ref, and a
// wider search would mask it rather than improve it.
func resolveDocRefTiers(ctx context.Context, c *cli.Client, docs []model.Doc, projectKey, ref string) (model.Doc, string, error) {
	doc, section, err := resolveDocRef(docs, projectKey, ref)
	if err == nil {
		return doc, section, nil
	}
	var unresolved *designdoc.UnresolvedError
	if errors.As(err, &unresolved) {
		// Tier 2, live since 025 landed (WL-276): the caller already reached
		// the backbone for docs, so ask it whose key this is.
		return resolveForeignDocRef(ctx, c, unresolved.Key, ref)
	}
	var notFound *designdoc.NotFoundError
	if !errors.As(err, &notFound) {
		return model.Doc{}, "", err
	}
	base, section := designdoc.SplitFragment(ref)
	if d, rerr := c.ResolveDoc(ctx, base); rerr == nil {
		return d, section, nil
	}
	return model.Doc{}, "", err
}

// resolveForeignDocRef is 026 §4.2's tier 2: resolve a shorthand whose key
// is not the current checkout's against the backbone's own knowledge of the
// org. The project whose key it is supplies the candidate docs, and the ref
// then resolves through the same pure grammar as a local one — kind check
// included. A key no project carries returns *designdoc.UnresolvedError
// (tier 3); a known key whose document is missing is a defect and errors
// (the §4.2 table's "the key is known and the document is not").
func resolveForeignDocRef(ctx context.Context, c *cli.Client, key, ref string) (model.Doc, string, error) {
	projects, _, err := c.ListProjects(ctx)
	if err != nil {
		return model.Doc{}, "", fmt.Errorf("resolve project key %s: %w", key, err)
	}
	for _, p := range projects.Projects {
		if p.Key != key {
			continue
		}
		list, _, err := c.ListDocs(ctx, cli.DocListFilter{Project: p.ID})
		if err != nil {
			return model.Doc{}, "", fmt.Errorf("list %s docs: %w", p.ID, err)
		}
		return resolveDocRef(list.Docs, key, ref)
	}
	return model.Doc{}, "", &designdoc.UnresolvedError{Key: key}
}

// unresolvedResult is the --json shape of a tier-3 UnresolvedError.
type unresolvedResult struct {
	Unresolved string `json:"unresolved"`
}

// docShowResult is the --json shape of `lode show` for a spec or ADR (026
// §3). Doc and Slug identify the rendered document; they replaced a "path"
// field that named the corpus file, which no longer exists now that documents
// are read from the backbone. 026 does not pin this shape, and nothing
// machine-readable consumed "path".
type docShowResult struct {
	Doc     int64  `json:"doc"`
	Slug    string `json:"slug"`
	Section string `json:"section"`
	Content string `json:"content"`
}

// writeUnresolved prints a tier-3 UnresolvedError: the bare message on
// stdout normally, or, under --json, {"unresolved": "<message>"} alone (no
// path/section/content — there is no document to report them for). Either
// way this returns nil: 026 §4.2 tier 3 is printed, exit code unaffected.
func writeUnresolved(cmd *cobra.Command, err error) error {
	if !jsonOut(cmd) {
		fmt.Fprintln(cmd.OutOrStdout(), err.Error())
		return nil
	}
	return printJSON(cmd, unresolvedResult{Unresolved: err.Error()})
}

// writeDocShow prints content through cli.Markdown (raw off-TTY, ANSI-styled
// on a terminal — including through --pager, since its writer reports the
// real terminal's fd), or, under --json, as docShowResult with content set to
// exactly what the non-JSON path would have printed.
func writeDocShow(cmd *cobra.Command, doc model.Doc, section string, content []byte) error {
	if !jsonOut(cmd) {
		cli.Markdown(cmd.OutOrStdout(), string(content))
		return nil
	}
	return printJSON(cmd, docShowResult{
		Doc: doc.ID, Slug: doc.Slug, Section: section, Content: string(content),
	})
}
