package opencode

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewProvider(t *testing.T) {
	p := NewProvider()
	if p == nil {
		t.Fatal("NewProvider returned nil")
	}
}

func TestProviderName(t *testing.T) {
	p := NewProvider()
	name := p.Name()
	if name != "OpenCode" {
		t.Errorf("Name() = %q, want %q", name, "OpenCode")
	}
}

func TestParseOpenCodeCommand(t *testing.T) {
	tests := []struct {
		name         string
		customCmd    string
		expectedCmd  string
		expectedArgs []string
	}{
		{
			name:         "empty command uses default",
			customCmd:    "",
			expectedCmd:  "opencode",
			expectedArgs: nil,
		},
		{
			name:         "simple custom command",
			customCmd:    "/usr/local/bin/opencode",
			expectedCmd:  "/usr/local/bin/opencode",
			expectedArgs: nil,
		},
		{
			name:         "custom command with arguments",
			customCmd:    "/usr/local/bin/opencode --debug",
			expectedCmd:  "/usr/local/bin/opencode",
			expectedArgs: []string{"--debug"},
		},
		{
			name:         "command with multiple arguments",
			customCmd:    "opencode --verbose --config /path/to/config",
			expectedCmd:  "opencode",
			expectedArgs: []string{"--verbose", "--config", "/path/to/config"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := parseOpenCodeCommand(tt.customCmd)

			if cmd != tt.expectedCmd {
				t.Errorf("command = %q, want %q", cmd, tt.expectedCmd)
			}

			if len(args) != len(tt.expectedArgs) {
				t.Fatalf("args length = %d, want %d", len(args), len(tt.expectedArgs))
			}

			for i, arg := range args {
				if arg != tt.expectedArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, arg, tt.expectedArgs[i])
				}
			}
		})
	}
}

func TestClassifyCheckError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "permission error",
			err:      os.ErrPermission,
			expected: ErrTypePermissionDenied,
		},
		{
			name:     "generic error",
			err:      os.ErrInvalid,
			expected: ErrTypeVersionFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyCheckError(tt.err)
			if got != tt.expected {
				t.Errorf("classifyCheckError() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDetectAgent_ProjectExists(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// Create a real git repo to get a proper project hash
	projectPath := createTestGitRepoForProvider(t)
	osGetwd = func() (string, error) {
		return projectPath, nil
	}

	// Create storage and project directories
	storageDir := filepath.Join(fakeHome, ".local", "share", "opencode", "storage")
	hash, _ := ComputeProjectHash(projectPath)
	projectDir := filepath.Join(storageDir, "session", hash)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	// Create a session file
	sessionFile := filepath.Join(projectDir, "ses_test123.json")
	if err := os.WriteFile(sessionFile, []byte("{}"), 0o644); err != nil {
		t.Fatalf("failed to create session file: %v", err)
	}

	p := NewProvider()
	detected := p.DetectAgent(projectPath, false)

	if !detected {
		t.Error("DetectAgent should return true when session files exist")
	}
}

// createTestGitRepoForProvider creates a temporary git repository for provider tests.
func createTestGitRepoForProvider(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to configure git email: %v", err)
	}

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to configure git name: %v", err)
	}

	testFile := filepath.Join(repoDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	cmd = exec.Command("git", "add", "test.txt")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "initial commit")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	return repoDir
}

func TestDetectAgent_ProjectMissing(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	projectPath := "/my/project"
	osGetwd = func() (string, error) {
		return projectPath, nil
	}

	// Create storage directory but not the project directory
	storageDir := filepath.Join(fakeHome, ".local", "share", "opencode", "storage")
	sessionDir := filepath.Join(storageDir, "session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}

	p := NewProvider()
	detected := p.DetectAgent(projectPath, false)

	if detected {
		t.Error("DetectAgent should return false when project doesn't exist")
	}
}

func TestDetectAgent_EmptyProjectDir(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	projectPath := "/my/project"
	osGetwd = func() (string, error) {
		return projectPath, nil
	}

	// Create project directory but without any session files
	storageDir := filepath.Join(fakeHome, ".local", "share", "opencode", "storage")
	hash, _ := ComputeProjectHash(projectPath)
	projectDir := filepath.Join(storageDir, "session", hash)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	p := NewProvider()
	detected := p.DetectAgent(projectPath, false)

	if detected {
		t.Error("DetectAgent should return false when no session files exist")
	}
}

func TestConvertToAgentChatSession_Success(t *testing.T) {
	text := "Hello"
	fullSession := &FullSession{
		Session: &Session{
			ID:        "ses_test",
			Slug:      "test-session",
			Directory: "/workspace",
			Time: TimeInfo{
				Created: "2025-01-01T10:00:00Z",
				Updated: "2025-01-01T11:00:00Z",
			},
		},
		Messages: []FullMessage{
			{
				Message: &Message{
					ID:   "msg_1",
					Role: RoleUser,
					Time: MessageTime{Created: "2025-01-01T10:00:00Z"},
				},
				Parts: []Part{
					{ID: "prt_1", Type: PartTypeText, Text: &text},
				},
			},
		},
	}

	chatSession := convertToAgentChatSession(fullSession, "/workspace", false)

	if chatSession == nil {
		t.Fatal("convertToAgentChatSession returned nil")
	}

	if chatSession.SessionID != "ses_test" {
		t.Errorf("SessionID = %q, want %q", chatSession.SessionID, "ses_test")
	}

	if chatSession.CreatedAt != "2025-01-01T10:00:00Z" {
		t.Errorf("CreatedAt = %q, want %q", chatSession.CreatedAt, "2025-01-01T10:00:00Z")
	}

	if chatSession.SessionData == nil {
		t.Error("SessionData should not be nil")
	}

	if chatSession.RawData == "" {
		t.Error("RawData should not be empty")
	}

	// Verify RawData is valid JSON
	var rawJSON map[string]interface{}
	if err := json.Unmarshal([]byte(chatSession.RawData), &rawJSON); err != nil {
		t.Errorf("RawData is not valid JSON: %v", err)
	}
}

func TestConvertToAgentChatSession_NilSession(t *testing.T) {
	result := convertToAgentChatSession(nil, "/workspace", false)
	if result != nil {
		t.Error("expected nil for nil session")
	}
}

func TestConvertToAgentChatSession_NilSessionField(t *testing.T) {
	fullSession := &FullSession{
		Session: nil,
	}

	result := convertToAgentChatSession(fullSession, "/workspace", false)
	if result != nil {
		t.Error("expected nil for nil Session field")
	}
}

func TestConvertToAgentChatSession_EmptyMessages(t *testing.T) {
	fullSession := &FullSession{
		Session: &Session{
			ID:   "ses_empty",
			Time: TimeInfo{Created: "2025-01-01T10:00:00Z"},
		},
		Messages: []FullMessage{},
	}

	result := convertToAgentChatSession(fullSession, "/workspace", false)
	if result != nil {
		t.Error("expected nil for session with no messages")
	}
}

func TestConvertToAgentChatSession_DefaultSlug(t *testing.T) {
	// When session has no user content to generate slug from
	fullSession := &FullSession{
		Session: &Session{
			ID:   "ses_noslug",
			Slug: "",
			Time: TimeInfo{Created: "2025-01-01T10:00:00Z"},
		},
		Messages: []FullMessage{
			{
				Message: &Message{
					ID:   "msg_1",
					Role: RoleAssistant, // No user message
					Time: MessageTime{Created: "2025-01-01T10:00:00Z"},
				},
				Parts: []Part{},
			},
		},
	}

	result := convertToAgentChatSession(fullSession, "/workspace", false)

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Should have default slug when no user content
	if result.Slug == "" {
		t.Error("Slug should not be empty")
	}
}

func TestGetAgentChatSession_NotFound(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// Create storage directories
	storageDir := filepath.Join(fakeHome, ".local", "share", "opencode", "storage")
	for _, subdir := range []string{"project", "session", "message", "part"} {
		if err := os.MkdirAll(filepath.Join(storageDir, subdir), 0o755); err != nil {
			t.Fatalf("failed to create directory: %v", err)
		}
	}

	p := NewProvider()
	session, err := p.GetAgentChatSession("/project", "nonexistent", false)

	if err != nil {
		t.Fatalf("GetAgentChatSession returned unexpected error: %v", err)
	}

	if session != nil {
		t.Error("expected nil for non-existent session")
	}
}

func TestGetAgentChatSessions_EmptyProject(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	projectPath := "/my/project"

	// Create storage directories
	storageDir := filepath.Join(fakeHome, ".local", "share", "opencode", "storage")
	hash, _ := ComputeProjectHash(projectPath)
	projectDir := filepath.Join(storageDir, "session", hash)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	p := NewProvider()
	sessions, err := p.GetAgentChatSessions(projectPath, false)

	if err != nil {
		t.Fatalf("GetAgentChatSessions returned error: %v", err)
	}

	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestGetAgentChatSessions_WithSessions(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	projectPath := "/my/project"

	// Create storage directories
	storageDir := filepath.Join(fakeHome, ".local", "share", "opencode", "storage")
	hash, _ := ComputeProjectHash(projectPath)
	projectDir := filepath.Join(storageDir, "session", hash)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	// Create a session
	session := Session{
		ID:        "ses_test",
		ProjectID: hash,
		Directory: projectPath,
		Time: TimeInfo{
			Created: "2025-01-01T10:00:00Z",
			Updated: "2025-01-01T11:00:00Z",
		},
	}
	sessionPath := filepath.Join(projectDir, "ses_test.json")
	sessionBytes, _ := json.Marshal(session)
	if err := os.WriteFile(sessionPath, sessionBytes, 0o644); err != nil {
		t.Fatalf("failed to write session: %v", err)
	}

	// Create messages directory
	messagesDir := filepath.Join(storageDir, "message", "ses_test")
	if err := os.MkdirAll(messagesDir, 0o755); err != nil {
		t.Fatalf("failed to create messages dir: %v", err)
	}

	// Create a message
	message := Message{
		ID:        "msg_001",
		SessionID: "ses_test",
		Role:      RoleUser,
		Time:      MessageTime{Created: "2025-01-01T10:00:00Z"},
	}
	messagePath := filepath.Join(messagesDir, "msg_001.json")
	messageBytes, _ := json.Marshal(message)
	if err := os.WriteFile(messagePath, messageBytes, 0o644); err != nil {
		t.Fatalf("failed to write message: %v", err)
	}

	// Create parts directory and a part
	partsDir := filepath.Join(storageDir, "part", "msg_001")
	if err := os.MkdirAll(partsDir, 0o755); err != nil {
		t.Fatalf("failed to create parts dir: %v", err)
	}

	text := "Hello, world!"
	part := Part{
		ID:        "prt_001",
		SessionID: "ses_test",
		MessageID: "msg_001",
		Type:      PartTypeText,
		Text:      &text,
	}
	partPath := filepath.Join(partsDir, "prt_001.json")
	partBytes, _ := json.Marshal(part)
	if err := os.WriteFile(partPath, partBytes, 0o644); err != nil {
		t.Fatalf("failed to write part: %v", err)
	}

	p := NewProvider()
	sessions, err := p.GetAgentChatSessions(projectPath, false)

	if err != nil {
		t.Fatalf("GetAgentChatSessions returned error: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	if sessions[0].SessionID != "ses_test" {
		t.Errorf("SessionID = %q, want %q", sessions[0].SessionID, "ses_test")
	}
}

func TestBuildCheckErrorMessage(t *testing.T) {
	tests := []struct {
		name       string
		errorType  string
		path       string
		isCustom   bool
		stderr     string
		wantSubstr string
	}{
		{
			name:       "not found default",
			errorType:  ErrTypeNotFound,
			path:       "opencode",
			isCustom:   false,
			wantSubstr: "Install OpenCode",
		},
		{
			name:       "not found custom",
			errorType:  ErrTypeNotFound,
			path:       "/custom/path/opencode",
			isCustom:   true,
			wantSubstr: "Verify the path you supplied",
		},
		{
			name:       "permission denied",
			errorType:  ErrTypePermissionDenied,
			path:       "/usr/bin/opencode",
			isCustom:   false,
			wantSubstr: "chmod +x",
		},
		{
			name:       "version failed with stderr",
			errorType:  ErrTypeVersionFailed,
			path:       "opencode",
			isCustom:   false,
			stderr:     "segfault",
			wantSubstr: "segfault",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCheckErrorMessage(tt.errorType, tt.path, tt.isCustom, tt.stderr)
			if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("buildCheckErrorMessage() = %q, want substring %q", got, tt.wantSubstr)
			}
		})
	}
}

func TestFormatJSONParseError(t *testing.T) {
	filePath := "/path/to/file.json"
	parseErr := &json.SyntaxError{Offset: 10}

	got := formatJSONParseError(filePath, parseErr)

	if !strings.Contains(got, filePath) {
		t.Errorf("formatJSONParseError should contain file path: %q", got)
	}

	if !strings.Contains(got, "corrupt") {
		t.Errorf("formatJSONParseError should mention corrupt: %q", got)
	}
}
