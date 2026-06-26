package event

import (
	"context"
	"time"
)

// Event type constants
const (
	// Todo events
	TypeTodoUpdated = "todo.updated"

	// Message events
	TypeMessagePartDelta = "message.part.delta"

	// Session events
	TypeSessionDiff  = "session.diff"
	TypeSessionError = "session.error"

	// Permission events
	TypePermissionAsked   = "permission.asked"
	TypePermissionReplied = "permission.replied"

	// Question events
	TypeQuestionAsked    = "question.asked"
	TypeQuestionReplied  = "question.replied"
	TypeQuestionRejected = "question.rejected"

	// VCS events (reserved)
	TypeVCSBranchUpdated = "vcs.branch.updated"

	// Project events (reserved)
	TypeProjectUpdated = "project.updated"

	// LSP events (reserved)
	TypeLSPUpdated = "lsp.updated"

	// Server events (reserved)
	TypeServerConnected = "server.connected"

	// Global events (reserved)
	TypeGlobalDisposed = "global.disposed"
)

// Handler is the function signature for event handlers
type Handler func(ctx context.Context, e Event) error

// UnsubscribeFunc is used to unsubscribe from an event
type UnsubscribeFunc func()

// Event represents a generic event in the system
type Event struct {
	ID         string         `json:"id"`          // Unique event ID (ULID)
	Type       string         `json:"type"`        // Event type, e.g., "todo.updated"
	SessionID  string         `json:"session_id"`  // Optional: associated session
	ChannelID  string         `json:"channel_id"`  // Optional: target channel
	Data       any            `json:"data"`        // Event payload
	Timestamp  time.Time      `json:"timestamp"`   // Event creation time
	Metadata   map[string]any `json:"metadata"`    // Extended fields
}

// === Event Data Structures ===

// TodoUpdatedData is the payload for todo.updated events
// Contains tasks scoped to a specific parent session.
// Subscribers may filter by ParentSession if handling multiple concurrent sessions.
type TodoUpdatedData struct {
	SessionID       string `json:"session_id"`        // The parent session this update is scoped to
	ChangedTaskID   string `json:"changed_task_id"`   // Which task triggered this update (empty if all changed)
	Todos           []Todo `json:"todos"`             // All tasks for this session
}

type Todo struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Status        string `json:"status"`      // pending, running, completed, cancelled, failed
	Progress      int    `json:"progress"`    // 0-100
	ChannelID     string `json:"channel_id"`  // Which channel this task belongs to
	SessionID     string `json:"session_id"`  // Associated session
	ParentSession string `json:"parent_session"` // Parent session for background tasks
	StartedAt     int64  `json:"started_at"`
	CompletedAt   int64  `json:"completed_at,omitempty"`
	Error         string `json:"error,omitempty"`
}

// MessagePartDeltaData is the payload for message.part.delta events
type MessagePartDeltaData struct {
	MessageID string `json:"message_id"`
	PartID    string `json:"part_id"`
	Field     string `json:"field"` // e.g., "text", "thinking"
	Delta     string `json:"delta"`
}

// SessionErrorData is the payload for session.error events
type SessionErrorData struct {
	SessionID string `json:"session_id"`
	Error     string `json:"error"`
	Stack     string `json:"stack,omitempty"`
}

// SessionDiffData is the payload for session.diff events
type SessionDiffData struct {
	SessionID string   `json:"session_id"`
	Files     []string `json:"files"`
	Diff      string   `json:"diff"`
}

// PermissionAskedData is the payload for permission.asked events
type PermissionAskedData struct {
	RequestID string `json:"request_id"`
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	SessionID string `json:"session_id"`
}

// PermissionRepliedData is the payload for permission.replied events
type PermissionRepliedData struct {
	RequestID string `json:"request_id"`
	Reply     string `json:"reply"` // allow, reject, once
	SessionID string `json:"session_id"`
}

// QuestionAskedData is the payload for question.asked events
type QuestionAskedData struct {
	RequestID string   `json:"request_id"`
	Question  string   `json:"question"`
	Options   []string `json:"options"`
	SessionID string   `json:"session_id"`
}

// QuestionRepliedData is the payload for question.replied events
type QuestionRepliedData struct {
	RequestID string `json:"request_id"`
	Answer    string `json:"answer"`
	SessionID string `json:"session_id"`
}

// QuestionRejectedData is the payload for question.rejected events
type QuestionRejectedData struct {
	RequestID string `json:"request_id"`
	SessionID string `json:"session_id"`
}

// === Reserved Event Data Structures ===

// VCSBranchUpdatedData is the payload for vcs.branch.updated events (reserved)
type VCSBranchUpdatedData struct {
	Project string `json:"project"`
	Branch  string `json:"branch"`
}

// ProjectUpdatedData is the payload for project.updated events (reserved)
type ProjectUpdatedData struct {
	Project string   `json:"project"`
	Changes []string `json:"changes"`
}

// LSPUpdatedData is the payload for lsp.updated events (reserved)
type LSPUpdatedData struct {
	Status      string `json:"status"`
	Diagnostics []any  `json:"diagnostics"`
}

// ServerConnectedData is the payload for server.connected events (reserved)
type ServerConnectedData struct {
	ClientID string `json:"client_id"`
}

// GlobalDisposedData is the payload for global.disposed events (reserved)
type GlobalDisposedData struct{}
