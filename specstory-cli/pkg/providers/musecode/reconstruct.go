package musecode

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
	// reconstructedSemver is stamped as the build that wrote the transcript.
	// Muse reads its own build metadata back, so a plausible value keeps the
	// record well-formed; the real version is unknowable at reconstruct time.
	reconstructedSemver = "0.1.0"

	// reconstructedToolSurface matches what Muse Code 0.1.0 writes.
	reconstructedToolSurface = "2"

	// recordSpacing separates consecutive reconstructed records. Muse's clock
	// is microseconds; one millisecond apart keeps every record distinct at the
	// millisecond precision the parser converts to.
	recordSpacing = int64(time.Millisecond / time.Microsecond)
)

// ReconstructSession rebuilds a Muse Code native transcript from the neutral
// SessionData so `muse resume <id>` can continue the conversation.
//
// Muse stores a session as an append-only event log: a metadata record at
// session open, then run events keyed by run_id. Tool calls and thinking are
// already flattened into agent text by FlattenSessionData, so reconstruction
// emits only the three events a conversation needs — `started` for a user turn,
// `assistant_message_committed` for an agent turn, and `terminal` to close each
// run. See docs/SESSION-PORTABILITY.md and docs/MUSE-FORMAT.md.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}
	workspaceRoot := spi.ResolveWorkspaceRoot(opts, data)

	newID := uuid.NewString()
	baseMicros := time.Now().UTC().UnixMicro()

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)

	sequence := int64(0)
	emit := func(payloadType string, payload map[string]any) error {
		sequence++
		record := map[string]any{
			"schema_version":         1,
			"id":                     uuid.NewString(),
			"stream":                 map[string]any{"kind": "session", "id": newID},
			"sequence":               sequence,
			"recorded_at":            baseMicros + sequence*recordSpacing,
			"record_type":            "event",
			"durability":             "durable",
			"causation_id":           nil,
			"payload_type":           payloadType,
			"payload_schema_version": 1,
			"payload":                payload,
		}
		if err := encoder.Encode(record); err != nil {
			return fmt.Errorf("failed to encode muse record: %w", err)
		}
		return nil
	}

	metadata := map[string]any{
		"kind": "metadata",
		"record": map[string]any{
			"workspace_root":       workspaceRoot,
			"provider_id":          "meta",
			"build":                map[string]any{"semver": reconstructedSemver},
			"tool_surface_version": reconstructedToolSurface,
			// Provenance back-link to the source session this was reconstructed
			// from, so the lineage stays traceable (matches claude/codex/qwen).
			"specstorySourceSessionId": data.SessionID,
		},
	}
	if err := emit(payloadTypeMetadata, metadata); err != nil {
		return nil, err
	}

	currentRun := ""
	closeRun := func() error {
		if currentRun == "" {
			return nil
		}
		err := emit(payloadTypeSession, museRunRecord(currentRun, map[string]any{
			"kind":     eventTerminal,
			"terminal": "completed",
			"reason":   nil,
		}))
		currentRun = ""
		return err
	}

	for _, turn := range turns {
		if turn.Role == schema.RoleUser {
			if err := closeRun(); err != nil {
				return nil, err
			}
			currentRun = uuid.NewString()
			if err := emit(payloadTypeSession, museRunRecord(currentRun, map[string]any{
				"kind":   eventStarted,
				"prompt": turn.Text,
			})); err != nil {
				return nil, err
			}
			continue
		}

		// A leading agent turn (the migration note) has no user turn to belong
		// to, so it opens a run of its own rather than being dropped.
		if currentRun == "" {
			currentRun = uuid.NewString()
		}
		if err := emit(payloadTypeSession, museRunRecord(currentRun, map[string]any{
			"kind":       eventAssistantMessageCommitted,
			"message_id": uuid.NewString(),
			"text":       turn.Text,
		})); err != nil {
			return nil, err
		}
	}

	if err := closeRun(); err != nil {
		return nil, err
	}

	// Close the session explicitly. Muse treats a transcript that ends without a
	// session.end record as a process that died, and on resume it writes its own
	// session.end with exit_reason "crash_inferred" and warns the user that the
	// previous run ended abnormally. Nothing crashed here: the transcript is
	// written complete in one pass, so it is closed as such.
	//
	// The record carries no resource_usage block, which is exactly the shape
	// Muse itself writes when it has no runtime measurements to report; uptime
	// is zero because no process ever ran.
	if err := emit(payloadTypeSessionEnd, map[string]any{
		"kind": "session_end",
		"record": map[string]any{
			"schema_version": 1,
			"session_id":     newID,
			"exit_reason":    "clean",
			"uptime_ms":      0,
		},
	}); err != nil {
		return nil, err
	}

	// Muse keys a session by directory, so the "filename" carries the session
	// directory: NativeSessionPath joins it under today's date shard and the
	// caller's MkdirAll creates the directory on the way to the file.
	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  filepath.Join(newID, sessionFileName),
		Content:   buf.Bytes(),
	}, nil
}

// museRunRecord wraps a run event in the runtime.session payload envelope.
func museRunRecord(runID string, event map[string]any) map[string]any {
	return map[string]any{
		"kind":   "run",
		"run_id": runID,
		"event":  event,
	}
}

// NativeSessionPath returns where a reconstructed transcript belongs in Muse's
// store: sessions/YYYY/MM/DD/<session-id>/session.jsonl under today's date
// shard. The directory is deliberately not created here — the caller writes the
// file and creates its parents, and SupportsReconstruction callers probe this
// method just to build target lists.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	sessionsRoot, err := GetMuseSessionsDir()
	if err != nil {
		return "", err
	}

	now := time.Now()
	return filepath.Join(
		sessionsRoot,
		fmt.Sprintf("%04d", now.Year()),
		fmt.Sprintf("%02d", int(now.Month())),
		fmt.Sprintf("%02d", now.Day()),
		filename,
	), nil
}

// SupportsReconstruction reports true: this provider has a native serializer
// (see ReconstructSession), so it can be a cross-agent resume target.
func (p *Provider) SupportsReconstruction() bool {
	return true
}
