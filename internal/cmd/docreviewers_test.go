package cmd

import (
	"context"
	"strings"
	"testing"
)

// TestDocSetReviewersAndShow covers `lode doc set reviewers <actor…> <ref>`
// and its read side `lode doc reviewers <ref>` (025 §7.3, WL-359, WL-487):
// the set command assigns the durable set, the view prints it back plus
// whoever still owes a review after `lode approval request`, and the
// removed `--set` flag on the view is now an error.
func TestDocSetReviewersAndShow(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	if err := st.CreateActor(context.Background(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	specFile := writeDocFile(t, docTestBody)
	if _, err := runLode(t, "doc", "add", "--project", "proj", "--kind", "spec",
		"--slug", "rev-spec", "--file", specFile); err != nil {
		t.Fatalf("doc add: %v", err)
	}

	out, err := runLode(t, "doc", "set", "reviewers", "bob", "rev-spec")
	if err != nil {
		t.Fatalf("doc set reviewers: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "bob") {
		t.Fatalf("doc set reviewers output = %q, want it to report bob", out)
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

	// The view no longer writes: --set is gone, not silently accepted.
	if _, err := runLode(t, "doc", "reviewers", "rev-spec", "--set", "bob"); err == nil {
		t.Fatal("lode doc reviewers --set: want an unknown-flag error, got nil")
	}

	// Naming no actors at all is the clear.
	if _, err := runLode(t, "doc", "set", "reviewers", "rev-spec"); err != nil {
		t.Fatalf("clear reviewers: %v", err)
	}
	out, err = runLode(t, "doc", "reviewers", "rev-spec")
	if err != nil {
		t.Fatalf("doc reviewers (show after clear): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "none assigned") {
		t.Errorf("doc reviewers output after clear = %q, want it to report none assigned", out)
	}

	// An unknown field is refused before any round trip.
	if _, err := runLode(t, "doc", "set", "owner", "bob", "rev-spec"); err == nil {
		t.Fatal("lode doc set owner: want an unknown-field error, got nil")
	}
}
