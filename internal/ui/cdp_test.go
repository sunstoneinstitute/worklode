//go:build narrowcheck

package ui

// cdp_test.go is a minimal Chrome DevTools Protocol client for the
// narrow-width audit (narrowbrowser_test.go). It speaks CDP over Chrome's
// --remote-debugging-pipe rather than over a WebSocket, which is why it needs
// no dependency at all: the pipe carries the same JSON messages, NUL-delimited,
// on file descriptors 3 and 4. Nothing here is worth generalising — it does
// exactly the four things the audit needs (open a page, set a viewport, load a
// URL, evaluate a script) and reports every other CDP reply as an error.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// cdpMessage is one CDP frame in either direction: a command going out (ID,
// Method, Params, SessionID) or a reply/event coming back (ID, Result, Error).
type cdpMessage struct {
	ID        int             `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    any             `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

func (e *cdpError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("cdp: %s (%s)", e.Message, e.Data)
	}
	return "cdp: " + e.Message
}

// browser is a running headless Chrome plus the pipe to it. Commands are
// matched to replies by id; events (which carry no id) are discarded, since
// the audit polls document.readyState rather than subscribing to Page events.
type browser struct {
	cmd     *exec.Cmd
	in      *os.File
	out     *os.File
	mu      sync.Mutex
	nextID  int
	pending map[int]chan cdpMessage
	dead    chan struct{}
	deadErr error
}

// launchBrowser starts bin headless with a throwaway profile in profileDir and
// returns a client attached to its CDP pipe.
func launchBrowser(bin, profileDir string) (*browser, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin,
		"--headless=new",
		"--remote-debugging-pipe",
		"--user-data-dir="+profileDir,
		"--no-sandbox",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--disable-extensions",
		"--disable-background-networking",
		"--disable-sync",
		// Hidden scrollbars keep clientWidth equal to the emulated viewport, so
		// a reported page width is the page's, not the scrollbar's.
		"--hide-scrollbars",
		"--force-device-scale-factor=1",
		"about:blank",
	)
	cmd.ExtraFiles = []*os.File{inR, outW}
	// Chrome is chatty on stderr about D-Bus and GPU in a headless container;
	// none of it is a failure this audit can act on.
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	inR.Close()
	outW.Close()

	b := &browser{cmd: cmd, in: inW, out: outR, pending: map[int]chan cdpMessage{}, dead: make(chan struct{})}
	go b.readLoop()
	return b, nil
}

func (b *browser) readLoop() {
	r := bufio.NewReaderSize(b.out, 1<<20)
	for {
		raw, err := r.ReadBytes(0)
		if err != nil {
			b.mu.Lock()
			b.deadErr = fmt.Errorf("browser pipe closed: %w", err)
			for _, ch := range b.pending {
				close(ch)
			}
			b.pending = map[int]chan cdpMessage{}
			b.mu.Unlock()
			close(b.dead)
			return
		}
		var msg cdpMessage
		if err := json.Unmarshal(raw[:len(raw)-1], &msg); err != nil || msg.ID == 0 {
			continue // an event, or a frame this client does not model
		}
		b.mu.Lock()
		ch := b.pending[msg.ID]
		delete(b.pending, msg.ID)
		b.mu.Unlock()
		if ch != nil {
			ch <- msg
			close(ch)
		}
	}
}

// call sends one CDP command and waits for its reply. session is "" for a
// browser-level command and the flat session id for a page-level one.
func (b *browser) call(session, method string, params map[string]any) (json.RawMessage, error) {
	b.mu.Lock()
	if b.deadErr != nil {
		err := b.deadErr
		b.mu.Unlock()
		return nil, err
	}
	b.nextID++
	id := b.nextID
	ch := make(chan cdpMessage, 1)
	b.pending[id] = ch
	b.mu.Unlock()

	body, err := json.Marshal(cdpMessage{ID: id, Method: method, Params: params, SessionID: session})
	if err != nil {
		return nil, err
	}
	if _, err := b.in.Write(append(body, 0)); err != nil {
		return nil, fmt.Errorf("write %s: %w", method, err)
	}
	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("%s: %w", method, b.deadErr)
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("%s: %w", method, msg.Error)
		}
		return msg.Result, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("%s: timed out waiting for the browser", method)
	}
}

func (b *browser) close() {
	if b.cmd.Process != nil {
		_, _ = b.call("", "Browser.close", nil)
		select {
		case <-b.dead:
		case <-time.After(3 * time.Second):
			_ = b.cmd.Process.Kill()
		}
		_ = b.cmd.Wait()
	}
	b.in.Close()
	b.out.Close()
}

// newPage opens a page target and attaches to it, returning the flat session
// id every later page-level command carries.
func (b *browser) newPage() (string, error) {
	res, err := b.call("", "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		return "", err
	}
	var target struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(res, &target); err != nil {
		return "", err
	}
	res, err = b.call("", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true})
	if err != nil {
		return "", err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(res, &attached); err != nil {
		return "", err
	}
	return attached.SessionID, nil
}

// viewport emulates a width x height CSS-pixel viewport at scale 1. mobile is
// false so the layout viewport is exactly width — the pages carry a
// width=device-width meta tag, so mobile emulation would give the same layout
// anyway, and this way the number in a finding is the number that was asked for.
func (b *browser) viewport(session string, width, height int) error {
	_, err := b.call(session, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false,
	})
	return err
}

// load navigates to url and returns once the document has finished loading and
// its web fonts have settled — both change layout, and layout is the thing
// being measured.
func (b *browser) load(session, url string) error {
	if _, err := b.call(session, "Page.navigate", map[string]any{"url": url}); err != nil {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		state, err := b.evaluate(session, "document.readyState + ' ' + location.href", false)
		if err == nil && strings.HasPrefix(state, "complete ") && strings.HasSuffix(state, url) {
			_, _ = b.evaluate(session, "document.fonts.ready.then(function () { return 'ready' })", true)
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("load %s: page did not finish loading", url)
}

// evaluate runs expr in the page and returns its value as a string. It is the
// audit's only channel out of the browser, so a thrown exception is an error
// here rather than an empty result.
func (b *browser) evaluate(session, expr string, awaitPromise bool) (string, error) {
	res, err := b.call(session, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  awaitPromise,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text       string                        `json:"text"`
			Exception  *struct{ Description string } `json:"exception"`
			LineNumber int                           `json:"lineNumber"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	if out.Exception != nil {
		detail := out.Exception.Text
		if out.Exception.Exception != nil && out.Exception.Exception.Description != "" {
			detail = out.Exception.Exception.Description
		}
		return "", fmt.Errorf("evaluate: %s (line %d)", detail, out.Exception.LineNumber)
	}
	var s string
	if err := json.Unmarshal(out.Result.Value, &s); err != nil {
		return string(out.Result.Value), nil
	}
	return s, nil
}
