package cursoride

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// cursorBubbleTypeUser and cursorBubbleTypeAssistant are the type values Cursor IDE
// uses to distinguish user and assistant bubbles in ComposerConversation rows.
const (
	cursorBubbleTypeUser      = 1
	cursorBubbleTypeAssistant = 2

	// cursorUnifiedModeAgent is the unifiedMode value for Cursor's "Agent" mode.
	// Reconstructed sessions use this mode since cross-provider imports are agent-style.
	cursorUnifiedModeAgent = 2
)

// ReconstructSession rebuilds a Cursor IDE native session from the neutral SessionData
// and writes it directly into the global state.vscdb. Cursor IDE stores all sessions
// as key-value rows in a single shared SQLite database rather than per-session files,
// so the write happens here instead of in the caller's writeReconstructedSession step.
//
// One composerData:* row is written for the session metadata, and one bubbleId:*:* row
// is written per flattened turn. Tool calls and thinking are already collapsed into plain
// text by FlattenSessionData; we store them verbatim in the bubble's Text field.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}

	globalDbPath, err := GetGlobalDatabasePath()
	if err != nil {
		return nil, fmt.Errorf("cursor IDE global database not found: %w", err)
	}

	// Resolve workspace early — we need workspace.ID for the workspaceIdentifier field
	// in composerData, which Cursor uses to associate sessions with their project.
	workspaceRoot := spi.ResolveWorkspaceRoot(opts, data)
	if workspaceRoot == "" {
		return nil, fmt.Errorf("cannot register session in Cursor IDE: no workspace root provided")
	}
	workspace, wsErr := FindWorkspaceForProject(workspaceRoot)
	if wsErr != nil {
		return nil, fmt.Errorf("cannot register session in Cursor IDE: no workspace found for %q: %w", workspaceRoot, wsErr)
	}

	newID := uuid.NewString()
	nowMs := time.Now().UnixMilli()

	// Build one bubble per flattened turn.
	bubbles := make([]ComposerConversation, len(turns))
	headers := make([]ComposerConversationHeader, len(turns))
	for i, turn := range turns {
		bubbleID := uuid.NewString()
		bubbleType := cursorBubbleTypeAssistant
		if turn.Role == schema.RoleUser {
			bubbleType = cursorBubbleTypeUser
		}
		tsMs := float64(nowMs + int64(i)*1000)
		createdAt := time.UnixMilli(nowMs + int64(i)*1000).UTC().Format(time.RFC3339Nano)
		bubbles[i] = ComposerConversation{
			BubbleID:    bubbleID,
			Type:        bubbleType,
			Text:        turn.Text,
			UnifiedMode: cursorUnifiedModeAgent,
			TimingInfo: &TimingInfo{
				ClientStartTime: tsMs,
				ClientEndTime:   tsMs + 500,
			},
		}
		// grouping.isRenderable must be true; without it Cursor skips the bubble entirely.
		headers[i] = ComposerConversationHeader{
			BubbleID:  bubbleID,
			Type:      bubbleType,
			Grouping:  &BubbleGrouping{IsRenderable: true, HasText: turn.Text != ""},
			CreatedAt: createdAt,
		}
	}

	// Build a complete composerData JSON that matches Cursor's expected schema.
	// Cursor filters sessions based on fields like hasLoaded, isDraft, status, and isAgentic;
	// omitting them causes the session to be invisible in the UI even if the data is present.

	composerMap := map[string]interface{}{
		// Identity
		"_v":         16,
		"composerId": newID,
		"name":       spi.ResumedSessionTitle(data.Slug),
		// Visibility-critical fields: Cursor hides sessions where these are absent or wrong.
		"hasLoaded":   true,
		"isDraft":     false,
		"status":      "completed",
		"isAgentic":   true,
		"unifiedMode": "agent", // string at composer level (int on bubbles)
		// Timestamps
		"createdAt":     nowMs,
		"lastUpdatedAt": nowMs + int64(len(turns))*1000,
		// workspaceIdentifier associates this session with its workspace so Cursor's Agent
		// sidebar shows it under the right project. Without this field Cursor ignores the session.
		"workspaceIdentifier": map[string]interface{}{
			"id": workspace.ID,
			"uri": map[string]interface{}{
				"$mid":     1,
				"fsPath":   workspaceRoot,
				"external": "file://" + workspaceRoot,
				"path":     workspaceRoot,
				"scheme":   "file",
			},
		},
		// Conversation index
		"fullConversationHeadersOnly": headers,
		// Standard empty fields that Cursor expects to be present.
		"richText":                      `{"root":{"children":[{"children":[],"format":"","indent":0,"type":"paragraph","version":1}],"format":"","indent":0,"type":"root","version":1}}`,
		"text":                          "",
		"conversationMap":               map[string]interface{}{},
		"generatingBubbleIds":           []interface{}{},
		"isReadingLongFile":             false,
		"codeBlockData":                 map[string]interface{}{},
		"originalFileStates":            map[string]interface{}{},
		"newlyCreatedFiles":             []interface{}{},
		"newlyCreatedFolders":           []interface{}{},
		"hasChangedContext":             false,
		"activeTabsShouldBeReactive":    false,
		"capabilities":                  []interface{}{},
		"context":                       map[string]interface{}{},
		"isFileListExpanded":            false,
		"canvasPillCollapsed":           false,
		"browserChipManuallyDisabled":   false,
		"browserChipManuallyEnabled":    false,
		"forceMode":                     "edit",
		"usageData":                     map[string]interface{}{},
		"contextUsagePercent":           0,
		"allAttachedFileCodeChunksUris": []interface{}{},
		"modelConfig":                   map[string]interface{}{"modelName": "default", "maxMode": false, "selectedModels": []interface{}{}},
		"subComposerIds":                []interface{}{},
		"subagentComposerIds":           []interface{}{},
		"capabilityContexts":            []interface{}{},
		"todos":                         []interface{}{},
		"isQueueExpanded":               true,
		"hasUnreadMessages":             false,
		"gitHubPromptDismissed":         false,
		"totalLinesAdded":               0,
		"totalLinesRemoved":             0,
		"addedFiles":                    0,
		"removedFiles":                  0,
		"filesChangedCount":             0,
		"subtitle":                      "",
		"isCreatingWorktree":            false,
		"isApplyingWorktree":            false,
		"isUndoingWorktree":             false,
		"applied":                       false,
		"pendingCreateWorktree":         false,
		"worktreeStartedReadOnly":       false,
		"isBestOfNSubcomposer":          false,
		"isBestOfNParent":               false,
		"isSpec":                        false,
		"isProject":                     false,
		"isSpecSubagentDone":            false,
		"isContinuationInProgress":      false,
		"stopHookLoopCount":             0,
		"trackedGitRepos":               []interface{}{},
		"isNAL":                         true,
		"agentBackend":                  "cursor-agent",
		"planModeSuggestionUsed":        false,
		"debugModeSuggestionUsed":       false,
		"queueItems":                    []interface{}{},
	}
	composerJSON, err := json.Marshal(composerMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal composer data: %w", err)
	}

	db, err := OpenDatabaseReadWrite(globalDbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open Cursor IDE database for writing: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close Cursor IDE database after reconstruction", "error", closeErr)
		}
	}()

	if err := InsertComposerSession(db, newID, composerJSON, bubbles); err != nil {
		return nil, fmt.Errorf("failed to write reconstructed session to Cursor IDE database: %w", err)
	}

	slog.Info("Reconstructed session written to Cursor IDE global database",
		"composerID", newID, "turns", len(turns), "dbPath", globalDbPath)

	regMeta := ComposerHeadMeta{
		ComposerID:    newID,
		Name:          spi.ResumedSessionTitle(data.Slug),
		CreatedAt:     nowMs,
		LastUpdatedAt: nowMs + int64(len(turns))*1000,
		WorkspaceID:   workspace.ID,
	}
	// Write to the global composer.composerHeaders (ItemTable) — the authoritative source that
	// composerDataService.allComposersData.allComposers is loaded from on startup.
	// Sessions absent from this key are invisible in the sidebar regardless of other indexes.
	if hdrErr := WriteGlobalComposerHeader(globalDbPath, regMeta, workspaceRoot); hdrErr != nil {
		return nil, fmt.Errorf("failed to write global composer header: %w", hdrErr)
	}
	// Append to selectedComposerIds so Cursor opens the resumed session as an active tab.
	// This is a UX convenience only — sidebar visibility comes from composer.composerHeaders above.
	if selErr := AppendToSelectedComposerIDs(workspace.DBPath, newID); selErr != nil {
		slog.Warn("Failed to register session as active tab in workspace", "composerID", newID, "error", selErr)
	}
	slog.Info("Reconstructed session registered in Cursor IDE",
		"composerID", newID, "workspaceID", workspace.ID)

	// The resume flow writes rec.Content to the path returned by NativeSessionPath.
	// Cursor IDE uses a shared global DB rather than per-session files, so we return
	// empty content; NativeSessionPath points to a harmless temp path.
	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  newID,
		Content:   []byte{},
	}, nil
}

// NativeSessionPath returns a temp path where the resume flow writes the (empty) sentinel
// content. Cursor IDE sessions live in the shared global state.vscdb, not per-session
// files, so the actual data was already written in ReconstructSession. The temp file is
// harmless and will be cleaned up by the OS.
func (p *Provider) NativeSessionPath(_ string, filename string) (string, error) {
	return filepath.Join(os.TempDir(), "specstory-cursor-"+filename), nil
}
