package api_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// sourceBlock is a block outside Worklode Markdown, kept verbatim (WL-REQ-1871).
var sourceBlock = model.EditorBlock{Type: "source", Props: json.RawMessage(`{"markdown":"<div>raw</div>"}`)}

// TestRuleReadAsBlocks: ?as=blocks adds the body as blocks and the SHA-256 of
// the stored body; without it the response carries neither (WL-REQ-1867).
func TestRuleReadAsBlocks(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	if rr := doReq(t, h, http.MethodPost, "/api/v1/rules", token, model.AddRuleInput{Project: projID, Heading: "R", Body: "## H\n\npara\n", Kind: "invariant"}); rr.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rr.Code, rr.Body)
	}
	rr := doReq(t, h, http.MethodGet, "/api/v1/rules/WL-RULE-1", token, nil)
	if strings.Contains(rr.Body.String(), `"blocks"`) || strings.Contains(rr.Body.String(), "body_sha256") {
		t.Errorf("plain read carries blocks: %s", rr.Body)
	}
	var plain model.Rule
	decodeInto(t, rr, &plain)

	var got model.BlocksBody
	decodeInto(t, doReq(t, h, http.MethodGet, "/api/v1/rules/WL-RULE-1?as=blocks", token, nil), &got)
	if len(got.Blocks) != 2 || got.Blocks[0].Type != "heading" || got.Blocks[1].Type != "paragraph" {
		t.Fatalf("got %+v", got.Blocks)
	}
	// The hash is of the body as stored: a rule body is stored with a
	// leading newline, so it is not the input's hash.
	if got.BodySHA256 != sha(string(plain.Body)) {
		t.Errorf("body_sha256 = %q, want sha of %q", got.BodySHA256, plain.Body)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/rules/WL-RULE-1?as=html", token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("as=html: %d, want 422", rr.Code)
	}
}

// TestRuleWriteBlocks: PUT /rules/{id} takes blocks in place of body.
func TestRuleWriteBlocks(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	doReq(t, h, http.MethodPost, "/api/v1/rules", token, model.AddRuleInput{Project: projID, Heading: "R", Body: "x", Kind: "invariant"})

	rr := doReq(t, h, http.MethodPut, "/api/v1/rules/WL-RULE-1", token, model.EditRuleInput{Heading: "R", Blocks: []model.EditorBlock{{Type: "video"}}})
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), "video") {
		t.Errorf("video block: %d %s", rr.Code, rr.Body)
	}
	rr = doReq(t, h, http.MethodPut, "/api/v1/rules/WL-RULE-1", token, model.EditRuleInput{Heading: "R", Body: "y", Blocks: []model.EditorBlock{sourceBlock}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("body and blocks: %d, want 422", rr.Code)
	}
	rr = doReq(t, h, http.MethodPut, "/api/v1/rules/WL-RULE-1", token, model.EditRuleInput{Heading: "R", Blocks: []model.EditorBlock{sourceBlock}})
	var c model.Rule
	decodeInto(t, rr, &c)
	if rr.Code != http.StatusOK || strings.TrimSpace(string(c.Body)) != "<div>raw</div>" {
		t.Errorf("source block: %d body %q", rr.Code, c.Body)
	}
}

// TestTaskBlocks: GET /tasks/{id}?as=blocks and PATCH with blocks.
func TestTaskBlocks(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	task := createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "T", "priority": "medium", "kind": "chore", "body": "# H\n\npara\n"})
	path := "/api/v1/tasks/" + task["id"].(string)

	var got struct {
		model.BlocksBody
		Body string `json:"body"`
	}
	decodeInto(t, doReq(t, h, http.MethodGet, path+"?as=blocks", token, nil), &got)
	if len(got.Blocks) != 2 || got.BodySHA256 != sha(got.Body) {
		t.Errorf("read: %+v", got)
	}

	if rr := doReq(t, h, http.MethodPatch, path, token, map[string]any{"blocks": []model.EditorBlock{{Type: "video"}}}); rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), "video") {
		t.Errorf("video block: %d %s", rr.Code, rr.Body)
	}
	if rr := doReq(t, h, http.MethodPatch, path, token, map[string]any{"body": "x", "blocks": []model.EditorBlock{sourceBlock}}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("body and blocks: %d", rr.Code)
	}
	rr := doReq(t, h, http.MethodPatch, path, token, map[string]any{"blocks": []model.EditorBlock{sourceBlock}})
	var tk model.Task
	decodeInto(t, rr, &tk)
	if rr.Code != http.StatusOK || strings.TrimSpace(string(tk.Body)) != "<div>raw</div>" {
		t.Errorf("source block: %d body %q", rr.Code, tk.Body)
	}
}

// TestDocBlocks: GET /docs/{id}?as=blocks serves any document, PUT
// /docs/{id}/body takes blocks for a plan and refuses them for a spec, whose
// text changes through its rules (WL-REQ-1300).
func TestDocBlocks(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	spec := createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "s", Body: ruleDocV1})
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "plan", Slug: "p",
		Body: "---\nstatus: draft\n---\n# P\n\nText.\n"})

	for _, d := range []model.Doc{spec, plan} {
		var got struct {
			model.BlocksBody
			Body string `json:"body"`
		}
		decodeInto(t, doReq(t, h, http.MethodGet, docPath(d.ID, "?as=blocks"), token, nil), &got)
		if len(got.Blocks) == 0 || got.BodySHA256 != sha(got.Body) {
			t.Errorf("%s read: %+v", d.Kind, got)
		}
	}

	if rr := doReq(t, h, http.MethodPut, docPath(spec.ID, "/body"), token, model.UpdateDocBodyInput{Blocks: []model.EditorBlock{sourceBlock}}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("spec blocks write: %d, want 422", rr.Code)
	}
	if rr := doReq(t, h, http.MethodPut, docPath(plan.ID, "/body"), token, model.UpdateDocBodyInput{Body: "x", Blocks: []model.EditorBlock{sourceBlock}}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("body and blocks: %d", rr.Code)
	}
	if rr := doReq(t, h, http.MethodPut, docPath(plan.ID, "/body"), token, model.UpdateDocBodyInput{Blocks: []model.EditorBlock{{Type: "video"}}}); rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), "video") {
		t.Errorf("video block: %d %s", rr.Code, rr.Body)
	}
	heading := model.EditorBlock{Type: "heading", Props: json.RawMessage(`{"level":1}`), Content: []model.InlineContent{{Type: "text", Text: "P2"}}}
	rr := doReq(t, h, http.MethodPut, docPath(plan.ID, "/body"), token, model.UpdateDocBodyInput{Blocks: []model.EditorBlock{heading, sourceBlock}})
	var d model.Doc
	decodeInto(t, rr, &d)
	if rr.Code != http.StatusOK || !strings.Contains(string(d.Body), "# P2\n\n<div>raw</div>") {
		t.Errorf("plan blocks write: %d body %q", rr.Code, d.Body)
	}
}
