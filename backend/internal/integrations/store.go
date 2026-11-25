package integrations

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	Allow   = "allow"
	Confirm = "confirm"
	Deny    = "deny"
)

type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("integration database path is required")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve integration database path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
			return nil, fmt.Errorf("create integration database directory: %w", err)
		}
		file, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, fmt.Errorf("create protected integration database: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("close integration database file: %w", err)
		}
		if err := os.Chmod(absolute, 0600); err != nil {
			return nil, fmt.Errorf("protect integration database file: %w", err)
		}
		path = absolute
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open integration database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure integration database: %w", err)
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS integration_accounts (
			provider TEXT NOT NULL,
			email TEXT NOT NULL,
			connected_at TEXT NOT NULL,
			PRIMARY KEY (provider, email)
		)`,
		`CREATE TABLE IF NOT EXISTS integration_permissions (
			provider TEXT NOT NULL,
			account TEXT NOT NULL DEFAULT '*',
			action TEXT NOT NULL,
			resource TEXT NOT NULL,
			decision TEXT NOT NULL CHECK (decision IN ('allow', 'confirm', 'deny')),
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (provider, account, action, resource)
		)`,
		`CREATE TABLE IF NOT EXISTS integration_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider TEXT NOT NULL,
			account TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			target TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS integration_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS integration_audit_created_at ON integration_audit(created_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize integration database: %w", err)
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) EmergencyStopped() (bool, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM integration_settings WHERE key = 'emergency_stop'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read integration emergency-stop setting: %w", err)
	}
	switch value {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("invalid integration emergency-stop setting %q", value)
	}
}

func (s *Store) SetEmergencyStopped(stopped bool) error {
	value := "0"
	if stopped {
		value = "1"
	}
	_, err := s.db.Exec(`
		INSERT INTO integration_settings (key, value) VALUES ('emergency_stop', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, value)
	if err != nil {
		return fmt.Errorf("save integration emergency-stop setting: %w", err)
	}
	return nil
}

func (s *Store) SaveAccount(account Account) error {
	if account.Provider == "" || account.Email == "" {
		return errors.New("provider and account email are required")
	}
	if account.ConnectedAt.IsZero() {
		account.ConnectedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`
		INSERT INTO integration_accounts (provider, email, connected_at)
		VALUES (?, ?, ?)
		ON CONFLICT(provider, email) DO UPDATE SET connected_at = excluded.connected_at
	`, account.Provider, account.Email, account.ConnectedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save integration account: %w", err)
	}
	return nil
}

func (s *Store) RemoveAccount(provider, email string) error {
	if provider == "" || email == "" {
		return errors.New("provider and account email are required")
	}
	if _, err := s.db.Exec(`DELETE FROM integration_accounts WHERE provider = ? AND email = ?`, provider, email); err != nil {
		return fmt.Errorf("remove integration account: %w", err)
	}
	if _, err := s.db.Exec(`DELETE FROM integration_permissions WHERE provider = ? AND account = ?`, provider, email); err != nil {
		return fmt.Errorf("remove account permissions: %w", err)
	}
	return nil
}

func (s *Store) Accounts() ([]Account, error) {
	rows, err := s.db.Query(`SELECT provider, email, connected_at FROM integration_accounts ORDER BY provider, email`)
	if err != nil {
		return nil, fmt.Errorf("list integration accounts: %w", err)
	}
	defer rows.Close()
	var accounts []Account
	for rows.Next() {
		var account Account
		var connectedAt string
		if err := rows.Scan(&account.Provider, &account.Email, &connectedAt); err != nil {
			return nil, fmt.Errorf("read integration account: %w", err)
		}
		account.ConnectedAt, err = time.Parse(time.RFC3339Nano, connectedAt)
		if err != nil {
			return nil, fmt.Errorf("parse integration account timestamp: %w", err)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate integration accounts: %w", err)
	}
	return accounts, nil
}

func (s *Store) HasAccount(provider, email string) (bool, error) {
	var exists int
	err := s.db.QueryRow(
		`SELECT 1 FROM integration_accounts WHERE provider = ? AND email = ?`,
		provider, email,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check connected integration account: %w", err)
	}
	return true, nil
}

func (s *Store) SetPermission(provider, action, resource, decision string) error {
	if provider == "" || !validCommunicationAction(action) || resource == "" {
		return errors.New("provider, supported action, and resource are required")
	}
	if decision != Allow && decision != Confirm && decision != Deny {
		return fmt.Errorf("unsupported integration permission decision %q", decision)
	}
	_, err := s.db.Exec(`
		INSERT INTO integration_permissions (provider, account, action, resource, decision, updated_at)
		VALUES (?, '*', ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(provider, account, action, resource) DO UPDATE SET
			decision = excluded.decision,
			updated_at = CURRENT_TIMESTAMP
	`, provider, action, resource, decision)
	if err != nil {
		return fmt.Errorf("save integration permission: %w", err)
	}
	return nil
}

func (s *Store) SetAccountPermission(provider, account, action, resource, decision string) error {
	if provider == "" || account == "" || !validCommunicationAction(action) || resource == "" {
		return errors.New("provider, account, supported action, and resource are required")
	}
	if decision != Allow && decision != Confirm && decision != Deny {
		return fmt.Errorf("unsupported integration permission decision %q", decision)
	}
	_, err := s.db.Exec(`
		INSERT INTO integration_permissions (provider, account, action, resource, decision, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(provider, account, action, resource) DO UPDATE SET
			decision = excluded.decision,
			updated_at = CURRENT_TIMESTAMP
	`, provider, account, action, resource, decision)
	if err != nil {
		return fmt.Errorf("save account integration permission: %w", err)
	}
	return nil
}

func (s *Store) Permission(provider, action, resource string) (string, error) {
	return s.PermissionFor(provider, "", action, resource)
}

func (s *Store) PermissionFor(provider, account, action, resource string) (string, error) {
	var decision string
	accounts := []string{"*"}
	if account != "" {
		accounts = []string{account, "*"}
	}
	resources := []string{resource}
	if resource != "*" {
		resources = append(resources, "*")
	}
	for _, scopedAccount := range accounts {
		for _, scopedResource := range resources {
			err := s.db.QueryRow(
				`SELECT decision FROM integration_permissions
				WHERE provider = ? AND account = ? AND action = ? AND resource = ?`,
				provider, scopedAccount, action, scopedResource,
			).Scan(&decision)
			if err == nil {
				return decision, nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("read integration permission: %w", err)
			}
		}
	}
	switch action {
	case "read_email", "search_email", "read_calendar":
		return Allow, nil
	default:
		return Confirm, nil
	}
}

func (s *Store) AccountPermission(provider, account, action, resource string) (string, bool, error) {
	var decision string
	err := s.db.QueryRow(`
		SELECT decision FROM integration_permissions
		WHERE provider = ? AND account = ? AND action = ? AND resource = ?
	`, provider, account, action, resource).Scan(&decision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read account integration permission: %w", err)
	}
	return decision, true, nil
}

func (s *Store) Permissions() ([]Permission, error) {
	rows, err := s.db.Query(`
		SELECT provider, account, action, resource, decision
		FROM integration_permissions
		ORDER BY provider, account, action, resource
	`)
	if err != nil {
		return nil, fmt.Errorf("list integration permissions: %w", err)
	}
	defer rows.Close()
	var entries []Permission
	for rows.Next() {
		var entry Permission
		if err := rows.Scan(&entry.Provider, &entry.Account, &entry.Action, &entry.Resource, &entry.Decision); err != nil {
			return nil, fmt.Errorf("read integration permission: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate integration permissions: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Provider != entries[j].Provider {
			return entries[i].Provider < entries[j].Provider
		}
		if entries[i].Account != entries[j].Account {
			return entries[i].Account < entries[j].Account
		}
		if entries[i].Action != entries[j].Action {
			return entries[i].Action < entries[j].Action
		}
		return entries[i].Resource < entries[j].Resource
	})
	return entries, nil
}

func (s *Store) RecordAudit(event AuditEvent) error {
	if event.Provider == "" || !validCommunicationAction(event.Action) || event.Result == "" {
		return errors.New("provider, supported action, and result are required")
	}
	id, err := s.BeginAudit(event)
	if err != nil {
		return err
	}
	return s.FinishAudit(id, event.Result)
}

func (s *Store) BeginAudit(event AuditEvent) (int64, error) {
	if event.Provider == "" || !validCommunicationAction(event.Action) {
		return 0, errors.New("provider and supported action are required")
	}
	result, err := s.db.Exec(`
		INSERT INTO integration_audit (provider, account, action, target, result)
		VALUES (?, ?, ?, ?, 'pending')
	`, event.Provider, event.Account, event.Action, event.Target)
	if err != nil {
		return 0, fmt.Errorf("begin integration activity record: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read integration activity ID: %w", err)
	}
	return id, nil
}

func (s *Store) FinishAudit(id int64, result string) error {
	if id <= 0 {
		return errors.New("integration activity ID must be positive")
	}
	switch result {
	case "success", "failed", "denied", "unknown":
	default:
		return fmt.Errorf("unsupported integration activity result %q", result)
	}
	updated, err := s.db.Exec(`
		UPDATE integration_audit SET result = ? WHERE id = ? AND result = 'pending'
	`, result, id)
	if err != nil {
		return fmt.Errorf("finish integration activity record: %w", err)
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return fmt.Errorf("check integration activity update: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("integration activity record %d is missing or already finalized", id)
	}
	return nil
}

func (s *Store) AuditTrail(limit int) ([]AuditEvent, error) {
	if limit < 1 || limit > 500 {
		return nil, errors.New("audit limit must be between 1 and 500")
	}
	rows, err := s.db.Query(`
		SELECT id, provider, account, action, target, result, created_at
		FROM integration_audit ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("read integration activity: %w", err)
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var createdAt string
		if err := rows.Scan(&event.ID, &event.Provider, &event.Account, &event.Action, &event.Target, &event.Result, &createdAt); err != nil {
			return nil, fmt.Errorf("read integration activity entry: %w", err)
		}
		event.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse integration activity timestamp: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate integration activity: %w", err)
	}
	return events, nil
}

func validCommunicationAction(action string) bool {
	switch action {
	case "read_email", "search_email", "send_email", "reply_email", "forward_email",
		"archive_email", "trash_email", "download_attachment", "read_calendar",
		"create_event", "update_event", "delete_event", "test_connection",
		"connect_account", "disconnect_account":
		return true
	default:
		return false
	}
}

func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close integration database: %w", err)
	}
	return nil
}
