// tokenfile.go is the token store of last resort: a 0600 file under
// ~/.config/worklode for machines with no OS keychain at all. It is reached
// only through fallbackTokenStore, which probes for a keychain first — see
// tokenstore.go and spec 001 §8.5.
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// tokenFilePath returns ~/.config/worklode/token.
func tokenFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "worklode", "token"), nil
}

// FileTokenStore keeps tokens in a cleartext file, one `<server> <token>` line
// per server. It is keyed by server URL for the same reason the keychain is:
// LODE_SERVER can point a later command at a different server, and a token
// minted for one must not travel to another.
type FileTokenStore struct {
	// path overrides the default location; empty means ~/.config/worklode/token.
	path string
}

// NewFileTokenStore returns a store at the default path.
func NewFileTokenStore() *FileTokenStore { return &FileTokenStore{} }

// NewFileTokenStoreAt returns a store at an explicit path, for tests.
func NewFileTokenStoreAt(path string) *FileTokenStore { return &FileTokenStore{path: path} }

func (f *FileTokenStore) filePath() (string, error) {
	if f.path != "" {
		return f.path, nil
	}
	return tokenFilePath()
}

// load reads the file into a server → token map. A missing file is an empty
// map, not an error: "no token yet" and "no file yet" are the same state.
// Malformed lines are skipped rather than failing the read — a token for
// another server should still resolve.
func (f *FileTokenStore) load() (map[string]string, string, error) {
	path, err := f.filePath()
	if err != nil {
		return nil, "", err
	}
	tokens := map[string]string{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return tokens, path, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		server, token, ok := strings.Cut(line, " ")
		if !ok || server == "" || token == "" {
			continue
		}
		tokens[server] = token
	}
	return tokens, path, nil
}

// save writes the map back, or removes the file when nothing is left — an
// empty secret file is just litter. The write goes to a temp file in the same
// directory and is renamed into place: rename is atomic, so a crash cannot
// leave a truncated token, and the mode comes from the temp file, so an
// existing file with looser permissions is replaced by a 0600 one rather than
// being written through.
func (f *FileTokenStore) save(tokens map[string]string, path string) error {
	if len(tokens) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	servers := make([]string, 0, len(tokens))
	for server := range tokens {
		servers = append(servers, server)
	}
	sort.Strings(servers) // stable file across writes
	var b strings.Builder
	for _, server := range servers {
		fmt.Fprintf(&b, "%s %s\n", server, tokens[server])
	}
	tmp, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return fmt.Errorf("create temp token file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func (f *FileTokenStore) Get(server string) (string, error) {
	tokens, _, err := f.load()
	if err != nil {
		return "", err
	}
	token, ok := tokens[server]
	if !ok {
		return "", ErrTokenNotFound
	}
	return token, nil
}

func (f *FileTokenStore) Set(server, token string) error {
	if server == "" {
		return errors.New("token file: empty server URL")
	}
	// One line per server is the whole format; a value carrying a newline or a
	// space would silently corrupt the next read. wl_ tokens never do.
	if strings.ContainsAny(server, " \t\r\n") || strings.ContainsAny(token, " \t\r\n") {
		return errors.New("token file: server URL and token must not contain whitespace")
	}
	tokens, path, err := f.load()
	if err != nil {
		return err
	}
	tokens[server] = token
	return f.save(tokens, path)
}

func (f *FileTokenStore) Delete(server string) error {
	tokens, path, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := tokens[server]; !ok {
		return ErrTokenNotFound
	}
	delete(tokens, server)
	return f.save(tokens, path)
}
