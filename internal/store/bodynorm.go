package store

import (
	"sync/atomic"

	"github.com/sunstoneinstitute/worklode/internal/mdblocks"
)

// bodyNormMetrics counts normalisations. The writers that normalise take a
// transaction, not the Store, so WithMetrics sets this once per process; nil
// records nothing.
var bodyNormMetrics atomic.Pointer[mdblocks.Metrics]

// normalizeBody is the stored form of a document, rule or task body
// (WL-REQ-1867): mdblocks.Normalize, counted under kind. Every write path
// markdownWriters lists for Doc.Body, Rule.Body and Task.Body stores it, and
// runs it before inline references are extracted.
func normalizeBody(kind, body string) string {
	return string(bodyNormMetrics.Load().Normalize(kind, []byte(body)))
}
