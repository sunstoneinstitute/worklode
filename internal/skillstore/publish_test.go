package skillstore

import (
	"archive/tar"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// demoEntries is the "demo" skill's fixture: a plain file plus an
// executable one, so the exec bit has something to survive through both
// extract and the publish-side copy fallback.
func demoEntries() []tarEntry {
	return []tarEntry{
		{Name: "SKILL.md", Content: "demo body", Mode: 0o644, Typeflag: tar.TypeReg},
		{Name: "run.sh", Content: "#!/bin/sh\necho hi\n", Mode: 0o755, Typeflag: tar.TypeReg},
	}
}

// installedDirs returns a fresh Dirs with one skill ("demo") installed, for
// tests that publish from an already-populated links dir.
func installedDirs(t *testing.T) Dirs {
	t.Helper()
	dirs := testDirs(t)
	entries := demoEntries()
	arch := buildTar(t, entries)
	hash := hashEntries(entries)
	if _, err := Ensure(dirs, "demo", hash, func() ([]byte, error) { return arch, nil }); err != nil {
		t.Fatalf("setup: ensure demo skill: %v", err)
	}
	return dirs
}

func TestPublishDirLinkCreatesSymlink(t *testing.T) {
	dirs := installedDirs(t)
	agents := filepath.Join(t.TempDir(), ".agents", "skills")
	res, err := PublishDirLink(dirs, agents)
	if err != nil || res.Action != "linked" {
		t.Fatalf("publish: %+v %v", res, err)
	}
	target, err := os.Readlink(agents)
	if err != nil || target != dirs.Links {
		t.Fatalf("target = %q err=%v; want %s", target, err, dirs.Links)
	}

	// Re-run: idempotent, reports unchanged, symlink untouched.
	res2, err := PublishDirLink(dirs, agents)
	if err != nil || res2.Action != "unchanged" {
		t.Fatalf("re-run: %+v %v", res2, err)
	}
	if target2, _ := os.Readlink(agents); target2 != dirs.Links {
		t.Fatalf("unchanged re-run retargeted symlink: %q", target2)
	}

	// A symlink already pointing elsewhere is skipped and untouched — never
	// repoint a link Worklode did not create.
	elsewhere := t.TempDir()
	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := os.Symlink(elsewhere, foreign); err != nil {
		t.Fatalf("setup: %v", err)
	}
	res3, err := PublishDirLink(dirs, foreign)
	if err != nil || res3.Action != "skipped" {
		t.Fatalf("foreign symlink: %+v %v", res3, err)
	}
	if got, _ := os.Readlink(foreign); got != elsewhere {
		t.Fatalf("foreign symlink retargeted: %q", got)
	}
}

func TestPublishDirLinkDegradesToPerSkillInsideRealDir(t *testing.T) {
	// agents dir exists as a REAL directory with a foreign skill in it:
	// spec 008 §18 row 4 — link per-skill inside it, delete nothing.
	dirs := installedDirs(t)
	agents := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(filepath.Join(agents, "their-skill"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	res, err := PublishDirLink(dirs, agents)
	if err != nil || res.Action != "per-skill" {
		t.Fatalf("publish: %+v %v", res, err)
	}

	// their-skill untouched; our skill linked as agents/<name> -> version dir.
	if _, err := os.Stat(filepath.Join(agents, "their-skill")); err != nil {
		t.Fatalf("foreign skill removed: %v", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(dirs.Links, "demo"))
	if err != nil {
		t.Fatalf("resolve demo: %v", err)
	}
	got, err := os.Readlink(filepath.Join(agents, "demo"))
	if err != nil || got != want {
		t.Fatalf("demo link = %q err=%v; want %s", got, err, want)
	}
}

func TestPublishPerSkill(t *testing.T) {
	// Target dir gets one symlink per name in dirs.Links, each resolving
	// to the version dir (the RESOLVED store path, not the name link, so
	// the harness never double-resolves through ~/.worklode). Foreign
	// entries in the target dir survive. Re-run converges. A target entry
	// that is a real dir (user's own skill with the same name) is skipped
	// and reported, never replaced.
	dirs := installedDirs(t) // "demo" already installed
	files2 := map[string]string{"SKILL.md": "second body"}
	arch2 := gzTar(t, files2)
	hash2 := hashFiles(files2)
	if _, err := Ensure(dirs, "second", hash2, func() ([]byte, error) { return arch2, nil }); err != nil {
		t.Fatalf("setup: ensure second skill: %v", err)
	}

	target := t.TempDir()

	// A target entry with the SAME NAME as a skill, but a real dir owned
	// by the user: must be skipped, reported, never replaced.
	if err := os.MkdirAll(filepath.Join(target, "demo"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "demo", "mine.txt"), []byte("user's own"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// An entry unrelated to any skill name: survives untouched, and is not
	// a reported skip (nothing was ever meant to go there).
	if err := os.WriteFile(filepath.Join(target, "unrelated.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	res, err := PublishPerSkill(dirs, target)
	if err != nil || res.Action != "linked" {
		t.Fatalf("publish: %+v %v", res, err)
	}
	if !reflect.DeepEqual(res.Skips, []string{"demo"}) {
		t.Fatalf("skips = %v, want [demo]", res.Skips)
	}

	// demo untouched: real dir, foreign content intact.
	if got, err := os.ReadFile(filepath.Join(target, "demo", "mine.txt")); err != nil || string(got) != "user's own" {
		t.Fatalf("demo dir was replaced: %q, %v", got, err)
	}

	// second linked to its RESOLVED version dir.
	versionDir, err := filepath.EvalSymlinks(filepath.Join(dirs.Links, "second"))
	if err != nil {
		t.Fatalf("resolve second: %v", err)
	}
	got, err := os.Readlink(filepath.Join(target, "second"))
	if err != nil || got != versionDir {
		t.Fatalf("second link = %q err=%v; want %s", got, err, versionDir)
	}

	// Unrelated entry survives.
	if got, err := os.ReadFile(filepath.Join(target, "unrelated.txt")); err != nil || string(got) != "keep me" {
		t.Fatalf("unrelated entry disturbed: %q, %v", got, err)
	}

	// Re-run converges: same skip, second link still resolves, no error.
	res2, err := PublishPerSkill(dirs, target)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if !reflect.DeepEqual(res2.Skips, []string{"demo"}) {
		t.Fatalf("re-run skips = %v, want [demo]", res2.Skips)
	}
	if got, err := os.Readlink(filepath.Join(target, "second")); err != nil || got != versionDir {
		t.Fatalf("re-run second link = %q err=%v; want %s", got, err, versionDir)
	}
}

func TestPublishCopyFallback(t *testing.T) {
	// Inject symlink failure (see below) and assert the version dir is
	// copied file-for-file instead, with res naming the copy so a stale
	// copy is diagnosable (spec 008 §18 row 5).
	dirs := installedDirs(t)
	target := t.TempDir()

	orig := symlink
	symlink = func(string, string) error { return errors.New("injected: symlinks unavailable") }
	defer func() { symlink = orig }()

	res, err := PublishPerSkill(dirs, target)
	if err != nil || res.Action != "copied" {
		t.Fatalf("publish: %+v %v", res, err)
	}

	versionDir, err := filepath.EvalSymlinks(filepath.Join(dirs.Links, "demo"))
	if err != nil {
		t.Fatalf("resolve demo: %v", err)
	}
	linkPath := filepath.Join(target, "demo")
	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat copy: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("demo should be a real copy, not a symlink")
	}
	got, err := os.ReadFile(filepath.Join(linkPath, "SKILL.md"))
	if err != nil {
		t.Fatalf("read copy: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(versionDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("copy content = %q, want %q", got, want)
	}

	// The exec bit must survive the copy, same as it survives extract.
	runInfo, err := os.Stat(filepath.Join(linkPath, "run.sh"))
	if err != nil {
		t.Fatalf("stat copied run.sh: %v", err)
	}
	if runInfo.Mode().Perm()&0o111 == 0 {
		t.Fatalf("copied run.sh should be executable, got mode %v", runInfo.Mode())
	}
	skillInfo, err := os.Stat(filepath.Join(linkPath, "SKILL.md"))
	if err != nil {
		t.Fatalf("stat copied SKILL.md: %v", err)
	}
	if skillInfo.Mode().Perm()&0o111 != 0 {
		t.Fatalf("copied SKILL.md should not be executable, got mode %v", skillInfo.Mode())
	}
}

// TestPublishDirLinkPerSkillCopyFallbackReportsCopied covers the full
// degradation chain: an existing real directory at target forces PublishDirLink
// to delegate to PublishPerSkill, and symlinks being unavailable forces that
// delegate to copy. The "copied" signal must survive both hops — spec 008
// §18 row 5 requires the copy be diagnosable, and PublishDirLink is the
// entry point the harnesses actually go through, so losing the signal there
// loses it where it matters most.
func TestPublishDirLinkPerSkillCopyFallbackReportsCopied(t *testing.T) {
	dirs := installedDirs(t)
	target := t.TempDir() // a real dir already, forcing the per-skill branch

	orig := symlink
	symlink = func(string, string) error { return errors.New("injected: symlinks unavailable") }
	defer func() { symlink = orig }()

	res, err := PublishDirLink(dirs, target)
	if err != nil || res.Action != "copied" {
		t.Fatalf("publish: %+v %v", res, err)
	}

	linkPath := filepath.Join(target, "demo")
	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat copy: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("demo should be a real copy, not a symlink")
	}
	if got, err := os.ReadFile(filepath.Join(linkPath, "SKILL.md")); err != nil || string(got) != "demo body" {
		t.Fatalf("copy content: %q, %v", got, err)
	}
}

// TestPublishPerSkillReplacesOwnStaleLink is the one path that actually
// exercises the destructive branch on something Worklode created: a name
// already published, then re-installed at a new hash, must have its target
// link swapped to the new version — not left pointing at the old one, and
// not (incorrectly) treated as foreign and skipped.
func TestPublishPerSkillReplacesOwnStaleLink(t *testing.T) {
	dirs := installedDirs(t) // "demo" at its first hash
	target := t.TempDir()

	if _, err := PublishPerSkill(dirs, target); err != nil {
		t.Fatalf("initial publish: %v", err)
	}
	oldVersion, err := os.Readlink(filepath.Join(target, "demo"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}

	// Install a new version of the same skill: dirs.Links now points
	// elsewhere, so the target's link is ours but stale.
	files2 := map[string]string{"SKILL.md": "demo body v2"}
	arch2 := gzTar(t, files2)
	hash2 := hashFiles(files2)
	if _, err := Ensure(dirs, "demo", hash2, func() ([]byte, error) { return arch2, nil }); err != nil {
		t.Fatalf("ensure v2: %v", err)
	}

	res, err := PublishPerSkill(dirs, target)
	if err != nil || res.Action != "linked" {
		t.Fatalf("re-publish: %+v %v", res, err)
	}
	if len(res.Skips) != 0 {
		t.Fatalf("skips = %v, want none", res.Skips)
	}
	newVersion, err := os.Readlink(filepath.Join(target, "demo"))
	if err != nil {
		t.Fatalf("readlink after swap: %v", err)
	}
	if newVersion == oldVersion {
		t.Fatalf("stale link not replaced: still %s", newVersion)
	}
	got, err := os.ReadFile(filepath.Join(target, "demo", "SKILL.md"))
	if err != nil || string(got) != "demo body v2" {
		t.Fatalf("content after swap: %q, %v", got, err)
	}
}

// TestPublishDirLinkSkipsRealFile covers a plain file sitting where the dir
// link is expected to go: neither a symlink nor a directory, so it cannot
// be repointed or degraded into — skipped, untouched.
func TestPublishDirLinkSkipsRealFile(t *testing.T) {
	dirs := installedDirs(t)
	target := filepath.Join(t.TempDir(), "skills")
	if err := os.WriteFile(target, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	res, err := PublishDirLink(dirs, target)
	if err != nil || res.Action != "skipped" {
		t.Fatalf("publish: %+v %v", res, err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "not a dir" {
		t.Fatalf("target file disturbed: %q, %v", got, err)
	}
}

// TestPublishPerSkillSkipsDanglingNameLink covers a name symlink in
// dirs.Links whose target no longer resolves (EvalSymlinks fails): the name
// is reported in Skips and never gets a (broken) entry created for it in
// target.
func TestPublishPerSkillSkipsDanglingNameLink(t *testing.T) {
	dirs := installedDirs(t)
	if err := os.Symlink(filepath.Join("..", "store", "deadbeef"), filepath.Join(dirs.Links, "ghost")); err != nil {
		t.Fatalf("setup: %v", err)
	}
	target := t.TempDir()

	res, err := PublishPerSkill(dirs, target)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !reflect.DeepEqual(res.Skips, []string{"ghost"}) {
		t.Fatalf("skips = %v, want [ghost]", res.Skips)
	}
	if _, err := os.Lstat(filepath.Join(target, "ghost")); !os.IsNotExist(err) {
		t.Fatalf("ghost should not be created in target, lstat err=%v", err)
	}
}
