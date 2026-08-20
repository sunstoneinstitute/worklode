package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
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
func runDocShow(cmd *cobra.Command, ref, section, expectedKind string) error {
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

	doc, refSection, err := resolveDocRef(list.Docs, cfg.ProjectKey, ref)
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
	data := []byte(detail.Body)

	if section == "" {
		return writeDocShow(cmd, doc, "", data)
	}

	parsed, err := designdoc.Parse(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", doc.Slug, err)
	}
	text, ok := sectionSubtree(data, parsed, section)
	if !ok {
		return fmt.Errorf("no section %s in %s", section, doc.Slug)
	}
	return writeDocShow(cmd, doc, section, []byte(text))
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

// writeDocShow prints content as raw bytes, or, under --json, as
// docShowResult with content set to exactly what the non-JSON path would
// have printed.
func writeDocShow(cmd *cobra.Command, doc model.Doc, section string, content []byte) error {
	if !jsonOut(cmd) {
		cmd.OutOrStdout().Write(content)
		return nil
	}
	return printJSON(cmd, docShowResult{
		Doc: doc.ID, Slug: doc.Slug, Section: section, Content: string(content),
	})
}

// sectionSubtree returns the exact source text of the section anchored
// anchor within src, together with its whole subtree — every following
// section until one at the same or shallower Level (026 §3: a section is
// always its whole subtree).
//
// Positions come from headingLineStarts, a structural scan that finds every
// heading line by shape alone, matched to doc.Sections by index rather than
// by content. That means no step here ever needs a *particular* section's
// Anchor — not the target's neighbors, not the boundary section right after
// the subtree — which matters because anchorless H5/H6 subsections are legal
// (docs/authoring-design-docs.md) and because the literal text of a real
// anchor sometimes recurs elsewhere in the same document (a fenced example,
// prose explaining the anchor syntax): searching for anchor text directly,
// as an earlier version of this function did, could match either hazard.
func sectionSubtree(src []byte, doc *designdoc.Document, anchor string) (string, bool) {
	var target *designdoc.Section
	for _, sec := range doc.Sections {
		if sec.Anchor == anchor {
			target = sec
			break
		}
	}
	if target == nil {
		return "", false
	}
	lastIdx := target.Index
	for _, sec := range doc.Sections[target.Index+1:] {
		if sec.Level <= target.Level {
			break
		}
		lastIdx = sec.Index
	}

	starts := headingLineStarts(src)
	if len(starts) != len(doc.Sections) {
		// This scan and designdoc.Parse disagree on how many headings the
		// document has; trust neither rather than risk wrong bytes.
		return "", false
	}
	end := len(src)
	if lastIdx+1 < len(starts) {
		end = starts[lastIdx+1]
	}
	return string(src[starts[target.Index]:end]), true
}

// headingLine matches an ATX heading line at depth 2-6 by shape alone — no
// number, text or anchor capture, since headingLineStarts only needs
// positions, and doc.Sections already has the rest. H1 (a single '#') is
// excluded, mirroring designdoc's own exclusion of the document title.
var headingLine = regexp.MustCompile(`^#{2,6}[ \t]`)

// headingLineStarts returns the byte offset of the start of every heading
// line outside fenced code and outside any YAML frontmatter, in document
// order — the same set and order designdoc.Parse populates
// Document.Sections from, so headingLineStarts(src)[i] is section i's
// heading start whether or not that section (or any other one along the
// way) carries an anchor.
func headingLineStarts(src []byte) []int {
	var starts []int
	var fence string
	for pos := frontmatterEnd(src); pos <= len(src); {
		end := len(src)
		nl := bytes.IndexByte(src[pos:], '\n')
		if nl >= 0 {
			end = pos + nl
		}
		line := bytes.TrimRight(src[pos:end], "\r")
		stripped := bytes.TrimLeft(line, " \t")
		switch {
		case fence != "":
			if bytes.HasPrefix(stripped, []byte(fence)) {
				fence = ""
			}
		case bytes.HasPrefix(stripped, []byte("```")), bytes.HasPrefix(stripped, []byte("~~~")):
			fence = string(stripped[:3])
		case headingLine.Match(line):
			starts = append(starts, pos)
		}
		if nl < 0 {
			break
		}
		pos = end + 1
	}
	return starts
}

// frontmatterEnd returns the byte offset where the document body begins:
// right after the YAML frontmatter block, mirroring the delimiter check
// designdoc's own (unexported) splitFrontmatter uses — a leading "---"
// line, then the next line that is exactly "---" once trailing spaces,
// tabs, and the line ending are trimmed. No frontmatter, or an unterminated
// block, means the body is the whole input: offset 0.
func frontmatterEnd(src []byte) int {
	if !bytes.HasPrefix(src, []byte("---\n")) && !bytes.HasPrefix(src, []byte("---\r\n")) {
		return 0
	}
	for pos := bytes.IndexByte(src, '\n') + 1; pos < len(src); {
		end := len(src)
		nl := bytes.IndexByte(src[pos:], '\n')
		if nl >= 0 {
			end = pos + nl + 1
		}
		if string(bytes.TrimRight(src[pos:end], " \t\r\n")) == "---" {
			return end
		}
		if nl < 0 {
			break
		}
		pos = end
	}
	return 0
}
