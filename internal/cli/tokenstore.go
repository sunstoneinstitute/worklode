// tokenstore.go stores the wl_ bearer token in the OS keychain (macOS Keychain,
// Linux Secret Service, Windows Credential Manager) instead of cleartext on
// disk. Tokens are keyed by server URL so one machine can hold tokens for
// several worklode servers.
package cli

import "github.com/zalando/go-keyring"

// keychainService is the keychain "service" all wl tokens live under.
const keychainService = "worklode"

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
