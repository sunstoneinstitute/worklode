package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
)

// TestDeriveFailureStatusBlamesTheRightParty: POST /api/v1/derive reads
// Postgres as well as writing the graph endpoint. Mapping is tested here
// rather than through the handler because reaching runServerDerivers needs a
// configured GitHub App as well as a graph client.
func TestDeriveFailureStatusBlamesTheRightParty(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"graph unavailable", fmt.Errorf("put graph: %w", graphserver.ErrUnavailable), http.StatusBadGateway},
		{"sparql unavailable", fmt.Errorf("stored hash: %w", graphserver.ErrSPARQLUnavailable), http.StatusBadGateway},
		{"store failure", errors.New("task prs: connection refused"), http.StatusInternalServerError},
	} {
		if got := deriveFailureStatus(tc.err); got != tc.want {
			t.Errorf("%s: deriveFailureStatus = %d; want %d", tc.name, got, tc.want)
		}
	}
}
