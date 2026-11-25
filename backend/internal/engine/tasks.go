package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultTaskTimeout = 5 * time.Minute
	maxTaskHistory     = 200
)

type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
	TaskTimedOut  TaskStatus = "timed_out"
)

type BackgroundTask struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`
	Output      string     `json:"output,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   time.Time  `json:"startedAt,omitempty"`
	CompletedAt time.Time  `json:"completedAt,omitempty"`
}

type taskEntry struct {
	task   BackgroundTask
	cancel context.CancelFunc
}

type TaskManager struct {
	mu     sync.Mutex
	tasks  map[string]*taskEntry
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed bool
}

func NewTaskManager() *TaskManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &TaskManager{
		tasks:  make(map[string]*taskEntry),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (m *TaskManager) Start(description string, timeout time.Duration, run func(context.Context) (string, error)) (BackgroundTask, error) {
	if run == nil {
		return BackgroundTask{}, errors.New("background task requires a runner")
	}
	if timeout <= 0 {
		timeout = defaultTaskTimeout
	}
	id, err := newTaskID()
	if err != nil {
		return BackgroundTask{}, fmt.Errorf("create background task id: %w", err)
	}
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	entry := &taskEntry{
		task: BackgroundTask{
			ID:          id,
			Description: description,
			Status:      TaskQueued,
			CreatedAt:   now,
		},
		cancel: cancel,
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		return BackgroundTask{}, errors.New("background task manager is closed")
	}
	m.tasks[id] = entry
	m.pruneLocked()
	m.wg.Add(1)
	snapshot := entry.task
	m.mu.Unlock()

	go m.run(ctx, entry, run)
	return snapshot, nil
}

func (m *TaskManager) run(ctx context.Context, entry *taskEntry, run func(context.Context) (string, error)) {
	defer m.wg.Done()
	m.mu.Lock()
	entry.task.Status = TaskRunning
	entry.task.StartedAt = time.Now().UTC()
	m.mu.Unlock()

	output, err := invokeTaskRunner(ctx, run)
	if err == nil {
		err = ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry.task.CompletedAt = time.Now().UTC()
	entry.cancel()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		entry.task.Status = TaskTimedOut
		entry.task.Error = "task exceeded its time limit"
	case errors.Is(err, context.Canceled):
		entry.task.Status = TaskCancelled
		entry.task.Error = "task was cancelled"
	case err != nil:
		entry.task.Status = TaskFailed
		entry.task.Error = err.Error()
	default:
		entry.task.Status = TaskCompleted
		entry.task.Output = output
	}
}

func invokeTaskRunner(ctx context.Context, run func(context.Context) (string, error)) (output string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("background task failed unexpectedly: %v", recovered)
		}
	}()
	return run(ctx)
}

func (m *TaskManager) List() []BackgroundTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks := make([]BackgroundTask, 0, len(m.tasks))
	for _, entry := range m.tasks {
		tasks = append(tasks, entry.task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})
	return tasks
}

func (m *TaskManager) Cancel(id string) error {
	m.mu.Lock()
	entry, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("background task %q was not found", id)
	}
	if entry.task.Status != TaskQueued && entry.task.Status != TaskRunning {
		m.mu.Unlock()
		return fmt.Errorf("background task %q is already %s", id, entry.task.Status)
	}
	cancel := entry.cancel
	m.mu.Unlock()
	cancel()
	return nil
}

func (m *TaskManager) Close() {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		m.cancel()
		for _, entry := range m.tasks {
			entry.cancel()
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func (e *Engine) listTasks() (Result, error) {
	tasks := e.tasks.List()
	if len(tasks) == 0 {
		return Result{Output: "No background tasks are running or scheduled.", Data: tasks}, nil
	}
	var output strings.Builder
	output.WriteString("Background tasks:\n")
	for _, task := range tasks {
		fmt.Fprintf(&output, "  %s  %-10s %s", task.ID, task.Status, task.Description)
		if task.Output != "" {
			fmt.Fprintf(&output, " — %s", task.Output)
		}
		if task.Error != "" {
			fmt.Fprintf(&output, " — %s", task.Error)
		}
		output.WriteByte('\n')
	}
	return Result{Output: output.String(), Data: tasks}, nil
}

func (e *Engine) cancelTask(id string) (Result, error) {
	if err := e.tasks.Cancel(id); err != nil {
		return Result{}, err
	}
	return Result{Output: "Cancellation requested for background task " + id + "."}, nil
}

func (e *Engine) scheduleReminder(delayText, message string) (string, error) {
	delay, err := time.ParseDuration(delayText)
	if err != nil || delay < time.Second || delay > 30*24*time.Hour {
		return "", errors.New("reminder duration must be between 1s and 30d, using Go duration syntax such as 10m or 1h")
	}
	message = strings.TrimSpace(message)
	if message == "" || len(message) > 1000 {
		return "", errors.New("reminder message must contain 1 to 1000 characters")
	}
	task, err := e.tasks.Start("Reminder: "+message, delay+time.Minute, func(ctx context.Context) (string, error) {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "Reminder: " + message, nil
		}
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Scheduled reminder %s for %s from now (task %s).", message, delay, task.ID), nil
}

func (m *TaskManager) pruneLocked() {
	if len(m.tasks) <= maxTaskHistory {
		return
	}
	entries := make([]*taskEntry, 0, len(m.tasks))
	for _, entry := range m.tasks {
		if entry.task.Status != TaskQueued && entry.task.Status != TaskRunning {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].task.CreatedAt.Before(entries[j].task.CreatedAt)
	})
	for _, entry := range entries {
		if len(m.tasks) <= maxTaskHistory {
			break
		}
		delete(m.tasks, entry.task.ID)
	}
}

func newTaskID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
