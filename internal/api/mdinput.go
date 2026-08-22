// The MarkdownInput component's two endpoints (WL-299). POST /preview
// renders a draft through the same mdrender pipeline every stored body goes
// through, so the preview can never disagree with the page that renders the
// saved text. POST /dictate proxies recorded audio to the configured
// speech-to-text provider (ElevenLabs Scribe) — a proxy because the API key
// must never reach the browser, and the browser could not reach the vendor
// anyway under the cockpit's default-src 'self' CSP.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/mdrender"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// maxDictationAudio caps one dictation clip. Scribe accepts far larger
// files, but a cockpit dictation is a spoken paragraph, and this endpoint
// buffers the clip to build the multipart request.
const maxDictationAudio = 15 << 20

// dictationTimeout bounds the whole proxy call. Transcription latency grows
// with clip length; a minute-long clip transcribes well inside this.
const dictationTimeout = 60 * time.Second

// previewMarkdown handles POST /preview: the draft in the "body" form field
// comes back as the sanitized HTML fragment mdrender produces for stored
// task bodies. Same-origin only, like every state-shaped web POST — the
// render is cheap but not free, and the fragment is only meant for the
// cockpit's own component.
func (s *server) previewMarkdown(w http.ResponseWriter, r *http.Request) {
	if !s.sameOriginForm(r) {
		writeErr(w, http.StatusForbidden, "cross-origin form submission rejected")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWebForm)
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed form body")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, string(mdrender.Body(r.PostFormValue("body"))))
}

// dictate handles POST /dictate: the request body is one recorded audio clip
// (the Content-Type is the recorder's mime type), the response is
// {"text": "<transcription>"}. 503 when no provider is configured — the
// forms render no microphone then, so an actual 503 means a stale page or a
// hand-built request.
func (s *server) dictate(w http.ResponseWriter, r *http.Request) {
	if !s.sameOriginForm(r) {
		writeErr(w, http.StatusForbidden, "cross-origin form submission rejected")
		return
	}
	if s.cfg.SpeechToTextAPIKey == "" {
		s.observeDictation("unconfigured")
		writeErr(w, http.StatusServiceUnavailable, "no speech-to-text provider configured")
		return
	}
	audio, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDictationAudio))
	if err != nil {
		s.observeDictation("too_large")
		writeErr(w, http.StatusRequestEntityTooLarge, "audio clip too large")
		return
	}
	if len(audio) == 0 {
		s.observeDictation("bad_request")
		writeErr(w, http.StatusBadRequest, "empty audio clip")
		return
	}

	text, err := s.transcribe(r, audio)
	if err != nil {
		s.observeDictation("provider_error")
		s.log.Warn("dictation transcription failed", "err", err)
		writeErr(w, http.StatusBadGateway, "transcription failed")
		return
	}
	s.observeDictation("ok")
	writeJSON(w, http.StatusOK, model.DictationResult{Text: text})
}

// transcribe sends one audio clip to ElevenLabs' speech-to-text endpoint
// and returns the transcription. The vendor contract: multipart form with
// the clip in "file" and a "model_id", authenticated by the xi-api-key
// header, answering {"text": ...}.
func (s *server) transcribe(r *http.Request, audio []byte) (string, error) {
	base := strings.TrimSuffix(s.cfg.SpeechToTextURL, "/")
	if base == "" {
		base = "https://api.elevenlabs.io"
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("model_id", "scribe_v1"); err != nil {
		return "", fmt.Errorf("build transcription request: %w", err)
	}
	part, err := mw.CreateFormFile("file", "dictation")
	if err != nil {
		return "", fmt.Errorf("build transcription request: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return "", fmt.Errorf("build transcription request: %w", err)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("build transcription request: %w", err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), dictationTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/speech-to-text", &buf)
	if err != nil {
		return "", fmt.Errorf("build transcription request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("xi-api-key", s.cfg.SpeechToTextAPIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("speech-to-text call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("speech-to-text answered %d: %s", resp.StatusCode, snippet)
	}
	var out model.DictationResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("decode transcription: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}

// dictationEnabled reports whether the dictation button should render:
// exactly whether a speech-to-text provider is configured.
func (s *server) dictationEnabled() bool { return s.cfg.SpeechToTextAPIKey != "" }
