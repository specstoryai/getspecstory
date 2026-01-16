package geminicli

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleSessionJSON = `{
  "sessionId": "session-1",
  "projectHash": "hash",
  "startTime": "2025-11-18T17:00:00Z",
  "lastUpdated": "2025-11-18T17:05:00Z",
  "messages": [
    {
      "id": "warmup",
      "timestamp": "2025-11-18T17:00:01Z",
      "type": "user",
      "content": "/model"
    },
    {
      "id": "real-user",
      "timestamp": "2025-11-18T17:00:10Z",
      "type": "user",
      "content": "What is the status?"
    },
    {
      "id": "agent-tool",
      "timestamp": "2025-11-18T17:00:20Z",
      "type": "gemini",
      "content": "",
      "toolCalls": [
        {
          "id": "tool-1",
          "name": "run_shell_command",
          "args": {"command": "ls"},
          "result": [
            {
              "functionResponse": {
                "id": "tool-1",
                "name": "run_shell_command",
                "response": {"output": "ok"}
              }
            }
          ],
          "status": "success",
          "timestamp": "2025-11-18T17:00:20Z"
        }
      ]
    }
  ]
}`

func TestFindSessionsParsesLogsAndTrimsWarmup(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "hash")
	chatsDir := filepath.Join(projectDir, "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	sessionPath := filepath.Join(chatsDir, "session-test.json")
	if err := os.WriteFile(sessionPath, []byte(sampleSessionJSON), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	logsJSON := `[
      {"sessionId":"session-1","messageId":0,"type":"user","message":"/model","timestamp":"2025-11-18T17:00:01Z"},
      {"sessionId":"session-1","messageId":1,"type":"user","message":"What is the status?","timestamp":"2025-11-18T17:00:10Z"}
    ]`
	if err := os.WriteFile(filepath.Join(projectDir, "logs.json"), []byte(logsJSON), 0o644); err != nil {
		t.Fatalf("write logs: %v", err)
	}

	sessions, err := FindSessions(projectDir)
	if err != nil {
		t.Fatalf("FindSessions failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session got %d", len(sessions))
	}

	session := sessions[0]
	if len(session.Messages) == 0 || session.Messages[0].ID != "real-user" {
		t.Fatalf("warmup messages should be trimmed; first message: %#v", session.Messages[0])
	}

	logs := session.LogsForMessage("What is the status?")
	if len(logs) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(logs))
	}

	tool := session.Messages[1].ToolCalls[0]
	if got := toolOutput(tool); got != "ok" {
		t.Fatalf("toolOutput mismatch, got %q", got)
	}
}
