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
