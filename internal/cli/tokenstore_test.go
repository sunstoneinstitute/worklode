package cli_test

import (
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/work-tracker/internal/cli"
)

func TestKeychainTokenStore(t *testing.T) {
	keyring.MockInit() // in-memory backend; no real keychain touched

	ts := cli.NewKeychainTokenStore()
	const server = "https://wl.example.com"

	if _, err := ts.Get(server); err == nil {
		t.Fatal("expected miss before set")
	}
	if err := ts.Set(server, "wt_abc"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := ts.Get(server)
	if err != nil || got != "wt_abc" {
		t.Fatalf("get = %q,%v; want wt_abc,nil", got, err)
	}
	if err := ts.Delete(server); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := ts.Get(server); err == nil {
		t.Fatal("expected miss after delete")
	}
}
