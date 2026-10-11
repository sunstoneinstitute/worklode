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
PREFIX prov: <http://www.w3.org/ns/prov#>
PREFIX rdf:  <http://www.w3.org/1999/02/22-rdf-syntax-ns#>
PREFIX xsd:  <http://www.w3.org/2001/XMLSchema#>
`

// The two named-graph families WL-REQ-1706 partitions the layers by. Both
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

// violationsQuery is WL-REQ-1362's violation direction:
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

// staleIntentQuery is WL-REQ-1362's other direction: declared − observed.
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

// docGapsQuery is WL-REQ-1673's documentation gap: components with no governing
// DesignDoc.
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

// The WL-REQ-1707 standing queries, over wl:implements claims and the rules
// they name.
//
// A claim's data shape is RDF 1.2 (WL-REQ-239, wl:pinnedVersion): the asserted
// edge, an IRI reifier bound to that edge's triple term by rdf:reifies, and
// wl:pinnedVersion on the reifier. `<< ?c wl:implements ?s >> wl:pinnedVersion
// ?pv` is the SPARQL 1.2 annotation pattern for exactly those three triples,
// and it expands inside the enclosing GRAPH — so a claim's three triples must
// be co-located in one named graph, which is how observed/repo-implements
// writes them.
//

// unimplementedQuery is WL-REQ-1673 row 1: an accepted rule
// no component claims. A rule carries wl:status only in its document's
// canonical graph (graphproj.RuleTriples), so the status pattern confines the
// read to that graph without naming it.
const unimplementedQuery = sparqlPrefixes + `SELECT DISTINCT ?s WHERE {
  GRAPH ?g { ?s a wl:Rule ; wl:status wlc:accepted . }
  FILTER NOT EXISTS { GRAPH ?ig { ?c wl:implements ?s } }
} ORDER BY ?s`

// coverageQuery is WL-REQ-1673 row 2: implemented ÷ non-superseded
// rules, per document. A rule nobody claims contributes nothing through the OPTIONAL,
// so a document at zero coverage still gets a row — which is the whole corpus
// until observed/repo-implements writes its first claim, and the honest
// answer meanwhile.
const coverageQuery = sparqlPrefixes + `SELECT ?doc (COUNT(DISTINCT ?s) AS ?total) (COUNT(DISTINCT ?impl) AS ?implemented) WHERE {
  GRAPH ?g { ?s a wl:Rule ; dct:isPartOf ?doc ; wl:status ?st . }
  FILTER (?st != wlc:superseded)
  OPTIONAL {
    GRAPH ?ig { ?c wl:implements ?s }
    BIND(?s AS ?impl)
  }
} GROUP BY ?doc ORDER BY ?doc`

// staleClaimQuery is WL-REQ-1673 row 3: a claim pinned at a version
// older than the one that last revised the rule it names. Both versions are compared as
// xsd:integer, never as the strings or the IRIs carrying them — WL-REQ-166: v10
// does not sort after v3 either way round.
const staleClaimQuery = sparqlPrefixes + `SELECT DISTINCT ?c ?s WHERE {
  GRAPH ?cg { << ?c wl:implements ?s >> wl:pinnedVersion ?pv . }
  GRAPH ?pg { ?pv dcat:version ?pinned . }
  GRAPH ?sg { ?s wl:lastRevisedIn ?rev . }
  GRAPH ?rg { ?rev dcat:version ?current . }
  FILTER (xsd:integer(?current) > xsd:integer(?pinned))
} ORDER BY ?c ?s`

// orphanedClaimQuery is WL-REQ-1673 row 4: a claim naming a rule
// its document no longer arranges. The document is reached from the pinned
// snapshot through dcat:hasVersion, and its current arrangement is the
// canonical graph's dct:isPartOf (graphproj.RuleTriples).
const orphanedClaimQuery = sparqlPrefixes + `SELECT DISTINCT ?c ?s WHERE {
  GRAPH ?cg { << ?c wl:implements ?s >> wl:pinnedVersion ?pv . }
  GRAPH ?dg { ?doc dcat:hasVersion ?pv . }
  FILTER NOT EXISTS { GRAPH ?sg { ?s dct:isPartOf ?doc } }
} ORDER BY ?c ?s`

// deliveredCoverageQuery is WL-REQ-1673 row 5: for each wl:implements claim, the
// environments the claiming component's deliverable is actually deployed to.
// It is the join that turns "component A claims rule S" into "rule S is
// live in prod", which is what WL-REQ-243 calls the vertical.
//
// Two arms, because a deliverable is witnessed two ways (WL-REQ-243's witness
// table) and there is no reasoner (WL-SPEC-79), so wl:Effect's subclass edge to
// wl:Deliverable does not fire and each arm names its own type:
//
//   - A wl:Deliverable targets an Artifact, and the witness is a Deployment
//     that prov:used that artifact. The `?a a wl:Artifact` pattern is what
//     separates this arm from the Effect one: a deliverable declares its
//     targets as dct:relation, so the target's type is the only thing saying
//     which kind of target it is.
//   - A wl:Effect ships nothing, so its dct:relation names the Deployment
//     directly and reaching wlc:deployed is the whole witness.
//
// v1 checks deployment status alone. WL-REQ-243's open question 10 adds the
// default-branch-commit witness for the Effect arm — that a deployed
// Kustomization reconciles a commit on the delivering component's branch,
// rather than any revision at all — and until it is settled an Effect counts
// as delivered as soon as its Deployment says deployed.
//
// Deployment, Artifact and Environment nodes are projected today
// (internal/storederive/deploy.go); Deliverable and Effect nodes are not
// (WL-REQ-243, WL-PLAN-118). So against a production graph this answers empty,
// whatever it holds, until that projection lands — the query is right and its
// left-hand input is missing.
const deliveredCoverageQuery = sparqlPrefixes + `SELECT DISTINCT ?c ?s ?env WHERE {
  GRAPH ?ig { ?c wl:implements ?s . }
  {
    GRAPH ?dg { ?d a wl:Deliverable ; wl:deliveredBy ?c ; dct:relation ?a . }
    GRAPH ?ag { ?a a wl:Artifact . }
    GRAPH ?pg {
      ?dep prov:used ?a ; wl:toEnvironment ?env ; wl:deploymentStatus wlc:deployed .
    }
  } UNION {
    GRAPH ?dg { ?d a wl:Effect ; wl:deliveredBy ?c ; dct:relation ?dep . }
    GRAPH ?pg { ?dep wl:toEnvironment ?env ; wl:deploymentStatus wlc:deployed . }
  }
} ORDER BY ?c ?s ?env`

// taskRequiresQuery pulls the KG half of the critical-path DAG:
// wl:dependsOn is the projected task dependency (subPropertyOf
// dct:requires; queried directly — no reasoner, WL-SPEC-79).
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

// Unimplemented runs WL-REQ-1673's unimplemented-intent query, returning the rule IRIs
// no component claims.
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

// Coverage runs WL-REQ-1673's per-document coverage query.
func Coverage(ctx context.Context, c *graphserver.Client) ([]model.DocCoverage, error) {
	rows, err := c.Select(ctx, coverageQuery)
	if err != nil {
		return nil, fmt.Errorf("rule coverage: %w", err)
	}
	out := make([]model.DocCoverage, 0, len(rows))
	for _, r := range rows {
		implemented, _ := strconv.Atoi(r["implemented"])
		total, _ := strconv.Atoi(r["total"])
		out = append(out, model.DocCoverage{Doc: r["doc"], Implemented: implemented, Total: total})
	}
	return out, nil
}

// StaleClaims runs WL-REQ-1673's stale-claim query.
func StaleClaims(ctx context.Context, c *graphserver.Client) ([]model.Claim, error) {
	rows, err := c.Select(ctx, staleClaimQuery)
	if err != nil {
		return nil, fmt.Errorf("stale claims: %w", err)
	}
	return claims(rows), nil
}

// OrphanedClaims runs WL-REQ-1673's orphaned-claim query.
func OrphanedClaims(ctx context.Context, c *graphserver.Client) ([]model.Claim, error) {
	rows, err := c.Select(ctx, orphanedClaimQuery)
	if err != nil {
		return nil, fmt.Errorf("orphaned claims: %w", err)
	}
	return claims(rows), nil
}

// DeliveredCoverage runs WL-REQ-1673's delivered-coverage query: one row per
// (claim, environment) the claim is live in. A claim delivered to two
// environments is two rows, which is what a caller asking "is this rule in
// prod" needs to see.
func DeliveredCoverage(ctx context.Context, c *graphserver.Client) ([]model.DeliveredClaim, error) {
	rows, err := c.Select(ctx, deliveredCoverageQuery)
	if err != nil {
		return nil, fmt.Errorf("delivered coverage: %w", err)
	}
	out := make([]model.DeliveredClaim, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.DeliveredClaim{
			Component: r["c"], Rule: r["s"], Environment: r["env"]})
	}
	return out, nil
}

func claims(rows []map[string]string) []model.Claim {
	out := make([]model.Claim, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.Claim{Component: r["c"], Rule: r["s"]})
	}
	return out
}
