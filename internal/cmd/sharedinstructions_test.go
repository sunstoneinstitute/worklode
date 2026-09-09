package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedInstructionsMigration(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "separate", true: "symlink"}[linked], func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "CLAUDE.md", "# Project\n\n"+agentsBlock)
			if linked {
				if err := os.Symlink("CLAUDE.md", filepath.Join(root, agentsFile)); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, root, agentsFile, "# Agent notes\n\n"+agentsBlock)
			}
			writeFile(t, root, claudeFile, "# Personal notes\n\n"+agentsBlock)
			res, err := ensureInstructions(root)
			if err != nil {
				t.Fatal(err)
			}
			if res.SharedMD != instrCreated || res.BlockFile != sharedInstructionsFile {
				t.Fatalf("result: %+v", res)
			}
			if got := readFile(t, filepath.Join(root, sharedInstructionsFile)); got != agentsBlock {
				t.Fatalf("shared body: %q", got)
			}
			if got := readFile(t, filepath.Join(root, claudeFile)); got != "# Personal notes\n" {
				t.Fatalf("personal notes: %q", got)
			}
			for _, name := range []string{agentsFile, "CLAUDE.md"} {
				got := readFile(t, filepath.Join(root, name))
				if strings.Count(got, instructionReadLine) != 1 || strings.Contains(got, agentsBlockBegin) {
					t.Fatalf("%s: %q", name, got)
				}
			}
			res, err = ensureInstructions(root)
			if err != nil || res.SharedMD != instrUnchanged || res.AgentsMD != instrUnchanged || res.ClaudeMD != instrUnchanged {
				t.Fatalf("repeat: %+v %v", res, err)
			}
			if linked {
				if target, err := os.Readlink(filepath.Join(root, agentsFile)); err != nil || target != "CLAUDE.md" {
					t.Fatalf("link: %q %v", target, err)
				}
			}
			res, err = removeInstructions(root)
			if err != nil || res.SharedMD != instrRemoved {
				t.Fatalf("uninstall: %+v %v", res, err)
			}
			if _, err := os.Stat(filepath.Join(root, sharedInstructionsFile)); !os.IsNotExist(err) {
				t.Fatalf("shared file remains: %v", err)
			}
			if !strings.Contains(readFile(t, filepath.Join(root, agentsFile)), instructionReadLine) {
				t.Fatal("root pointer removed")
			}
		})
	}
}

func TestSharedInstructionsRemoveLegacyImport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, agentsFile, agentsBlock)
	writeFile(t, root, claudeFile, claudeImportLine+"\n")
	if _, err := ensureInstructions(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, claudeFile)); !os.IsNotExist(err) {
		t.Fatalf("legacy import remains: %v", err)
	}
	for _, name := range []string{agentsFile, "CLAUDE.md"} {
		if got := readFile(t, filepath.Join(root, name)); got != instructionReadLine+"\n" {
			t.Fatalf("%s: %q", name, got)
		}
	}
}

func TestSharedInstructionsPreserveAuthoredContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, sharedInstructionsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const notes = "# Shared team notes\n"
	writeFile(t, root, sharedInstructionsFile, notes)
	res, err := ensureInstructions(root)
	if err != nil || res.SharedMD != instrAdded {
		t.Fatalf("install: %+v %v", res, err)
	}
	if got := readFile(t, path); !strings.HasPrefix(got, notes) {
		t.Fatalf("notes lost: %q", got)
	}
	if _, err := removeInstructions(root); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != notes {
		t.Fatalf("uninstall: %q", got)
	}
}

func TestInstructionPointerIgnoresQuotedExamples(t *testing.T) {
	for _, fence := range []string{"```", "~~~"} {
		t.Run(fence, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, agentsFile)
			original := fence + "\n" + instructionReadLine + "\n" + fence + "\n\nPersonal instructions without a trailing newline."
			writeFile(t, root, agentsFile, original)
			if _, err := ensureInstructionPointer(path); err != nil {
				t.Fatal(err)
			}
			want := original + "\n\n" + instructionReadLine + "\n"
			if got := readFile(t, path); got != want {
				t.Fatalf("pointer: %q", got)
			}
			if action, err := ensureInstructionPointer(path); err != nil || action != instrUnchanged {
				t.Fatalf("repeat: %s %v", action, err)
			}
		})
	}
}

func TestSharedInstructionsWriteFailurePreservesLegacy(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".worklode", "A file blocks the directory.\n")
	writeFile(t, root, claudeFile, agentsBlock)
	if _, err := ensureInstructions(root); err == nil {
		t.Fatal("expected write error")
	}
	if got := readFile(t, filepath.Join(root, claudeFile)); got != agentsBlock {
		t.Fatal("legacy instructions lost")
	}
}

func TestReportSharedInstructions(t *testing.T) {
	var out strings.Builder
	command := discardCmd()
	command.SetOut(&out)
	res := &instructionsResult{SharedMD: instrCreated, BlockFile: sharedInstructionsFile, AgentsMD: instrAdded, ClaudeMD: instrUnchanged}
	if err := reportInstall(command, installResult{Instructions: res}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{sharedInstructionsFile + ": created", "AGENTS.md: added read instruction", "CLAUDE.md: unchanged read instruction"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("report missing %q: %s", want, &out)
		}
	}
	out.Reset()
	res.SharedMD = instrRemoved
	if err := reportUninstall(command, uninstallResult{Instructions: res}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), sharedInstructionsFile+": removed; root read instructions retained") {
		t.Fatalf("uninstall report: %s", &out)
	}
}
