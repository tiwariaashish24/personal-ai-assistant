package reminders

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	Pending   = "pending"
	Completed = "completed"
	Cancelled = "cancelled"
)

var ErrNotFound = errors.New("reminder not found")

type Reminder struct {
	ID          string    `json:"id"`
	Message     string    `json:"message"`
	DueAt       time.Time `json:"dueAt"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	CompletedAt time.Time `json:"completedAt,omitempty"`
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("reminder database path is required")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve reminder database path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
			return nil, fmt.Errorf("create reminder database directory: %w", err)
		}
		path = absolute
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open reminder database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure reminder database: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS reminders (
			id TEXT PRIMARY KEY,
			message TEXT NOT NULL,
			due_at TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'completed', 'cancelled')),
			created_at TEXT NOT NULL,
			completed_at TEXT
		)
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize reminder database: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Create(reminder Reminder) error {
	if reminder.ID == "" || strings.TrimSpace(reminder.Message) == "" || reminder.DueAt.IsZero() {
		return errors.New("reminder id, message, and due time are required")
	}
	if reminder.Status == "" {
		reminder.Status = Pending
	}
	if reminder.Status != Pending {
		return fmt.Errorf("new reminders must start as %s", Pending)
	}
	if reminder.CreatedAt.IsZero() {
		reminder.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`
		INSERT INTO reminders (id, message, due_at, status, created_at, completed_at)
		VALUES (?, ?, ?, ?, ?, NULL)
	`, reminder.ID, reminder.Message, reminder.DueAt.UTC().Format(time.RFC3339Nano),
		reminder.Status, reminder.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save reminder: %w", err)
	}
	return nil
}

func (s *Store) Pending() ([]Reminder, error) {
	return s.list(`status = ?`, Pending)
}

func (s *Store) List(limit int) ([]Reminder, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT id, message, due_at, status, created_at, completed_at
		FROM reminders ORDER BY due_at DESC, created_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list reminders: %w", err)
	}
	defer rows.Close()
	return scanReminders(rows)
}

func (s *Store) list(where, status string) ([]Reminder, error) {
	rows, err := s.db.Query(`
		SELECT id, message, due_at, status, created_at, completed_at
		FROM reminders WHERE `+where+` ORDER BY due_at ASC, created_at ASC
	`, status)
	if err != nil {
		return nil, fmt.Errorf("list pending reminders: %w", err)
	}
	defer rows.Close()
	return scanReminders(rows)
}

func scanReminders(rows *sql.Rows) ([]Reminder, error) {
	var reminders []Reminder
	for rows.Next() {
		var reminder Reminder
		var dueAt, createdAt string
		var completedAt sql.NullString
		if err := rows.Scan(&reminder.ID, &reminder.Message, &dueAt, &reminder.Status, &createdAt, &completedAt); err != nil {
			return nil, fmt.Errorf("read reminder: %w", err)
		}
		var err error
		reminder.DueAt, err = time.Parse(time.RFC3339Nano, dueAt)
		if err != nil {
			return nil, fmt.Errorf("parse reminder due time: %w", err)
		}
		reminder.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse reminder creation time: %w", err)
		}
		if completedAt.Valid {
			reminder.CompletedAt, err = time.Parse(time.RFC3339Nano, completedAt.String)
			if err != nil {
				return nil, fmt.Errorf("parse reminder completion time: %w", err)
			}
		}
		reminders = append(reminders, reminder)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reminders: %w", err)
	}
	return reminders, nil
}

func (s *Store) MarkCompleted(id string) error {
	return s.mark(id, Completed)
}

func (s *Store) MarkCancelled(id string) error {
	return s.mark(id, Cancelled)
}

func (s *Store) mark(id, status string) error {
	if id == "" {
		return errors.New("reminder id is required")
	}
	result, err := s.db.Exec(`
		UPDATE reminders
		SET status = ?, completed_at = ?
		WHERE id = ? AND status = ?
	`, status, time.Now().UTC().Format(time.RFC3339Nano), id, Pending)
	if err != nil {
		return fmt.Errorf("update reminder status: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check reminder update: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close reminder database: %w", err)
	}
	return nil
}
