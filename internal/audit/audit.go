// Package audit defines the append-only audit trail shape shared by repo and
// handlers (DESIGN §3.6). Writes go through repo (the only SQL place).
package audit

import "time"

// Entry is one audit record to append.
type Entry struct {
	ActorRole string         // admin | organizer | system
	ActorID   string         // uuid or "" for system
	Action    string         // e.g. organizer_delete, config_set, lottery_draw
	Target    string         // affected resource id/desc
	Meta      map[string]any // structured detail
	RequestID string
}

// Row is one audit record read back.
type Row struct {
	ID        string         `json:"id"`
	ActorRole string         `json:"actor_role"`
	ActorID   string         `json:"actor_id,omitempty"`
	Action    string         `json:"action"`
	Target    string         `json:"target,omitempty"`
	Meta      map[string]any `json:"meta"`
	RequestID string         `json:"request_id,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
