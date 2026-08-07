package qwencode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	mb                    = 1024 * 1024
	maxReasonableLineSize = 250 * mb // 250MB sanity limit to prevent OOM from malformed or malicious files
)

// QwenRecord is one line of a Qwen Code session transcript: a self-describing
// envelope (uuid/parentUuid/sessionId/timestamp/type/provenance/cwd/version)
// wrapping a Gemini-style message payload.
//
// Record types observed in the wild:
//   - "user":        a user turn (provenance "real_user"; subtype
//     "mid_turn_user_message" for interjections, subtype "notification" with
//     provenance "system" for injected task notifications)
//   - "assistant":   a model turn (parts: thought text, plain text, functionCall)
//   - "tool_result": the outcome of one functionCall (functionResponse part
//     plus a toolCallResult envelope field)
//   - "system":      non-conversational records (skipped): telemetry and
//     attribution snapshots, plus subtype "slash_command" (slash commands are
//     never recorded as user turns, verified empirically) and subtype
//     "chat_compression" (context compaction appends this record with the
//     summary in systemPayload; the transcript is never rewritten, so the
//     append-only assumption holds through compaction)
//
// Subagent delegations (the "agent"/"task" tools) do not create separate
// transcript files: the delegation and its result are an ordinary
// functionCall/tool_result pair inline in the parent session.
//
// The "provenance" field is absent in Qwen Code versions before ~0.21.x
// (verified against 0.20.1 and 0.21.0, which write the same envelope format
// otherwise); parsing treats a missing provenance as a real record.
type QwenRecord struct {
	UUID           string              `json:"uuid"`
	ParentUUID     string              `json:"parentUuid"`
	SessionID      string              `json:"sessionId"`
	Timestamp      string              `json:"timestamp"`
	Type           string              `json:"type"`
	Subtype        string              `json:"subtype,omitempty"`
	Provenance     string              `json:"provenance,omitempty"`
	Cwd            string              `json:"cwd,omitempty"`
	Version        string              `json:"version,omitempty"`
	GitBranch      string              `json:"gitBranch,omitempty"`
	Model          string              `json:"model,omitempty"`
	Message        *QwenMessage        `json:"message,omitempty"`
	UsageMetadata  *QwenUsageMetadata  `json:"usageMetadata,omitempty"`
	ToolCallResult *QwenToolCallResult `json:"toolCallResult,omitempty"`
}

// QwenMessage is the Gemini-style message payload: role "user" or "model" with typed parts.
type QwenMessage struct {
	Role  string     `json:"role"`
	Parts []QwenPart `json:"parts"`
}

// QwenPart is one typed part of a message. Exactly one of the optional fields
// is populated: plain text (Thought=false), thinking text (Thought=true),
// a functionCall, or a functionResponse.
type QwenPart struct {
	Text             string                `json:"text,omitempty"`
	Thought          bool                  `json:"thought,omitempty"`
	FunctionCall     *QwenFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *QwenFunctionResponse `json:"functionResponse,omitempty"`
}

// QwenFunctionCall is a tool invocation requested by the model.
type QwenFunctionCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// QwenFunctionResponse carries a tool's response back to the model.
// Response typically holds {"output": "..."} on success or {"error": "..."} on failure.
type QwenFunctionResponse struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// QwenToolCallResult is the envelope-level summary of a tool call outcome on a
// tool_result record. ResultDisplay is polymorphic: a plain string for shell
// output, or an object with fileDiff/fileName/originalContent/newContent for
// file operations.
type QwenToolCallResult struct {
	CallID          string          `json:"callId"`
	Status          string          `json:"status"` // "success" or "error"
	ResultDisplay   json.RawMessage `json:"resultDisplay,omitempty"`
	ErrorType       string          `json:"errorType,omitempty"`
	ExecutionStatus string          `json:"executionStatus,omitempty"`
}

// QwenUsageMetadata carries Gemini-style token counts on assistant records.
type QwenUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
}

// QwenSession is one parsed session transcript. A session maps 1:1 to a chats
// JSONL file: `qwen --resume`/`--continue` appends to the same file, so no
// cross-file merging is needed (unlike Gemini CLI's split session files).
type QwenSession struct {
	ID          string
	FilePath    string
	Records     []QwenRecord
	StartTime   string // timestamp of the first record
	LastUpdated string // timestamp of the last record
	Cwd         string // working directory from the first record that carries one
	Version     string // Qwen Code version from the first record that carries one
}

// ParseSessionFile parses a single Qwen Code session JSONL file. Records are
// kept in file order: the transcript is append-only and append order is the
// true conversation order (resumed sessions append to the same file).
func ParseSessionFile(filePath string) (*QwenSession, error) {
	slog.Debug("ParseSessionFile: Reading Qwen session file", "path", filePath)

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = file.Close() }() // Read-only file; close errors not actionable

	// Use bufio.Reader instead of Scanner to handle arbitrarily large lines
	reader := bufio.NewReader(file)

	session := &QwenSession{FilePath: filePath}
	lineNumber := 0

	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")

		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("error reading line %d: %w", lineNumber+1, err)
		}

		atEOF := err == io.EOF
		hasContent := len(strings.TrimSpace(line)) > 0

		if hasContent || !atEOF {
			lineNumber++
		}

		if !hasContent {
			if atEOF {
				break
			}
			continue
		}

		if len(line) > maxReasonableLineSize {
			return nil, fmt.Errorf("line %d exceeds reasonable size limit (%d MB): refusing to process potentially malformed file",
				lineNumber, maxReasonableLineSize/mb)
		}

		var record QwenRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			// Log and skip corrupted lines rather than failing the entire parse:
			// the file may be mid-write when the watcher fires.
			slog.Warn("ParseSessionFile: Skipping corrupted JSONL line",
				"file", filepath.Base(filePath),
				"line", lineNumber,
				"error", err)
			if atEOF {
				break
			}
			continue
		}

		accumulateRecord(session, record)

		if atEOF {
			break
		}
	}

	if session.ID == "" {
		// Fall back to the filename stem (chats files are named <session-id>.jsonl)
		session.ID = strings.TrimSuffix(filepath.Base(filePath), ".jsonl")
	}

	slog.Debug("ParseSessionFile: Parsed Qwen session",
		"path", filePath,
		"sessionId", session.ID,
		"recordCount", len(session.Records))

	return session, nil
}

// accumulateRecord appends a record and folds its envelope metadata into the session.
func accumulateRecord(session *QwenSession, record QwenRecord) {
	session.Records = append(session.Records, record)

	if session.ID == "" && record.SessionID != "" {
		session.ID = record.SessionID
	}
	if session.StartTime == "" && record.Timestamp != "" {
		session.StartTime = record.Timestamp
	}
	if record.Timestamp > session.LastUpdated {
		session.LastUpdated = record.Timestamp
	}
	if session.Cwd == "" && record.Cwd != "" {
		session.Cwd = record.Cwd
	}
	if session.Version == "" && record.Version != "" {
		session.Version = record.Version
	}
}

// FindSessions scans a Qwen project directory's chats/ subdirectory for
// session transcripts. Returns sessions sorted by last update (most recent first).
func FindSessions(projectDir string) ([]*QwenSession, error) {
	chatsDir := filepath.Join(projectDir, "chats")

	slog.Debug("FindSessions: Scanning Qwen chats directory", "chatsDir", chatsDir)

	if _, err := os.Stat(chatsDir); os.IsNotExist(err) {
		slog.Debug("FindSessions: Chats directory does not exist", "chatsDir", chatsDir)
		return []*QwenSession{}, nil
	}

	entries, err := os.ReadDir(chatsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read chats directory: %w", err)
	}

	var sessions []*QwenSession
	parseFailures := 0
	for _, entry := range entries {
		// The chats dir also holds <session-id>.runtime.json files; only .jsonl
		// files are transcripts.
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		filePath := filepath.Join(chatsDir, entry.Name())
		session, err := ParseSessionFile(filePath)
		if err != nil {
			slog.Warn("FindSessions: Failed to parse session file, skipping",
				"file", filePath, "error", err)
			parseFailures++
			continue
		}
		if len(session.Records) == 0 {
			slog.Debug("FindSessions: Skipping empty session file", "file", filePath)
			continue
		}
		sessions = append(sessions, session)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastUpdated > sessions[j].LastUpdated
	})

	slog.Info("FindSessions: Completed Qwen chats scan",
		"chatsDir", chatsDir,
		"sessionsFound", len(sessions),
		"parseFailures", parseFailures)

	return sessions, nil
}

// FirstRealUserText returns the text of the first real user turn, used for
// slugs and readable names. Records injected by the system (notifications,
// telemetry) are skipped.
func (s *QwenSession) FirstRealUserText() string {
	for _, record := range s.Records {
		if !record.IsRealUserTurn() {
			continue
		}
		if text := record.TextContent(); text != "" {
			return text
		}
	}
	return ""
}

// IsRealUserTurn reports whether the record is a genuine user turn (typed by a
// human), as opposed to system-injected user-role records like task notifications.
func (r *QwenRecord) IsRealUserTurn() bool {
	if r.Type != "user" {
		return false
	}
	// Provenance "system" marks injected records (subtype "notification").
	// Older/absent provenance defaults to treating the record as real.
	return r.Provenance == "" || r.Provenance == "real_user"
}

// TextContent concatenates the record's plain-text parts (thought parts excluded).
func (r *QwenRecord) TextContent() string {
	if r.Message == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range r.Message.Parts {
		if part.Thought || part.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(part.Text)
	}
	return b.String()
}

// ThoughtContent concatenates the record's thinking parts.
func (r *QwenRecord) ThoughtContent() string {
	if r.Message == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range r.Message.Parts {
		if !part.Thought || part.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(part.Text)
	}
	return b.String()
}

// resultDisplayString decodes the polymorphic ResultDisplay field: a plain
// string (shell output) or an object whose fileDiff/output field is the most
// useful display form (file operations).
func resultDisplayString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		if diff, ok := obj["fileDiff"].(string); ok && diff != "" {
			return diff
		}
		if output, ok := obj["output"].(string); ok && output != "" {
			return output
		}
	}

	return ""
}
