package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Skill is one org-wide agent skill at its latest synced version.
//
// Skill is deliberately not model.Skill: ID, SourcePath, and SkillMD's
// content-hashed home are database bookkeeping this package needs internally
// (sync, archive lookup) that never cross the wire, so they stay outside the
// five fields model.Skill declares (ADR 036 §3, "store scan plumbing").
// api.toSkillJSON and api.toPinnedSkillJSON are the conversion points from
// this type to model.Skill and model.PinnedSkill.
type Skill struct {
	ID          int64
	Qualifier   string // plugin that ships it; the <plugin> of <plugin>:<name>
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
	Qualifier   string
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
// It returns the skill id, and changed=true when the content hash differs from
// the stored latest version (including brand-new skills) so the caller can
// re-embed that exact row without looking it up again by name.
func (s *Store) UpsertSkill(ctx context.Context, u SkillUpsert) (int64, bool, error) {
	if u.ContentHash == "" {
		return 0, false, fmt.Errorf("skill %s: content hash required: %w", u.Name, ErrInvalidInput)
	}
	// The qualifier is half the skill's identity (037 §4.2); without it two
	// plugins' same-named skills are one row again.
	if u.Qualifier == "" {
		return 0, false, fmt.Errorf("skill %s: qualifier required: %w", u.Name, ErrInvalidInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("upsert skill %s: %w", u.Name, err)
	}
	defer tx.Rollback()

	var id int64
	var repo, latestHash string
	err = tx.QueryRowContext(ctx, `
		SELECT s.id, s.source_repo, coalesce(v.content_hash, '')
		FROM skills s
		LEFT JOIN skill_versions v ON v.id = s.latest_version_id
		WHERE s.qualifier = $1 AND s.name = $2`, u.Qualifier, u.Name).Scan(&id, &repo, &latestHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO skills (qualifier, name, description, source_repo, source_path)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			u.Qualifier, u.Name, u.Description, u.SourceRepo, u.SourcePath).Scan(&id); err != nil {
			return 0, false, fmt.Errorf("insert skill %s: %w", u.Name, err)
		}
	case err != nil:
		return 0, false, fmt.Errorf("upsert skill %s: %w", u.Name, err)
	case repo != u.SourceRepo:
		return 0, false, fmt.Errorf("skill %s already sourced from %s: %w", u.Name, repo, ErrInvalidInput)
	}

	if latestHash == u.ContentHash {
		if _, err := tx.ExecContext(ctx, `
			UPDATE skills SET deleted_at = NULL, description = $2, source_path = $3
			WHERE id = $1`, id, u.Description, u.SourcePath); err != nil {
			return 0, false, fmt.Errorf("refresh skill %s: %w", u.Name, err)
		}
		return id, false, tx.Commit()
	}

	// ON CONFLICT fires when content reverts to an earlier hash, or on a
	// concurrent same-hash sync.
	var versionID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO skill_versions (skill_id, git_commit, content_hash, frontmatter, skill_md, archive, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT ON CONSTRAINT skill_versions_hash_unique
		DO UPDATE SET git_commit = excluded.git_commit
		RETURNING id`,
		id, u.GitCommit, u.ContentHash, u.Frontmatter, u.SkillMD, u.Archive, s.Now()).Scan(&versionID)
	if err != nil {
		return 0, false, fmt.Errorf("insert skill version %s@%s: %w", u.Name, u.ContentHash, err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE skills SET latest_version_id = $2, description = $3, source_path = $4, deleted_at = NULL
		WHERE id = $1`, id, versionID, u.Description, u.SourcePath); err != nil {
		return 0, false, fmt.Errorf("point skill %s at version: %w", u.Name, err)
	}
	return id, true, tx.Commit()
}

// SoftDeleteSkillsExcept marks every live skill from sourceRepo whose
// qualified name is not in keep as deleted, returning how many were marked.
// keep holds qualified names (<plugin>:<name>), which is what a sync collects.
// A nil or empty keep marks every live skill from that repo as deleted.
func (s *Store) SoftDeleteSkillsExcept(ctx context.Context, sourceRepo string, keep []string) (int64, error) {
	if keep == nil {
		keep = []string{}
	}
	keepJSON, err := json.Marshal(keep)
	if err != nil {
		return 0, fmt.Errorf("soft delete skills: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE skills SET deleted_at = $3
		WHERE source_repo = $1 AND deleted_at IS NULL
		  AND qualifier || ':' || name NOT IN (SELECT jsonb_array_elements_text($2::jsonb))`,
		sourceRepo, string(keepJSON), s.Now())
	if err != nil {
		return 0, fmt.Errorf("soft delete skills from %s: %w", sourceRepo, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("soft delete skills from %s rows affected: %w", sourceRepo, err)
	}
	return n, nil
}

// skillSelect ends after the LEFT JOIN; callers append JOIN/WHERE/ORDER BY.
const skillSelect = `
	SELECT s.id, s.qualifier, s.name, s.description, s.source_repo, s.source_path,
	       coalesce(v.content_hash, ''), coalesce(v.skill_md, ''),
	       s.deleted_at IS NOT NULL
	FROM skills s
	LEFT JOIN skill_versions v ON v.id = s.latest_version_id`

func scanSkill(row rowScanner) (*Skill, error) {
	var sk Skill
	err := row.Scan(&sk.ID, &sk.Qualifier, &sk.Name, &sk.Description, &sk.SourceRepo, &sk.SourcePath,
		&sk.ContentHash, &sk.SkillMD, &sk.Deleted)
	if err != nil {
		return nil, err
	}
	return &sk, nil
}

// GetSkill returns one skill (deleted or not) by reference: either the
// qualified name (<plugin>:<name>) or a bare name that matches exactly one
// skill. A bare name matching more than one reports ErrAmbiguousSkill naming
// the candidates rather than picking one (037 §4.2).
func (s *Store) GetSkill(ctx context.Context, ref string) (*Skill, error) {
	res, err := s.ResolveSkillRefs(ctx, []string{ref})
	if err != nil {
		return nil, err
	}
	r := res[0]
	switch {
	case r.Skill != nil:
		return r.Skill, nil
	case len(r.Candidates) > 0:
		return nil, r.ambiguousErr()
	default:
		return nil, fmt.Errorf("skill %s: %w", ref, ErrNotFound)
	}
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
	return collectRows(rows, "list skills", byValue(scanSkill))
}

// SkillsMissingEmbeddings returns live skills with no stored vector at all,
// ordered by name — the set a sync must embed to converge, whether they were
// never embedded, lost their vectors to a provider change, or failed a
// transient embed call. Description and SkillMD come along so the caller can
// embed without a second query. A skill with chunk rows but no vector counts
// as missing: 040 §8 invalidates by nulling the column, leaving the text in
// place for the lexical arm.
func (s *Store) SkillsMissingEmbeddings(ctx context.Context) ([]Skill, error) {
	rows, err := s.db.QueryContext(ctx, skillSelect+`
		WHERE s.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM index_chunks e
		                   WHERE e.skill_id = s.id AND e.embedding IS NOT NULL)
		ORDER BY s.name`)
	if err != nil {
		return nil, fmt.Errorf("skills missing embeddings: %w", err)
	}
	return collectRows(rows, "skills missing embeddings", byValue(scanSkill))
}

// SkillsByNames returns the named skills (deleted included, so brief pins can
// warn rather than vanish), ordered as asked and deduped to first occurrence;
// names that resolve to nothing, or ambiguously, are simply absent. Callers
// that must tell those two apart use ResolveSkillRefs.
func (s *Store) SkillsByNames(ctx context.Context, names []string) ([]Skill, error) {
	res, err := s.ResolveSkillRefs(ctx, names)
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, r := range res {
		if r.Skill != nil {
			out = append(out, *r.Skill)
		}
	}
	return out, nil
}

// dedupeFirst returns names with duplicates removed, keeping first occurrence
// order.
func dedupeFirst(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// SkillArchive returns the stored tar.gz for one exact version. name is a
// skill reference, resolved by GetSkill's rules.
func (s *Store) SkillArchive(ctx context.Context, name, hash string) ([]byte, error) {
	sk, err := s.GetSkill(ctx, name)
	if err != nil {
		return nil, err
	}
	var archive []byte
	err = s.db.QueryRowContext(ctx, `
		SELECT archive FROM skill_versions
		WHERE skill_id = $1 AND content_hash = $2`, sk.ID, hash).Scan(&archive)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("skill archive %s@%s: %w", name, hash, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("skill archive %s@%s: %w", name, hash, err)
	}
	return archive, nil
}
