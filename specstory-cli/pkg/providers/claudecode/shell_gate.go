package claudecode

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Claude Code 2.1.269+ diffs the git working tree before and after every Bash
// tool call (setting bashEditDiffEnabled, on by default in auto and bypass
// permission modes) and attributes every file that changed in between to that
// command, rendering it in the conversation as an edit. Claude Code appends the
// assistant record carrying the tool_use block before the command starts, so a
// save triggered by that record lands inside the diff window and SpecStory's own
// files show up as edits made by the command.
//
// The gate below defers a session's save while a shell tool call is still open
// (a tool_use with no tool_result yet) and lets the save through on the next
// JSONL event, which is normally the tool_result itself. Only shell tools are
// gated: other tools do not trigger the diff, and gating them would only delay
// the transcript.
//
// See https://github.com/specstoryai/getspecstory/issues/317 for the full
// analysis and the reproduction.

// shellGateMaxWait bounds how long a session save can stay deferred. Claude
// Code always closes a tool call with a tool_result, even when interrupted, so
// the timer only fires when Claude was killed mid-command. It is long on
// purpose: a flush during a still-running command would be attributed to it.
const shellGateMaxWait = 5 * time.Minute

// shellToolNames are the Claude Code tools whose calls trigger the edit diff.
var shellToolNames = map[string]bool{
	"bash":       true,
	"powershell": true,
}

// isShellToolName reports whether a Claude Code tool name is a shell tool.
func isShellToolName(name string) bool {
	return shellToolNames[strings.ToLower(name)]
}

// openShellToolUses returns the ids of shell tool calls in the current exchange
// that have no tool_result yet. The set resets on every regular user prompt,
// since a new prompt means the previous turn is over. Sidechain (subagent)
// records count like main-thread ones: a subagent's Bash call runs the same
// diff, and a sidechain user message is a prompt to the subagent, not a new
// turn, so it does not reset the set.
func openShellToolUses(records []JSONLRecord) []string {
	open := make(map[string]bool)
	var order []string

	for _, record := range records {
		recordType, _ := record.Data["type"].(string)
		message, _ := record.Data["message"].(map[string]interface{})
		content, _ := message["content"].([]interface{})

		switch recordType {
		case "assistant":
			for _, part := range content {
				block, ok := part.(map[string]interface{})
				if !ok || block["type"] != "tool_use" {
					continue
				}
				name, _ := block["name"].(string)
				id, _ := block["id"].(string)
				if id == "" || !isShellToolName(name) {
					continue
				}
				if !open[id] {
					open[id] = true
					order = append(order, id)
				}
			}

		case "user":
			isToolResult := false
			for _, part := range content {
				block, ok := part.(map[string]interface{})
				if !ok || block["type"] != "tool_result" {
					continue
				}
				isToolResult = true
				if id, _ := block["tool_use_id"].(string); id != "" {
					delete(open, id)
				}
			}
			isSidechain, _ := record.Data["isSidechain"].(bool)
			if !isToolResult && !isSidechain {
				// A new prompt on the main thread: whatever was open belongs
				// to a finished turn
				open = make(map[string]bool)
				order = nil
			}
		}
	}

	ids := order[:0]
	for _, id := range order {
		if open[id] {
			ids = append(ids, id)
		}
	}
	return ids
}

// deferredScans tracks session files whose save was deferred by the shell gate,
// keyed by JSONL path, with the fallback timer that forces the save if no
// further event arrives.
type deferredScan struct {
	claudeProjectDir string
	timer            *time.Timer
}

var (
	deferredScansMu sync.Mutex
	deferredScans   = make(map[string]deferredScan)
)

// deferScan records that a scan of file was skipped because a shell tool call
// is open, arming the fallback timer if none is running. An existing timer is
// left alone so a stream of events during one long command cannot postpone the
// fallback indefinitely.
func deferScan(claudeProjectDir, file string, openIDs []string) {
	deferredScansMu.Lock()
	defer deferredScansMu.Unlock()

	if _, armed := deferredScans[file]; armed {
		return
	}
	slog.Info("shellGate: deferring session save while shell tool calls are open",
		"file", file,
		"openToolUses", openIDs)
	timer := time.AfterFunc(shellGateMaxWait, func() {
		slog.Warn("shellGate: no tool_result arrived within the wait window, saving anyway",
			"file", file,
			"maxWait", shellGateMaxWait)
		scanJSONLFilesWithOptions(claudeProjectDir, file, true)
	})
	deferredScans[file] = deferredScan{claudeProjectDir: claudeProjectDir, timer: timer}
}

// clearDeferredScan stops the fallback timer for file, if any, because its
// scan went through.
func clearDeferredScan(file string) {
	deferredScansMu.Lock()
	defer deferredScansMu.Unlock()

	if entry, ok := deferredScans[file]; ok {
		entry.timer.Stop()
		delete(deferredScans, file)
	}
}

// flushDeferredScans forces a save of every deferred session. Called on
// shutdown, while the session callback is still registered, so the last state
// of a session is never lost to the gate.
func flushDeferredScans() {
	deferredScansMu.Lock()
	pending := make(map[string]string, len(deferredScans))
	for file, entry := range deferredScans {
		entry.timer.Stop()
		pending[file] = entry.claudeProjectDir
	}
	deferredScans = make(map[string]deferredScan)
	deferredScansMu.Unlock()

	for file, claudeProjectDir := range pending {
		slog.Info("shellGate: flushing deferred session save on shutdown", "file", file)
		scanJSONLFilesWithOptions(claudeProjectDir, file, true)
	}
}
