package model

// Markdown is a user-editable field rendered as Markdown (WL-RULE-1850). Every
// write of one re-extracts its inline references in the same transaction
// (WL-REQ-1852); internal/store's markdownWriters names the write path of each.
type Markdown string
