package store

import (
	"regexp"
	"slices"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// schemeChecks pairs each wlc: concept scheme in ns/concept.ttl with the
// column whose CHECK constraint must list exactly its members. 025 §17 makes
// the Turtle authoritative for these enums; scripts/nsgen.py keeps the Go
// side derived and internal/ns pins it, but nothing until this test held the
// schema to the same list — so a scheme could grow a member and the CHECK
// stay behind, or a migration widen a CHECK the ontology never heard of.
var schemeChecks = map[string]struct {
	table  string
	column string
	// rename maps a concept's local name to the value the column stores,
	// for the schemes where the two deliberately differ.
	rename map[string]string
}{
	"TaskKind":         {table: "tasks", column: "kind"},
	"DesignDocStatus":  {table: "docs", column: "status"},
	"CoverageLevel":    {table: "doc_edges", column: "coverage"},
	"ArtifactKind":     {table: "artifacts", column: "kind"},
	"DeploymentStatus": {table: "deployments", column: "status"},
	"RuntimeEventKind": {table: "runtime_events", column: "kind"},
	// wlc:pypi_target is spelled apart from wlc:pypi because the artifact
	// kind and the target kind are different concepts; the relational schema
	// lets them share the name.
	"DeployTargetKind": {
		table: "deployments", column: "target_kind",
		rename: map[string]string{"pypi_target": "pypi"},
	},
}

// schemesWithoutTable are the schemes that tag terms in the ontology rather
// than values in a column, so no CHECK constraint mirrors them.
var schemesWithoutTable = map[string]string{
	"ModelLayer": "wl:layer tags ontology terms; it is never stored",
}

// checkedValues returns the values the single CHECK constraint over
// table.column admits. Postgres normalises `col IN (...)` to
// `col = ANY (ARRAY[...])`, and a column may carry more than one CHECK — the
// coverage column has a second one tying it to the edge type — so the match
// is on the ANY form, and finding anything but exactly one is a failure.
func checkedValues(t *testing.T, s *Store, table, column string) []string {
	t.Helper()
	rows, err := s.DBForTests().Query(
		`SELECT pg_get_constraintdef(c.oid)
		   FROM pg_constraint c JOIN pg_class r ON r.oid = c.conrelid
		  WHERE r.relname = $1 AND c.contype = 'c'`, table)
	if err != nil {
		t.Fatalf("read CHECK constraints on %s: %v", table, err)
	}
	defer rows.Close()

	anyList := regexp.MustCompile(
		`\b` + regexp.QuoteMeta(column) + ` = ANY \(ARRAY\[([^\]]*)\]`)
	literal := regexp.MustCompile(`'([^']*)'::text`)

	var found [][]string
	for rows.Next() {
		var def string
		if err := rows.Scan(&def); err != nil {
			t.Fatalf("scan constraintdef: %v", err)
		}
		m := anyList.FindStringSubmatch(def)
		if m == nil {
			continue
		}
		var values []string
		for _, lit := range literal.FindAllStringSubmatch(m[1], -1) {
			values = append(values, lit[1])
		}
		found = append(found, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("%s.%s: want exactly one `= ANY (ARRAY[...])` CHECK, found %d",
			table, column, len(found))
	}
	slices.Sort(found[0])
	return found[0]
}

// TestConceptSchemesMatchCheckConstraints holds every concept scheme against
// the column that stores it. Adding a member to ns/concept.ttl without the
// migration that widens the CHECK — the omission 025 §17's "in one commit"
// warns about — fails here.
func TestConceptSchemesMatchCheckConstraints(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)

	for scheme, members := range ns.Schemes {
		if why, ok := schemesWithoutTable[scheme]; ok {
			t.Logf("skipping wlc:%s: %s", scheme, why)
			continue
		}
		col, ok := schemeChecks[scheme]
		if !ok {
			t.Errorf("wlc:%s is in ns/concept.ttl but named by neither schemeChecks "+
				"nor schemesWithoutTable — say which column stores it, or why none does", scheme)
			continue
		}
		want := make([]string, 0, len(members))
		for _, m := range members {
			if renamed, ok := col.rename[m]; ok {
				m = renamed
			}
			want = append(want, m)
		}
		slices.Sort(want)
		got := checkedValues(t, s, col.table, col.column)
		if !slices.Equal(got, want) {
			t.Errorf("%s.%s CHECK admits %v, wlc:%s has %v — the Turtle and the migration disagree",
				col.table, col.column, got, scheme, want)
		}
	}

	// The reverse direction: a table entry naming a scheme that no longer
	// exists is a stale mapping, not a passing test.
	for scheme := range schemeChecks {
		if _, ok := ns.Schemes[scheme]; !ok {
			t.Errorf("schemeChecks names wlc:%s, which ns/concept.ttl no longer declares", scheme)
		}
	}
	for scheme := range schemesWithoutTable {
		if _, ok := ns.Schemes[scheme]; !ok {
			t.Errorf("schemesWithoutTable names wlc:%s, which ns/concept.ttl no longer declares", scheme)
		}
	}
}
