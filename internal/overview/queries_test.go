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
