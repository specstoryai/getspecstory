package opencode

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadProject loads a project from its JSON file.
// Project files are stored at: storage/project/{projectHash}.json
func LoadProject(projectHash string) (*Project, error) {
	slog.Debug("LoadProject: Loading project", "projectHash", projectHash)

	projectPath, err := GetProjectFilePath(projectHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get project file path: %w", err)
	}

	data, err := os.ReadFile(projectPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("LoadProject: Project file not found", "path", projectPath)
			return nil, fmt.Errorf("project file not found: %s", projectPath)
		}
		return nil, fmt.Errorf("failed to read project file: %w", err)
	}

	var project Project
	if err := json.Unmarshal(data, &project); err != nil {
		slog.Error("LoadProject: Failed to parse project JSON", "path", projectPath, "error", err)
		return nil, fmt.Errorf("%s", formatJSONParseError(projectPath, err))
	}

	slog.Debug("LoadProject: Successfully loaded project",
		"projectHash", projectHash,
		"projectID", project.ID,
		"worktree", project.Worktree)

	return &project, nil
}

// LoadSession loads a session from its JSON file.
// Session files are stored at: storage/session/{projectHash}/ses_{id}.json
func LoadSession(projectHash, sessionID string) (*Session, error) {
	slog.Debug("LoadSession: Loading session",
		"projectHash", projectHash,
		"sessionID", sessionID)

	sessionsDir, err := GetSessionsDir(projectHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions directory: %w", err)
	}

	// Session files on disk are named with "ses_" prefix (e.g., ses_abc123.json).
	// However, callers may pass either the raw ID ("abc123") or the full filename
	// stem ("ses_abc123"). This logic handles both cases:
	//   1. First try sessionID as-is (handles "ses_abc123" or any direct filename match)
	//   2. If not found and sessionID lacks "ses_" prefix, try adding it (handles "abc123")
	// This flexibility allows callers to use session IDs from different sources without
	// needing to know the exact file naming convention.
	sessionPath := filepath.Join(sessionsDir, sessionID+".json")
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		if !strings.HasPrefix(sessionID, "ses_") {
			sessionPath = filepath.Join(sessionsDir, "ses_"+sessionID+".json")
		}
	}

	data, err := os.ReadFile(sessionPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("LoadSession: Session file not found", "path", sessionPath)
			return nil, fmt.Errorf("session file not found: %s", sessionPath)
		}
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		slog.Error("LoadSession: Failed to parse session JSON", "path", sessionPath, "error", err)
		return nil, fmt.Errorf("%s", formatJSONParseError(sessionPath, err))
	}

	slog.Debug("LoadSession: Successfully loaded session",
		"sessionID", session.ID,
		"title", session.Title,
		"created", session.Time.Created)

	return &session, nil
}

// LoadSessionsForProject loads all sessions for a project.
// Scans storage/session/{projectHash}/ for all session JSON files.
func LoadSessionsForProject(projectHash string) ([]Session, error) {
	slog.Debug("LoadSessionsForProject: Loading sessions for project", "projectHash", projectHash)

	sessionsDir, err := GetSessionsDir(projectHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions directory: %w", err)
	}

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("LoadSessionsForProject: Sessions directory not found", "path", sessionsDir)
			return []Session{}, nil
		}
		return nil, fmt.Errorf("failed to read sessions directory: %w", err)
	}

	var sessions []Session
	var parseErrors int

	for _, entry := range entries {
		// Skip directories and non-JSON files
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Skip files that don't start with "ses_"
		if !strings.HasPrefix(entry.Name(), "ses_") {
			continue
		}

		sessionPath := filepath.Join(sessionsDir, entry.Name())
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			slog.Debug("LoadSessionsForProject: Failed to read session file",
				"path", sessionPath, "error", err)
			parseErrors++
			continue
		}

		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			slog.Debug("LoadSessionsForProject: Failed to parse session JSON",
				"path", sessionPath, "error", err,
				"formattedError", formatJSONParseError(sessionPath, err))
			parseErrors++
			continue
		}

		sessions = append(sessions, session)
	}

	// Sort sessions by creation time (newest first)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Time.Created > sessions[j].Time.Created
	})

	slog.Debug("LoadSessionsForProject: Loaded sessions",
		"projectHash", projectHash,
		"sessionCount", len(sessions),
		"parseErrors", parseErrors)

	return sessions, nil
}

// LoadMessagesForSession loads all messages for a session.
// Scans storage/message/{sessionID}/ for all message JSON files.
// Messages are sorted by creation time (oldest first).
func LoadMessagesForSession(sessionID string) ([]Message, error) {
	slog.Debug("LoadMessagesForSession: Loading messages for session", "sessionID", sessionID)

	messagesDir, err := GetMessagesDir(sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages directory: %w", err)
	}

	entries, err := os.ReadDir(messagesDir)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("LoadMessagesForSession: Messages directory not found", "path", messagesDir)
			return []Message{}, nil
		}
		return nil, fmt.Errorf("failed to read messages directory: %w", err)
	}

	var messages []Message
	var parseErrors int

	for _, entry := range entries {
		// Skip directories and non-JSON files
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Skip files that don't start with "msg_"
		if !strings.HasPrefix(entry.Name(), "msg_") {
			continue
		}

		messagePath := filepath.Join(messagesDir, entry.Name())
		data, err := os.ReadFile(messagePath)
		if err != nil {
			slog.Debug("LoadMessagesForSession: Failed to read message file",
				"path", messagePath, "error", err)
			parseErrors++
			continue
		}

		var message Message
		if err := json.Unmarshal(data, &message); err != nil {
			slog.Debug("LoadMessagesForSession: Failed to parse message JSON",
				"path", messagePath, "error", err,
				"formattedError", formatJSONParseError(messagePath, err))
			parseErrors++
			continue
		}

		messages = append(messages, message)
	}

	// Sort messages by creation time (oldest first for chronological order)
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Time.Created < messages[j].Time.Created
	})

	slog.Debug("LoadMessagesForSession: Loaded messages",
		"sessionID", sessionID,
		"messageCount", len(messages),
		"parseErrors", parseErrors)

	return messages, nil
}

// LoadPartsForMessage loads all parts for a message.
// Scans storage/part/{messageID}/ for all part JSON files.
// Parts are sorted by time (using Start time if available, falling back to ID order).
func LoadPartsForMessage(messageID string) ([]Part, error) {
	slog.Debug("LoadPartsForMessage: Loading parts for message", "messageID", messageID)

	partsDir, err := GetPartsDir(messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get parts directory: %w", err)
	}

	entries, err := os.ReadDir(partsDir)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("LoadPartsForMessage: Parts directory not found", "path", partsDir)
			return []Part{}, nil
		}
		return nil, fmt.Errorf("failed to read parts directory: %w", err)
	}

	var parts []Part
	var parseErrors int

	for _, entry := range entries {
		// Skip directories and non-JSON files
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Skip files that don't start with "prt_"
		if !strings.HasPrefix(entry.Name(), "prt_") {
			continue
		}

		partPath := filepath.Join(partsDir, entry.Name())
		data, err := os.ReadFile(partPath)
		if err != nil {
			slog.Debug("LoadPartsForMessage: Failed to read part file",
				"path", partPath, "error", err)
			parseErrors++
			continue
		}

		var part Part
		if err := json.Unmarshal(data, &part); err != nil {
			slog.Debug("LoadPartsForMessage: Failed to parse part JSON",
				"path", partPath, "error", err,
				"formattedError", formatJSONParseError(partPath, err))
			parseErrors++
			continue
		}

		parts = append(parts, part)
	}

	// Sort parts by time (Start time if available)
	sort.Slice(parts, func(i, j int) bool {
		return partSortKey(parts[i]) < partSortKey(parts[j])
	})

	slog.Debug("LoadPartsForMessage: Loaded parts",
		"messageID", messageID,
		"partCount", len(parts),
		"parseErrors", parseErrors)

	return parts, nil
}

// partSortKey returns a string key for sorting parts by time.
// Uses the Start time if available (as zero-padded int64 string for proper sorting),
// otherwise falls back to part ID.
func partSortKey(p Part) string {
	if p.Time != nil && p.Time.Start != nil {
		// Format as zero-padded string to ensure proper lexicographic sorting
		return fmt.Sprintf("%020d", *p.Time.Start)
	}
	// Fall back to ID when time is unavailable. This assumes opencode generates
	// part IDs in chronological order (e.g., using ULIDs or sequential counters).
	// If this assumption is wrong, parts without timestamps may appear out of order.
	return p.ID
}

// AssembleFullSession assembles a complete session with all its messages and parts.
// This loads the project (if available), all messages for the session, and all parts for each message.
func AssembleFullSession(session *Session) (*FullSession, error) {
	if session == nil {
		return nil, fmt.Errorf("session is nil")
	}

	slog.Debug("AssembleFullSession: Assembling full session",
		"sessionID", session.ID,
		"projectID", session.ProjectID)

	// Load project (optional - may not exist)
	var project *Project
	if session.ProjectID != "" {
		var err error
		project, err = LoadProject(session.ProjectID)
		if err != nil {
			// Project file is optional, log but don't fail
			slog.Debug("AssembleFullSession: Could not load project, continuing without it",
				"projectID", session.ProjectID,
				"error", err)
		}
	}

	// Load all messages for this session
	messages, err := LoadMessagesForSession(session.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load messages: %w", err)
	}

	// Load parts for each message
	var fullMessages []FullMessage
	for _, msg := range messages {
		parts, err := LoadPartsForMessage(msg.ID)
		if err != nil {
			slog.Debug("AssembleFullSession: Failed to load parts for message",
				"messageID", msg.ID,
				"error", err)
			// Continue with empty parts rather than failing
			parts = []Part{}
		}

		fullMessages = append(fullMessages, FullMessage{
			Message: &msg,
			Parts:   parts,
		})
	}

	fullSession := &FullSession{
		Session:  session,
		Project:  project,
		Messages: fullMessages,
	}

	slog.Info("AssembleFullSession: Successfully assembled full session",
		"sessionID", session.ID,
		"messageCount", len(fullMessages),
		"hasProject", project != nil)

	return fullSession, nil
}

// LoadAndAssembleSession is a convenience function that loads a session by ID
// and assembles it with all messages and parts.
func LoadAndAssembleSession(projectHash, sessionID string) (*FullSession, error) {
	slog.Debug("LoadAndAssembleSession: Loading and assembling session",
		"projectHash", projectHash,
		"sessionID", sessionID)

	session, err := LoadSession(projectHash, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	return AssembleFullSession(session)
}

// LoadAllSessionsForProject loads all sessions for a project and assembles them
// with all their messages and parts.
//
// Note: This function loads ALL sessions with ALL their messages and parts into memory
// simultaneously. For projects with many large sessions, this could consume significant
// memory. Consider using LoadSessionsForProject and AssembleFullSession individually
// if memory usage becomes a concern.
//
// Sessions that fail to assemble are logged and skipped; they are not included in the
// returned slice. As a result, the returned slice may contain fewer sessions than exist
// on disk for the given project.
func LoadAllSessionsForProject(projectHash string) ([]*FullSession, error) {
	slog.Debug("LoadAllSessionsForProject: Loading all sessions for project", "projectHash", projectHash)

	sessions, err := LoadSessionsForProject(projectHash)
	if err != nil {
		return nil, fmt.Errorf("failed to load sessions: %w", err)
	}

	var fullSessions []*FullSession
	var assembleErrors int

	for i := range sessions {
		fullSession, err := AssembleFullSession(&sessions[i])
		if err != nil {
			slog.Debug("LoadAllSessionsForProject: Failed to assemble session",
				"sessionID", sessions[i].ID,
				"error", err)
			assembleErrors++
			continue
		}
		fullSessions = append(fullSessions, fullSession)
	}

	slog.Info("LoadAllSessionsForProject: Loaded all sessions",
		"projectHash", projectHash,
		"sessionCount", len(fullSessions),
		"assembleErrors", assembleErrors)

	return fullSessions, nil
}

// GetFirstUserMessageContent extracts the content of the first user message in a session.
// Returns empty string if no user message is found.
// This is used for generating session slugs.
func GetFirstUserMessageContent(fullSession *FullSession) string {
	if fullSession == nil {
		return ""
	}

	if len(fullSession.Messages) == 0 {
		return ""
	}

	for _, fullMsg := range fullSession.Messages {
		if fullMsg.Message.Role != RoleUser {
			continue
		}

		// Look for text parts in user message
		for _, part := range fullMsg.Parts {
			if part.Type == PartTypeText && part.Text != nil && *part.Text != "" {
				return *part.Text
			}
		}
	}

	return ""
}
