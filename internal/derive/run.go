// Package derive implements spec 007's observed-layer derivers. Every
// deriver is a pure function producing the complete N-Triples document for
// its source; Run performs the shared contract — idempotent, full-replace,
// cheap to re-run, confined to one observed/* named graph.
package derive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
)

// Branch is the fixed graph-server branch the work graph lives on — the
// same value as projector.Branch (spec 006 §13.2 item 5).
const Branch = "main"

// unsafeInIRI are the characters an N-Triples/SPARQL IRIREF may not contain;
// a graph name carrying one could break out of the <...> in storedHash's query.
const unsafeInIRI = "<>\"{}|^`\\"

// checkGraphIRI rejects a graph name that cannot be safely interpolated into
// an IRIREF.
func checkGraphIRI(graphIRI string) error {
	if strings.ContainsAny(graphIRI, unsafeInIRI) ||
		strings.IndexFunc(graphIRI, unicode.IsSpace) >= 0 {
		return fmt.Errorf("unsafe graph IRI %q: must contain no whitespace and none of %s", graphIRI, unsafeInIRI)
	}
	return nil
}

// Result reports one deriver run.
type Result struct {
	Graph   string `json:"graph"`
	Hash    string `json:"hash"`
	Skipped bool   `json:"skipped"`
	Bytes   int    `json:"bytes"`
}

// HashOf returns the content hash Run compares against.
func HashOf(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// storedHash reads the hash triple of a graph's previous run ("" if none).
// The hash is a triple about the graph, inside the graph — replaced
// atomically with everything else, so no checkpoint table is needed.
// graphIRI must have passed checkGraphIRI before reaching the query.
func storedHash(ctx context.Context, c *graphserver.Client, graphIRI string) (string, error) {
	rows, err := c.Select(ctx, fmt.Sprintf(
		"SELECT ?h WHERE { GRAPH <%s> { <%s> <%s> ?h } }",
		graphIRI, graphIRI, graphproj.DCTIdentifier))
	if err != nil {
		return "", fmt.Errorf("read stored hash of %s: %w", graphIRI, err)
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0]["h"], nil
}

// Run applies the deriver contract: if the payload's hash matches the
// graph's stored hash the run is a no-op; otherwise the graph is atomically
// replaced (one GSP PUT via graphserver.PutGraph — N-Triples is a Turtle
// subset, so the rendered document is the payload as-is) by the payload plus
// a triple recording the new hash. Embedding the hash in the PUT body keeps
// the write a single atomic operation and works against graph-server's
// GSP-plus-read-only-SPARQL surface (spec 009) — no SPARQL Update is ever
// needed. Run never touches any graph other than graphIRI.
func Run(ctx context.Context, c *graphserver.Client, graphIRI string, payload []byte) (Result, error) {
	if err := checkGraphIRI(graphIRI); err != nil {
		return Result{}, err
	}
	hash := HashOf(payload)
	prev, err := storedHash(ctx, c, graphIRI)
	if err != nil {
		return Result{}, err
	}
	if prev == hash {
		return Result{Graph: graphIRI, Hash: hash, Skipped: true}, nil
	}
	doc := fmt.Sprintf("%s<%s> <%s> %q .\n", payload, graphIRI, graphproj.DCTIdentifier, hash)
	if _, err := c.PutGraph(ctx, Branch, graphIRI, []byte(doc)); err != nil {
		return Result{}, fmt.Errorf("replace %s: %w", graphIRI, err)
	}
	return Result{Graph: graphIRI, Hash: hash, Bytes: len(payload)}, nil
}
