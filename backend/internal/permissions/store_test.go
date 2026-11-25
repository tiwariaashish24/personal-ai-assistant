package permissions

import (
	"path/filepath"
	"testing"
)

func TestPermissionDecisionsPersistAndUseMostSpecificReadFolder(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "permissions.db")
	workspace := filepath.Join(t.TempDir(), "workspace")
	approvedFolder := filepath.Join(t.TempDir(), "approved")
	nestedFolder := filepath.Join(approvedFolder, "private")

	store, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Initialize(workspace); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("read_file", approvedFolder, Allow); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("read_file", nestedFolder, Deny); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Initialize(workspace); err != nil {
		t.Fatal(err)
	}

	decision, err := store.ReadFolderDecision(filepath.Join(approvedFolder, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if decision != Allow {
		t.Fatalf("approved folder decision = %q, want allow", decision)
	}
	decision, err = store.ReadFolderDecision(filepath.Join(nestedFolder, "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if decision != Deny {
		t.Fatalf("nested folder decision = %q, want deny", decision)
	}
	decision, err = store.Decision("delete_file", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if decision != Confirm {
		t.Fatalf("delete decision = %q, want confirm", decision)
	}
}
