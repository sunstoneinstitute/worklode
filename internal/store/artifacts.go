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
	var builtAt sql.NullTime
	if !a.BuiltAt.IsZero() {
		builtAt = sql.NullTime{Time: a.BuiltAt.UTC(), Valid: true}
	}

	// DO UPDATE (unlike DO NOTHING) always produces a row, so RETURNING
	// carries the id on both the insert and the conflict path — no follow-up
	// SELECT.
	var id int64
	if err := tx.QueryRow(
		`INSERT INTO artifacts (kind, name, version, digest, repo, source_sha, built_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (kind, name, version) DO UPDATE SET
		   digest = excluded.digest,
		   repo = excluded.repo,
		   source_sha = excluded.source_sha,
		   built_at = excluded.built_at
		 RETURNING id`,
		a.Kind, a.Name, a.Version, digest, a.Repo, a.SourceSHA, builtAt,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("upsert artifact %s/%s@%s: %w", a.Kind, a.Name, a.Version, err)
	}
	return id, nil
}

// artifactColumns is the SELECT list scanArtifact expects, in order.
const artifactColumns = `id, kind, name, version, digest, repo, source_sha, built_at`

func scanArtifact(row rowScanner) (*Artifact, error) {
	var a Artifact
	var digest, repo, sourceSHA sql.NullString
	var builtAt sql.NullTime
	if err := row.Scan(&a.ID, &a.Kind, &a.Name, &a.Version, &digest, &repo, &sourceSHA, &builtAt); err != nil {
		return nil, err
	}
	if digest.Valid {
		a.Digest = &digest.String
	}
	a.Repo = repo.String
	a.SourceSHA = sourceSHA.String
	if builtAt.Valid {
		a.BuiltAt = builtAt.Time.UTC()
	}
	return &a, nil
}

// ArtifactsBySourceSHA returns every artifact built from sourceSHA, ordered
// by id.
func (s *Store) ArtifactsBySourceSHA(ctx context.Context, sha string) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+artifactColumns+` FROM artifacts WHERE source_sha = $1 ORDER BY id`, sha)
	if err != nil {
		return nil, fmt.Errorf("artifacts by source sha %s: %w", sha, err)
	}
	return collectRows(rows, fmt.Sprintf("artifacts by source sha %s", sha), byValue(scanArtifact))
}

// ArtifactIDBySourceSHA looks up an artifact by source_sha inside the given
// transaction, for callers (e.g. the Flux webhook) that must resolve an
// artifact atomically with the rest of their apply. Returns nil if no
// artifact matches. Several artifacts can share a source_sha (built from the
// same commit by different jobs); the newest one (highest id) wins.
func ArtifactIDBySourceSHA(tx *sql.Tx, sha string) (*int64, error) {
	var id int64
	err := tx.QueryRow(
		`SELECT id FROM artifacts WHERE source_sha = $1 ORDER BY id DESC LIMIT 1`, sha,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("artifact id by source sha %s: %w", sha, err)
	}
	return &id, nil
}

// ArtifactByDigest looks up an artifact by its OCI digest inside the given
// transaction, for callers (the Flux webhook) that must resolve it
// atomically with the rest of their apply. Returns nil if no artifact
// matches. Several artifacts can share a digest (the same image published
// under two tags); the newest one (highest id) wins.
func ArtifactByDigest(tx *sql.Tx, digest string) (*Artifact, error) {
	row := tx.QueryRow(
		`SELECT `+artifactColumns+` FROM artifacts WHERE digest = $1 ORDER BY id DESC LIMIT 1`,
		digest)
	a, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("artifact by digest %s: %w", digest, err)
	}
	return a, nil
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
		`SELECT `+artifactColumns+` FROM artifacts WHERE kind = 'docker_image' AND name = $1 AND version = $2`,
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
		`SELECT `+artifactColumns+` FROM artifacts WHERE kind = 'docker_image' AND name = $1 AND version = $2`,
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
	ts := now.UTC()
	_, err := tx.Exec(
		`INSERT INTO deployments (artifact_id, environment, target_kind, target_name, status, first_seen, last_update)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (environment, target_kind, target_name) DO UPDATE SET
		   status = excluded.status,
		   artifact_id = COALESCE(excluded.artifact_id, deployments.artifact_id),
		   last_update = excluded.last_update`,
		artifactID, d.Environment, d.TargetKind, d.TargetName, d.Status, ts, ts,
	)
	if err != nil {
		return fmt.Errorf("upsert deployment %s/%s/%s: %w", d.Environment, d.TargetKind, d.TargetName, err)
	}
	return nil
}

// DeploymentStatus returns the current status of a deployment inside the
// given transaction ("" if none exists yet). Use it when a state transition
// (e.g. detecting a Flux recovery) must read the prior status atomically
// with the upsert that follows.
func DeploymentStatus(tx *sql.Tx, environment, targetKind, targetName string) (string, error) {
	var status string
	err := tx.QueryRow(
		`SELECT status FROM deployments WHERE environment = $1 AND target_kind = $2 AND target_name = $3`,
		environment, targetKind, targetName,
	).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("deployment status %s/%s/%s: %w", environment, targetKind, targetName, err)
	}
	return status, nil
}

func scanDeployment(row rowScanner) (*Deployment, error) {
	var d Deployment
	var artifactID sql.NullInt64
	if err := row.Scan(&d.ID, &artifactID, &d.Environment, &d.TargetKind, &d.TargetName,
		&d.Status, &d.FirstSeen, &d.LastUpdate); err != nil {
		return nil, err
	}
	if artifactID.Valid {
		d.ArtifactID = &artifactID.Int64
	}
	d.FirstSeen = d.FirstSeen.UTC()
	d.LastUpdate = d.LastUpdate.UTC()
	return &d, nil
}

// deploymentColumns is the SELECT list scanDeployment expects, in order.
const deploymentColumns = `id, artifact_id, environment, target_kind, target_name, status, first_seen, last_update`

// DeploymentsForArtifact returns the deployments currently referencing
// artifactID, ordered by last_update then id.
func (s *Store) DeploymentsForArtifact(ctx context.Context, artifactID int64) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deploymentColumns+` FROM deployments WHERE artifact_id = $1 ORDER BY last_update, id`,
		artifactID)
	if err != nil {
		return nil, fmt.Errorf("deployments for artifact %d: %w", artifactID, err)
	}
	return collectRows(rows, fmt.Sprintf("deployments for artifact %d", artifactID), byValue(scanDeployment))
}

// ListDeployments returns deployments, optionally filtered by environment
// ("" means all), ordered by environment, target_kind, target_name.
func (s *Store) ListDeployments(ctx context.Context, environment string) ([]Deployment, error) {
	q := `SELECT ` + deploymentColumns + ` FROM deployments`
	var args []any
	if environment != "" {
		q += ` WHERE environment = $1`
		args = append(args, environment)
	}
	q += ` ORDER BY environment, target_kind, target_name`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	return collectRows(rows, "list deployments", byValue(scanDeployment))
}
