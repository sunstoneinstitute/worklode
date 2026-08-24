// Package worktree maps Worklode task identity onto git worktrees: the
// <base>/<branch> directory layout, the fallback branch name, and the lease
// identity string the backbone stores.
package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/gitexec"
)

// DefaultBase is the worktree base directory used when worktree_dir /
// LODE_WORKTREE_DIR is unset (spec 008 §5.1).
const DefaultBase = ".worktrees"

// idRe matches a task id anywhere in the worktree's directory name. The base
// directory is the guard; this only extracts (spec 008 §5.2).
var idRe = regexp.MustCompile(`[A-Z][A-Z0-9]*-[0-9]+`)

// Layout is the resolved worktree directory layout for a checkout. Construct
// it with NewLayout — the zero value has no base and rejects every path.
type Layout struct {
	base  string   // slash-separated, as configured
	parts []string // base split into segments
}

// NewLayout validates a configured base directory. It is interpreted relative
// to the git root, so an absolute path or one escaping the root is refused.
func NewLayout(base string) (Layout, error) {
	if base == "" {
		base = DefaultBase
	}
	// IsAbs before trimming: trimming "/" off "/abs/path" would make an
	// absolute path look relative.
	if filepath.IsAbs(base) || strings.HasPrefix(base, "/") {
		return Layout{}, fmt.Errorf("worktree dir %q must be a path relative to the repository root, not an absolute path", base)
	}
	base = strings.Trim(filepath.ToSlash(strings.TrimSpace(base)), "/")
	if base == "" {
		return Layout{}, fmt.Errorf("worktree dir must not be empty")
	}
	parts := strings.Split(base, "/")
	for _, p := range parts {
		if p == "" {
			return Layout{}, fmt.Errorf("worktree dir %q must not contain empty segments (e.g. a doubled %q)", base, "/")
		}
		if p == "." || p == ".." {
			return Layout{}, fmt.Errorf("worktree dir %q must not contain %q or %q segments", base, ".", "..")
		}
	}
	return Layout{base: base, parts: parts}, nil
}

// Base returns the configured base directory, relative to the git root.
func (l Layout) Base() string { return l.base }

// DirName is the directory a branch gets under the base directory. The layout
// is flat, so a "/" from a namespaced template ("team/{{ .id }}-{{ .slug }}")
// is flattened to "-": every worktree is one directory below the base, and
// under the default template the directory name is the branch name verbatim
// (spec 008 §5.1).
func DirName(branch string) string { return strings.ReplaceAll(branch, "/", "-") }

// Dir returns the worktree directory for a branch: <root>/<base>/<dirname>.
//
// Dir panics on a zero Layout: a silently wrong worktree path (dropping the
// base entirely) is far worse than a loud crash — construct with NewLayout.
func (l Layout) Dir(root, branch string) string {
	if len(l.parts) == 0 {
		panic("worktree: Dir called on zero Layout; construct with NewLayout")
	}
	return filepath.Join(root, filepath.FromSlash(l.base), DirName(branch))
}

// ParseDir returns the task id when path is a directory immediately below the
// base directory whose name carries an id. The layout is flat, so exactly one
// segment below the base counts: anything deeper is a path inside a worktree
// (or someone else's directory), not a worktree root. This is the uniform hook
// guard: ok=false ⇒ NOP.
func (l Layout) ParseDir(path string) (taskID string, ok bool) {
	seg, ok := l.segmentBelowBase(path)
	if !ok {
		return "", false
	}
	id := idRe.FindString(seg)
	if id == "" {
		return "", false
	}
	return id, true
}

// segmentBelowBase is the guard half of ParseDir: the single directory name
// one level below the base directory, or ok=false. It is a pure string
// operation — no config, no subprocess — which is what lets it run on every
// hook event (spec 008 §5.2).
func (l Layout) segmentBelowBase(path string) (string, bool) {
	if len(l.parts) == 0 {
		return "", false
	}
	segs := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	idx := lastIndexOf(segs, l.parts)
	if idx < 0 {
		return "", false
	}
	below := segs[idx+len(l.parts):]
	if len(below) != 1 {
		return "", false
	}
	return below[0], true
}

// WorktreeRootOf returns the worktree root a path sits at or below: the base
// plus the one segment under it, with anything deeper trimmed off. Where
// segmentBelowBase is the hook *guard* — exactly one segment, so a path inside
// a worktree is rejected — this is for classifying a recorded working
// directory, which is routinely a subdirectory of the worktree it belongs to.
// Pure string work, like its sibling.
func (l Layout) WorktreeRootOf(path string) (string, bool) {
	if len(l.parts) == 0 || path == "" {
		return "", false
	}
	segs := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	idx := lastIndexOf(segs, l.parts)
	if idx < 0 {
		return "", false
	}
	end := idx + len(l.parts) + 1
	if end > len(segs) {
		return "", false
	}
	return filepath.FromSlash(strings.Join(segs[:end], "/")), true
}

// lastIndexOf returns the starting index of the last occurrence of sub in
// segs, or -1. The *last* occurrence wins so a repository that itself sits
// inside someone's worktree base still resolves against its own.
func lastIndexOf(segs, sub []string) int {
	for i := len(segs) - len(sub); i >= 0; i-- {
		if slices.Equal(segs[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// Root walks up from dir to the enclosing git worktree root
// (git -C dir rev-parse --show-toplevel); ok=false outside a repo.
func Root(dir string) (string, bool) {
	return gitexec.Line(dir, "rev-parse", "--show-toplevel")
}

// MainRoot walks up from dir to the root of the *main* worktree — the
// checkout that owns the shared git directory — rather than to dir's own
// worktree root. In the main checkout the two are the same; inside a linked
// worktree (`.worktrees/<id>-<slug>/`) they differ, and it is the main
// checkout that answers for anything repo-wide rather than workspace-local.
//
// This is the instruction-file analogue of what githooks.Dir already does for
// hooks: `rev-parse --git-path hooks` resolves to the shared hooks directory
// common to every worktree, so hooks installed from a task worktree land once,
// for the whole repo. Repo-root files (AGENTS.md, CLAUDE.md) are tracked
// content, so anchoring them at a linked worktree's own root would dirty that
// worktree's branch with a change the task never asked for (WL-219).
//
// The main root is derived from the common git dir — <main>/.git — one level
// up, and then verified: the candidate must itself be a worktree root whose
// own git dir *is* that common dir. A layout where that does not hold (a bare
// clone, `--separate-git-dir`) falls back to dir's own root, which is the
// conservative answer rather than a path outside the repo. ok=false only
// outside a repo, matching Root.
func MainRoot(dir string) (string, bool) {
	root, ok := Root(dir)
	if !ok {
		return "", false
	}
	common, ok := gitexec.Line(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if !ok {
		return root, true
	}
	gitDir, ok := gitexec.Line(dir, "rev-parse", "--absolute-git-dir")
	if !ok {
		return root, true
	}
	if filepath.Clean(common) == filepath.Clean(gitDir) {
		return root, true // dir is the main worktree
	}
	candidate := filepath.Dir(filepath.Clean(common))
	mainRoot, ok := Root(candidate)
	if !ok {
		return root, true
	}
	mainGitDir, ok := gitexec.Line(mainRoot, "rev-parse", "--absolute-git-dir")
	if !ok || filepath.Clean(mainGitDir) != filepath.Clean(common) {
		return root, true
	}
	return mainRoot, true
}

// Identity returns "<hostname>:<abs path>" — the lease worktree identity.
// It resolves path to its git worktree root first, so any directory inside a
// worktree yields the same stable identity. Fails outside a git worktree.
func Identity(path string) (string, error) {
	root, err := gitexec.Text(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git worktree: %w", path, err)
	}
	return IdentityOf(root)
}

// IdentityOf is Identity for a caller that already holds the worktree root
// (from Root, say): it formats the identity without forking git a second time
// for an answer it has. root must be a worktree root — it is trusted, not
// resolved.
func IdentityOf(root string) (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("determine hostname: %w", err)
	}
	return host + ":" + root, nil
}

// GitDir returns the worktree-private git dir (rev-parse --git-dir, abs).
func GitDir(root string) (string, error) {
	dir, err := gitexec.Text(root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git worktree: %w", root, err)
	}
	return dir, nil
}

// EnableWorktreeConfigExtension turns on extensions.worktreeConfig in root's
// own local git config, idempotently. This must happen before a repo grows a
// second worktree: git refuses `config --worktree` across multiple
// worktrees unless the *local* repo config (not global — verified: a
// global-only setting is silently ignored for this check) already has the
// extension enabled. `lode install` calls this once per repo, and `lode
// next` calls it again defensively right before creating a worktree, so
// stamping works even in a repo nobody ran `lode install` in.
//
// It refuses to act on a bare repository or one with core.worktree set.
// Enabling the extension changes how git scopes core.bare and core.worktree —
// git's own enabler migrates those two keys into the main worktree's
// config.worktree first, and skipping that migration silently breaks every
// existing linked worktree of a bare clone ("fatal: this operation must be run
// in a work tree"). Rather than perform that migration, this returns an error
// and leaves the repo alone; callers treat the failure as a warning, and
// TaskID's directory-name fallback keeps working there.
func EnableWorktreeConfigExtension(root string) error {
	if bare, ok := gitexec.Line(root, "rev-parse", "--is-bare-repository"); ok && bare == "true" {
		return fmt.Errorf("refusing to enable extensions.worktreeConfig on the bare repository %s: "+
			"it requires migrating core.bare/core.worktree into the main worktree's config.worktree first, "+
			"which would otherwise break existing linked worktrees", root)
	}
	if _, ok := gitexec.Line(root, "config", "--get", "core.worktree"); ok {
		return fmt.Errorf("refusing to enable extensions.worktreeConfig on %s: core.worktree is set, "+
			"so the extension requires migrating core.bare/core.worktree into the main worktree's "+
			"config.worktree first, which would otherwise break existing linked worktrees", root)
	}
	return gitexec.Run(root, "config", "extensions.worktreeConfig", "true")
}

// SetTaskID stamps dir's own worktree-private git config with its task id,
// under the worklode.task-id key, so TaskID can resolve it explicitly instead of
// parsing the directory name. Once a repo has more than one worktree, this
// requires EnableWorktreeConfigExtension to have already run against it —
// callers should treat a failure here as a warning, not fatal: TaskID's
// ParseDir fallback keeps working from the directory name regardless.
func SetTaskID(dir, taskID string) error {
	return gitexec.Run(dir, "config", "--worktree", "worklode.task-id", taskID)
}

// UnsetTaskID removes dir's worklode.task-id stamp, so a worktree that
// outlives the lease it was created for stops claiming that binding. Callers
// invoke it where a binding ends but the directory survives (`done`, `block`,
// a release naming this worktree's task, a rolled-back claim); a worktree that
// is removed outright needs no call, because the stamp lives in the worktree's
// own config file and goes with it.
//
// Clearing is safe to do eagerly: under the standard <base>/<branch> layout
// TaskID falls back to the id in the directory name, so unsetting degrades
// resolution to the path rule rather than unbinding the worktree entirely.
//
// A missing key is not an error — git exits 5 for "unset an option which does
// not exist" — so this is idempotent. Like SetTaskID, callers should treat a
// genuine failure as a warning rather than fatal.
func UnsetTaskID(dir string) error {
	err := gitexec.Run(dir, "config", "--worktree", "--unset", "worklode.task-id")
	if err == nil || gitexec.ExitCode(err) == 5 {
		return nil
	}
	return err
}

// StampedTaskID reads the worklode.task-id SetTaskID stamped on dir's own
// worktree, with none of TaskID's layout guard: it answers "is this workspace
// bound to a task", which is a different question from "is this one of our
// worktrees" and has callers — the status line — that want the first without
// the second.
//
// A repo where extensions.worktreeConfig was never enabled fails the read
// rather than answering wrongly, which is the right degradation: the stamp
// cannot be worktree-scoped there, so there is nothing trustworthy to report.
func StampedTaskID(dir string) (taskID string, ok bool) {
	return gitexec.Line(dir, "config", "--worktree", "--get", "worklode.task-id")
}

// TaskID resolves the task id of a worktree root, preferring the explicit
// worklode.task-id worktree config SetTaskID stamps over the id carried by the
// directory name. Explicit wins, so a worktree renamed after creation — to
// something the id pattern no longer matches — still resolves; the
// directory-name fallback keeps worktrees created before this field existed
// (or without extensions.worktreeConfig enabled) working unchanged.
//
// The guard is unchanged and still runs first: dir must be exactly one
// directory below the base, on strings alone. Only once it has cleared that
// does TaskID spend a git subprocess, so the reject-fast path every hook
// event takes stays free of one (spec 008 §5.2).
func (l Layout) TaskID(dir string) (taskID string, ok bool) {
	seg, ok := l.segmentBelowBase(dir)
	if !ok {
		return "", false
	}
	if id, ok := StampedTaskID(dir); ok {
		return id, true
	}
	id := idRe.FindString(seg)
	if id == "" {
		return "", false
	}
	return id, true
}

// CurrentBranch returns the branch checked out at root. A detached HEAD is
// an error: the doc-sync gate (spec 025 §16.2) needs a branch to compare and to
// record as provenance.
func CurrentBranch(root string) (string, error) {
	branch, err := gitexec.Text(root, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve current branch (detached HEAD?): %w", err)
	}
	return branch, nil
}

// DefaultBranch returns the repository's default branch as recorded by the
// remote's HEAD (spec 025 §16.2), read from refs/remotes/origin/HEAD — local
// state git clone writes, so no network round trip. A repo without it (an
// old clone, or `git init` with a remote added by hand) gets an error naming
// the fix.
func DefaultBranch(root string) (string, error) {
	ref, err := gitexec.Text(root, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("no default branch recorded for origin; run `git remote set-head origin --auto` (needs network) and retry")
	}
	return strings.TrimPrefix(ref, "refs/remotes/origin/"), nil
}

// ExcludeFile returns the repo's info/exclude path (creating its parent),
// via `git rev-parse --git-path info/exclude` — which resolves to the
// common dir for linked worktrees, exactly where per-machine excludes
// belong.
func ExcludeFile(root string) (string, error) {
	p, err := gitexec.Text(root, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git worktree: %w", root, err)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return p, nil
}

// IsClean reports whether root's working tree has no uncommitted changes,
// untracked files included — `git status --porcelain` prints nothing (025 §16.2).
func IsClean(root string) (bool, error) {
	out, err := gitexec.Text(root, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out == "", nil
}
