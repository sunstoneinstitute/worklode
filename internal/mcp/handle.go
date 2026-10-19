package mcp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"regexp"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/refkind"
)

// A project handle is `<KEY>.<token>`: token is base64url (no padding) of
// 4 bytes of expiry (unix hours) and the first 8 bytes of an HMAC-SHA256 over
// actor, key and expiry. The actor is only inside the MAC, so a handle signed
// for another user fails like a tampered one. Nothing is stored (WL-REQ-1828).

// Handle failure reasons. Every one is answered as confirm_project.
const (
	handleBareKey   = "bare_key"
	handleWrongUser = "wrong_user"
	handleExpired   = "expired"
	handleMalformed = "malformed"
)

var (
	bareKeyRe = regexp.MustCompile(`^` + refkind.KeyPattern + `$`)
	fullRefRe = regexp.MustCompile(`^(` + refkind.KeyPattern + `)-(?:(?:SPEC|PLAN|REQ|RULE)-)?\d+$`)
)

func handleMAC(secret []byte, actor, key string, hours uint32) []byte {
	m := hmac.New(sha256.New, secret)
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], hours)
	m.Write([]byte(actor + "\x00" + key + "\x00"))
	m.Write(h[:])
	return m.Sum(nil)[:8]
}

// signHandle expires at exp rounded up to the next whole hour.
func signHandle(secret, actor, key string, exp time.Time) string {
	hours := uint32((exp.Unix() + 3599) / 3600)
	raw := make([]byte, 4, 12)
	binary.BigEndian.PutUint32(raw, hours)
	raw = append(raw, handleMAC([]byte(secret), actor, key, hours)...)
	return key + "." + base64.RawURLEncoding.EncodeToString(raw)
}

// verifyHandle returns the project key and "" when the handle is valid for
// actor at now, else "" and the reason. A handle is expired from its expiry
// instant on, and expiry is checked only after the MAC verifies.
func verifyHandle(secret, actor, handle string, now time.Time) (key string, reason string) {
	key, tok, ok := strings.Cut(handle, ".")
	if !ok {
		if bareKeyRe.MatchString(handle) {
			return "", handleBareKey
		}
		return "", handleMalformed
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw) != 12 || !bareKeyRe.MatchString(key) {
		return "", handleMalformed
	}
	hours := binary.BigEndian.Uint32(raw[:4])
	if !hmac.Equal(raw[4:], handleMAC([]byte(secret), actor, key, hours)) {
		return "", handleWrongUser
	}
	if now.Unix() >= int64(hours)*3600 {
		return "", handleExpired
	}
	return key, ""
}

// fullRef returns the project key of a full ref: WL-<n>, WL-SPEC-<n>,
// WL-PLAN-<n>, WL-REQ-<n> or WL-RULE-<n>.
func fullRef(s string) (projectKey string, ok bool) {
	m := fullRefRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1], true
}
