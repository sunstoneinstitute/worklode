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
	"strings"
)

// MaxEntries caps the number of files in one skill version. Bytes alone do
// not stop an archive of many empty files from exhausting inodes. It lives
// here because both sides must agree: a dir the server ingests but no client
// will extract is a skill nobody can install.
const MaxEntries = 2000

// ValidName reports whether name can serve as a skill's org-unique
// identifier. It becomes a path segment in the local store and a URL segment
// on GET /api/v1/skills/{name}, so separators, the dot dirs, and any leading
// dot are out. Enforced at ingest and again at extract, for the same reason
// as MaxEntries.
func ValidName(name string) bool {
	return name != "" && !strings.ContainsAny(name, `/\`) &&
		name != "." && name != ".." && !strings.HasPrefix(name, ".")
}

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
