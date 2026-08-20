package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// Agent-facing markdown hardcodes `lode ...` invocations. Renaming a command or
// a flag silently rots every one of them, and an agent following rotted
// instructions fails in a way no other test catches. This walks the cobra tree
// and checks each invocation still resolves.
//
// docs/agent-surfaces.md is the register of what is checked and what to do when
// this fails, including the downstream surfaces this test cannot see.

// surfaceFiles returns every agent-facing markdown file, relative to repoRoot.
func surfaceFiles(t *testing.T, repoRoot string) []string {
	t.Helper()

	var files []string
	for _, rel := range []string{"CLAUDE.md", "internal/cmd/CLAUDE.md", "docs/agent-surfaces.md"} {
		if _, err := os.Stat(filepath.Join(repoRoot, rel)); err == nil {
			files = append(files, rel)
		}
	}
	for _, tree := range []string{".claude/skills", "plugins"} {
		root := filepath.Join(repoRoot, tree)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".md") {
				rel, relErr := filepath.Rel(repoRoot, path)
				if relErr != nil {
					return relErr
				}
				files = append(files, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", tree, err)
		}
	}
	return files
}

// exemptions reads the opt-out list. An entry is one normalized invocation
// ("lode doc list --needs-planning"), exempted everywhere it appears.
func exemptions(t *testing.T) map[string]bool {
	t.Helper()

	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(packageDir, "testdata", "agent-surface-exempt.txt"))
	if os.IsNotExist(err) {
		return out
	}
	if err != nil {
		t.Fatalf("reading exemption list: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}

type invocation struct {
	file string
	line int
	text string
}

var codeSpan = regexp.MustCompile("`([^`\n]+)`")

// findInvocations pulls every `lode ...` out of one markdown file: inline code
// spans, and command lines inside fenced blocks (whose backslash continuations
// are joined first, so flags on the second line are checked too).
func findInvocations(file string, body string) []invocation {
	var found []invocation
	inFence := false
	lines := strings.Split(body, "\n")

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		lineNo := i + 1

		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}

		if inFence {
			cmd := strings.TrimSpace(line)
			cmd = strings.TrimPrefix(cmd, "$ ")
			for strings.HasSuffix(cmd, "\\") && i+1 < len(lines) {
				i++
				cmd = strings.TrimSpace(strings.TrimSuffix(cmd, "\\")) + " " + strings.TrimSpace(lines[i])
			}
			if isInvocation(cmd) {
				found = append(found, invocation{file, lineNo, cmd})
			}
			continue
		}

		for _, m := range codeSpan.FindAllStringSubmatch(line, -1) {
			if span := strings.TrimSpace(m[1]); isInvocation(span) {
				found = append(found, invocation{file, lineNo, span})
			}
		}
	}
	return found
}

func isInvocation(s string) bool {
	return s == "lode" || strings.HasPrefix(s, "lode ")
}

// tokenize splits a shell-ish command on whitespace, keeping quoted runs whole
// so a --flag mentioned inside a quoted argument is not read as a flag.
func tokenize(s string) []string {
	var tokens []string
	var cur strings.Builder
	var quote rune

	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// resolve walks tokens down the command tree, returning the deepest command
// reached and the tokens left over.
func resolve(root *cobra.Command, tokens []string) (*cobra.Command, []string) {
	cur := root
	i := 0
	for ; i < len(tokens); i++ {
		t := tokens[i]
		if strings.HasPrefix(t, "-") {
			break
		}
		sub := findSub(cur, t)
		if sub == nil {
			break
		}
		cur = sub
	}
	return cur, tokens[i:]
}

func findSub(c *cobra.Command, name string) *cobra.Command {
	for _, sub := range c.Commands() {
		if sub.Name() == name {
			return sub
		}
		for _, alias := range sub.Aliases {
			if alias == name {
				return sub
			}
		}
	}
	return nil
}

func lookupFlag(c *cobra.Command, name string) *pflag.Flag {
	c.InitDefaultHelpFlag()
	c.InitDefaultVersionFlag()
	for _, set := range []*pflag.FlagSet{c.Flags(), c.PersistentFlags(), c.InheritedFlags()} {
		if f := set.Lookup(name); f != nil {
			return f
		}
	}
	return nil
}

// placeholder reports whether a token is documentation filler (<id>, NAME, …)
// rather than something the CLI would see.
func placeholder(s string) bool {
	return strings.ContainsAny(s, "<>[]…")
}

// checkInvocation returns a reason the invocation does not resolve, or "".
func checkInvocation(root *cobra.Command, text string) string {
	tokens := tokenize(text)[1:] // drop "lode"
	cmd, rest := resolve(root, tokens)

	if cmd == root && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		return fmt.Sprintf("%q is not a lode command", rest[0])
	}
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") && !placeholder(rest[0]) &&
		!cmd.Runnable() && cmd.HasSubCommands() {
		return fmt.Sprintf("%q is not a subcommand of %q", rest[0], cmd.CommandPath())
	}

	for i, t := range rest {
		if t == "--" {
			break // everything after is the wrapped command's own argv
		}
		name, ok := flagName(t)
		if !ok {
			continue
		}
		flag := lookupFlag(cmd, name)
		if flag == nil {
			return fmt.Sprintf("%q has no flag --%s", cmd.CommandPath(), name)
		}
		if reason := checkFlagValue(cmd, flag, name, flagValue(rest, i)); reason != "" {
			return reason
		}
	}
	return ""
}

// flagValue returns the value written for the flag at rest[i], from either
// --flag=value or the token after it. It is "" when none was written — a
// boolean flag, or a value the author left off.
func flagValue(rest []string, i int) string {
	if _, v, found := strings.Cut(rest[i], "="); found {
		return v
	}
	if i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "-") {
		return rest[i+1]
	}
	return ""
}

// checkFlagValue rejects a value outside the closed set a flag's usage string
// names. Only --kind is checked: it is the flag agent docs get wrong (a task
// kind is not a document kind, and "spec" is neither), and the two enums
// differ per command, which a hardcoded table here would get wrong in the same
// way the docs did.
func checkFlagValue(cmd *cobra.Command, flag *pflag.Flag, name, value string) string {
	if name != "kind" || value == "" || placeholder(value) {
		return ""
	}
	allowed := enumValues(flag)
	if len(allowed) == 0 || slices.Contains(allowed, value) {
		return ""
	}
	return fmt.Sprintf("%q --kind takes %s, not %q",
		cmd.CommandPath(), strings.Join(allowed, ", "), value)
}

// usageEnum matches the value list a flag's usage string spells out, as in
// "document kind: spec, adr, or plan (required)". The optional "or" covers the
// prose form; a trailing parenthetical falls outside the match.
var usageEnum = regexp.MustCompile(`: ([a-z_]+(?:, (?:or )?[a-z_]+)+)`)

// enumValues returns the closed set a flag's usage names, or nil when it names
// none — which means the value is unchecked, not that anything goes.
func enumValues(flag *pflag.Flag) []string {
	m := usageEnum.FindAllStringSubmatch(flag.Usage, -1)
	if len(m) == 0 {
		return nil
	}
	values := strings.Split(m[len(m)-1][1], ", ")
	for i, v := range values {
		values[i] = strings.TrimPrefix(v, "or ")
	}
	return values
}

// flagName extracts the long-flag name from a token. Shorthands are skipped
// rather than resolved: agent docs spell flags out in full, and a one-letter
// token is as likely to be a placeholder as a flag.
func flagName(token string) (string, bool) {
	if !strings.HasPrefix(token, "--") || token == "--" {
		return "", false
	}
	name, _, _ := strings.Cut(strings.TrimPrefix(token, "--"), "=")
	if name == "" || placeholder(name) {
		return "", false
	}
	return name, true
}

func TestAgentSurfaces(t *testing.T) {
	// packageDir, not the working directory: TestMain moves the process out of
	// internal/cmd before any test runs.
	repoRoot := filepath.Join(packageDir, "..", "..")
	exempt := exemptions(t)

	var failures []string
	for _, rel := range surfaceFiles(t, repoRoot) {
		body, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		for _, inv := range findInvocations(rel, string(body)) {
			if exempt[inv.text] {
				continue
			}
			if reason := checkInvocation(rootCmd, inv.text); reason != "" {
				failures = append(failures,
					fmt.Sprintf("%s:%d: %s\n\t%s", inv.file, inv.line, reason, inv.text))
			}
		}
	}

	if len(failures) > 0 {
		t.Errorf("agent-facing docs name CLI surface that no longer exists:\n\n%s\n\n"+
			"Fix the docs, or add the invocation to internal/cmd/testdata/agent-surface-exempt.txt\n"+
			"with a comment saying why. See docs/agent-surfaces.md — and note that it lists\n"+
			"downstream surfaces this test cannot see, in particular worklode-onboarding in\n"+
			"sunstoneinstitute/claude-plugins.",
			strings.Join(failures, "\n"))
	}
}

func TestCheckInvocation(t *testing.T) {
	cases := []struct {
		text     string
		wantFail bool
	}{
		{"lode", false},
		{"lode next --json", false},
		{"lode task add --title \"use --force here\" --kind bug", false},
		{"lode secrets exec -- <command> [args...]", false},
		{"lode task block <part-N-id> --by <part-N-1-id>", false},
		{"lode doc", false},
		{"lode task tree", false},

		{"lode doc list --kind spec", false},
		{"lode doc new --kind plan --slug s --file f", false}, // "spec, adr, or plan"
		{"lode task add --kind design", false},
		{"lode task add --kind <the kind>", false},
		{"lode task list --parent WL-1", false},

		{"lode tsak add", true},
		// The bug that motivated the value check: "spec" is a document kind,
		// not a task kind (claude-plugins#133).
		{"lode task add --kind spec", true},
		{"lode task add --kind=spec", true},
		{"lode doc list --kind design", true},
		{"lode task treee", true},
		{"lode next --jsonn", true},
		{"lode task add --titel x", true},
	}
	for _, c := range cases {
		reason := checkInvocation(rootCmd, c.text)
		if (reason != "") != c.wantFail {
			t.Errorf("%q: got %q, wantFail=%v", c.text, reason, c.wantFail)
		}
	}
}

func TestFindInvocations(t *testing.T) {
	body := "Run `lode next` first.\n" +
		"```bash\n" +
		"$ lode task add --title \"x\" \\\n" +
		"    --kind bug\n" +
		"```\n" +
		"Prose saying lode next should not match.\n"

	got := findInvocations("f.md", body)
	if len(got) != 2 {
		t.Fatalf("got %d invocations, want 2: %+v", len(got), got)
	}
	if got[0].text != "lode next" {
		t.Errorf("span: got %q", got[0].text)
	}
	if got[1].text != `lode task add --title "x" --kind bug` {
		t.Errorf("continuation not joined: got %q", got[1].text)
	}
}

// TestKindEnumMatchesNS pins enumValues' parse of the task --kind usage string
// to ns.TaskKinds, the generated source of truth (025 §17). Without this the
// value check would quietly follow a usage string that had itself drifted.
func TestKindEnumMatchesNS(t *testing.T) {
	cmd, _ := resolve(rootCmd, []string{"task", "add"})
	got := slices.Clone(enumValues(lookupFlag(cmd, "kind")))
	slices.Sort(got)

	if !slices.Equal(got, ns.TaskKinds) {
		t.Errorf("`lode task add --kind` usage names %v, ns.TaskKinds is %v", got, ns.TaskKinds)
	}
}
