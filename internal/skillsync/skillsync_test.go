package skillsync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestParseSources(t *testing.T) {
	got, err := ParseSources("acme/claude-plugins@main:plugins/*/skills/*,acme/skills@v1:skills/*")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 || got[0].Repo != "acme/claude-plugins" || got[0].Ref != "main" ||
		got[0].Glob != "plugins/*/skills/*" || got[1].Ref != "v1" {
		t.Fatalf("parse: %+v", got)
	}
	if s, err := ParseSources(""); err != nil || s != nil {
		t.Fatalf("empty: %v %v", s, err)
	}
	for _, bad := range []string{"norepo", "a/b:glob", "a/b@ref", "a/b@ref:"} {
		if _, err := ParseSources(bad); err == nil {
			t.Fatalf("want error for %q", bad)
		}
	}
	// One repo listed twice would make each entry soft-delete the other's
	// skills, whatever the ref and glob say.
	for _, dup := range []string{"a/b@main:x/*,a/b@main:x/*", "a/b@main:x/*,a/b@v2:y/*"} {
		_, err := ParseSources(dup)
		if err == nil || !strings.Contains(err.Error(), "a/b") {
			t.Fatalf("want duplicate-repo error for %q, got %v", dup, err)
		}
	}
	// A glob that can never match a dir would soft-delete the whole repo, so
	// it is rejected at config time rather than at sync time.
	for _, bad := range []string{"a/b@main:skills/*/", "a/b@main:./skills/*", "a/b@main:skills/["} {
		if _, err := ParseSources(bad); err == nil {
			t.Fatalf("want glob error for %q", bad)
		}
	}
}

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name, md, wantName, wantDesc string
	}{
		{"plain", skillMD, "tdd", "Red-green-refactor discipline"},
		{"quoted", "---\nname: \"tdd\"\ndescription: 'quoted'\n---\n", "tdd", "quoted"},
		{"no frontmatter", "just prose", "", ""},
		{"folded", "---\nname: app\ndescription: >-\n  One line,\n  then another.\n---\n",
			"app", "One line, then another."},
		{"folded plus", "---\ndescription: >+\n  Kept.\nname: app\n---\n", "app", "Kept."},
		{"literal", "---\nname: app\ndescription: |\n  Line one.\n    Indented.\n---\n",
			"app", "Line one.\n  Indented."},
		// Regression: a folded description whose continuation line looks like a
		// key must not clobber the real name.
		{"no key clobber", "---\nname: real\ndescription: >-\n  name: fake\n  see oidc: RoleBindings\n---\n",
			"real", "name: fake see oidc: RoleBindings"},
		{"block at end of frontmatter", "---\nname: app\ndescription: >\n  Trailing.\n---\nbody",
			"app", "Trailing."},
		// Trimming each end independently would cut the closing quote only.
		{"unbalanced quotes", "---\nname: app\ndescription: Ask \"why\"\n---\n", "app", `Ask "why"`},
		{"BOM", "\ufeff---\nname: app\ndescription: Fine\n---\n", "app", "Fine"},
		// A Windows-authored SKILL.md must still parse: the closing marker is
		// "---\r", and a block scalar's value must not carry the \r either.
		{"CRLF", "---\r\nname: app\r\ndescription: Fine\r\n---\r\n", "app", "Fine"},
		{"CRLF literal block", "---\r\nname: app\r\ndescription: |\r\n  Line one.\r\n  Line two.\r\n---\r\n",
			"app", "Line one.\nLine two."},
		// Without a closing marker there is no frontmatter block, so the
		// document body must not be scanned for keys.
		{"unterminated", "---\nname: app\n\n# Heading\n\nname: from the body\n", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, desc := parseFrontmatter(tt.md)
			if name != tt.wantName || desc != tt.wantDesc {
				t.Fatalf("got name=%q desc=%q, want name=%q desc=%q", name, desc, tt.wantName, tt.wantDesc)
			}
		})
	}
}

// tarballOf builds a GitHub-shaped tarball: entries under root/, gzipped.
func tarballOf(t *testing.T, root string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for p, c := range files {
		if err := tw.WriteHeader(&tar.Header{Name: root + "/" + p, Mode: 0o644, Size: int64(len(c)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(c)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

const skillMD = "---\nname: tdd\ndescription: Red-green-refactor discipline\n---\n\nUse TDD always."

func TestSyncAll(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()

	tb := tarballOf(t, "acme-claude-plugins-abc123", map[string]string{
		"plugins/sp/skills/tdd/SKILL.md":            skillMD,
		"plugins/sp/skills/tdd/references/notes.md": "extra notes",
		"plugins/sp/skills/noskillmd/other.md":      "not a skill (no SKILL.md)",
		"README.md":                                 "not under glob",
	})
	fetch := func(ctx context.Context, repo, ref string) ([]byte, error) { return tb, nil }
	sy := &Syncer{Store: st, Fetch: fetch}
	src := []Source{{Repo: "acme/claude-plugins", Ref: "main", Glob: "plugins/*/skills/*"}}

	sum, err := sy.SyncAll(ctx, src)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if sum.Synced != 1 || sum.Deleted != 0 {
		t.Fatalf("summary: %+v", sum)
	}
	sk, err := st.GetSkill(ctx, "tdd")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sk.Description != "Red-green-refactor discipline" || sk.SkillMD != skillMD {
		t.Fatalf("skill: %+v", sk)
	}
	// Archive is a readable tar.gz containing both files.
	arch, err := st.SkillArchive(ctx, "tdd", sk.ContentHash)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	names := tarNames(t, arch)
	if len(names) != 2 || names["SKILL.md"] == false || names["references/notes.md"] == false {
		t.Fatalf("archive entries: %v", names)
	}

	// Second sync, unchanged content: still one skill, nothing changed.
	if sum, err := sy.SyncAll(ctx, src); err != nil || sum.Changed != 0 {
		t.Fatalf("resync: %+v err=%v", sum, err)
	}

	// Skill dir removed upstream: soft-deleted.
	empty := tarballOf(t, "acme-claude-plugins-def456", map[string]string{"README.md": "x"})
	sy.Fetch = func(ctx context.Context, repo, ref string) ([]byte, error) { return empty, nil }
	sum, err = sy.SyncAll(ctx, src)
	if err != nil || sum.Deleted != 1 {
		t.Fatalf("delete sync: %+v err=%v", sum, err)
	}
}

func tarNames(t *testing.T, gzBytes []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(gzBytes))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	out := map[string]bool{}
	for {
		h, err := tr.Next()
		if err != nil {
			return out
		}
		out[h.Name] = true
	}
}

// A tarball fetch failure aborts its source; a bad skill only skips that skill.
func TestSyncAllFailureScopes(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()
	src := []Source{{Repo: "acme/p", Ref: "main", Glob: "skills/*"}}

	sy := &Syncer{Store: st, Fetch: func(ctx context.Context, repo, ref string) ([]byte, error) {
		return nil, errors.New("boom")
	}}
	if _, err := sy.SyncAll(ctx, src); err == nil || !strings.Contains(err.Error(), "acme/p@main") {
		t.Fatalf("want source-scoped fetch error, got %v", err)
	}

	sy.Fetch = func(ctx context.Context, repo, ref string) ([]byte, error) { return []byte("not gzip"), nil }
	if _, err := sy.SyncAll(ctx, src); err == nil {
		t.Fatalf("want error for undecodable tarball")
	}

	// One skill with unparseable frontmatter is skipped; its sibling still syncs.
	tb := tarballOf(t, "acme-p-aaa111", map[string]string{
		"skills/good/SKILL.md": skillMD,
		"skills/bad/SKILL.md":  "no frontmatter here",
	})
	sy.Fetch = func(ctx context.Context, repo, ref string) ([]byte, error) { return tb, nil }
	sum, err := sy.SyncAll(ctx, src)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if sum.Synced != 1 {
		t.Fatalf("want only the good skill synced: %+v", sum)
	}
}

// Two dirs declaring one skill name collide; the sync warns and names both.
func TestSyncAllDuplicateSkillName(t *testing.T) {
	st := store.OpenTestStore(t)
	var logbuf bytes.Buffer
	tb := tarballOf(t, "acme-p-aaa111", map[string]string{
		"skills/one/SKILL.md": skillMD,
		"skills/two/SKILL.md": skillMD,
	})
	sy := &Syncer{
		Store: st,
		Fetch: func(ctx context.Context, repo, ref string) ([]byte, error) { return tb, nil },
		Log:   slog.New(slog.NewTextHandler(&logbuf, nil)),
	}
	if _, err := sy.SyncAll(context.Background(), []Source{{Repo: "acme/p", Ref: "main", Glob: "skills/*"}}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got := logbuf.String()
	if !strings.Contains(got, "duplicate skill name") ||
		!strings.Contains(got, "skills/one") || !strings.Contains(got, "skills/two") {
		t.Fatalf("want a warning naming both dirs, got: %s", got)
	}
}

// An over-size skill dir is dropped, and its bytes are not retained.
func TestSkillDirsDropsOversize(t *testing.T) {
	orig := maxSkillBytes
	maxSkillBytes = 1024
	t.Cleanup(func() { maxSkillBytes = orig })

	big := strings.Repeat("x", maxSkillBytes+1)
	tb := tarballOf(t, "acme-p-aaa111", map[string]string{
		"skills/fat/SKILL.md":  skillMD,
		"skills/fat/big.md":    big,
		"skills/slim/SKILL.md": skillMD,
	})
	sy := &Syncer{}
	tree, err := sy.skillDirs(tb, testSource)
	if err != nil {
		t.Fatalf("skillDirs: %v", err)
	}
	dirs, commit := tree.dirs, tree.commit
	if commit != "aaa111" {
		t.Fatalf("commit: %q", commit)
	}
	if len(dirs) != 1 || dirs[0].dir != "skills/slim" {
		t.Fatalf("dirs: %+v", dirs)
	}
}

// The file-count cap has to hold at ingest too. Without it a 2100-file skill
// dir passed ingest, listed fine and served its archive, then failed on every
// client with "archive exceeds 2000 entries".
func TestSkillDirsDropsOverEntryCount(t *testing.T) {
	orig := maxSkillEntries
	maxSkillEntries = 4
	t.Cleanup(func() { maxSkillEntries = orig })

	files := map[string]string{"skills/fat/SKILL.md": skillMD, "skills/slim/SKILL.md": skillMD}
	for i := 0; i < maxSkillEntries; i++ {
		files[fmt.Sprintf("skills/fat/f%02d.md", i)] = "x"
	}
	var logbuf bytes.Buffer
	sy := &Syncer{Log: slog.New(slog.NewTextHandler(&logbuf, nil))}
	tree, err := sy.skillDirs(tarballOf(t, "acme-p-aaa111", files), testSource)
	if err != nil {
		t.Fatalf("skillDirs: %v", err)
	}
	dirs := tree.dirs
	if len(dirs) != 1 || dirs[0].dir != "skills/slim" {
		t.Fatalf("dirs: %+v", dirs)
	}
	if got := logbuf.String(); !strings.Contains(got, "too many files") {
		t.Fatalf("want a warning naming the dropped dir, got: %s", got)
	}
}

// A frontmatter name that is not a usable path or URL segment must be
// rejected at ingest. Stored, it would list fine and then fail every install
// and be unroutable on GET /api/v1/skills/{name}.
func TestBuildUpsertRejectsUnusableName(t *testing.T) {
	for _, name := range []string{"../escape", "a/b", ".hidden", ".", ".."} {
		md := "---\nname: " + name + "\ndescription: d\n---\n\nbody"
		files := map[string]file{"SKILL.md": {data: []byte(md)}}
		if _, err := buildUpsert(testSource, "aaa111", "skills/x", files, "p"); err == nil {
			t.Fatalf("name %q: want an ingest error", name)
		}
	}
}

var testSource = Source{Repo: "acme/p", Ref: "main", Glob: "skills/*"}

// tarEntry is one tarball entry with explicit mode and type, for the cases
// tarballOf's plain string map cannot express.
type tarEntry struct {
	body string
	mode int64
	typ  byte
	link string
}

func tarballOfEntries(t *testing.T, root string, files map[string]tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for p, e := range files {
		h := &tar.Header{Name: root + "/" + p, Mode: e.mode, Typeflag: e.typ, Linkname: e.link}
		if e.typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// Symlinks are dropped — extracting one would write outside the skill dir —
// but the drop is reported, so a skill referencing the missing file is not a
// silent mystery.
func TestSkillDirsSkipsSymlinks(t *testing.T) {
	tb := tarballOfEntries(t, "acme-p-aaa111", map[string]tarEntry{
		"skills/one/SKILL.md":           {body: skillMD, mode: 0o644, typ: tar.TypeReg},
		"skills/one/references/link.md": {mode: 0o777, typ: tar.TypeSymlink, link: "../../two/references/link.md"},
	})
	var logbuf bytes.Buffer
	sy := &Syncer{Log: slog.New(slog.NewTextHandler(&logbuf, nil))}
	tree, err := sy.skillDirs(tb, testSource)
	if err != nil {
		t.Fatalf("skillDirs: %v", err)
	}
	dirs := tree.dirs
	if len(dirs) != 1 {
		t.Fatalf("dirs: %+v", dirs)
	}
	if _, ok := dirs[0].files["references/link.md"]; ok {
		t.Fatalf("symlink was ingested")
	}
	got := logbuf.String()
	if !strings.Contains(got, "skipping non-regular entry") || !strings.Contains(got, "references/link.md") {
		t.Fatalf("want a warning naming the entry, got: %s", got)
	}
}

// Skills ship scripts: the executable bit must survive into the archive and
// must change the content hash.
func TestExecutableBitSurvives(t *testing.T) {
	tb := tarballOfEntries(t, "acme-p-aaa111", map[string]tarEntry{
		"skills/one/SKILL.md":         {body: skillMD, mode: 0o644, typ: tar.TypeReg},
		"skills/one/scripts/check.sh": {body: "#!/bin/sh\ntrue\n", mode: 0o755, typ: tar.TypeReg},
	})
	sy := &Syncer{}
	tree, err := sy.skillDirs(tb, testSource)
	if err != nil || len(tree.dirs) != 1 {
		t.Fatalf("skillDirs: %+v %v", tree, err)
	}
	dirs, commit := tree.dirs, tree.commit
	if !dirs[0].files["scripts/check.sh"].exec || dirs[0].files["SKILL.md"].exec {
		t.Fatalf("exec bits: %+v", dirs[0].files)
	}
	u, err := buildUpsert(testSource, commit, dirs[0].dir, dirs[0].files, "p")
	if err != nil {
		t.Fatalf("buildUpsert: %v", err)
	}
	modes := tarModes(t, u.Archive)
	if modes["scripts/check.sh"] != 0o755 || modes["SKILL.md"] != 0o644 {
		t.Fatalf("archive modes: %v", modes)
	}
	off := map[string]file{"s.sh": {data: []byte("x")}}
	on := map[string]file{"s.sh": {data: []byte("x"), exec: true}}
	if contentHash(off) == contentHash(on) {
		t.Fatalf("chmod +x did not change the content hash")
	}
}

func tarModes(t *testing.T, gzBytes []byte) map[string]int64 {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(gzBytes))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	out := map[string]int64{}
	for {
		h, err := tr.Next()
		if err != nil {
			return out
		}
		out[h.Name] = h.Mode
	}
}

// A glob that matches nothing soft-deletes the whole repo's skills. That may
// be correct, so it still happens — but it must never happen quietly.
func TestSyncAllZeroMatchWarnsBeforeDeleting(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()
	tb := tarballOf(t, "acme-p-aaa111", map[string]string{"skills/one/SKILL.md": skillMD})
	var logbuf bytes.Buffer
	sy := &Syncer{
		Store: st,
		Fetch: func(ctx context.Context, repo, ref string) ([]byte, error) { return tb, nil },
		Log:   slog.New(slog.NewTextHandler(&logbuf, nil)),
	}
	if _, err := sy.SyncAll(ctx, []Source{testSource}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// Same tarball, a glob one level too deep — a plausible config typo that
	// path.Match accepts.
	logbuf.Reset()
	sum, err := sy.SyncAll(ctx, []Source{{Repo: "acme/p", Ref: "main", Glob: "skills/*/*"}})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if sum.Synced != 0 || sum.Deleted != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	got := logbuf.String()
	if !strings.Contains(got, "skill source matched no dirs") ||
		!strings.Contains(got, "skills/*/*") || !strings.Contains(got, "deleting=1") {
		t.Fatalf("want a warning naming the glob and the count, got: %s", got)
	}
}

// One failing source must not strand the others, and Changed must separate a
// real ingest from a steady-state no-op.
func TestSyncAllContinuesPastFailedSource(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()
	tb := tarballOf(t, "acme-b-bbb222", map[string]string{"skills/one/SKILL.md": skillMD})
	sy := &Syncer{Store: st, Fetch: func(ctx context.Context, repo, ref string) ([]byte, error) {
		if repo == "acme/a" {
			return nil, errors.New("boom")
		}
		return tb, nil
	}}
	src := []Source{
		{Repo: "acme/a", Ref: "main", Glob: "skills/*"},
		{Repo: "acme/b", Ref: "main", Glob: "skills/*"},
	}
	sum, err := sy.SyncAll(ctx, src)
	if err == nil || !strings.Contains(err.Error(), "acme/a@main") {
		t.Fatalf("want the failing source reported, got %v", err)
	}
	if sum.Synced != 1 || sum.Changed != 1 {
		t.Fatalf("healthy source should still have synced: %+v", sum)
	}
	// Steady state: seen again, but nothing new.
	sum, _ = sy.SyncAll(ctx, src)
	if sum.Synced != 1 || sum.Changed != 0 {
		t.Fatalf("want synced-but-unchanged: %+v", sum)
	}
}

// sortedDirs orders by dir name, which map iteration does not.
func TestSortedDirs(t *testing.T) {
	m := map[string]*skillDir{
		"c": {dir: "c"}, "a": {dir: "a"}, "b": {dir: "b"},
	}
	got := sortedDirs(m)
	if len(got) != 3 || got[0].dir != "a" || got[1].dir != "b" || got[2].dir != "c" {
		t.Fatalf("sortedDirs: %+v", got)
	}
}

// The length prefix keeps a concatenation collision from hashing equal.
func TestContentHashNoConcatCollision(t *testing.T) {
	a := contentHash(map[string]file{"a": {data: []byte("xy")}, "b": {data: []byte("")}})
	b := contentHash(map[string]file{"a": {data: []byte("x")}, "b": {data: []byte("y")}})
	if a == b {
		t.Fatalf("collision: %s", a)
	}
	if a != contentHash(map[string]file{"b": {data: []byte("")}, "a": {data: []byte("xy")}}) {
		t.Fatalf("hash depends on map order")
	}
}

func TestMatchesPush(t *testing.T) {
	src := []Source{{Repo: "acme/p", Ref: "main", Glob: "skills/*"}}
	if !MatchesPush(src, "acme/p", "main") {
		t.Fatalf("want match")
	}
	if MatchesPush(src, "acme/p", "dev") || MatchesPush(src, "acme/q", "main") {
		t.Fatalf("want no match")
	}
}
