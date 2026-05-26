package antigravitycli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Type aliases for convenience — use the shared schema types.
type (
	SessionData  = schema.SessionData
	ProviderInfo = schema.ProviderInfo
	Exchange     = schema.Exchange
	Message      = schema.Message
	ContentPart  = schema.ContentPart
	ToolInfo     = schema.ToolInfo
)

const (
	providerSchemaID = "antigravity-cli"
	providerName     = "Antigravity CLI"
	fallbackSlug     = "antigravity-session"
)

// Antigravity wraps the real prompt in <USER_REQUEST> and appends
// <ADDITIONAL_METADATA> / <USER_SETTINGS_CHANGE> blocks that must not be shown.
var (
	userRequestRe   = regexp.MustCompile(`(?s)<USER_REQUEST>(.*?)</USER_REQUEST>`)
	metadataBlockRe = regexp.MustCompile(`(?s)<(ADDITIONAL_METADATA|USER_SETTINGS_CHANGE|SYSTEM_MESSAGE)>.*?</(ADDITIONAL_METADATA|USER_SETTINGS_CHANGE|SYSTEM_MESSAGE)>`)
	// modelRe pulls the model name out of the first turn's <USER_SETTINGS_CHANGE>
	// block, e.g. "...changed setting `Model Selection` from None to Gemini 3.5
	// Flash (High). ..." → "Gemini 3.5 Flash (High)". The terminator is a period
	// followed by whitespace or end-of-string so the decimal point in a version
	// like "3.5" does not cut the match short.
	modelRe = regexp.MustCompile(`Model Selection.{0,4}from .+? to (.+?)\.(?:\s|$)`)
)

// toolPathArgKeys are the tool-arg keys that carry absolute filesystem paths.
// They are the only on-disk signal of a print-mode session's workspace, since
// the transcript has no workspace field (see the format spec, §5).
var (
	toolPathArgKeys      = []string{"Cwd", "AbsolutePath", "TargetFile", "SearchPath", "DirectoryPath"}
	workspaceDirArgKeys  = []string{"Cwd", "SearchPath", "DirectoryPath"}
	workspaceFileArgKeys = []string{"AbsolutePath", "TargetFile"}
)

// convertToAgentSession converts a parsed agSession into the unified
// AgentChatSession format. Returns nil for empty/unconvertible sessions.
func convertToAgentSession(session *agSession, workspaceRoot string, debugRaw bool) *spi.AgentChatSession {
	if session == nil || len(session.Steps) == 0 {
		return nil
	}

	sessionData, err := generateAgentSession(session, workspaceRoot)
	if err != nil {
		slog.Debug("antigravity: skipping session due to conversion error",
			"conversationId", session.ConversationID, "error", err)
		return nil
	}

	if debugRaw {
		writeDebugRaw(session)
	}

	return &spi.AgentChatSession{
		SessionID:   session.ConversationID,
		CreatedAt:   sessionData.CreatedAt,
		Slug:        sessionData.Slug,
		SessionData: sessionData,
		RawData:     session.RawData,
	}
}

// generateAgentSession converts an agSession into the shared SessionData schema.
func generateAgentSession(session *agSession, workspaceRoot string) (*SessionData, error) {
	// Prefer the session's own resolved workspace; fall back to the project the
	// CLI is syncing from so WorkspaceRoot is never empty.
	workspace := strings.TrimSpace(workspaceRoot)
	if ws := strings.TrimSpace(session.Workspace); ws != "" {
		workspace = ws
	}

	exchanges := buildExchanges(session, workspace)
	if len(exchanges) == 0 {
		return nil, fmt.Errorf("session contains no conversational exchanges")
	}

	created := strings.TrimSpace(session.CreatedAt)
	updated := strings.TrimSpace(session.UpdatedAt)
	if updated == "" {
		updated = created
	}

	// Provider.Version must be non-empty for schema validation. Antigravity does
	// not stamp the model on every step, so we use the model derived from the
	// first turn's settings block and fall back to "unknown".
	version := strings.TrimSpace(session.Model)
	if version == "" {
		version = "unknown"
	}

	// Antigravity surfaces no per-step token usage in the transcript, so — like
	// DeepSeek TUI — we intentionally do not synthesize a schema.Usage value.

	data := &SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      providerSchemaID,
			Name:    providerName,
			Version: version,
		},
		SessionID:     session.ConversationID,
		CreatedAt:     created,
		UpdatedAt:     updated,
		Slug:          deriveSlug(session),
		WorkspaceRoot: workspace,
		Exchanges:     exchanges,
	}

	return data, nil
}

// buildExchanges groups transcript steps into exchanges. Steps are processed in
// file order (NOT by step_index — the index has a benign gap at 4). Each
// USER_INPUT starts a new exchange; PLANNER_RESPONSE adds an agent text message
// plus one message per tool call; a tool-result step attaches its output to the
// matching pending tool message.
func buildExchanges(session *agSession, workspaceRoot string) []Exchange {
	var exchanges []Exchange
	var current *Exchange

	flush := func() {
		if current != nil && len(current.Messages) > 0 {
			exchanges = append(exchanges, *current)
		}
	}

	for _, step := range session.Steps {
		switch {
		case step.Type == typeUserInput && step.Source == sourceUserExplicit:
			flush()
			current = &Exchange{}
			msg := convertUserStep(step)
			if len(msg.Content) > 0 {
				current.Messages = append(current.Messages, msg)
				current.StartTime = step.CreatedAt
				current.EndTime = step.CreatedAt
			}

		case step.Type == typePlannerResponse:
			if current == nil {
				current = &Exchange{}
			}
			current.Messages = append(current.Messages, convertPlannerStep(step, session.ConversationID, session.Model, workspaceRoot)...)
			current.EndTime = step.CreatedAt

		case isToolResultType(step.Type):
			// Tool results carry source MODEL and immediately follow their
			// PLANNER_RESPONSE; they are not new turns.
			if current != nil {
				attachToolResult(step, current, session.TaskOutputs)
				current.EndTime = step.CreatedAt
			}

		case step.Type == typeConversationHistory, step.Type == typeSystemMessage:
			// Replayed context / injected notices — not user-visible turn content.
			continue

		default:
			slog.Debug("antigravity: skipping unrecognized step",
				"conversationId", session.ConversationID, "type", step.Type, "stepIndex", step.StepIndex)
		}
	}

	flush()

	for i := range exchanges {
		exchanges[i].ExchangeID = fmt.Sprintf("%s:%d", session.ConversationID, i)
		if exchanges[i].StartTime == "" {
			exchanges[i].StartTime = session.CreatedAt
		}
		if exchanges[i].EndTime == "" {
			exchanges[i].EndTime = exchanges[i].StartTime
		}
	}

	return exchanges
}

// convertUserStep converts a USER_INPUT step to a schema user message, stripping
// the <USER_REQUEST> wrapper and metadata blocks. Returns a zero-value Message
// (empty Content) when nothing user-visible remains; callers drop it.
func convertUserStep(step transcriptStep) Message {
	text := cleanUserPrompt(step.Content)
	if text == "" {
		return Message{}
	}
	return Message{
		Timestamp: step.CreatedAt,
		Role:      schema.RoleUser,
		Content:   []ContentPart{{Type: schema.ContentTypeText, Text: text}},
	}
}

// convertPlannerStep converts a PLANNER_RESPONSE into an ordered set of messages:
// an optional agent message carrying the model's thinking + reply text, followed
// by one message per tool call.
func convertPlannerStep(step transcriptStep, conversationID, model, workspaceRoot string) []Message {
	var msgs []Message

	var parts []ContentPart
	if thinking := strings.TrimSpace(step.Thinking); thinking != "" {
		parts = append(parts, ContentPart{Type: schema.ContentTypeThinking, Text: thinking})
	}
	if text := strings.TrimSpace(step.Content); text != "" {
		parts = append(parts, ContentPart{Type: schema.ContentTypeText, Text: text})
	}
	if len(parts) > 0 {
		msgs = append(msgs, Message{
			Timestamp: step.CreatedAt,
			Role:      schema.RoleAgent,
			Model:     model,
			Content:   parts,
		})
	}

	for idx, tc := range step.ToolCalls {
		// Antigravity tool calls carry no explicit id, so synthesize a stable one
		// from the conversation + step + tool position.
		useID := fmt.Sprintf("%s:%d:%d", conversationID, step.StepIndex, idx)
		msgs = append(msgs, convertToolCallMessage(tc, useID, model, step.CreatedAt, workspaceRoot))
	}

	return msgs
}

// convertToolCallMessage builds an agent Message for a single tool call. The
// FormattedMarkdown is set from the input here; attachToolResult re-renders it
// once the matching result step lands.
func convertToolCallMessage(tc transcriptToolCall, useID, model, timestamp, workspaceRoot string) Message {
	name := tc.Name
	if name == "" {
		name = "unknown"
	}
	tool := &ToolInfo{
		Name:  name,
		Type:  classifyToolType(name),
		UseID: useID,
		Input: tc.Args,
	}
	if formatted := formatToolCall(tool); formatted != "" {
		tool.FormattedMarkdown = &formatted
	}
	return Message{
		Timestamp: timestamp,
		Role:      schema.RoleAgent,
		Model:     model,
		Tool:      tool,
		PathHints: extractPathHints(tc.Args, workspaceRoot),
	}
}

// attachToolResult routes a tool-result step's content to the oldest tool
// message in the current exchange that has not yet received output (FIFO).
// Correlation is positional because Antigravity transcripts carry no call→result
// id. After attaching, the tool's FormattedMarkdown is re-rendered to include
// the output.
func attachToolResult(step transcriptStep, current *Exchange, taskOutputs map[int]string) {
	content := cleanResultContent(step)
	if strings.EqualFold(step.Status, "RUNNING") {
		if taskOutput := strings.TrimSpace(taskOutputs[step.StepIndex]); taskOutput != "" {
			if content != "" {
				content += "\n\nTask output:\n" + taskOutput
			} else {
				content = taskOutput
			}
		}
	}
	for i := range current.Messages {
		tool := current.Messages[i].Tool
		if tool == nil || tool.Output != nil {
			continue
		}
		tool.Output = map[string]any{"content": content}
		if formatted := formatToolCall(tool); formatted != "" {
			tool.FormattedMarkdown = &formatted
		}
		return
	}
	slog.Debug("antigravity: tool result with no pending tool call", "type", step.Type, "stepIndex", step.StepIndex)
}

// cleanResultContent returns a tool result's content with the leading tab
// indentation that Antigravity inserts into RUN_COMMAND output blocks removed.
func cleanResultContent(step transcriptStep) string {
	content := strings.TrimSpace(step.Content)
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimLeft(line, "\t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// cleanUserPrompt extracts the real prompt from a USER_INPUT content blob.
func cleanUserPrompt(raw string) string {
	if m := userRequestRe.FindStringSubmatch(raw); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	// No wrapper (defensive): drop any metadata blocks and return the remainder.
	cleaned := metadataBlockRe.ReplaceAllString(raw, "")
	return strings.TrimSpace(cleaned)
}

// deriveSlug builds a filename-safe slug from the first user prompt.
func deriveSlug(session *agSession) string {
	if prompt := firstUserPromptText(session); prompt != "" {
		if slug := spi.GenerateFilenameFromUserMessage(prompt); slug != "" {
			return slug
		}
	}
	return fallbackSlug
}

// deriveModel extracts the model name from the first turn's settings-change
// block. Returns "" when no model is recorded (e.g. continuation-only captures).
func deriveModel(steps []transcriptStep) string {
	for _, step := range steps {
		if step.Type != typeUserInput {
			continue
		}
		if m := modelRe.FindStringSubmatch(step.Content); len(m) == 2 {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// resolveSessionWorkspace determines the workspace a conversation belongs to.
// history.jsonl is authoritative when it has an entry (interactive sessions);
// otherwise the workspace is inferred as the common ancestor of the absolute
// paths the tools touched. Returns "" when nothing is resolvable.
func resolveSessionWorkspace(conversationID string, steps []transcriptStep, history map[string]historyEntry) string {
	if entry, ok := history[conversationID]; ok {
		if ws := strings.TrimSpace(entry.Workspace); ws != "" {
			return ws
		}
	}
	return commonPathPrefix(collectWorkspacePaths(steps))
}

// sessionWorkspaceKnown reports whether a session has any signal of which
// project it belongs to. Text-only print-mode sessions have neither a history
// entry nor tool paths, so their workspace is unknowable; such "unscoped"
// sessions are surfaced only on explicit by-id retrieval, never in project-
// filtered bulk listings (which would otherwise pollute every project).
func sessionWorkspaceKnown(session *agSession) bool {
	return strings.TrimSpace(session.Workspace) != "" || len(collectToolPaths(session.Steps)) > 0
}

// sessionMatchesProject reports whether a session belongs to projectPath. An
// empty projectPath matches every session (no filtering). A session matches when
// its resolved workspace and the project are nested either way, or when any tool
// touched a path inside the project.
func sessionMatchesProject(session *agSession, projectPath string) bool {
	if strings.TrimSpace(projectPath) == "" {
		return true
	}
	if session.Workspace != "" {
		if pathWithin(session.Workspace, projectPath) || pathWithin(projectPath, session.Workspace) {
			return true
		}
	}
	for _, p := range collectToolPaths(session.Steps) {
		if pathWithin(p, projectPath) {
			return true
		}
	}
	return false
}

// collectToolPaths gathers the absolute filesystem paths referenced by every
// tool call in the transcript, normalizing file:// URIs to plain paths.
func collectToolPaths(steps []transcriptStep) []string {
	var paths []string
	seen := make(map[string]bool)
	for _, step := range steps {
		for _, tc := range step.ToolCalls {
			for _, key := range toolPathArgKeys {
				val, ok := tc.Args[key].(string)
				if !ok {
					continue
				}
				appendUniqueAbsPath(&paths, seen, val)
			}
		}
	}
	return paths
}

// collectWorkspacePaths gathers directory candidates for workspace inference.
// File-valued tool args are reduced to their parent directory; directory-valued
// args are used as-is.
func collectWorkspacePaths(steps []transcriptStep) []string {
	var paths []string
	seen := make(map[string]bool)
	add := func(value string, fileValue bool) {
		p := stripFileURI(strings.TrimSpace(value))
		if p == "" || !filepath.IsAbs(p) {
			return
		}
		if fileValue {
			p = filepath.Dir(p)
		}
		appendUniqueAbsPath(&paths, seen, p)
	}

	for _, step := range steps {
		for _, tc := range step.ToolCalls {
			for _, key := range workspaceDirArgKeys {
				if val, ok := tc.Args[key].(string); ok {
					add(val, false)
				}
			}
			for _, key := range workspaceFileArgKeys {
				if val, ok := tc.Args[key].(string); ok {
					add(val, true)
				}
			}
		}
	}
	return paths
}

func appendUniqueAbsPath(paths *[]string, seen map[string]bool, value string) {
	p := stripFileURI(strings.TrimSpace(value))
	if p == "" || !filepath.IsAbs(p) || seen[p] {
		return
	}
	seen[p] = true
	*paths = append(*paths, p)
}

// commonPathPrefix returns the deepest directory that is an ancestor of every
// given absolute path, or "" when the slice is empty or paths share no common
// root.
func commonPathPrefix(paths []string) string {
	if len(paths) == 0 {
		return ""
	}

	split := func(p string) []string {
		return strings.Split(filepath.Clean(p), string(filepath.Separator))
	}

	common := split(paths[0])
	for _, p := range paths[1:] {
		parts := split(p)
		n := len(common)
		if len(parts) < n {
			n = len(parts)
		}
		i := 0
		for i < n && common[i] == parts[i] {
			i++
		}
		common = common[:i]
	}

	if len(common) <= 1 {
		return ""
	}
	return strings.Join(common, string(filepath.Separator))
}

// stripFileURI removes a leading file:// scheme so URIs like
// file:///tmp/main.go become /tmp/main.go.
func stripFileURI(value string) string {
	return strings.TrimPrefix(value, "file://")
}

// msEpochToRFC3339 converts a millisecond epoch timestamp to an RFC3339 UTC
// string. Returns "" for non-positive input.
func msEpochToRFC3339(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// writeDebugRaw writes the raw transcript to the debug directory when --debug-raw
// is set. The unified session-data.json is written centrally by the CLI.
func writeDebugRaw(session *agSession) {
	if session == nil || session.RawData == "" {
		return
	}
	dir := spi.GetDebugDir(session.ConversationID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Debug("antigravity: unable to create debug dir", "error", err)
		return
	}
	rawPath := filepath.Join(dir, "raw-transcript.jsonl")
	if err := os.WriteFile(rawPath, []byte(session.RawData), 0o644); err != nil {
		slog.Debug("antigravity: failed to write debug raw file", "path", rawPath, "error", err)
		return
	}
	slog.Debug("antigravity: wrote debug raw file", "conversationId", session.ConversationID, "path", rawPath)
}
