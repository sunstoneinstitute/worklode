package overview

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

const sparqlPrefixes = `PREFIX wl:   <https://worklode.io/ns/ontology#>
PREFIX wlc:  <https://worklode.io/ns/concept/>
PREFIX dct:  <http://purl.org/dc/terms/>
PREFIX dcat: <http://www.w3.org/ns/dcat#>
PREFIX rdf:  <http://www.w3.org/1999/02/22-rdf-syntax-ns#>
PREFIX xsd:  <http://www.w3.org/2001/XMLSchema#>
`

// The two named-graph families spec 007 §1.1 partitions the layers by. Both
// are derived from iri, which owns the grammar (iri.DeclaredGraph and
// iri.ObservedGraph mint members of exactly these families).
const (
	declaredFamily = iri.GraphNS + "declared/"
	observedFamily = iri.GraphNS + "observed/"
)

// Every pattern here is wrapped in GRAPH, including the ones that read a
// single layer-agnostic fact. Nothing this package reads is in the default
// graph: the projector and the derivers only ever write named graphs, and an
// endpoint that does not serve the union of them as its default graph
// (Oxigraph without --union-default-graph, which is how docker-compose runs
// it) answers an unwrapped pattern with nothing. Wrapping is correct under
// either configuration, so the query does not depend on the endpoint's.

// violationsQuery is spec 007 §3.1 (violation direction):
// observed − declared − un-expired acknowledged. The layer partition is the
// graph-name family; today's date is injected from Go (design call 8).
//
// Two constraints the GRAPH wrapping makes explicit:
//
//   - Co-location. A deviation's four triples must sit in one named graph
//     (?vg), and its dct:valid in one graph (?eg, not necessarily the same).
//     The projector writes each deviation as a unit, so this holds; a writer
//     that split a deviation across graphs would make it invisible here.
//   - Declared-only. ?vg is confined to the declared family: a deviation is
//     sanctioned by a design document, so it belongs to the declared layer.
//     Without the filter an observed graph could carry a deviation, and a
//     buggy deriver would then suppress its own violations.
func violationsQuery(today string) string {
	return sparqlPrefixes + fmt.Sprintf(`SELECT DISTINCT ?from ?to WHERE {
  GRAPH ?og { ?from dct:requires ?to . }
  FILTER(STRSTARTS(STR(?og), %q))
  FILTER NOT EXISTS {
    GRAPH ?dg { ?from dct:requires ?to . }
    FILTER(STRSTARTS(STR(?dg), %q))
  }
  FILTER NOT EXISTS {
    GRAPH ?vg {
      ?dev a wl:AcceptedDeviation ;
           rdf:subject ?from ; rdf:predicate dct:requires ; rdf:object ?to .
    }
    FILTER(STRSTARTS(STR(?vg), %q))
    FILTER NOT EXISTS {
      GRAPH ?eg { ?dev dct:valid ?exp } FILTER (?exp < %q^^xsd:date)
    }
  }
} ORDER BY ?from ?to`, observedFamily, declaredFamily, declaredFamily, today)
}

// staleIntentQuery is §4.1's other direction: declared − observed.
func staleIntentQuery() string {
	return sparqlPrefixes + fmt.Sprintf(`SELECT DISTINCT ?from ?to WHERE {
  GRAPH ?dg { ?from dct:requires ?to . }
  FILTER(STRSTARTS(STR(?dg), %q))
  FILTER NOT EXISTS {
    GRAPH ?og { ?from dct:requires ?to . }
    FILTER(STRSTARTS(STR(?og), %q))
  }
} ORDER BY ?from ?to`, declaredFamily, observedFamily)
}

// acknowledgedQuery lists every deviation, active and expired
// (`lode graph drift --acknowledged`). It carries violationsQuery's declared-only
// confinement so the listing is exactly the set that suppresses violations —
// a deviation the report showed but the suppression ignored would be worse
// than not listing it.
func acknowledgedQuery() string {
	return sparqlPrefixes + fmt.Sprintf(`SELECT DISTINCT ?from ?to ?by ?exp WHERE {
  GRAPH ?g {
    ?dev a wl:AcceptedDeviation ;
         rdf:subject ?from ; rdf:predicate dct:requires ; rdf:object ?to ;
         wl:sanctionedBy ?by .
  }
  FILTER(STRSTARTS(STR(?g), %q))
  OPTIONAL { GRAPH ?eg { ?dev dct:valid ?exp } }
} ORDER BY ?from ?to`, declaredFamily)
}

// docGapsQuery is §4.2: components with no governing DesignDoc.
//
// Co-location assumption: the type and the wl:governs edge must be in the
// same named graph (?dg). The projector writes a document's declaration as a
// unit, so this holds; a document typed in one graph and governing from
// another would read here as no governance at all.
const docGapsQuery = sparqlPrefixes + `SELECT DISTINCT ?c WHERE {
  GRAPH ?g { ?c a wl:Component . }
  FILTER NOT EXISTS { GRAPH ?dg { ?d a wl:DesignDoc ; wl:governs ?c . } }
} ORDER BY ?c`

// unmatchedQuery reads deriver 2's coverage gaps.
const unmatchedQuery = sparqlPrefixes + `SELECT DISTINCT ?repo ?path WHERE {
  GRAPH ?g { ?repo wl:unmatchedPath ?path . }
} ORDER BY ?repo ?path`

// The 025 §11.5 standing queries, over wl:implements claims and the sections
// they name.
//
// A claim's data shape is RDF 1.2 (006 §3, wl:pinnedVersion): the asserted
// edge, an IRI reifier bound to that edge's triple term by rdf:reifies, and
// wl:pinnedVersion on the reifier. `<< ?c wl:implements ?s >> wl:pinnedVersion
// ?pv` is the SPARQL 1.2 annotation pattern for exactly those three triples,
// and it expands inside the enclosing GRAPH — so a claim's three triples must
// be co-located in one named graph, which is how observed/repo-implements
// writes them.
//
// §11.5's fifth row, delivered coverage, is deliberately not here: it joins a
// claim's component through wl:deliveredBy to a wl:Deployment, and neither
// Deliverable nor Deployment is projected yet, so the query would answer
// nothing whatever the graph held. It belongs with that projection.

// unimplementedQuery is §11.5 row 1: an accepted section no component claims.
// A section carries wl:status only in its document's canonical graph
// (graphproj.SectionTriples), so the status pattern confines the read to that
// graph without naming it — a version snapshot's sections are not candidates.
const unimplementedQuery = sparqlPrefixes + `SELECT DISTINCT ?s WHERE {
  GRAPH ?g { ?s a wl:Section ; wl:status wlc:accepted . }
  FILTER NOT EXISTS { GRAPH ?ig { ?c wl:implements ?s } }
} ORDER BY ?s`

// coverageQuery is §11.5 row 2: implemented ÷ non-superseded sections, per
// document. A section nobody claims contributes nothing through the OPTIONAL,
// so a document at zero coverage still gets a row — which is the whole corpus
// until observed/repo-implements writes its first claim, and the honest
// answer meanwhile.
const coverageQuery = sparqlPrefixes + `SELECT ?doc (COUNT(DISTINCT ?s) AS ?total) (COUNT(DISTINCT ?impl) AS ?implemented) WHERE {
  GRAPH ?g { ?s a wl:Section ; dct:isPartOf ?doc ; wl:status ?st . }
  FILTER (?st != wlc:superseded)
  OPTIONAL {
    GRAPH ?ig { ?c wl:implements ?s }
    BIND(?s AS ?impl)
  }
} GROUP BY ?doc ORDER BY ?doc`

// staleClaimQuery is §11.5 row 3: a claim pinned at a version older than the
// one that last revised the section it names. Both versions are compared as
// xsd:integer, never as the strings or the IRIs carrying them — 025 §4.1: v10
// does not sort after v3 either way round.
const staleClaimQuery = sparqlPrefixes + `SELECT DISTINCT ?c ?s WHERE {
  GRAPH ?cg { << ?c wl:implements ?s >> wl:pinnedVersion ?pv . }
  GRAPH ?pg { ?pv dcat:version ?pinned . }
  GRAPH ?sg { ?s wl:lastRevisedIn ?rev . }
  GRAPH ?rg { ?rev dcat:version ?current . }
  FILTER (xsd:integer(?current) > xsd:integer(?pinned))
} ORDER BY ?c ?s`

// orphanedClaimQuery is §11.5 row 4: a claim naming a section its document no
// longer has. The document is reached from the pinned snapshot through
// dcat:hasVersion, and its current section set is the canonical graph's, since
// graphproj.SectionTriples projects the published sections of the current
// version and nothing else.
const orphanedClaimQuery = sparqlPrefixes + `SELECT DISTINCT ?c ?s WHERE {
  GRAPH ?cg { << ?c wl:implements ?s >> wl:pinnedVersion ?pv . }
  GRAPH ?dg { ?doc dcat:hasVersion ?pv . }
  FILTER NOT EXISTS { GRAPH ?sg { ?s dct:isPartOf ?doc } }
} ORDER BY ?c ?s`

// taskRequiresQuery pulls the KG half of the critical-path DAG:
// wl:dependsOn is the projected task dependency (subPropertyOf
// dct:requires; queried directly — no reasoner, spec 006).
const taskRequiresQuery = sparqlPrefixes + `SELECT DISTINCT ?from ?to WHERE {
  GRAPH ?g { ?from wl:dependsOn ?to . }
} ORDER BY ?from ?to`

// today formats the injected query clock.
func today() string { return time.Now().UTC().Format("2006-01-02") }

func driftEdges(rows []map[string]string) []model.DriftEdge {
	out := make([]model.DriftEdge, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.DriftEdge{From: r["from"], To: r["to"]})
	}
	return out
}

// Violations runs the 4.1 violation query.
func Violations(ctx context.Context, c *graphserver.Client) ([]model.DriftEdge, error) {
	rows, err := c.Select(ctx, violationsQuery(today()))
	if err != nil {
		return nil, fmt.Errorf("drift violations: %w", err)
	}
	return driftEdges(rows), nil
}

// StaleIntent runs the 4.1 stale-intent query.
func StaleIntent(ctx context.Context, c *graphserver.Client) ([]model.DriftEdge, error) {
	rows, err := c.Select(ctx, staleIntentQuery())
	if err != nil {
		return nil, fmt.Errorf("stale intent: %w", err)
	}
	return driftEdges(rows), nil
}

// Acknowledged lists accepted deviations, marking expiry against the
// injected clock.
func Acknowledged(ctx context.Context, c *graphserver.Client) ([]model.Deviation, error) {
	rows, err := c.Select(ctx, acknowledgedQuery())
	if err != nil {
		return nil, fmt.Errorf("acknowledged deviations: %w", err)
	}
	now := today()
	out := make([]model.Deviation, 0, len(rows))
	for _, r := range rows {
		d := model.Deviation{From: r["from"], To: r["to"], SanctionedBy: r["by"], ValidUntil: r["exp"]}
		d.Expired = d.ValidUntil != "" && d.ValidUntil < now
		out = append(out, d)
	}
	return out, nil
}

// Gaps runs the 4.2 doc-gap and unmatched-path queries.
func Gaps(ctx context.Context, c *graphserver.Client) ([]model.Gap, error) {
	var out []model.Gap
	rows, err := c.Select(ctx, docGapsQuery)
	if err != nil {
		return nil, fmt.Errorf("doc gaps: %w", err)
	}
	for _, r := range rows {
		out = append(out, model.Gap{Component: r["c"]})
	}
	rows, err = c.Select(ctx, unmatchedQuery)
	if err != nil {
		return nil, fmt.Errorf("unmatched paths: %w", err)
	}
	for _, r := range rows {
		out = append(out, model.Gap{Repo: r["repo"], Path: r["path"]})
	}
	return out, nil
}

// Unimplemented runs §11.5's unimplemented-intent query, returning the
// section IRIs no component claims.
func Unimplemented(ctx context.Context, c *graphserver.Client) ([]string, error) {
	rows, err := c.Select(ctx, unimplementedQuery)
	if err != nil {
		return nil, fmt.Errorf("unimplemented intent: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r["s"])
	}
	return out, nil
}

// Coverage runs §11.5's per-document coverage query.
func Coverage(ctx context.Context, c *graphserver.Client) ([]model.DocCoverage, error) {
	rows, err := c.Select(ctx, coverageQuery)
	if err != nil {
		return nil, fmt.Errorf("section coverage: %w", err)
	}
	out := make([]model.DocCoverage, 0, len(rows))
	for _, r := range rows {
		implemented, _ := strconv.Atoi(r["implemented"])
		total, _ := strconv.Atoi(r["total"])
		out = append(out, model.DocCoverage{Doc: r["doc"], Implemented: implemented, Total: total})
	}
	return out, nil
}

// StaleClaims runs §11.5's stale-claim query.
func StaleClaims(ctx context.Context, c *graphserver.Client) ([]model.Claim, error) {
	rows, err := c.Select(ctx, staleClaimQuery)
	if err != nil {
		return nil, fmt.Errorf("stale claims: %w", err)
	}
	return claims(rows), nil
}

// OrphanedClaims runs §11.5's orphaned-claim query.
func OrphanedClaims(ctx context.Context, c *graphserver.Client) ([]model.Claim, error) {
	rows, err := c.Select(ctx, orphanedClaimQuery)
	if err != nil {
		return nil, fmt.Errorf("orphaned claims: %w", err)
	}
	return claims(rows), nil
}

func claims(rows []map[string]string) []model.Claim {
	out := make([]model.Claim, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.Claim{Component: r["c"], Section: r["s"]})
	}
	return out
}
