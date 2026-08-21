package overview

import (
	"context"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

const sparqlPrefixes = `PREFIX wl:  <https://worklode.io/ns/ontology#>
PREFIX dct: <http://purl.org/dc/terms/>
PREFIX rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#>
PREFIX xsd: <http://www.w3.org/2001/XMLSchema#>
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
// (`lode drift --acknowledged`). It carries violationsQuery's declared-only
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
