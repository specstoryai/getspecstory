package claudecode

import (
	"log/slog"
	"strings"
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
// (a tool_use with no tool_result yet) and saves on an event once all shell
// calls have results. Only shell tools are gated: other tools do not trigger
// the diff, and gating them would only delay the transcript.
//
// See https://github.com/specstoryai/getspecstory/issues/317 for the full
// analysis and the reproduction.

// shellGateMaxWait is the fallback deadline, checked on the watcher tick.
// Prefer persistence over suppressing false edits indefinitely: elapsed time
// cannot distinguish a dead session from a live, long-running command. A forced
// save may therefore appear as a Bash edit after this deadline. Sync and final
// post-exit exports bypass the gate entirely.
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

// deferredSession identifies the merged session, which may span several JSONL
// files. All of its files must share one deadline and release it together.
type deferredSession struct {
	claudeProjectDir string
	sessionID        string
}

// deferredScan records the original deadline so repeated events cannot defer
// persistence indefinitely, plus the latest source file for a targeted rescan.
type deferredScan struct {
	file     string
	deadline time.Time
}

// Only the watcher loop accesses this registry while running. StopWatcher joins
// that loop before flushing it. Deadlines are checked in the same loop as event
// scans, so no timer goroutines can race conversion, saves, or shutdown.
var deferredScans = make(map[deferredSession]deferredScan)

func deferScan(session deferredSession, file string, openIDs []string) {
	if entry, pending := deferredScans[session]; pending {
		// Keep a current source path without extending the session's deadline.
		entry.file = file
		deferredScans[session] = entry
		return
	}
	slog.Info("shellGate: deferring session save while shell tool calls are open",
		"file", file,
		"sessionId", session.sessionID,
		"openToolUses", openIDs)
	deferredScans[session] = deferredScan{
		file:     file,
		deadline: time.Now().Add(shellGateMaxWait),
	}
}

func clearDeferredScan(session deferredSession) {
	delete(deferredScans, session)
}

// flushExpiredDeferredScans runs on the watcher loop, after event processing.
// Looking up current entries rather than queuing timeout callbacks means a
// completed call's old deadline cannot force a save for a later call.
func flushExpiredDeferredScans(now time.Time) {
	for session, entry := range deferredScans {
		if !now.Before(entry.deadline) {
			slog.Warn("shellGate: deferral deadline expired; saving even if shell calls remain open",
				"file", entry.file, "sessionId", session.sessionID, "maxWait", shellGateMaxWait)
			flushDeferredScan(session, entry)
		}
	}
}

// flushDeferredScans attempts to save every deferred session after the watcher
// has stopped, while its callback is still installed.
func flushDeferredScans() {
	for session, entry := range deferredScans {
		slog.Info("shellGate: flushing deferred session save on shutdown", "file", entry.file, "sessionId", session.sessionID)
		flushDeferredScan(session, entry)
	}
}

func flushDeferredScan(session deferredSession, entry deferredScan) {
	// Consume this deadline even if parsing fails; a subsequent file event can
	// retry, without repeatedly forcing a broken file on every watcher tick.
	clearDeferredScan(session)
	scanJSONLFilesWithOptions(session.claudeProjectDir, entry.file, true)
}
