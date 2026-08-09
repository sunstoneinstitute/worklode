package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

type docSectionJSON struct {
	Anchor   string `json:"anchor"`
	Heading  string `json:"heading"`
	Depth    int    `json:"depth"`
	Position int    `json:"position"`
}

type docEdgeJSON struct {
	SrcAnchor    string `json:"src_anchor"`
	Rel          string `json:"rel"`
	Target       string `json:"target"`
	TargetAnchor string `json:"target_anchor"`
}

type docUpsertJSON struct {
	Kind        string           `json:"kind"`
	Ordinal     string           `json:"ordinal"`
	Status      string           `json:"status"`
	Title       string           `json:"title"`
	Body        string           `json:"body"`
	Frontmatter json.RawMessage  `json:"frontmatter"`
	Sections    []docSectionJSON `json:"sections"`
	Edges       []docEdgeJSON    `json:"edges"`
}

type docSyncRequest struct {
	Project      string          `json:"project"`
	SourceBranch string          `json:"source_branch"`
	Dirty        bool            `json:"dirty"`
	Force        bool            `json:"force"`
	DryRun       bool            `json:"dry_run"`
	Docs         []docUpsertJSON `json:"docs"`
}

type docResultJSON struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Outcome string `json:"outcome"`
}

type docSyncResponse struct {
	DryRun    bool            `json:"dry_run"`
	Added     int             `json:"added"`
	Updated   int             `json:"updated"`
	Unchanged int             `json:"unchanged"`
	Results   []docResultJSON `json:"results"`
}

func toStoreUpserts(in []docUpsertJSON) []store.DocUpsert {
	out := make([]store.DocUpsert, 0, len(in))
	for _, d := range in {
		u := store.DocUpsert{
			Kind: d.Kind, Ordinal: d.Ordinal, Status: d.Status,
			Title: d.Title, Body: d.Body, Frontmatter: d.Frontmatter,
		}
		for _, sec := range d.Sections {
			u.Sections = append(u.Sections, store.DocSection(sec))
		}
		for _, e := range d.Edges {
			u.Edges = append(u.Edges, store.DocEdge(e))
		}
		out = append(out, u)
	}
	return out
}

func toSyncResponse(dryRun bool, results []store.DocSyncResult) docSyncResponse {
	resp := docSyncResponse{DryRun: dryRun, Results: []docResultJSON{}}
	for _, r := range results {
		resp.Results = append(resp.Results, docResultJSON{ID: r.DocID, Kind: r.Kind, Outcome: r.Outcome})
		switch r.Outcome {
		case "added":
			resp.Added++
		case "updated":
			resp.Updated++
		case "unchanged":
			resp.Unchanged++
		}
	}
	return resp
}

// syncDocs handles POST /api/v1/docs/sync — spec 034 §3/§4's bulk upsert.
func (s *server) syncDocs(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req docSyncRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	upserts := toStoreUpserts(req.Docs)

	if req.DryRun {
		results, err := s.st.DocSyncOutcomes(r.Context(), req.Project, upserts)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toSyncResponse(true, results))
		return
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	// Slim payload: the docs table holds the content; the event holds the act.
	payload, err := json.Marshal(map[string]any{
		"project": req.Project, "source_branch": req.SourceBranch,
		"dirty": req.Dirty, "force": req.Force, "doc_count": len(req.Docs),
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	now := s.st.Now()
	prov := store.DocSyncProvenance{SourceBranch: req.SourceBranch, Dirty: req.Dirty}

	var results []store.DocSyncResult
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "docs.synced", payload,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			results, err = s.st.ApplyDocSync(tx, now, eventID, req.Project, prov, upserts)
			return err
		})
	s.observeDocSync(results, req.Force, err, time.Since(start))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSyncResponse(false, results))
}
