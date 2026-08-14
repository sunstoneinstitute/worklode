package cli_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func TestFileTokenStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worklode", "token")
	ts := cli.NewFileTokenStoreAt(path)

	if _, err := ts.Get("https://a.example"); !errors.Is(err, cli.ErrTokenNotFound) {
		t.Fatalf("get before set = %v; want ErrTokenNotFound", err)
	}
	if err := ts.Set("https://a.example", "wl_aaa"); err != nil {
		t.Fatalf("set a: %v", err)
	}
	if err := ts.Set("https://b.example", "wl_bbb"); err != nil {
		t.Fatalf("set b: %v", err)
	}
	for server, want := range map[string]string{"https://a.example": "wl_aaa", "https://b.example": "wl_bbb"} {
		got, err := ts.Get(server)
		if err != nil || got != want {
			t.Fatalf("get %s = %q,%v; want %q,nil", server, got, err, want)
		}
	}

	// Deleting one server leaves the other's token alone — the whole reason the
	// file is keyed by server rather than holding a single token.
	if err := ts.Delete("https://a.example"); err != nil {
		t.Fatalf("delete a: %v", err)
	}
	if _, err := ts.Get("https://a.example"); !errors.Is(err, cli.ErrTokenNotFound) {
		t.Fatalf("get a after delete = %v; want ErrTokenNotFound", err)
	}
	if got, err := ts.Get("https://b.example"); err != nil || got != "wl_bbb" {
		t.Fatalf("get b after deleting a = %q,%v; want wl_bbb,nil", got, err)
	}
	if err := ts.Delete("https://a.example"); !errors.Is(err, cli.ErrTokenNotFound) {
		t.Fatalf("second delete = %v; want ErrTokenNotFound", err)
	}

	// The last token out removes the file rather than leaving an empty one.
	if err := ts.Delete("https://b.example"); err != nil {
		t.Fatalf("delete b: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat after last delete = %v; want not-exist", err)
	}
}

func TestFileTokenStorePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worklode", "token")
	ts := cli.NewFileTokenStoreAt(path)
	if err := ts.Set("https://a.example", "wl_aaa"); err != nil {
		t.Fatalf("set: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("token file mode = %04o; want 0600", got)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat token dir: %v", err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("token dir mode = %04o; want 0700", got)
	}
}

// A pre-existing world-readable file must come out 0600, not be written
// through: the rename replaces the inode, so the mode comes with it.
func TestFileTokenStoreTightensLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("https://a.example wl_old\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ts := cli.NewFileTokenStoreAt(path)
	if err := ts.Set("https://a.example", "wl_new"); err != nil {
		t.Fatalf("set: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode after rewrite = %04o; want 0600", got)
	}
	if got, err := ts.Get("https://a.example"); err != nil || got != "wl_new" {
		t.Fatalf("get = %q,%v; want wl_new,nil", got, err)
	}
}

func TestFileTokenStoreRejectsWhitespace(t *testing.T) {
	ts := cli.NewFileTokenStoreAt(filepath.Join(t.TempDir(), "token"))
	if err := ts.Set("https://a.example", "wl_a wl_b"); err == nil {
		t.Error("set with a space in the token: want error")
	}
	if err := ts.Set("https://a.example", "wl_a\nhttps://evil.example wl_b"); err == nil {
		t.Error("set with a newline in the token: want error")
	}
	if err := ts.Set("", "wl_a"); err == nil {
		t.Error("set with an empty server: want error")
	}
}

// TestNoKeychainBackend pins the line between "this machine has no keychain"
// and "the keychain is there and said no" — the distinction the whole fallback
// rests on (spec 001 §8.5).
func TestNoKeychainBackend(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"empty keychain", keyring.ErrNotFound, false},
		{"wrapped empty keychain", fmt.Errorf("get: %w", keyring.ErrNotFound), false},
		{"unsupported platform", keyring.ErrUnsupportedPlatform, true},
		{"no session bus", errors.New("dbus: couldn't determine address of session bus"), true},
		{"nothing serves org.freedesktop.secrets",
			dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"}, true},
		{"name has no owner",
			&dbus.Error{Name: "org.freedesktop.DBus.Error.NameHasNoOwner"}, true},
		{"wrapped service unknown",
			fmt.Errorf("set: %w", dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"}), true},
		{"secret service refused",
			dbus.Error{Name: "org.freedesktop.Secret.Error.IsLocked"}, false},
		{"prompt dismissed",
			dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cli.NoKeychainBackendForTest(tc.err); got != tc.want {
				t.Errorf("noKeychainBackend(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}

// stubKeychain is a TokenStore that fails every write with a fixed error, for
// the "keychain exists and refuses" case.
type stubKeychain struct {
	err   error
	saved map[string]string
}

func (s *stubKeychain) Get(server string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	tok, ok := s.saved[server]
	if !ok {
		return "", cli.ErrTokenNotFound
	}
	return tok, nil
}

func (s *stubKeychain) Set(server, token string) error {
	if s.err != nil {
		return s.err
	}
	if s.saved == nil {
		s.saved = map[string]string{}
	}
	s.saved[server] = token
	return nil
}

func (s *stubKeychain) Delete(server string) error {
	if s.err != nil {
		return s.err
	}
	if _, ok := s.saved[server]; !ok {
		return cli.ErrTokenNotFound
	}
	delete(s.saved, server)
	return nil
}

func TestFallbackWritesFileWhenNoKeychain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	keychain := &stubKeychain{err: keyring.ErrUnsupportedPlatform}
	ts := cli.NewFallbackTokenStoreForTest(keychain, cli.NewFileTokenStoreAt(path), false)

	if err := ts.Set("https://a.example", "wl_aaa"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("token file not written: %v", err)
	}
	if got, err := ts.Get("https://a.example"); err != nil || got != "wl_aaa" {
		t.Fatalf("get = %q,%v; want wl_aaa,nil", got, err)
	}
	if err := ts.Delete("https://a.example"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := ts.Get("https://a.example"); !errors.Is(err, cli.ErrTokenNotFound) {
		t.Fatalf("get after delete = %v; want ErrTokenNotFound", err)
	}
}

// The point of probing rather than catching: a keychain that exists and fails
// must surface the failure, not quietly write the token to disk instead.
func TestFallbackDoesNotWriteFileWhenKeychainFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	locked := dbus.Error{Name: "org.freedesktop.Secret.Error.IsLocked"}
	keychain := &stubKeychain{err: locked}
	ts := cli.NewFallbackTokenStoreForTest(keychain, cli.NewFileTokenStoreAt(path), true)

	err := ts.Set("https://a.example", "wl_aaa")
	if err == nil {
		t.Fatal("set with a locked keychain: want an error")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("token file exists after a failed keychain write (%v); want no file", statErr)
	}
}

// A token written before a keychain existed still resolves once one appears.
func TestFallbackReadsFileWhenKeychainIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := cli.NewFileTokenStoreAt(path).Set("https://a.example", "wl_from_file"); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	ts := cli.NewFallbackTokenStoreForTest(&stubKeychain{}, cli.NewFileTokenStoreAt(path), true)
	if got, err := ts.Get("https://a.example"); err != nil || got != "wl_from_file" {
		t.Fatalf("get = %q,%v; want wl_from_file,nil", got, err)
	}
}

// Logout must clear both stores, so a token cannot survive on disk just
// because a keychain appeared after it was written.
func TestFallbackDeleteClearsBothStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	file := cli.NewFileTokenStoreAt(path)
	if err := file.Set("https://a.example", "wl_from_file"); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	keychain := &stubKeychain{saved: map[string]string{"https://a.example": "wl_from_keychain"}}
	ts := cli.NewFallbackTokenStoreForTest(keychain, file, true)

	if err := ts.Delete("https://a.example"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := file.Get("https://a.example"); !errors.Is(err, cli.ErrTokenNotFound) {
		t.Fatalf("file still holds a token after delete (%v)", err)
	}
	if _, err := keychain.Get("https://a.example"); !errors.Is(err, cli.ErrTokenNotFound) {
		t.Fatalf("keychain still holds a token after delete (%v)", err)
	}
	if err := ts.Delete("https://a.example"); !errors.Is(err, cli.ErrTokenNotFound) {
		t.Fatalf("delete with nothing stored = %v; want ErrTokenNotFound", err)
	}
}

// The default store puts the file at ~/.config/worklode/token.
func TestFallbackFileIsUnderConfigWorklode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ts := cli.NewFallbackTokenStoreForTest(&stubKeychain{err: keyring.ErrUnsupportedPlatform}, cli.NewFileTokenStore(), false)
	if err := ts.Set("https://a.example", "wl_aaa"); err != nil {
		t.Fatalf("set: %v", err)
	}
	want := filepath.Join(home, ".config", "worklode", "token")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("stat %s: %v", want, err)
	}
}
