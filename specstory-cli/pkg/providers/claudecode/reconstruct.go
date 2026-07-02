package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

const (
	// reconstructedClaudeModel is a placeholder model label for reconstructed
	// assistant records. It is only a historical display value — on resume Claude
	// Code uses its own configured model for the next turn.
	reconstructedClaudeModel = "claude-opus-4-8"

	// reconstructedClaudeVersion mirrors the `version` field Claude Code writes on
	// its records. A concrete value matching observed sessions maximizes the
	// chance the loader accepts a synthesized file.
	reconstructedClaudeVersion = "2.1.178"
)

// ReconstructSession rebuilds a Claude Code native JSONL session from the neutral
// SessionData so `claude --resume <id>` can continue the conversation.
//
// The forward parser collapses Claude's parentUuid DAG and drops the API
// message.id and thinking signatures, so reconstruction synthesizes a fresh,
// linear parentUuid chain of plain user/assistant text records (tool calls and
// thinking are already flattened into agent text by FlattenSessionData). This is
// the structure validated by the Stage 1 spike. See docs/SESSION-PORTABILITY.md.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}
	cwd := spi.ResolveWorkspaceRoot(opts, data)

	newID := uuid.NewString()
	base := time.Now().UTC()

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Preserve <, >, & in conversation/markdown text rather than \u-escaping them.
	enc.SetEscapeHTML(false)

	var prevUUID string
	for i, turn := range turns {
		recUUID := uuid.NewString()
		ts := spi.RFC3339Millis(base.Add(time.Duration(i) * time.Second))

		rec := map[string]interface{}{
			"isSidechain": false,
			"userType":    "external",
			"cwd":         cwd,
			"sessionId":   newID,
			"version":     reconstructedClaudeVersion,
			"gitBranch":   "",
			"uuid":        recUUID,
			"timestamp":   ts,
		}

		// Linear chain: the first record roots the DAG (parentUuid null) and
		// carries provenance back to the source session.
		if prevUUID == "" {
			rec["parentUuid"] = nil
			rec["specstorySourceSessionId"] = data.SessionID
		} else {
			rec["parentUuid"] = prevUUID
		}

		if turn.Role == schema.RoleUser {
			rec["type"] = "user"
			rec["message"] = map[string]interface{}{
				"role":    "user",
				"content": turn.Text,
			}
		} else {
			rec["type"] = "assistant"
			rec["message"] = map[string]interface{}{
				"id":          "msg_" + recUUID,
				"type":        "message",
				"role":        "assistant",
				"model":       reconstructedClaudeModel,
				"content":     []map[string]interface{}{{"type": "text", "text": turn.Text}},
				"stop_reason": "end_turn",
				"usage":       map[string]interface{}{"input_tokens": 1, "output_tokens": 1},
			}
		}

		if err := enc.Encode(rec); err != nil {
			return nil, fmt.Errorf("failed to encode reconstructed record: %w", err)
		}

		prevUUID = recUUID
	}

	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  newID + ".jsonl",
		Content:   buf.Bytes(),
	}, nil
}

// NativeSessionPath returns where a reconstructed session file belongs in Claude
// Code's store: ~/.claude/projects/<cwd-encoded>/<filename>. The directory is not
// required to exist (the caller creates it).
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	dir, err := resolveClaudeProjectDir(projectPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filename), nil
}
