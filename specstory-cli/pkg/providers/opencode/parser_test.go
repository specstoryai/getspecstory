package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Test helper to create a temporary OpenCode storage structure.
func setupTestStorage(t *testing.T) (string, func()) {
	t.Helper()

	fakeHome := t.TempDir()

	// Override the osUserHomeDir function
	originalUserHome := osUserHomeDir
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// Create base storage directories
	storageDir := filepath.Join(fakeHome, ".local", "share", "opencode", "storage")
	for _, subdir := range []string{"project", "session", "message", "part"} {
		if err := os.MkdirAll(filepath.Join(storageDir, subdir), 0o755); err != nil {
			t.Fatalf("failed to create %s directory: %v", subdir, err)
		}
	}

	cleanup := func() {
		osUserHomeDir = originalUserHome
	}

	return storageDir, cleanup
}

// writeJSONFile writes a struct to a JSON file.
func writeJSONFile(t *testing.T, path string, data interface{}) {
	t.Helper()

	bytes, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create directory %s: %v", dir, err)
	}

	if err := os.WriteFile(path, bytes, 0o644); err != nil {
		t.Fatalf("failed to write file %s: %v", path, err)
	}
}

func TestLoadProject_Success(t *testing.T) {
	storageDir, cleanup := setupTestStorage(t)
	defer cleanup()

	projectHash := "abc123"
	project := Project{
		ID:       projectHash,
		Worktree: "/home/user/myproject",
		VCS:      "git",
		Time: TimeInfo{
			Created: 1735725600000,
			Updated: 1735729200000,
		},
	}

	projectPath := filepath.Join(storageDir, "project", projectHash+".json")
	writeJSONFile(t, projectPath, project)

	loaded, err := LoadProject(projectHash)
	if err != nil {
		t.Fatalf("LoadProject returned error: %v", err)
	}

	if loaded.ID != project.ID {
		t.Errorf("loaded.ID = %q, want %q", loaded.ID, project.ID)
	}
	if loaded.Worktree != project.Worktree {
		t.Errorf("loaded.Worktree = %q, want %q", loaded.Worktree, project.Worktree)
	}
}

func TestLoadProject_NotFound(t *testing.T) {
	_, cleanup := setupTestStorage(t)
	defer cleanup()

	_, err := LoadProject("nonexistent")
	if err == nil {
		t.Fatal("LoadProject should return error for non-existent project")
	}
}

func TestLoadProject_MalformedJSON(t *testing.T) {
	storageDir, cleanup := setupTestStorage(t)
	defer cleanup()

	projectHash := "malformed"
	projectPath := filepath.Join(storageDir, "project", projectHash+".json")

	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	if err := os.WriteFile(projectPath, []byte("{invalid json"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	_, err := LoadProject(projectHash)
	if err == nil {
		t.Fatal("LoadProject should return error for malformed JSON")
	}
}

func TestLoadSession_Success(t *testing.T) {
	storageDir, cleanup := setupTestStorage(t)
	defer cleanup()

	projectHash := "proj123"
	sessionID := "ses_abc"
	session := Session{
		ID:        sessionID,
		Slug:      "test-session",
		Version:   "1.0.0",
		ProjectID: projectHash,
		Directory: "/home/user/myproject",
		Time: TimeInfo{
			Created: 1735725600000,
			Updated: 1735729200000,
		},
	}

	sessionDir := filepath.Join(storageDir, "session", projectHash)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("failed to create session directory: %v", err)
	}
	sessionPath := filepath.Join(sessionDir, sessionID+".json")
	writeJSONFile(t, sessionPath, session)

	loaded, err := LoadSession(projectHash, sessionID)
	if err != nil {
		t.Fatalf("LoadSession returned error: %v", err)
	}

	if loaded.ID != session.ID {
		t.Errorf("loaded.ID = %q, want %q", loaded.ID, session.ID)
	}
	if loaded.Slug != session.Slug {
		t.Errorf("loaded.Slug = %q, want %q", loaded.Slug, session.Slug)
	}
}

func TestLoadSession_WithoutSesPrefix(t *testing.T) {
	// Tests that LoadSession can find a session when given an ID without the "ses_" prefix
	storageDir, cleanup := setupTestStorage(t)
	defer cleanup()

	projectHash := "proj123"
	sessionID := "ses_abc"
	session := Session{
		ID:        sessionID,
		Slug:      "test-session",
		ProjectID: projectHash,
		Time: TimeInfo{
			Created: 1735725600000,
			Updated: 1735729200000,
		},
	}

	sessionDir := filepath.Join(storageDir, "session", projectHash)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("failed to create session directory: %v", err)
	}
	sessionPath := filepath.Join(sessionDir, sessionID+".json")
	writeJSONFile(t, sessionPath, session)

	// Try loading with just "abc" (without "ses_" prefix)
	loaded, err := LoadSession(projectHash, "abc")
	if err != nil {
		t.Fatalf("LoadSession returned error: %v", err)
	}

	if loaded.ID != sessionID {
		t.Errorf("loaded.ID = %q, want %q", loaded.ID, sessionID)
	}
}

func TestLoadSession_NotFound(t *testing.T) {
	storageDir, cleanup := setupTestStorage(t)
	defer cleanup()

	projectHash := "proj123"
	sessionDir := filepath.Join(storageDir, "session", projectHash)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("failed to create session directory: %v", err)
	}

	_, err := LoadSession(projectHash, "nonexistent")
	if err == nil {
		t.Fatal("LoadSession should return error for non-existent session")
	}
}

func TestLoadSessionsForProject_MultipleSessions(t *testing.T) {
	storageDir, cleanup := setupTestStorage(t)
	defer cleanup()

	projectHash := "proj123"
	sessionDir := filepath.Join(storageDir, "session", projectHash)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("failed to create session directory: %v", err)
	}

	// Create multiple sessions with different timestamps
	sessions := []Session{
		{
			ID: "ses_oldest", Time: TimeInfo{Created: 1735725600000},
		},
		{
			ID: "ses_newest", Time: TimeInfo{Created: 1735898400000},
		},
		{
			ID: "ses_middle", Time: TimeInfo{Created: 1735812000000},
		},
	}

	for _, s := range sessions {
		path := filepath.Join(sessionDir, s.ID+".json")
		writeJSONFile(t, path, s)
	}

	// Also create a non-session file that should be ignored
	if err := os.WriteFile(filepath.Join(sessionDir, "other.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("failed to write other file: %v", err)
	}

	loaded, err := LoadSessionsForProject(projectHash)
	if err != nil {
		t.Fatalf("LoadSessionsForProject returned error: %v", err)
	}

	if len(loaded) != 3 {
		t.Fatalf("LoadSessionsForProject returned %d sessions, want 3", len(loaded))
	}

	// Verify sorting (newest first)
	if loaded[0].ID != "ses_newest" {
		t.Errorf("first session should be newest, got %q", loaded[0].ID)
	}
	if loaded[1].ID != "ses_middle" {
		t.Errorf("second session should be middle, got %q", loaded[1].ID)
	}
	if loaded[2].ID != "ses_oldest" {
		t.Errorf("third session should be oldest, got %q", loaded[2].ID)
	}
}

func TestLoadSessionsForProject_EmptyDirectory(t *testing.T) {
	storageDir, cleanup := setupTestStorage(t)
	defer cleanup()

	projectHash := "proj123"
	sessionDir := filepath.Join(storageDir, "session", projectHash)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("failed to create session directory: %v", err)
	}

	loaded, err := LoadSessionsForProject(projectHash)
	if err != nil {
		t.Fatalf("LoadSessionsForProject returned error: %v", err)
	}

	if len(loaded) != 0 {
		t.Fatalf("LoadSessionsForProject should return empty slice for empty directory, got %d", len(loaded))
	}
}

func TestLoadSessionsForProject_NonExistentDirectory(t *testing.T) {
	_, cleanup := setupTestStorage(t)
	defer cleanup()

	loaded, err := LoadSessionsForProject("nonexistent")
	if err != nil {
		t.Fatalf("LoadSessionsForProject returned error: %v", err)
	}

	// Should return empty slice, not error, for non-existent directory
	if len(loaded) != 0 {
		t.Fatalf("LoadSessionsForProject should return empty slice for non-existent directory")
	}
}

func TestLoadMessagesForSession_MultipleMessages(t *testing.T) {
	storageDir, cleanup := setupTestStorage(t)
	defer cleanup()

	sessionID := "ses_abc"
	messagesDir := filepath.Join(storageDir, "message", sessionID)
	if err := os.MkdirAll(messagesDir, 0o755); err != nil {
		t.Fatalf("failed to create messages directory: %v", err)
	}

	// Create multiple messages with different timestamps
	messages := []Message{
		{
			ID:        "msg_third",
			SessionID: sessionID,
			Role:      RoleAssistant,
			Time:      MessageTime{Created: 1735725720000},
		},
		{
			ID:        "msg_first",
			SessionID: sessionID,
			Role:      RoleUser,
			Time:      MessageTime{Created: 1735725600000},
		},
		{
			ID:        "msg_second",
			SessionID: sessionID,
			Role:      RoleAssistant,
			Time:      MessageTime{Created: 1735725660000},
		},
	}

	for _, m := range messages {
		path := filepath.Join(messagesDir, m.ID+".json")
		writeJSONFile(t, path, m)
	}

	loaded, err := LoadMessagesForSession(sessionID)
	if err != nil {
		t.Fatalf("LoadMessagesForSession returned error: %v", err)
	}

	if len(loaded) != 3 {
		t.Fatalf("LoadMessagesForSession returned %d messages, want 3", len(loaded))
	}

	// Verify sorting (oldest first - chronological order)
	if loaded[0].ID != "msg_first" {
		t.Errorf("first message should be oldest, got %q", loaded[0].ID)
	}
	if loaded[1].ID != "msg_second" {
		t.Errorf("second message should be middle, got %q", loaded[1].ID)
	}
	if loaded[2].ID != "msg_third" {
		t.Errorf("third message should be newest, got %q", loaded[2].ID)
	}
}

func TestLoadMessagesForSession_EmptyDirectory(t *testing.T) {
	storageDir, cleanup := setupTestStorage(t)
	defer cleanup()

	sessionID := "ses_empty"
	messagesDir := filepath.Join(storageDir, "message", sessionID)
	if err := os.MkdirAll(messagesDir, 0o755); err != nil {
		t.Fatalf("failed to create messages directory: %v", err)
	}

	loaded, err := LoadMessagesForSession(sessionID)
	if err != nil {
		t.Fatalf("LoadMessagesForSession returned error: %v", err)
	}

	if len(loaded) != 0 {
		t.Fatalf("LoadMessagesForSession should return empty slice for empty directory")
	}
}

func TestLoadPartsForMessage_MultipleParts(t *testing.T) {
	storageDir, cleanup := setupTestStorage(t)
	defer cleanup()

	messageID := "msg_abc"
	partsDir := filepath.Join(storageDir, "part", messageID)
	if err := os.MkdirAll(partsDir, 0o755); err != nil {
		t.Fatalf("failed to create parts directory: %v", err)
	}

	text := "Hello"
	parts := []Part{
		{
			ID:        "prt_second",
			MessageID: messageID,
			Type:      PartTypeText,
			Text:      &text,
			Time:      &PartTime{Start: int64Ptr(1735725660000)},
		},
		{
			ID:        "prt_first",
			MessageID: messageID,
			Type:      PartTypeText,
			Text:      &text,
			Time:      &PartTime{Start: int64Ptr(1735725600000)},
		},
	}

	for _, p := range parts {
		path := filepath.Join(partsDir, p.ID+".json")
		writeJSONFile(t, path, p)
	}

	loaded, err := LoadPartsForMessage(messageID)
	if err != nil {
		t.Fatalf("LoadPartsForMessage returned error: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("LoadPartsForMessage returned %d parts, want 2", len(loaded))
	}

	// Verify sorting by time (oldest first)
	if loaded[0].ID != "prt_first" {
		t.Errorf("first part should be oldest, got %q", loaded[0].ID)
	}
	if loaded[1].ID != "prt_second" {
		t.Errorf("second part should be newer, got %q", loaded[1].ID)
	}
}

func TestLoadPartsForMessage_FallbackToIDSorting(t *testing.T) {
	// When parts don't have timestamps, they should fall back to ID-based sorting
	storageDir, cleanup := setupTestStorage(t)
	defer cleanup()

	messageID := "msg_notime"
	partsDir := filepath.Join(storageDir, "part", messageID)
	if err := os.MkdirAll(partsDir, 0o755); err != nil {
		t.Fatalf("failed to create parts directory: %v", err)
	}

	text := "Hello"
	parts := []Part{
		{ID: "prt_bbb", MessageID: messageID, Type: PartTypeText, Text: &text},
		{ID: "prt_aaa", MessageID: messageID, Type: PartTypeText, Text: &text},
	}

	for _, p := range parts {
		path := filepath.Join(partsDir, p.ID+".json")
		writeJSONFile(t, path, p)
	}

	loaded, err := LoadPartsForMessage(messageID)
	if err != nil {
		t.Fatalf("LoadPartsForMessage returned error: %v", err)
	}

	// Should sort by ID when no time is available
	if loaded[0].ID != "prt_aaa" {
		t.Errorf("first part should be prt_aaa (alphabetically first), got %q", loaded[0].ID)
	}
}

func TestAssembleFullSession_Success(t *testing.T) {
	storageDir, cleanup := setupTestStorage(t)
	defer cleanup()

	projectHash := "proj123"
	sessionID := "ses_abc"
	messageID := "msg_001"
	partID := "prt_001"

	// Create project
	project := Project{
		ID:       projectHash,
		Worktree: "/home/user/project",
		Time:     TimeInfo{Created: 1735725600000, Updated: 1735725600000},
	}
	writeJSONFile(t, filepath.Join(storageDir, "project", projectHash+".json"), project)

	// Create session
	session := Session{
		ID:        sessionID,
		ProjectID: projectHash,
		Directory: "/home/user/project",
		Time:      TimeInfo{Created: 1735725600000, Updated: 1735725600000},
	}

	// Create message
	messagesDir := filepath.Join(storageDir, "message", sessionID)
	if err := os.MkdirAll(messagesDir, 0o755); err != nil {
		t.Fatalf("failed to create messages directory: %v", err)
	}
	message := Message{
		ID:        messageID,
		SessionID: sessionID,
		Role:      RoleUser,
		Time:      MessageTime{Created: 1735725600000, Updated: 1735725600000},
	}
	writeJSONFile(t, filepath.Join(messagesDir, messageID+".json"), message)

	// Create part
	partsDir := filepath.Join(storageDir, "part", messageID)
	if err := os.MkdirAll(partsDir, 0o755); err != nil {
		t.Fatalf("failed to create parts directory: %v", err)
	}
	text := "Hello, world!"
	part := Part{
		ID:        partID,
		SessionID: sessionID,
		MessageID: messageID,
		Type:      PartTypeText,
		Text:      &text,
	}
	writeJSONFile(t, filepath.Join(partsDir, partID+".json"), part)

	// Assemble
	fullSession, err := AssembleFullSession(&session)
	if err != nil {
		t.Fatalf("AssembleFullSession returned error: %v", err)
	}

	if fullSession.Session.ID != sessionID {
		t.Errorf("session ID = %q, want %q", fullSession.Session.ID, sessionID)
	}
	if fullSession.Project == nil {
		t.Error("project should not be nil")
	} else if fullSession.Project.ID != projectHash {
		t.Errorf("project ID = %q, want %q", fullSession.Project.ID, projectHash)
	}
	if len(fullSession.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(fullSession.Messages))
	}
	if len(fullSession.Messages[0].Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(fullSession.Messages[0].Parts))
	}
	if *fullSession.Messages[0].Parts[0].Text != text {
		t.Errorf("part text = %q, want %q", *fullSession.Messages[0].Parts[0].Text, text)
	}
}

func TestAssembleFullSession_NilSession(t *testing.T) {
	_, err := AssembleFullSession(nil)
	if err == nil {
		t.Fatal("AssembleFullSession should return error for nil session")
	}
}

func TestAssembleFullSession_MissingProject(t *testing.T) {
	// Project is optional - should still succeed without it
	_, cleanup := setupTestStorage(t)
	defer cleanup()

	sessionID := "ses_noproject"
	session := Session{
		ID:        sessionID,
		ProjectID: "nonexistent",
		Directory: "/home/user/project",
		Time:      TimeInfo{Created: 1735725600000, Updated: 1735725600000},
	}

	fullSession, err := AssembleFullSession(&session)
	if err != nil {
		t.Fatalf("AssembleFullSession returned error: %v", err)
	}

	// Project should be nil since it doesn't exist
	if fullSession.Project != nil {
		t.Error("project should be nil when project file doesn't exist")
	}
}

func TestGetFirstUserMessageContent(t *testing.T) {
	text1 := "First user message"
	text2 := "Second user message"
	thinkingText := "Thinking..."

	tests := []struct {
		name        string
		fullSession *FullSession
		expected    string
	}{
		{
			name:        "nil session",
			fullSession: nil,
			expected:    "",
		},
		{
			name: "no messages",
			fullSession: &FullSession{
				Session:  &Session{ID: "ses_1"},
				Messages: []FullMessage{},
			},
			expected: "",
		},
		{
			name: "only assistant messages",
			fullSession: &FullSession{
				Session: &Session{ID: "ses_1"},
				Messages: []FullMessage{
					{
						Message: &Message{ID: "msg_1", Role: RoleAssistant},
						Parts: []Part{
							{Type: PartTypeText, Text: &text1},
						},
					},
				},
			},
			expected: "",
		},
		{
			name: "user message with text part",
			fullSession: &FullSession{
				Session: &Session{ID: "ses_1"},
				Messages: []FullMessage{
					{
						Message: &Message{ID: "msg_1", Role: RoleUser},
						Parts: []Part{
							{Type: PartTypeText, Text: &text1},
						},
					},
				},
			},
			expected: text1,
		},
		{
			name: "multiple messages returns first user content",
			fullSession: &FullSession{
				Session: &Session{ID: "ses_1"},
				Messages: []FullMessage{
					{
						Message: &Message{ID: "msg_1", Role: RoleUser},
						Parts: []Part{
							{Type: PartTypeText, Text: &text1},
						},
					},
					{
						Message: &Message{ID: "msg_2", Role: RoleAssistant},
						Parts: []Part{
							{Type: PartTypeText, Text: &thinkingText},
						},
					},
					{
						Message: &Message{ID: "msg_3", Role: RoleUser},
						Parts: []Part{
							{Type: PartTypeText, Text: &text2},
						},
					},
				},
			},
			expected: text1,
		},
		{
			name: "user message with non-text parts first",
			fullSession: &FullSession{
				Session: &Session{ID: "ses_1"},
				Messages: []FullMessage{
					{
						Message: &Message{ID: "msg_1", Role: RoleUser},
						Parts: []Part{
							{Type: PartTypeReasoning, Text: &thinkingText},
							{Type: PartTypeText, Text: &text1},
						},
					},
				},
			},
			expected: text1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetFirstUserMessageContent(tt.fullSession)
			if got != tt.expected {
				t.Errorf("GetFirstUserMessageContent() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestPartSortKey(t *testing.T) {
	startTime := int64(1735725600000)
	tests := []struct {
		name     string
		part     Part
		expected string
	}{
		{
			name: "with start time",
			part: Part{
				ID:   "prt_abc",
				Time: &PartTime{Start: &startTime},
			},
			expected: "00000001735725600000", // Zero-padded to 20 digits
		},
		{
			name: "without time",
			part: Part{
				ID: "prt_xyz",
			},
			expected: "prt_xyz",
		},
		{
			name: "with nil Start pointer",
			part: Part{
				ID:   "prt_nil",
				Time: &PartTime{Start: nil},
			},
			expected: "prt_nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := partSortKey(tt.part)
			if got != tt.expected {
				t.Errorf("partSortKey() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// Helper function to create string pointers
func strPtr(s string) *string {
	return &s
}

// Helper function to create int64 pointers for timestamps
func int64Ptr(i int64) *int64 {
	return &i
}
