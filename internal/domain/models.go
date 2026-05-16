// Package domain holds the core entities shared across modules.
package domain

import "time"

type Organizer struct {
	ID        string
	Name      string
	LoginName string
	Status    string
	CreatedAt time.Time
}

type AdminUser struct {
	ID        string
	LoginName string
	Status    string
	CreatedAt time.Time
}

type Event struct {
	ID                 string    `json:"id"`
	OrganizerID        string    `json:"organizer_id"`
	Name               string    `json:"name"`
	Status             string    `json:"status"`
	StartAt            time.Time `json:"start_at"`
	EndAt              time.Time `json:"end_at"`
	ExpectedCount      int       `json:"expected_count"`
	ScreenTemplateCode string    `json:"screen_template_code"`
	FlowConfigID       string    `json:"flow_config_id"`
	CreatedAt          time.Time `json:"created_at"`
}

type Participant struct {
	ID             string         `json:"id"`
	EventID        string         `json:"event_id"`
	ParticipantKey string         `json:"-"`
	IdentityType   string         `json:"identity_type"`
	IdentityValue  string         `json:"identity_value,omitempty"`
	Profile        map[string]any `json:"profile"`
	FirstSeenAt    time.Time      `json:"first_seen_at"`
}

type ExportJob struct {
	ID          string     `json:"id"`
	OrganizerID string     `json:"organizer_id"`
	EventID     string     `json:"event_id"`
	Format      string     `json:"format"`
	Status      string     `json:"status"`
	StorageKey  string     `json:"storage_key,omitempty"`
	Error       string     `json:"error,omitempty"`
	RequestedAt time.Time  `json:"requested_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}
