package integrations

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	gmailAPIBase    = "https://gmail.googleapis.com/gmail/v1/users/me"
	calendarAPIBase = "https://www.googleapis.com/calendar/v3"
)

var htmlTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

func (m *Manager) gmailBase() string {
	return strings.TrimRight(m.google.GmailAPIBase, "/")
}

func (m *Manager) calendarBase() string {
	return strings.TrimRight(m.google.CalendarAPIBase, "/")
}

type gmailMessage struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	LabelIDs     []string `json:"labelIds"`
	InternalDate string   `json:"internalDate"`
	Payload      struct {
		MimeType string `json:"mimeType"`
		Headers  []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
		Body struct {
			Data string `json:"data"`
			Size int    `json:"size"`
		} `json:"body"`
		Parts []gmailPart `json:"parts"`
	} `json:"payload"`
}

type gmailPart struct {
	PartID   string `json:"partId"`
	MimeType string `json:"mimeType"`
	Filename string `json:"filename"`
	Body     struct {
		AttachmentID string `json:"attachmentId"`
		Data         string `json:"data"`
		Size         int    `json:"size"`
	} `json:"body"`
	Parts []gmailPart `json:"parts"`
}

func (m *Manager) SearchEmails(ctx context.Context, account, query string, limit int) ([]Message, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("email search query is required")
	}
	if len(query) > 1024 {
		return nil, errors.New("email search query is too long")
	}
	if limit < 1 || limit > 25 {
		return nil, errors.New("email search limit must be between 1 and 25")
	}
	client, err := m.GoogleClient(ctx, account)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(m.gmailBase() + "/messages")
	if err != nil {
		return nil, err
	}
	params := endpoint.Query()
	params.Set("q", query)
	params.Set("maxResults", strconv.Itoa(limit))
	endpoint.RawQuery = params.Encode()
	var listing struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := doGoogleJSON(ctx, client, "GET", endpoint.String(), nil, &listing); err != nil {
		return nil, fmt.Errorf("search Gmail: %w", err)
	}

	messages := make([]Message, 0, len(listing.Messages))
	for _, entry := range listing.Messages {
		item, err := m.getGmailMessage(ctx, client, entry.ID)
		if err != nil {
			return nil, err
		}
		messages = append(messages, item)
	}
	return messages, nil
}

func (m *Manager) ReadEmail(ctx context.Context, account, messageID string) (Message, error) {
	client, err := m.GoogleClient(ctx, account)
	if err != nil {
		return Message{}, err
	}
	message, err := m.getGmailMessage(ctx, client, messageID)
	if err != nil {
		return Message{}, err
	}
	return message, nil
}

func (m *Manager) SendEmail(ctx context.Context, account, recipient, subject, body string) (string, error) {
	return m.SendEmailTo(ctx, account, []string{recipient}, subject, body)
}

func (m *Manager) SendEmailTo(ctx context.Context, account string, recipients []string, subject, body string) (string, error) {
	if len(recipients) == 0 || len(recipients) > 20 {
		return "", errors.New("email must have between 1 and 20 recipients")
	}
	to := make([]*mail.Address, 0, len(recipients))
	for _, recipient := range recipients {
		address, err := parseAddress(recipient)
		if err != nil {
			return "", fmt.Errorf("invalid email recipient: %w", err)
		}
		to = append(to, address)
	}
	if strings.TrimSpace(subject) == "" || strings.ContainsAny(subject, "\r\n") {
		return "", errors.New("email subject is required and cannot contain line breaks")
	}
	if len(body) > 1<<20 {
		return "", errors.New("email body exceeds 1 MiB")
	}
	client, err := m.GoogleClient(ctx, account)
	if err != nil {
		return "", err
	}
	raw := composeEmail(to, subject, body, nil)
	var sent struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	if err := doGoogleJSON(ctx, client, "POST", m.gmailBase()+"/messages/send", map[string]string{
		"raw": base64.RawURLEncoding.EncodeToString(raw),
	}, &sent); err != nil {
		return "", fmt.Errorf("send Gmail message: %w", err)
	}
	return sent.ID, nil
}

func (m *Manager) ReplyEmail(ctx context.Context, account, messageID, body string) (string, error) {
	client, err := m.GoogleClient(ctx, account)
	if err != nil {
		return "", err
	}
	original, err := m.getRawGmailMessage(ctx, client, messageID)
	if err != nil {
		return "", err
	}
	headers := headerMap(original.Payload.Headers)
	toHeader := headers["reply-to"]
	if toHeader == "" {
		toHeader = headers["from"]
	}
	to, err := parseAddress(toHeader)
	if err != nil {
		return "", fmt.Errorf("read original sender: %w", err)
	}
	subject := headers["subject"]
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	messageIDHeader := headers["message-id"]
	references := strings.TrimSpace(strings.Join([]string{headers["references"], messageIDHeader}, " "))
	replyHeaders := map[string]string{
		"In-Reply-To": messageIDHeader,
		"References":  references,
	}
	raw := composeEmail([]*mail.Address{to}, subject, body, replyHeaders)
	var sent struct {
		ID string `json:"id"`
	}
	endpoint := m.gmailBase() + "/messages/send"
	if err := doGoogleJSON(ctx, client, "POST", endpoint, map[string]any{
		"raw":      base64.RawURLEncoding.EncodeToString(raw),
		"threadId": original.ThreadID,
	}, &sent); err != nil {
		return "", fmt.Errorf("reply to Gmail message: %w", err)
	}
	return sent.ID, nil
}

func (m *Manager) ForwardEmail(ctx context.Context, account, messageID, recipient string) (string, error) {
	to, err := parseAddress(recipient)
	if err != nil {
		return "", fmt.Errorf("invalid email recipient: %w", err)
	}
	client, err := m.GoogleClient(ctx, account)
	if err != nil {
		return "", err
	}
	original, err := m.getRawGmailMessage(ctx, client, messageID)
	if err != nil {
		return "", err
	}
	headers := headerMap(original.Payload.Headers)
	subject := headers["subject"]
	if !strings.HasPrefix(strings.ToLower(subject), "fwd:") {
		subject = "Fwd: " + subject
	}
	body := fmt.Sprintf(
		"\n\n---------- Forwarded message ----------\nFrom: %s\nDate: %s\nSubject: %s\nTo: %s\n\n%s",
		headers["from"], headers["date"], headers["subject"], headers["to"], extractText(original.Payload.Parts, original.Payload.MimeType, original.Payload.Body.Data),
	)
	raw := composeEmail([]*mail.Address{to}, subject, body, nil)
	var sent struct {
		ID string `json:"id"`
	}
	if err := doGoogleJSON(ctx, client, "POST", m.gmailBase()+"/messages/send", map[string]string{
		"raw": base64.RawURLEncoding.EncodeToString(raw),
	}, &sent); err != nil {
		return "", fmt.Errorf("forward Gmail message: %w", err)
	}
	return sent.ID, nil
}

func (m *Manager) ArchiveEmail(ctx context.Context, account, messageID string) error {
	return m.modifyGmailMessage(ctx, account, messageID, map[string][]string{
		"removeLabelIds": {"INBOX"},
	})
}

func (m *Manager) TrashEmail(ctx context.Context, account, messageID string) error {
	client, err := m.GoogleClient(ctx, account)
	if err != nil {
		return err
	}
	if err := doGoogleJSON(ctx, client, "POST", m.gmailBase()+"/messages/"+url.PathEscape(messageID)+"/trash", map[string]string{}, nil); err != nil {
		return fmt.Errorf("move Gmail message to trash: %w", err)
	}
	return nil
}

func (m *Manager) modifyGmailMessage(ctx context.Context, account, messageID string, body any) error {
	client, err := m.GoogleClient(ctx, account)
	if err != nil {
		return err
	}
	endpoint := m.gmailBase() + "/messages/" + url.PathEscape(messageID) + "/modify"
	if err := doGoogleJSON(ctx, client, "POST", endpoint, body, nil); err != nil {
		return fmt.Errorf("update Gmail message: %w", err)
	}
	return nil
}

type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

func (m *Manager) DownloadAttachment(ctx context.Context, account, messageID, filename string) (Attachment, error) {
	client, err := m.GoogleClient(ctx, account)
	if err != nil {
		return Attachment{}, err
	}
	message, err := m.getRawGmailMessage(ctx, client, messageID)
	if err != nil {
		return Attachment{}, err
	}
	var selected *gmailPart
	var visit func([]gmailPart)
	visit = func(parts []gmailPart) {
		for i := range parts {
			part := &parts[i]
			if part.Filename != "" && strings.EqualFold(part.Filename, filename) {
				selected = part
				return
			}
			visit(part.Parts)
			if selected != nil {
				return
			}
		}
	}
	visit(message.Payload.Parts)
	if selected == nil || selected.Body.AttachmentID == "" {
		return Attachment{}, errors.New("attachment was not found on that message")
	}
	endpoint := m.gmailBase() + "/messages/" + url.PathEscape(messageID) + "/attachments/" + url.PathEscape(selected.Body.AttachmentID)
	var result struct {
		Data string `json:"data"`
		Size int    `json:"size"`
	}
	if err := getLargeJSON(ctx, client, endpoint, &result); err != nil {
		return Attachment{}, fmt.Errorf("download Gmail attachment: %w", err)
	}
	if result.Size > 25*1024*1024 {
		return Attachment{}, errors.New("attachment exceeds the 25 MiB download limit")
	}
	data, err := decodeBase64URL(result.Data)
	if err != nil {
		return Attachment{}, fmt.Errorf("decode Gmail attachment: %w", err)
	}
	if len(data) > 25*1024*1024 {
		return Attachment{}, errors.New("attachment exceeds the 25 MiB download limit")
	}
	return Attachment{
		Filename:    path.Base(strings.ReplaceAll(selected.Filename, "\\", "/")),
		ContentType: mime.TypeByExtension(path.Ext(selected.Filename)),
		Data:        data,
	}, nil
}

func (m *Manager) ListCalendarEvents(ctx context.Context, account string, from, until time.Time) ([]CalendarEvent, error) {
	if from.IsZero() {
		from = time.Now().UTC()
	}
	if until.IsZero() {
		until = from.AddDate(0, 0, 30)
	}
	if !until.After(from) {
		return nil, errors.New("calendar end time must be after start time")
	}
	client, err := m.GoogleClient(ctx, account)
	if err != nil {
		return nil, err
	}
	endpoint, _ := url.Parse(m.calendarBase() + "/calendars/primary/events")
	params := endpoint.Query()
	params.Set("timeMin", from.Format(time.RFC3339))
	params.Set("timeMax", until.Format(time.RFC3339))
	params.Set("singleEvents", "true")
	params.Set("orderBy", "startTime")
	params.Set("maxResults", "50")
	endpoint.RawQuery = params.Encode()
	var response struct {
		Items []calendarEventResource `json:"items"`
	}
	if err := getJSON(ctx, client, endpoint.String(), &response); err != nil {
		return nil, fmt.Errorf("list Google Calendar events: %w", err)
	}
	events := make([]CalendarEvent, 0, len(response.Items))
	for _, item := range response.Items {
		event, err := item.value()
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func (m *Manager) CreateCalendarEvent(ctx context.Context, account string, event CalendarEvent) (CalendarEvent, error) {
	if err := validateEvent(event); err != nil {
		return CalendarEvent{}, err
	}
	client, err := m.GoogleClient(ctx, account)
	if err != nil {
		return CalendarEvent{}, err
	}
	var response calendarEventResource
	if err := doGoogleJSON(ctx, client, "POST", m.calendarBase()+"/calendars/primary/events", calendarRequest(event), &response); err != nil {
		return CalendarEvent{}, fmt.Errorf("create Google Calendar event: %w", err)
	}
	created, err := response.value()
	if err != nil {
		return CalendarEvent{}, err
	}
	return created, nil
}

func (m *Manager) UpdateCalendarEvent(ctx context.Context, account string, event CalendarEvent) (CalendarEvent, error) {
	if event.ID == "" {
		return CalendarEvent{}, errors.New("calendar event ID is required")
	}
	if err := validateEvent(event); err != nil {
		return CalendarEvent{}, err
	}
	client, err := m.GoogleClient(ctx, account)
	if err != nil {
		return CalendarEvent{}, err
	}
	endpoint := m.calendarBase() + "/calendars/primary/events/" + url.PathEscape(event.ID)
	var response calendarEventResource
	if err := doGoogleJSON(ctx, client, "PUT", endpoint, calendarRequest(event), &response); err != nil {
		return CalendarEvent{}, fmt.Errorf("update Google Calendar event: %w", err)
	}
	updated, err := response.value()
	if err != nil {
		return CalendarEvent{}, err
	}
	return updated, nil
}

func (m *Manager) DeleteCalendarEvent(ctx context.Context, account, eventID string) error {
	if strings.TrimSpace(eventID) == "" {
		return errors.New("calendar event ID is required")
	}
	client, err := m.GoogleClient(ctx, account)
	if err != nil {
		return err
	}
	endpoint := m.calendarBase() + "/calendars/primary/events/" + url.PathEscape(eventID)
	if err := doGoogleJSON(ctx, client, "DELETE", endpoint, nil, nil); err != nil {
		return fmt.Errorf("delete Google Calendar event: %w", err)
	}
	return nil
}

func (m *Manager) getGmailMessage(ctx context.Context, client *http.Client, id string) (Message, error) {
	message, err := m.getRawGmailMessage(ctx, client, id)
	if err != nil {
		return Message{}, err
	}
	headers := headerMap(message.Payload.Headers)
	var received time.Time
	if timestamp, err := strconv.ParseInt(message.InternalDate, 10, 64); err == nil {
		received = time.UnixMilli(timestamp).UTC()
	}
	var recipients []string
	for _, header := range []string{"to", "cc"} {
		if value := headers[header]; value != "" {
			recipients = append(recipients, value)
		}
	}
	return Message{
		Provider: Google,
		ID:       message.ID,
		ThreadID: message.ThreadID,
		From:     headers["from"],
		To:       recipients,
		Subject:  headers["subject"],
		Body:     extractText(message.Payload.Parts, message.Payload.MimeType, message.Payload.Body.Data),
		Received: received,
		Unread:   containsString(message.LabelIDs, "UNREAD"),
		Labels:   message.LabelIDs,
	}, nil
}

func (m *Manager) getRawGmailMessage(ctx context.Context, client *http.Client, id string) (gmailMessage, error) {
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, `/\?#`) {
		return gmailMessage{}, errors.New("valid Gmail message ID is required")
	}
	endpoint := m.gmailBase() + "/messages/" + url.PathEscape(id) + "?format=full"
	var message gmailMessage
	if err := getJSON(ctx, client, endpoint, &message); err != nil {
		return gmailMessage{}, fmt.Errorf("read Gmail message: %w", err)
	}
	return message, nil
}

func headerMap(headers []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}) map[string]string {
	values := make(map[string]string, len(headers))
	for _, header := range headers {
		key := strings.ToLower(header.Name)
		if _, exists := values[key]; !exists {
			values[key] = cleanHeaderValue(header.Value)
		}
	}
	return values
}

func extractText(parts []gmailPart, mimeType, data string) string {
	if mimeType == "text/plain" && data != "" {
		decoded, err := decodeBase64URL(data)
		if err == nil {
			return string(decoded)
		}
	}
	var htmlText string
	for _, part := range parts {
		if text := extractText(part.Parts, part.MimeType, part.Body.Data); text != "" {
			return text
		}
		if part.MimeType == "text/html" && part.Body.Data != "" {
			decoded, err := decodeBase64URL(part.Body.Data)
			if err == nil {
				htmlText = string(decoded)
			}
		}
	}
	if data != "" {
		decoded, err := decodeBase64URL(data)
		if err == nil && mimeType == "text/plain" {
			return string(decoded)
		}
		if err == nil && mimeType == "text/html" {
			htmlText = string(decoded)
		}
	}
	if htmlText != "" {
		return strings.TrimSpace(html.UnescapeString(htmlTagPattern.ReplaceAllString(htmlText, " ")))
	}
	return ""
}

func decodeBase64URL(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func getLargeJSON(ctx context.Context, client *http.Client, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create Google API request: %w", err)
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
	const maxAttachmentResponse = 36 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAttachmentResponse+1))
	if err != nil {
		return fmt.Errorf("read Google attachment response: %w", err)
	}
	if len(body) > maxAttachmentResponse {
		return errors.New("attachment response exceeds the 36 MiB limit")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode Google attachment response: %w", err)
	}
	return nil
}

func parseAddress(value string) (*mail.Address, error) {
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n") {
		return nil, errors.New("address is empty or contains line breaks")
	}
	address, err := mail.ParseAddress(value)
	if err != nil {
		return nil, err
	}
	if address.Address == "" || !strings.Contains(address.Address, "@") {
		return nil, errors.New("address must include an email domain")
	}
	return address, nil
}

func composeEmail(to []*mail.Address, subject, body string, extraHeaders map[string]string) []byte {
	var message bytes.Buffer
	recipients := make([]string, len(to))
	for index, address := range to {
		recipients[index] = address.String()
	}
	fmt.Fprintf(&message, "To: %s\r\n", strings.Join(recipients, ", "))
	fmt.Fprintf(&message, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", cleanHeaderValue(subject)))
	for name, value := range extraHeaders {
		if value != "" {
			fmt.Fprintf(&message, "%s: %s\r\n", name, cleanHeaderValue(value))
		}
	}
	message.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	message.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return message.Bytes()
}

func cleanHeaderValue(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

func validateEvent(event CalendarEvent) error {
	if strings.TrimSpace(event.Summary) == "" {
		return errors.New("calendar event title is required")
	}
	if strings.ContainsAny(event.Summary, "\r\n") {
		return errors.New("calendar event title cannot contain line breaks")
	}
	if event.Start.IsZero() || event.End.IsZero() || !event.End.After(event.Start) {
		return errors.New("calendar event end must be after its start")
	}
	return nil
}

func calendarRequest(event CalendarEvent) map[string]any {
	return map[string]any{
		"summary":     event.Summary,
		"description": event.Description,
		"location":    event.Location,
		"start":       map[string]string{"dateTime": event.Start.Format(time.RFC3339)},
		"end":         map[string]string{"dateTime": event.End.Format(time.RFC3339)},
		"reminders": map[string]any{
			"useDefault": true,
		},
	}
}

type calendarEventResource struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Location    string `json:"location"`
	Start       struct {
		DateTime string `json:"dateTime"`
		Date     string `json:"date"`
	} `json:"start"`
	End struct {
		DateTime string `json:"dateTime"`
		Date     string `json:"date"`
	} `json:"end"`
}

func (resource calendarEventResource) value() (CalendarEvent, error) {
	start, err := parseCalendarTime(resource.Start.DateTime, resource.Start.Date)
	if err != nil {
		return CalendarEvent{}, fmt.Errorf("parse calendar event start: %w", err)
	}
	end, err := parseCalendarTime(resource.End.DateTime, resource.End.Date)
	if err != nil {
		return CalendarEvent{}, fmt.Errorf("parse calendar event end: %w", err)
	}
	return CalendarEvent{
		ID:          resource.ID,
		Summary:     resource.Summary,
		Description: resource.Description,
		Location:    resource.Location,
		Start:       start,
		End:         end,
	}, nil
}

func parseCalendarTime(dateTime, date string) (time.Time, error) {
	if dateTime != "" {
		return time.Parse(time.RFC3339, dateTime)
	}
	if date != "" {
		return time.ParseInLocation("2006-01-02", date, time.UTC)
	}
	return time.Time{}, errors.New("calendar date is missing")
}

func doGoogleJSON(ctx context.Context, client *http.Client, method, endpoint string, body, target any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Google API request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return fmt.Errorf("create Google API request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("call Google API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		errorBody, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		if readErr != nil {
			return fmt.Errorf("read Google API error response: %w", readErr)
		}
		return fmt.Errorf("Google API returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(errorBody)))
	}
	if target == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 10*1024*1024)).Decode(target); err != nil {
		return fmt.Errorf("decode Google API response: %w", err)
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
