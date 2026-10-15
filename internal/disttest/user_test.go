package disttest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserDistributionTemplates(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()

	homebrew := render(t, root, filepath.Join(".github", "homebrew", "render-formula.py"),
		filepath.Join(".github", "homebrew", "worklode.rb.template"), filepath.Join(tmp, "worklode.rb"),
		"URL=https://example.test/worklode.tar.gz", "SHA256=deadbeef")
	for _, binary := range []struct{ name, source string }{
		{"lode", "./cmd/lode"},
		{"lode-hook", "./cmd/lode-hook"},
		{"lode-statusline", "./cmd/lode-statusline"},
	} {
		if !strings.Contains(homebrew, "output: bin/\""+binary.name+"\"), \""+binary.source+"\"") ||
			!strings.Contains(homebrew, "#{bin}/"+binary.name+" --version") {
			t.Errorf("Homebrew formula does not build/install and version-check %q from %q", binary.name, binary.source)
		}
	}
}

func render(t *testing.T, root, script, template, output string, env ...string) string {
	t.Helper()
	cmd := exec.Command("python3", filepath.Join(root, script), filepath.Join(root, template), output)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("render %s: %v\n%s", script, err, out)
	}
	rendered, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	return string(rendered)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
