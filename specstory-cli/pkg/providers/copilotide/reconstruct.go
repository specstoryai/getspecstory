package copilotide

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/term"
	_ "modernc.org/sqlite" // Pure Go SQLite driver, for the workspace state.vscdb index write

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

const (
	// copilotSessionVersion is the chatSessions serialization version current VS Code
	// writes (the `version` field inside the kind:0 snapshot).
	copilotSessionVersion = 3

	// copilotResponderUsername mirrors the responderUsername VS Code stamps on sessions.
	copilotResponderUsername = "GitHub Copilot"

	// copilotModelID is the model identifier VS Code uses for automatic model selection;
	// reconstructed turns did not run through a specific Copilot model, so "auto" is the
	// honest label and lets the user continue with whatever model VS Code picks.
	copilotModelID = "copilot/auto"

	// importedRequestText hosts leading agent turns (e.g. the migration note): VS Code's
	// chat model is strictly request(user) -> response(agent) pairs, so an agent turn
	// with no preceding user turn needs a synthetic host request.
	importedRequestText = "Session imported via SpecStory."

	// chatSessionIndexKey is the ItemTable key in the workspace state.vscdb where VS Code
	// keeps its chat session index. Sessions absent from this index do not appear in the
	// Chat panel's session list, so reconstruction must register the new session here.
	chatSessionIndexKey = "chat.ChatSessionStore.index"

	// indexResponseStateComplete is the lastResponseState value VS Code records on
	// sessions whose final response finished normally.
	indexResponseStateComplete = 1
)

// ReconstructSession rebuilds a VS Code Copilot native session from the neutral
// SessionData: a single kind:0 JSONL snapshot (the format current VS Code writes to
// chatSessions/<sessionId>.jsonl) returned as Content for the caller to write via
// NativeSessionPath, plus a session entry registered in the workspace state.vscdb
// chat.ChatSessionStore.index — without which VS Code never shows the session.
//
// Flattened turns are paired into VS Code request blocks: each user turn opens a
// request, consecutive agent turns collapse into its markdown response. VS Code must be
// fully quit and restarted to pick up the index entry — it holds the index in memory in
// the main process and flushes it over ours on shutdown, and a "Developer: Reload
// Window" keeps that process alive, so a reload is not enough (verified empirically).
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}

	workspaceRoot := spi.ResolveWorkspaceRoot(opts, data)
	if workspaceRoot == "" {
		return nil, fmt.Errorf("cannot register session in VS Code: no workspace root provided")
	}
	workspace, wsErr := p.findWorkspaceForReconstruction(workspaceRoot)
	if wsErr != nil {
		return nil, fmt.Errorf("cannot register session in %s: no workspace found for %q (open the folder in %s once first): %w", p.variant.AppName, workspaceRoot, p.variant.AppName, wsErr)
	}

	// A running instance of this variant would erase the session: VS Code holds
	// the chat-session index in memory and flushes it over our write when it
	// exits. Hold the write until the app is fully quit (prompting the user),
	// so the registration below actually survives to the next startup.
	p.waitUntilAppQuit()

	newID := uuid.NewString()
	nowMs := time.Now().UnixMilli()
	title := spi.ResumedSessionTitle(data.Slug)

	requests := buildRequestBlocks(turns, nowMs)
	// Match the timestamp of the last request block (staggered 1s per request
	// starting at nowMs) so the session's "last updated" ordering is exact.
	lastMs := nowMs
	if n := len(requests); n > 0 {
		lastMs = nowMs + int64(n-1)*1000
	}

	// The file mirrors a session VS Code itself compacted on close: a single
	// kind:0 snapshot with the conversation inline and the same top-level key
	// set (verified against a VS Code 1.131-authored file that loads
	// correctly). inputState is deliberately omitted even though VS Code
	// serializes one: it embeds per-user model and auth metadata (including the
	// GitHub account label), which must not be templated into reconstructed
	// sessions; VS Code defaults it when absent.
	composer := map[string]any{
		"version":           copilotSessionVersion,
		"sessionId":         newID,
		"creationDate":      nowMs,
		"customTitle":       title,
		"lastMessageDate":   lastMs,
		"isImported":        true,
		"initialLocation":   "panel",
		"hasPendingEdits":   false,
		"pendingRequests":   []any{},
		"responderUsername": copilotResponderUsername,
		"requests":          requests,
	}

	var content bytes.Buffer
	line, err := json.Marshal(map[string]any{"kind": 0, "v": composer})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal session snapshot: %w", err)
	}
	content.Write(line)
	content.WriteByte('\n')

	entry := sessionIndexEntry{
		SessionID:       newID,
		Title:           title,
		LastMessageDate: lastMs,
		Timing: sessionIndexTiming{
			Created:            nowMs,
			LastRequestStarted: lastMs,
			LastRequestEnded:   lastMs,
		},
		InitialLocation:   "panel",
		LastResponseState: indexResponseStateComplete,
		PermissionLevel:   "default",
	}
	if err := writeSessionIndexEntry(workspace.StateDBPath(), entry); err != nil {
		return nil, fmt.Errorf("failed to register session in VS Code workspace index: %w", err)
	}

	slog.Info("Reconstructed session registered in VS Code workspace index",
		"sessionID", newID, "workspaceID", workspace.ID, "turns", len(turns), "requests", len(requests))

	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  newID + ".jsonl",
		Content:   content.Bytes(),
	}, nil
}

// waitUntilAppQuit blocks while this variant's app is running, prompting the
// user to quit it: an external write to the chat-session index only survives
// when the app's process is fully dead. Detection is per variant, so a running
// stock VS Code doesn't hold up an Insiders-targeted reconstruction. When
// stdin isn't a terminal (scripted resume), prompting is impossible: warn once
// and proceed so automation isn't wedged — the session file still lands, only
// its panel registration is at risk.
func (p *Provider) waitUntilAppQuit() {
	if !p.appRunning() {
		return
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "\nWarning: %s is running — the imported session may not appear in its Chat panel. Quit it fully before resuming to avoid this.\n", p.variant.AppName)
		return
	}
	// On macOS, closing every window still leaves the app running — only a real
	// quit (Cmd+Q) kills the process. On Linux, closing the last window exits.
	howToQuit := "Quit it fully"
	if runtime.GOOS == "darwin" {
		howToQuit = "Quit it fully (Cmd+Q — closing its windows is not enough)"
	}
	reader := bufio.NewReader(os.Stdin)
	for p.appRunning() {
		fmt.Fprintf(os.Stderr, "\n%s is running, and the imported session would be lost when it exits.\n%s, then press Enter to continue (Ctrl-C aborts): ", p.variant.AppName, howToQuit)
		if _, err := reader.ReadString('\n'); err != nil {
			// Stdin closed mid-prompt: proceed best-effort rather than hang.
			return
		}
	}
	fmt.Fprintln(os.Stderr)
}

// appRunning reports whether any process of this variant's application is
// alive. On macOS the app bundle path is the reliable discriminator; on Linux
// the launcher binary name is matched as a path segment, with a trailing
// boundary so e.g. "code" cannot match "codex".
func (p *Provider) appRunning() bool {
	var pattern string
	if runtime.GOOS == "darwin" {
		pattern = p.variant.BundleName + ".app/Contents/MacOS"
	} else {
		pattern = "/" + p.variant.Command + "( |$)"
	}
	out, err := exec.Command("pgrep", "-f", pattern).Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// NativeSessionPath resolves where a reconstructed session file belongs: the matched
// workspace's chatSessions directory. The directory may not exist yet — the caller
// creates it — so the lookup must not require it (findWorkspaceForReconstruction).
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	workspace, err := p.findWorkspaceForReconstruction(projectPath)
	if err != nil {
		return "", fmt.Errorf("no %s workspace found for %q (open the folder in %s once first): %w", p.variant.AppName, projectPath, p.variant.AppName, err)
	}
	return filepath.Join(GetChatSessionsPath(workspace.Dir), filename), nil
}

// SupportsReconstruction reports true: this provider has a native serializer
// (see ReconstructSession), so it can be a cross-agent resume target.
func (p *Provider) SupportsReconstruction() bool {
	return true
}

// buildRequestBlocks pairs the flattened turns into VS Code request blocks: a user turn
// opens a request, consecutive agent turns join into its single markdown response item.
// Leading agent turns (the migration note) get a synthetic host request, and a trailing
// user turn simply carries an empty response. Timestamps are staggered one second per
// request so ordering is stable.
func buildRequestBlocks(turns []spi.Turn, baseMs int64) []map[string]any {
	var blocks []map[string]any
	var userText string
	var agentParts []string
	open := false

	flush := func() {
		if !open {
			return
		}
		ts := baseMs + int64(len(blocks))*1000
		blocks = append(blocks, buildRequestBlock(userText, strings.Join(agentParts, "\n\n"), ts))
		userText, agentParts, open = "", nil, false
	}

	for _, turn := range turns {
		if turn.Role == schema.RoleUser {
			flush()
			userText, open = turn.Text, true
		} else {
			if !open {
				userText, open = importedRequestText, true
			}
			agentParts = append(agentParts, turn.Text)
		}
	}
	flush()

	return blocks
}

// requestAgentTemplate and requestModeInfoTemplate are the chat-participant and
// mode descriptors copied verbatim from a request VS Code 1.131 (Copilot Chat
// 0.60.0) serialized itself. VS Code's session revival drops requests that lack
// their agent identity and completed-state fields — the session then loads
// empty and is auto-deleted — so every reconstructed request carries the stock
// Copilot agent-mode descriptors. The extension version inside is a snapshot,
// not a constraint: VS Code reconciles the descriptor with the live agent
// registry at load.
const requestAgentTemplate = `{"extensionId":{"value":"GitHub.copilot-chat","_lower":"github.copilot-chat"},"extensionVersion":"0.60.0","publisherDisplayName":"GitHub","extensionPublisherId":"GitHub","extensionDisplayName":"GitHub Copilot","id":"github.copilot.editsAgent","description":"Edit files in your workspace in agent mode","when":"config.chat.agent.enabled","metadata":{"themeIcon":{"id":"tools"},"hasFollowups":false,"supportIssueReporting":false},"name":"agent","fullName":"GitHub Copilot","isDefault":true,"locations":["panel"],"modes":["agent"],"slashCommands":[]}`

const requestModeInfoTemplate = `{"kind":"agent","isBuiltin":true,"telemetryModeId":"agent","telemetryModeName":"agent","permissionLevel":"default"}`

// requestModelStateComplete is the modelState value VS Code records on a
// request whose response finished normally.
const requestModelStateComplete = 1

// buildRequestBlock serializes one user->agent exchange mirroring a completed
// request from a VS Code-authored session file field-for-field: the message
// part carries kind "text" plus range, editorRange, and text (verified against
// a VS Code 1.131 file — earlier VS Code builds serialized parts without the
// kind discriminator, so match the current shape, not old files), the response
// is a serialized markdown string (an object with a `value` field and no
// `kind`), and the completed-state fields — agent, modeInfo, modelState,
// result, responseTimestamp — are present because VS Code's revival treats
// requests without them as unloadable.
func buildRequestBlock(userText, agentText string, tsMs int64) map[string]any {
	lines := strings.Split(userText, "\n")

	response := []any{}
	if agentText != "" {
		response = append(response, map[string]any{
			"value":             agentText,
			"supportThemeIcons": false,
			"supportHtml":       false,
		})
	}

	return map[string]any{
		"requestId":         "request_" + uuid.NewString(),
		"responseId":        "response_" + uuid.NewString(),
		"timestamp":         tsMs,
		"responseTimestamp": tsMs,
		"timeSpentWaiting":  0,
		"modelId":           copilotModelID,
		"agent":             json.RawMessage(requestAgentTemplate),
		"modeInfo":          json.RawMessage(requestModeInfoTemplate),
		"modelState": map[string]any{
			"value":       requestModelStateComplete,
			"completedAt": tsMs,
		},
		"message": map[string]any{
			"text": userText,
			"parts": []any{map[string]any{
				"kind": "text",
				"text": userText,
				"range": map[string]any{
					"start":        0,
					"endExclusive": utf16Len(userText),
				},
				"editorRange": map[string]any{
					"startLineNumber": 1,
					"startColumn":     1,
					"endLineNumber":   len(lines),
					"endColumn":       utf16Len(lines[len(lines)-1]) + 1,
				},
			}},
		},
		"response": response,
		"result": map[string]any{
			"timings":  map[string]any{"firstProgress": 0, "totalElapsed": 0},
			"metadata": map[string]any{},
		},
		"variableData":      map[string]any{"variables": []any{}},
		"followups":         []any{},
		"contentReferences": []any{},
		"codeCitations":     []any{},
	}
}

// utf16Len returns the length of s in UTF-16 code units — the unit VS Code uses for
// offsets and columns in serialized ranges.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++ // surrogate pair
		}
	}
	return n
}

// sessionIndexEntry is one entry in VS Code's chat.ChatSessionStore.index, keyed by
// session ID. isEmpty/isExternal/hasPendingEdits marshal explicitly (not omitempty)
// because VS Code stores them as literal false on real entries.
type sessionIndexEntry struct {
	SessionID         string             `json:"sessionId"`
	Title             string             `json:"title"`
	LastMessageDate   int64              `json:"lastMessageDate"`
	Timing            sessionIndexTiming `json:"timing"`
	InitialLocation   string             `json:"initialLocation"`
	HasPendingEdits   bool               `json:"hasPendingEdits"`
	IsEmpty           bool               `json:"isEmpty"`
	IsExternal        bool               `json:"isExternal"`
	LastResponseState int                `json:"lastResponseState"`
	PermissionLevel   string             `json:"permissionLevel"`
}

// sessionIndexTiming mirrors the timing sub-object of an index entry.
type sessionIndexTiming struct {
	Created            int64 `json:"created"`
	LastRequestStarted int64 `json:"lastRequestStarted,omitempty"`
	LastRequestEnded   int64 `json:"lastRequestEnded,omitempty"`
}

// writeSessionIndexEntry merges entry into the chat.ChatSessionStore.index value of the
// given workspace state.vscdb, preserving all existing entries byte-for-byte (they are
// kept as raw JSON, never re-marshaled). A missing index row starts a fresh one. The
// write races VS Code's own in-memory copy — a running VS Code flushes its version over
// ours on shutdown — which is why the resume flow tells the user to restart VS Code.
func writeSessionIndexEntry(dbPath string, entry sessionIndexEntry) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open workspace state database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close workspace state database", "error", closeErr)
		}
	}()
	db.SetMaxOpenConns(1)
	// Wait out short-lived locks from a running VS Code instead of failing immediately.
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		slog.Warn("Failed to set busy_timeout on workspace state database", "error", err)
	}

	index := struct {
		Version int                        `json:"version"`
		Entries map[string]json.RawMessage `json:"entries"`
	}{Version: 1, Entries: map[string]json.RawMessage{}}

	var raw string
	scanErr := db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", chatSessionIndexKey).Scan(&raw)
	switch {
	case scanErr == nil:
		if err := json.Unmarshal([]byte(raw), &index); err != nil {
			return fmt.Errorf("failed to parse existing chat session index: %w", err)
		}
		if index.Entries == nil {
			index.Entries = map[string]json.RawMessage{}
		}
	case errors.Is(scanErr, sql.ErrNoRows):
		// No index yet (chat never used in this workspace) — start a fresh one.
	default:
		return fmt.Errorf("failed to read chat session index: %w", scanErr)
	}

	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal index entry: %w", err)
	}
	index.Entries[entry.SessionID] = entryJSON

	indexJSON, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("failed to marshal chat session index: %w", err)
	}

	if _, err := db.Exec(
		"INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)",
		chatSessionIndexKey, string(indexJSON),
	); err != nil {
		return fmt.Errorf("failed to write chat session index: %w", err)
	}
	return nil
}
