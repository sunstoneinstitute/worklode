#!/usr/bin/env python3
"""Generate internal/ns/gen.go from ns/concept.ttl.

ns/concept.ttl is the source of the enums that also appear as CHECK
constraints and as Go literals (025 §17). This makes the Turtle the one that
is typed by hand and the Go the one that is derived, so a kind or a status
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
GEN_GO = ROOT / "internal" / "ns" / "gen.go"

SKOS = "http://www.w3.org/2004/02/skos/core#"
RDF_TYPE = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
RDF_NIL = "http://www.w3.org/1999/02/22-rdf-syntax-ns#nil"
RDF_FIRST = "http://www.w3.org/1999/02/22-rdf-syntax-ns#first"
RDF_REST = "http://www.w3.org/1999/02/22-rdf-syntax-ns#rest"


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
    predicate-object lists (`;`), object lists (`,`), and RDF collections
    (`( ... )`). Everything else - blank node property lists, numeric and
    boolean literals as bare tokens - raises TurtleError.
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
        return f"_:list{self._bnode}"

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
            # Literals are not needed by any caller; keep the raw form so the
            # parser stays total, and consume any language/datatype suffix.
            nxt = self.peek()
            if nxt and nxt[1].startswith("@"):
                self.next()
            elif nxt and nxt[1] == "^^":
                self.next()
                self.term()
            return "\x00literal"
        if kind == "punct" and text == "(":
            return self.collection()
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
        subject = self.term()
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
                if nxt and nxt[1] == ".":
                    break
                continue
            break
        self.expect(".")


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


def ordered_list(triples, head: str) -> list[str]:
    firsts = {s: o for s, p, o in triples if p == RDF_FIRST}
    rests = {s: o for s, p, o in triples if p == RDF_REST}
    out: list[str] = []
    node = head
    while node != RDF_NIL:
        if node not in firsts:
            raise TurtleError(f"malformed collection at {node}")
        out.append(firsts[node])
        node = rests[node]
    return out


def extract(ttl: str) -> tuple[list[str], list[str]]:
    triples = Parser(ttl).parse()
    wlc = "https://worklode.io/ns/concept/"

    kinds = sorted(local_name(m, wlc) for m in scheme_members(triples, wlc + "TaskKind"))

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
    if set(ordered) != status_set:
        raise TurtleError(
            "wlc:DesignDocStatusOrder does not list exactly the scheme's members: "
            f"list={sorted(ordered)} scheme={sorted(status_set)}"
        )
    statuses = [local_name(o, wlc) for o in ordered]
    return kinds, statuses


# --------------------------------------------------------------------------
# Emission
# --------------------------------------------------------------------------

TEMPLATE = '''// Code generated by scripts/nsgen.py from ns/concept.ttl. DO NOT EDIT.

// Package ns exposes the concept schemes of ns/concept.ttl as Go values.
//
// ns/ owns the shared schema (025 §17), so an enum change is a change to the
// Turtle first, then `scripts/nsgen.py` to regenerate, then the migration
// that moves the matching CHECK constraint — in one commit.
package ns

// TaskKinds mirrors wlc:TaskKind and the tasks.kind CHECK constraint,
// alphabetically.
var TaskKinds = []string{%s}

// DesignDocStatuses mirrors wlc:DesignDocStatus and the docs.status CHECK
// constraint, in the lifecycle order of wlc:DesignDocStatusOrder.
var DesignDocStatuses = []string{%s}
'''


def render(kinds: list[str], statuses: list[str]) -> str:
    def lit(values: list[str]) -> str:
        return ", ".join(f'"{v}"' for v in values)

    return TEMPLATE % (lit(kinds), lit(statuses))


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "--check",
        action="store_true",
        help="exit 1 with a diff if internal/ns/gen.go is stale",
    )
    args = ap.parse_args()

    try:
        kinds, statuses = extract(CONCEPT_TTL.read_text(encoding="utf-8"))
    except TurtleError as exc:
        print(f"{CONCEPT_TTL.relative_to(ROOT)}: {exc}", file=sys.stderr)
        return 1
    want = render(kinds, statuses)

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
            tofile=f"{rel} (from ns/concept.ttl)",
        )
    )
    print(f"{rel} is stale — run ./scripts/nsgen.py and commit", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
