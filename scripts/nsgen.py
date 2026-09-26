#!/usr/bin/env python3
"""Generate internal/ns/gen.go from ns/concept.ttl and ns/ontology.ttl.

ns/concept.ttl is the source of the enums that also appear as CHECK
constraints and as Go literals (025 §17). ns/ontology.ttl is the source of
the stored edge types, read from its `wl:edgeOrigin`/`wl:storedAs`
annotations (WL-SPEC-77 §8.1). This makes the Turtle the one that is typed by
hand and the Go the one that is derived, so a kind, a status or an edge type
cannot be added in one place and forgotten in the other.

Stdlib only, deliberately. The obvious implementation imports rdflib, but that
is not installable on a PEP 668 distro without a venv or --break-system-
packages, which would put a setup step between an editor of ns/*.ttl and a
regenerate. Instead this parses the Turtle subset the ns/ files actually use
and raises on anything it does not understand, so an unparsed construct fails
loudly rather than silently dropping a concept.

Usage:
    scripts/nsgen.py            # write internal/ns/gen.go
    scripts/nsgen.py --check    # exit 1 with a diff if the file is stale
"""

from __future__ import annotations

import argparse
import difflib
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CONCEPT_TTL = ROOT / "ns" / "concept.ttl"
ONTOLOGY_TTL = ROOT / "ns" / "ontology.ttl"
GEN_GO = ROOT / "internal" / "ns" / "gen.go"

SKOS = "http://www.w3.org/2004/02/skos/core#"
RDF_TYPE = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
RDF_NIL = "http://www.w3.org/1999/02/22-rdf-syntax-ns#nil"
RDF_FIRST = "http://www.w3.org/1999/02/22-rdf-syntax-ns#first"
RDF_REST = "http://www.w3.org/1999/02/22-rdf-syntax-ns#rest"
OWL = "http://www.w3.org/2002/07/owl#"
WL = "https://worklode.io/ns/ontology#"
WLC = "https://worklode.io/ns/concept/"


class TurtleError(Exception):
    """The input used a construct this parser does not implement."""


# --------------------------------------------------------------------------
# Turtle subset parser
# --------------------------------------------------------------------------

_TOKEN_RE = re.compile(
    r"""
      (?P<ws>\s+)
    | (?P<comment>\#[^\n]*)
    | (?P<longstr>\"\"\"(?:[^\"\\]|\\.|\"(?!\"\"))*\"\"\")
    | (?P<str>\"(?:[^\"\\\n]|\\.)*\")
    | (?P<iri><[^<>\"{}|^`\\\s]*>)
    | (?P<directive>@[A-Za-z]+)
    | (?P<caret>\^\^)
    | (?P<punct>[.;,()\[\]])
    # A prefixed name's local part may contain '.' but not end in one, or
    # `wlc:TaskKind.` would parse as a name and swallow the statement's
    # terminator - dropping a concept with no error.
    | (?P<pname>[A-Za-z_][\w-]*:(?:[\w\-%](?:[\w.\-%]*[\w\-%])?)?)
    | (?P<word>[A-Za-z][\w-]*)
    | (?P<number>[+-]?[\d.]+)
    """,
    re.VERBOSE,
)


def tokenize(text: str) -> list[tuple[str, str]]:
    """Split Turtle into (kind, text) tokens, dropping whitespace/comments."""
    tokens: list[tuple[str, str]] = []
    pos = 0
    while pos < len(text):
        m = _TOKEN_RE.match(text, pos)
        if not m:
            line = text.count("\n", 0, pos) + 1
            raise TurtleError(f"line {line}: cannot tokenize {text[pos:pos + 30]!r}")
        pos = m.end()
        kind = m.lastgroup
        assert kind is not None
        if kind in ("ws", "comment"):
            continue
        tokens.append((kind, m.group()))
    return tokens


class Parser:
    """Parses the Turtle subset used by ns/*.ttl into a triple list.

    Supported: @prefix/@base directives, IRIs, prefixed names, `a`, string
    literals (short and long, with optional language tag or ^^datatype),
    predicate-object lists (`;`), object lists (`,`), RDF collections
    (`( ... )`), and blank node property lists (`[]`, `[ p o ; ... ]`).

    A literal is returned as its quoted source text minus any language tag or
    datatype, so an IRI can never be mistaken for one.

    Everything else raises TurtleError: bare numeric and boolean literals,
    SPARQL-style PREFIX/BASE, the default prefix (`:x`), and - the one a human might actually write - a language
    tag with a subtag, `"hi"@en-GB`.
    """

    def __init__(self, text: str) -> None:
        self.tokens = tokenize(text)
        self.i = 0
        self.prefixes: dict[str, str] = {}
        self.base = ""
        self.triples: list[tuple[str, str, str]] = []
        self._bnode = 0

    # -- token helpers ----------------------------------------------------

    def peek(self) -> tuple[str, str] | None:
        return self.tokens[self.i] if self.i < len(self.tokens) else None

    def next(self) -> tuple[str, str]:
        tok = self.peek()
        if tok is None:
            raise TurtleError("unexpected end of input")
        self.i += 1
        return tok

    def expect(self, text: str) -> None:
        kind, got = self.next()
        if got != text:
            raise TurtleError(f"expected {text!r}, got {got!r}")

    def fresh_bnode(self) -> str:
        self._bnode += 1
        return f"_:b{self._bnode}"

    # -- terms ------------------------------------------------------------

    def resolve_pname(self, pname: str) -> str:
        prefix, _, local = pname.partition(":")
        if prefix not in self.prefixes:
            raise TurtleError(f"unknown prefix {prefix + ':'!r} in {pname!r}")
        return self.prefixes[prefix] + local

    def term(self) -> str:
        kind, text = self.next()
        if kind == "iri":
            inner = text[1:-1]
            return inner if ":" in inner else self.base + inner
        if kind == "pname":
            return self.resolve_pname(text)
        if kind == "word" and text == "a":
            return RDF_TYPE
        if kind in ("str", "longstr"):
            nxt = self.peek()
            if nxt and nxt[1].startswith("@"):
                self.next()
            elif nxt and nxt[1] == "^^":
                self.next()
                self.term()
            return text
        if kind == "punct" and text == "(":
            return self.collection()
        if kind == "punct" and text == "[":
            node = self.fresh_bnode()
            tok = self.peek()
            if tok and tok[1] == "]":
                self.next()
                return node
            self.predicate_objects(node)
            self.expect("]")
            return node
        raise TurtleError(f"unsupported term {text!r}")

    def collection(self) -> str:
        """Expand `( a b c )` into rdf:first/rdf:rest cells; returns the head."""
        items: list[str] = []
        while True:
            tok = self.peek()
            if tok is None:
                raise TurtleError("unterminated collection")
            if tok[1] == ")":
                self.next()
                break
            items.append(self.term())
        if not items:
            return RDF_NIL
        cells = [self.fresh_bnode() for _ in items]
        for n, (cell, item) in enumerate(zip(cells, items)):
            rest = cells[n + 1] if n + 1 < len(cells) else RDF_NIL
            self.triples.append((cell, RDF_FIRST, item))
            self.triples.append((cell, RDF_REST, rest))
        return cells[0]

    # -- statements -------------------------------------------------------

    def parse(self) -> list[tuple[str, str, str]]:
        while self.peek() is not None:
            kind, text = self.peek()  # type: ignore[misc]
            if kind == "directive":
                self.directive()
            else:
                self.statement()
        return self.triples

    def directive(self) -> None:
        _, name = self.next()
        if name == "@prefix":
            _, pname = self.next()
            self.prefixes[pname.rstrip(":")] = self.term()
        elif name == "@base":
            self.base = self.term()
        else:
            raise TurtleError(f"unsupported directive {name!r}")
        self.expect(".")

    def statement(self) -> None:
        self.predicate_objects(self.term())
        self.expect(".")

    def predicate_objects(self, subject: str) -> None:
        while True:
            predicate = self.term()
            while True:
                self.triples.append((subject, predicate, self.term()))
                if self.peek() and self.peek()[1] == ",":  # type: ignore[index]
                    self.next()
                    continue
                break
            tok = self.peek()
            if tok and tok[1] == ";":
                self.next()
                # A trailing `;` before `.` is legal Turtle.
                nxt = self.peek()
                if nxt and nxt[1] in (".", "]"):
                    break
                continue
            break


# --------------------------------------------------------------------------
# Extraction
# --------------------------------------------------------------------------


def local_name(iri: str, namespace: str) -> str:
    if not iri.startswith(namespace):
        raise TurtleError(f"{iri!r} is not in {namespace!r}")
    return iri[len(namespace):]


def scheme_members(triples, scheme: str) -> set[str]:
    """Concepts declared `skos:inScheme <scheme>`, checked to be skos:Concept."""
    members = {s for s, p, o in triples if p == SKOS + "inScheme" and o == scheme}
    typed = {s for s, p, o in triples if p == RDF_TYPE and o == SKOS + "Concept"}
    untyped = members - typed
    if untyped:
        raise TurtleError(
            f"in scheme {scheme}: not declared `a skos:Concept`: {sorted(untyped)}"
        )
    if not members:
        raise TurtleError(f"scheme {scheme} has no members")
    return members


def check_no_orphan_concepts(triples) -> None:
    """Every skos:Concept must sit in a declared scheme via skos:inScheme.

    Without this the generator is silent about the mistakes that actually
    happen: a misspelled `skos:inscheme`, a scheme name accidentally quoted,
    or a concept attached only by `skos:hasTopConcept`. Each leaves a real
    concept out of its scheme's member set, and a member set is a Go slice
    that a CHECK constraint is supposed to match — so a drop must be an error,
    not a shorter list.
    """
    concepts = {s for s, p, o in triples if p == RDF_TYPE and o == SKOS + "Concept"}
    schemes = {s for s, p, o in triples if p == RDF_TYPE and o == SKOS + "ConceptScheme"}
    placed = {
        s for s, p, o in triples
        if p == SKOS + "inScheme" and o in schemes
    }
    orphans = concepts - placed
    if orphans:
        raise TurtleError(
            "skos:Concept with no `skos:inScheme <a declared scheme>`: "
            f"{sorted(orphans)}"
        )


def ordered_list(triples, head: str) -> list[str]:
    firsts = {s: o for s, p, o in triples if p == RDF_FIRST}
    rests = {s: o for s, p, o in triples if p == RDF_REST}
    out: list[str] = []
    seen: set[str] = set()
    node = head
    while node != RDF_NIL:
        if node not in firsts:
            raise TurtleError(f"malformed collection at {node}")
        if node in seen:
            # Unreachable via `( ... )`, which mints fresh cells; possible if
            # someone writes rdf:rest by hand. Raise rather than spin.
            raise TurtleError(f"cyclic rdf:rest chain at {node}")
        seen.add(node)
        out.append(firsts[node])
        node = rests[node]
    return out


def all_schemes(triples, wlc: str) -> dict[str, list[str]]:
    """Every wlc: concept scheme mapped to its members' local names, sorted."""
    schemes = sorted(
        s for s, p, o in triples
        if p == RDF_TYPE and o == SKOS + "ConceptScheme"
    )
    return {
        local_name(s, wlc): sorted(
            local_name(m, wlc) for m in scheme_members(triples, s)
        )
        for s in schemes
    }


def extract(ttl: str) -> tuple[dict[str, list[str]], list[str]]:
    parser = Parser(ttl)
    triples = parser.parse()
    if "wlc" not in parser.prefixes:
        raise TurtleError("no `@prefix wlc:` declared")
    wlc = parser.prefixes["wlc"]
    check_no_orphan_concepts(triples)

    schemes = all_schemes(triples, wlc)

    status_scheme = wlc + "DesignDocStatus"
    status_set = scheme_members(triples, status_scheme)
    order_heads = [
        o for s, p, o in triples
        if s == wlc + "DesignDocStatusOrder" and p == SKOS + "memberList"
    ]
    if len(order_heads) != 1:
        raise TurtleError(
            f"wlc:DesignDocStatusOrder needs exactly one skos:memberList, got {len(order_heads)}"
        )
    ordered = ordered_list(triples, order_heads[0])
    # Length as well as membership: a duplicate entry has the same set as the
    # scheme, and would emit a Go slice with a repeated status.
    if len(ordered) != len(status_set) or set(ordered) != status_set:
        raise TurtleError(
            "wlc:DesignDocStatusOrder does not list exactly the scheme's members, once each: "
            f"list={ordered} scheme={sorted(status_set)}"
        )
    statuses = [local_name(o, wlc) for o in ordered]
    return schemes, statuses


EDGE_TABLES = ("doc_edges", "rule_edges", "task_edges")
_STORED_AS_RE = re.compile(r"^(doc_edges|rule_edges|task_edges)\.[A-Za-z_]+$")


def extract_edges(ttl: str) -> list[tuple[str, str, str, str, bool]]:
    """Stored edge types from ontology.ttl, sorted by table then type.

    Each entry is (table, type, property IRI, inverse IRI or "", symmetric).
    A declared property is stored, one entry per `wl:storedAs` literal; an
    inferred one is only ever derived as the `owl:inverseOf` of a declared one.
    """
    triples = Parser(ttl).parse()
    origins: dict[str, str] = {}
    for s, p, o in triples:
        if p != WL + "edgeOrigin":
            continue
        if o not in (WLC + "declared", WLC + "inferred"):
            raise TurtleError(f"{s}: wl:edgeOrigin {o!r} is neither wlc:declared nor wlc:inferred")
        origins[s] = o
    declared = {s for s, o in origins.items() if o == WLC + "declared"}
    stored: dict[str, list[str]] = {}
    for s, p, o in triples:
        if p == WL + "storedAs":
            stored.setdefault(s, []).append(o)
    inverse_of: dict[str, list[str]] = {}
    for s, p, o in triples:
        if p == OWL + "inverseOf":
            inverse_of.setdefault(s, []).append(o)
    symmetric = {s for s, p, o in triples if p == RDF_TYPE and o == OWL + "SymmetricProperty"}

    inverses: dict[str, str] = {}
    for s, o in origins.items():
        if o != WLC + "inferred":
            continue
        if s in stored:
            raise TurtleError(f"{s}: an inferred property carries wl:storedAs")
        targets = [t for t in inverse_of.get(s, []) if t in declared]
        if not targets:
            raise TurtleError(f"{s}: an inferred property needs owl:inverseOf a declared property")
        for t in targets:
            inverses.setdefault(t, s)

    edges: dict[tuple[str, str], tuple[str, str, str, str, bool]] = {}
    for prop in sorted(declared):
        if prop not in stored:
            raise TurtleError(f"{prop}: a declared property has no wl:storedAs")
        for lit in stored[prop]:
            value = lit[1:-1] if lit.startswith('"') else lit
            if not _STORED_AS_RE.match(value):
                raise TurtleError(f"{prop}: wl:storedAs {lit} is not <edge table>.<type>")
            table, _, typ = value.partition(".")
            if (table, typ) in edges:
                raise TurtleError(f"{table}.{typ} is stored by both {edges[table, typ][2]} and {prop}")
            edges[table, typ] = (table, typ, prop, inverses.get(prop, ""), prop in symmetric)
    return [edges[k] for k in sorted(edges)]


# --------------------------------------------------------------------------
# Emission
# --------------------------------------------------------------------------

TEMPLATE = '''// Code generated by scripts/nsgen.py from ns/concept.ttl and ns/ontology.ttl. DO NOT EDIT.

// Package ns exposes the concept schemes of ns/concept.ttl as Go values.
//
// ns/ owns the shared schema (025 §17), so an enum change is a change to the
// Turtle first, then `scripts/nsgen.py` to regenerate, then the migration
// that moves the matching CHECK constraint — in one commit.
package ns

// Schemes is every wlc: concept scheme, keyed by its local name, members
// sorted. Most schemes have no Go caller; they are here so
// internal/store/nsenums_test.go can hold each one against the CHECK
// constraint that is supposed to list it, which is the leg of 025 §17 that
// nothing else checks.
var Schemes = map[string][]string{
%s}

// TaskKinds mirrors wlc:TaskKind and the tasks.kind CHECK constraint,
// alphabetically.
var TaskKinds = Schemes["TaskKind"]

// DesignDocStatuses mirrors wlc:DesignDocStatus and the docs.status CHECK
// constraint, in the lifecycle order of wlc:DesignDocStatusOrder.
var DesignDocStatuses = []string{%s}

// EdgeTerms is every stored edge type of ns/ontology.ttl, sorted by table
// then type.
var EdgeTerms = []EdgeTerm{
%s}
'''


def render(schemes: dict[str, list[str]], statuses: list[str], edges) -> str:
    def lit(values: list[str]) -> str:
        return ", ".join(f'"{v}"' for v in values)

    # Key column padded so the output is already gofmt-stable; a generator
    # whose output gofmt would rewrite makes --check fail on a clean tree.
    width = max((len(name) for name in schemes), default=0) + 4
    table = "".join(
        f'\t{(chr(34) + name + chr(34) + ":").ljust(width)}{{{lit(members)}}},\n'
        for name, members in sorted(schemes.items())
    )
    terms = "".join(
        f'\t{{Table: "{t}", Type: "{typ}", Property: "{prop}", Inverse: "{inv}", Symmetric: {str(sym).lower()}}},\n'
        for t, typ, prop, inv, sym in edges
    )
    return TEMPLATE % (table, lit(statuses), terms)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "--check",
        action="store_true",
        help="exit 1 with a diff if internal/ns/gen.go is stale",
    )
    args = ap.parse_args()

    try:
        schemes, statuses = extract(CONCEPT_TTL.read_text(encoding="utf-8"))
    except (TurtleError, OSError) as exc:
        print(f"{CONCEPT_TTL.relative_to(ROOT)}: {exc}", file=sys.stderr)
        return 1
    try:
        edges = extract_edges(ONTOLOGY_TTL.read_text(encoding="utf-8"))
    except (TurtleError, OSError) as exc:
        print(f"{ONTOLOGY_TTL.relative_to(ROOT)}: {exc}", file=sys.stderr)
        return 1
    want = render(schemes, statuses, edges)

    if not args.check:
        GEN_GO.parent.mkdir(parents=True, exist_ok=True)
        GEN_GO.write_text(want, encoding="utf-8")
        return 0

    have = GEN_GO.read_text(encoding="utf-8") if GEN_GO.exists() else ""
    if have == want:
        return 0
    rel = GEN_GO.relative_to(ROOT)
    sys.stderr.writelines(
        difflib.unified_diff(
            have.splitlines(keepends=True),
            want.splitlines(keepends=True),
            fromfile=f"{rel} (on disk)",
            tofile=f"{rel} (from ns/)",
        )
    )
    print(f"{rel} is stale — run ./scripts/nsgen.py and commit", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
