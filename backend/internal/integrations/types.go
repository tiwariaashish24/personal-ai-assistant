package integrations

import "time"

const Google = "google"

type Account struct {
	Provider    string    `json:"provider"`
	Email       string    `json:"email"`
	ConnectedAt time.Time `json:"connectedAt"`
}

type ProviderStatus struct {
	Provider         string    `json:"provider"`
	Name             string    `json:"name"`
	Configured       bool      `json:"configured"`
	EmergencyStopped bool      `json:"emergencyStopped"`
	Accounts         []Account `json:"accounts"`
	Capabilities     []string  `json:"capabilities"`
}

type Message struct {
	Provider string    `json:"provider"`
	ID       string    `json:"id"`
	ThreadID string    `json:"threadId,omitempty"`
	From     string    `json:"from"`
	To       []string  `json:"to,omitempty"`
	Subject  string    `json:"subject"`
	Body     string    `json:"body"`
	Received time.Time `json:"received,omitempty"`
	Unread   bool      `json:"unread"`
	Labels   []string  `json:"labels,omitempty"`
}

type Notification struct {
	Provider string    `json:"provider"`
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title"`
	Body     string    `json:"body,omitempty"`
	Received time.Time `json:"received,omitempty"`
}

type CalendarEvent struct {
	ID          string    `json:"id,omitempty"`
	Summary     string    `json:"summary"`
	Description string    `json:"description,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Location    string    `json:"location,omitempty"`
}

type AuditEvent struct {
	ID        int64     `json:"id"`
	Provider  string    `json:"provider"`
	Account   string    `json:"account,omitempty"`
	Action    string    `json:"action"`
	Target    string    `json:"target,omitempty"`
	Result    string    `json:"result"`
	CreatedAt time.Time `json:"createdAt"`
}

type Permission struct {
	Provider string `json:"provider"`
	Account  string `json:"account,omitempty"`
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Decision string `json:"decision"`
}
