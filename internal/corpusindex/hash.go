package corpusindex

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
)

// ContentHash hashes a subject's indexed text: kind plus every input string,
// each length-delimited so no concatenation of two inputs can collide with a
// different split of the same bytes. It is identical across every chunk row
// belonging to one subject (§5) — the convergence loop (§7) compares it
// against the live subject to decide what has gone stale.
func ContentHash(kind string, inputs ...string) string {
	h := sha256.New()
	writeField(h, kind)
	for _, in := range inputs {
		writeField(h, in)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeField hashes s prefixed by its own length, so ContentHash("k", "ab",
// "c") and ContentHash("k", "a", "bc") — same bytes concatenated differently
// — hash to different values.
func writeField(w io.Writer, s string) {
	var lenBuf [8]byte
	binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(s)))
	w.Write(lenBuf[:])
	io.WriteString(w, s)
}
