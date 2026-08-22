package overview

import (
	"strings"
	"testing"
)

func TestQueriesConfineLayersByGraphFamily(t *testing.T) {
	v := violationsQuery("2026-07-30")
	for _, want := range []string{
		`"https://worklode.io/ns/graph/observed/"`,
		`"https://worklode.io/ns/graph/declared/"`,
		`"2026-07-30"^^xsd:date`,
		"wl:AcceptedDeviation",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("violations query missing %s:\n%s", want, v)
		}
	}
	if !strings.Contains(staleIntentQuery(), "graph/declared/") {
		t.Error("stale-intent query does not scope to the declared family")
	}
}

// TestDeviationsAreConfinedToTheDeclaredFamily: a deviation is sanctioned by
// a design document, so both the suppression and the listing must reject one
// written into an observed graph — otherwise a buggy deriver suppresses its
// own violations, and `--acknowledged` lists more than suppression honours.
func TestDeviationsAreConfinedToTheDeclaredFamily(t *testing.T) {
	want := `FILTER(STRSTARTS(STR(?vg), "https://worklode.io/ns/graph/declared/"))`
	if v := violationsQuery("2026-07-30"); !strings.Contains(v, want) {
		t.Errorf("violations query does not confine the deviation graph:\n%s", v)
	}
	ack := acknowledgedQuery()
	if !strings.Contains(ack, `FILTER(STRSTARTS(STR(?g), "https://worklode.io/ns/graph/declared/"))`) {
		t.Errorf("acknowledged query does not confine the deviation graph:\n%s", ack)
	}
}
