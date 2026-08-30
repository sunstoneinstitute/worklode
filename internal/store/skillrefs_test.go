package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// seedQualified stores one skill under an explicit plugin qualifier. Kept
// local so these tests state both halves of the identity they are about.
func seedQualified(t *testing.T, st *Store, qualifier, name string) {
	t.Helper()
	_, _, err := st.UpsertSkill(context.Background(), SkillUpsert{
		Qualifier: qualifier, Name: name,
		Description: "desc of " + qualifier + ":" + name,
		SourceRepo:  "acme/claude-plugins",
		SourcePath:  "plugins/" + qualifier + "/skills/" + name,
		GitCommit:   "abc123", ContentHash: "h-" + qualifier + "-" + name,
		SkillMD:     "---\nname: " + name + "\n---\nbody",
		Frontmatter: json.RawMessage(`{"name":"` + name + `"}`),
		Archive:     []byte("tar-bytes"),
	})
	if err != nil {
		t.Fatalf("seed %s:%s: %v", qualifier, name, err)
	}
}

// 037 §4.1's collision, gone: two plugins ship "brainstorming", both land, and
// each is separately reachable by its qualified name. Under the old
// UNIQUE (name) constraint the second sync lost outright.
func TestTwoPluginsShipOneSkillName(t *testing.T) {
	t.Parallel()
	st := OpenTestStore(t)
	ctx := context.Background()
	seedQualified(t, st, "superpowers", "brainstorming")
	seedQualified(t, st, "lode", "brainstorming")

	for _, want := range []string{"superpowers:brainstorming", "lode:brainstorming"} {
		sk, err := st.GetSkill(ctx, want)
		if err != nil {
			t.Fatalf("get %s: %v", want, err)
		}
		if sk.QualifiedName() != want {
			t.Fatalf("got %s, want %s", sk.QualifiedName(), want)
		}
	}
}

// A bare name that no longer names one skill reports the candidates instead of
// picking one: silently handing the task whichever row sorted first is the
// failure mode the qualifier exists to end.
func TestBareNameAmbiguousReportsCandidates(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	st := OpenTestStore(t, WithMetrics(reg))
	ctx := context.Background()
	seedQualified(t, st, "superpowers", "brainstorming")
	seedQualified(t, st, "lode", "brainstorming")

	_, err := st.GetSkill(ctx, "brainstorming")
	if !errors.Is(err, ErrAmbiguousSkill) {
		t.Fatalf("err = %v, want ErrAmbiguousSkill", err)
	}
	for _, want := range []string{"superpowers:brainstorming", "lode:brainstorming"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name candidate %s", err, want)
		}
	}
	if got := testutil.ToFloat64(st.metrics.skillAmbiguous); got != 1 {
		t.Fatalf("worklode_skill_name_ambiguous_total = %v, want 1", got)
	}
}

// Qualifying is only required once a name is ambiguous, so every pin and
// invocation written before this change keeps working.
func TestBareNameResolvesWhileUnique(t *testing.T) {
	t.Parallel()
	st := OpenTestStore(t)
	seedQualified(t, st, "superpowers", "brainstorming")

	sk, err := st.GetSkill(context.Background(), "brainstorming")
	if err != nil {
		t.Fatalf("get brainstorming: %v", err)
	}
	if sk.QualifiedName() != "superpowers:brainstorming" {
		t.Fatalf("got %s", sk.QualifiedName())
	}
}

// WL-74: a plan task pinning a plugin-shipped skill in the "plugin:skill" form
// the authoring guide documents resolves against the registry, with no
// "pinned skill not found" warning.
func TestQualifiedPinResolvesWithoutWarning(t *testing.T) {
	t.Parallel()
	st := OpenTestStore(t)
	seedQualified(t, st, "superpowers", "test-driven-development")

	pinned, warnings, err := st.ResolvePins(context.Background(),
		[]string{"superpowers:test-driven-development"})
	if err != nil {
		t.Fatalf("resolve pins: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if len(pinned) != 1 || pinned[0].QualifiedName() != "superpowers:test-driven-development" {
		t.Fatalf("pinned = %+v", pinned)
	}
}

// A pin naming a plugin the org never synced still finds an equivalent skill
// by the segment after its first colon — the rule that kept plan pins working
// before any plugin was a source, and that must keep working now.
func TestPinFallsBackToSuffixForUnsyncedPlugin(t *testing.T) {
	t.Parallel()
	st := OpenTestStore(t)
	seedQualified(t, st, "sunstone", "test-driven-development")

	pinned, warnings, err := st.ResolvePins(context.Background(),
		[]string{"superpowers:test-driven-development"})
	if err != nil {
		t.Fatalf("resolve pins: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if len(pinned) != 1 || pinned[0].QualifiedName() != "sunstone:test-driven-development" {
		t.Fatalf("pinned = %+v", pinned)
	}
}

// An ambiguous pin warns and names the candidates rather than resolving to
// one of them, so a brief never quietly carries a skill nobody chose.
func TestResolvePinsWarnsOnAmbiguity(t *testing.T) {
	t.Parallel()
	st := OpenTestStore(t)
	seedQualified(t, st, "superpowers", "brainstorming")
	seedQualified(t, st, "lode", "brainstorming")

	pinned, warnings, err := st.ResolvePins(context.Background(), []string{"brainstorming"})
	if err != nil {
		t.Fatalf("resolve pins: %v", err)
	}
	if len(pinned) != 0 {
		t.Fatalf("pinned = %+v, want none", pinned)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "ambiguous") ||
		!strings.Contains(warnings[0], "lode:brainstorming") {
		t.Fatalf("warnings = %v", warnings)
	}
}

// A skill withdrawn from its source repo must not make a live same-named one
// ambiguous: the live one is the only thing a bare pin could mean.
func TestDeletedSkillDoesNotMakeLiveOneAmbiguous(t *testing.T) {
	t.Parallel()
	st := OpenTestStore(t)
	ctx := context.Background()
	seedQualified(t, st, "superpowers", "brainstorming")
	seedQualified(t, st, "lode", "brainstorming")
	if _, err := st.SoftDeleteSkillsExcept(ctx, "acme/claude-plugins",
		[]string{"lode:brainstorming"}); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	sk, err := st.GetSkill(ctx, "brainstorming")
	if err != nil {
		t.Fatalf("get brainstorming: %v", err)
	}
	if sk.QualifiedName() != "lode:brainstorming" || sk.Deleted {
		t.Fatalf("got %s deleted=%v", sk.QualifiedName(), sk.Deleted)
	}
}

// A skill has no identity without a qualifier: storing one would collapse the
// qualified name back to the bare one this change replaced.
func TestUpsertSkillRequiresQualifier(t *testing.T) {
	t.Parallel()
	st := OpenTestStore(t)
	_, _, err := st.UpsertSkill(context.Background(), SkillUpsert{
		Name: "tdd", Description: "d", SourceRepo: "acme/p", SourcePath: "skills/tdd",
		GitCommit: "abc", ContentHash: "h", SkillMD: "body",
		Frontmatter: json.RawMessage(`{}`), Archive: []byte("t"),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// 0039 runs against a registry that already holds rows, so the qualifier
// column has to be backfilled before the uniqueness constraint it feeds is
// added. A manifest-derived qualifier needs the source tarball, which SQL does
// not have, so the backfill is the source repo's last segment — a placeholder
// the first sync after deploy corrects.
func TestSkillQualifierBackfillOnPopulatedRegistry(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(migrationsThrough(t, 38)); err != nil {
		t.Fatalf("migrate through 0038: %v", err)
	}
	// Inserted directly: the store is held at 0038, where skills has no
	// qualifier column for UpsertSkill to write.
	if _, err := s.DBForTests().Exec(
		`INSERT INTO skills (name, description, source_repo, source_path)
		 VALUES ('brainstorming', 'd', 'acme/claude-plugins', 'plugins/one/skills/brainstorming')`,
	); err != nil {
		t.Fatalf("insert pre-0039 skill: %v", err)
	}

	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}

	var qualifier string
	if err := s.DBForTests().QueryRow(
		`SELECT qualifier FROM skills WHERE name = 'brainstorming'`).Scan(&qualifier); err != nil {
		t.Fatalf("read qualifier: %v", err)
	}
	if qualifier != "claude-plugins" {
		t.Fatalf("qualifier = %q, want claude-plugins", qualifier)
	}

	// The backfilled row keeps its identity, and a second plugin's same-named
	// skill now fits alongside it.
	seedQualified(t, s, "lode", "brainstorming")
	sk, err := s.GetSkill(context.Background(), "claude-plugins:brainstorming")
	if err != nil {
		t.Fatalf("get backfilled skill: %v", err)
	}
	if sk.Name != "brainstorming" {
		t.Fatalf("got %+v", sk)
	}
}
