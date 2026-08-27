package graphproj

// GitHubHost qualifies repo-derived local ids passed to internal/kg/iri. The
// backbone stores repos as "owner/name" (GitHub full_name); the shared IRI
// grammar wants host-qualified, owner/repo-split coordinates.
const GitHubHost = "github.com"

// External vocabulary the runtime projection reuses (006 §3.1 table),
// extending the constants task.go declares. wl: terms resolve through
// iri.Term, wlc: concepts through iri.Concept — never hardcoded.
const (
	DCTIdentifier       = "http://purl.org/dc/terms/identifier"
	OWLVersionInfo      = "http://www.w3.org/2002/07/owl#versionInfo"
	ProvGeneratedAtTime = "http://www.w3.org/ns/prov#generatedAtTime"
	ProvStartedAtTime   = "http://www.w3.org/ns/prov#startedAtTime"
	ProvUsed            = "http://www.w3.org/ns/prov#used"
	ProvWasDerivedFrom  = "http://www.w3.org/ns/prov#wasDerivedFrom"
)
