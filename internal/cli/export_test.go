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
