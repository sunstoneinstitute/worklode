---
status: superseded
implements: docs/specs/016-org-wide-skills.md
---
# Org-wide Agent Skills Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement spec 016 (`docs/specs/016-org-wide-skills.md`): a git-synced org skill registry in the backbone with chunked pgvector embeddings, a recommendation endpoint, task pins, a `lode skills` CLI group, content-addressed local install under `~/.worklode/skills`, and brief integration.

**Architecture:** Skills sync from configured GitHub repos (webhook push + manual `lode skills sync`) into three new Postgres tables; an embedding provider (OpenAI-compatible HTTP, optional) turns SKILL.md into chunk vectors; `POST /api/v1/skills/recommend` does cosine top-k; `lode task brief` inlines pinned SKILL.md bodies and offers embedding matches; the session-start hook lazily fetches archives into a content-addressed local store.

**Tech Stack:** Go 1.26, stdlib `net/http` mux, `database/sql` over pgx stdlib driver, golang-migrate, cobra, pgvector (SQL only — no Go client lib; vectors are passed as `[x,y,…]::vector` text literals).

**Conventions that bind every task** (from the existing codebase):
- Handlers stay thin: parse/validate → store → `mapStoreErr` → `writeJSON`. Wire structs are private `xxxJSON` types in `internal/api`, mirrored by exported structs in `internal/cli`.
- Store: one file per entity, hand-written SQL, errors wrapped `fmt.Errorf("verb noun: %w", err)`, `sql.ErrNoRows` → `ErrNotFound`, named constraints (used by `isUniqueViolationOn`).
- Tests: stdlib `testing` only, table-driven, `t.Fatalf`. Store tests in-package via `OpenTestStore(t)`; api tests external via `newTestServer(t)` / `doReq(...)` from `internal/api/server_test.go`.
- Config: env vars only — field on `api.Config` (server.go) + `os.Getenv` in `internal/cmd/serve.go`; unset = feature off, malformed = fatal at boot.
- Run tests: `docker compose up -d` then `go test ./...`; CI parity: `go test -race -count=1 ./...`. Commit gate: pre-commit runs gofmt + go vet.

**Out of scope (per spec):** graph projection of `ls:Skill` (spec 03 projection doesn't exist yet), design-doc frontmatter pins (waits for spec 014), `doc_iri` recommend input, `lode reconcile` integration (spec 07 unimplemented — the admin sync endpoint is the fallback).

---

### Task 1: Migration 0007 + pgvector test infrastructure

**Files:**
- Create: `deploy/base/migrations/0007_skills.up.sql`, `deploy/base/migrations/0007_skills.down.sql`
- Modify: `docker-compose.yml` (postgres image), `.github/workflows/_test.yml` (postgres service image), `deploy/base/kustomization.yaml` (configMapGenerator file list), `internal/store/store_test.go` (`wantTables`)

- [ ] **Step 1: Switch local + CI Postgres to a pgvector image**

In `docker-compose.yml`, change the postgres service image from `postgres:17` to `pgvector/pgvector:pg17` (same tag family, drop-in). In `.github/workflows/_test.yml`, make the same image swap in the `services.postgres` block. Then locally:

```bash
docker compose down -v && docker compose up -d
```

- [ ] **Step 2: Write the failing test expectation**

In `internal/store/store_test.go`, extend `wantTables` with `"skill_embeddings", "skill_versions", "skills"` (keep the list sorted the way the existing entries are).

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/store/ -run TestMigrateAppliesMigrations -v`
Expected: FAIL — missing tables.

- [ ] **Step 4: Write the migration pair**

`deploy/base/migrations/0007_skills.up.sql`:

```sql
-- Org-wide agent skills: registry synced from git source repos, chunked
-- pgvector embeddings for recommendation, and per-task skill pins.
-- See docs/specs/016-org-wide-skills.md.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE skills (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name              text NOT NULL,
    description       text NOT NULL,
    source_repo       text NOT NULL,
    source_path       text NOT NULL,
    latest_version_id bigint,
    deleted_at        timestamptz,
    CONSTRAINT skills_name_unique UNIQUE (name)
);

CREATE TABLE skill_versions (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    skill_id     bigint NOT NULL REFERENCES skills(id) ON DELETE RESTRICT,
    git_commit   text NOT NULL,
    content_hash text NOT NULL,
    frontmatter  jsonb NOT NULL,
    skill_md     text NOT NULL,
    archive      bytea NOT NULL,
    created_at   timestamptz NOT NULL,
    CONSTRAINT skill_versions_hash_unique UNIQUE (skill_id, content_hash)
);

ALTER TABLE skills
    ADD CONSTRAINT skills_latest_version_fk
    FOREIGN KEY (latest_version_id) REFERENCES skill_versions(id);

-- Latest version only; empty when no embedding provider is configured.
CREATE TABLE skill_embeddings (
    skill_id    bigint NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    chunk_index int NOT NULL,
    embedding   vector NOT NULL,
    PRIMARY KEY (skill_id, chunk_index)
);

-- Task pins: skill names the task author wants injected into the brief.
ALTER TABLE tasks ADD COLUMN skills jsonb NOT NULL DEFAULT '[]';
```

`deploy/base/migrations/0007_skills.down.sql`:

```sql
ALTER TABLE tasks DROP COLUMN skills;
DROP TABLE skill_embeddings;
ALTER TABLE skills DROP CONSTRAINT skills_latest_version_fk;
DROP TABLE skill_versions;
DROP TABLE skills;
-- The vector extension is left installed: CREATE EXTENSION IF NOT EXISTS is
-- idempotent on re-up and other databases in the instance may use it.
```

- [ ] **Step 5: Wire the migration files into the k8s ConfigMap**

In `deploy/base/kustomization.yaml`, add both `0007_skills.up.sql` and `0007_skills.down.sql` to the `worklode-migrations` `configMapGenerator` file list, following the 0005 entries.

- [ ] **Step 6: Run tests to verify pass (including down-migration round trip)**

Run: `go test ./internal/store/ -run 'TestMigrate' -v`
Expected: PASS (`TestMigrateAppliesMigrations` and `TestMigrateRoundTrip`).

- [ ] **Step 7: Commit**

```bash
git add deploy/base/migrations/0007_skills.up.sql deploy/base/migrations/0007_skills.down.sql deploy/base/kustomization.yaml docker-compose.yml .github/workflows/_test.yml internal/store/store_test.go
git commit -m "feat(store): skills schema (0007) + pgvector test images"
```

---

### Task 2: Store — skills registry CRUD

**Files:**
- Create: `internal/store/skills.go`, `internal/store/skills_test.go`

- [ ] **Step 1: Write the failing test**

`internal/store/skills_test.go` (in-package, like the other store tests):

```go
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

	changed, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1"))
	if err != nil || !changed {
		t.Fatalf("first upsert: changed=%v err=%v", changed, err)
	}
	// Same hash again: no change.
	changed, err = s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1"))
	if err != nil || changed {
		t.Fatalf("idempotent upsert: changed=%v err=%v", changed, err)
	}
	// New hash: new version, changed.
	changed, err = s.UpsertSkill(ctx, testSkillUpsert("tdd", "h2"))
	if err != nil || !changed {
		t.Fatalf("new-hash upsert: changed=%v err=%v", changed, err)
	}

	sk, err := s.GetSkill(ctx, "tdd")
	if err != nil {
		t.Fatalf("get: %v", err)
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
	if _, err := s.UpsertSkill(ctx, u); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("collision: %v", err)
	}

	// Soft delete everything from the repo except a kept set.
	if _, err := s.UpsertSkill(ctx, testSkillUpsert("debugging", "h9")); err != nil {
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
	if _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h2")); err != nil {
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

	// SkillsByNames preserves ask-order and reports misses via found map.
	got, err := s.SkillsByNames(ctx, []string{"debugging", "ghost", "tdd"})
	if err != nil {
		t.Fatalf("by names: %v", err)
	}
	if len(got) != 2 || got[0].Name != "debugging" || got[1].Name != "tdd" {
		t.Fatalf("by names order: %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/ -run TestUpsertSkillLifecycle -v`
Expected: FAIL — `UpsertSkill` undefined.

- [ ] **Step 3: Implement `internal/store/skills.go`**

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Skill is one org-wide agent skill at its latest synced version.
type Skill struct {
	ID          int64
	Name        string
	Description string
	SourceRepo  string
	SourcePath  string
	ContentHash string
	SkillMD     string
	Deleted     bool
}

// SkillMatch is one embedding-recommendation hit.
type SkillMatch struct {
	Name        string
	Description string
	ContentHash string
	Score       float64
}

// SkillUpsert is one skill dir as found in a source repo at sync time.
type SkillUpsert struct {
	Name        string
	Description string
	SourceRepo  string
	SourcePath  string
	GitCommit   string
	ContentHash string
	SkillMD     string
	Frontmatter json.RawMessage
	Archive     []byte
}

// UpsertSkill records the latest synced state of one skill and undeletes it.
// It reports changed=true when the content hash differs from the stored
// latest version (including brand-new skills) so the caller can re-embed.
func (s *Store) UpsertSkill(ctx context.Context, u SkillUpsert) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("upsert skill %s: %w", u.Name, err)
	}
	defer tx.Rollback()

	var id int64
	var repo, latestHash string
	err = tx.QueryRowContext(ctx, `
		SELECT s.id, s.source_repo, coalesce(v.content_hash, '')
		FROM skills s
		LEFT JOIN skill_versions v ON v.id = s.latest_version_id
		WHERE s.name = $1`, u.Name).Scan(&id, &repo, &latestHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO skills (name, description, source_repo, source_path)
			VALUES ($1, $2, $3, $4) RETURNING id`,
			u.Name, u.Description, u.SourceRepo, u.SourcePath).Scan(&id); err != nil {
			return false, fmt.Errorf("insert skill %s: %w", u.Name, err)
		}
	case err != nil:
		return false, fmt.Errorf("upsert skill %s: %w", u.Name, err)
	case repo != u.SourceRepo:
		return false, fmt.Errorf("skill %s already sourced from %s: %w", u.Name, repo, ErrInvalidInput)
	}

	if latestHash == u.ContentHash {
		if _, err := tx.ExecContext(ctx, `
			UPDATE skills SET deleted_at = NULL, description = $2, source_path = $3
			WHERE id = $1`, id, u.Description, u.SourcePath); err != nil {
			return false, fmt.Errorf("refresh skill %s: %w", u.Name, err)
		}
		return false, tx.Commit()
	}

	var versionID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO skill_versions (skill_id, git_commit, content_hash, frontmatter, skill_md, archive, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT ON CONSTRAINT skill_versions_hash_unique
		DO UPDATE SET git_commit = excluded.git_commit
		RETURNING id`,
		id, u.GitCommit, u.ContentHash, u.Frontmatter, u.SkillMD, u.Archive, s.Now()).Scan(&versionID)
	if err != nil {
		return false, fmt.Errorf("insert skill version %s@%s: %w", u.Name, u.ContentHash, err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE skills SET latest_version_id = $2, description = $3, source_path = $4, deleted_at = NULL
		WHERE id = $1`, id, versionID, u.Description, u.SourcePath); err != nil {
		return false, fmt.Errorf("point skill %s at version: %w", u.Name, err)
	}
	return true, tx.Commit()
}

// SoftDeleteSkillsExcept marks every live skill from sourceRepo whose name is
// not in keep as deleted, returning how many were marked.
func (s *Store) SoftDeleteSkillsExcept(ctx context.Context, sourceRepo string, keep []string) (int64, error) {
	keepJSON, err := json.Marshal(keep)
	if err != nil {
		return 0, fmt.Errorf("soft delete skills: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE skills SET deleted_at = $3
		WHERE source_repo = $1 AND deleted_at IS NULL
		  AND name NOT IN (SELECT jsonb_array_elements_text($2::jsonb))`,
		sourceRepo, string(keepJSON), s.Now())
	if err != nil {
		return 0, fmt.Errorf("soft delete skills from %s: %w", sourceRepo, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

const skillSelect = `
	SELECT s.id, s.name, s.description, s.source_repo, s.source_path,
	       coalesce(v.content_hash, ''), coalesce(v.skill_md, ''),
	       s.deleted_at IS NOT NULL
	FROM skills s
	LEFT JOIN skill_versions v ON v.id = s.latest_version_id`

func scanSkill(row interface{ Scan(...any) error }) (*Skill, error) {
	var sk Skill
	err := row.Scan(&sk.ID, &sk.Name, &sk.Description, &sk.SourceRepo, &sk.SourcePath,
		&sk.ContentHash, &sk.SkillMD, &sk.Deleted)
	if err != nil {
		return nil, err
	}
	return &sk, nil
}

// GetSkill returns one skill (deleted or not) by name.
func (s *Store) GetSkill(ctx context.Context, name string) (*Skill, error) {
	sk, err := scanSkill(s.db.QueryRowContext(ctx, skillSelect+` WHERE s.name = $1`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("skill %s: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get skill %s: %w", name, err)
	}
	return sk, nil
}

// ListSkills returns skills ordered by name, excluding soft-deleted ones
// unless includeDeleted is set.
func (s *Store) ListSkills(ctx context.Context, includeDeleted bool) ([]Skill, error) {
	q := skillSelect
	if !includeDeleted {
		q += ` WHERE s.deleted_at IS NULL`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY s.name`)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()
	var out []Skill
	for rows.Next() {
		sk, err := scanSkill(rows)
		if err != nil {
			return nil, fmt.Errorf("list skills: %w", err)
		}
		out = append(out, *sk)
	}
	return out, rows.Err()
}

// SkillsByNames returns the named skills (deleted included, so brief pins can
// warn rather than vanish), ordered as asked; missing names are simply absent.
func (s *Store) SkillsByNames(ctx context.Context, names []string) ([]Skill, error) {
	if len(names) == 0 {
		return nil, nil
	}
	namesJSON, err := json.Marshal(names)
	if err != nil {
		return nil, fmt.Errorf("skills by names: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, skillSelect+`
		JOIN (SELECT value AS want, ordinality
		      FROM jsonb_array_elements_text($1::jsonb) WITH ORDINALITY) w
		  ON w.want = s.name
		ORDER BY w.ordinality`, string(namesJSON))
	if err != nil {
		return nil, fmt.Errorf("skills by names: %w", err)
	}
	defer rows.Close()
	var out []Skill
	for rows.Next() {
		sk, err := scanSkill(rows)
		if err != nil {
			return nil, fmt.Errorf("skills by names: %w", err)
		}
		out = append(out, *sk)
	}
	return out, rows.Err()
}

// SkillArchive returns the stored tar.gz for one exact version.
func (s *Store) SkillArchive(ctx context.Context, name, hash string) ([]byte, error) {
	var archive []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT v.archive FROM skill_versions v
		JOIN skills s ON s.id = v.skill_id
		WHERE s.name = $1 AND v.content_hash = $2`, name, hash).Scan(&archive)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("skill archive %s@%s: %w", name, hash, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("skill archive %s@%s: %w", name, hash, err)
	}
	return archive, nil
}
```

Note: `skillSelect` joins on `latest_version_id`, so `SkillsByNames`'s extra JOIN clause must come after the existing LEFT JOIN — it composes as written because `skillSelect` ends after the LEFT JOIN line.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/ -run TestUpsertSkillLifecycle -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/skills.go internal/store/skills_test.go
git commit -m "feat(store): skills registry CRUD"
```

---

### Task 3: Store — embeddings and recommend query

**Files:**
- Create: `internal/store/skill_embeddings.go`, `internal/store/skill_embeddings_test.go`

- [ ] **Step 1: Write the failing test**

`internal/store/skill_embeddings_test.go`:

```go
package store

import (
	"context"
	"testing"
)

func TestSkillEmbeddingsRecommend(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"tdd", "debugging"} {
		if _, err := s.UpsertSkill(ctx, testSkillUpsert(name, "h-"+name)); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	tdd, _ := s.GetSkill(ctx, "tdd")
	dbg, _ := s.GetSkill(ctx, "debugging")

	// Orthogonal-ish unit vectors: query matches tdd chunk 1 best.
	if err := s.ReplaceSkillEmbeddings(ctx, tdd.ID, [][]float32{{1, 0, 0}, {0.9, 0.1, 0}}); err != nil {
		t.Fatalf("embed tdd: %v", err)
	}
	if err := s.ReplaceSkillEmbeddings(ctx, dbg.ID, [][]float32{{0, 1, 0}}); err != nil {
		t.Fatalf("embed dbg: %v", err)
	}

	got, err := s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0.5)
	if err != nil {
		t.Fatalf("recommend: %v", err)
	}
	if len(got) != 1 || got[0].Name != "tdd" || got[0].Score < 0.99 {
		t.Fatalf("recommend: %+v", got)
	}

	// Floor at 0 returns both, best-first, max over chunks.
	got, err = s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0)
	if err != nil || len(got) != 2 || got[0].Name != "tdd" {
		t.Fatalf("recommend all: %+v err=%v", got, err)
	}

	// Replace wipes old chunks.
	if err := s.ReplaceSkillEmbeddings(ctx, tdd.ID, [][]float32{{0, 0, 1}}); err != nil {
		t.Fatalf("re-embed: %v", err)
	}
	got, _ = s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0.5)
	if len(got) != 0 {
		t.Fatalf("after replace: %+v", got)
	}

	// Soft-deleted skills are never recommended.
	if err := s.ReplaceSkillEmbeddings(ctx, dbg.ID, [][]float32{{1, 0, 0}}); err != nil {
		t.Fatalf("re-embed dbg: %v", err)
	}
	if _, err := s.SoftDeleteSkillsExcept(ctx, "acme/claude-plugins", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ = s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0.5)
	if len(got) != 0 {
		t.Fatalf("deleted still recommended: %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/ -run TestSkillEmbeddingsRecommend -v`
Expected: FAIL — `ReplaceSkillEmbeddings` undefined.

- [ ] **Step 3: Implement `internal/store/skill_embeddings.go`**

```go
package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// vectorLiteral renders v in pgvector's text input format, e.g. "[1,0.5]".
// Vectors are bound as text and cast with ::vector — no client-side pgvector
// dependency needed.
func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// ReplaceSkillEmbeddings swaps the full chunk-vector set for one skill.
func (s *Store) ReplaceSkillEmbeddings(ctx context.Context, skillID int64, vecs [][]float32) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace skill embeddings %d: %w", skillID, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_embeddings WHERE skill_id = $1`, skillID); err != nil {
		return fmt.Errorf("clear skill embeddings %d: %w", skillID, err)
	}
	for i, v := range vecs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO skill_embeddings (skill_id, chunk_index, embedding)
			VALUES ($1, $2, $3::vector)`, skillID, i, vectorLiteral(v)); err != nil {
			return fmt.Errorf("insert skill embedding %d/%d: %w", skillID, i, err)
		}
	}
	return tx.Commit()
}

// RecommendSkills returns live skills scored by max cosine similarity over
// their chunks against query, best-first, at or above floor.
func (s *Store) RecommendSkills(ctx context.Context, query []float32, limit int, floor float64) ([]SkillMatch, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.name, s.description, coalesce(v.content_hash, ''),
		       max(1 - (e.embedding <=> $1::vector)) AS score
		FROM skill_embeddings e
		JOIN skills s ON s.id = e.skill_id
		LEFT JOIN skill_versions v ON v.id = s.latest_version_id
		WHERE s.deleted_at IS NULL
		GROUP BY s.name, s.description, v.content_hash
		HAVING max(1 - (e.embedding <=> $1::vector)) >= $2
		ORDER BY score DESC
		LIMIT $3`, vectorLiteral(query), floor, limit)
	if err != nil {
		return nil, fmt.Errorf("recommend skills: %w", err)
	}
	defer rows.Close()
	var out []SkillMatch
	for rows.Next() {
		var m SkillMatch
		if err := rows.Scan(&m.Name, &m.Description, &m.ContentHash, &m.Score); err != nil {
			return nil, fmt.Errorf("recommend skills: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/ -run TestSkillEmbeddingsRecommend -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/skill_embeddings.go internal/store/skill_embeddings_test.go
git commit -m "feat(store): skill embeddings + cosine recommend query"
```

---

### Task 4: `internal/embed` — chunker + OpenAI-compatible provider

**Files:**
- Create: `internal/embed/embed.go`, `internal/embed/embed_test.go`

- [ ] **Step 1: Write the failing test**

`internal/embed/embed_test.go`:

```go
package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChunks(t *testing.T) {
	if got := Chunks("", 10, 2); got != nil {
		t.Fatalf("empty: %v", got)
	}
	if got := Chunks("short", 10, 2); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short: %v", got)
	}
	got := Chunks(strings.Repeat("a", 25), 10, 2)
	// step = 8: [0:10] [8:18] [16:25]
	if len(got) != 3 || len(got[0]) != 10 || len(got[2]) != 9 {
		t.Fatalf("overlap: %d chunks, lens %d/%d", len(got), len(got[0]), len(got[len(got)-1]))
	}
}

func TestOpenAIEmbed(t *testing.T) {
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		gotModel = req.Model
		// Return out of order to prove index-based reassembly.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":1,"embedding":[0.5,0.5]},{"index":0,"embedding":[1,0]}]}`))
	}))
	defer srv.Close()

	p := &OpenAI{URL: srv.URL, Model: "test-model", Key: "sk-x"}
	vecs, err := p.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][0] != 0.5 {
		t.Fatalf("vecs: %v", vecs)
	}
	if gotAuth != "Bearer sk-x" || gotModel != "test-model" {
		t.Fatalf("auth=%q model=%q", gotAuth, gotModel)
	}
}

func TestOpenAIEmbedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"nope"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	p := &OpenAI{URL: srv.URL, Model: "m"}
	if _, err := p.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("want error")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/embed/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement `internal/embed/embed.go`**

```go
// Package embed abstracts text-embedding computation behind a small provider
// interface. The server holds the only credentials; agents never embed.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

// Provider computes one vector per input text, order-preserving.
type Provider interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Chunk sizing for SKILL.md bodies and recommend-query text, in runes.
// ~4 chars/token puts 6000 runes safely inside every mainstream embedding
// model's window; the overlap keeps boundary-spanning matches findable.
const (
	ChunkRunes   = 6000
	ChunkOverlap = 600
)

// Chunks splits s into overlapping chunks of at most size runes.
func Chunks(s string, size, overlap int) []string {
	r := []rune(s)
	if len(r) == 0 {
		return nil
	}
	if size <= 0 {
		size = ChunkRunes
	}
	if overlap < 0 || overlap >= size {
		overlap = 0
	}
	step := size - overlap
	var out []string
	for start := 0; ; start += step {
		end := start + size
		if end >= len(r) {
			out = append(out, string(r[start:]))
			return out
		}
		out = append(out, string(r[start:end]))
	}
}

// Truncate returns at most n leading runes of s.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// OpenAI calls an OpenAI-compatible embeddings endpoint (the full URL,
// e.g. https://api.example.com/v1/embeddings).
type OpenAI struct {
	URL   string
	Model string
	Key   string
	// HTTPClient overrides the default 30s-timeout client (tests).
	HTTPClient *http.Client
}

func (p *OpenAI) client() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (p *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"model": p.Model, "input": texts})
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.Key != "" {
		req.Header.Set("Authorization", "Bearer "+p.Key)
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("embed: %s returned %d: %s", p.URL, resp.StatusCode, msg)
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w", err)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embed: got %d vectors for %d inputs", len(out.Data), len(texts))
	}
	sort.Slice(out.Data, func(i, j int) bool { return out.Data[i].Index < out.Data[j].Index })
	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/embed/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/embed/
git commit -m "feat(embed): chunker + OpenAI-compatible embedding provider"
```

---

### Task 5: `githubauth` — repo tarball download

**Files:**
- Create: `internal/githubauth/content.go`
- Modify/Test: `internal/githubauth/content_test.go` (new file; reuse the `httptest.Server` + `AppAuth.BaseURL` pattern from `internal/githubauth/app_test.go`)

- [ ] **Step 1: Write the failing test**

`internal/githubauth/content_test.go` — follow `app_test.go`'s fixture style exactly (same fake-installation-token endpoints; copy its helper for constructing a test `AppAuth` with `BaseURL` pointed at the server). The new assertions:

```go
func TestTarball(t *testing.T) {
	// Fake API: installation lookup + token mint (copied pattern from
	// app_test.go), plus the tarball endpoint returning bytes.
	mux := http.NewServeMux()
	registerFakeInstallationEndpoints(t, mux) // as app_test.go does inline; extract or repeat
	mux.HandleFunc("GET /repos/acme/plugins/tarball/main", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got == "" {
			t.Errorf("tarball request missing auth")
		}
		w.Write([]byte("fake-tarball-bytes"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newTestAppAuth(t, srv.URL) // same constructor style as app_test.go
	got, err := a.Tarball(context.Background(), "acme/plugins", "main")
	if err != nil {
		t.Fatalf("tarball: %v", err)
	}
	if string(got) != "fake-tarball-bytes" {
		t.Fatalf("tarball bytes: %q", got)
	}
}
```

(If `app_test.go` has no reusable helpers, inline the same fake endpoints it uses — do not invent a new fixture style.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/githubauth/ -run TestTarball -v`
Expected: FAIL — `Tarball` undefined.

- [ ] **Step 3: Implement `internal/githubauth/content.go`**

```go
package githubauth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// maxTarball caps a skill-source repo download. Source repos are docs-sized;
// anything bigger is a misconfiguration, not a skill collection.
const maxTarball = 64 << 20

// Tarball downloads the repo tarball at ref using an installation token.
// The result is a gzipped tar whose entries share a single
// "<owner>-<repo>-<sha>/" root directory (GitHub's tarball format).
func (a *AppAuth) Tarball(ctx context.Context, repo, ref string) ([]byte, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	tok, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("tarball %s@%s: %w", repo, ref, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		a.BaseURL+"/repos/"+p+"/tarball/"+url.PathEscape(ref), nil)
	if err != nil {
		return nil, fmt.Errorf("tarball %s@%s: %w", repo, ref, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	// GitHub answers 302 to a signed codeload URL; the default client follows
	// it and Go strips the Authorization header on the cross-host hop, which
	// is exactly right — the redirect URL carries its own auth.
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("tarball %s@%s: %w", repo, ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("tarball %s@%s: status %d: %s", repo, ref, resp.StatusCode, msg)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTarball+1))
	if err != nil {
		return nil, fmt.Errorf("tarball %s@%s: %w", repo, ref, err)
	}
	if len(data) > maxTarball {
		return nil, fmt.Errorf("tarball %s@%s: exceeds %d bytes", repo, ref, maxTarball)
	}
	return data, nil
}
```

Adjust field/helper names (`BaseURL`, `repoPath`, `InstallationToken`) to the actual ones in `internal/githubauth/app.go` if they differ — mirror `DiscoverDoneState`'s request construction.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/githubauth/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/githubauth/content.go internal/githubauth/content_test.go
git commit -m "feat(githubauth): repo tarball download via installation token"
```

---

### Task 6: `internal/skillsync` — source config + sync engine

**Files:**
- Create: `internal/skillsync/skillsync.go`, `internal/skillsync/sources.go`, `internal/skillsync/skillsync_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/skillsync/skillsync_test.go`:

```go
package skillsync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestParseSources(t *testing.T) {
	got, err := ParseSources("acme/claude-plugins@main:plugins/*/skills/*,acme/skills@v1:skills/*")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 || got[0].Repo != "acme/claude-plugins" || got[0].Ref != "main" ||
		got[0].Glob != "plugins/*/skills/*" || got[1].Ref != "v1" {
		t.Fatalf("parse: %+v", got)
	}
	if s, err := ParseSources(""); err != nil || s != nil {
		t.Fatalf("empty: %v %v", s, err)
	}
	for _, bad := range []string{"norepo", "a/b:glob", "a/b@ref", "a/b@ref:"} {
		if _, err := ParseSources(bad); err == nil {
			t.Fatalf("want error for %q", bad)
		}
	}
}

// tarballOf builds a GitHub-shaped tarball: entries under root/, gzipped.
func tarballOf(t *testing.T, root string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for p, c := range files {
		if err := tw.WriteHeader(&tar.Header{Name: root + "/" + p, Mode: 0o644, Size: int64(len(c)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(c)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

const skillMD = "---\nname: tdd\ndescription: Red-green-refactor discipline\n---\n\nUse TDD always."

type fakeEmbed struct{ calls int }

func (f *fakeEmbed) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.calls++
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}

func TestSyncAll(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()

	tb := tarballOf(t, "acme-claude-plugins-abc123", map[string]string{
		"plugins/sp/skills/tdd/SKILL.md":            skillMD,
		"plugins/sp/skills/tdd/references/notes.md": "extra notes",
		"plugins/sp/skills/noskillmd/other.md":      "not a skill (no SKILL.md)",
		"README.md":                                 "not under glob",
	})
	fetch := func(ctx context.Context, repo, ref string) ([]byte, error) { return tb, nil }
	emb := &fakeEmbed{}
	sy := &Syncer{Store: st, Fetch: fetch, Embed: emb}
	src := []Source{{Repo: "acme/claude-plugins", Ref: "main", Glob: "plugins/*/skills/*"}}

	sum, err := sy.SyncAll(ctx, src)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if sum.Synced != 1 || sum.Deleted != 0 {
		t.Fatalf("summary: %+v", sum)
	}
	sk, err := st.GetSkill(ctx, "tdd")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sk.Description != "Red-green-refactor discipline" || sk.SkillMD != skillMD {
		t.Fatalf("skill: %+v", sk)
	}
	if emb.calls != 1 {
		t.Fatalf("embed calls: %d", emb.calls)
	}
	// Archive is a readable tar.gz containing both files.
	arch, err := st.SkillArchive(ctx, "tdd", sk.ContentHash)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	names := tarNames(t, arch)
	if len(names) != 2 || names["SKILL.md"] == false || names["references/notes.md"] == false {
		t.Fatalf("archive entries: %v", names)
	}

	// Second sync, unchanged content: no re-embed.
	if _, err := sy.SyncAll(ctx, src); err != nil {
		t.Fatalf("resync: %v", err)
	}
	if emb.calls != 1 {
		t.Fatalf("re-embedded unchanged skill: %d calls", emb.calls)
	}

	// Skill dir removed upstream: soft-deleted.
	empty := tarballOf(t, "acme-claude-plugins-def456", map[string]string{"README.md": "x"})
	sy.Fetch = func(ctx context.Context, repo, ref string) ([]byte, error) { return empty, nil }
	sum, err = sy.SyncAll(ctx, src)
	if err != nil || sum.Deleted != 1 {
		t.Fatalf("delete sync: %+v err=%v", sum, err)
	}
}

func tarNames(t *testing.T, gzBytes []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(gzBytes))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	out := map[string]bool{}
	for {
		h, err := tr.Next()
		if err != nil {
			return out
		}
		out[h.Name] = true
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/skillsync/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement `internal/skillsync/sources.go`**

```go
package skillsync

import (
	"fmt"
	"strings"
)

// Source is one configured skill source: a repo tree at a ref, filtered by a
// dir glob that names skill directories (each containing a SKILL.md).
// Wire format (LODE_SKILL_SOURCES): "owner/repo@ref:glob[,owner/repo@ref:glob...]".
type Source struct {
	Repo string // owner/name
	Ref  string // branch or tag
	Glob string // path.Match pattern for skill dirs, e.g. plugins/*/skills/*
}

// ParseSources parses the LODE_SKILL_SOURCES value. Empty means no sources.
func ParseSources(s string) ([]Source, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []Source
	for _, entry := range strings.Split(s, ",") {
		repo, rest, ok := strings.Cut(entry, "@")
		if !ok || !strings.Contains(repo, "/") {
			return nil, fmt.Errorf("skill source %q: want owner/repo@ref:glob", entry)
		}
		ref, glob, ok := strings.Cut(rest, ":")
		if !ok || ref == "" || glob == "" {
			return nil, fmt.Errorf("skill source %q: want owner/repo@ref:glob", entry)
		}
		out = append(out, Source{Repo: strings.TrimSpace(repo), Ref: ref, Glob: glob})
	}
	return out, nil
}

// MatchesPush reports whether a push to repo's branch should trigger a sync.
func MatchesPush(sources []Source, repo, branch string) bool {
	for _, src := range sources {
		if src.Repo == repo && src.Ref == branch {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Implement `internal/skillsync/skillsync.go`**

```go
// Package skillsync ingests skill directories from configured git source
// repos into the backbone: parse SKILL.md frontmatter, content-hash the dir,
// archive it, and (when a provider is configured) embed the SKILL.md body.
package skillsync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path"
	"sort"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// maxSkillBytes caps one skill dir's total content; larger dirs are skipped
// with a warning. Skills are prose, not payloads.
const maxSkillBytes = 1 << 20

// FetchFunc downloads a repo tarball at a ref (githubauth.AppAuth.Tarball).
type FetchFunc func(ctx context.Context, repo, ref string) ([]byte, error)

type Syncer struct {
	Store *store.Store
	Fetch FetchFunc
	Embed embed.Provider // nil = pins-only instance, skip embedding
	Log   *slog.Logger   // nil = slog.Default()
}

type Summary struct {
	Synced  int `json:"synced"`
	Deleted int `json:"deleted"`
}

func (sy *Syncer) log() *slog.Logger {
	if sy.Log != nil {
		return sy.Log
	}
	return slog.Default()
}

// SyncAll fully syncs every source: upsert found skills, soft-delete the
// missing, re-embed the changed. One bad source aborts (operator config).
func (sy *Syncer) SyncAll(ctx context.Context, sources []Source) (Summary, error) {
	var sum Summary
	for _, src := range sources {
		n, d, err := sy.syncSource(ctx, src)
		if err != nil {
			return sum, fmt.Errorf("sync %s@%s: %w", src.Repo, src.Ref, err)
		}
		sum.Synced += n
		sum.Deleted += d
	}
	return sum, nil
}

func (sy *Syncer) syncSource(ctx context.Context, src Source) (synced, deleted int, err error) {
	tb, err := sy.Fetch(ctx, src.Repo, src.Ref)
	if err != nil {
		return 0, 0, err
	}
	dirs, commit, err := skillDirs(tb, src.Glob)
	if err != nil {
		return 0, 0, err
	}
	var seen []string
	for dir, files := range sortedDirs(dirs) {
		_ = dir
		u, err := buildUpsert(src, commit, files.dir, files.files)
		if err != nil {
			sy.log().Warn("skipping skill dir", "dir", files.dir, "err", err)
			continue
		}
		changed, err := sy.Store.UpsertSkill(ctx, *u)
		if err != nil {
			sy.log().Warn("skill upsert failed", "skill", u.Name, "err", err)
			continue
		}
		seen = append(seen, u.Name)
		synced++
		if changed && sy.Embed != nil {
			if err := sy.embedSkill(ctx, u.Name, u.Description, u.SkillMD); err != nil {
				sy.log().Warn("skill embed failed", "skill", u.Name, "err", err)
			}
		}
	}
	n, err := sy.Store.SoftDeleteSkillsExcept(ctx, src.Repo, seen)
	if err != nil {
		return synced, 0, err
	}
	return synced, int(n), nil
}

func (sy *Syncer) embedSkill(ctx context.Context, name, description, skillMD string) error {
	chunks := embed.Chunks(description+"\n\n"+skillMD, embed.ChunkRunes, embed.ChunkOverlap)
	vecs, err := sy.Embed.Embed(ctx, chunks)
	if err != nil {
		return err
	}
	sk, err := sy.Store.GetSkill(ctx, name)
	if err != nil {
		return err
	}
	return sy.Store.ReplaceSkillEmbeddings(ctx, sk.ID, vecs)
}

type skillDir struct {
	dir   string
	files map[string][]byte // paths relative to dir
}

// sortedDirs yields deterministic iteration order for logging/tests.
func sortedDirs(m map[string]skillDir) map[int]skillDir {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[int]skillDir, len(m))
	for i, k := range keys {
		out[i] = m[k]
	}
	return out
}

// skillDirs walks the tarball and groups files by the skill dir (the glob
// match) that owns them. Also extracts the commit sha from the root dir name
// ("owner-repo-<sha>/").
func skillDirs(tgz []byte, glob string) (map[string]skillDir, string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, "", fmt.Errorf("gunzip tarball: %w", err)
	}
	tr := tar.NewReader(gz)
	segs := strings.Count(glob, "/") + 1
	dirs := map[string]skillDir{}
	var commit string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("read tarball: %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		root, rel, ok := strings.Cut(path.Clean(h.Name), "/")
		if !ok || rel == "" {
			continue
		}
		if commit == "" {
			if i := strings.LastIndex(root, "-"); i >= 0 {
				commit = root[i+1:]
			}
		}
		parts := strings.Split(rel, "/")
		if len(parts) <= segs { // file directly at or above skill-dir depth
			continue
		}
		dir := strings.Join(parts[:segs], "/")
		if ok, _ := path.Match(glob, dir); !ok {
			continue
		}
		content, err := io.ReadAll(io.LimitReader(tr, maxSkillBytes+1))
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", h.Name, err)
		}
		d, exists := dirs[dir]
		if !exists {
			d = skillDir{dir: dir, files: map[string][]byte{}}
		}
		d.files[strings.Join(parts[segs:], "/")] = content
		dirs[dir] = d
	}
	// Only dirs with a SKILL.md are skills; drop oversized dirs.
	for dir, d := range dirs {
		if _, ok := d.files["SKILL.md"]; !ok {
			delete(dirs, dir)
			continue
		}
		total := 0
		for _, c := range d.files {
			total += len(c)
		}
		if total > maxSkillBytes {
			delete(dirs, dir)
		}
	}
	return dirs, commit, nil
}

func buildUpsert(src Source, commit, dir string, files map[string][]byte) (*store.SkillUpsert, error) {
	md := string(files["SKILL.md"])
	name, description := parseFrontmatter(md)
	if name == "" {
		return nil, fmt.Errorf("SKILL.md has no frontmatter name")
	}
	fm, err := json.Marshal(map[string]string{"name": name, "description": description})
	if err != nil {
		return nil, err
	}
	archive, err := buildArchive(files)
	if err != nil {
		return nil, err
	}
	return &store.SkillUpsert{
		Name: name, Description: description,
		SourceRepo: src.Repo, SourcePath: dir,
		GitCommit: commit, ContentHash: contentHash(files),
		SkillMD: md, Frontmatter: fm, Archive: archive,
	}, nil
}

// contentHash is sha256 over the sorted (path, content) pairs — independent
// of archive encoding, so the local cache key never churns on tar details.
func contentHash(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(files[p]))
		h.Write(files[p])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// buildArchive produces a deterministic tar.gz: sorted paths, fixed mode,
// zero timestamps.
func buildArchive(files map[string][]byte) ([]byte, error) {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, p := range paths {
		if err := tw.WriteHeader(&tar.Header{Name: p, Mode: 0o644, Size: int64(len(files[p])), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(files[p]); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// parseFrontmatter extracts name and description from a leading "---" YAML
// block. Deliberately minimal: single-line "key: value" scalars only, which
// is what the SKILL.md convention uses. No YAML dependency.
func parseFrontmatter(md string) (name, description string) {
	lines := strings.Split(md, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			name = val
		case "description":
			description = val
		}
	}
	return name, description
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/skillsync/ -v`
Expected: PASS (fix compile details against the real store API as needed — the test is the contract).

- [ ] **Step 6: Commit**

```bash
git add internal/skillsync/
git commit -m "feat(skillsync): source config parsing + tarball sync engine"
```

---

### Task 7: API — config wiring + skills endpoints

**Files:**
- Create: `internal/api/skills.go`, `internal/api/skills_test.go`
- Modify: `internal/api/server.go` (Config fields, server struct fields, route registrations, boot validation), `internal/cmd/serve.go` (env wiring)

- [ ] **Step 1: Add config + server plumbing**

In `internal/api/server.go`:

1. Add to `Config` (with the same comment style as neighbors):

```go
	// SkillSources configures org skill source repos, comma-separated
	// "owner/repo@ref:glob" entries. LODE_SKILL_SOURCES. Requires the GitHub
	// App to be configured. Unset: skill sync off.
	SkillSources string
	// EmbeddingURL is a full OpenAI-compatible embeddings endpoint URL.
	// LODE_EMBEDDING_URL. Unset: recommendations run pins-only.
	EmbeddingURL string
	// EmbeddingModel names the model sent to EmbeddingURL. LODE_EMBEDDING_MODEL.
	EmbeddingModel string
	// EmbeddingAPIKey authenticates against EmbeddingURL. LODE_EMBEDDING_API_KEY.
	EmbeddingAPIKey string
	// SkillScoreFloor is the minimum cosine similarity for a recommendation,
	// default 0.35. LODE_SKILL_SCORE_FLOOR.
	SkillScoreFloor string
```

2. Add to the `server` struct:

```go
	embedder     embed.Provider     // nil = pins-only
	skillSyncer  *skillsync.Syncer  // nil = sync off
	skillSources []skillsync.Source
	skillFloor   float64
	skillSyncMu  sync.Mutex
```

3. In `NewServer`, after the existing feature validation (mirror the "unset off / malformed fatal" pattern):

```go
	s.skillFloor = 0.35
	if cfg.SkillScoreFloor != "" {
		f, err := strconv.ParseFloat(cfg.SkillScoreFloor, 64)
		if err != nil || f < 0 || f > 1 {
			return nil, nil, fmt.Errorf("LODE_SKILL_SCORE_FLOOR: want a float in [0,1], got %q", cfg.SkillScoreFloor)
		}
		s.skillFloor = f
	}
	if cfg.EmbeddingURL != "" {
		if cfg.EmbeddingModel == "" {
			return nil, nil, fmt.Errorf("LODE_EMBEDDING_MODEL is required when LODE_EMBEDDING_URL is set")
		}
		s.embedder = &embed.OpenAI{URL: cfg.EmbeddingURL, Model: cfg.EmbeddingModel, Key: cfg.EmbeddingAPIKey}
	}
	sources, err := skillsync.ParseSources(cfg.SkillSources)
	if err != nil {
		return nil, nil, fmt.Errorf("LODE_SKILL_SOURCES: %w", err)
	}
	if len(sources) > 0 {
		if appAuth == nil {
			return nil, nil, fmt.Errorf("LODE_SKILL_SOURCES requires the GitHub App (LODE_GITHUB_APP_ID/LODE_GITHUB_APP_PRIVATE_KEY)")
		}
		s.skillSources = sources
		s.skillSyncer = &skillsync.Syncer{Store: st, Fetch: appAuth.Tarball, Embed: s.embedder, Log: s.log}
	}
```

4. Register routes next to the task routes:

```go
	mux.Handle("GET /api/v1/skills", s.auth(s.listSkills))
	mux.Handle("GET /api/v1/skills/{name}", s.auth(s.getSkill))
	mux.Handle("GET /api/v1/skills/{name}/archive/{hash}", s.auth(s.skillArchive))
	mux.Handle("POST /api/v1/skills/recommend", s.auth(s.recommendSkills))
	mux.Handle("POST /api/v1/skills/sync", s.auth(requireAdmin(s.syncSkills)))
```

In `internal/cmd/serve.go`, add the five `os.Getenv` lines to the `api.Config` literal: `SkillSources: os.Getenv("LODE_SKILL_SOURCES")`, `EmbeddingURL: os.Getenv("LODE_EMBEDDING_URL")`, `EmbeddingModel: os.Getenv("LODE_EMBEDDING_MODEL")`, `EmbeddingAPIKey: os.Getenv("LODE_EMBEDDING_API_KEY")`, `SkillScoreFloor: os.Getenv("LODE_SKILL_SCORE_FLOOR")`.

- [ ] **Step 2: Write the failing endpoint tests**

`internal/api/skills_test.go` (external `package api_test`, using `newTestServer`, `newTestServerAdmin`, `doReq`, `decodeMap` from `server_test.go`). Seed skills directly through the store handle that `newTestServer` returns (`st.UpsertSkill(...)` + `st.ReplaceSkillEmbeddings(...)` — same seeding style other api tests use for tasks):

```go
func TestSkillsEndpoints(t *testing.T) {
	st, h, token := newTestServer(t)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")   // helper: UpsertSkill with hash "h-tdd"
	seedSkill(t, st, "debugging", "Systematic debugging loop")

	// List.
	rr := doReq(t, h, "GET", "/api/v1/skills", token, nil)
	if rr.Code != 200 {
		t.Fatalf("list: %d %s", rr.Code, rr.Body)
	}
	// Get.
	rr = doReq(t, h, "GET", "/api/v1/skills/tdd", token, nil)
	if rr.Code != 200 {
		t.Fatalf("get: %d", rr.Code)
	}
	// Archive round-trips bytes with the right content type.
	rr = doReq(t, h, "GET", "/api/v1/skills/tdd/archive/h-tdd", token, nil)
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("archive: %d %q", rr.Code, rr.Header().Get("Content-Type"))
	}
	rr = doReq(t, h, "GET", "/api/v1/skills/tdd/archive/wrong", token, nil)
	if rr.Code != 404 {
		t.Fatalf("archive miss: %d", rr.Code)
	}
	// Recommend without a provider: pins-only degradation, provider "none".
	rr = doReq(t, h, "POST", "/api/v1/skills/recommend", token,
		map[string]any{"text": "write tests first"})
	if rr.Code != 200 {
		t.Fatalf("recommend: %d %s", rr.Code, rr.Body)
	}
	body := decodeMap(t, rr)
	if body["provider"] != "none" {
		t.Fatalf("provider: %v", body["provider"])
	}
	// Sync without configuration: 422.
	_, hAdmin, adminToken := newTestServerAdmin(t)
	rr = doReq(t, hAdmin, "POST", "/api/v1/skills/sync", adminToken, nil)
	if rr.Code != 422 {
		t.Fatalf("sync unconfigured: %d", rr.Code)
	}
	// Sync as non-admin: 403.
	rr = doReq(t, h, "POST", "/api/v1/skills/sync", token, nil)
	if rr.Code != 403 {
		t.Fatalf("sync non-admin: %d", rr.Code)
	}
}
```

Also add `TestRecommendWithProvider`: construct the handler via `api.NewServer(st, api.Config{EmbeddingURL: fakeSrv.URL, EmbeddingModel: "m"})` where `fakeSrv` is an `httptest.Server` returning a fixed vector (as in the embed tests), seed one skill + embeddings whose vector matches, POST `{"task_id": <seeded task>}` and `{"text": "..."}`, assert a match with `score`, and that a name pinned on the task appears in `pinned` and not `matches`.

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/api/ -run TestSkills -v`
Expected: FAIL — handlers undefined.

- [ ] **Step 4: Implement `internal/api/skills.go`**

```go
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// recommendTimeout bounds the embedding-provider call on the recommend and
// brief paths; on expiry the response degrades to pins-only.
const recommendTimeout = 2 * time.Second

type skillJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SourceRepo  string `json:"source_repo"`
	Hash        string `json:"hash"`
	Deleted     bool   `json:"deleted"`
}

type skillMatchJSON struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Hash        string  `json:"hash"`
	Score       float64 `json:"score"`
}

type pinnedSkillJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hash        string `json:"hash"`
	Content     string `json:"content"`
}

type recommendationJSON struct {
	Pinned      []pinnedSkillJSON `json:"pinned"`
	Matches     []skillMatchJSON  `json:"matches"`
	Warnings    []string          `json:"warnings"`
	Provider    string            `json:"provider"`
}

func toSkillJSON(sk store.Skill) skillJSON {
	return skillJSON{Name: sk.Name, Description: sk.Description, SourceRepo: sk.SourceRepo,
		Hash: sk.ContentHash, Deleted: sk.Deleted}
}

func (s *server) listSkills(w http.ResponseWriter, r *http.Request) {
	skills, err := s.st.ListSkills(r.Context(), r.URL.Query().Get("deleted") == "true")
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	out := make([]skillJSON, 0, len(skills))
	for _, sk := range skills {
		out = append(out, toSkillJSON(sk))
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

func (s *server) getSkill(w http.ResponseWriter, r *http.Request) {
	sk, err := s.st.GetSkill(r.Context(), r.PathValue("name"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSkillJSON(*sk))
}

func (s *server) skillArchive(w http.ResponseWriter, r *http.Request) {
	data, err := s.st.SkillArchive(r.Context(), r.PathValue("name"), r.PathValue("hash"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

type recommendRequest struct {
	TaskID string `json:"task_id"`
	Text   string `json:"text"`
	Limit  int    `json:"limit"`
}

func (s *server) recommendSkills(w http.ResponseWriter, r *http.Request) {
	var req recommendRequest
	if !readJSON(w, r, &req) { // match the actual readJSON signature in server.go
		return
	}
	if (req.TaskID == "") == (req.Text == "") {
		writeErr(w, http.StatusUnprocessableEntity, "exactly one of task_id or text is required")
		return
	}
	var pins []string
	text := req.Text
	if req.TaskID != "" {
		task, err := s.st.GetTask(r.Context(), req.TaskID) // use the actual task-fetch used by taskBrief
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		text = task.Title + "\n\n" + task.Body
		pins = task.Skills
	}
	rec, err := s.recommendation(r.Context(), text, pins, req.Limit)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// recommendation resolves pins (inline content) and, when a provider is
// configured, embedding matches. Provider failures degrade to pins-only with
// a warning — recommendations never block work.
func (s *server) recommendation(ctx context.Context, text string, pins []string, limit int) (*recommendationJSON, error) {
	rec := &recommendationJSON{
		Pinned: []pinnedSkillJSON{}, Matches: []skillMatchJSON{}, Warnings: []string{},
		Provider: "none",
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	pinnedNames := map[string]bool{}
	if len(pins) > 0 {
		skills, err := s.st.SkillsByNames(ctx, pins)
		if err != nil {
			return nil, err
		}
		found := map[string]store.Skill{}
		for _, sk := range skills {
			found[sk.Name] = sk
		}
		for _, name := range pins {
			sk, ok := found[name]
			if !ok {
				rec.Warnings = append(rec.Warnings, "pinned skill not found: "+name)
				continue
			}
			if sk.Deleted {
				rec.Warnings = append(rec.Warnings, "pinned skill removed from its source repo: "+name)
			}
			pinnedNames[name] = true
			rec.Pinned = append(rec.Pinned, pinnedSkillJSON{
				Name: sk.Name, Description: sk.Description, Hash: sk.ContentHash, Content: sk.SkillMD,
			})
		}
	}

	if s.embedder == nil {
		return rec, nil
	}
	rec.Provider = "openai-compatible"
	ectx, cancel := context.WithTimeout(ctx, recommendTimeout)
	defer cancel()
	vecs, err := s.embedder.Embed(ectx, []string{embed.Truncate(text, embed.ChunkRunes)})
	if err != nil {
		rec.Warnings = append(rec.Warnings, "embedding provider unavailable; matches omitted")
		return rec, nil
	}
	matches, err := s.st.RecommendSkills(ctx, vecs[0], limit+len(pinnedNames), s.skillFloor)
	if err != nil {
		return nil, err
	}
	for _, m := range matches {
		if pinnedNames[m.Name] || len(rec.Matches) >= limit {
			continue
		}
		rec.Matches = append(rec.Matches, skillMatchJSON{
			Name: m.Name, Description: m.Description, Hash: m.ContentHash, Score: m.Score,
		})
	}
	return rec, nil
}

func (s *server) syncSkills(w http.ResponseWriter, r *http.Request) {
	if s.skillSyncer == nil {
		writeErr(w, http.StatusUnprocessableEntity, "no skill sources configured (LODE_SKILL_SOURCES)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	s.skillSyncMu.Lock()
	sum, err := s.skillSyncer.SyncAll(ctx, s.skillSources)
	s.skillSyncMu.Unlock()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "skill sync failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}
```

Adapt `readJSON`'s call shape, the task getter name, and `writeErr` signatures to the real helpers in `server.go`/`tasks.go` — keep behavior identical. (`task.Skills` arrives in Task 8; until then leave `pins` nil in `recommendSkills` with a `// Task pins wired in the task-pins commit` note only if Task 8 hasn't landed — if executing in order, Task 8 comes later, so implement `recommendSkills` first with `pins = nil` and a follow-up edit lands in Task 8.)

- [ ] **Step 5: Run tests**

Run: `go test ./internal/api/ -run TestSkills -v` then `go test ./internal/api/ ./internal/cmd/ -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/skills.go internal/api/skills_test.go internal/api/server.go internal/cmd/serve.go
git commit -m "feat(api): skills endpoints (list/get/archive/recommend/sync) + config"
```

---

### Task 8: Task pins end-to-end

**Files:**
- Modify: `internal/store/tasks.go` (Task.Skills, TaskInput.Skills, scan + insert), `internal/store/tasks_test.go`, `internal/api/tasks.go` (create request + `toTaskJSON` + new `setTaskSkills` handler), `internal/api/server.go` (route), `internal/api/skills.go` (wire `pins` from task in `recommendSkills`), `internal/cli/client.go` (Task struct + `SetTaskSkills`), `internal/cmd/task.go` (`--skill` flag on add, new `newTaskSkillsCmd`)

- [ ] **Step 1: Write failing store test**

In `internal/store/tasks_test.go`, add:

```go
func TestTaskSkills(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	id := createTestTask(t, s) // reuse this file's existing task-seeding helper

	task, err := s.GetTask(ctx, id)
	if err != nil || len(task.Skills) != 0 {
		t.Fatalf("default skills: %+v err=%v", task.Skills, err)
	}
	if err := s.SetTaskSkills(ctx, id, []string{"tdd", "debugging"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	task, _ = s.GetTask(ctx, id)
	if len(task.Skills) != 2 || task.Skills[0] != "tdd" {
		t.Fatalf("skills: %+v", task.Skills)
	}
}
```

(Use the file's actual seeding helper and `GetTask` name.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/ -run TestTaskSkills -v`
Expected: FAIL.

- [ ] **Step 3: Implement store changes**

In `internal/store/tasks.go`:
- Add `Skills []string` to `Task` and to `TaskInput`.
- In every SELECT that scans a full task row, select `coalesce(skills::text, '[]')` into a `string` and `json.Unmarshal` it into `Skills` (nil-safe: unmarshal into `&t.Skills`, leave `[]`→empty slice). Follow the file's existing scan-helper structure — add the column at the end of the column list and scan helper.
- In `CreateTask` (the `tx`-taking one), marshal `in.Skills` (nil → `[]`) and insert `skills = $n::jsonb`.
- Add:

```go
// SetTaskSkills replaces the task's pinned skill names.
func (s *Store) SetTaskSkills(ctx context.Context, id string, skills []string) error {
	if skills == nil {
		skills = []string{}
	}
	b, err := json.Marshal(skills)
	if err != nil {
		return fmt.Errorf("set task skills %s: %w", id, err)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE tasks SET skills = $2::jsonb WHERE id = $1`, id, string(b))
	if err != nil {
		return fmt.Errorf("set task skills %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("set task skills %s: %w", id, ErrNotFound)
	}
	return nil
}
```

(Adapt the id column/type to the real tasks schema — mirror a neighboring single-column task update.)

- [ ] **Step 4: API + CLI surface**

- `internal/api/tasks.go`: accept `"skills": []string` in the create-task request (pass into `TaskInput`); add `Skills []string \`json:"skills"\`` to `taskJSON` + populate in `toTaskJSON` (empty slice, never null — match `open_blockers` discipline).
- New handler in `internal/api/tasks.go`, route `PUT /api/v1/tasks/{id}/skills` registered under `s.auth`:

```go
func (s *server) setTaskSkills(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Skills []string `json:"skills"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.st.SetTaskSkills(r.Context(), r.PathValue("id"), req.Skills); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": req.Skills})
}
```

Wrap it in `RecordEvent` with type `task.skills_set` if the file's other mutating handlers all do — mirror `createTask`'s shape exactly.
- `internal/api/skills.go`: in `recommendSkills`, set `pins = task.Skills` (replacing the Task-7 nil).
- `internal/cli/client.go`: add `Skills []string \`json:"skills"\`` to the `Task` mirror struct; add:

```go
func (c *Client) SetTaskSkills(ctx context.Context, id string, skills []string) ([]byte, error) {
	return c.do(ctx, http.MethodPut, "/api/v1/tasks/"+url.PathEscape(id)+"/skills",
		map[string]any{"skills": skills})
}
```

- `internal/cmd/task.go`: on `newTaskAddCmd`, add `--skill` (`StringArrayVar`, repeatable) feeding the create request. Add `newTaskSkillsCmd` to the `task` group:

```go
func newTaskSkillsCmd() *cobra.Command {
	var set []string
	cmd := &cobra.Command{
		Use:   "skills <id>",
		Short: "Show or replace the task's pinned skills",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("set") {
				raw, err := c.SetTaskSkills(cmd.Context(), args[0], set)
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
				}
				return nil
			}
			task, raw, err := c.Task(cmd.Context(), args[0]) // the existing single-task getter
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.Join(task.Skills, "\n"))
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&set, "set", nil, "replace pinned skills (comma-separated)")
	return cmd
}
```

(Adapt getter names to `client.go`'s actual single-task method.) Add an api-level test in `internal/api/tasks_test.go` for `PUT /api/v1/tasks/{id}/skills` (200 + echoed list, 404 unknown id).

- [ ] **Step 5: Run tests**

Run: `go test ./internal/store/ ./internal/api/ ./internal/cli/ ./internal/cmd/ -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/tasks.go internal/store/tasks_test.go internal/api/tasks.go internal/api/tasks_test.go internal/api/server.go internal/api/skills.go internal/cli/client.go internal/cmd/task.go
git commit -m "feat: task skill pins (store + API + CLI)"
```

---

### Task 9: Webhook + boot sync triggers

**Files:**
- Modify: `internal/hooks/github.go` (skill-push gate), `internal/hooks/github_test.go`, `internal/api/server.go` (trigger wiring + boot sync)

- [ ] **Step 1: Write the failing test**

In `internal/hooks/github_test.go`, add a test that constructs the handler with a skill-push callback and posts a signed `push` event (reuse the file's `sign()` helper and payload fixtures) for a repo/branch that (a) matches → callback called once, response `{"status":"ok"}`, event type recorded as `push.skills`; (b) doesn't match → callback not called, existing behavior unchanged.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/hooks/ -run TestGitHubSkillPush -v`
Expected: FAIL — constructor doesn't accept the callback.

- [ ] **Step 3: Implement**

- Change `NewGitHubHandler(st, secret, log)` to `NewGitHubHandler(st, secret, log, onSkillPush func(repo, branch string) bool)` (update the two call sites: `internal/api/server.go` and tests). Add `Ref string \`json:"ref"\`` to the handler's `envelope` struct if absent.
- In `ServeHTTP`, for `event == "push"`, before the `ProjectForRepo` lookup:

```go
	if event == "push" && h.onSkillPush != nil {
		if branch, ok := strings.CutPrefix(env.Ref, "refs/heads/"); ok && h.onSkillPush(env.Repository.FullName, branch) {
			typ = "push.skills"
			// Record the event for provenance; sync runs async in the callback.
			// apply stays nil.
		}
	}
```

Follow the function's existing flow so `RecordEvent` still runs with the delivery id (duplicate deliveries stay idempotent) and the response stays `{"status":"ok"}`.
- In `internal/api/server.go`, pass the trigger when constructing the handler:

```go
	skillPush := func(repo, branch string) bool {
		if !skillsync.MatchesPush(s.skillSources, repo, branch) {
			return false
		}
		go s.runSkillSync(context.Background(), "webhook push")
		return true
	}
```

and add:

```go
// runSkillSync serializes full syncs; overlapping triggers coalesce by
// skipping when one is already running.
func (s *server) runSkillSync(ctx context.Context, reason string) {
	if s.skillSyncer == nil || !s.skillSyncMu.TryLock() {
		return
	}
	defer s.skillSyncMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	sum, err := s.skillSyncer.SyncAll(ctx, s.skillSources)
	if err != nil {
		s.log.Warn("skill sync failed", "reason", reason, "err", err)
		return
	}
	s.log.Info("skill sync", "reason", reason, "synced", sum.Synced, "deleted", sum.Deleted)
}
```

(Change `syncSkills` from Task 7 to reuse the mutex the same way it already does.) At the end of `NewServer`, when `s.skillSyncer != nil`: `go s.runSkillSync(context.Background(), "boot")`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/hooks/ ./internal/api/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/hooks/github.go internal/hooks/github_test.go internal/api/server.go
git commit -m "feat: skill sync triggers — webhook push + boot"
```

---

### Task 10: `internal/skillstore` — local content-addressed store

**Files:**
- Create: `internal/skillstore/skillstore.go`, `internal/skillstore/skillstore_test.go`

- [ ] **Step 1: Write the failing test**

```go
package skillstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func gzTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for p, c := range files {
		tw.WriteHeader(&tar.Header{Name: p, Mode: 0o644, Size: int64(len(c)), Typeflag: tar.TypeReg})
		tw.Write([]byte(c))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestEnsure(t *testing.T) {
	root := t.TempDir()
	arch := gzTar(t, map[string]string{"SKILL.md": "body", "references/notes.md": "n"})
	fetches := 0
	fetch := func() ([]byte, error) { fetches++; return arch, nil }

	p, err := Ensure(root, "tdd", "aabb01", fetch)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(p, "SKILL.md")); string(got) != "body" {
		t.Fatalf("content: %q", got)
	}
	// Second ensure with the same hash: cache hit, no fetch.
	if _, err := Ensure(root, "tdd", "aabb01", fetch); err != nil || fetches != 1 {
		t.Fatalf("cache: fetches=%d err=%v", fetches, err)
	}
	// New hash: fetch again, symlink follows.
	arch2 := gzTar(t, map[string]string{"SKILL.md": "v2"})
	if _, err := Ensure(root, "tdd", "ccdd02", func() ([]byte, error) { return arch2, nil }); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "tdd", "SKILL.md")); string(got) != "v2" {
		t.Fatalf("after upgrade: %q", got)
	}
	// Old version still present in the store for concurrent worktrees.
	if _, err := os.Stat(filepath.Join(root, ".store", "aabb01", "SKILL.md")); err != nil {
		t.Fatalf("old version gone: %v", err)
	}

	// Hostile entries rejected.
	for _, bad := range []map[string]string{
		{"../escape.md": "x"},
		{"/abs.md": "x"},
	} {
		b := bad
		if _, err := Ensure(root, "evil", "eeff03", func() ([]byte, error) { return gzTar(t, b), nil }); err == nil {
			t.Fatalf("want traversal error for %v", b)
		}
	}
	// Bad identifiers rejected.
	if _, err := Ensure(root, "a/b", "aabb01", fetch); err == nil {
		t.Fatal("want name error")
	}
	if _, err := Ensure(root, "ok", "not hex!", fetch); err == nil {
		t.Fatal("want hash error")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/skillstore/ -v`
Expected: FAIL — package missing.

- [ ] **Step 3: Implement `internal/skillstore/skillstore.go`**

```go
// Package skillstore manages the local content-addressed skill cache:
// <root>/.store/<hash>/ holds unpacked skill dirs; <root>/<name> is a
// symlink to the current version. Concurrent worktrees can hold different
// versions because store dirs are immutable once extracted.
package skillstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxExtracted caps the unpacked size of one skill version.
const maxExtracted = 8 << 20

// Root returns the local skill dir: $LODE_SKILLS_DIR or ~/.worklode/skills.
func Root() (string, error) {
	if v := os.Getenv("LODE_SKILLS_DIR"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("skill store root: %w", err)
	}
	return filepath.Join(home, ".worklode", "skills"), nil
}

// Path returns the stable per-name path (the symlink), whether or not it
// exists yet. Callers print this in briefs.
func Path(root, name string) string { return filepath.Join(root, name) }

func validName(name string) bool {
	return name != "" && !strings.ContainsAny(name, `/\`) && name != "." && name != ".." && !strings.HasPrefix(name, ".")
}

func validHash(hash string) bool {
	if len(hash) < 6 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

// Ensure makes <root>/<name> point at the unpacked version identified by
// hash, calling fetch for the tar.gz only when that version is not already
// in the store. Returns the symlink path.
func Ensure(root, name, hash string, fetch func() ([]byte, error)) (string, error) {
	if !validName(name) {
		return "", fmt.Errorf("skill name %q: invalid", name)
	}
	if !validHash(hash) {
		return "", fmt.Errorf("skill hash %q: invalid", hash)
	}
	dst := filepath.Join(root, ".store", hash)
	if _, err := os.Stat(dst); err != nil {
		data, err := fetch()
		if err != nil {
			return "", fmt.Errorf("fetch skill %s@%s: %w", name, hash, err)
		}
		if err := extract(data, dst); err != nil {
			return "", fmt.Errorf("extract skill %s@%s: %w", name, hash, err)
		}
	}
	link := Path(root, name)
	if err := swapSymlink(dst, link); err != nil {
		return "", fmt.Errorf("link skill %s: %w", name, err)
	}
	return link, nil
}

func extract(tgz []byte, dst string) error {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return err
	}
	tmp := dst + ".tmp-" + randSuffix()
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		rel := filepath.Clean(h.Name)
		if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") || strings.Contains(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive entry %q: escapes destination", h.Name)
		}
		total += h.Size
		if total > maxExtracted {
			return fmt.Errorf("archive exceeds %d bytes", int64(maxExtracted))
		}
		p := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, io.LimitReader(tr, maxExtracted)); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		// A concurrent Ensure won the race; its content is identical.
		if _, statErr := os.Stat(dst); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func swapSymlink(target, link string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	if cur, err := os.Readlink(link); err == nil && cur == target {
		return nil
	}
	tmp := link + ".tmp-" + randSuffix()
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

func randSuffix() string {
	var b [4]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/skillstore/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/skillstore/
git commit -m "feat(skillstore): content-addressed local skill cache"
```

---

### Task 11: CLI — client methods + `lode skills` group

**Files:**
- Create: `internal/cmd/skills.go`
- Modify: `internal/cli/client.go` (Skill types + 5 methods), `internal/cli/client_test.go`, `internal/cmd/` command test file matching the existing pattern

- [ ] **Step 1: Client methods (test-first, in `internal/cli/client_test.go`'s existing httptest style)**

Add to `internal/cli/client.go` (mirroring `internal/api`'s wire types):

```go
// Skill mirrors internal/api's skillJSON.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SourceRepo  string `json:"source_repo"`
	Hash        string `json:"hash"`
	Deleted     bool   `json:"deleted"`
}

// SkillMatch and PinnedSkill mirror skillMatchJSON / pinnedSkillJSON.
type SkillMatch struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Hash        string  `json:"hash"`
	Score       float64 `json:"score"`
}

type PinnedSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hash        string `json:"hash"`
	Content     string `json:"content"`
}

// SkillRecommendation mirrors recommendationJSON.
type SkillRecommendation struct {
	Pinned   []PinnedSkill `json:"pinned"`
	Matches  []SkillMatch  `json:"matches"`
	Warnings []string      `json:"warnings"`
	Provider string        `json:"provider"`
}

func (c *Client) Skills(ctx context.Context) ([]Skill, []byte, error)
func (c *Client) Skill(ctx context.Context, name string) (Skill, []byte, error)
func (c *Client) SkillArchive(ctx context.Context, name, hash string) ([]byte, error)
func (c *Client) RecommendSkills(ctx context.Context, taskID, text string, limit int) (SkillRecommendation, []byte, error)
func (c *Client) SyncSkills(ctx context.Context) ([]byte, error)
```

Each is a thin `c.do(...)` wrapper exactly like `Brief`; `Skills` unwraps the `{"skills": [...]}` envelope. Tests: one httptest round-trip per method asserting path, method, and decode (copy the structure of the existing `TestBrief`-style client tests).

- [ ] **Step 2: `internal/cmd/skills.go`**

```go
package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/skillstore"
)

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Org-wide agent skills: list, recommend, install, sync",
	}
	cmd.AddCommand(newSkillsListCmd(), newSkillsRecommendCmd(), newSkillsInstallCmd(), newSkillsSyncCmd())
	return cmd
}

func init() { rootCmd.AddCommand(newSkillsCmd()) }

func newSkillsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List org skills known to the server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			skills, raw, err := c.Skills(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			for _, sk := range skills {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", sk.Name, sk.Description)
			}
			return nil
		},
	}
}

func newSkillsRecommendCmd() *cobra.Command {
	var taskID, text, file string
	var limit int
	cmd := &cobra.Command{
		Use:   "recommend",
		Short: "Recommend skills for a task or free text",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			set := 0
			for _, v := range []string{taskID, text, file} {
				if v != "" {
					set++
				}
			}
			if set != 1 {
				return fmt.Errorf("exactly one of --task, --text, --file is required")
			}
			if file != "" {
				b, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				text = string(b)
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			rec, raw, err := c.RecommendSkills(cmd.Context(), taskID, text, limit)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			out := cmd.OutOrStdout()
			for _, p := range rec.Pinned {
				fmt.Fprintf(out, "pinned\t%s\t%s\n", p.Name, p.Description)
			}
			for _, m := range rec.Matches {
				fmt.Fprintf(out, "%.2f\t%s\t%s\n", m.Score, m.Name, m.Description)
			}
			for _, w := range rec.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "recommend for this task id")
	cmd.Flags().StringVar(&text, "text", "", "recommend for this free text")
	cmd.Flags().StringVar(&file, "file", "", "recommend for this file's contents")
	cmd.Flags().IntVar(&limit, "limit", 5, "max matches")
	return cmd
}

func newSkillsInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <name>[@<hash>]",
		Short: "Install a skill into the local store (~/.worklode/skills)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, hash, _ := strings.Cut(args[0], "@")
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			if hash == "" {
				sk, _, err := c.Skill(cmd.Context(), name)
				if err != nil {
					return err
				}
				hash = sk.Hash
			}
			root, err := skillstore.Root()
			if err != nil {
				return err
			}
			p, err := skillstore.Ensure(root, name, hash, func() ([]byte, error) {
				return c.SkillArchive(cmd.Context(), name, hash)
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), p)
			return nil
		},
	}
}

func newSkillsSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Trigger a full server-side skill sync (admin)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			raw, err := c.SyncSkills(cmd.Context())
			if err != nil {
				return err
			}
			printRaw(cmd, raw)
			return nil
		},
	}
}
```

(Add the missing `os` import; match `newAPIClient` / helper names to `root.go`.)

- [ ] **Step 3: Run tests**

Run: `go test ./internal/cli/ ./internal/cmd/ -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/cli/client.go internal/cli/client_test.go internal/cmd/skills.go
git commit -m "feat(cli): lode skills group + client methods"
```

---

### Task 12: Brief integration

**Files:**
- Modify: `internal/store/brief.go` + `internal/store/brief_test.go` (pins), `internal/api/brief.go` + `internal/api/brief_test.go` (skills section + recommendations), `internal/cli/client.go` (Brief mirror), `internal/cmd/task.go` (`printBrief`)

- [ ] **Step 1: Store — resolve pins into the brief (test-first)**

`internal/store/brief_test.go`: add a case — task with `Skills: ["tdd", "ghost"]` where `tdd` exists (seeded via `UpsertSkill`) → `Brief.PinnedSkills` has one entry with `SkillMD` populated; `Brief.SkillWarnings == ["pinned skill not found: ghost"]`. Then in `internal/store/brief.go`:

- Add to `Brief`:

```go
	// PinnedSkills are the task's pinned skills, content included; deleted
	// pins still resolve (with a warning) so briefs never break.
	PinnedSkills []Skill
	// SkillWarnings surface unknown/deleted pins.
	SkillWarnings []string
```

- In `(s *Store) Brief`, after the task load: if `len(task.Skills) > 0`, call `s.SkillsByNames` and populate both fields (warnings: `"pinned skill not found: <name>"`, `"pinned skill removed from its source repo: <name>"`). This keeps the bounded-queries contract: exactly one extra query, only when pins exist. Update the doc comment's "no file contents" sentence to: pinned SKILL.md bodies are the one deliberate exception, budget-bounded by the pin list the task author wrote.

- [ ] **Step 2: API — skills section on the brief (test-first)**

`internal/api/brief_test.go`: brief for a task with a pin asserts `skills.pinned[0].content != ""`, `skills.provider == "none"`, `skills.recommended == []`. In `internal/api/brief.go`:

- Extend `briefJSON` with `Skills recommendationJSON \`json:"skills"\``.
- In `toBriefJSON` populate `Pinned`/`Warnings` from the store Brief (reuse `pinnedSkillJSON`).
- In the `taskBrief` handler, after assembling: call `s.recommendation(...)`-style matching — reuse the Task-7 helper with `text = task.Title + "\n\n" + task.Body`, pins already resolved (pass the resolved pins to avoid a second lookup: refactor `recommendation` to accept pre-resolved pinned skills — keep one code path; the store Brief already did pin resolution, so `recommendation` gains a variant `matchesFor(ctx, text, excludeNames, limit)` used by both). Keep the 2s degrade-to-pins-only behavior.

- [ ] **Step 3: CLI mirror + rendering**

- `internal/cli/client.go`: add `Skills SkillRecommendation \`json:"skills"\`` to the `Brief` struct.
- `internal/cmd/task.go` `printBrief`: after the existing sections, render:

```go
	if len(b.Skills.Pinned) > 0 || len(b.Skills.Matches) > 0 {
		fmt.Fprintln(out, "\nSkills:")
		for _, p := range b.Skills.Pinned {
			fmt.Fprintf(out, "  pinned  %s — %s (content in brief)\n", p.Name, p.Description)
		}
		for _, m := range b.Skills.Matches {
			fmt.Fprintf(out, "  %.2f    %s — %s\n", m.Score, m.Name, m.Description)
		}
		for _, w := range b.Skills.Warnings {
			fmt.Fprintf(out, "  warning: %s\n", w)
		}
	}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/ ./internal/api/ ./internal/cli/ ./internal/cmd/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/brief.go internal/store/brief_test.go internal/api/brief.go internal/api/brief_test.go internal/cli/client.go internal/cmd/task.go
git commit -m "feat(brief): pinned skill content + embedding recommendations"
```

---

### Task 13: Hook — lazy local fetch + compactBrief

**Files:**
- Modify: `internal/hookrun/hookrun.go`, `internal/hookrun/hookrun_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/hookrun/hookrun_test.go` (existing fixture style: fake backbone `httptest.Server`, `Options.NewClient` injection): make the fake brief response include a pinned skill (with content + hash) and one recommended match; set `LODE_SKILLS_DIR` to a `t.TempDir()` via `t.Setenv`; the fake server also serves `GET /api/v1/skills/{name}/archive/{hash}` with a valid tar.gz. Assert after `session-start`:
1. The emitted `additionalContext` contains the pinned skill's SKILL.md content and, for the recommended skill, the instruction line with its local path.
2. Both skills' dirs exist under the temp skills root.
3. An archive-fetch failure (fake returns 500 for one skill) still exits 0 and still emits the brief (warn-only discipline).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/hookrun/ -run TestSessionStartSkills -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `internal/hookrun/hookrun.go`:

- In `handleSessionStart`, after the brief fetch and before `emitAdditionalContext`, add:

```go
	skillPaths := ensureSkills(ctx, opts, c, brief)
```

- New function (warn-only, per the package's never-fail invariant):

```go
// ensureSkills lazily fetches brief-referenced skill archives into the local
// content-addressed store. Failures are warnings: the pinned content is
// already inline in the brief, and recommended skills degrade to an install
// hint. Returns name -> local path for the ones that are present.
func ensureSkills(ctx context.Context, opts Options, c *cli.Client, b cli.Brief) map[string]string {
	root, err := skillstore.Root()
	if err != nil {
		warnf(opts.Stderr, "skill store: %v", err) // match the file's existing warn helper
		return nil
	}
	paths := map[string]string{}
	ensure := func(name, hash string) {
		if hash == "" {
			return
		}
		bctx, cancel := context.WithTimeout(ctx, backboneTimeout)
		defer cancel()
		p, err := skillstore.Ensure(root, name, hash, func() ([]byte, error) {
			return c.SkillArchive(bctx, name, hash)
		})
		if err != nil {
			warnf(opts.Stderr, "skill %s: %v (run: lode skills install %s)", name, err, name)
			return
		}
		paths[name] = p
	}
	for _, p := range b.Skills.Pinned {
		ensure(p.Name, p.Hash)
	}
	for _, m := range b.Skills.Matches {
		ensure(m.Name, m.Hash)
	}
	return paths
}
```

- Extend `compactBrief` (give it the `skillPaths` map — adjust its signature and the two other call sites, passing `nil` there unless those events should also fetch):

```go
	if len(b.Skills.Pinned)+len(b.Skills.Matches) > 0 {
		fmt.Fprintf(&sb, "\n## Skills\n")
		for _, p := range b.Skills.Pinned {
			fmt.Fprintf(&sb, "\n### Pinned: %s\n%s\n", p.Name, p.Content)
			if path := skillPaths[p.Name]; path != "" {
				fmt.Fprintf(&sb, "(supporting files: %s)\n", path)
			}
		}
		if len(b.Skills.Matches) > 0 {
			fmt.Fprintf(&sb, "\n### Possibly relevant org skills\nRead the SKILL.md if relevant to this task:\n")
			for _, m := range b.Skills.Matches {
				loc := "lode skills install " + m.Name
				if path := skillPaths[m.Name]; path != "" {
					loc = path + "/SKILL.md"
				}
				fmt.Fprintf(&sb, "- %s (%.2f): %s — %s\n", m.Name, m.Score, m.Description, loc)
			}
		}
	}
```

(Match `compactBrief`'s actual string-building style; `warnf` = whatever warn helper the file uses.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/hookrun/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/hookrun/hookrun.go internal/hookrun/hookrun_test.go
git commit -m "feat(hookrun): lazy skill fetch + skills section in session context"
```

---

### Task 14: Deploy manifests + docs + full-suite gate

**Files:**
- Modify: `deploy/base/postgres.yaml`, `deploy/base/configmap.yaml`, `deploy/overlays/hzdev/kustomization.yaml`, `deploy/overlays/hzprod/kustomization.yaml`, `deploy/overlays/hzdev/externalsecret-worklode-secrets.yaml`, `deploy/overlays/hzprod/externalsecret-worklode-secrets.yaml`, `docker-compose.yml` (env passthrough), `README.md`

- [ ] **Step 1: CNPG pgvector image**

In `deploy/base/postgres.yaml`, change `imageName` to a pgvector-capable CNPG image: `ghcr.io/cloudnative-pg/postgresql:17.5-standard-bookworm` (the `standard` flavor bundles pgvector; the migration's `CREATE EXTENSION IF NOT EXISTS vector` works because pgvector is a trusted extension — no superuser needed). Verify the exact current 17.x standard tag before committing (`docs` or ghcr listing); pin the full version, not a floating major.

- [ ] **Step 2: Config + secrets**

- `deploy/base/configmap.yaml`: add `LODE_SKILL_SOURCES` (e.g. `sunstoneinstitute/claude-plugins@main:plugins/*/skills/*`), `LODE_EMBEDDING_URL`, `LODE_EMBEDDING_MODEL` — empty-string placeholders in base if per-env values differ, with real values patched in `deploy/overlays/{hzdev,hzprod}/kustomization.yaml` (existing JSON-patch `op: add` style).
- Both `externalsecret-worklode-secrets.yaml` overlays: add `LODE_EMBEDDING_API_KEY` (`secretKey` + `remoteRef {key: worklode-secrets, property: LODE_EMBEDDING_API_KEY}`).
- `docker-compose.yml`: add the five new env vars to the worklode service with the `${VAR:-}` passthrough idiom.

- [ ] **Step 3: Validate kustomize builds**

Run: `kubectl kustomize deploy/overlays/hzdev >/dev/null && kubectl kustomize deploy/overlays/hzprod >/dev/null`
Expected: both succeed.

- [ ] **Step 4: README**

Add a short `## Org skills` section to `README.md`: what `lode skills list|recommend|install|sync` do, the `LODE_SKILL_SOURCES` format, pins via `lode task add --skill` / `lode task skills`, and the pins-only degradation when `LODE_EMBEDDING_URL` is unset. Keep it to ~20 lines, matching the README's existing terseness.

- [ ] **Step 5: Full-suite gate (CI parity)**

Run: `gofmt -l . && go vet ./... && go test -race -count=1 ./... && go test -race -count=1 -tags e2e ./e2e/`
Expected: all pass, gofmt output empty.

- [ ] **Step 6: Commit**

```bash
git add deploy/ docker-compose.yml README.md
git commit -m "feat(deploy): pgvector CNPG image + skills config wiring; document lode skills"
```

---

## Plan self-review notes (already applied)

- **Spec coverage:** registry+sync (T1,2,5,6,9,14) · embeddings+recommend (T3,4,7) · pins (T8) · distribution (T10,11) · brief+activation (T12,13) · degradation paths are asserted in T6/T7/T13 tests · acceptance criteria 1–6 map to T6/T9 (1), T7 (2), T12/T13 (3), T7 (4), T10 (5), by-construction + T13 (6).
- **Known adaptation points** (flagged inline, intentional): exact helper names in `server.go`/`app_test.go`/`hookrun.go` must be matched to the real code at implementation time; the tests define behavior, not the snippets' identifier spellings.
- **Not covered on purpose:** graph projection, doc-frontmatter pins, `doc_iri`, reconcile integration (all deferred per spec); e2e coverage of sync (would need a fake GitHub — unit tests in T6 cover the engine; revisit when spec 07's reconcile lands).
