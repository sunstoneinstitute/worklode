package designdoc

import (
	"slices"
	"strings"
	"testing"
)

func TestMechanicalFindings(t *testing.T) {
	cases := []struct {
		name      string
		old, new  string
		wantRules []string
	}{
		{"reworded prose, no assertion changed",
			specWith("plain text"), specWith("plain text, clarified"), nil},
		{"requires gains an entry",
			specRequiring(), specRequiring("029-research-work.md"), []string{"new-dependency"}},
		{"wl: token added",
			specWith("nothing"), specWith("emits `wl:DocumentAccepted`"), []string{"ns-term", "surface-token"}},
		{"code span changed",
			specWith("run `lode doc show`"), specWith("run `lode doc get`"), []string{"surface-token"}},
		{"fenced DDL line changed",
			specWithFence("a text"), specWithFence("a bigint"), []string{"surface-token"}},
		{"acceptance criteria reworded",
			specSection("Acceptance criteria", "old"), specSection("Acceptance criteria", "new"), []string{"acceptance-criteria"}},
		{"unchanged section not scanned",
			twoSections("old text"), twoSections("new text"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			old := mustParse(t, c.old)
			new := mustParse(t, c.new)

			findings := MechanicalFindings(old, new)

			var gotRules []string
			for _, f := range findings {
				gotRules = append(gotRules, f.Rule)
			}
			if !slices.Equal(gotRules, c.wantRules) {
				t.Errorf("rules = %v, want %v", gotRules, c.wantRules)
			}
		})
	}
}

func TestChangedAnchors(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
		want     []string
	}{
		{"reword one section", specWith("a"), specWith("b"), []string{"sec-1"}},
		{"add a section",
			"# Spec\n\n## Section {#sec-1}\n\na\n",
			"# Spec\n\n## Section {#sec-1}\n\na\n\n## Second {#sec-2}\n\nb\n",
			[]string{"sec-2"}},
		{"byte-identical bodies", specWith("a"), specWith("a"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			old := mustParse(t, c.old)
			new := mustParse(t, c.new)

			got := ChangedAnchors(old, new)

			if !slices.Equal(got, c.want) {
				t.Errorf("ChangedAnchors = %v, want %v", got, c.want)
			}
		})
	}
}

// specWith returns a single-section document whose §sec-1 body is text.
func specWith(text string) string {
	return "# Spec\n\n## Section {#sec-1}\n\n" + text + "\n"
}

// specWithFence returns a single-section document whose §sec-1 body is a
// fenced code block with one content line, line.
func specWithFence(line string) string {
	return "# Spec\n\n## Section {#sec-1}\n\n```sql\n" + line + "\n```\n"
}

// specSection returns a single-section document with a custom heading.
func specSection(heading, text string) string {
	return "# Spec\n\n## " + heading + " {#sec-1}\n\n" + text + "\n"
}

// specRequiring returns a document whose frontmatter `requires` list holds
// deps (possibly none) and whose body never changes.
func specRequiring(deps ...string) string {
	var front strings.Builder
	if len(deps) > 0 {
		front.WriteString("---\nrequires:\n")
		for _, d := range deps {
			front.WriteString("  - " + d + "\n")
		}
		front.WriteString("---\n")
	}
	return front.String() + "# Spec\n\n## Section {#sec-1}\n\nbody text\n"
}

// twoSections returns a document with two sections: §sec-2's body is
// sec2Text, and §sec-3 always holds a wl: token in a code span but its text
// never varies across a test case's old/new pair — the fixture for "an
// unchanged section is not scanned even though it contains a trigger".
func twoSections(sec2Text string) string {
	return "# Spec\n\n## Section Two {#sec-2}\n\n" + sec2Text +
		"\n\n## Section Three {#sec-3}\n\nuses `wl:Foo` here\n"
}
