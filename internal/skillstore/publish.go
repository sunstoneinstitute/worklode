package skillstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/skillhash"
)

// symlink is os.Symlink, a var so a test can inject a failure to exercise
// the copy fallback (spec 008 §18 row 5) without a real symlink-less
// filesystem.
var symlink = os.Symlink

// PublishResult reports one publication step for install's report.
type PublishResult struct {
	Path   string   `json:"path"`
	Action string   `json:"action"` // linked | per-skill | copied | unchanged | skipped
	Skips  []string `json:"skips,omitempty"`
}

// PublishDirLink makes target a symlink to the whole links dir — one link
// serving every harness that reads target (spec 008 §17.3, the four
// ~/.agents/skills harnesses). An existing real directory degrades to
// per-skill links inside it (spec 008 §18 row 4: never replace a directory
// Worklode did not create); an existing foreign symlink is left untouched.
func PublishDirLink(dirs Dirs, target string) (PublishResult, error) {
	res := PublishResult{Path: target}
	info, err := os.Lstat(target)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return res, fmt.Errorf("publish dir link: %w", err)
		}
		if err := symlink(dirs.Links, target); err != nil {
			// Symlinks unavailable outright: fall back to the same
			// per-skill mechanism a pre-existing real dir uses.
			per, perr := PublishPerSkill(dirs, target)
			per.Action = "per-skill"
			return per, perr
		}
		res.Action = "linked"
		return res, nil
	case err != nil:
		return res, fmt.Errorf("publish dir link: %w", err)
	case info.Mode()&os.ModeSymlink != 0:
		cur, rerr := os.Readlink(target)
		if rerr != nil {
			return res, fmt.Errorf("publish dir link: %w", rerr)
		}
		if cur == dirs.Links {
			res.Action = "unchanged"
		} else {
			res.Action = "skipped" // foreign symlink: never repoint it
		}
		return res, nil
	case info.IsDir():
		per, perr := PublishPerSkill(dirs, target)
		per.Action = "per-skill"
		return per, perr
	default:
		res.Action = "skipped" // a plain file where a skills dir is expected: foreign, untouched
		return res, nil
	}
}

// PublishPerSkill links <target>/<name> to the resolved version dir for
// every name symlink in dirs.Links. For directories users own and populate
// themselves (~/.claude/skills), where replacing the directory would strip
// their own skills.
func PublishPerSkill(dirs Dirs, target string) (PublishResult, error) {
	res := PublishResult{Path: target}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return res, fmt.Errorf("publish per-skill: %w", err)
	}
	entries, err := os.ReadDir(dirs.Links)
	if err != nil {
		return res, fmt.Errorf("publish per-skill: %w", err)
	}

	var linked, copied, skipped bool
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 {
			continue // dirs.Links holds only name symlinks (spec 008 §17.3)
		}
		name := e.Name()
		versionDir, err := filepath.EvalSymlinks(filepath.Join(dirs.Links, name))
		if err != nil {
			res.Skips = append(res.Skips, name)
			skipped = true
			continue
		}
		linkPath := filepath.Join(target, name)
		action, ok, err := publishEntry(dirs, versionDir, linkPath)
		if err != nil {
			return res, fmt.Errorf("publish per-skill %s: %w", name, err)
		}
		if !ok {
			res.Skips = append(res.Skips, name)
			skipped = true
			continue
		}
		switch action {
		case "copied":
			copied = true
		case "linked":
			linked = true
		}
	}

	switch {
	case copied:
		res.Action = "copied"
	case linked:
		res.Action = "linked"
	case skipped:
		res.Action = "skipped"
	default:
		res.Action = "unchanged"
	}
	return res, nil
}

// publishEntry links or copies one skill's version dir into linkPath,
// honoring what is already there. Already correct: unchanged, silently.
// A foreign symlink or a real dir/file: skipped, never touched — never
// delete a path Worklode did not create (spec 008 §18 row 4). A symlink
// Worklode created itself (pointing inside dirs.Store, just to a stale
// version): replaced.
func publishEntry(dirs Dirs, versionDir, linkPath string) (action string, ok bool, err error) {
	info, err := os.Lstat(linkPath)
	switch {
	case os.IsNotExist(err):
		// nothing there yet: proceed to create below
	case err != nil:
		return "", false, err
	case info.Mode()&os.ModeSymlink != 0:
		cur, rerr := os.Readlink(linkPath)
		if rerr != nil {
			return "", false, rerr
		}
		if cur == versionDir {
			return "unchanged", true, nil
		}
		if !withinStore(cur, dirs.Store) {
			return "", false, nil // foreign symlink: skip, never repoint
		}
		// Ours, stale: fall through to replace.
	default:
		return "", false, nil // real dir or file: foreign, never delete
	}

	act, err := linkVersion(versionDir, linkPath)
	if err != nil {
		return "", false, err
	}
	return act, true, nil
}

// withinStore reports whether path resolves inside dirs.Store — the marker
// that a symlink was created by Worklode itself (nothing else points there),
// so it is safe to repoint even though it currently targets an old version.
func withinStore(path, store string) bool {
	rel, err := filepath.Rel(store, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// linkVersion atomically points linkPath at versionDir (an absolute path),
// replacing whatever publishEntry already determined is safe to remove. If
// symlinks are unavailable (spec 008 §18 row 5), it falls back to copying
// versionDir's content in full so the harness still sees the skill, and
// reports "copied" so a stale copy is diagnosable.
func linkVersion(versionDir, linkPath string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return "", err
	}
	suffix, err := randSuffix()
	if err != nil {
		return "", err
	}
	tmp := linkPath + ".tmp-" + suffix
	if err := symlink(versionDir, tmp); err != nil {
		if err := os.RemoveAll(linkPath); err != nil {
			return "", err
		}
		if err := copyDir(versionDir, linkPath); err != nil {
			return "", err
		}
		return "copied", nil
	}
	defer os.Remove(tmp) // no-op once rename consumes it; cleans up on failure
	if err := os.RemoveAll(linkPath); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, linkPath); err != nil {
		return "", err
	}
	return "linked", nil
}

// copyDir recursively copies src into dst, preserving the exec bit the same
// way extract does (skillhash.Mode) — the copy fallback for a filesystem
// where symlinks are unavailable (spec 008 §18 row 5). Symlink entries are
// skipped: extract never materializes them, so a version dir never has one.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		exec := info.Mode().Perm()&0o111 != 0
		return os.WriteFile(target, data, os.FileMode(skillhash.Mode(exec)))
	})
}
