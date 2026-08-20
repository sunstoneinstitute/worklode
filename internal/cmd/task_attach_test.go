package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// blobSrv records what the CLI sent: uploads, task patches, and attaches.
type blobSrv struct {
	mu       sync.Mutex
	uploads  [][]byte
	patched  []string // bodies sent to PATCH /api/v1/tasks/{id}
	attached []string // filenames sent to POST /api/v1/tasks/{id}/blobs
	created  []string // bodies sent to POST /api/v1/tasks
}

// startBlobSrv wires a fake server. Each upload gets a synthetic hash keyed
// on call order, so assertions do not have to compute SHA-256.
func startBlobSrv(t *testing.T, mediaFor func(body []byte) string) *blobSrv {
	t.Helper()
	s := &blobSrv{}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/blobs", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.uploads = append(s.uploads, b)
		n := len(s.uploads)
		s.mu.Unlock()
		hash := strings.Repeat(string(rune('a'+n-1)), 64)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"hash": hash, "media_type": mediaFor(b),
			"size": len(b), "url": "/blob/" + hash,
		})
	})
	mux.HandleFunc("GET /api/v1/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": r.PathValue("id"), "title": "T", "body": "existing body",
			"project": "p", "priority": "medium", "kind": "bug", "state": "ready",
		})
	})
	mux.HandleFunc("PATCH /api/v1/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Body *string `json:"body"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		if req.Body != nil {
			s.patched = append(s.patched, *req.Body)
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"WL-1"}`)
	})
	mux.HandleFunc("POST /api/v1/tasks/{id}/blobs", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Filename string `json:"filename"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.attached = append(s.attached, req.Filename)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"attached"}`)
	})
	mux.HandleFunc("POST /api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Body string `json:"body"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.created = append(s.created, req.Body)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":"WL-1"}`)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
	return s
}

// writeFile creates a file in a temp dir and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestAttachEmbedsImagesOnly: an image is appended to the body as markdown;
// a log file is attached only. This is the embedded/attached split.
func TestAttachEmbedsImagesOnly(t *testing.T) {
	dir := t.TempDir()
	png := writeFile(t, dir, "shot.png", "\x89PNG\r\n\x1a\n fake")
	log := writeFile(t, dir, "crash.log", "boom\n")

	srv := startBlobSrv(t, func(b []byte) string {
		if strings.HasPrefix(string(b), "\x89PNG") {
			return "image/png"
		}
		return "text/plain; charset=utf-8"
	})

	out, err := runLode(t, "task", "attach", "WL-1", png, log)
	if err != nil {
		t.Fatalf("attach: %v\n%s", err, out)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.uploads) != 2 {
		t.Fatalf("uploads = %d, want 2", len(srv.uploads))
	}
	if len(srv.patched) != 1 {
		t.Fatalf("patches = %d, want 1 (only the image edits the body)", len(srv.patched))
	}
	body := srv.patched[0]
	if !strings.Contains(body, "![shot.png](/blob/"+strings.Repeat("a", 64)+")") {
		t.Fatalf("body missing the image reference:\n%s", body)
	}
	if strings.Contains(body, "crash.log") {
		t.Fatalf("non-embeddable file leaked into the body:\n%s", body)
	}
	if !strings.HasPrefix(body, "existing body") {
		t.Fatalf("existing body not preserved:\n%s", body)
	}
	if len(srv.attached) != 1 || srv.attached[0] != "crash.log" {
		t.Fatalf("attached = %v, want [crash.log]", srv.attached)
	}
}

// TestAttachNoEmbed: --no-embed attaches an image without touching the body.
func TestAttachNoEmbed(t *testing.T) {
	dir := t.TempDir()
	png := writeFile(t, dir, "shot.png", "\x89PNG\r\n\x1a\n fake")
	srv := startBlobSrv(t, func([]byte) string { return "image/png" })

	if out, err := runLode(t, "task", "attach", "--no-embed", "WL-1", png); err != nil {
		t.Fatalf("attach: %v\n%s", err, out)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.patched) != 0 {
		t.Fatalf("--no-embed edited the body: %v", srv.patched)
	}
	if len(srv.attached) != 1 || srv.attached[0] != "shot.png" {
		t.Fatalf("attached = %v, want [shot.png]", srv.attached)
	}
}
