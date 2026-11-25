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
	t.Setenv("JARVIS_DB_PATH", filepath.Join(t.TempDir(), "permissions.db"))
	assistant, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := assistant.Close(); err != nil {
			t.Errorf("close assistant: %v", err)
		}
	})
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

	result, err = assistant.Execute(`delete "notes file.txt"`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Confirmation == "" {
		t.Fatal("expected a confirmation before moving a file to trash")
	}
	if _, err := os.Stat(filepath.Join(root, "notes file.txt")); err != nil {
		t.Fatalf("file was deleted before confirmation: %v", err)
	}
	result, err = assistant.ExecuteConfirmed(`delete "notes file.txt"`)
	if err != nil {
		t.Fatal(err)
	}
	_, confirmationID, ok := strings.Cut(result.Output, "ID: ")
	if !ok || len(confirmationID) != 32 {
		t.Fatalf("delete result did not include a recoverable trash id: %q", result.Output)
	}
	if _, err := os.Stat(filepath.Join(root, "notes file.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected file to be moved to trash, stat error: %v", err)
	}
	if _, err := assistant.Execute(`create "notes file.txt" replacement`); err != nil {
		t.Fatal(err)
	}
	if _, err := assistant.Execute("restore " + confirmationID); err == nil {
		t.Fatal("expected restore to refuse overwriting an existing file")
	}
	if err := os.Remove(filepath.Join(root, "notes file.txt")); err != nil {
		t.Fatal(err)
	}
	trash, err := assistant.Execute("trash")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(trash.Output, confirmationID) {
		t.Fatalf("trash listing did not include id %q: %s", confirmationID, trash.Output)
	}
	restored, err := assistant.Execute("restore " + confirmationID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Output != "Restored notes file.txt." {
		t.Fatalf("restore returned %q", restored.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "notes file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello world again" {
		t.Fatalf("restored file contains %q", content)
	}
}

func TestCreateBytesPreservesBinaryFileContents(t *testing.T) {
	assistant, root := newTestEngine(t)
	want := []byte{0, 0xff, 0x80, 'J', 'A', 'R', 'V', 'I', 'S'}
	if _, err := assistant.createBytes("attachment.pdf", want); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "attachment.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("written bytes = %v, want %v", got, want)
	}
}

func TestReadNeedsApprovedFolderAndCanBeRevoked(t *testing.T) {
	assistant, _ := newTestEngine(t)
	folder := t.TempDir()
	path := filepath.Join(folder, "private.txt")
	if err := os.WriteFile(path, []byte("approved"), 0600); err != nil {
		t.Fatal(err)
	}
	quotedPath := `"` + path + `"`
	if _, err := assistant.Execute("read " + quotedPath); err == nil {
		t.Fatal("expected unapproved folder read to be rejected")
	}
	if _, err := assistant.Execute(`permission read add "` + folder + `"`); err != nil {
		t.Fatal(err)
	}
	result, err := assistant.Execute("read " + quotedPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "approved" {
		t.Fatalf("read returned %q", result.Output)
	}
	if _, err := assistant.Execute(`permission read remove "` + folder + `"`); err != nil {
		t.Fatal(err)
	}
	if _, err := assistant.Execute("read " + quotedPath); err == nil {
		t.Fatal("expected revoked folder read to be rejected")
	}
}

func TestInternalTrashCannotBeManagedAsWorkspaceFile(t *testing.T) {
	assistant, _ := newTestEngine(t)
	if _, err := assistant.Execute(`create .jarvis-trash/file.txt content`); err == nil {
		t.Fatal("expected direct file creation in internal trash to be rejected")
	}
}

func TestDeleteRefusesUnrecognizedExistingTrashDirectory(t *testing.T) {
	assistant, root := newTestEngine(t)
	trashRoot := filepath.Join(root, trashDirectory)
	if err := os.Mkdir(trashRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := assistant.Execute("delete keep.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := assistant.ExecuteConfirmed("delete keep.txt"); err == nil {
		t.Fatal("expected deletion to reject an unrecognized trash directory")
	}
	if content, err := os.ReadFile(filepath.Join(root, "keep.txt")); err != nil || string(content) != "keep" {
		t.Fatalf("original file was changed: %q, %v", content, err)
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
	assistant.launch = func(name string, _ ...string) error {
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
	assistant.launch = func(name string, _ ...string) error {
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
