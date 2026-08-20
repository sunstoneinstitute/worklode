package skillstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// installedDirs returns a fresh Dirs with one skill ("demo") installed, for
// tests that publish from an already-populated links dir.
func installedDirs(t *testing.T) Dirs {
	t.Helper()
	dirs := testDirs(t)
	files := map[string]string{"SKILL.md": "demo body"}
	arch := gzTar(t, files)
	hash := hashFiles(files)
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
}
