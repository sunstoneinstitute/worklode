package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// approvalInstruction is the WL-REQ-1842 fallback instruction.
const approvalInstruction = "Ask the user with your question tool (e.g. AskUserQuestion), offering these choices and an optional note. In a voice conversation, read the approval and the choices and wait for the verdict. Then call approval_decide with that verdict and confirmed=true."

// reconnectAnswer is the WL-REQ-1842 answer to a decide refused for stale
// group claims.
const reconnectAnswer = "Your group membership is out of date, so the approval was not decided. Reconnect the Worklode connector to log in again, then decide again."

// approvalChoices are the WL-REQ-1842 verdicts as offered; verdictOf maps
// one to the API's spelling.
var approvalChoices = []string{"approve", "request changes", "reject"}

// elicitVerdict is the id of approval_decide's input request and the name of
// its verdict field; elicitNote is the note field.
const (
	elicitVerdict = "verdict"
	elicitNote    = "note"
)

type docReviewersArgs struct {
	Project   string   `json:"project"`
	ID        string   `json:"id"`
	Reviewers []string `json:"reviewers"`
}

type approvalRequestArgs struct {
	Project string `json:"project"`
	Doc     string `json:"doc"`
}

type approvalListArgs struct {
	State  string `json:"state"`
	Cursor string `json:"cursor"`
}

type approvalAddArgs struct {
	Project  string `json:"project"`
	Entity   string `json:"entity"`
	Approver string `json:"approver"`
}

type approvalDecideArgs struct {
	ID        int64  `json:"id"`
	Verdict   string `json:"verdict"`
	Note      string `json:"note"`
	Confirmed bool   `json:"confirmed"`
}

const docRefProp = `{"type":"string","description":"A spec or plan's ref, as written or spoken."}`

func (s *server) addReviewTools() {
	s.addTool("doc_reviewers", "Show a document's reviewers and who has not yet decided. With reviewers, replace the set.",
		`{"type":"object","properties":{`+projectProp+`,"id":`+docRefProp+`,"reviewers":{"type":"array","items":{"type":"string"},"description":"The actor ids that review the document."}},"required":["id"]}`,
		s.docReviewers)
	s.addTool("approval_request", "Ask a document's reviewers for their verdict on its current version.",
		`{"type":"object","properties":{`+projectProp+`,"doc":`+docRefProp+`},"required":["doc"]}`,
		s.approvalRequest)
	s.addTool("approval_list", "List the approvals waiting on a decision, ten at a time.",
		`{"type":"object","properties":{"state":{"type":"string","description":"Only approvals in this state."},"cursor":{"type":"string","description":"The cursor from the previous page."}}}`,
		s.approvalList)
	s.addTool("approval_add", "Require an approval from one person on a document or task.",
		`{"type":"object","properties":{`+projectProp+`,"entity":{"type":"string","description":"The document or task's ref, as written or spoken."},"approver":{"type":"string","description":"The actor id that decides."}},"required":["entity","approver"]}`,
		s.approvalAdd)
	s.addTool("approval_decide", "Record the user's verdict on an approval. The verdict is the user's call: never decide on their behalf. Pass confirmed=true only with the verdict the user gave.",
		`{"type":"object","properties":{"id":{"type":"integer","description":"The approval's id."},"verdict":{"type":"string","enum":["approve","request changes","reject"]},"note":{"type":"string","description":"The user's note on the verdict."},"confirmed":{"type":"boolean","description":"The user gave this verdict."}},"required":["id"]}`,
		s.approvalDecide)
}

// docReviewers reads the reviewers from GET /docs/{id} (D3) or replaces them.
func (s *server) docReviewers(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[docReviewersArgs](req)
	if err != nil {
		return nil, err
	}
	d, err := s.docID(ctx, c, a.Project, a.ID)
	if err != nil {
		return nil, err
	}
	ref := cli.DocRef(d)
	if a.Reviewers != nil {
		if _, _, err := c.SetDocReviewers(ctx, d.ID, a.Reviewers); err != nil {
			return nil, err
		}
		return say(fmt.Sprintf("%s is reviewed by %s.", ref, orNobody(a.Reviewers)), nil), nil
	}
	det, _, err := c.GetDoc(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	return say(fmt.Sprintf("%s is reviewed by %s; awaiting %s.", ref, orNobody(det.Reviewers), orNobody(det.ReviewersAwaiting)),
		map[string][]string{"reviewers": det.Reviewers, "awaiting": det.ReviewersAwaiting}), nil
}

func orNobody(names []string) string {
	if len(names) == 0 {
		return "nobody"
	}
	return strings.Join(names, ", ")
}

func (s *server) approvalRequest(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[approvalRequestArgs](req)
	if err != nil {
		return nil, err
	}
	d, err := s.docID(ctx, c, a.Project, a.Doc)
	if err != nil {
		return nil, err
	}
	got, _, err := c.RequestDocApproval(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	return say(fmt.Sprintf("Asked the reviewers of %s for their verdict on version %d.", cli.DocRef(got), got.Version), nil), nil
}

func (s *server) approvalList(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[approvalListArgs](req)
	if err != nil {
		return nil, err
	}
	r, _, err := c.ListApprovals(ctx)
	if err != nil {
		return nil, err
	}
	rows := r.Approvals
	if a.State != "" {
		rows = rows[:0:0]
		for _, row := range r.Approvals {
			if row.State == a.State {
				rows = append(rows, row)
			}
		}
	}
	if len(rows) == 0 {
		return say("No approvals are waiting.", nil), nil
	}
	page, next, err := pageOf(rows, a.Cursor, s.cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	cli.ApprovalTable(&b, page)
	if next != "" {
		fmt.Fprintf(&b, "\nMore approvals: call approval_list again with cursor %q.\n", next)
	}
	return say(fmt.Sprintf("%d approvals are waiting.", len(rows)), b.String()), nil
}

// approvalAdd requires approver on a document, or on a task when the ref
// names no document.
func (s *server) approvalAdd(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[approvalAddArgs](req)
	if err != nil {
		return nil, err
	}
	in := model.RequireApprovalInput{Actor: a.Approver}
	name := ""
	d, err := s.docID(ctx, c, a.Project, a.Entity)
	var te *toolError
	switch {
	case err == nil:
		in.EntityKind, in.EntityID, name = "doc", model.DocEntityID(d.ID), cli.DocRef(d)
	case errors.As(err, &te) && te.outcome == outcomeNotFound:
		task, err := s.taskRef(ctx, c, a.Project, a.Entity)
		if err != nil {
			return nil, err
		}
		if _, _, err := c.GetTask(ctx, task); err != nil {
			return nil, err
		}
		in.EntityKind, in.EntityID, name = "task", task, task
	default:
		return nil, err
	}
	got, _, err := c.RequireApproval(ctx, in)
	if err != nil {
		return nil, err
	}
	return say(fmt.Sprintf("Approval %d on %s now waits on %s.", got.ID, name, a.Approver), nil), nil
}

// approvalDecide records a verdict only when the user gave it: a confirmed
// verdict, or an accepted elicitation (WL-REQ-1842, D4). Otherwise it returns
// the approval's facts, the choices and the instruction, and decides nothing.
func (s *server) approvalDecide(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[approvalDecideArgs](req)
	if err != nil {
		return nil, err
	}
	row, err := awaitingApproval(ctx, c, a.ID)
	if err != nil {
		return nil, err
	}
	if a.Confirmed && a.Verdict != "" {
		return s.decide(ctx, c, row, a.Verdict, a.Note)
	}
	if resp, answered := req.Params.InputResponses[elicitVerdict]; answered {
		if er, ok := resp.(*mcp.ElicitResult); ok && er.Action == "accept" {
			verdict, _ := er.Content[elicitVerdict].(string)
			note, _ := er.Content[elicitNote].(string)
			if verdict != "" {
				return s.decide(ctx, c, row, verdict, note)
			}
		}
		return say(fmt.Sprintf("The user gave no verdict on approval %d; nothing was decided.", row.ID), nil), nil
	}
	who, _, err := c.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	facts := fmt.Sprintf("Approval %d on %s %s (%s), revision %s, decided by %s.",
		row.ID, row.EntityKind, row.EntityID, row.Title, revisionOf(row), who.ID)
	if canElicit(req) {
		schema, _ := json.Marshal(map[string]any{"type": "object", "required": []string{elicitVerdict}, "properties": map[string]any{
			elicitVerdict: map[string]any{"type": "string", "title": "Your verdict", "enum": approvalChoices},
			elicitNote:    map[string]any{"type": "string", "title": "Note (optional)"},
		}})
		return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{elicitVerdict: &mcp.ElicitParams{
			Mode: "form", Message: facts, RequestedSchema: json.RawMessage(schema),
		}}}, nil
	}
	return say("Ask the user for their verdict. Nothing was decided.", struct {
		Approval    int64    `json:"approval"`
		Entity      string   `json:"entity"`
		Title       string   `json:"title"`
		Revision    string   `json:"revision"`
		Approver    string   `json:"approver"`
		Choices     []string `json:"choices"`
		Instruction string   `json:"instruction"`
	}{row.ID, row.EntityKind + " " + row.EntityID, row.Title, revisionOf(row), who.ID, approvalChoices, approvalInstruction}), nil
}

func revisionOf(a model.AwaitingApproval) string {
	if a.SubjectRevision == "" {
		return "current"
	}
	return a.SubjectRevision
}

// awaitingApproval finds approval id in GET /approvals, the only API read of
// an approval row.
func awaitingApproval(ctx context.Context, c *cli.Client, id int64) (model.AwaitingApproval, error) {
	r, _, err := c.ListApprovals(ctx)
	if err != nil {
		return model.AwaitingApproval{}, err
	}
	for _, a := range r.Approvals {
		if a.ID == id {
			return a, nil
		}
	}
	return model.AwaitingApproval{}, &toolError{outcome: outcomeNotFound, msg: fmt.Sprintf("No approval %d is waiting on a decision.", id)}
}

func (s *server) decide(ctx context.Context, c *cli.Client, row model.AwaitingApproval, verdict, note string) (*mcp.CallToolResult, error) {
	got, _, err := c.DecideApproval(ctx, row.ID, model.DecideApprovalInput{Verdict: strings.ReplaceAll(strings.TrimSpace(verdict), " ", "_"), Note: note})
	var ce *cli.ClientError
	if errors.As(err, &ce) && ce.Status == http.StatusForbidden && strings.Contains(ce.Msg, "log in again") {
		return nil, &toolError{outcome: outcomeRefused, msg: reconnectAnswer}
	}
	if err != nil {
		return nil, err
	}
	return say(fmt.Sprintf("Recorded %s on approval %s.", strings.ReplaceAll(got.State, "_", " "), strconv.FormatInt(got.ID, 10)), nil), nil
}
