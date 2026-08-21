package skillsync

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// The whole point of the qualifier: one marketplace-shaped source publishing
// two plugins that ship the same skill name. Before 037 §4 the second one lost
// to a UNIQUE (name) constraint; now the two are separate identities.
func TestQualifierDistinguishesTwoPluginsInOneSource(t *testing.T) {
	src := Source{Repo: "acme/claude-plugins", Ref: "main", Glob: "plugins/*/skills/*"}
	tb := tarballOf(t, "acme-claude-plugins-aaa111", map[string]string{
		"plugins/one/.claude-plugin/plugin.json":    `{"name":"superpowers"}`,
		"plugins/one/skills/brainstorming/SKILL.md": "---\nname: brainstorming\ndescription: d\n---\nbody",
		"plugins/two/.claude-plugin/plugin.json":    `{"name":"lode"}`,
		"plugins/two/skills/brainstorming/SKILL.md": "---\nname: brainstorming\ndescription: d\n---\nbody",
	})
	sy := &Syncer{}
	tree, err := sy.skillDirs(tb, src)
	if err != nil {
		t.Fatalf("skillDirs: %v", err)
	}
	if len(tree.dirs) != 2 {
		t.Fatalf("dirs: %+v", tree.dirs)
	}
	got := map[string]string{}
	for _, d := range tree.dirs {
		got[d.dir] = sy.qualifierFor(src, tree.manifests, d.dir)
	}
	if got["plugins/one/skills/brainstorming"] != "superpowers" ||
		got["plugins/two/skills/brainstorming"] != "lode" {
		t.Fatalf("qualifiers: %+v", got)
	}
}

// A repo of bare skills ships no manifest. Falling back to the repo's last
// segment keeps every synced skill qualified without requiring the source to
// be packaged as a plugin.
func TestQualifierFallsBackToRepoSegment(t *testing.T) {
	sy := &Syncer{}
	src := Source{Repo: "acme/org-skills", Ref: "main", Glob: "skills/*"}
	if got := sy.qualifierFor(src, map[string]string{}, "skills/git"); got != "org-skills" {
		t.Fatalf("qualifier = %q, want org-skills", got)
	}
	// A repo name that is not usable as a qualifier must not cost its skills
	// their registry entry.
	odd := Source{Repo: "acme/.hidden", Ref: "main", Glob: "skills/*"}
	if got := sy.qualifierFor(odd, map[string]string{}, "skills/git"); !validQualifier(got) {
		t.Fatalf("qualifier %q is not usable", got)
	}
}

// The qualifier is a namespace claim, so it comes from the plugin manifest and
// never from SKILL.md: a skill that could name its own plugin could claim
// another plugin's.
func TestQualifierIgnoresFrontmatterClaim(t *testing.T) {
	src := Source{Repo: "acme/claude-plugins", Ref: "main", Glob: "plugins/*/skills/*"}
	tb := tarballOf(t, "acme-claude-plugins-aaa111", map[string]string{
		"plugins/one/.claude-plugin/plugin.json": `{"name":"lode"}`,
		"plugins/one/skills/tdd/SKILL.md": "---\nname: tdd\nplugin: superpowers\n" +
			"qualifier: superpowers\ndescription: d\n---\nbody",
	})
	sy := &Syncer{}
	tree, err := sy.skillDirs(tb, src)
	if err != nil {
		t.Fatalf("skillDirs: %v", err)
	}
	q := sy.qualifierFor(src, tree.manifests, tree.dirs[0].dir)
	u, err := buildUpsert(src, tree.commit, tree.dirs[0].dir, tree.dirs[0].files, q)
	if err != nil {
		t.Fatalf("buildUpsert: %v", err)
	}
	if u.Qualifier != "lode" || u.Name != "tdd" {
		t.Fatalf("identity = %s:%s, want lode:tdd", u.Qualifier, u.Name)
	}
}

// Manifests are metadata about a skill, not part of it. Adding one to the
// archive would change every existing skill's content hash and mint a
// spurious new version of the whole registry on the first sync after this
// ships, so the archive must stay byte-identical.
func TestManifestStaysOutOfTheArchive(t *testing.T) {
	src := Source{Repo: "acme/claude-plugins", Ref: "main", Glob: "plugins/*/skills/*"}
	skill := map[string]string{
		"plugins/one/skills/tdd/SKILL.md": "---\nname: tdd\ndescription: d\n---\nbody",
	}
	withManifest := map[string]string{
		"plugins/one/.claude-plugin/plugin.json": `{"name":"lode"}`,
	}
	for k, v := range skill {
		withManifest[k] = v
	}

	sy := &Syncer{}
	hashOf := func(files map[string]string) (string, []byte) {
		t.Helper()
		tree, err := sy.skillDirs(tarballOf(t, "acme-claude-plugins-aaa111", files), src)
		if err != nil || len(tree.dirs) != 1 {
			t.Fatalf("skillDirs: %+v %v", tree, err)
		}
		u, err := buildUpsert(src, tree.commit, tree.dirs[0].dir, tree.dirs[0].files, "lode")
		if err != nil {
			t.Fatalf("buildUpsert: %v", err)
		}
		return u.ContentHash, u.Archive
	}
	bareHash, bareArchive := hashOf(skill)
	gotHash, gotArchive := hashOf(withManifest)
	if gotHash != bareHash {
		t.Fatalf("content hash changed: %s != %s", gotHash, bareHash)
	}
	if !bytes.Equal(gotArchive, bareArchive) {
		t.Fatalf("archive changed when a manifest was present")
	}
}

// A manifest at the repo root covers every skill under it; a nearer one wins.
func TestQualifierPrefersNearestManifest(t *testing.T) {
	sy := &Syncer{}
	src := Source{Repo: "acme/p", Ref: "main", Glob: "plugins/*/skills/*"}
	manifests := map[string]string{"": "root", "plugins/one": "near"}
	if got := sy.qualifierFor(src, manifests, "plugins/one/skills/x"); got != "near" {
		t.Fatalf("qualifier = %q, want near", got)
	}
	if got := sy.qualifierFor(src, manifests, "plugins/two/skills/x"); got != "root" {
		t.Fatalf("qualifier = %q, want root", got)
	}
}

// An unusable manifest name is reported, not silently adopted: a qualifier
// carrying a colon would make the qualified name unparseable, and the sync
// falls back to the repo segment.
func TestUnusablePluginNameIsRejectedAndWarned(t *testing.T) {
	for _, blob := range []string{`{"name":"a:b"}`, `{"name":""}`, `{"name":"../x"}`, `not json`} {
		if got := pluginName([]byte(blob)); got != "" {
			t.Fatalf("pluginName(%s) = %q, want rejected", blob, got)
		}
	}

	src := Source{Repo: "acme/p", Ref: "main", Glob: "plugins/*/skills/*"}
	var logbuf bytes.Buffer
	sy := &Syncer{Log: slog.New(slog.NewTextHandler(&logbuf, nil))}
	tree, err := sy.skillDirs(tarballOf(t, "acme-p-aaa111", map[string]string{
		"plugins/one/.claude-plugin/plugin.json": `{"name":"a:b"}`,
		"plugins/one/skills/tdd/SKILL.md":        "---\nname: tdd\ndescription: d\n---\nbody",
	}), src)
	if err != nil {
		t.Fatalf("skillDirs: %v", err)
	}
	if got := sy.qualifierFor(src, tree.manifests, tree.dirs[0].dir); got != "p" {
		t.Fatalf("qualifier = %q, want the repo-segment fallback p", got)
	}
	if !strings.Contains(logbuf.String(), "plugin manifest has no usable name") {
		t.Fatalf("want a warning, got: %s", logbuf.String())
	}
}
