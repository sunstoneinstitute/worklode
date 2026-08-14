//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
)

// TestGraphServerAcceptance is spec 009's acceptance criterion against a
// live graph-server. Skipped unless LODE_GRAPHSERVER_URL is set; see the
// README "Graph-server acceptance" section for the full env.
func TestGraphServerAcceptance(t *testing.T) {
	if os.Getenv("LODE_GRAPHSERVER_URL") == "" {
		t.Skip("LODE_GRAPHSERVER_URL not set")
	}
	client, err := graphserver.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	ctx := context.Background()

	// Unique fixture per run: comp-a is governed, comp-b is the drift.
	// IRIs follow spec 006 as amended by 014 §1 (base worklode.io/ns/);
	// the project graph family follows the knowledge-graph plan.
	nonce := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	graphIRI := "https://worklode.io/ns/graph/project/" + nonce
	compA := "https://worklode.io/ns/id/component/" + nonce + "/comp-a"
	compB := "https://worklode.io/ns/id/component/" + nonce + "/comp-b"
	doc := "https://worklode.io/ns/id/doc/" + nonce + "-doc"
	t.Logf("fixture graph %s", graphIRI)
	turtle := fmt.Sprintf(`@prefix wl: <https://worklode.io/ns/ontology#> .
<%s> a wl:Component .
<%s> a wl:Component .
<%s> a wl:DesignDoc ; wl:governs <%s> .
`, compA, compB, doc, compA)

	// Step 1+2 — authenticate (the token source) and PUT to fixed main.
	created, err := client.PutGraph(ctx, "main", graphIRI, []byte(turtle))
	// Registered before the assertions below: a PUT that committed
	// server-side but failed client-side, or one that unexpectedly
	// replaced rather than created, must not leak a graph on a shared
	// instance. Deleting a graph that was never created is harmless.
	t.Cleanup(func() {
		if err := client.DeleteGraph(context.Background(), "main", graphIRI); err != nil &&
			!errors.Is(err, graphserver.ErrNotFound) {
			t.Logf("cleanup DeleteGraph %s: %v", graphIRI, err)
		}
	})
	if err != nil {
		t.Fatalf("PutGraph: %v", err)
	}
	if !created {
		t.Fatalf("first PUT of %s: created = false; nonce collision?", graphIRI)
	}

	// Idempotent re-PUT (spec 009 item 4: the atomic per-branch write).
	if created, err = client.PutGraph(ctx, "main", graphIRI, []byte(turtle)); err != nil {
		t.Fatalf("re-PUT: %v", err)
	}
	if created {
		t.Fatal("re-PUT: created = true; want 204 replace")
	}

	// Step 3 — read back over GSP. Assumes the server serializes absolute
	// IRIs rather than prefixed names; a Turtle writer that emitted
	// `@prefix ns0: <.../component/nonce/>` then `ns0:comp-a` would make
	// this substring check a false negative on otherwise-correct data.
	body, err := client.GetGraph(ctx, "main", graphIRI)
	if err != nil {
		t.Fatalf("GetGraph: %v", err)
	}
	for _, iri := range []string{compA, compB, doc} {
		if !strings.Contains(string(body), iri) {
			t.Fatalf("read-back is missing %s:\n%s", iri, body)
		}
	}

	// Step 4 — the drift question over SPARQL, scoped to this run's graph.
	// The materializer is asynchronous, so poll until it catches up.
	query := fmt.Sprintf(`PREFIX wl: <https://worklode.io/ns/ontology#>
SELECT ?component WHERE {
  GRAPH <%s> {
    ?component a wl:Component .
    FILTER NOT EXISTS { ?doc a wl:DesignDoc ; wl:governs ?component . }
  }
}`, graphIRI)
	deadline := time.Now().Add(90 * time.Second)
	for {
		rows, err := client.Select(ctx, query)
		if err == nil && len(rows) == 1 && rows[0]["component"] == compB {
			return
		}
		// A hard error is fatal at once; an unavailable endpoint, an empty
		// result, or a partially-materialized one is worth another look.
		if err != nil && !errors.Is(err, graphserver.ErrSPARQLUnavailable) {
			t.Fatalf("Select: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("materializer did not catch up for graph %s: rows=%v err=%v", graphIRI, rows, err)
		}
		time.Sleep(3 * time.Second)
	}
}
