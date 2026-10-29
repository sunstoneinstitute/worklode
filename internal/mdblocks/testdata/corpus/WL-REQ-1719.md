
Id resolution is a separate question from the path guard (WL-REQ-270), paid only by events that cleared the guard: `git config --worktree --get worklode.task-id`, which `lode work next` stamps at creation, falling back to the first `[A-Z][A-Z0-9]*-[0-9]+` substring in the segment. The stamp needs `extensions.worktreeConfig` in the repo's local git config; `lode install` and `lode work next` both enable it. A stamped worktree outside the base is still invisible.

