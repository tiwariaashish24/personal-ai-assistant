package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryVault struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newMemoryVault() *memoryVault {
	return &memoryVault{values: make(map[string][]byte)}
}

func (v *memoryVault) Get(key string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	value, ok := v.values[key]
	if !ok {
		return nil, fmt.Errorf("credential %q not found", key)
	}
	return append([]byte(nil), value...), nil
}

func (v *memoryVault) Set(key string, value []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.values[key] = append([]byte(nil), value...)
	return nil
}

func (v *memoryVault) Delete(key string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.values, key)
	return nil
}

func TestGoogleOAuthCallbackStoresAccountAndRefreshesTokens(t *testing.T) {
	vault := newMemoryVault()
	tokenRequests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			if err := request.ParseForm(); err != nil {
				t.Errorf("parse token request: %v", err)
			}
			if request.Form.Get("grant_type") == "authorization_code" && request.Form.Get("code_verifier") == "" {
				t.Error("PKCE verifier was missing from token exchange")
			}
			tokenRequests++
			writer.Header().Set("Content-Type", "application/json")
			if request.Form.Get("grant_type") == "authorization_code" {
				_, _ = writer.Write([]byte(`{"access_token":"initial","refresh_token":"refresh-1","token_type":"Bearer","expires_in":3600}`))
			} else {
				_, _ = writer.Write([]byte(`{"access_token":"refreshed","refresh_token":"refresh-2","token_type":"Bearer","expires_in":3600}`))
			}
		case "/profile":
			if request.Header.Get("Authorization") != "Bearer initial" && request.Header.Get("Authorization") != "Bearer refreshed" {
				t.Errorf("profile request authorization = %q", request.Header.Get("Authorization"))
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"emailAddress":"Jarvis.User@example.com","messagesTotal":12,"threadsTotal":9}`))
		case "/calendar/v3/calendars/primary/events":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	store, err := OpenStore(filepath.Join(t.TempDir(), "jarvis.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := NewManager(store, vault, GoogleOAuthConfig{
		ClientID:        "test-client",
		AuthURL:         server.URL + "/authorize",
		TokenURL:        server.URL + "/token",
		ProfileURL:      server.URL + "/profile",
		CalendarAPIBase: server.URL + "/calendar/v3",
	})
	if err != nil {
		t.Fatal(err)
	}

	account, err := manager.Connect(context.Background(), Google, func(authURL string) error {
		parsed, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		if parsed.Query().Get("code_challenge") == "" || parsed.Query().Get("access_type") != "offline" {
			t.Errorf("OAuth URL omitted PKCE or offline access: %s", authURL)
		}
		callback, err := url.Parse(parsed.Query().Get("redirect_uri"))
		if err != nil {
			return err
		}
		query := callback.Query()
		query.Set("state", parsed.Query().Get("state"))
		query.Set("code", "test-code")
		callback.RawQuery = query.Encode()
		response, err := http.Get(callback.String())
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("callback returned HTTP %d", response.StatusCode)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.Email != "jarvis.user@example.com" {
		t.Fatalf("account email = %q", account.Email)
	}

	tokenData, err := vault.Get(credentialKey(Google, account.Email))
	if err != nil {
		t.Fatal(err)
	}
	var token map[string]any
	if err := json.Unmarshal(tokenData, &token); err != nil {
		t.Fatal(err)
	}
	token["access_token"] = "expired"
	token["refresh_token"] = "refresh-1"
	token["expiry"] = time.Now().Add(-time.Hour).Format(time.RFC3339)
	expired, err := json.Marshal(token)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Set(credentialKey(Google, account.Email), expired); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.TestGoogleConnection(context.Background(), account.Email); err != nil {
		t.Fatal(err)
	}
	if tokenRequests != 2 {
		t.Fatalf("token endpoint requests = %d, want initial exchange and refresh", tokenRequests)
	}
	refreshed, err := vault.Get(credentialKey(Google, account.Email))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(refreshed), `"refresh_token":"refresh-2"`) {
		t.Fatalf("refreshed token was not saved to vault: %s", refreshed)
	}
}

func TestManagerRejectsRemoteHTTPProviderEndpoints(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = NewManager(store, newMemoryVault(), GoogleOAuthConfig{
		ClientID: "test",
		AuthURL:  "http://example.com/auth",
	})
	if err == nil {
		t.Fatal("expected non-loopback HTTP OAuth endpoint to be rejected")
	}
}
