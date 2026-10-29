
`internal/transcript` parses Claude Code transcripts for historical import and has no live caller. An import posts to the same replacement endpoint, so a session already seen through OTel is replaced, never doubled. It classifies a transcript's turns by the working directory each was recorded in, resolved to a task worktree of this repository or to overhead, so the split in section 8 holds for imported data too.

