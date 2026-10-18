package piagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

const (
	// The shared text-only portability format drops model and API metadata.
	// Empty strings denote unknown native metadata; they must not attribute
	// imported turns to the model selected for future turns.
	reconstructedPiProvider = ""
	reconstructedPiModel    = ""

	// Pi 0.85.1 writes session format version 3, independently of its release.
	piHeaderVersion = 3

	// reconstructedPiStopReason mirrors the stopReason pi writes on a completed
	// assistant turn (observed "stop"). The loader ignores it for records that
	// carry text content, but matching the real value keeps the file faithful.
	reconstructedPiStopReason = "stop"
)

// ReconstructSession rebuilds a native pi JSONL v3 session from the neutral
// SessionData so `pi --session-id <id>` can continue the conversation.
//
// The forward parser collapses pi's id/parentId tree to the active leaf path and
// folds tool calls and thinking into agent text (via FlattenSessionData), so
// reconstruction emits a fresh, strictly linear parentId chain of plain
// user/assistant text entries under a fresh v3 header. It is NOT a structural
// round-trip and does not replay structured toolCall/toolResult entries — the
// fidelity bar is "valid to pi's own loader, conveys the gist". See
// docs/SESSION-PORTABILITY.md.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}
	cwd := spi.ResolveWorkspaceRoot(opts, data)
	// Only an explicit destination names a local directory. A source workspace
	// may belong to another machine and must retain its recorded spelling.
	if opts.WorkspaceRoot != "" {
		cwd, err = spi.GetCanonicalPath(opts.WorkspaceRoot)
		if err != nil {
			return nil, fmt.Errorf("pi: canonicalizing reconstruction destination: %w", err)
		}
	}

	newID := uuid.NewString()
	base := time.Now().UTC()

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Preserve <, >, & in conversation/markdown text rather than \u-escaping them.
	enc.SetEscapeHTML(false)

	// Line 0: the session header (type "session", version 3, no id/parentId).
	// specstorySourceSessionId is provenance — pi's sessionHeader decode ignores
	// unknown fields, preserving the source link without affecting native loading.
	header := map[string]interface{}{
		"type":                     entrySession,
		"version":                  piHeaderVersion,
		"id":                       newID,
		"timestamp":                spi.RFC3339Millis(base),
		"cwd":                      cwd,
		"specstorySourceSessionId": data.SessionID,
	}
	if err := enc.Encode(header); err != nil {
		return nil, fmt.Errorf("pi: encoding session header: %w", err)
	}

	// Lines 1..N: one "message" entry per flattened turn, threaded into a linear
	// parentId chain (first entry parentId:null, each next → previous entry id).
	// A fresh forward-only chain of UUIDs cannot form a cycle, so no write-side
	// cycle guard is needed (the read-side walkToRoot still guards on load).
	var prevID *string
	for i, turn := range turns {
		recID := uuid.NewString()
		ts := spi.RFC3339Millis(base.Add(time.Duration(i+1) * time.Second))

		rec := map[string]interface{}{
			"type":      entryMessage,
			"id":        recID,
			"parentId":  prevID, // nil pointer → JSON null for the first entry
			"timestamp": ts,
			"message":   reconstructMessage(turn),
		}
		if err := enc.Encode(rec); err != nil {
			return nil, fmt.Errorf("pi: encoding reconstructed message: %w", err)
		}

		id := recID
		prevID = &id
	}

	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  piNativeFilename(base, newID),
		Content:   buf.Bytes(),
	}, nil
}

// reconstructMessage builds the wrapped `message` payload for a flattened turn.
// User content is emitted as the bare-string form (pi's read side accepts a
// string or an array; the string is the simplest valid shape). Assistant content
// is the {type:text} block array pi writes.
func reconstructMessage(turn spi.Turn) map[string]interface{} {
	if turn.Role == schema.RoleUser {
		return map[string]interface{}{
			"role":    "user",
			"content": turn.Text,
		}
	}
	return map[string]interface{}{
		"role":       "assistant",
		"content":    []map[string]interface{}{{"type": "text", "text": turn.Text}},
		"provider":   reconstructedPiProvider,
		"model":      reconstructedPiModel,
		"api":        "",
		"stopReason": reconstructedPiStopReason,
	}
}

// piNativeFilename reproduces pi's own `<timestamp>_<id>.jsonl` session filename:
// the header's RFC 3339 millisecond timestamp with ':' and '.' replaced by '-'
// (filesystem-safe, no ':'), then '_' and the session id. Observed real file:
// 2026-09-03T15-50-46-352Z_01a067f7-...jsonl, whose id equals the header id.
func piNativeFilename(base time.Time, id string) string {
	safeTS := strings.NewReplacer(":", "-", ".", "-").Replace(spi.RFC3339Millis(base))
	return fmt.Sprintf("%s_%s.jsonl", safeTS, id)
}

// NativeSessionPath returns where a reconstructed session file belongs in pi's
// store: <sessions-root>/--<encoded-cwd>--/<filename> for the default layout, or
// <override>/<filename> for the flat PI_CODING_AGENT_SESSION_DIR layout. The
// directory is not required to exist — the caller (`specstory resume`) creates it
// and writes the file.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	dir, err := resolvePiSessionDir(projectPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filename), nil
}

// resolvePiSessionDir returns the directory a reconstructed pi session belongs
// in, reusing ProjectSessionDir — the same encoder the read side uses — so a
// reconstructed file lands in the exact directory pi will look in. It resolves
// without requiring the directory to exist and handles both the default
// encoded-cwd layout and the flat override, keeping the write side free of any
// filesystem side effects.
func resolvePiSessionDir(projectPath string) (string, error) {
	return ProjectSessionDir(projectPath)
}

// SupportsReconstruction reports that Pi accepts native text-only imports.
func (p *Provider) SupportsReconstruction() bool {
	return true
}
