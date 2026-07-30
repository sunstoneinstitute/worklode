package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func testSkillUpsert(name, hash string) SkillUpsert {
	return SkillUpsert{
		Name: name, Description: "desc of " + name,
		SourceRepo: "acme/claude-plugins", SourcePath: "plugins/p/skills/" + name,
		GitCommit: "abc123", ContentHash: hash,
		SkillMD:     "---\nname: " + name + "\n---\nbody",
		Frontmatter: json.RawMessage(`{"name":"` + name + `"}`),
		Archive:     []byte("tar-bytes"),
	}
}

func TestUpsertSkillLifecycle(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	id, changed, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1"))
	if err != nil || !changed {
		t.Fatalf("first upsert: changed=%v err=%v", changed, err)
	}
	// Same hash again: no change, same id.
	id2, changed, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1"))
	if err != nil || changed || id2 != id {
		t.Fatalf("idempotent upsert: id=%d changed=%v err=%v", id2, changed, err)
	}
	// New hash: new version, changed, still the same skill row.
	id2, changed, err = s.UpsertSkill(ctx, testSkillUpsert("tdd", "h2"))
	if err != nil || !changed || id2 != id {
		t.Fatalf("new-hash upsert: id=%d changed=%v err=%v", id2, changed, err)
	}

	sk, err := s.GetSkill(ctx, "tdd")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sk.ID != id {
		t.Fatalf("upsert returned id %d, GetSkill says %d", id, sk.ID)
	}
	if sk.ContentHash != "h2" || sk.SkillMD == "" || sk.Deleted {
		t.Fatalf("get after upsert: %+v", sk)
	}

	// Archive fetch by name+hash, both versions retained.
	if _, err := s.SkillArchive(ctx, "tdd", "h1"); err != nil {
		t.Fatalf("archive h1: %v", err)
	}
	if _, err := s.SkillArchive(ctx, "tdd", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archive miss: %v", err)
	}

	// Cross-repo name collision is rejected.
	u := testSkillUpsert("tdd", "h3")
	u.SourceRepo = "other/repo"
	if _, _, err := s.UpsertSkill(ctx, u); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("collision: %v", err)
	}

	// Soft delete everything from the repo except a kept set.
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("debugging", "h9")); err != nil {
		t.Fatalf("second skill: %v", err)
	}
	n, err := s.SoftDeleteSkillsExcept(ctx, "acme/claude-plugins", []string{"debugging"})
	if err != nil || n != 1 {
		t.Fatalf("soft delete: n=%d err=%v", n, err)
	}
	if sk, _ := s.GetSkill(ctx, "tdd"); sk == nil || !sk.Deleted {
		t.Fatalf("tdd should be soft-deleted: %+v", sk)
	}
	// Re-upserting the same content resurrects it.
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h2")); err != nil {
		t.Fatalf("resurrect: %v", err)
	}
	if sk, _ := s.GetSkill(ctx, "tdd"); sk.Deleted {
		t.Fatalf("tdd should be live again")
	}

	// ListSkills excludes deleted by default.
	all, err := s.ListSkills(ctx, false)
	if err != nil || len(all) != 2 {
		t.Fatalf("list: n=%d err=%v", len(all), err)
	}

	// SkillsByNames preserves ask-order; missing names are absent. A
	// duplicate name (e.g. a repeated task pin) is deduped to one result.
	got, err := s.SkillsByNames(ctx, []string{"debugging", "ghost", "tdd", "debugging"})
	if err != nil {
		t.Fatalf("by names: %v", err)
	}
	if len(got) != 2 || got[0].Name != "debugging" || got[1].Name != "tdd" {
		t.Fatalf("by names order: %+v", got)
	}
}

// TestSoftDeleteSkillsExceptNilKeep guards against json.Marshal([]string(nil))
// producing `null`, which makes jsonb_array_elements_text error at runtime.
// Callers (sync engine with no skills left upstream) pass nil, and it must
// soft-delete every live skill from that repo rather than erroring.
func TestSoftDeleteSkillsExceptNilKeep(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("debugging", "h2")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	n, err := s.SoftDeleteSkillsExcept(ctx, "acme/claude-plugins", nil)
	if err != nil {
		t.Fatalf("soft delete with nil keep: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected all 2 skills soft-deleted, got %d", n)
	}

	all, err := s.ListSkills(ctx, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("expected no live skills, got %d", len(all))
	}
}

// TestSoftDeleteSkillsExceptScopesToSourceRepo guards the source_repo filter,
// the only thing stopping a sync of one repo from soft-deleting skills that
// belong to a different repo.
func TestSoftDeleteSkillsExceptScopesToSourceRepo(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	other := testSkillUpsert("other-skill", "h2")
	other.SourceRepo = "other/repo"
	if _, _, err := s.UpsertSkill(ctx, other); err != nil {
		t.Fatalf("seed other-repo skill: %v", err)
	}

	n, err := s.SoftDeleteSkillsExcept(ctx, "acme/claude-plugins", nil)
	if err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 skill soft-deleted, got %d", n)
	}

	sk, err := s.GetSkill(ctx, "other-skill")
	if err != nil {
		t.Fatalf("get other-skill: %v", err)
	}
	if sk.Deleted {
		t.Fatalf("other-skill from a different repo should still be live")
	}
}

func TestUpsertSkillEmptyContentHash(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	_, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", ""))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty content hash: want ErrInvalidInput, got %v", err)
	}

	if _, err := s.GetSkill(ctx, "tdd"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected no skill row written, got: %v", err)
	}
}

func TestGetSkillNotFound(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	_, err := s.GetSkill(ctx, "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSkill: want ErrNotFound, got %v", err)
	}
}

// TestUpsertSkillContentRevert exercises the ON CONFLICT DO UPDATE branch:
// reverting to an earlier content hash must not lose either version row, and
// must still report changed=true.
func TestUpsertSkillContentRevert(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("h1: %v", err)
	}
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h2")); err != nil {
		t.Fatalf("h2: %v", err)
	}
	_, changed, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1"))
	if err != nil || !changed {
		t.Fatalf("revert to h1: changed=%v err=%v", changed, err)
	}

	sk, err := s.GetSkill(ctx, "tdd")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sk.ContentHash != "h1" {
		t.Fatalf("expected latest version to point back at h1, got %+v", sk)
	}

	if _, err := s.SkillArchive(ctx, "tdd", "h1"); err != nil {
		t.Fatalf("archive h1 after revert: %v", err)
	}
	if _, err := s.SkillArchive(ctx, "tdd", "h2"); err != nil {
		t.Fatalf("archive h2 still present after revert: %v", err)
	}

	var count int
	err = s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM skill_versions v
		JOIN skills s ON s.id = v.skill_id
		WHERE s.name = $1`, "tdd").Scan(&count)
	if err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected exactly 2 version rows, got %d", count)
	}
}
