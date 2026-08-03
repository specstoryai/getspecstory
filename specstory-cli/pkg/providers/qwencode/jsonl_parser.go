package qwencode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Qwen Code records each session as a JSONL transcript at
// ~/.qwen/projects/<sanitized-cwd>/chats/<session-id>.jsonl.
//
// Each line is a self-describing record envelope (uuid/parentUuid/sessionId/
// timestamp/type/cwd/version) wrapping a Gemini-style message payload
// (role "user"/"model" with typed parts). Qwen Code is a Gemini CLI fork, so
// the message parts use the same shapes: text, thought, functionCall and
// functionResponse.

// Record type values for QwenSessionEntry.Type.
const (
	entryTypeUser       = "user"
	entryTypeAssistant  = "assistant"
	entryTypeSystem     = "system"
	entryTypeToolResult = "tool_result"
)

// Provenance values for QwenSessionEntry.Provenance. Only records authored by
// the real user or the model map to conversation content; system and
// tool_result records carry payloads handled separately.
const (
	provenanceRealUser     = "real_user"
	provenanceAssistantOut = "assistant_output"
	provenanceSystem       = "system"
	provenanceToolResult   = "tool_result"
)

// QwenFunctionCall is a tool invocation requested by the model.
type QwenFunctionCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// QwenFunctionResponse is the payload a tool result carries inside a part.
type QwenFunctionResponse struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// QwenPart is one item of a message's parts array. Exactly one field is
// populated per part.
type QwenPart struct {
	Text             *string               `json:"text,omitempty"`
	Thought          bool                  `json:"thought,omitempty"`
	FunctionCall     *QwenFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *QwenFunctionResponse `json:"functionResponse,omitempty"`
}

// QwenMessage is the Gemini-style message payload carried by a record.
type QwenMessage struct {
	Role  string     `json:"role"` // "user" or "model"
	Parts []QwenPart `json:"parts"`
}

// QwenUsageMetadata mirrors the token accounting Qwen Code writes on
// assistant records (Gemini CLI field names).
type QwenUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	ToolsTokenCount         int `json:"toolsTokenCount"`
}

// QwenToolCallResult is the structured result Qwen Code attaches to
// tool_result records alongside the functionResponse part.
type QwenToolCallResult struct {
	CallID        string          `json:"callId"`
	Status        string          `json:"status"` // "success" or "error"
	ResultDisplay string          `json:"resultDisplay"`
	Error         json.RawMessage `json:"error,omitempty"`
	ErrorType     string          `json:"errorType,omitempty"`
}

// QwenSessionEntry is a single JSONL record.
type QwenSessionEntry struct {
	UUID           string              `json:"uuid"`
	ParentUUID     string              `json:"parentUuid"`
	SessionID      string              `json:"sessionId"`
	Timestamp      string              `json:"timestamp"`
	Type           string              `json:"type"`
	Provenance     string              `json:"provenance"`
	Subtype        string              `json:"subtype,omitempty"`
	Cwd            string              `json:"cwd,omitempty"`
	Version        string              `json:"version,omitempty"`
	GitBranch      string              `json:"gitBranch,omitempty"`
	Model          string              `json:"model,omitempty"`
	Message        *QwenMessage        `json:"message,omitempty"`
	UsageMetadata  *QwenUsageMetadata  `json:"usageMetadata,omitempty"`
	ToolCallResult *QwenToolCallResult `json:"toolCallResult,omitempty"`
	SystemPayload  json.RawMessage     `json:"systemPayload,omitempty"`
}

// QwenSession is a parsed transcript.
type QwenSession struct {
	ID          string             `json:"sessionId"`
	Cwd         string             `json:"cwd"`
	Version     string             `json:"version"`
	Model       string             `json:"model"`
	StartTime   string             `json:"startTime"`
	LastUpdated string             `json:"lastUpdated"`
	Entries     []QwenSessionEntry `json:"entries"`
	FilePath    string             `json:"-"` // Path to the session file
}

// FirstUserMessage returns the text of the first record authored by the real
// user, or "" when the transcript has none.
func (s *QwenSession) FirstUserMessage() string {
	for _, entry := range s.Entries {
		if entry.Type != entryTypeUser || entry.Provenance != provenanceRealUser {
			continue
		}
		if text := entryText(entry); text != "" {
			return text
		}
	}
	return ""
}

// entryText concatenates the plain-text parts of an entry's message.
// Thought parts are excluded — they are surfaced as thinking content, not user text.
func entryText(entry QwenSessionEntry) string {
	if entry.Message == nil {
		return ""
	}
	var parts []string
	for _, part := range entry.Message.Parts {
		if part.Text != nil && !part.Thought {
			if trimmed := strings.TrimSpace(*part.Text); trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// ParseSessionFile parses a Qwen Code JSONL transcript. Malformed lines are
// skipped with a warning rather than failing the whole session, mirroring the
// tolerance the other JSONL providers apply.
func ParseSessionFile(filePath string) (*QwenSession, error) {
	slog.Debug("ParseSessionFile: Reading session file", "path", filePath)

	file, err := os.Open(filePath)
	if err != nil {
		slog.Error("ParseSessionFile: Failed to open session file", "path", filePath, "error", err)
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	session := &QwenSession{FilePath: filePath}
	reader := bufio.NewReader(file)
	lineNum := 0

	for {
		// Why: ReadString can return data AND io.EOF on the last line (no
		// trailing newline), so always process the line first, then check EOF.
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return nil, fmt.Errorf("failed to read line: %w", readErr)
		}

		lineNum++
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			var entry QwenSessionEntry
			if jsonErr := json.Unmarshal([]byte(trimmed), &entry); jsonErr != nil {
				slog.Warn("ParseSessionFile: Skipping malformed JSONL line",
					"file", filePath,
					"line", lineNum,
					"error", jsonErr)
			} else {
				session.adopt(entry)
			}
		}

		if readErr == io.EOF {
			break
		}
	}

	// Fall back to the filename for the session ID when no record carried one.
	if session.ID == "" {
		base := strings.TrimSuffix(strings.TrimSuffix(filePath, ".jsonl"), ".runtime.json")
		if idx := strings.LastIndex(base, string(os.PathSeparator)); idx >= 0 {
			base = base[idx+1:]
		}
		session.ID = base
	}

	slog.Info("ParseSessionFile: Successfully parsed session",
		"path", filePath,
		"sessionId", session.ID,
		"startTime", session.StartTime,
		"entryCount", len(session.Entries))

	return session, nil
}

// adopt folds one record into the session: keeps the first-seen id/cwd/
// version/model and tracks the earliest/latest timestamps.
func (s *QwenSession) adopt(entry QwenSessionEntry) {
	s.Entries = append(s.Entries, entry)

	if s.ID == "" && entry.SessionID != "" {
		s.ID = entry.SessionID
	}
	if s.Cwd == "" && entry.Cwd != "" {
		s.Cwd = entry.Cwd
	}
	if s.Version == "" && entry.Version != "" {
		s.Version = entry.Version
	}
	if s.Model == "" && entry.Model != "" {
		s.Model = entry.Model
	}

	if entry.Timestamp != "" {
		if s.StartTime == "" || entry.Timestamp < s.StartTime {
			s.StartTime = entry.Timestamp
		}
		if s.LastUpdated == "" || entry.Timestamp > s.LastUpdated {
			s.LastUpdated = entry.Timestamp
		}
	}
}
