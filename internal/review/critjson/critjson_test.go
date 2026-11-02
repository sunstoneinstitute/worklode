package critjson_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/review/critjson"
)

// open returns a fixture captured at the tag PIN names.
func open(t *testing.T, name string) *os.File {
	t.Helper()
	pin, err := os.ReadFile("PIN")
	if err != nil {
		t.Fatal(err)
	}
	line, _, _ := strings.Cut(string(pin), "\n")
	tag, _, _ := strings.Cut(line, " ")
	f, err := os.Open(filepath.Join("testdata", tag, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func decode(t *testing.T, name string) critjson.Finish {
	t.Helper()
	dec := json.NewDecoder(open(t, name))
	dec.DisallowUnknownFields()
	var got critjson.Finish
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("%s no longer matches the pinned shape: %v", name, err)
	}
	return got
}

func TestCritContract(t *testing.T) {
	unresolved := decode(t, "finish_unresolved.json")
	if unresolved.Approved {
		t.Error("finish_unresolved: approved is true")
	}
	var line bool
	for _, c := range unresolved.CommentsJSON {
		if c.StartLine > 0 && c.Body != "" {
			line = true
		}
	}
	if !line {
		t.Error("finish_unresolved: no comment with a start line and a body")
	}

	approved := decode(t, "finish_approved.json")
	if !approved.Approved {
		t.Error("finish_approved: approved is false")
	}
	if n := len(approved.CommentsUnresolvedJSON); n != 0 || approved.UnresolvedCount != 0 {
		t.Errorf("finish_approved: %d unresolved comments, unresolved_count %d", n, approved.UnresolvedCount)
	}
}
