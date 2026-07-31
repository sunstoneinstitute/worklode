// Package skillhash computes the content hash that identifies a skill
// version. It is a leaf package — stdlib only — so both the server-side
// syncer (internal/skillsync) and the client-side local cache
// (internal/skillstore) can depend on it without pulling each other's
// dependencies (store, githubauth, embed) into the other's binary.
package skillhash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// File is one entry contributing to a skill version's content hash.
type File struct {
	Path string
	Data []byte
	Exec bool
}

// Mode is the one mode bit that survives a sync: skills ship scripts, and
// an archive extracted without the executable bit fails at run time.
func Mode(exec bool) int64 {
	if exec {
		return 0o755
	}
	return 0o644
}

// Sum is sha256 over the sorted (path, length, mode, content) tuples —
// independent of archive encoding, so the hash never churns on tar details.
// The length makes the encoding self-delimiting, so no two distinct file
// sets can hash equal by shifting bytes across a boundary. The mode is in
// so an upstream chmod +x produces a new version. Order of the input slice
// does not matter: the result depends only on the set of files.
func Sum(files []File) string {
	sorted := make([]File, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	h := sha256.New()
	for _, f := range sorted {
		fmt.Fprintf(h, "%s\x00%d\x00%o\x00", f.Path, len(f.Data), Mode(f.Exec))
		h.Write(f.Data)
	}
	return hex.EncodeToString(h.Sum(nil))
}
