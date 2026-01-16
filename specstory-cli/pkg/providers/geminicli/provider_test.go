package geminicli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderGetAgentChatSessions(t *testing.T) {
	tmp := t.TempDir()

	originalHome := osUserHomeDir
	osUserHomeDir = func() (string, error) { return tmp, nil }
	t.Cleanup(func() { osUserHomeDir = originalHome })

	projectPath := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	hash, err := HashProjectPath(projectPath)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	geminiRoot := filepath.Join(tmp, ".gemini", "tmp", hash)
	chatsDir := filepath.Join(geminiRoot, "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatalf("mkdir chats: %v", err)
	}

	sessionJSON := `{
      "sessionId": "session-test",
      "projectHash": "` + hash + `",
      "startTime": "2025-11-18T17:00:00Z",
      "lastUpdated": "2025-11-18T17:05:00Z",
      "messages": [
        {"id":"user","timestamp":"2025-11-18T17:01:00Z","type":"user","content":"Hello"},
        {"id":"agent","timestamp":"2025-11-18T17:01:10Z","type":"gemini","content":"Hi there"}
      ]
    }`

	if err := os.WriteFile(filepath.Join(chatsDir, "session-test.json"), []byte(sessionJSON), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	provider := NewProvider()
	sessions, err := provider.GetAgentChatSessions(projectPath, false)
	if err != nil {
		t.Fatalf("GetAgentChatSessions failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].SessionID != "session-test" {
		t.Fatalf("unexpected session ID: %s", sessions[0].SessionID)
	}
	if sessions[0].SessionData == nil {
		t.Fatalf("SessionData should not be nil")
	}
}
