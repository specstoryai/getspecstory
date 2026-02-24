// Package provenance correlates filesystem changes with AI agent activity
// to determine which file changes were caused by which agent interactions.
package provenance

import (
	"errors"
	"strings"
	"time"
)

// Validation errors returned by the Validate methods.
var (
	ErrMissingID              = errors.New("ID is required")
	ErrMissingPath            = errors.New("path is required")
	ErrMissingFilePath        = errors.New("file path is required")
	ErrPathNotAbsolute        = errors.New("path must be absolute (start with /)")
	ErrMissingTimestamp       = errors.New("timestamp is required")
	ErrMissingChangeType      = errors.New("change type is required")
	ErrInvalidFileChangeType  = errors.New("change type must be one of: create, modify, delete, rename")
	ErrInvalidAgentChangeType = errors.New("change type must be one of: create, edit, write, delete")
	ErrMissingSessionID       = errors.New("session ID is required")
	ErrMissingExchangeID      = errors.New("exchange ID is required")
	ErrMissingAgentType       = errors.New("agent type is required")
)

// FileEvent represents a filesystem change detected by the consumer's file watcher.
// Paths must be absolute with forward-slash delimiters (e.g. /Users/sean/project/src/foo.go).
type FileEvent struct {
	ID         string    // Unique identifier
	Path       string    // Absolute path, forward slashes
	ChangeType string    // "create", "modify", "delete", "rename"
	Timestamp  time.Time // When the file changed (file ModTime)
}

// validFileChangeTypes are the allowed values for FileEvent.ChangeType.
var validFileChangeTypes = map[string]bool{
	"create": true,
	"modify": true,
	"delete": true,
	"rename": true,
}

// validAgentChangeTypes are the allowed values for AgentEvent.ChangeType.
var validAgentChangeTypes = map[string]bool{
	"create": true,
	"edit":   true,
	"write":  true,
	"delete": true,
}

// Validate checks that a FileEvent has all required fields and that the path is absolute.
func (e FileEvent) Validate() error {
	if e.ID == "" {
		return ErrMissingID
	}
	if e.Path == "" {
		return ErrMissingPath
	}
	if !strings.HasPrefix(e.Path, "/") {
		return ErrPathNotAbsolute
	}
	if e.ChangeType == "" {
		return ErrMissingChangeType
	}
	if !validFileChangeTypes[e.ChangeType] {
		return ErrInvalidFileChangeType
	}
	if e.Timestamp.IsZero() {
		return ErrMissingTimestamp
	}
	return nil
}

// AgentEvent represents a file operation reported by an AI agent session.
// Paths may be relative or absolute — the library normalizes them before storage.
type AgentEvent struct {
	ID            string    // Deterministic ID for deduplication
	FilePath      string    // Path the agent touched (may be relative)
	ChangeType    string    // "create", "edit", "write", "delete"
	Timestamp     time.Time // When the operation was recorded
	SessionID     string    // Agent session ID
	ExchangeID    string    // Specific exchange within session
	MessageID     string    // Agent message ID
	AgentType     string    // "claude-code", "cursor", etc.
	AgentModel    string    // "claude-sonnet-4-20250514", etc.
	ActorHost     string    // Machine hostname
	ActorUsername string    // OS user
}

// Validate checks that an AgentEvent has all required fields.
func (e AgentEvent) Validate() error {
	if e.ID == "" {
		return ErrMissingID
	}
	if e.FilePath == "" {
		return ErrMissingFilePath
	}
	if e.ChangeType == "" {
		return ErrMissingChangeType
	}
	if !validAgentChangeTypes[e.ChangeType] {
		return ErrInvalidAgentChangeType
	}
	if e.Timestamp.IsZero() {
		return ErrMissingTimestamp
	}
	if e.SessionID == "" {
		return ErrMissingSessionID
	}
	if e.ExchangeID == "" {
		return ErrMissingExchangeID
	}
	if e.AgentType == "" {
		return ErrMissingAgentType
	}
	return nil
}

// NormalizePath normalizes an agent file path for storage and matching.
// Backslashes are replaced with forward slashes, and a leading "/" is added if missing.
func NormalizePath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

// ProvenanceRecord is the output of correlation — a file change attributed to an agent exchange.
type ProvenanceRecord struct {
	// File data
	FilePath   string    // Absolute path, forward slashes
	ChangeType string    // "create", "modify", "delete", "rename"
	Timestamp  time.Time // When file changed

	// Attribution
	SessionID  string // Agent session that caused the change
	ExchangeID string // Specific exchange that caused the change
	AgentType  string // "claude-code", "cursor", etc.
	AgentModel string // Model used
	MessageID  string // For tracing

	// Actor info
	ActorHost     string
	ActorUsername string

	// Correlation metadata
	MatchedAt time.Time // When correlation occurred
}
