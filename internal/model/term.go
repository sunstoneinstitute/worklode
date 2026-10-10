package model

// Term is a definition rule read as the term it defines (WL-REQ-1368).
// Slug is the heading's slug, the last segment of the term's canonical page
// /projects/<proj>/term/<slug>. Instance is true when the term resolved
// from the instance glossary because the project defines no such term.
// NeededBy is every needs edge into the definition; a list leaves it empty.
type Term struct {
	Slug     string     `json:"slug"`
	Instance bool       `json:"instance"`
	Rule     Rule       `json:"rule"`
	NeededBy []RuleEdge `json:"needed_by"`
}
