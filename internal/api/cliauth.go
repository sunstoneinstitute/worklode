// cliauth.go implements the server-mediated CLI login flow: a discovery
// endpoint, a login-start endpoint that stamps a loopback redirect target into
// a signed cookie, and a token endpoint that redeems a one-time code for a wt_
// token. The one-time code is minted in finishLogin (shared by both web
// callbacks) once the actor is provisioned. See
// docs/plans/2026-07-20-provider-neutral-cli-login-design.md.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// cliCodeTTL bounds how long a one-time code is valid between the browser
// redirect and the CLI's token exchange.
const cliCodeTTL = 60 * time.Second

type cliCode struct {
	actorID string
	state   string
	expires time.Time
}

// cliCodeStore holds pending one-time codes in memory. The server is
// single-instance, so a restart simply drops pending 60s codes.
type cliCodeStore struct {
	mu    sync.Mutex
	codes map[string]cliCode
	now   func() time.Time
}

func newCLICodeStore(now func() time.Time) *cliCodeStore {
	return &cliCodeStore{codes: map[string]cliCode{}, now: now}
}

// mint stores a fresh single-use code bound to actorID and the CLI state.
func (s *cliCodeStore) mint(actorID, state string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[code] = cliCode{actorID: actorID, state: state, expires: s.now().Add(cliCodeTTL)}
	return code, nil
}

// redeem returns the bound actor id and consumes the code. It fails if the
// code is unknown, expired, or the state does not match. A state mismatch does
// NOT consume the code.
func (s *cliCodeStore) redeem(code, state string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.codes[code]
	if !ok || s.now().After(c.expires) {
		delete(s.codes, code)
		return "", false
	}
	if c.state != state {
		return "", false
	}
	delete(s.codes, code)
	return c.actorID, true
}
