package disttest

import (
	"encoding/json"
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
	for _, name := range []string{"lode", "lode-hook", "lode-statusline"} {
		if !strings.Contains(homebrew, "./cmd/"+name) ||
			!strings.Contains(homebrew, "bin/\""+name+"\"") ||
			!strings.Contains(homebrew, "#{bin}/"+name+" --version") {
			t.Errorf("Homebrew formula does not build, install, and version-check %q", name)
		}
	}

	scoop := render(t, root, filepath.Join(".github", "scoop", "render-manifest.py"),
		filepath.Join(".github", "scoop", "worklode.json.template"), filepath.Join(tmp, "worklode.json"),
		"VERSION=1.2.3", "URL=https://example.test/worklode.zip", "SHA256=deadbeef")
	var manifest struct {
		Architecture struct {
			AMD64 struct {
				Bin []string `json:"bin"`
			} `json:"64bit"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal([]byte(scoop), &manifest); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(manifest.Architecture.AMD64.Bin, ","), "lode.exe,lode-hook.exe,lode-statusline.exe"; got != want {
		t.Errorf("Scoop bin = %q, want %q", got, want)
	}

	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "_build-windows.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lode.exe", "lode-hook.exe", "lode-statusline.exe"} {
		if !strings.Contains(string(workflow), "zip -q \"$ZIP\" lode.exe lode-hook.exe lode-statusline.exe") {
			t.Errorf("Windows archive does not include all three executables")
			break
		}
		if !strings.Contains(string(workflow), name) {
			t.Errorf("Windows workflow does not build %q", name)
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
