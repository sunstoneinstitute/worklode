// Package graphproj projects Worklode domain entities into RDF triples for
// the data-platform knowledge graph (spec 006 §11). A projector writes a
// whole named graph with a GSP replace, not incremental patches, so
// Document must render byte-identical output for the same triple set
// regardless of build order: determinism is what makes that replace
// idempotent.
package graphproj

import (
	"slices"
	"strings"
)

// termKind distinguishes an IRI reference from a literal.
type termKind int

const (
	kindIRI termKind = iota
	kindLiteral
)

// Term is an RDF term: either an IRI reference or a literal, optionally
// typed. Build one with IRIRef, Text, or Typed.
type Term struct {
	kind     termKind
	value    string
	datatype string // literal only; empty means plain (no ^^<...>)
}

// IRIRef builds an IRI reference term.
func IRIRef(iri string) Term {
	return Term{kind: kindIRI, value: iri}
}

// Text builds a plain (untyped) literal term.
func Text(value string) Term {
	return Term{kind: kindLiteral, value: value}
}

// Typed builds a literal term with an explicit datatype IRI.
func Typed(value, datatypeIRI string) Term {
	return Term{kind: kindLiteral, value: value, datatype: datatypeIRI}
}

// escapeLiteral escapes a literal's lexical form per N-Triples STRING_LITERAL_QUOTE.
// Backslash must be replaced first so its escaped form isn't re-escaped by
// the replacements that follow.
var escapeLiteral = strings.NewReplacer(
	`\`, `\\`,
	`"`, `\"`,
	"\n", `\n`,
	"\r", `\r`,
	"\t", `\t`,
).Replace

// String renders the term in N-Triples form: "<iri>" for an IRI reference,
// `"text"` for a plain literal, `"text"^^<datatype>` for a typed one.
func (t Term) String() string {
	if t.kind == kindIRI {
		return "<" + t.value + ">"
	}
	lit := `"` + escapeLiteral(t.value) + `"`
	if t.datatype != "" {
		lit += "^^<" + t.datatype + ">"
	}
	return lit
}

// Triple is a single RDF statement: subject and predicate are IRIs, object
// is any Term.
type Triple struct {
	S, P string
	O    Term
}

// String renders the triple as one N-Triples line, without a trailing
// newline: "<S> <P> O .".
func (t Triple) String() string {
	return "<" + t.S + "> <" + t.P + "> " + t.O.String() + " ."
}

// Document renders triples as a GSP-PUT-ready N-Triples document: one
// triple per line, sorted lexicographically by the rendered line, with
// exact duplicate lines dropped. Sorting happens after rendering, so the
// same triples in any order, with any duplicates, produce byte-identical
// output.
func Document(triples []Triple) []byte {
	lines := make([]string, len(triples))
	for i, t := range triples {
		lines[i] = t.String()
	}
	slices.Sort(lines)
	lines = slices.Compact(lines)

	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
