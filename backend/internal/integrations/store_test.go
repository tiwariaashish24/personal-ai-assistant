package integrations

import (
	"path/filepath"
	"testing"
)

func TestIntegrationPermissionsAndAuditPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jarvis.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAccount(Account{Provider: Google, Email: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccountPermission(Google, "owner@example.com", "send_email", "friend@example.com", Allow); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAudit(AuditEvent{
		Provider: Google, Account: "owner@example.com", Action: "send_email", Target: "friend@example.com", Result: "success",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	accounts, err := store.Accounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Email != "owner@example.com" {
		t.Fatalf("accounts = %#v", accounts)
	}
	decision, err := store.PermissionFor(Google, "owner@example.com", "send_email", "friend@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if decision != Allow {
		t.Fatalf("trusted recipient decision = %q, want %q", decision, Allow)
	}
	decision, err = store.PermissionFor(Google, "owner@example.com", "send_email", "new@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if decision != Confirm {
		t.Fatalf("unknown recipient decision = %q, want %q", decision, Confirm)
	}
	events, err := store.AuditTrail(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Account != "owner@example.com" || events[0].Target != "friend@example.com" {
		t.Fatalf("audit events = %#v", events)
	}
	if err := store.RemoveAccount(Google, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	decision, err = store.PermissionFor(Google, "owner@example.com", "send_email", "friend@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if decision != Confirm {
		t.Fatalf("revoked account permission = %q, want %q", decision, Confirm)
	}
}

func TestIntegrationPermissionRejectsUnknownActions(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetPermission(Google, "run_shell", "*", Allow); err == nil {
		t.Fatal("expected unsupported action to be rejected")
	}
}

func TestIntegrationAuditMustBeFinalizedOnce(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id, err := store.BeginAudit(AuditEvent{
		Provider: Google, Account: "owner@example.com", Action: "send_email", Target: "friend@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.AuditTrail(10)
	if err != nil || len(events) != 1 || events[0].Result != "pending" {
		t.Fatalf("pending audit = %#v, error = %v", events, err)
	}
	if err := store.FinishAudit(id, "failed"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishAudit(id, "success"); err == nil {
		t.Fatal("expected finalized audit event to reject a second result")
	}
	events, err = store.AuditTrail(10)
	if err != nil || len(events) != 1 || events[0].Result != "failed" {
		t.Fatalf("final audit = %#v, error = %v", events, err)
	}
}

func TestEmergencyStopPersistsAndDefaultsOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jarvis.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := store.EmergencyStopped()
	if err != nil || stopped {
		t.Fatalf("default emergency stop = %v, error = %v", stopped, err)
	}
	if err := store.SetEmergencyStopped(true); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stopped, err = store.EmergencyStopped()
	if err != nil || !stopped {
		t.Fatalf("persisted emergency stop = %v, error = %v", stopped, err)
	}
}
