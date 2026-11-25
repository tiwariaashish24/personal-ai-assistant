package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"jarvis/internal/integrations"
)

type testIntegrationVault struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (v *testIntegrationVault) Get(key string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	value, ok := v.values[key]
	if !ok {
		return nil, fmt.Errorf("credential %q not found", key)
	}
	return append([]byte(nil), value...), nil
}

func (v *testIntegrationVault) Set(key string, value []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.values[key] = append([]byte(nil), value...)
	return nil
}

func (v *testIntegrationVault) Delete(key string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.values, key)
	return nil
}

func TestUnknownGmailRecipientRequiresAndUsesExactConfirmation(t *testing.T) {
	assistant, _ := newTestEngine(t)
	var sendCount int
	apiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/gmail/v1/users/me/messages/send" {
			http.NotFound(writer, request)
			return
		}
		sendCount++
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"sent-1"}`))
	}))
	defer apiServer.Close()

	oldManager := assistant.integrations
	if err := oldManager.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := integrations.OpenStore(filepath.Join(t.TempDir(), "integrations.db"))
	if err != nil {
		t.Fatal(err)
	}
	vault := &testIntegrationVault{values: make(map[string][]byte)}
	token, err := json.Marshal(&oauth2.Token{
		AccessToken:  "test-access",
		RefreshToken: "test-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Set("google:owner@example.com", token); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAccount(integrations.Account{Provider: integrations.Google, Email: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	manager, err := integrations.NewManager(store, vault, integrations.GoogleOAuthConfig{
		ClientID:        "test-client",
		AuthURL:         apiServer.URL + "/authorize",
		TokenURL:        apiServer.URL + "/token",
		ProfileURL:      apiServer.URL + "/profile",
		GmailAPIBase:    apiServer.URL + "/gmail/v1/users/me",
		CalendarAPIBase: apiServer.URL + "/calendar/v3",
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant.integrations = manager

	var completionRequests int
	aiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		completionRequests++
		if completionRequests != 1 {
			t.Errorf("AI completion called %d times; confirmation must execute the original tool arguments", completionRequests)
		}
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"send-1","type":"function","function":{"name":"send_gmail","arguments":"{\"account\":\"owner@example.com\",\"recipients\":[\"new@example.com\"],\"subject\":\"Hello\",\"body\":\"Hi there\"}"}}]}}]}`))
	}))
	defer aiServer.Close()
	assistant.ai = &aiAssistant{client: aiServer.Client(), endpoint: aiServer.URL, model: "test"}

	command := "send an email to new@example.com"
	result, err := assistant.Execute(command)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Confirmation, "new@example.com") || sendCount != 0 {
		t.Fatalf("confirmation = %q, send count = %d", result.Confirmation, sendCount)
	}
	result, err = assistant.ExecuteConfirmed(command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "Sent email sent-1 to new@example.com." || sendCount != 1 || completionRequests != 1 {
		t.Fatalf("confirmed result = %#v, sends = %d, AI completions = %d", result, sendCount, completionRequests)
	}
	events, err := assistant.integrations.AuditTrail(10)
	if err != nil || len(events) != 1 || events[0].Action != "send_email" || events[0].Result != "success" {
		t.Fatalf("send audit events = %#v, error = %v", events, err)
	}
}

func TestGoogleActionFailureIsAudited(t *testing.T) {
	assistant, _ := newTestEngine(t)
	_, err := assistant.executeAndAudit(integrations.AuditEvent{
		Provider: integrations.Google,
		Account:  "owner@example.com",
		Action:   "send_email",
		Target:   "friend@example.com",
	}, func() (string, error) {
		return "", errors.New("provider unavailable")
	})
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("operation error = %v", err)
	}
	events, err := assistant.integrations.AuditTrail(10)
	if err != nil || len(events) != 1 || events[0].Result != "failed" {
		t.Fatalf("failed action audit = %#v, error = %v", events, err)
	}
}

func TestEmergencyStopBlocksGoogleActionsAndRequiresConfirmationToResume(t *testing.T) {
	assistant, _ := newTestEngine(t)
	if _, err := assistant.Execute("integration emergency-stop on"); err != nil {
		t.Fatal(err)
	}
	executed := false
	_, err := assistant.authorizeGoogleAction("search mail", "owner@example.com", "search_email", "*", "Search?", false, func() (string, error) {
		executed = true
		return "unexpected", nil
	})
	if err == nil || !strings.Contains(err.Error(), "emergency stop") || executed {
		t.Fatalf("action error = %v, operation executed = %v", err, executed)
	}
	result, err := assistant.Execute("integration emergency-stop off")
	if err != nil || result.Confirmation == "" {
		t.Fatalf("resume result = %#v, error = %v", result, err)
	}
	result, err = assistant.ExecuteConfirmed("integration emergency-stop off")
	if err != nil || !strings.Contains(result.Output, "disabled") {
		t.Fatalf("confirmed resume result = %#v, error = %v", result, err)
	}
	stopped, err := assistant.integrations.EmergencyStopped()
	if err != nil || stopped {
		t.Fatalf("emergency stop after confirmation = %v, error = %v", stopped, err)
	}
	events, err := assistant.integrations.AuditTrail(10)
	if err != nil || len(events) != 1 || events[0].Result != "denied" {
		t.Fatalf("emergency-stop audit = %#v, error = %v", events, err)
	}
}

func TestApprovedAttachmentDownloadsCanRunWithoutPrompt(t *testing.T) {
	assistant, _ := newTestEngine(t)
	if err := assistant.integrations.SetAccountPermission(
		integrations.Google, "owner@example.com", "download_attachment", "*", integrations.Allow,
	); err != nil {
		t.Fatal(err)
	}
	executed := false
	output, err := assistant.authorizeGoogleAction("download report", "owner@example.com", "download_attachment", "*", "Download?", false, func() (string, error) {
		executed = true
		return "Downloaded report.pdf.", nil
	})
	if err != nil || output != "Downloaded report.pdf." || !executed {
		t.Fatalf("approved attachment result = %q, error = %v, executed = %v", output, err, executed)
	}
}

func TestTrustedGmailRecipientMaySendRoutineEmailWithoutPrompt(t *testing.T) {
	assistant, _ := newTestEngine(t)
	var sendCount int
	apiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/gmail/v1/users/me/messages/send" {
			sendCount++
			_, _ = writer.Write([]byte(`{"id":"sent-2"}`))
			return
		}
		http.NotFound(writer, request)
	}))
	defer apiServer.Close()

	oldManager := assistant.integrations
	if err := oldManager.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := integrations.OpenStore(filepath.Join(t.TempDir(), "integrations.db"))
	if err != nil {
		t.Fatal(err)
	}
	vault := &testIntegrationVault{values: make(map[string][]byte)}
	token, _ := json.Marshal(&oauth2.Token{
		AccessToken:  "test-access",
		RefreshToken: "test-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	})
	if err := vault.Set("google:owner@example.com", token); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAccount(integrations.Account{Provider: integrations.Google, Email: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccountPermission(integrations.Google, "owner@example.com", "send_email", "friend@example.com", integrations.Allow); err != nil {
		t.Fatal(err)
	}
	manager, err := integrations.NewManager(store, vault, integrations.GoogleOAuthConfig{
		ClientID:        "test-client",
		AuthURL:         apiServer.URL + "/authorize",
		TokenURL:        apiServer.URL + "/token",
		ProfileURL:      apiServer.URL + "/profile",
		GmailAPIBase:    apiServer.URL + "/gmail/v1/users/me",
		CalendarAPIBase: apiServer.URL + "/calendar/v3",
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant.integrations = manager

	requests := 0
	aiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if requests == 1 {
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"send-2","type":"function","function":{"name":"send_gmail","arguments":"{\"account\":\"owner@example.com\",\"recipients\":[\"friend@example.com\"],\"subject\":\"Reminder\",\"body\":\"See you soon.\"}"}}]}}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Sent the routine reminder."}}]}`))
	}))
	defer aiServer.Close()
	assistant.ai = &aiAssistant{client: aiServer.Client(), endpoint: aiServer.URL, model: "test"}

	result, err := assistant.Execute("send my friend a reminder")
	if err != nil {
		t.Fatal(err)
	}
	if result.Confirmation != "" || result.Output != "Sent the routine reminder." || sendCount != 1 {
		t.Fatalf("routine send result = %#v, send count = %d", result, sendCount)
	}
}

func TestSensitiveContentRequiresConfirmationForTrustedContact(t *testing.T) {
	for _, value := range []string{
		"My password is correct-horse",
		"Card 4111 1111 1111 1111",
		"SSN 123-45-6789",
		"Call me at 555 123 9876",
	} {
		if !containsSensitiveContent(value) {
			t.Errorf("sensitive content not detected: %q", value)
		}
	}
}
