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

	"github.com/sunstoneinstitute/worklode/internal/mdrender"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// posterHash is what the fake server hands back as a video's extracted first
// frame. Distinct from the letter-keyed upload hashes so a body citing it
// cannot be citing the video by accident.
var posterHash = strings.Repeat("9", 64)

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
		media := mediaFor(b)
		resp := map[string]any{
			"hash": hash, "media_type": media,
			"size": len(b), "url": "/blob/" + hash,
		}
		// What a server with ffmpeg answers a video upload with: the first
		// frame, stored as a blob of its own (spec 021 §5).
		if strings.HasPrefix(media, "video/") {
			resp["poster_url"] = "/blob/" + posterHash
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
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

// TestAttachAlt: --alt supplies the embedded image's alt text instead of the
// filename default (spec 021 Q021.1).
func TestAttachAlt(t *testing.T) {
	dir := t.TempDir()
	png := writeFile(t, dir, "shot.png", "\x89PNG\r\n\x1a\n fake")
	srv := startBlobSrv(t, func([]byte) string { return "image/png" })

	out, err := runLode(t, "task", "attach", "--alt", "map flashes narrow at 390px", "WL-1", png)
	if err != nil {
		t.Fatalf("attach: %v\n%s", err, out)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.patched) != 1 {
		t.Fatalf("patches = %d, want 1", len(srv.patched))
	}
	body := srv.patched[0]
	if !strings.Contains(body, "![map flashes narrow at 390px](/blob/"+strings.Repeat("a", 64)+")") {
		t.Fatalf("body missing the --alt text:\n%s", body)
	}
	if strings.Contains(body, "shot.png") {
		t.Fatalf("filename leaked into alt text despite --alt:\n%s", body)
	}
}

// TestAttachAltRejectsMultipleImages: --alt names one image's alt text, so
// attaching two embeddable images with one --alt is ambiguous and refused
// rather than silently reusing the same alt text for both.
func TestAttachAltRejectsMultipleImages(t *testing.T) {
	dir := t.TempDir()
	png1 := writeFile(t, dir, "shot1.png", "\x89PNG\r\n\x1a\n one")
	png2 := writeFile(t, dir, "shot2.png", "\x89PNG\r\n\x1a\n two")
	srv := startBlobSrv(t, func([]byte) string { return "image/png" })

	_, err := runLode(t, "task", "attach", "--alt", "one alt for both?", "WL-1", png1, png2)
	if err == nil {
		t.Fatalf("attach with --alt and two images: want an error")
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.patched) != 0 {
		t.Fatalf("body was patched despite the rejected --alt reuse: %v", srv.patched)
	}
}

// TestAttachEmbedsVideoWithPoster: a video embeds as <video>, not as an
// image, and carries the poster frame the upload extracted — without it the
// element is a black rectangle until someone presses play (spec 021 Q021.2).
func TestAttachEmbedsVideoWithPoster(t *testing.T) {
	dir := t.TempDir()
	mp4 := writeFile(t, dir, "flash.mp4", "fake video bytes")
	srv := startBlobSrv(t, func([]byte) string { return "video/mp4" })

	if out, err := runLode(t, "task", "attach", "WL-1", mp4); err != nil {
		t.Fatalf("attach: %v\n%s", err, out)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.patched) != 1 {
		t.Fatalf("patches = %d, want 1", len(srv.patched))
	}
	body := srv.patched[0]
	want := `<video src="/blob/` + strings.Repeat("a", 64) + `" poster="/blob/` + posterHash + `" controls preload="metadata"></video>`
	if !strings.Contains(body, want) {
		t.Fatalf("body:\n%s\nwant it to contain:\n%s", body, want)
	}
	if strings.Contains(body, "![") {
		t.Fatalf("video embedded as an image, which renders as a broken image:\n%s", body)
	}
}

// TestEmbedMarkupWithoutPoster: an instance whose image has no ffmpeg answers
// with no poster, and that must still produce a playable element rather than
// a poster="" the sanitiser drops and a reader is left wondering about.
func TestEmbedMarkupWithoutPoster(t *testing.T) {
	hash := strings.Repeat("b", 64)
	got := embedMarkup("flash.webm", model.BlobResponse{
		Hash: hash, MediaType: "video/webm", URL: "/blob/" + hash,
	})
	want := `<video src="/blob/` + hash + `" controls preload="metadata"></video>`
	if got != want {
		t.Fatalf("embedMarkup = %q, want %q", got, want)
	}
}

// TestEmbedMarkupSurvivesTheSanitiser ties the two halves together: what
// attach writes into a body is only useful if what renders bodies keeps it.
// The allowlist in internal/mdrender is deliberately narrow — a stray
// attribute, or a src the /blob/<hash> pattern does not match, is dropped in
// silence — so the element is asserted after a render, not before.
func TestEmbedMarkupSurvivesTheSanitiser(t *testing.T) {
	video := strings.Repeat("a", 64)
	got := string(mdrender.Body(mdrender.ProjectKeys{}, "a body\n\n"+embedMarkup("flash.mp4", model.BlobResponse{
		Hash: video, MediaType: "video/mp4", URL: "/blob/" + video,
		PosterURL: "/blob/" + posterHash,
	})+"\n"))
	for _, want := range []string{
		`<video`, `src="/blob/` + video + `"`, `poster="/blob/` + posterHash + `"`,
		`controls`, `preload="metadata"`, `</video>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered body lost %s:\n%s", want, got)
		}
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
