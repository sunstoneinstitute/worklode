package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestCreateDeliverableWithArtifact: the JSON API accepts the address a
// deliverable is verified by (029 §3.1) and echoes it back on the read, while
// reported_state stays empty — nothing has reported yet, and the deliverable
// itself never claims a state (§3.2).
func TestCreateDeliverableWithArtifact(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	const artifact = "bigquery://sunstone-prod/cow/casualties"
	rr := doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
		model.CreateDeliverableInput{Name: "Casualty datapackage", Artifact: "  " + artifact + "  "})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	var created model.Deliverable
	decodeInto(t, rr, &created)
	if created.Artifact != artifact {
		t.Fatalf("created artifact = %q, want %q", created.Artifact, artifact)
	}

	rr = doReq(t, h, "GET", "/api/v1/projects/proj/deliverables", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var list model.DeliverableListResponse
	decodeInto(t, rr, &list)
	if len(list.Deliverables) != 1 {
		t.Fatalf("listed %+v, want one deliverable", list.Deliverables)
	}
	got := list.Deliverables[0]
	if got.Artifact != artifact {
		t.Errorf("listed artifact = %q, want %q", got.Artifact, artifact)
	}
	if got.ReportedState != "" || got.ReportedAt != nil {
		t.Errorf("listed reported state = %q at %v, want unreported", got.ReportedState, got.ReportedAt)
	}
}

// TestCreateDeliverableArtifactBounds: the artifact is length-checked and
// nothing else. A catalog address is not a browser link, so schemes the URL
// field refuses are legal here.
func TestCreateDeliverableArtifactBounds(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	for _, artifact := range []string{
		"gs://sunstone-prod/cow/casualties",
		"iceberg://warehouse/cow.casualties",
		"urn:datapackage:cow-casualties",
	} {
		rr := doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
			model.CreateDeliverableInput{Name: artifact, Artifact: artifact})
		if rr.Code != http.StatusCreated {
			t.Errorf("%s: status = %d, want 201; body %s", artifact, rr.Code, rr.Body.String())
		}
	}

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
		model.CreateDeliverableInput{Name: "too long", Artifact: strings.Repeat("x", 2001)})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-long artifact status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
}
