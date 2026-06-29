package deepseektui

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

// deepseekTimestamp formats a time the way DeepSeek records do (RFC3339 micros, Z).
func deepseekTimestamp(t time.Time) string {
	return t.Format("2006-01-02T15:04:05.000000Z")
}

// ReconstructSession rebuilds a DeepSeek TUI native session from the neutral
// SessionData so `deepseek --resume <id>` can continue the conversation.
//
// DeepSeek stores a session as a single JSON document
// `{schema_version, system_prompt, metadata, messages}` where each message is
// `{role, content:[{type:"text",text}]}`. Tool calls and thinking are already
// flattened into agent text by FlattenSessionData, so reconstruction emits only
// `user`/`assistant` text messages. The session is associated with a project via
// metadata.workspace. See docs/SESSION-PORTABILITY.md.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}
	cwd := spi.ResolveWorkspaceRoot(opts, data)

	newID := uuid.NewString()
	now := deepseekTimestamp(time.Now().UTC())

	title := spi.ResumedSessionTitle(data.Slug)

	messages := make([]map[string]interface{}, 0, len(turns))
	for _, turn := range turns {
		messages = append(messages, map[string]interface{}{
			"role":    spi.ReconstructRole(turn.Role),
			"content": []map[string]interface{}{{"type": "text", "text": turn.Text}},
		})
	}

	session := map[string]interface{}{
		"schema_version": 1,
		"system_prompt":  "",
		"metadata": map[string]interface{}{
			"id":            newID,
			"title":         title,
			"created_at":    now,
			"updated_at":    now,
			"message_count": len(messages),
			"total_tokens":  0,
			"model":         "",
			"workspace":     cwd,
			"mode":          "agent",
		},
		"messages": messages,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(session); err != nil {
		return nil, fmt.Errorf("failed to encode deepseek session: %w", err)
	}

	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  newID + ".json",
		Content:   buf.Bytes(),
	}, nil
}

// NativeSessionPath returns where a reconstructed session belongs in DeepSeek's
// store: ~/.deepseek/sessions/<filename>. DeepSeek sessions are not project-scoped
// by directory (the project is recorded in metadata.workspace), so projectPath is
// unused. The directory is not required to exist (the caller creates it).
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	dir, err := resolveSessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filename), nil
}
