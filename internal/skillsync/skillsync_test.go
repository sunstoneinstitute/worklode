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

// fakeEmbed returns one fixed vector per chunk. id and vec identify the
// embedding space, so two of them stand in for a provider swap; fails makes
// that many leading calls return an error, standing in for a transient 429.
type fakeEmbed struct {
	calls int
	id    string
	vec   []float32
	fails int
}

func (f *fakeEmbed) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.calls++
	if f.fails > 0 {
		f.fails--
		return nil, errors.New("embed unavailable")
	}
	v := f.vec
	if v == nil {
		v = []float32{1, 0, 0}
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = v
	}
	return out, nil
}

func (f *fakeEmbed) ID() string {
	if f.id == "" {
		return "fake"
	}
	return f.id
}

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
	emb := &fakeEmbed{}
	sy := &Syncer{Store: st, Fetch: fetch, Embed: emb}
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
	if emb.calls != 1 {
		t.Fatalf("embed calls: %d", emb.calls)
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

	// Second sync, unchanged content: no re-embed.
	if _, err := sy.SyncAll(ctx, src); err != nil {
		t.Fatalf("resync: %v", err)
	}
	if emb.calls != 1 {
		t.Fatalf("re-embedded unchanged skill: %d calls", emb.calls)
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
	dirs, commit, err := sy.skillDirs(tb, testSource)
	if err != nil {
		t.Fatalf("skillDirs: %v", err)
	}
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
	dirs, _, err := sy.skillDirs(tarballOf(t, "acme-p-aaa111", files), testSource)
	if err != nil {
		t.Fatalf("skillDirs: %v", err)
	}
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
		if _, err := buildUpsert(testSource, "aaa111", "skills/x", files); err == nil {
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
	dirs, _, err := sy.skillDirs(tb, testSource)
	if err != nil {
		t.Fatalf("skillDirs: %v", err)
	}
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
	dirs, commit, err := sy.skillDirs(tb, testSource)
	if err != nil || len(dirs) != 1 {
		t.Fatalf("skillDirs: %+v %v", dirs, err)
	}
	if !dirs[0].files["scripts/check.sh"].exec || dirs[0].files["SKILL.md"].exec {
		t.Fatalf("exec bits: %+v", dirs[0].files)
	}
	u, err := buildUpsert(testSource, commit, dirs[0].dir, dirs[0].files)
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

// skillSyncer wires a syncer over a fixed one-skill source. Callers set
// sy.Embed themselves — the embedding provider is what these tests vary.
func skillSyncer(t *testing.T, st *store.Store, logbuf *bytes.Buffer) (*Syncer, []Source) {
	t.Helper()
	tb := tarballOf(t, "acme-p-aaa111", map[string]string{"skills/tdd/SKILL.md": skillMD})
	sy := &Syncer{
		Store: st,
		Fetch: func(ctx context.Context, repo, ref string) ([]byte, error) { return tb, nil },
	}
	if logbuf != nil {
		sy.Log = slog.New(slog.NewTextHandler(logbuf, nil))
	}
	return sy, []Source{testSource}
}

// The embedding space the stored vectors belong to is recorded, so a later
// sync can tell whether they are still comparable.
func TestSyncAllRecordsEmbeddingProvider(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()
	sy, src := skillSyncer(t, st, nil)
	sy.Embed = &fakeEmbed{id: "fake:a"}

	sum, err := sy.SyncAll(ctx, src)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	// The skill was embedded as it changed, so convergence found nothing left.
	if sum.Embedded != 0 {
		t.Fatalf("summary: %+v", sum)
	}
	got, err := st.EmbeddingProviderID(ctx)
	if err != nil || got != "fake:a" {
		t.Fatalf("provider id = %q err=%v, want fake:a", got, err)
	}
}

// A provider change invalidates every stored vector — they are not comparable
// with the new model's — so the corpus is cleared and re-embedded even though
// no skill content changed.
func TestSyncAllProviderChangeReembeds(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()
	var logbuf bytes.Buffer
	sy, src := skillSyncer(t, st, &logbuf)

	sy.Embed = &fakeEmbed{id: "fake:a", vec: []float32{1, 0, 0}}
	if _, err := sy.SyncAll(ctx, src); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	sy.Embed = &fakeEmbed{id: "fake:b", vec: []float32{0, 1, 0}}
	sum, err := sy.SyncAll(ctx, src)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if sum.Embedded != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	if id, err := st.EmbeddingProviderID(ctx); err != nil || id != "fake:b" {
		t.Fatalf("provider id = %q err=%v, want fake:b", id, err)
	}
	if got, err := st.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0.5); err != nil || len(got) != 0 {
		t.Fatalf("old-space vectors survived: %+v err=%v", got, err)
	}
	got, err := st.RecommendSkills(ctx, []float32{0, 1, 0}, 5, 0.5)
	if err != nil || len(got) != 1 || got[0].Name != "tdd" {
		t.Fatalf("new-space recommend: %+v err=%v", got, err)
	}
	if log := logbuf.String(); !strings.Contains(log, "fake:a") || !strings.Contains(log, "fake:b") {
		t.Fatalf("want a log naming both provider ids, got: %s", log)
	}
}

// The change is worth logging even when it finds nothing to clear: an
// operator debugging empty recommendations needs the record of the swap, and
// a corpus can be empty because every embed call had been failing. Only the
// true first boot — no id recorded, nothing stored — stays silent.
func TestSyncAllProviderChangeWithoutStoredVectorsLogs(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()
	var logbuf bytes.Buffer
	sy, src := skillSyncer(t, st, &logbuf)

	// Every embed fails, so the id is recorded with no vectors behind it.
	sy.Embed = &fakeEmbed{id: "fake:a", fails: 99}
	if _, err := sy.SyncAll(ctx, src); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if log := logbuf.String(); strings.Contains(log, "embedding provider changed") {
		t.Fatalf("first boot should record the id in silence, got: %s", log)
	}
	logbuf.Reset()

	sy.Embed = &fakeEmbed{id: "fake:b", fails: 99}
	if _, err := sy.SyncAll(ctx, src); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	log := logbuf.String()
	if !strings.Contains(log, "embedding provider changed") ||
		!strings.Contains(log, "fake:a") || !strings.Contains(log, "fake:b") {
		t.Fatalf("want the swap logged with both provider ids, got: %s", log)
	}
}

// The same provider must not trigger a clear: re-embedding the whole corpus
// on every sync would be a needless bill and a needless outage window.
func TestSyncAllUnchangedProviderKeepsEmbeddings(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()
	sy, src := skillSyncer(t, st, nil)
	emb := &fakeEmbed{id: "fake:a"}
	sy.Embed = emb

	if _, err := sy.SyncAll(ctx, src); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	sum, err := sy.SyncAll(ctx, src)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if emb.calls != 1 || sum.Embedded != 0 {
		t.Fatalf("re-embedded unchanged corpus: calls=%d summary=%+v", emb.calls, sum)
	}
	got, err := st.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0.5)
	if err != nil || len(got) != 1 {
		t.Fatalf("embeddings lost: %+v err=%v", got, err)
	}
}

// A transient embed failure at upsert time would otherwise un-search the
// skill forever: its content hash matches from then on, so it is never
// re-embedded. The convergence pass retries it on the next sync.
func TestSyncAllRetriesFailedEmbed(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()
	sy, src := skillSyncer(t, st, nil)
	// Two failures: the upsert-time embed and the first convergence attempt.
	sy.Embed = &fakeEmbed{id: "fake:a", fails: 2}

	sum, err := sy.SyncAll(ctx, src)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if sum.Synced != 1 || sum.Embedded != 0 {
		t.Fatalf("first summary: %+v", sum)
	}
	if got, _ := st.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0.5); len(got) != 0 {
		t.Fatalf("embedded despite failure: %+v", got)
	}

	sum, err = sy.SyncAll(ctx, src)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if sum.Changed != 0 || sum.Embedded != 1 {
		t.Fatalf("second summary: %+v", sum)
	}
	got, err := st.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0.5)
	if err != nil || len(got) != 1 || got[0].Name != "tdd" {
		t.Fatalf("convergence did not embed: %+v err=%v", got, err)
	}
}

// A re-embed that fails must not leave the previous version's vectors
// behind. The skill's content hash matches from then on, so no later sync
// would replace them and the skill would be recommended forever against text
// it no longer contains. Dropping them first makes it a skill with no
// vectors, which the convergence pass repairs — here, in the same pass.
func TestSyncAllFailedReembedDropsStaleVectors(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()
	const head = "---\nname: tdd\ndescription: Red-green-refactor discipline\n---\n\n"
	md := head + "v1 body."
	sy := &Syncer{Store: st, Fetch: func(ctx context.Context, repo, ref string) ([]byte, error) {
		return tarballOf(t, "acme-p-aaa111", map[string]string{"skills/tdd/SKILL.md": md}), nil
	}}
	src := []Source{testSource}

	sy.Embed = &fakeEmbed{id: "fake:a", vec: []float32{1, 0, 0}}
	if _, err := sy.SyncAll(ctx, src); err != nil {
		t.Fatalf("v1 sync: %v", err)
	}

	// v2 arrives and the embed at upsert time fails; convergence retries it.
	md = head + "v2 body."
	sy.Embed = &fakeEmbed{id: "fake:a", vec: []float32{0, 1, 0}, fails: 1}
	if _, err := sy.SyncAll(ctx, src); err != nil {
		t.Fatalf("v2 sync: %v", err)
	}
	if got, _ := st.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0.5); len(got) != 0 {
		t.Fatalf("v1 vectors survived a failed re-embed of v2: %+v", got)
	}
	got, err := st.RecommendSkills(ctx, []float32{0, 1, 0}, 5, 0.5)
	if err != nil || len(got) != 1 || got[0].Name != "tdd" {
		t.Fatalf("v2 not embedded by the same pass: %+v err=%v", got, err)
	}
}

// With no provider configured nothing recommends, so stored vectors are
// inert rather than wrong — and wiping them would destroy data an operator
// may be about to re-enable.
func TestSyncAllWithoutProviderLeavesEmbeddings(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()
	sy, src := skillSyncer(t, st, nil)
	sy.Embed = &fakeEmbed{id: "fake:a"}
	if _, err := sy.SyncAll(ctx, src); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	sy.Embed = nil
	if _, err := sy.SyncAll(ctx, src); err != nil {
		t.Fatalf("sync without provider: %v", err)
	}
	if id, err := st.EmbeddingProviderID(ctx); err != nil || id != "fake:a" {
		t.Fatalf("provider id = %q err=%v, want fake:a", id, err)
	}
	got, err := st.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0.5)
	if err != nil || len(got) != 1 {
		t.Fatalf("embeddings cleared without a provider: %+v err=%v", got, err)
	}
}
