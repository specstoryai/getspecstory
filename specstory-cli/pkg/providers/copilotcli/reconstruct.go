package copilotcli

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

// reconstructedFileExt names a reconstructed session's file: the session ID
// plus this extension, which NativeSessionPath maps back to the session's own
// directory, since every Copilot session is a directory holding events.jsonl.
const reconstructedFileExt = ".jsonl"

// ReconstructSession rebuilds a Copilot CLI native session from the neutral
// SessionData so `copilot --resume=<id>` can continue the conversation.
//
// A session is an append-only events.jsonl whose records share an envelope
// (type, data, id, timestamp, parentId) chained through parentId. Loading
// needs only session.start followed by the conversation: user.message and
// assistant.message records. Tool calls and thinking are already flattened
// into agent text by spi.PrepareTurns, so no tool records are written, and no
// end-of-session record is needed: Copilot resumes such a file without
// warning and appends its own session.resume. See COPILOTCLI-FORMAT.md.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}
	cwd := spi.ResolveWorkspaceRoot(opts, data)
	// Copilot records the working directory symlinks resolved and in on-disk
	// case, and lists the session under the project only when the spelling
	// matches. The local destination is canonicalized; a root carried in the
	// source data is left verbatim, as it may name another machine's path.
	if opts.WorkspaceRoot != "" {
		cwd = spi.CanonicalizePathOrClean(cwd)
	}

	newID := uuid.NewString()
	base := time.Now().UTC()

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	var parentID any
	write := func(step int, eventType string, eventData map[string]any) error {
		id := uuid.NewString()
		record := map[string]any{
			"type":      eventType,
			"data":      eventData,
			"id":        id,
			"timestamp": spi.RFC3339Millis(base.Add(time.Duration(step) * time.Second)),
			"parentId":  parentID,
		}
		if err := enc.Encode(record); err != nil {
			return fmt.Errorf("failed to encode Copilot CLI %s record: %w", eventType, err)
		}
		parentID = id
		return nil
	}

	start := map[string]any{
		"sessionId": newID,
		"version":   1,
		"producer":  "copilot-agent",
		// Copilot refuses to load a session.start without this field, but
		// accepts it empty; the imported turns came from no Copilot version.
		"copilotVersion": "",
		"startTime":      spi.RFC3339Millis(base),
		"context":        map[string]any{"cwd": cwd},
		// Provenance back-link to the source session this was reconstructed
		// from, so the lineage stays traceable. See docs/SESSION-PORTABILITY.md.
		"specstorySourceSessionId": data.SessionID,
	}
	if err := write(0, eventSessionStart, start); err != nil {
		return nil, err
	}

	for i, turn := range turns {
		var err error
		if turn.Role == schema.RoleUser {
			err = write(i+1, eventUserMessage, map[string]any{"content": turn.Text})
		} else {
			err = write(i+1, eventAssistantMsg, map[string]any{
				"messageId":    uuid.NewString(),
				"content":      turn.Text,
				"toolRequests": []any{},
			})
		}
		if err != nil {
			return nil, err
		}
	}

	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  newID + reconstructedFileExt,
		Content:   buf.Bytes(),
	}, nil
}

// NativeSessionPath returns where a reconstructed session belongs in Copilot's
// store: $COPILOT_HOME/session-state/<id>/events.jsonl, the ID taken from the
// filename ReconstructSession suggested. The store is global (the project is
// recorded in session.start), so projectPath is unused. The directory is not
// required to exist; the caller creates it.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	sessionID := strings.TrimSuffix(filename, reconstructedFileExt)
	// ReconstructSession names files by a freshly minted UUID, the form every
	// Copilot session ID takes; anything else did not come from it.
	if _, err := uuid.Parse(sessionID); sessionID == filename || err != nil || !isValidSessionID(sessionID) {
		return "", fmt.Errorf("not a reconstructed Copilot CLI session filename: %q", filename)
	}
	root, err := sessionsRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, sessionID, eventsFileName), nil
}

// SupportsReconstruction reports true: ReconstructSession writes a session
// Copilot CLI resumes, so it can be a cross-agent resume target.
func (p *Provider) SupportsReconstruction() bool {
	return true
}
