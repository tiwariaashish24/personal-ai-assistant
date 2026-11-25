package integrations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestGmailSearchSendAndArchiveUseGoogleAPI(t *testing.T) {
	body := base64.RawURLEncoding.EncodeToString([]byte("Hello from Gmail"))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-access" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/gmail/v1/users/me/messages":
			if request.URL.Query().Get("q") != "from:alice@example.com" {
				t.Errorf("Gmail query = %q", request.URL.Query().Get("q"))
			}
			_, _ = writer.Write([]byte(`{"messages":[{"id":"message-1"}]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/gmail/v1/users/me/messages/message-1":
			_, _ = writer.Write([]byte(fmt.Sprintf(`{
				"id":"message-1","threadId":"thread-1","labelIds":["INBOX","UNREAD"],
				"internalDate":"1735689600000",
				"payload":{"mimeType":"text/plain","headers":[
					{"name":"From","value":"Alice <alice@example.com>"},
					{"name":"To","value":"Jarvis <jarvis@example.com>"},
					{"name":"Subject","value":"Greetings"}
				],"body":{"data":%q}}
			}`, body)))
		case request.Method == http.MethodPost && request.URL.Path == "/gmail/v1/users/me/messages/send":
			var payload struct {
				Raw string `json:"raw"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode Gmail send body: %v", err)
			}
			decoded, err := base64.RawURLEncoding.DecodeString(payload.Raw)
			if err != nil || !strings.Contains(string(decoded), `To: "Bob" <bob@example.com>`) ||
				!strings.Contains(string(decoded), "Subject: Project update") ||
				!strings.Contains(string(decoded), "The draft is ready.") {
				t.Errorf("unexpected raw email: %q, decode error: %v", decoded, err)
			}
			_, _ = writer.Write([]byte(`{"id":"sent-1","threadId":"thread-sent"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/gmail/v1/users/me/messages/message-1/modify":
			var payload struct {
				RemoveLabelIDs []string `json:"removeLabelIds"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode archive request: %v", err)
			}
			if len(payload.RemoveLabelIDs) != 1 || payload.RemoveLabelIDs[0] != "INBOX" {
				t.Errorf("archive payload = %#v", payload)
			}
			_, _ = writer.Write([]byte(`{}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	manager := newGoogleTestManager(t, server.URL)

	messages, err := manager.SearchEmails(context.Background(), "owner@example.com", "from:alice@example.com", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Subject != "Greetings" || messages[0].Body != "Hello from Gmail" ||
		!messages[0].Unread || messages[0].From != "Alice <alice@example.com>" {
		t.Fatalf("unexpected Gmail search result: %#v", messages)
	}
	if id, err := manager.SendEmail(context.Background(), "owner@example.com", "Bob <bob@example.com>", "Project update", "The draft is ready."); err != nil || id != "sent-1" {
		t.Fatalf("send result = %q, error = %v", id, err)
	}
	if err := manager.ArchiveEmail(context.Background(), "owner@example.com", "message-1"); err != nil {
		t.Fatal(err)
	}
}

func TestGoogleCalendarCreateAndDelete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-access" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/calendar/v3/calendars/primary/events":
			var payload struct {
				Summary string `json:"summary"`
				Start   struct {
					DateTime string `json:"dateTime"`
				} `json:"start"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode event request: %v", err)
			}
			if payload.Summary != "Planning" || payload.Start.DateTime == "" {
				t.Errorf("calendar payload = %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"id":"event-1","summary":"Planning","start":{"dateTime":"2026-01-02T10:00:00Z"},"end":{"dateTime":"2026-01-02T11:00:00Z"}}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/calendar/v3/calendars/primary/events/event-1":
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	manager := newGoogleTestManager(t, server.URL)

	event, err := manager.CreateCalendarEvent(context.Background(), "owner@example.com", CalendarEvent{
		Summary: "Planning",
		Start:   time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC),
		End:     time.Date(2026, 1, 2, 11, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != "event-1" || event.Summary != "Planning" {
		t.Fatalf("created event = %#v", event)
	}
	if err := manager.DeleteCalendarEvent(context.Background(), "owner@example.com", event.ID); err != nil {
		t.Fatal(err)
	}
}

func TestGoogleCalendarRejectsInvalidTimeRange(t *testing.T) {
	manager := newGoogleTestManager(t, "http://127.0.0.1:1")
	_, err := manager.CreateCalendarEvent(context.Background(), "owner@example.com", CalendarEvent{
		Summary: "Invalid",
		Start:   time.Date(2026, 1, 2, 11, 0, 0, 0, time.UTC),
		End:     time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected invalid event time range to fail")
	}
}

func TestGmailReplyForwardTrashAndAttachmentDownload(t *testing.T) {
	attachmentData := []byte{0, 0xff, 0x80, 'J', 'A', 'R', 'V', 'I', 'S'}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/gmail/v1/users/me/messages/message-1":
			body := base64.RawURLEncoding.EncodeToString([]byte("Original message"))
			_, _ = writer.Write([]byte(fmt.Sprintf(`{
				"id":"message-1","threadId":"thread-1",
				"payload":{"mimeType":"text/plain","headers":[
					{"name":"From","value":"Alice <alice@example.com>"},
					{"name":"Reply-To","value":"Alice <reply@example.com>"},
					{"name":"To","value":"Jarvis <jarvis@example.com>"},
					{"name":"Subject","value":"Project update"},
					{"name":"Message-ID","value":"<original@example.com>"},
					{"name":"References","value":"<older@example.com>"}
				],"body":{"data":%q},"parts":[
					{"mimeType":"application/octet-stream","filename":"..\\evil.bin","body":{"attachmentId":"attachment-1","size":9}}
				]}}
			}`, body)))
		case request.Method == http.MethodPost && request.URL.Path == "/gmail/v1/users/me/messages/send":
			var payload struct {
				Raw      string `json:"raw"`
				ThreadID string `json:"threadId"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode sent message: %v", err)
			}
			raw, err := base64.RawURLEncoding.DecodeString(payload.Raw)
			if err != nil {
				t.Errorf("decode raw message: %v", err)
			}
			if request.URL.Path == "/gmail/v1/users/me/messages/send" && payload.ThreadID != "" {
				if payload.ThreadID != "thread-1" || !strings.Contains(string(raw), `To: "Alice" <reply@example.com>`) ||
					!strings.Contains(string(raw), "In-Reply-To: <original@example.com>") {
					t.Errorf("reply payload has invalid thread or headers: %#v\n%s", payload, raw)
				}
			} else if !strings.Contains(string(raw), `To: "Bob" <bob@example.com>`) ||
				!strings.Contains(string(raw), "Forwarded message") ||
				!strings.Contains(string(raw), "Original message") {
				t.Errorf("forward payload is incomplete: %s", raw)
			}
			_, _ = writer.Write([]byte(`{"id":"sent-1"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/gmail/v1/users/me/messages/message-1/trash":
			writer.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodGet && request.URL.Path == "/gmail/v1/users/me/messages/message-1/attachments/attachment-1":
			_, _ = writer.Write([]byte(fmt.Sprintf(`{"data":%q,"size":%d}`,
				base64.URLEncoding.EncodeToString(attachmentData), len(attachmentData))))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	manager := newGoogleTestManager(t, server.URL)

	if id, err := manager.ReplyEmail(context.Background(), "owner@example.com", "message-1", "Thanks."); err != nil || id != "sent-1" {
		t.Fatalf("reply result = %q, error = %v", id, err)
	}
	if id, err := manager.ForwardEmail(context.Background(), "owner@example.com", "message-1", "Bob <bob@example.com>"); err != nil || id != "sent-1" {
		t.Fatalf("forward result = %q, error = %v", id, err)
	}
	if err := manager.TrashEmail(context.Background(), "owner@example.com", "message-1"); err != nil {
		t.Fatal(err)
	}
	attachment, err := manager.DownloadAttachment(context.Background(), "owner@example.com", "message-1", `..\evil.bin`)
	if err != nil {
		t.Fatal(err)
	}
	if attachment.Filename != "evil.bin" || string(attachment.Data) != string(attachmentData) {
		t.Fatalf("attachment = %#v", attachment)
	}
}

func TestExtractTextUsesHTMLBodyAndEmailHeadersAreSanitized(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte("<p>Hello <b>JARVIS</b></p>"))
	if got := extractText(nil, "text/html", encoded); got != "Hello  JARVIS" {
		t.Fatalf("HTML text = %q", got)
	}
	headers := []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{{Name: "Subject", Value: "hello\r\nBcc: attacker@example.com"}}
	if got := headerMap(headers)["subject"]; got != "hello  Bcc: attacker@example.com" {
		t.Fatalf("sanitized header = %q", got)
	}
	message := composeEmail(nil, "hello\r\nBcc: attacker@example.com", "first\r\nsecond\nthird", nil)
	if strings.Contains(string(message), "\r\nBcc: attacker@example.com") ||
		!strings.Contains(string(message), "Subject: hello  Bcc: attacker@example.com\r\n") ||
		!strings.Contains(string(message), "first\r\nsecond\r\nthird") {
		t.Fatalf("composed MIME message contains unsafe or malformed line breaks: %q", message)
	}
}

func newGoogleTestManager(t *testing.T, baseURL string) *Manager {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "jarvis.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAccount(Account{Provider: Google, Email: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close integration store: %v", err)
		}
	})
	vault := newMemoryVault()
	token, err := json.Marshal(&oauth2.Token{
		AccessToken:  "test-access",
		RefreshToken: "test-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Set(credentialKey(Google, "owner@example.com"), token); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(store, vault, GoogleOAuthConfig{
		ClientID:        "test-client",
		AuthURL:         baseURL + "/authorize",
		TokenURL:        baseURL + "/token",
		ProfileURL:      baseURL + "/profile",
		GmailAPIBase:    baseURL + "/gmail/v1/users/me",
		CalendarAPIBase: baseURL + "/calendar/v3",
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
