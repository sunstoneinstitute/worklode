package api

import (
	"net/http"
	"regexp"
	"strconv"
)

// clauseRef is the CL arm of 025 §14.3's <KEY>-<TYPE>-<n> grammar.
var clauseRef = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-CL-(\d+)$`)

// parseClauseRef splits "WL-CL-12" into its project key and number.
func parseClauseRef(ref string) (key string, number int64, ok bool) {
	m := clauseRef.FindStringSubmatch(ref)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// getClause handles GET /api/v1/clauses/{id}, where {id} is a clause ref
// such as WL-CL-12.
func (s *server) getClause(w http.ResponseWriter, r *http.Request) {
	key, number, ok := parseClauseRef(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "clause id must look like WL-CL-12")
		return
	}
	c, err := s.st.GetClause(r.Context(), key, number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
