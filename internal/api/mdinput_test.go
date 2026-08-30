// The MarkdownInput component's endpoints (WL-299): the preview fragment,
// the dictation proxy, and the forms' opt-in rendering of the microphone.

package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
)

// TestPreviewRendersSanitizedMarkdown pins that POST /preview is the same
// pipeline a stored body renders through: markdown becomes HTML, and markup
// the sanitizer strips from stored bodies is stripped here too.
func TestPreviewRendersSanitizedMarkdown(t *testing.T) {
	t.Parallel()
	_, h, _ := newTestServer(t)

	rr := doForm(t, h, "/preview",
		url.Values{"body": {"**bold** text\n\n<script>alert(1)</script>"}}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<strong>bold</strong>") {
		t.Errorf("preview did not render markdown: %q", body)
	}
	if strings.Contains(body, "<script") {
		t.Errorf("preview leaked a script tag: %q", body)
	}

	// Cross-origin is refused like every state-shaped web POST.
	rr = doForm(t, h, "/preview", url.Values{"body": {"x"}},
		map[string]string{"Sec-Fetch-Site": "cross-site"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-origin preview status = %d, want 403", rr.Code)
	}
}

// TestDictateUnconfigured pins the degraded posture: no provider key means
// no microphone on the forms and a 503 from the endpoint.
func TestDictateUnconfigured(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	req := httptest.NewRequest("POST", "/dictate", strings.NewReader("audio-bytes"))
	req.Header.Set("Content-Type", "audio/webm")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured dictate status = %d, want 503; body %s", rr.Code, rr.Body.String())
	}

	page := doReq(t, h, "GET", "/projects/proj/tasks/new", "", nil).Body.String()
	if strings.Contains(page, "data-md-dictate") {
		t.Errorf("unconfigured instance renders the dictation button")
	}
	if !strings.Contains(page, "data-mdinput") || !strings.Contains(page, "data-md-preview") {
		t.Errorf("task form is missing the MarkdownInput component:\n%s", page)
	}
}

// TestDictateProxiesToProvider drives the configured path against a fake
// ElevenLabs: the clip goes out as multipart with the model and key, the
// transcription comes back as JSON, and a provider failure is a 502.
func TestDictateProxiesToProvider(t *testing.T) {
	t.Parallel()
	var gotKey, gotModel string
	var fail bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/speech-to-text" {
			http.NotFound(w, r)
			return
		}
		gotKey = r.Header.Get("xi-api-key")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, "bad multipart", http.StatusBadRequest)
			return
		}
		gotModel = r.FormValue("model_id")
		if _, _, err := r.FormFile("file"); err != nil {
			http.Error(w, "no file", http.StatusBadRequest)
			return
		}
		if fail {
			http.Error(w, "upstream broken", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text": " transcribed words "}`))
	}))
	t.Cleanup(provider.Close)

	st, h, _ := newTestServerWithConfig(t, api.Config{
		WebOpen:            true,
		SpeechToTextAPIKey: "el-key",
		SpeechToTextURL:    provider.URL,
	})
	createProject(t, st, "proj")

	req := httptest.NewRequest("POST", "/dictate", strings.NewReader("fake-audio"))
	req.Header.Set("Content-Type", "audio/webm")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("dictate status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); !strings.Contains(got, `"text":"transcribed words"`) {
		t.Fatalf("dictate body = %q, want the trimmed transcription", got)
	}
	if gotKey != "el-key" || gotModel != "scribe_v1" {
		t.Fatalf("provider saw key %q model %q; want el-key / scribe_v1", gotKey, gotModel)
	}

	// The configured instance renders the microphone.
	page := doReq(t, h, "GET", "/projects/proj/tasks/new", "", nil).Body.String()
	if !strings.Contains(page, "data-md-dictate") {
		t.Errorf("configured instance renders no dictation button:\n%s", page)
	}

	// A provider failure is a 502, never a fabricated transcription.
	fail = true
	req = httptest.NewRequest("POST", "/dictate", strings.NewReader("fake-audio"))
	req.Header.Set("Content-Type", "audio/webm")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("provider-failure status = %d, want 502", rr.Code)
	}

	// An empty clip never reaches the provider.
	req = httptest.NewRequest("POST", "/dictate", strings.NewReader(""))
	req.Header.Set("Content-Type", "audio/webm")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty-clip status = %d, want 400", rr.Code)
	}
}

// TestDeliverableFormUsesMarkdownInput pins that the second form swapped its
// textarea for the component too.
func TestDeliverableFormUsesMarkdownInput(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	page := doReq(t, h, "GET", "/projects/proj/deliverables/new", "", nil).Body.String()
	if !strings.Contains(page, "data-mdinput") ||
		!strings.Contains(page, `id="description" name="description"`) {
		t.Errorf("deliverable form is missing the MarkdownInput component:\n%s", page)
	}
}
