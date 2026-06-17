package codexcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// reconstructedCodexCliVersion mirrors the `cli_version` Codex writes into
// session_meta. A concrete value matching observed sessions maximizes the chance
// the loader accepts a synthesized file.
const reconstructedCodexCliVersion = "0.139.0"

// codexTimestamp formats a time the way Codex records do (RFC3339 millis, Z).
func codexTimestamp(t time.Time) string {
	return t.Format("2006-01-02T15:04:05.000Z")
}

// ReconstructSession rebuilds a Codex CLI native rollout from the neutral
// SessionData so `codex resume <id>` can continue the conversation.
//
// Codex resume replays the response_item model transcript, while the forward
// parser reads the event_msg UI stream — so reconstruction emits BOTH for each
// turn: a response_item message (what the agent replays) and a matching event_msg
// (what the UI and our own re-parse read). Tool calls and thinking are already
// flattened into agent text by FlattenSessionData.
//
// We do NOT synthesize an environment_context block: Codex injects its own fresh
// environment context on every resume, consistent with the cross-provider policy
// that the target supplies its own preamble. See docs/SESSION-PORTABILITY.md.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	if data == nil {
		return nil, fmt.Errorf("cannot reconstruct nil session data")
	}

	cwd := opts.WorkspaceRoot
	if cwd == "" {
		cwd = data.WorkspaceRoot
	}

	turns := spi.FlattenSessionData(data, opts.MigrationNote)
	if len(turns) == 0 {
		return nil, fmt.Errorf("session has no content to reconstruct")
	}

	// Codex session IDs are UUIDv7. Fall back to v4 if v7 generation fails.
	newID := uuid.NewString()
	if v7, err := uuid.NewV7(); err == nil {
		newID = v7.String()
	}

	base := time.Now().UTC()

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	encode := func(rec map[string]interface{}) error {
		return enc.Encode(rec)
	}

	// session_meta must be the first record; it also carries provenance.
	metaTS := codexTimestamp(base)
	if err := encode(map[string]interface{}{
		"timestamp": metaTS,
		"type":      "session_meta",
		"payload": map[string]interface{}{
			"id":                       newID,
			"timestamp":                metaTS,
			"cwd":                      cwd,
			"originator":               "codex-tui",
			"cli_version":              reconstructedCodexCliVersion,
			"source":                   "cli",
			"model_provider":           "openai",
			"specstorySourceSessionId": data.SessionID,
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to encode session_meta: %w", err)
	}

	for i, turn := range turns {
		ts := codexTimestamp(base.Add(time.Duration(i+1) * time.Second))

		var role, contentType, eventType string
		if turn.Role == schema.RoleUser {
			role, contentType, eventType = "user", "input_text", "user_message"
		} else {
			role, contentType, eventType = "assistant", "output_text", "agent_message"
		}

		// response_item: the model-facing transcript the agent replays on resume.
		if err := encode(codexResponseItemMessage(ts, role, contentType, turn.Text)); err != nil {
			return nil, fmt.Errorf("failed to encode response_item: %w", err)
		}
		// event_msg: the UI/history stream the forward parser reads.
		if err := encode(codexEventMessage(ts, eventType, turn.Text)); err != nil {
			return nil, fmt.Errorf("failed to encode event_msg: %w", err)
		}
	}

	filename := fmt.Sprintf("rollout-%s-%s.jsonl", base.Format("2006-01-02T15-04-05"), newID)

	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  filename,
		Content:   buf.Bytes(),
	}, nil
}

// NativeSessionPath returns where a reconstructed rollout belongs in Codex's
// store: ~/.codex/sessions/YYYY/MM/DD/<filename>, dated today. Codex sessions are
// not project-scoped by directory, so projectPath is unused. The directory is not
// required to exist (the caller creates it).
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	now := time.Now()
	dir := filepath.Join(
		codexSessionsRoot(homeDir),
		fmt.Sprintf("%04d", now.Year()),
		fmt.Sprintf("%02d", int(now.Month())),
		fmt.Sprintf("%02d", now.Day()),
	)
	return filepath.Join(dir, filename), nil
}

// codexResponseItemMessage builds a response_item "message" record.
func codexResponseItemMessage(ts, role, contentType, text string) map[string]interface{} {
	return map[string]interface{}{
		"timestamp": ts,
		"type":      "response_item",
		"payload": map[string]interface{}{
			"type":    "message",
			"role":    role,
			"content": []map[string]interface{}{{"type": contentType, "text": text}},
		},
	}
}

// codexEventMessage builds an event_msg record (user_message / agent_message).
func codexEventMessage(ts, eventType, message string) map[string]interface{} {
	return map[string]interface{}{
		"timestamp": ts,
		"type":      "event_msg",
		"payload": map[string]interface{}{
			"type":    eventType,
			"message": message,
		},
	}
}
