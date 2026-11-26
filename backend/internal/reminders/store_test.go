package reminders

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestStorePersistsAndRestoresPendingReminders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reminders.db")

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dueAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	if err := store.Create(Reminder{ID: "rem-1", Message: "drink water", DueAt: dueAt}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	pending, err := reopened.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "rem-1" || pending[0].Message != "drink water" {
		t.Fatalf("pending reminders = %#v", pending)
	}
	if !pending[0].DueAt.Equal(dueAt) {
		t.Fatalf("dueAt = %s, want %s", pending[0].DueAt, dueAt)
	}

	if err := reopened.MarkCompleted("rem-1"); err != nil {
		t.Fatal(err)
	}
	pending, err = reopened.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after completion = %#v", pending)
	}
	if err := reopened.MarkCompleted("rem-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second completion error = %v, want ErrNotFound", err)
	}
}

func TestStoreMarkCancelled(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Create(Reminder{ID: "rem-2", Message: "call mom", DueAt: time.Now().UTC().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCancelled("rem-2"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCancelled("rem-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat cancellation error = %v, want ErrNotFound", err)
	}
	pending, err := store.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after cancellation = %#v", pending)
	}
}
