package cmd

import (
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/work-tracker/internal/cli"
)

func TestLogoutClearsKeychain(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("WT_TOKEN", "")
	t.Setenv("WT_SERVER", "https://wt.example.com")

	if err := cli.NewKeychainTokenStore().Set("https://wt.example.com", "wt_x"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := runLogout("https://wt.example.com"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := cli.NewKeychainTokenStore().Get("https://wt.example.com"); err == nil {
		t.Fatal("token should be gone after logout")
	}
}
