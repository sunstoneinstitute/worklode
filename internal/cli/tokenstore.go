// tokenstore.go stores the wl_ bearer token in the OS keychain (macOS Keychain,
// Linux Secret Service, Windows Credential Manager) instead of cleartext on
// disk. Tokens are keyed by server URL so one machine can hold tokens for
// several worklode servers. On a machine with no keychain at all the token
// falls back to a 0600 file (tokenfile.go) — see spec 001 §8.5.
package cli

import (
	"errors"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
)

// keychainService is the keychain "service" all worklode tokens live under.
const keychainService = "worklode"

// probeAccount is the account name keychainAvailable looks up. Accounts are
// server URLs, and no URL contains spaces, so this can never collide with a
// real entry.
const probeAccount = "worklode availability probe (not a server url)"

// noKeychainBackend reports whether err means this machine has no keychain to
// talk to, as opposed to a keychain that is present and refused.
//
// The distinction is the whole point: a keychain that exists and says no —
// locked collection, denied prompt — must surface as an error, because writing
// the token to disk instead would quietly downgrade a working secret store.
// Only genuine absence earns the file fallback.
func noKeychainBackend(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return false // a backend answered, it just holds nothing
	}
	if errors.Is(err, keyring.ErrUnsupportedPlatform) {
		return true // go-keyring has no provider for this GOOS
	}
	// A dbus.Error means the session bus carried the call and something
	// replied. Only the two "nobody is serving that name" replies mean there is
	// no Secret Service; everything else is the Secret Service saying no.
	var derr dbus.Error
	var perr *dbus.Error
	name := ""
	switch {
	case errors.As(err, &derr):
		name = derr.Name
	case errors.As(err, &perr):
		name = perr.Name
	default:
		// Not a dbus reply at all: dbus.SessionBus() failed before any call
		// could be made, so there is no bus to find a Secret Service on.
		return true
	}
	switch name {
	case "org.freedesktop.DBus.Error.ServiceUnknown",
		"org.freedesktop.DBus.Error.NameHasNoOwner":
		return true
	}
	return false
}

// keychainAvailable reports whether an OS keychain exists on this machine. It
// asks for an account that cannot exist: a backend that is present answers
// ErrNotFound even when it holds nothing, so anything noKeychainBackend
// recognises as absence means there is none.
func keychainAvailable() bool {
	_, err := keyring.Get(keychainService, probeAccount)
	return !noKeychainBackend(err)
}

// ErrTokenNotFound is returned by Get/Delete when no token exists for a server.
var ErrTokenNotFound = keyring.ErrNotFound

// TokenStore reads and writes the bearer token for a given server URL.
type TokenStore interface {
	Get(server string) (string, error)
	Set(server, token string) error
	Delete(server string) error
}

// KeychainTokenStore is the production TokenStore backed by the OS keychain.
type KeychainTokenStore struct{}

func NewKeychainTokenStore() KeychainTokenStore { return KeychainTokenStore{} }

func (KeychainTokenStore) Get(server string) (string, error) {
	return keyring.Get(keychainService, server)
}

func (KeychainTokenStore) Set(server, token string) error {
	return keyring.Set(keychainService, server, token)
}

func (KeychainTokenStore) Delete(server string) error {
	return keyring.Delete(keychainService, server)
}

// FallbackTokenStore is the production TokenStore: the OS keychain where one
// exists, a 0600 file where none does. The probe runs at most once per process
// — it costs a D-Bus round trip, and LoadConfig reads a token on nearly every
// command.
type FallbackTokenStore struct {
	keychain TokenStore
	file     TokenStore

	// probe reports whether a keychain exists; replaced in tests.
	probe     func() bool
	probeOnce sync.Once
	haveKeys  bool
}

// NewFallbackTokenStore returns the default production store.
func NewFallbackTokenStore() *FallbackTokenStore {
	return &FallbackTokenStore{
		keychain: NewKeychainTokenStore(),
		file:     NewFileTokenStore(),
		probe:    keychainAvailable,
	}
}

func (f *FallbackTokenStore) keychainUsable() bool {
	f.probeOnce.Do(func() { f.haveKeys = f.probe() })
	return f.haveKeys
}

// Get prefers the keychain and falls back to the file, so a token written
// before a keychain appeared on the machine still resolves afterwards.
func (f *FallbackTokenStore) Get(server string) (string, error) {
	if f.keychainUsable() {
		token, err := f.keychain.Get(server)
		if err == nil {
			return token, nil
		}
		if !errors.Is(err, ErrTokenNotFound) {
			return "", err
		}
	}
	return f.file.Get(server)
}

// Set writes to the keychain when there is one. A keychain that exists and
// fails is returned as an error rather than retried on disk (spec 001 §8.5).
func (f *FallbackTokenStore) Set(server, token string) error {
	if f.keychainUsable() {
		return f.keychain.Set(server, token)
	}
	return f.file.Set(server, token)
}

// Delete clears both stores, so `lode logout` cannot leave a token on disk
// just because a keychain has appeared since it was written.
func (f *FallbackTokenStore) Delete(server string) error {
	found := false
	if f.keychainUsable() {
		switch err := f.keychain.Delete(server); {
		case err == nil:
			found = true
		case !errors.Is(err, ErrTokenNotFound):
			return err
		}
	}
	switch err := f.file.Delete(server); {
	case err == nil:
		found = true
	case !errors.Is(err, ErrTokenNotFound):
		return err
	}
	if !found {
		return ErrTokenNotFound
	}
	return nil
}

// FileFallback reports the path Set writes to, and true, when this machine has
// no keychain. A machine with one gets ("", false).
func (f *FallbackTokenStore) FileFallback() (string, bool) {
	if f.keychainUsable() {
		return "", false
	}
	path, err := tokenFilePath()
	if err != nil {
		return "", false
	}
	return path, true
}

// DeleteToken removes the stored token for a server from every store the CLI
// writes to. `lode logout` is its only caller.
func DeleteToken(server string) error { return tokenStore.Delete(server) }

// TokenFileFallback reports where the CLI is about to write a token in
// cleartext, so `lode login` can say so out loud. It returns ("", false)
// whenever a keychain is in use — including for any store a test has swapped
// in, which is never the disk.
func TokenFileFallback() (string, bool) {
	if f, ok := tokenStore.(interface{ FileFallback() (string, bool) }); ok {
		return f.FileFallback()
	}
	return "", false
}
