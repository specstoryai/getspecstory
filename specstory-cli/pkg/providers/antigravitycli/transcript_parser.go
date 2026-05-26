package antigravitycli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Transcript step sources (the `source` field). Observed values are
// USER_EXPLICIT (the user prompt), SYSTEM (injected context/notices), and MODEL
// (the model's reasoning, tool calls, and tool results). Only USER_EXPLICIT is
// matched directly; the others are routed by `type`.
const sourceUserExplicit = "USER_EXPLICIT"

// Transcript step types (the `type` field). Tool calls are emitted on
// PLANNER_RESPONSE steps; each tool's result arrives as the immediately
// following step whose type is derived from the tool category (RUN_COMMAND,
// VIEW_FILE, CODE_ACTION, GREP_SEARCH, LIST_DIRECTORY, or GENERIC).
const (
	typeUserInput           = "USER_INPUT"
	typeConversationHistory = "CONVERSATION_HISTORY"
	typeSystemMessage       = "SYSTEM_MESSAGE"
	typePlannerResponse     = "PLANNER_RESPONSE"
	typeRunCommand          = "RUN_COMMAND"
	typeViewFile            = "VIEW_FILE"
	typeCodeAction          = "CODE_ACTION"
	typeGrepSearch          = "GREP_SEARCH"
	typeListDirectory       = "LIST_DIRECTORY"
	typeGeneric             = "GENERIC"
)

// maxTranscriptLineSize bounds JSONL sidecar scanning well above bufio's 64KB
// default while still placing a hard cap on malformed lines.
const maxTranscriptLineSize = 16 * 1024 * 1024

// historyEntry is one line of ~/.gemini/antigravity-cli/history.jsonl. It maps a
// prompt to the workspace it was issued in. NOTE: only interactive TUI sessions
// are logged here — `agy -p` (print mode) sessions are not — and the very first
// prompt of a session has an empty ConversationID (the id is assigned after the
// first turn completes).
type historyEntry struct {
	Display        string `json:"display"`
	Timestamp      int64  `json:"timestamp"` // epoch milliseconds
	Workspace      string `json:"workspace"` // absolute path, not symlink-resolved
	ConversationID string `json:"conversationId"`
}

// transcriptToolCall is one entry of a PLANNER_RESPONSE step's tool_calls array.
// Args keys are tool-specific and PascalCase (e.g. CommandLine, AbsolutePath,
// TargetFile); every tool also carries human-readable toolAction/toolSummary.
type transcriptToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// transcriptStep is one line of transcript_full.jsonl.
type transcriptStep struct {
	StepIndex int                  `json:"step_index"`
	Source    string               `json:"source"`
	Type      string               `json:"type"`
	Status    string               `json:"status"`
	CreatedAt string               `json:"created_at"` // RFC3339 UTC, second precision
	Content   string               `json:"content"`
	Thinking  string               `json:"thinking"`
	ToolCalls []transcriptToolCall `json:"tool_calls"`
}

// agSession is the parsed aggregate of one conversation — the analog of
// deepseektui's dsSession.
type agSession struct {
	ConversationID string
	Workspace      string // resolved from history.jsonl or inferred from tool paths
	CreatedAt      string // first step's created_at
	UpdatedAt      string // last step's created_at
	Model          string // derived from the USER_SETTINGS_CHANGE block, if present
	Steps          []transcriptStep
	TaskOutputs    map[int]string // async task output keyed by RUN_COMMAND step_index
	RawData        string         // full transcript bytes, retained only when wantRawData
}

// isToolResultType reports whether a step type represents a tool result (which
// always carries source MODEL and immediately follows its PLANNER_RESPONSE).
func isToolResultType(stepType string) bool {
	switch stepType {
	case typeRunCommand, typeViewFile, typeCodeAction, typeGrepSearch, typeListDirectory, typeGeneric:
		return true
	default:
		return false
	}
}

// loadHistoryIndex reads history.jsonl and returns a map conversationId →
// historyEntry. Lines without a conversationId (the first prompt of each
// session) are skipped because they cannot be attributed to a conversation yet;
// any later line for that session carries the id and the same workspace. A
// missing file yields an empty map and a nil error.
func loadHistoryIndex() (map[string]historyEntry, error) {
	path, err := resolveHistoryPath()
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]historyEntry{}, nil
		}
		return nil, fmt.Errorf("antigravity: cannot open history file: %w", err)
	}
	defer func() { _ = f.Close() }()

	index := make(map[string]historyEntry)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxTranscriptLineSize)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry historyEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			slog.Debug("antigravity: skipping malformed history line", "error", err)
			continue
		}
		if entry.ConversationID == "" {
			continue
		}
		index[entry.ConversationID] = entry
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("antigravity: cannot scan history file: %w", err)
	}

	return index, nil
}

// parseTranscript reads a conversation's transcript JSONL file and returns an
// agSession. When wantRawData is true the full file bytes are retained on
// RawData for cloud sync / debug-raw output; callers that only need the parsed
// structure pass false to avoid keeping a copy of the whole file in memory.
func parseTranscript(conversationID, transcriptPath string, history map[string]historyEntry, wantRawData bool) (*agSession, error) {
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		return nil, fmt.Errorf("antigravity: cannot read transcript %s: %w", transcriptPath, err)
	}

	// transcript.jsonl double-encodes every tool-arg value; transcript_full.jsonl
	// stores them natively. We only need to unescape when we fell back to the
	// former (see resolveTranscriptPath).
	fromFallback := strings.HasSuffix(transcriptPath, fallbackTranscriptFileName)

	var steps []transcriptStep
	for _, rawLine := range bytes.Split(data, []byte{'\n'}) {
		line := bytes.TrimSpace(rawLine)
		if len(line) == 0 {
			continue
		}
		var step transcriptStep
		if err := json.Unmarshal(line, &step); err != nil {
			slog.Debug("antigravity: skipping malformed transcript line",
				"conversationId", conversationID, "error", err)
			continue
		}
		if fromFallback {
			for i := range step.ToolCalls {
				step.ToolCalls[i].Args = normalizeFallbackArgs(step.ToolCalls[i].Args)
			}
		}
		steps = append(steps, step)
	}

	session := &agSession{
		ConversationID: conversationID,
		Steps:          steps,
	}
	if taskOutputs, err := loadTaskOutputs(transcriptPath); err == nil {
		session.TaskOutputs = taskOutputs
	} else {
		slog.Debug("antigravity: failed to load async task outputs",
			"conversationId", conversationID, "error", err)
	}
	if len(steps) > 0 {
		session.CreatedAt = strings.TrimSpace(steps[0].CreatedAt)
		session.UpdatedAt = strings.TrimSpace(steps[len(steps)-1].CreatedAt)
	}
	session.Workspace = resolveSessionWorkspace(conversationID, steps, history)
	session.Model = deriveModel(steps)
	if wantRawData {
		session.RawData = string(data)
	}

	return session, nil
}

// loadTaskOutputs reads optional async command logs from
// .system_generated/tasks/task-<step_index>.log. Antigravity names the task log
// after the RUN_COMMAND result step that remains RUNNING in the transcript.
func loadTaskOutputs(transcriptPath string) (map[int]string, error) {
	systemDir := filepath.Dir(filepath.Dir(transcriptPath))
	tasksDir := filepath.Join(systemDir, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	outputs := make(map[int]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "task-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		stepIndex, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "task-"), ".log"))
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(tasksDir, name))
		if err != nil {
			continue
		}
		if text := strings.TrimSpace(string(data)); text != "" {
			outputs[stepIndex] = text
		}
	}
	return outputs, nil
}

// normalizeFallbackArgs unescapes the double-encoded arg values found in
// transcript.jsonl. Each value there is itself a JSON-encoded scalar
// (string→string, "false"→bool, "5000"→int); decoding it once more recovers the
// native value. Values that don't decode are left as-is.
func normalizeFallbackArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return args
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		s, ok := v.(string)
		if !ok {
			out[k] = v
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(s), &decoded); err == nil {
			out[k] = decoded
		} else {
			out[k] = s
		}
	}
	return out
}

// sessionMetadata builds lightweight spi.SessionMetadata from an already-parsed
// session. Returns nil when the conversation has no usable user prompt — callers
// treat that as "skip".
func sessionMetadata(session *agSession, history map[string]historyEntry) *spi.SessionMetadata {
	prompt := firstUserPromptText(session)
	if prompt == "" {
		return nil
	}

	slug := spi.GenerateFilenameFromUserMessage(prompt)
	if slug == "" {
		slug = fallbackSlug
	}

	createdAt := session.CreatedAt
	if createdAt == "" {
		if entry, ok := history[session.ConversationID]; ok {
			createdAt = msEpochToRFC3339(entry.Timestamp)
		}
	}

	return &spi.SessionMetadata{
		SessionID: session.ConversationID,
		CreatedAt: createdAt,
		Slug:      slug,
		Name:      spi.GenerateReadableName(prompt),
	}
}

// firstUserPromptText returns the cleaned text of the first USER_INPUT step that
// carries a real prompt, or "" if none.
func firstUserPromptText(session *agSession) string {
	for _, step := range session.Steps {
		if step.Type != typeUserInput {
			continue
		}
		if text := cleanUserPrompt(step.Content); text != "" {
			return text
		}
	}
	return ""
}
