package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	root := t.TempDir()
	assistant, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	return assistant, root
}

func TestFileCommands(t *testing.T) {
	assistant, root := newTestEngine(t)

	for _, input := range []string{
		`create "notes file.txt" "hello world"`,
		`append "notes file.txt" " again"`,
	} {
		if _, err := assistant.Execute(input); err != nil {
			t.Fatalf("Execute(%q): %v", input, err)
		}
	}

	result, err := assistant.Execute(`read "notes file.txt"`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "hello world again" {
		t.Fatalf("read returned %q", result.Output)
	}

	if _, err := assistant.Execute(`delete "notes file.txt"`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "notes file.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted, stat error: %v", err)
	}
}

func TestFileCommandsRejectPathsOutsideWorkspace(t *testing.T) {
	assistant, root := newTestEngine(t)
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	if _, err := assistant.Execute(`read ../outside.txt`); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
	if _, err := assistant.Execute(`create ../outside.txt "changed"`); err == nil {
		t.Fatal("expected create outside workspace to be rejected")
	}
}

func TestFileCommandsRejectSymlinkEscape(t *testing.T) {
	assistant, root := newTestEngine(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := assistant.Execute(`read link.txt`); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestCommandsAndAppAllowlist(t *testing.T) {
	assistant, _ := newTestEngine(t)
	launched := ""
	assistant.launch = func(name string) error {
		launched = name
		return nil
	}

	if _, err := assistant.Execute("open vscode"); err != nil {
		t.Fatal(err)
	}
	if launched != "vscode" {
		t.Fatalf("launched %q, want vscode", launched)
	}
	if _, err := assistant.Execute("open powershell"); err == nil {
		t.Fatal("expected unsupported app to be rejected")
	}
	if _, err := assistant.Execute("run whoami"); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("expected unknown command error, got %v", err)
	}
	if _, err := assistant.Execute(`read "unfinished`); err == nil {
		t.Fatal("expected unclosed quote error")
	}
}

func TestSystemCommand(t *testing.T) {
	assistant, _ := newTestEngine(t)
	result, err := assistant.Execute("system")
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"CPU:", "RAM:", "Battery:"} {
		if !strings.Contains(result.Output, label) {
			t.Errorf("system output %q missing %q", result.Output, label)
		}
	}
}

func TestHindiCommands(t *testing.T) {
	assistant, root := newTestEngine(t)
	assistant.launch = func(name string) error {
		if name != "chrome" {
			t.Fatalf("launch called for %q", name)
		}
		return nil
	}

	if _, err := assistant.Execute("क्रोम खोलो"); err != nil {
		t.Fatal(err)
	}
	if _, err := assistant.Execute("फाइल बनाओ नोट्स.txt नमस्ते दुनिया"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "नोट्स.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "नमस्ते दुनिया" {
		t.Fatalf("file contains %q", content)
	}
	result, err := assistant.Execute("फाइल पढ़ो नोट्स.txt")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "नमस्ते दुनिया" {
		t.Fatalf("read returned %q", result.Output)
	}
}
