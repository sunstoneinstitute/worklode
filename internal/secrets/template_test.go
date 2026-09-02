package secrets

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPlaceholders(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"{{CLIENT_CERT}}", []string{"CLIENT_CERT"}},
		{"{{ CLIENT_CERT }}", []string{"CLIENT_CERT"}},
		{"{{\tCLIENT_CERT\n}}", []string{"CLIENT_CERT"}},
		{"a {{ A }} b {{ B }} c {{ A }}", []string{"A", "B"}}, // distinct, first-appearance order
		{"no placeholders here", nil},
	} {
		got, err := Placeholders(tc.text)
		if err != nil {
			t.Errorf("Placeholders(%q): %v", tc.text, err)
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("Placeholders(%q) = %v; want %v", tc.text, got, tc.want)
		}
	}
}

// TestPlaceholdersRejectsStrayBraces: a typo must fail catalog validation
// rather than render an artifact with a literal "{{ clientcert }}" in it.
func TestPlaceholdersRejectsStrayBraces(t *testing.T) {
	for _, text := range []string{
		"{{ unterminated",
		"{{ lowercase }}",
		"{{ }}",
		"{{ TWO WORDS }}",
		"{{ PATH }}", // loader-sensitive: the name grammar denies it
	} {
		if _, err := Placeholders(text); err == nil {
			t.Errorf("Placeholders(%q) = nil; want a rejection", text)
		}
	}
}

func TestValidateTemplate(t *testing.T) {
	e := Entry{
		Name:     "KUBECONFIG_HZDEV",
		Template: "kubeconfig-hzdev.yaml",
		Creds: []Cred{
			{Placeholder: "CLIENT_CERT", Ref: "op://v/i/cert"},
			{Placeholder: "CLIENT_KEY", Ref: "op://v/i/key"},
		},
	}
	if err := ValidateTemplate(e, "cert: {{ CLIENT_CERT }}\nkey: {{CLIENT_KEY}}\n"); err != nil {
		t.Fatalf("matching sets: %v", err)
	}
	// Undeclared placeholder: the error names the entry and the placeholder.
	err := ValidateTemplate(e, "cert: {{ CLIENT_CERT }}\nkey: {{ CLIENT_KEY }}\nca: {{ CA }}\n")
	if err == nil || !strings.Contains(err.Error(), "CA") || !strings.Contains(err.Error(), e.Name) {
		t.Fatalf("undeclared placeholder: %v; want an error naming CA and the entry", err)
	}
	// Unused cred: the other direction.
	err = ValidateTemplate(e, "cert: {{ CLIENT_CERT }}\n")
	if err == nil || !strings.Contains(err.Error(), "CLIENT_KEY") {
		t.Fatalf("unused cred: %v; want an error naming CLIENT_KEY", err)
	}
	// The text crosses two JSON round-trips, where Go's encoder would
	// silently replace invalid bytes and corrupt "verbatim".
	err = ValidateTemplate(e, "cert: {{ CLIENT_CERT }}\nkey: {{ CLIENT_KEY }}\xff\n")
	if err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid UTF-8: %v; want a rejection", err)
	}
}

func TestRender(t *testing.T) {
	got, err := Render("cert: {{ CLIENT_CERT }}\nkey: {{CLIENT_KEY}}\n",
		map[string]string{"CLIENT_CERT": "CERT", "CLIENT_KEY": "KEY"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "cert: CERT\nkey: KEY\n" {
		t.Fatalf("Render = %q", got)
	}
	if _, err := Render("{{ A }}", nil); err == nil {
		t.Fatal("Render with no value = nil; want an error naming the placeholder")
	}
}

// TestRenderIsSinglePass is spec 042 §2: a credential value that itself
// contains a placeholder sequence is substituted literally, never
// re-expanded — otherwise a value could inject another credential.
func TestRenderIsSinglePass(t *testing.T) {
	got, err := Render("v: {{ A }}\n", map[string]string{"A": "{{ B }}", "B": "leaked"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "v: {{ B }}\n" {
		t.Fatalf("Render = %q; want the placeholder-shaped value written literally", got)
	}
}

func TestRenderEntry(t *testing.T) {
	dir := t.TempDir()
	e := ManifestEntry{
		Name: "KUBECONFIG_HZDEV", Env: "KUBECONFIG",
		Template: "cert: {{ CLIENT_CERT }}\n",
		Items:    []string{"KUBECONFIG_HZDEV__CLIENT_CERT"},
	}
	path, err := RenderEntry(dir, e, map[string]string{"CLIENT_CERT": "CERT"})
	if err != nil {
		t.Fatalf("RenderEntry: %v", err)
	}
	if want := filepath.Join(RenderedDir(dir), e.Name); path != want {
		t.Fatalf("path = %q; want %q", path, want)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("path %q is not absolute; purge --task resolves it from anywhere", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o; want 0600", info.Mode().Perm())
	}
	if di, err := os.Stat(RenderedDir(dir)); err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("directory perm = %v, %v; want 0700", di, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "cert: CERT\n" {
		t.Fatalf("rendered = %q, %v", data, err)
	}

	// Re-rendering is stable and leaves no temp file behind: exec renders on
	// every invocation, so a leaked temp per exec would be a plaintext leak.
	if _, err := RenderEntry(dir, e, map[string]string{"CLIENT_CERT": "SECOND"}); err != nil {
		t.Fatalf("second RenderEntry: %v", err)
	}
	entries, err := os.ReadDir(RenderedDir(dir))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != e.Name {
		t.Fatalf("rendered directory = %v; want just the rendered file", entries)
	}
	if data, _ := os.ReadFile(path); string(data) != "cert: SECOND\n" {
		t.Fatalf("re-render left %q", data)
	}
}
