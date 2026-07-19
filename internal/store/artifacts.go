package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Artifact is one built, versioned unit deployed elsewhere: a Docker image,
// a PyPI package, a git tag, or a binary release.
type Artifact struct {
	ID        int64
	Kind      string
	Name      string
	Version   string
	Digest    *string
	Repo      string
	SourceSHA string
	BuiltAt   time.Time
}

// Deployment is one target's observed rollout state: a Flux Kustomization,
// a PyPI publish, or a manually tracked target.
type Deployment struct {
	ID          int64
	ArtifactID  *int64
	Environment string
	TargetKind  string
	TargetName  string
	Status      string
	FirstSeen   time.Time
	LastUpdate  time.Time
}

// CreateArtifact inserts a new artifact, or on redelivery (same kind, name,
// version) updates digest and source_sha in place. Returns the artifact's
// id either way.
func CreateArtifact(tx *sql.Tx, a Artifact) (int64, error) {
	var digest sql.NullString
	if a.Digest != nil {
		digest = sql.NullString{String: *a.Digest, Valid: true}
	}
	var builtAt sql.NullString
	if !a.BuiltAt.IsZero() {
		builtAt = sql.NullString{String: a.BuiltAt.UTC().Format(time.RFC3339), Valid: true}
	}

	_, err := tx.Exec(
		`INSERT INTO artifacts (kind, name, version, digest, repo, source_sha, built_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (kind, name, version) DO UPDATE SET
		   digest = excluded.digest,
		   repo = excluded.repo,
		   source_sha = excluded.source_sha,
		   built_at = excluded.built_at`,
		a.Kind, a.Name, a.Version, digest, a.Repo, a.SourceSHA, builtAt,
	)
	if err != nil {
		return 0, fmt.Errorf("upsert artifact %s/%s@%s: %w", a.Kind, a.Name, a.Version, err)
	}

	var id int64
	if err := tx.QueryRow(
		`SELECT id FROM artifacts WHERE kind = ? AND name = ? AND version = ?`,
		a.Kind, a.Name, a.Version,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("look up artifact %s/%s@%s: %w", a.Kind, a.Name, a.Version, err)
	}
	return id, nil
}

// artifactColumns is the SELECT list scanArtifact expects, in order.
const artifactColumns = `id, kind, name, version, digest, repo, source_sha, built_at`

func scanArtifact(row rowScanner) (*Artifact, error) {
	var a Artifact
	var digest, repo, sourceSHA, builtAt sql.NullString
	if err := row.Scan(&a.ID, &a.Kind, &a.Name, &a.Version, &digest, &repo, &sourceSHA, &builtAt); err != nil {
		return nil, err
	}
	if digest.Valid {
		a.Digest = &digest.String
	}
	a.Repo = repo.String
	a.SourceSHA = sourceSHA.String
	if builtAt.Valid {
		t, err := time.Parse(time.RFC3339, builtAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse artifact %d built_at: %w", a.ID, err)
		}
		a.BuiltAt = t
	}
	return &a, nil
}

// ArtifactsBySourceSHA returns every artifact built from sourceSHA, ordered
// by id.
func (s *Store) ArtifactsBySourceSHA(ctx context.Context, sha string) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+artifactColumns+` FROM artifacts WHERE source_sha = ? ORDER BY id`, sha)
	if err != nil {
		return nil, fmt.Errorf("artifacts by source sha %s: %w", sha, err)
	}
	defer rows.Close()

	var out []Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("artifacts by source sha %s: %w", sha, err)
	}
	return out, nil
}

// splitImage splits an image reference "registry/name:tag" into
// (name-without-tag, tag). ok is false if there is no ":" tag separator
// after the last "/" (so a registry port, e.g. "host:5000/name", is not
// mistaken for a tag).
func splitImage(image string) (name, tag string, ok bool) {
	slash := strings.LastIndex(image, "/")
	tail := image
	prefix := ""
	if slash >= 0 {
		tail = image[slash+1:]
		prefix = image[:slash+1]
	}
	colon := strings.LastIndex(tail, ":")
	if colon < 0 {
		return "", "", false
	}
	return prefix + tail[:colon], tail[colon+1:], true
}

// FindArtifactByImage looks up a docker_image artifact by an image
// reference "registry/name:tag" (matching name to name and tag to
// version). Returns ErrNotFound if image has no tag or no matching
// artifact exists.
func (s *Store) FindArtifactByImage(ctx context.Context, image string) (*Artifact, error) {
	name, tag, ok := splitImage(image)
	if !ok {
		return nil, fmt.Errorf("image %q has no tag: %w", image, ErrNotFound)
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+artifactColumns+` FROM artifacts WHERE kind = 'docker_image' AND name = ? AND version = ?`,
		name, tag)
	a, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("image %s: %w", image, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("find artifact by image %s: %w", image, err)
	}
	return a, nil
}

// findArtifactByImageTx is the tx-scoped equivalent of FindArtifactByImage,
// used by InsertRuntimeEvent to resolve artifact_id inside its own
// transaction.
func findArtifactByImageTx(tx *sql.Tx, image string) (*Artifact, error) {
	name, tag, ok := splitImage(image)
	if !ok {
		return nil, fmt.Errorf("image %q has no tag: %w", image, ErrNotFound)
	}
	row := tx.QueryRow(
		`SELECT `+artifactColumns+` FROM artifacts WHERE kind = 'docker_image' AND name = ? AND version = ?`,
		name, tag)
	a, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("image %s: %w", image, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("find artifact by image %s: %w", image, err)
	}
	return a, nil
}

// UpsertDeployment inserts a new deployment (first_seen = last_update =
// now), or on redelivery (same environment, target_kind, target_name)
// updates status and artifact_id and advances last_update, keeping the
// original first_seen. A nil ArtifactID on update keeps the previously
// linked artifact — a status-only event where the image wasn't resolved
// must not sever the link.
func UpsertDeployment(tx *sql.Tx, now time.Time, d Deployment) error {
	var artifactID sql.NullInt64
	if d.ArtifactID != nil {
		artifactID = sql.NullInt64{Int64: *d.ArtifactID, Valid: true}
	}
	nowStr := now.UTC().Format(time.RFC3339)
	_, err := tx.Exec(
		`INSERT INTO deployments (artifact_id, environment, target_kind, target_name, status, first_seen, last_update)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (environment, target_kind, target_name) DO UPDATE SET
		   status = excluded.status,
		   artifact_id = COALESCE(excluded.artifact_id, deployments.artifact_id),
		   last_update = excluded.last_update`,
		artifactID, d.Environment, d.TargetKind, d.TargetName, d.Status, nowStr, nowStr,
	)
	if err != nil {
		return fmt.Errorf("upsert deployment %s/%s/%s: %w", d.Environment, d.TargetKind, d.TargetName, err)
	}
	return nil
}

func scanDeployment(row rowScanner) (*Deployment, error) {
	var d Deployment
	var artifactID sql.NullInt64
	var firstSeen, lastUpdate string
	if err := row.Scan(&d.ID, &artifactID, &d.Environment, &d.TargetKind, &d.TargetName,
		&d.Status, &firstSeen, &lastUpdate); err != nil {
		return nil, err
	}
	if artifactID.Valid {
		d.ArtifactID = &artifactID.Int64
	}
	var err error
	if d.FirstSeen, err = time.Parse(time.RFC3339, firstSeen); err != nil {
		return nil, fmt.Errorf("parse deployment %d first_seen: %w", d.ID, err)
	}
	if d.LastUpdate, err = time.Parse(time.RFC3339, lastUpdate); err != nil {
		return nil, fmt.Errorf("parse deployment %d last_update: %w", d.ID, err)
	}
	return &d, nil
}

// deploymentColumns is the SELECT list scanDeployment expects, in order.
const deploymentColumns = `id, artifact_id, environment, target_kind, target_name, status, first_seen, last_update`

// DeploymentsForArtifact returns the deployments currently referencing
// artifactID, ordered by last_update then id.
func (s *Store) DeploymentsForArtifact(ctx context.Context, artifactID int64) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deploymentColumns+` FROM deployments WHERE artifact_id = ? ORDER BY last_update, id`,
		artifactID)
	if err != nil {
		return nil, fmt.Errorf("deployments for artifact %d: %w", artifactID, err)
	}
	defer rows.Close()

	var out []Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deployment: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("deployments for artifact %d: %w", artifactID, err)
	}
	return out, nil
}

// ListDeployments returns deployments, optionally filtered by environment
// ("" means all), ordered by environment, target_kind, target_name.
func (s *Store) ListDeployments(ctx context.Context, environment string) ([]Deployment, error) {
	q := `SELECT ` + deploymentColumns + ` FROM deployments`
	var args []any
	if environment != "" {
		q += ` WHERE environment = ?`
		args = append(args, environment)
	}
	q += ` ORDER BY environment, target_kind, target_name`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()

	var out []Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deployment: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	return out, nil
}
