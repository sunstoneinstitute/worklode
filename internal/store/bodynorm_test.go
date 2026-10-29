package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Every document, rule and task body write stores mdblocks.Normalize's
// output (WL-REQ-1867): a wrapped paragraph joins to one line and a `*`
// bullet becomes `-`.
const (
	rawBody  = "para one\nwrapped here\n\n* star bullet\n"
	normBody = "para one wrapped here\n\n- star bullet\n"
)

func TestAddRuleNormalises(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	r, err := s.AddRule(context.Background(), model.AddRuleInput{Project: "p1", Heading: "Heading", Body: rawBody}, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Body) != "\n"+normBody {
		t.Fatalf("AddRule body not normalised: %q", r.Body)
	}
}

// TestEditRuleNormalises edits a rule whose stored body is unnormalised
// (written before WL-REQ-1867), so the edit cannot be the "same text, no new
// version" no-op: the stored text differs from the input, and the normalised
// input differs from both.
func TestEditRuleNormalises(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	r, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "Heading", Body: "x"}, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE rule_versions SET body = $2 FROM rules r WHERE r.id = rule_versions.rule_id AND r.number = $1`,
		r.Number, "\n"+rawBody); err != nil {
		t.Fatal(err)
	}
	got, err := s.EditRule(ctx, "P1", r.Number, model.EditRuleInput{Heading: "Heading", Body: rawBody}, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != "\n"+normBody {
		t.Fatalf("EditRule body not normalised: %q", got.Body)
	}
	// Normalised against normalised stored text: the same input again writes nothing.
	again, err := s.EditRule(ctx, "P1", r.Number, model.EditRuleInput{Heading: "Heading", Body: rawBody}, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if again.Version != got.Version || again.Body != got.Body {
		t.Errorf("repeat edit changed the rule: %+v", again)
	}
}

// TestCreateDocNormalises: a plan created with a header and a task's YAML
// block keeps both intact while its prose is normalised.
func TestCreateDocNormalises(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "p", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# Plan\n\n" + rawBody +
			"\n## Tasks\n\n### Task 1 — Build it\n\n```yaml\nkind: feature\nskills:\n  - a\n```\n\nDo\nit.\n"})
	want := "# Plan\n\n" + normBody +
		"\n## Tasks\n\n### Task 1 — Build it\n\n```yaml\nkind: feature\nskills:\n  - a\n```\n\nDo it.\n"
	if string(d.Body) != want {
		t.Fatalf("plan body:\n%q\nwant:\n%q", d.Body, want)
	}
	if d.Status != "draft" {
		t.Errorf("header lost: status = %q", d.Status)
	}
}

// TestCreateDocNormalisesSpecRules: a spec's rule bodies come from its
// sections, so they are stored normalised too.
func TestCreateDocNormalisesSpecRules(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# T\n\n## 1. One {#sec-1}\n\n" + rawBody})
	r, err := s.GetRule(context.Background(), "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Body) != "\n"+normBody {
		t.Fatalf("spec rule body: %q", r.Body)
	}
}

func TestUpdateDocBodyNormalises(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "p", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# Plan\n\nx\n"})
	got, err := updateDocBody(t, s, d.ID, "# Plan\n\n"+rawBody)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != "# Plan\n\n"+normBody {
		t.Fatalf("body: %q", got.Body)
	}
}

func TestPatchDocNormalises(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Number: 100, Slug: "100-a",
		Body: patchSpecBody, CreatedBy: "stig", Status: "accepted"})
	body := strings.Replace(patchSpecBody, "Scope body.", "Scope body,\nrestated.", 1)
	got, _, err := patchDoc(t, s, DocPatchInput{ID: spec.ID, Body: body, Note: "n", ActorID: "stig"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain(string(got.Body)), "{#sec-1}\n\nScope body, restated.\n") {
		t.Fatalf("patched body not normalised:\n%s", got.Body)
	}
}

// TestPatchDocComparesNormalisedText: an accepted spec stored before
// WL-REQ-1867 still has wrapped text. A patch of one section changes only
// that section; the others differ from the stored text by normalisation
// alone and do not count as changed.
func TestPatchDocComparesNormalisedText(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Number: 100, Slug: "100-a",
		Body: patchSpecBody, CreatedBy: "stig", Status: "accepted"})
	// sec-2's rule is P1-REQ-2: give it a legacy wrapped body.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE rule_versions SET body = $1 FROM rules r WHERE r.id = rule_versions.rule_id AND r.number = 2`,
		"\nModel\nbody.\n\n"); err != nil {
		t.Fatal(err)
	}
	cur, err := s.GetDoc(ctx, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cur.Body), "Model\nbody.") {
		t.Fatalf("legacy text not in place:\n%s", cur.Body)
	}
	_, res, err := patchDoc(t, s, DocPatchInput{ID: spec.ID, Body: reword(plain(string(cur.Body))), Note: "n", ActorID: "stig"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.ChangedAnchors, ",") != "sec-1" {
		t.Errorf("changed anchors = %v, want [sec-1]", res.ChangedAnchors)
	}
	// Feeding the legacy text back unchanged is "nothing changed".
	cur, err = s.GetDoc(ctx, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := patchDoc(t, s, DocPatchInput{ID: spec.ID, Body: strings.Replace(plain(string(cur.Body)), "Model body.", "Model\nbody.", 1), Note: "n", ActorID: "stig"}); err == nil || !strings.Contains(err.Error(), "nothing changed") {
		t.Errorf("unchanged-but-wrapped patch: err = %v, want nothing changed", err)
	}
}

func TestCreateTaskNormalises(t *testing.T) {
	s := openDocStore(t)
	task := createTask(t, s, s.Now(), TaskInput{ProjectID: "p1", Title: "T", Body: rawBody, Priority: "medium", Kind: "feature"})
	if string(task.Body) != normBody {
		t.Fatalf("CreateTask body: %q", task.Body)
	}
}

func TestUpdateTaskFieldsNormalises(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	task := createTask(t, s, s.Now(), TaskInput{ProjectID: "p1", Title: "T", Body: "x", Priority: "medium", Kind: "feature"})
	body := model.Markdown(rawBody)
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return UpdateTaskFields(tx, s.Now(), task.ID, nil, &body, nil, nil, nil, nil, nil, nil, nil, nil)
	}); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := s.db.QueryRowContext(ctx, `SELECT body FROM tasks WHERE id = $1`, task.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != normBody {
		t.Fatalf("UpdateTaskFields body: %q", got)
	}
}
