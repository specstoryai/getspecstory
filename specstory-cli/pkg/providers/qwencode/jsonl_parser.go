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
	maxReasonableLineSize = 16 * mb // Bound allocation per record; discard oversized lines and continue.
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
// QWEN-FORMAT.md records the verified baseline and storage lifecycle. Keep
// unknown native fields in Raw for diagnostics as the format evolves.
type QwenRecord struct {
	Raw            json.RawMessage     `json:"-"` // Preserve unknown fields for debug output.
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
	InlineData *struct {
		MimeType string `json:"mimeType"`
	} `json:"inlineData,omitempty"`
	FileData *struct {
		MimeType string `json:"mimeType"`
		FileURI  string `json:"fileUri"`
	} `json:"fileData,omitempty"`
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
	ID            string
	FilePath      string
	Records       []QwenRecord
	StartTime     string // timestamp of the first record
	LastUpdated   string // timestamp of the last record
	Cwd           string // working directory from the first record that carries one
	Version       string // Qwen Code version from the first record that carries one
	FirstUserText string // Also retained by metadata-only scans.
}

// ParseSessionFile parses a single Qwen Code session JSONL file. Records are
// kept in file order: the transcript is append-only and append order is the
// true conversation order (resumed sessions append to the same file).
func ParseSessionFile(filePath string) (*QwenSession, error) {
	return parseSessionFile(filePath, false)
}

// metadataOnly keeps only envelope metadata and the first prompt, never the transcript body.
func parseSessionFile(filePath string, metadataOnly bool) (*QwenSession, error) {
	slog.Debug("ParseSessionFile: Reading Qwen session file", "path", filePath)

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = file.Close() }() // Read-only file; close errors not actionable

	reader := bufio.NewReader(file)
	session := &QwenSession{FilePath: filePath}
	for lineNumber := 1; ; lineNumber++ {
		line, oversized, err := readRecordLine(reader)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("error reading line %d: %w", lineNumber, err)
		}
		if oversized {
			slog.Warn("ParseSessionFile: Skipping oversized JSONL line", "path", filePath, "line", lineNumber, "limit", maxReasonableLineSize)
		} else if len(strings.TrimSpace(string(line))) > 0 {
			var record QwenRecord
			if parseErr := json.Unmarshal(line, &record); parseErr != nil {
				slog.Warn("ParseSessionFile: Skipping corrupted JSONL line", "path", filePath, "line", lineNumber, "error", parseErr)
			} else {
				if !metadataOnly {
					record.Raw = line
				}
				accumulateRecord(session, record)
				if metadataOnly {
					session.Records = nil
				}
			}
		}
		if err == io.EOF {
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

// readRecordLine bounds allocation before appending each buffer fragment. An
// oversized record is drained through its newline so subsequent turns survive.
func readRecordLine(reader *bufio.Reader) ([]byte, bool, error) {
	var line []byte
	oversized := false
	for {
		fragment, err := reader.ReadSlice('\n')
		if !oversized {
			if len(line)+len(fragment) > maxReasonableLineSize {
				oversized = true
				line = nil
			} else {
				line = append(line, fragment...)
			}
		}
		if err != bufio.ErrBufferFull {
			return line, oversized, err
		}
	}
}

// accumulateRecord appends a record and folds its envelope metadata into the session.
func accumulateRecord(session *QwenSession, record QwenRecord) {
	session.Records = append(session.Records, record)
	if session.FirstUserText == "" && record.IsRealUserTurn() {
		session.FirstUserText = strings.TrimSpace(record.TextContent())
	}

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
	return findSessions(projectDir, false, nil)
}

func findSessions(projectDir string, metadataOnly bool, progress func(int, int)) ([]*QwenSession, error) {
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

	total := 0
	for _, entry := range entries {
		if entry.Type().IsRegular() && isSessionFile(entry.Name()) {
			total++
		}
	}
	current := 0
	var sessions []*QwenSession
	parseFailures := 0
	for _, entry := range entries {
		// The chats dir also holds <session-id>.runtime.json files; only .jsonl
		// files are transcripts.
		if !entry.Type().IsRegular() || !isSessionFile(entry.Name()) {
			continue
		}

		filePath := filepath.Join(chatsDir, entry.Name())
		session, err := parseSessionFile(filePath, metadataOnly)
		current++
		if progress != nil {
			progress(current, total)
		}
		if err != nil {
			slog.Warn("FindSessions: Failed to parse session file, skipping",
				"file", filePath, "error", err)
			parseFailures++
			continue
		}
		if session.FirstRealUserText() == "" {
			slog.Debug("FindSessions: Skipping empty session file", "file", filePath)
			continue
		}
		sessions = append(sessions, session)
	}

	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].LastUpdated == sessions[j].LastUpdated {
			return sessions[i].ID < sessions[j].ID
		}
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
	if s.FirstUserText != "" {
		return s.FirstUserText
	}
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
		if part.Thought || part.displayText() == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(part.displayText())
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
		b.WriteString(part.displayText())
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

// displayText makes non-text attachments visible without placing base64 image
// bytes in markdown. The complete payload stays in RawData and debug records.
func (p QwenPart) displayText() string {
	if p.Text != "" {
		return p.Text
	}
	if p.InlineData != nil {
		return fmt.Sprintf("[Attachment: %s]", p.InlineData.MimeType)
	}
	if p.FileData != nil {
		return fmt.Sprintf("[Attachment: %s (%s)]", p.FileData.FileURI, p.FileData.MimeType)
	}
	return ""
}
