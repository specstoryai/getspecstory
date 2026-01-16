package geminicli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type GeminiToolCall struct {
	ID                     string             `json:"id"`
	Name                   string             `json:"name"`
	Args                   map[string]any     `json:"args"`
	Result                 []GeminiToolResult `json:"result"`
	Status                 string             `json:"status"`
	Timestamp              string             `json:"timestamp"`
	ResultDisplay          string             `json:"resultDisplay"`
	DisplayName            string             `json:"displayName"`
	Description            string             `json:"description"`
	RenderOutputAsMarkdown bool               `json:"renderOutputAsMarkdown"`
}

type GeminiToolResult struct {
	FunctionResponse *GeminiFunctionResponse `json:"functionResponse"`
}

type GeminiFunctionResponse struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

func (fr GeminiFunctionResponse) output() string {
	if fr.Response == nil {
		return ""
	}
	if out, ok := fr.Response["output"].(string); ok && out != "" {
		return out
	}
	if errStr, ok := fr.Response["error"].(string); ok && errStr != "" {
		return errStr
	}
	bytes, err := json.Marshal(fr.Response)
	if err != nil {
		return ""
	}
	return string(bytes)
}

type GeminiThought struct {
	Subject     string `json:"subject"`
	Description string `json:"description"`
	Timestamp   string `json:"timestamp"`
}

type GeminiTokens struct {
	Input    int `json:"input"`
	Output   int `json:"output"`
	Cached   int `json:"cached"`
	Thoughts int `json:"thoughts"`
	Tool     int `json:"tool"`
	Total    int `json:"total"`
}

type GeminiMessage struct {
	ID        string           `json:"id"`
	Timestamp string           `json:"timestamp"`
	Type      string           `json:"type"` // "user" or "gemini"
	Content   json.RawMessage  `json:"content"`
	ToolCalls []GeminiToolCall `json:"toolCalls"`
	Thoughts  []GeminiThought  `json:"thoughts"`
	Model     string           `json:"model"`
	Tokens    *GeminiTokens    `json:"tokens"`
}

type GeminiLogEntry struct {
	SessionID string `json:"sessionId"`
	MessageID int    `json:"messageId"`
	Type      string `json:"type"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

type GeminiSession struct {
	ID          string           `json:"sessionId"`
	ProjectHash string           `json:"projectHash"`
	StartTime   string           `json:"startTime"`
	LastUpdated string           `json:"lastUpdated"`
	Messages    []GeminiMessage  `json:"messages"`
	FilePath    string           `json:"-"` // Path to the session file
	Logs        []GeminiLogEntry `json:"-"`

	logIndex map[string][]GeminiLogEntry
}

func (s *GeminiSession) buildLogIndex() {
	if len(s.Logs) == 0 {
		return
	}
	index := make(map[string][]GeminiLogEntry)
	for _, entry := range s.Logs {
		key := normalizeLogKey(entry.Message)
		if key == "" {
			continue
		}
		index[key] = append(index[key], entry)
	}
	s.logIndex = index
}

func (s *GeminiSession) LogsForMessage(content string) []GeminiLogEntry {
	if len(s.Logs) == 0 {
		return nil
	}
	if s.logIndex == nil {
		s.buildLogIndex()
	}
	key := normalizeLogKey(content)
	if key == "" {
		return nil
	}
	entries := s.logIndex[key]
	if len(entries) == 0 {
		return nil
	}
	result := make([]GeminiLogEntry, len(entries))
	copy(result, entries)
	return result
}

func normalizeLogKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// extractSessionIDFromFilename extracts the short session ID prefix from a Gemini session filename.
// Supports two formats:
// 1. New format: session-<YYYY-MM-DDTHH-MM>-<short-id>.json -> "37ba9021"
// 2. Old format: session-<full-uuid>.json -> first 8 chars of UUID
// Returns empty string if the filename doesn't match either expected pattern.
// Why: Gemini CLI creates multiple files for the same session using the timestamped naming pattern,
// and we need to group them by the short ID to merge them into a single session.
// We also support the old format for backward compatibility with existing tests and older sessions.
func extractSessionIDFromFilename(filename string) string {
	// Remove .json extension
	if !strings.HasSuffix(filename, ".json") {
		return ""
	}
	base := strings.TrimSuffix(filename, ".json")

	// Remove session- prefix
	if !strings.HasPrefix(base, "session-") {
		return ""
	}
	base = strings.TrimPrefix(base, "session-")

	// Check if this is the new timestamped format: YYYY-MM-DDTHH-MM-SHORTID
	// The format has timestamps like "2025-11-19T23-22" followed by a hyphen and 8-char short ID
	lastHyphen := strings.LastIndex(base, "-")
	if lastHyphen != -1 {
		potentialShortID := base[lastHyphen+1:]
		// Check if the last part is an 8-character hex string
		if len(potentialShortID) == 8 {
			matched, _ := regexp.MatchString("^[0-9a-fA-F]{8}$", potentialShortID)
			if matched {
				return potentialShortID
			}
		}
	}

	// Fall back to old format: session-<identifier>.json
	// For old formats or test files, use the entire identifier as the short ID
	// This ensures single-file sessions still work and get grouped correctly
	if len(base) > 0 {
		// Take first 8 characters if available (for UUIDs), otherwise use the whole thing
		if len(base) >= 8 {
			return base[:8]
		}
		return base
	}

	return ""
}

// deduplicateMessages removes duplicate messages based on message ID.
// Messages are assumed to be sorted by timestamp.
// Why: When merging multiple session files, the same message might appear in multiple files
// during file transition periods. We deduplicate to ensure each message appears exactly once.
func deduplicateMessages(messages []GeminiMessage) []GeminiMessage {
	if len(messages) <= 1 {
		return messages
	}

	seen := make(map[string]bool)
	result := make([]GeminiMessage, 0, len(messages))

	for _, msg := range messages {
		if !seen[msg.ID] {
			seen[msg.ID] = true
			result = append(result, msg)
		}
	}

	return result
}

// parseAndMergeSessionFiles parses multiple session files for the same session ID
// and merges them into a single GeminiSession.
// Why: Gemini CLI splits long-running sessions across multiple files. We need to merge
// them to present a complete, chronological view of the entire session.
func parseAndMergeSessionFiles(shortID string, filePaths []string) (*GeminiSession, error) {
	if len(filePaths) == 0 {
		return nil, fmt.Errorf("no files provided for session with short ID %s", shortID)
	}

	slog.Debug("parseAndMergeSessionFiles: Parsing session files",
		"shortID", shortID,
		"fileCount", len(filePaths))

	// Parse all files
	var sessions []*GeminiSession
	for _, path := range filePaths {
		session, err := ParseSessionFile(path)
		if err != nil {
			slog.Warn("parseAndMergeSessionFiles: Failed to parse session file, skipping",
				"shortID", shortID,
				"file", path,
				"error", err)
			continue
		}

		// Validate session ID starts with the expected short ID
		if !strings.HasPrefix(session.ID, shortID) {
			slog.Warn("parseAndMergeSessionFiles: Session ID mismatch",
				"expectedPrefix", shortID,
				"actualID", session.ID,
				"file", path)
			// Continue anyway - the file matched the naming pattern
		}

		sessions = append(sessions, session)
	}

	if len(sessions) == 0 {
		return nil, fmt.Errorf("no valid session files found for short ID %s", shortID)
	}

	if len(sessions) == 1 {
		return sessions[0], nil
	}

	// Sort by startTime (earliest first)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartTime < sessions[j].StartTime
	})

	// Merge into first session (which has the earliest startTime)
	merged := sessions[0]

	for i := 1; i < len(sessions); i++ {
		// Merge messages
		merged.Messages = append(merged.Messages, sessions[i].Messages...)

		// Use latest lastUpdated
		if sessions[i].LastUpdated > merged.LastUpdated {
			merged.LastUpdated = sessions[i].LastUpdated
		}

		// Merge logs (if they exist - though typically loaded from logs.json)
		merged.Logs = append(merged.Logs, sessions[i].Logs...)
	}

	// Re-sort all messages by timestamp
	sort.SliceStable(merged.Messages, func(i, j int) bool {
		return merged.Messages[i].Timestamp < merged.Messages[j].Timestamp
	})

	// Remove duplicate messages (same ID)
	merged.Messages = deduplicateMessages(merged.Messages)

	slog.Info("parseAndMergeSessionFiles: Successfully merged session files",
		"sessionId", merged.ID,
		"fileCount", len(sessions),
		"totalMessages", len(merged.Messages),
		"startTime", merged.StartTime,
		"lastUpdated", merged.LastUpdated)

	return merged, nil
}

// ParseSessionFile parses a single Gemini session JSON file
func ParseSessionFile(filePath string) (*GeminiSession, error) {
	slog.Debug("ParseSessionFile: Reading session file", "path", filePath)

	data, err := os.ReadFile(filePath)
	if err != nil {
		slog.Error("ParseSessionFile: Failed to read session file", "path", filePath, "error", err)
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var session GeminiSession
	if err := json.Unmarshal(data, &session); err != nil {
		slog.Error("ParseSessionFile: Failed to parse session JSON", "path", filePath, "error", err)
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}
	session.FilePath = filePath

	slog.Info("ParseSessionFile: Successfully parsed session",
		"path", filePath,
		"sessionId", session.ID,
		"startTime", session.StartTime,
		"messageCount", len(session.Messages))

	return &session, nil
}

// FindSessions scans the chats directory for session files and merges multi-file sessions.
// Why: Gemini CLI creates multiple files for long-running sessions. We group files by
// session ID and merge them to present complete sessions.
func FindSessions(projectDir string) ([]*GeminiSession, error) {
	chatsDir := filepath.Join(projectDir, "chats")

	slog.Debug("FindSessions: Scanning chats directory", "projectDir", projectDir, "chatsDir", chatsDir)

	if _, err := os.Stat(chatsDir); os.IsNotExist(err) {
		slog.Debug("FindSessions: Chats directory does not exist", "chatsDir", chatsDir)
		return []*GeminiSession{}, nil
	}

	entries, err := os.ReadDir(chatsDir)
	if err != nil {
		slog.Error("FindSessions: Failed to read chats directory", "chatsDir", chatsDir, "error", err)
		return nil, fmt.Errorf("failed to read chats directory: %w", err)
	}

	slog.Debug("FindSessions: Found entries in chats directory", "chatsDir", chatsDir, "entryCount", len(entries))

	logsBySession := readGeminiLogs(projectDir)
	if len(logsBySession) > 0 {
		slog.Debug("FindSessions: Loaded logs for sessions", "sessionCount", len(logsBySession))
	}

	// Group session files by their short session ID
	// Why: Multiple files can belong to the same session
	sessionFileGroups := make(map[string][]string) // shortID -> []filePaths
	skippedFiles := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "session-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Extract the short session ID from the filename
		shortID := extractSessionIDFromFilename(entry.Name())
		if shortID == "" {
			slog.Warn("FindSessions: Could not extract session ID from filename, skipping",
				"file", entry.Name())
			skippedFiles++
			continue
		}

		filePath := filepath.Join(chatsDir, entry.Name())
		sessionFileGroups[shortID] = append(sessionFileGroups[shortID], filePath)
	}

	slog.Debug("FindSessions: Grouped session files",
		"uniqueSessions", len(sessionFileGroups),
		"skippedFiles", skippedFiles)

	// Parse and merge session files for each unique session
	var sessions []*GeminiSession
	parseFailures := 0

	for shortID, filePaths := range sessionFileGroups {
		mergedSession, err := parseAndMergeSessionFiles(shortID, filePaths)
		if err != nil {
			slog.Warn("FindSessions: Failed to parse/merge session files, skipping",
				"shortID", shortID,
				"fileCount", len(filePaths),
				"error", err)
			parseFailures++
			continue
		}

		// Attach logs to the merged session
		if logs, ok := logsBySession[mergedSession.ID]; ok {
			mergedSession.Logs = logs
			slog.Debug("FindSessions: Attached logs to merged session",
				"sessionId", mergedSession.ID,
				"logCount", len(logs))
		}

		normalizeGeminiSession(mergedSession)
		sessions = append(sessions, mergedSession)
	}

	// Sort by lastUpdated (most recent first)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastUpdated > sessions[j].LastUpdated
	})

	slog.Info("FindSessions: Completed scan",
		"projectDir", projectDir,
		"sessionsFound", len(sessions),
		"totalFiles", len(entries)-skippedFiles,
		"parseFailures", parseFailures)

	return sessions, nil
}

func readGeminiLogs(projectDir string) map[string][]GeminiLogEntry {
	result := make(map[string][]GeminiLogEntry)
	logFile := filepath.Join(projectDir, "logs.json")

	slog.Debug("readGeminiLogs: Attempting to read logs file", "logFile", logFile)

	data, err := os.ReadFile(logFile)
	if err != nil {
		// This is expected if there are no logs yet, so just DEBUG level
		slog.Debug("readGeminiLogs: Could not read logs file", "logFile", logFile, "error", err)
		return result
	}

	var entries []GeminiLogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		slog.Warn("readGeminiLogs: Failed to parse logs JSON", "logFile", logFile, "error", err)
		return result
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].SessionID == entries[j].SessionID {
			return entries[i].MessageID < entries[j].MessageID
		}
		return entries[i].SessionID < entries[j].SessionID
	})

	for _, entry := range entries {
		result[entry.SessionID] = append(result[entry.SessionID], entry)
	}

	slog.Info("readGeminiLogs: Successfully loaded logs",
		"logFile", logFile,
		"totalEntries", len(entries),
		"sessionsWithLogs", len(result))

	return result
}

func normalizeGeminiSession(session *GeminiSession) {
	if len(session.Messages) == 0 {
		return
	}

	sort.SliceStable(session.Messages, func(i, j int) bool {
		return session.Messages[i].Timestamp < session.Messages[j].Timestamp
	})

	session.Messages = trimWarmupMessages(session.Messages)
}

func trimWarmupMessages(messages []GeminiMessage) []GeminiMessage {
	firstReal := 0
	for i, msg := range messages {
		if msg.Type != "user" {
			firstReal = i
			break
		}
		content := strings.TrimSpace(msgContent(msg))
		if content == "" {
			continue
		}
		if strings.HasPrefix(content, "/") || strings.HasPrefix(strings.ToLower(content), "git ") {
			continue
		}
		firstReal = i
		break
	}

	if firstReal >= len(messages) {
		return messages
	}
	return messages[firstReal:]
}

func msgContent(msg GeminiMessage) string {
	return decodeContent(msg.Content)
}

func decodeContent(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			if text, ok := part["text"].(string); ok && text != "" {
				if builder.Len() > 0 {
					builder.WriteString("\n")
				}
				builder.WriteString(text)
			}
		}
		if builder.Len() > 0 {
			return builder.String()
		}
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		if text, ok := obj["text"].(string); ok && text != "" {
			return text
		}
		if len(obj) > 0 {
			bytes, err := json.Marshal(obj)
			if err == nil {
				return string(bytes)
			}
		}
	}

	return strings.Trim(string(raw), "\"")
}

func toolOutput(tc GeminiToolCall) string {
	if tc.ResultDisplay != "" {
		return tc.ResultDisplay
	}
	for _, result := range tc.Result {
		if result.FunctionResponse != nil {
			if output := result.FunctionResponse.output(); output != "" {
				return output
			}
		}
	}
	return ""
}
