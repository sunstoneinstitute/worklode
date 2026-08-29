package cmd

import (
	"context"
	"strings"
	"testing"
)

// TestDocReviewersSetAndShow: `lode doc reviewers <ref> --set` assigns the
// durable set (025 §7.3, WL-359); with no flag, the same command prints it
// back, plus whoever still owes a review after `lode approval request`.
func TestDocReviewersSetAndShow(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	if err := st.CreateActor(context.Background(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	specFile := writeDocFile(t, docTestBody)
	if _, err := runLode(t, "doc", "new", "--project", "proj", "--kind", "spec",
		"--slug", "rev-spec", "--file", specFile); err != nil {
		t.Fatalf("doc new: %v", err)
	}

	out, err := runLode(t, "doc", "reviewers", "rev-spec", "--set", "bob")
	if err != nil {
		t.Fatalf("doc reviewers --set: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "bob") {
		t.Fatalf("doc reviewers --set output = %q, want it to report bob", out)
	}

	out, err = runLode(t, "doc", "reviewers", "rev-spec")
	if err != nil {
		t.Fatalf("doc reviewers (show): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "bob") {
		t.Fatalf("doc reviewers output = %q, want it to name bob", out)
	}

	if _, err := runLode(t, "approval", "request", "rev-spec"); err != nil {
		t.Fatalf("approval request: %v", err)
	}
	out, err = runLode(t, "doc", "reviewers", "rev-spec")
	if err != nil {
		t.Fatalf("doc reviewers (show after request): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "still owed") || !strings.Contains(out, "bob") {
		t.Errorf("doc reviewers output = %q, want it to name bob as still owed", out)
	}
}
