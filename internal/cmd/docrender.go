package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// runDocShow renders a SPEC or ADR document, or one of its sections,
// cat-style (spec 026 §3). It backs every SPEC/ADR path through `lode show`
// (show.go): the typed-id dispatch (expectedKind "", since ResolveRef's own
// <KEY>-<TYPE>-<n> shorthand form already runs designdoc.CheckKind), and the
// --spec/--adr/--kind flags via runDocShowByOrdinal (expectedKind "SPEC" or
// "ADR"). It needs no server: the corpus is read straight off disk, so it
// works with no LODE_SERVER set.
//
// expectedKind, when non-empty, is independently verified against the
// resolved document's frontmatter with designdoc.CheckKind — the same check
// ResolveRef's shorthand form runs internally, so this is not a second
// implementation of the mismatch rule. It exists because ResolveRef's other
// resolution forms (a bare number, notably — the fallback runDocShowByOrdinal
// uses when the project key is unknown) never run that check themselves: a
// flag's kind must still be enforced even when there is no local shorthand
// to route it through.
func runDocShow(cmd *cobra.Command, ref, section, expectedKind string) error {
	section = strings.TrimPrefix(section, "#")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	corpus := designdoc.FindCorpus(cwd)
	if corpus == "" {
		return errors.New("not inside a worklode repo (no .worklode directory found)")
	}

	cfg, err := cli.LoadConfig()
	if err != nil {
		return err
	}

	resolved, err := designdoc.ResolveRef(corpus, cfg.ProjectKey, ref)
	if err != nil {
		var unresolved *designdoc.UnresolvedError
		if errors.As(err, &unresolved) {
			// 026 §4.2 tier 3: printed, exit code unaffected.
			return writeUnresolved(cmd, err)
		}
		return err
	}

	if expectedKind != "" {
		if err := designdoc.CheckKind(resolved.Path, expectedKind); err != nil {
			return err
		}
	}

	if resolved.Section != "" && section != "" && resolved.Section != section {
		return fmt.Errorf("ref %q carries #%s but --section %s was passed; they disagree", ref, resolved.Section, section)
	}
	if section == "" {
		section = resolved.Section
	}

	if section == "" {
		data, err := os.ReadFile(resolved.Path)
		if err != nil {
			return err
		}
		return writeDocShow(cmd, resolved.Path, "", data)
	}

	data, err := os.ReadFile(resolved.Path)
	if err != nil {
		return err
	}
	doc, err := designdoc.Parse(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", resolved.Path, err)
	}
	text, ok := sectionSubtree(data, doc, section)
	if !ok {
		return fmt.Errorf("no section %s in %s", section, resolved.Path)
	}
	return writeDocShow(cmd, resolved.Path, section, []byte(text))
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
	b, jerr := json.Marshal(struct {
		Unresolved string `json:"unresolved"`
	}{Unresolved: err.Error()})
	if jerr != nil {
		return fmt.Errorf("encode result: %w", jerr)
	}
	printRaw(cmd, b)
	return nil
}

// writeDocShow prints content as raw bytes, or as the {"path", "section",
// "content"} JSON shape 026 §3 wants for --json, with content set to exactly
// what the non-JSON path would have printed.
func writeDocShow(cmd *cobra.Command, path, section string, content []byte) error {
	if !jsonOut(cmd) {
		cmd.OutOrStdout().Write(content)
		return nil
	}
	b, err := json.Marshal(struct {
		Path    string `json:"path"`
		Section string `json:"section"`
		Content string `json:"content"`
	}{Path: path, Section: section, Content: string(content)})
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	printRaw(cmd, b)
	return nil
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
