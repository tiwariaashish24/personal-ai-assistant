package permissions

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	Allow   = "allow"
	Confirm = "confirm"
	Deny    = "deny"
)

type Entry struct {
	Action   string
	Resource string
	Decision string
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("permission database path is required")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve permission database path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
			return nil, fmt.Errorf("create permission database directory: %w", err)
		}
		path = absolute
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open permission database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure permission database: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS permissions (
			action TEXT NOT NULL,
			resource TEXT NOT NULL,
			decision TEXT NOT NULL CHECK (decision IN ('allow', 'confirm', 'deny')),
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (action, resource)
		)
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize permission database: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Initialize(workspace string) error {
	defaults := []Entry{
		{Action: "open_app", Resource: "*", Decision: Allow},
		{Action: "read_file", Resource: workspace, Decision: Allow},
		{Action: "create_file", Resource: workspace, Decision: Allow},
		{Action: "append_file", Resource: workspace, Decision: Allow},
		{Action: "delete_file", Resource: workspace, Decision: Confirm},
		{Action: "system_monitor", Resource: "*", Decision: Allow},
	}
	for _, entry := range defaults {
		if _, err := s.db.Exec(`
			INSERT OR IGNORE INTO permissions (action, resource, decision)
			VALUES (?, ?, ?)
		`, entry.Action, entry.Resource, entry.Decision); err != nil {
			return fmt.Errorf("initialize %s permission: %w", entry.Action, err)
		}
	}
	return nil
}

func (s *Store) Decision(action, resource string) (string, error) {
	var decision string
	err := s.db.QueryRow(
		`SELECT decision FROM permissions WHERE action = ? AND resource = ?`,
		action, resource,
	).Scan(&decision)
	if err == nil {
		return decision, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("read %s permission: %w", action, err)
	}
	err = s.db.QueryRow(
		`SELECT decision FROM permissions WHERE action = ? AND resource = '*'`,
		action,
	).Scan(&decision)
	if errors.Is(err, sql.ErrNoRows) {
		return Deny, nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s default permission: %w", action, err)
	}
	return decision, nil
}

func (s *Store) Set(action, resource, decision string) error {
	if action == "" || resource == "" {
		return errors.New("permission action and resource are required")
	}
	if decision != Allow && decision != Confirm && decision != Deny {
		return fmt.Errorf("unsupported permission decision %q", decision)
	}
	_, err := s.db.Exec(`
		INSERT INTO permissions (action, resource, decision, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(action, resource) DO UPDATE SET
			decision = excluded.decision,
			updated_at = CURRENT_TIMESTAMP
	`, action, resource, decision)
	if err != nil {
		return fmt.Errorf("save %s permission: %w", action, err)
	}
	return nil
}

func (s *Store) ReadFolderDecision(filePath string) (string, error) {
	rows, err := s.db.Query(`SELECT resource, decision FROM permissions WHERE action = 'read_file'`)
	if err != nil {
		return "", fmt.Errorf("list approved read folders: %w", err)
	}
	defer rows.Close()

	bestRoot := ""
	bestDecision := Deny
	for rows.Next() {
		var root string
		var decision string
		if err := rows.Scan(&root, &decision); err != nil {
			return "", fmt.Errorf("read approved folder: %w", err)
		}
		if root == "*" {
			continue
		}
		relative, err := filepath.Rel(root, filePath)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			if len(root) > len(bestRoot) || (len(root) == len(bestRoot) && decision == Deny) {
				bestRoot = root
				bestDecision = decision
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate approved read folders: %w", err)
	}
	if bestRoot == "" {
		return Deny, nil
	}
	return bestDecision, nil
}

func (s *Store) List() ([]Entry, error) {
	rows, err := s.db.Query(`SELECT action, resource, decision FROM permissions ORDER BY action, resource`)
	if err != nil {
		return nil, fmt.Errorf("list permissions: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.Action, &entry.Resource, &entry.Decision); err != nil {
			return nil, fmt.Errorf("read permission: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate permissions: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Action == entries[j].Action {
			return entries[i].Resource < entries[j].Resource
		}
		return entries[i].Action < entries[j].Action
	})
	return entries, nil
}

func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close permission database: %w", err)
	}
	return nil
}
