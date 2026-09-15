package qwencode

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

// ReconstructSession rebuilds a Qwen Code native session transcript from the
// neutral SessionData so `qwen --resume <id>` can continue the conversation.
//
// Qwen stores a session as an append-only JSONL file of self-describing record
// envelopes (uuid/parentUuid/sessionId/timestamp/type/provenance) wrapping
// Gemini-style message payloads. Tool calls and thinking are already flattened
// into agent text by FlattenSessionData, so reconstruction emits only
// user/assistant text records linked into a parentUuid chain. See
// docs/SESSION-PORTABILITY.md.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}
	cwd := spi.ResolveWorkspaceRoot(opts, data)
	// Qwen validates the recorded cwd's hash, not just the directory containing
	// the transcript. Canonicalize the local destination supplied by resume.
	if opts.WorkspaceRoot != "" {
		cwd = spi.CanonicalizePathOrClean(opts.WorkspaceRoot)
	}

	newID := uuid.NewString()
	base := time.Now().UTC()

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	parentUUID := any(nil)
	for i, turn := range turns {
		record := map[string]any{
			"uuid":       uuid.NewString(),
			"parentUuid": parentUUID,
			"sessionId":  newID,
			"timestamp":  spi.RFC3339Millis(base.Add(time.Duration(i) * time.Second)),
			"cwd":        cwd,
		}
		if turn.Role == schema.RoleUser {
			record["type"] = "user"
			record["provenance"] = "real_user"
			record["message"] = map[string]any{
				"role":  "user",
				"parts": []map[string]any{{"text": turn.Text}},
			}
		} else {
			record["type"] = "assistant"
			record["provenance"] = "assistant_output"
			record["message"] = map[string]any{
				"role":  "model",
				"parts": []map[string]any{{"text": turn.Text}},
			}
		}
		if i == 0 {
			// Provenance back-link to the source session this was reconstructed
			// from, so the lineage stays traceable (matches claude/codex/gemini).
			record["specstorySourceSessionId"] = data.SessionID
		}
		if err := enc.Encode(record); err != nil {
			return nil, fmt.Errorf("failed to encode qwen record: %w", err)
		}
		parentUUID = record["uuid"]
	}

	// Filename: <session-id>.jsonl (Qwen convention: chats files are named by session ID).
	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  newID + ".jsonl",
		Content:   buf.Bytes(),
	}, nil
}

// NativeSessionPath resolves the location Qwen will use when launched in the
// canonical project cwd. Directory creation belongs to the resume command.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	if !validSessionFilename(filename) {
		return "", fmt.Errorf("invalid Qwen session filename %q", filename)
	}
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return "", err
	}
	projectsDir, err := GetQwenProjectsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(projectsDir, SanitizeQwenCwd(projectPath), "chats", filename), nil
}

// SupportsReconstruction reports true: this provider has a native serializer
// (see ReconstructSession), so it can be a cross-agent resume target.
func (p *Provider) SupportsReconstruction() bool {
	return true
}
