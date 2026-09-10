package model

// ProbeTargetsResponse is the body of GET /api/v1/probe-targets: every
// artifact address (029 §3.2) a still-open entity declared with the
// "address" selector. Label declarations never appear here — their
// addresses are minted at build time and reach worklode by push, not poll.
type ProbeTargetsResponse struct {
	Artifacts []string `json:"artifacts"`
}

// ArtifactReportInput is the request body for POST /api/v1/artifact-reports,
// posted by the prober after probing one of the addresses ProbeTargets
// listed. State is one of the artifact_evidence CHECK values (published |
// updated | deprecated | removed | failed).
type ArtifactReportInput struct {
	Artifact   string `json:"artifact"`
	State      string `json:"state"`
	Version    string `json:"version"`
	URL        string `json:"url"`
	DedupeKey  string `json:"dedupe_key"`
	OccurredAt string `json:"occurred_at"`
}
