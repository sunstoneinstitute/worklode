// artifactevidence.go holds the two halves of spec 029 §3.1/§3.2's "verified
// by address": the declarations that say which entity owns an artifact
// address, and the evidence emitters report against it. Routing is a lookup,
// not a static map — a delivery names an artifact, and the fact lands against
// every still-open entity that declared it.
//
// The declaration and insert helpers are tx-scoped so the catalog ingest runs
// them inside its RecordEvent transaction: a delivery is all-or-nothing.

package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// DeclareArtifact records that (entityKind, entityID) is verified by the
// artifact at this address. Re-declaring is a no-op, so a create path that
// runs twice does not duplicate the routing target.
func DeclareArtifact(tx *sql.Tx, now time.Time, entityKind, entityID, artifact string) error {
	_, err := tx.Exec(
		`INSERT INTO artifact_declarations
		   (entity_kind, entity_id, artifact_uri, created_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (entity_kind, entity_id, artifact_uri) DO NOTHING`,
		entityKind, entityID, artifact, now.UTC())
	if err != nil {
		return fmt.Errorf("declare artifact %s for %s %s: %w", artifact, entityKind, entityID, err)
	}
	return nil
}

// DeclaredEntity names one routing target: the entity kind and its id.
type DeclaredEntity struct {
	Kind string
	ID   string
}

// openDeclarationsSQL routes an artifact address to the entities that
// declared it and are still open. "Open" differs per kind:
//
//   - deliverable: always. It stores no state at all (029 §3.2), and
//     supplying the state it lacks is what this path is for.
//   - task: live, and not past its repo's done_state — taskClosed's notion,
//     shared with the ready set, so evidence and blocking cannot drift on
//     what closed means.
//   - doc: live, and not superseded. An accepted spec is still the live
//     declaration; only superseded is past done.
//
// The task arm inherits taskClosed's bound aliases (ch, cht, tc, mc, pr), so
// it binds only ad and t.
var openDeclarationsSQL = `
SELECT 'deliverable'::text, d.id FROM artifact_declarations ad
  JOIN deliverables d ON d.id = ad.entity_id
 WHERE ad.entity_kind = 'deliverable' AND ad.artifact_uri = $1
UNION ALL
SELECT 'task'::text, t.id FROM artifact_declarations ad
  JOIN tasks t ON t.id = ad.entity_id
 WHERE ad.entity_kind = 'task' AND ad.artifact_uri = $1
   AND t.deleted_at IS NULL AND NOT ` + taskClosed("t") + `
UNION ALL
SELECT 'doc'::text, dc.id::text FROM artifact_declarations ad
  JOIN docs dc ON dc.id::text = ad.entity_id
 WHERE ad.entity_kind = 'doc' AND ad.artifact_uri = $1
   AND dc.deleted_at IS NULL AND dc.status <> 'superseded'
ORDER BY 1, 2`

// OpenDeclarationsForArtifact returns every open entity that declared
// artifact, ordered by kind then id so a delivery writes its evidence rows in
// the same order every time.
func OpenDeclarationsForArtifact(tx *sql.Tx, artifact string) ([]DeclaredEntity, error) {
	rows, err := tx.Query(openDeclarationsSQL, artifact)
	if err != nil {
		return nil, fmt.Errorf("open declarations for %s: %w", artifact, err)
	}
	return collectRows(rows, "open declarations for "+artifact, func(r rowScanner) (DeclaredEntity, error) {
		var d DeclaredEntity
		err := r.Scan(&d.Kind, &d.ID)
		return d, err
	})
}

// InsertArtifactEvidence files one reported fact against a declared artifact.
// inserted is false when this event already produced evidence for this entity
// — a redelivery, which must not double-file. ev.Source and ev.Provenance are
// the caller's to set; the event is the provenance record either way.
func InsertArtifactEvidence(tx *sql.Tx, eventID int64, ev model.ArtifactEvidence) (bool, error) {
	var detail any
	if len(ev.Detail) > 0 {
		detail = []byte(ev.Detail)
	}
	res, err := tx.Exec(
		`INSERT INTO artifact_evidence
		   (entity_kind, entity_id, artifact_uri, source, state, provenance,
		    version, url, detail, event_id, occurred_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (entity_kind, entity_id, event_id) DO NOTHING`,
		ev.EntityKind, ev.EntityID, ev.Artifact, ev.Source, ev.State, ev.Provenance,
		ev.Version, ev.URL, detail, eventID, ev.OccurredAt.UTC())
	if err != nil {
		return false, fmt.Errorf("insert artifact evidence for %s %s: %w",
			ev.EntityKind, ev.EntityID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert artifact evidence for %s %s: %w",
			ev.EntityKind, ev.EntityID, err)
	}
	return n > 0, nil
}

// There is deliberately no "read the latest evidence for an entity" helper
// here. The only reader today is the deliverable projection, which joins it in
// (deliverables.go's deliverableFrom); a second spelling of "newest by
// occurred_at, id" would be a second place for that rule to drift. Add one
// when a second entity kind needs the read, not before.
