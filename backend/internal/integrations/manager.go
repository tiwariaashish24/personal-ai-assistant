package integrations

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

const (
	keyringService = "JARVIS.integrations"
	googleProfile  = "https://gmail.googleapis.com/gmail/v1/users/me/profile"
)

type CredentialVault interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

type systemVault struct{}

func (systemVault) Get(key string) ([]byte, error) {
	value, err := keyring.Get(keyringService, key)
	if err != nil {
		return nil, err
	}
	return []byte(value), nil
}

func (systemVault) Set(key string, value []byte) error {
	return keyring.Set(keyringService, key, string(value))
}

func (systemVault) Delete(key string) error {
	return keyring.Delete(keyringService, key)
}

type GoogleOAuthConfig struct {
	ClientID        string
	ClientSecret    string
	AuthURL         string
	TokenURL        string
	ProfileURL      string
	GmailAPIBase    string
	CalendarAPIBase string
}

type Manager struct {
	store  *Store
	vault  CredentialVault
	google GoogleOAuthConfig
}

func NewManager(store *Store, vault CredentialVault, google GoogleOAuthConfig) (*Manager, error) {
	if store == nil || vault == nil {
		return nil, errors.New("integration store and credential vault are required")
	}
	if google.AuthURL == "" {
		google.AuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if google.TokenURL == "" {
		google.TokenURL = "https://oauth2.googleapis.com/token"
	}
	if google.ProfileURL == "" {
		google.ProfileURL = googleProfile
	}
	if google.GmailAPIBase == "" {
		google.GmailAPIBase = "https://gmail.googleapis.com/gmail/v1/users/me"
	}
	if google.CalendarAPIBase == "" {
		google.CalendarAPIBase = "https://www.googleapis.com/calendar/v3"
	}
	for _, endpoint := range []string{
		google.AuthURL, google.TokenURL, google.ProfileURL, google.GmailAPIBase, google.CalendarAPIBase,
	} {
		parsed, err := url.ParseRequestURI(endpoint)
		if err != nil || parsed.Host == "" || parsed.User != nil {
			return nil, fmt.Errorf("integration endpoint must be an absolute URL: %q", endpoint)
		}
		if parsed.Scheme == "https" {
			continue
		}
		ip := net.ParseIP(parsed.Hostname())
		if parsed.Scheme != "http" || parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("integration endpoint must use HTTPS or loopback HTTP: %q", endpoint)
		}
	}
	return &Manager{
		store:  store,
		vault:  vault,
		google: google,
	}, nil
}

func NewSystemManager(databasePath, clientID, clientSecret string) (*Manager, error) {
	store, err := OpenStore(databasePath)
	if err != nil {
		return nil, err
	}
	manager, err := NewManager(store, systemVault{}, GoogleOAuthConfig{
		ClientID: clientID, ClientSecret: clientSecret,
	})
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Close() error {
	return m.store.Close()
}

func (m *Manager) Accounts() ([]Account, error) {
	return m.store.Accounts()
}

func (m *Manager) Status() ([]ProviderStatus, error) {
	accounts, err := m.store.Accounts()
	if err != nil {
		return nil, err
	}
	emergencyStopped, err := m.store.EmergencyStopped()
	if err != nil {
		return nil, err
	}
	googleAccounts := make([]Account, 0)
	for _, account := range accounts {
		if account.Provider == Google {
			googleAccounts = append(googleAccounts, account)
		}
	}
	return []ProviderStatus{{
		Provider:         Google,
		Name:             "Google Workspace",
		Configured:       m.GoogleConfigured(),
		EmergencyStopped: emergencyStopped,
		Accounts:         googleAccounts,
		Capabilities: []string{
			"Gmail: search, read, send, reply, forward, archive, trash, attachment download",
			"Google Calendar: list, create, update, and delete events",
		},
	}}, nil
}

func (m *Manager) Permissions() ([]Permission, error) {
	return m.store.Permissions()
}

func (m *Manager) SetPermission(provider, action, resource, decision string) error {
	return m.store.SetPermission(provider, action, resource, decision)
}

func (m *Manager) EmergencyStopped() (bool, error) {
	return m.store.EmergencyStopped()
}

func (m *Manager) SetEmergencyStopped(stopped bool) error {
	return m.store.SetEmergencyStopped(stopped)
}

func (m *Manager) SetAccountPermission(provider, account, action, resource, decision string) error {
	return m.store.SetAccountPermission(provider, account, action, resource, decision)
}

func (m *Manager) Permission(provider, action, resource string) (string, error) {
	return m.store.Permission(provider, action, resource)
}

func (m *Manager) PermissionFor(provider, account, action, resource string) (string, error) {
	return m.store.PermissionFor(provider, account, action, resource)
}

func (m *Manager) AccountPermission(provider, account, action, resource string) (string, bool, error) {
	return m.store.AccountPermission(provider, account, action, resource)
}

func (m *Manager) AuditTrail(limit int) ([]AuditEvent, error) {
	return m.store.AuditTrail(limit)
}

func (m *Manager) RecordAudit(event AuditEvent) error {
	return m.store.RecordAudit(event)
}

func (m *Manager) BeginAudit(event AuditEvent) (int64, error) {
	return m.store.BeginAudit(event)
}

func (m *Manager) FinishAudit(id int64, result string) error {
	return m.store.FinishAudit(id, result)
}

func (m *Manager) GoogleConfigured() bool {
	return strings.TrimSpace(m.google.ClientID) != ""
}

func (m *Manager) GoogleSetupStatus() string {
	if !m.GoogleConfigured() {
		return "Google OAuth is not configured. Set JARVIS_GOOGLE_CLIENT_ID; see backend/README.md."
	}
	return "Google OAuth client ID is configured."
}

func (m *Manager) Connect(ctx context.Context, provider string, openBrowser func(string) error) (Account, error) {
	if provider != Google {
		return Account{}, fmt.Errorf("provider %q is not implemented yet", provider)
	}
	stopped, err := m.store.EmergencyStopped()
	if err != nil {
		return Account{}, err
	}
	if stopped {
		return Account{}, errors.New("communication emergency stop is active; reconnect is disabled")
	}
	if strings.TrimSpace(m.google.ClientID) == "" {
		return Account{}, errors.New("JARVIS_GOOGLE_CLIENT_ID is required; run configure google for setup instructions")
	}
	if openBrowser == nil {
		return Account{}, errors.New("browser opener is required")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Account{}, fmt.Errorf("start OAuth callback listener: %w", err)
	}
	defer listener.Close()

	state, err := randomURLToken(32)
	if err != nil {
		return Account{}, fmt.Errorf("create OAuth state: %w", err)
	}
	verifier := oauth2.GenerateVerifier()
	callbackURL := "http://" + listener.Addr().String() + "/oauth2/callback"
	oauthConfig := m.googleConfig(callbackURL)

	type callbackResult struct {
		code string
		err  error
	}
	result := make(chan callbackResult, 1)
	var once sync.Once
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/oauth2/callback" || request.Method != http.MethodGet {
				http.NotFound(writer, request)
				return
			}
			query := request.URL.Query()
			if subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(state)) != 1 {
				http.Error(writer, "OAuth state validation failed. Return to JARVIS and try connecting again.", http.StatusBadRequest)
				return
			}
			if providerError := query.Get("error"); providerError != "" {
				once.Do(func() { result <- callbackResult{err: fmt.Errorf("Google authorization failed: %s", providerError)} })
				fmt.Fprintln(writer, "Google authorization was not completed. You may close this tab.")
				return
			}
			code := query.Get("code")
			if code == "" {
				once.Do(func() {
					result <- callbackResult{err: errors.New("OAuth callback did not include an authorization code")}
				})
				http.Error(writer, "Authorization code missing.", http.StatusBadRequest)
				return
			}
			once.Do(func() { result <- callbackResult{code: code} })
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(writer, "Google authorization was received. Return to JARVIS to finish connecting.")
		}),
	}
	serveErr := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	authURL := oauthConfig.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.SetAuthURLParam("include_granted_scopes", "true"),
		oauth2.S256ChallengeOption(verifier),
	)
	if err := openBrowser(authURL); err != nil {
		return Account{}, fmt.Errorf("open Google authorization page: %w", err)
	}

	var callback callbackResult
	select {
	case callback = <-result:
	case err := <-serveErr:
		return Account{}, fmt.Errorf("OAuth callback server failed: %w", err)
	case <-ctx.Done():
		return Account{}, fmt.Errorf("wait for Google authorization: %w", ctx.Err())
	case <-time.After(5 * time.Minute):
		return Account{}, errors.New("Google authorization timed out after 5 minutes")
	}
	if callback.err != nil {
		return Account{}, callback.err
	}

	token, err := oauthConfig.Exchange(ctx, callback.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Account{}, fmt.Errorf("exchange Google authorization code: %w", err)
	}
	if token.RefreshToken == "" {
		return Account{}, errors.New("Google did not return a refresh token; remove JARVIS from your Google account permissions and connect again")
	}
	httpClient := oauth2.NewClient(ctx, oauthConfig.TokenSource(ctx, token))
	email, err := m.googleEmail(ctx, httpClient)
	if err != nil {
		return Account{}, err
	}
	account := Account{Provider: Google, Email: strings.ToLower(email), ConnectedAt: time.Now().UTC()}
	tokenBytes, err := json.Marshal(token)
	if err != nil {
		return Account{}, fmt.Errorf("encode Google tokens: %w", err)
	}
	key := credentialKey(account.Provider, account.Email)
	if err := m.vault.Set(key, tokenBytes); err != nil {
		return Account{}, fmt.Errorf("store Google tokens in the OS credential vault: %w", err)
	}
	if err := m.store.SaveAccount(account); err != nil {
		if deleteErr := m.vault.Delete(key); deleteErr != nil {
			return Account{}, errors.Join(fmt.Errorf("save Google account: %w", err), fmt.Errorf("remove unregistered Google token: %w", deleteErr))
		}
		return Account{}, fmt.Errorf("save Google account: %w", err)
	}
	return account, nil
}

func (m *Manager) Disconnect(provider, email string) error {
	if provider != Google {
		return fmt.Errorf("provider %q is not implemented yet", provider)
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return errors.New("account email is required")
	}
	if err := m.vault.Delete(credentialKey(provider, email)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("remove account credentials from the OS vault: %w", err)
	}
	if err := m.store.RemoveAccount(provider, email); err != nil {
		return err
	}
	return nil
}

func (m *Manager) GoogleClient(ctx context.Context, email string) (*http.Client, error) {
	stopped, err := m.store.EmergencyStopped()
	if err != nil {
		return nil, err
	}
	if stopped {
		return nil, errors.New("communication emergency stop is active")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, errors.New("Google account email is required")
	}
	connected, err := m.store.HasAccount(Google, email)
	if err != nil {
		return nil, err
	}
	if !connected {
		return nil, fmt.Errorf("Google account %s is not connected", email)
	}
	tokenBytes, err := m.vault.Get(credentialKey(Google, email))
	if err != nil {
		return nil, fmt.Errorf("load Google credentials from the OS vault: %w", err)
	}
	var token oauth2.Token
	if err := json.Unmarshal(tokenBytes, &token); err != nil {
		return nil, fmt.Errorf("decode Google credentials: %w", err)
	}
	source := &persistingTokenSource{
		source: m.googleConfig("").TokenSource(ctx, &token),
		vault:  m.vault,
		key:    credentialKey(Google, email),
		token:  &token,
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &oauth2.Transport{
			Source: source,
			Base:   http.DefaultTransport,
		},
	}, nil
}

func (m *Manager) TestGoogleConnection(ctx context.Context, email string) (string, error) {
	client, err := m.GoogleClient(ctx, email)
	if err != nil {
		return "", err
	}
	var profile struct {
		EmailAddress  string `json:"emailAddress"`
		MessagesTotal int    `json:"messagesTotal"`
		ThreadsTotal  int    `json:"threadsTotal"`
	}
	if err := getJSON(ctx, client, m.google.ProfileURL, &profile); err != nil {
		return "", fmt.Errorf("test Google connection: %w", err)
	}
	var calendar struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	calendarURL := strings.TrimRight(m.google.CalendarAPIBase, "/") + "/calendars/primary/events?maxResults=1"
	if err := getJSON(ctx, client, calendarURL, &calendar); err != nil {
		return "", fmt.Errorf("test Google Calendar connection: %w", err)
	}
	return fmt.Sprintf("Connected to Gmail as %s (%d messages) and Google Calendar.", profile.EmailAddress, profile.MessagesTotal), nil
}

func (m *Manager) googleEmail(ctx context.Context, client *http.Client) (string, error) {
	var profile struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := getJSON(ctx, client, m.google.ProfileURL, &profile); err != nil {
		return "", fmt.Errorf("read Google account profile: %w", err)
	}
	if strings.TrimSpace(profile.EmailAddress) == "" {
		return "", errors.New("Google account profile did not include an email address")
	}
	return profile.EmailAddress, nil
}

func (m *Manager) googleConfig(redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     m.google.ClientID,
		ClientSecret: m.google.ClientSecret,
		RedirectURL:  redirectURL,
		Scopes: []string{
			"openid",
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/gmail.modify",
			"https://www.googleapis.com/auth/gmail.send",
			"https://www.googleapis.com/auth/calendar.events",
		},
		Endpoint: oauth2.Endpoint{
			AuthURL:  m.google.AuthURL,
			TokenURL: m.google.TokenURL,
		},
	}
}

func credentialKey(provider, email string) string {
	return provider + ":" + strings.ToLower(email)
}

func randomURLToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

type persistingTokenSource struct {
	mu     sync.Mutex
	source oauth2.TokenSource
	vault  CredentialVault
	key    string
	token  *oauth2.Token
}

func (s *persistingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := s.source.Token()
	if err != nil {
		return nil, err
	}
	if next.AccessToken != s.token.AccessToken || next.RefreshToken != s.token.RefreshToken || !next.Expiry.Equal(s.token.Expiry) {
		encoded, err := json.Marshal(next)
		if err != nil {
			return nil, fmt.Errorf("encode refreshed OAuth token: %w", err)
		}
		if err := s.vault.Set(s.key, encoded); err != nil {
			return nil, fmt.Errorf("persist refreshed OAuth token: %w", err)
		}
		*s.token = *next
	}
	return next, nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create API request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("call Google API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		if readErr != nil {
			return fmt.Errorf("read Google API error response: %w", readErr)
		}
		return fmt.Errorf("Google API returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(target); err != nil {
		return fmt.Errorf("decode Google API response: %w", err)
	}
	return nil
}
