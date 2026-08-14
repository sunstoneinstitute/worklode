package cli

import (
	"os"
	"path/filepath"
)

func WriteRawConfigForTest(data string) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), 0o600)
}

func ReadRawConfigForTest() (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	return string(b), err
}

// LoadConfigFromForTest is LoadConfig with an explicit starting directory for
// the repo-local config walk, so tests need not chdir.
func LoadConfigFromForTest(startDir string) (Config, error) {
	return loadConfigFrom(startDir)
}

// SwapTokenStoreForTest replaces the package-level token store and returns a
// function that restores the original (pass to t.Cleanup).
func SwapTokenStoreForTest(ts TokenStore) func() {
	prev := tokenStore
	tokenStore = ts
	return func() { tokenStore = prev }
}

// NoKeychainBackendForTest exposes the availability classifier, which is the
// one piece of the fallback that cannot be exercised through a real keychain:
// CI has no Secret Service, and a developer machine has one.
func NoKeychainBackendForTest(err error) bool { return noKeychainBackend(err) }

// NewFallbackTokenStoreForTest builds a fallback store over explicit halves
// with the keychain probe already decided, so tests need neither a keychain
// nor a D-Bus session.
func NewFallbackTokenStoreForTest(keychain, file TokenStore, keychainPresent bool) *FallbackTokenStore {
	return &FallbackTokenStore{
		keychain: keychain,
		file:     file,
		probe:    func() bool { return keychainPresent },
	}
}
