package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyHookHelpForwardsToDirectBinary(t *testing.T) {
	bin := buildLodeBinary(t)
	command := exec.Command(bin, "hook", "--help")
	command.Env = append(os.Environ(), "PATH="+filepath.Dir(bin))
	out, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "lode-hook") {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestRunSiblingNamesMissingDistribution(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := runSibling(t.Context(), "lode-statusline", "user", nil, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "lode-statusline") || !strings.Contains(err.Error(), "user distribution") {
		t.Fatalf("error = %v", err)
	}
}
