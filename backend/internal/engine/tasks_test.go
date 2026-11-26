package engine

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskManagerCancelsRunningTask(t *testing.T) {
	manager := NewTaskManager()
	defer manager.Close()

	task, err := manager.Start("wait for cancellation", time.Minute, func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	finished := waitForTaskStatus(t, manager, task.ID, TaskCancelled)
	if finished.Error == "" {
		t.Fatal("cancelled task should explain its status")
	}
}

func TestTaskManagerMarksTimeoutAndCompletion(t *testing.T) {
	manager := NewTaskManager()
	defer manager.Close()

	timedOut, err := manager.Start("slow task", 10*time.Millisecond, func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForTaskStatus(t, manager, timedOut.ID, TaskTimedOut); got.Error == "" {
		t.Fatal("timed-out task should explain its status")
	}

	completed, err := manager.Start("quick task", time.Second, func(context.Context) (string, error) {
		return "finished", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitForTaskStatus(t, manager, completed.ID, TaskCompleted)
	if got.Output != "finished" || got.Error != "" {
		t.Fatalf("completed task = %#v", got)
	}
	if err := manager.Cancel(completed.ID); err == nil {
		t.Fatal("completed tasks must not be cancellable")
	}
}

func TestTaskManagerRecordsRunnerErrors(t *testing.T) {
	manager := NewTaskManager()
	defer manager.Close()

	task, err := manager.Start("failed task", time.Second, func(context.Context) (string, error) {
		return "", errors.New("runner failed")
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitForTaskStatus(t, manager, task.ID, TaskFailed)
	if got.Error != "runner failed" {
		t.Fatalf("failure = %q", got.Error)
	}
}

func TestTaskManagerRecoversRunnerPanics(t *testing.T) {
	manager := NewTaskManager()
	defer manager.Close()

	task, err := manager.Start("panicking task", time.Second, func(context.Context) (string, error) {
		panic("unexpected task panic")
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitForTaskStatus(t, manager, task.ID, TaskFailed)
	if !strings.Contains(got.Error, "unexpected task panic") {
		t.Fatalf("panic failure = %q", got.Error)
	}
}

func TestReminderCommandCreatesCancelableTask(t *testing.T) {
	assistant, _ := newTestEngine(t)
	result, err := assistant.Execute("remind 1h take a break")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Scheduled reminder") {
		t.Fatalf("reminder result = %q", result.Output)
	}
	currentTasks := assistant.tasks.List()
	if len(currentTasks) != 1 || currentTasks[0].Description != "Reminder: take a break" {
		t.Fatalf("scheduled tasks = %#v", currentTasks)
	}
	if _, err := assistant.Execute("cancel task " + currentTasks[0].ID); err != nil {
		t.Fatal(err)
	}
	cancelled := waitForTaskStatus(t, assistant.tasks, currentTasks[0].ID, TaskCancelled)
	if cancelled.Error == "" {
		t.Fatal("cancelled reminder should explain its status")
	}
	if _, err := assistant.Execute("remind 0s invalid"); err == nil {
		t.Fatal("expected invalid reminder duration to be rejected")
	}
}

func TestReminderPersistsAcrossEngineRestart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JARVIS_DB_PATH", filepath.Join(t.TempDir(), "jarvis.db"))

	first, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Execute("remind 1h persisted reminder"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Errorf("close restarted assistant: %v", err)
		}
	}()

	pending, err := second.reminders.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Message != "persisted reminder" {
		t.Fatalf("restored reminders = %#v", pending)
	}
	restored := false
	for _, task := range second.tasks.List() {
		if task.Description == "Reminder: persisted reminder" {
			restored = true
		}
	}
	if !restored {
		t.Fatalf("restored tasks = %#v", second.tasks.List())
	}
}

func waitForTaskStatus(t *testing.T, manager *TaskManager, id string, want TaskStatus) BackgroundTask {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, task := range manager.List() {
			if task.ID == id && task.Status == want {
				return task
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("task %q did not reach %q; current tasks: %#v", id, want, manager.List())
	return BackgroundTask{}
}
