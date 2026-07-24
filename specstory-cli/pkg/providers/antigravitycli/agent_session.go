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
	metadataBlockRe = regexp.MustCompile(`(?s)<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>|<USER_SETTINGS_CHANGE>.*?</USER_SETTINGS_CHANGE>|<SYSTEM_MESSAGE>.*?</SYSTEM_MESSAGE>`)
	// modelRe pulls the model name out of the first turn's <USER_SETTINGS_CHANGE>
	// block, e.g. "...changed setting `Model Selection` from None to Gemini 3.5
	// Flash (High). ..." → "Gemini 3.5 Flash (High)". The terminator is a period
	// followed by whitespace or end-of-string so the decimal point in a version
	// like "3.5" does not cut the match short.
	modelRe = regexp.MustCompile(`Model Selection.{0,4}from .+? to (.+?)\.(?:\s|$)`)
)

// toolPathArgKeys are the Antigravity tool-arg keys that carry absolute
// filesystem paths. They are the only on-disk signal of which project a
// print-mode session belongs to, since the transcript has no workspace field
// (see the format spec, §5). This is the canonical list — markdown_tools.go
// builds its path-hint field list from it rather than repeating the keys.
var toolPathArgKeys = []string{"Cwd", "AbsolutePath", "TargetFile", "SearchPath", "DirectoryPath"}

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
	// Prefer the session's own resolved workspace, then the project the CLI is
	// working from. Both can be empty at once — an unscoped text-only session
	// loaded by id with no project context (its reindex ref carries an empty
	// OriginCwd) — so last, fall back to the CLI's own cwd: the schema requires
	// a non-empty WorkspaceRoot, and for a session whose workspace is
	// unknowable the directory the CLI is running from is the only one there
	// is. Project attribution is unaffected — it derives from the ref, not
	// from this field.
	workspace := strings.TrimSpace(workspaceRoot)
	if ws := strings.TrimSpace(session.Workspace); ws != "" {
		workspace = ws
	}
	if workspace == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspace = cwd
		}
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

	// Antigravity surfaces no per-step token usage in the transcript, so — like
	// DeepSeek TUI — we intentionally do not synthesize a schema.Usage value.

	data := &SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:   providerSchemaID,
			Name: providerName,
			// Version is the agent CLI's version and must be non-empty to
			// validate. Antigravity records no CLI version in any session data
			// (it is only obtainable live from `agy --version`), hence the
			// constant. The model is not a substitute — it is carried on each
			// agent message's Model field instead.
			Version: "unknown",
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

// buildExchanges groups transcript steps into exchanges. Steps arrive already
// ordered by step_index (parseTranscript sorts them, because agy flushes async
// results to the file out of order) and are processed in that order. Each
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

		case isToolResultStep(step):
			// Tool results carry source MODEL and follow their PLANNER_RESPONSE;
			// they are not new turns.
			if current != nil {
				attachToolResult(step, current, session.TaskOutputs)
				current.EndTime = step.CreatedAt
			}

		case step.Type == typeConversationHistory, step.Type == typeSystemMessage, step.Type == typeCheckpoint:
			// Replayed context / injected notices / truncation markers — not
			// user-visible turn content.
			continue

		case step.Type == typeUserInput:
			// A USER_INPUT the first case rejected: the source is not
			// USER_EXPLICIT, so it is replayed or synthesized input rather than
			// something the user typed this turn.
			slog.Debug("antigravity: skipping non-explicit user input",
				"conversationId", session.ConversationID, "source", step.Source, "stepIndex", step.StepIndex)

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

// attachToolResult routes a tool-result step's content to a pending tool message
// in the current exchange (one that has not yet received output). Antigravity
// transcripts carry no call→result id, so correlation is by tool type first
// (route the result to a pending call whose type matches the result's category),
// falling back to the oldest pending call (positional/FIFO). The type check keeps
// results correctly paired when several calls are in flight and complete out of
// order. After attaching, the tool's FormattedMarkdown is re-rendered to include
// the output.
func attachToolResult(step transcriptStep, current *Exchange, taskOutputs map[int]string) {
	content := cleanResultContent(step)
	if strings.EqualFold(step.Status, statusRunning) {
		if taskOutput := strings.TrimSpace(taskOutputs[step.StepIndex]); taskOutput != "" {
			if content != "" {
				content += "\n\nTask output:\n" + taskOutput
			} else {
				content = taskOutput
			}
		}
	}
	tool := pickPendingTool(current, resultStepToolType(step.Type))
	if tool == nil {
		slog.Debug("antigravity: tool result with no pending tool call", "type", step.Type, "stepIndex", step.StepIndex)
		return
	}
	tool.Output = map[string]any{"content": content}
	if formatted := formatToolCall(tool); formatted != "" {
		tool.FormattedMarkdown = &formatted
	}
}

// pickPendingTool selects the tool message a result should attach to: the oldest
// pending (output-less) tool whose Type equals wantType, or — when wantType is
// empty (ambiguous result category) or none matches — the oldest pending tool of
// any type. Returns nil when no tool is awaiting output.
func pickPendingTool(current *Exchange, wantType string) *ToolInfo {
	var fallback *ToolInfo
	for i := range current.Messages {
		tool := current.Messages[i].Tool
		if tool == nil || tool.Output != nil {
			continue
		}
		if fallback == nil {
			fallback = tool
		}
		if wantType != "" && tool.Type == wantType {
			return tool
		}
	}
	return fallback
}

// resultStepToolType maps an Antigravity result step type to the SpecStory tool
// type (schema.ToolType*) the originating call carries on ToolInfo.Type, so a
// result can be routed to the matching pending call. Each mapping mirrors what
// classifyToolType assigns the originating tool — the two must agree or routing
// misses. GENERIC and any unlisted future type return "", which tells
// pickPendingTool to fall back to positional (FIFO) attachment. The granularity
// is the tool type, not the tool: e.g. grep_search and search_web both map to
// "search" and can still cross-pair with each other, but a typed result can
// never land on a pending call of a different type.
func resultStepToolType(resultType string) string {
	switch resultType {
	case typeRunCommand:
		return schema.ToolTypeShell
	case typeViewFile, typeListDirectory:
		return schema.ToolTypeRead
	case typeCodeAction:
		return schema.ToolTypeWrite
	case typeGrepSearch, typeSearchWeb, typeReadURLContent:
		return schema.ToolTypeSearch
	case typeGenerateImage, typeInvokeSubagent, typeAskQuestion:
		return schema.ToolTypeGeneric
	default:
		return ""
	}
}

// resultBoilerplate are framework filler phrases agy appends to tool results.
// They are instructions to the model, not information for a human reader, so they
// are stripped from rendered output. Matched as substrings because agy sometimes
// appends them inline to a content line (e.g. right after "The following changes
// were made … to: <path>.").
var resultBoilerplate = []string{
	"If relevant, proactively run terminal commands to execute this code for the USER. Don't ask for permission.",
	"Please note that the above snippet only shows the MODIFIED lines from the last change. It shows up to 3 lines of unchanged lines before and after the modified lines. The actual file contents may have many more lines not shown.",
	"Do not output the path of this image to show to the user since the user can already see it. However, you can embed this image in artifacts for the USER's review.",
	"The subagents will send you a message when they have completed their task or require guidance. There is no need to poll for their responses.",
	"You can use the view_file tool to read specific sections if needed.",
}

// cleanResultContent normalizes a tool result's content for display: it strips
// the per-result "Created At:"/"Completed At:" timing header (redundant with the
// turn timestamps) and the framework boilerplate above, then collapses the blank
// lines those removals leave behind. Tab de-indentation is confined to
// RUN_COMMAND results, and even there only the block's shared indent is removed:
// agy uniformly tab-indents the whole output block (format spec §3.9), but any
// deeper tabs are the command's real output (e.g. a printed Makefile rule) and
// must survive. Other result types are not indented by agy at all, so their
// leading tabs are content and pass through untouched.
func cleanResultContent(step transcriptStep) string {
	// Only whole-string trimming happens at the end: trimming up front would eat
	// the first line's indentation and skew the shared-indent computation below.
	content := step.Content
	if strings.TrimSpace(content) == "" {
		return ""
	}
	for _, phrase := range resultBoilerplate {
		content = strings.ReplaceAll(content, phrase, "")
	}
	kept := make([]string, 0)
	for _, line := range strings.Split(content, "\n") {
		deindented := strings.TrimLeft(line, "\t")
		if strings.HasPrefix(deindented, "Created At:") || strings.HasPrefix(deindented, "Completed At:") {
			continue
		}
		kept = append(kept, line)
	}
	if step.Type == typeRunCommand {
		kept = stripSharedTabIndent(kept)
	}
	return collapseBlankLines(strings.TrimSpace(strings.Join(kept, "\n")))
}

// stripSharedTabIndent removes the leading-tab indentation common to every
// non-blank line, so a uniformly indented block loses only the indent agy added
// while tabs that are part of the text itself remain. Whitespace-only lines
// neither constrain the shared indent nor keep their whitespace.
func stripSharedTabIndent(lines []string) []string {
	shared := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		tabs := len(line) - len(strings.TrimLeft(line, "\t"))
		if shared == -1 || tabs < shared {
			shared = tabs
		}
	}
	if shared <= 0 {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue // leave as "" — blank lines carry no indent worth keeping
		}
		out[i] = line[shared:]
	}
	return out
}

// collapseBlankLines reduces any run of blank lines to a single blank line, so
// content stays readable after header/boilerplate removal opens gaps.
func collapseBlankLines(s string) string {
	var b strings.Builder
	blank := false
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
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

// resolveSessionWorkspace determines the workspace a conversation belongs to,
// using only sources that state it outright: history.jsonl (interactive
// sessions), then the conversationId -> projectId -> workspace join recovered
// from retained CLI logs. Returns "" when neither knows.
//
// A workspace is deliberately NOT inferred from the paths the session's tools
// touched. The common ancestor of those paths collapses to something far above
// the project as soon as one path falls outside it (reading ~/.gitconfig is
// enough to yield $HOME), and sessionMatchesProject treats a workspace that
// contains the project as a match — so an inferred ancestor would attach the
// session to every unrelated project beneath it. Sessions with no stated
// workspace are still matched to their project by the tool-path scan in
// sessionMatchesProject, which requires a path actually inside that project.
func resolveSessionWorkspace(conversationID string, history map[string]historyEntry, projectWorkspaces map[string]string) string {
	if entry, ok := history[conversationID]; ok {
		if ws := strings.TrimSpace(entry.Workspace); ws != "" {
			return ws
		}
	}
	return strings.TrimSpace(projectWorkspaces[conversationID])
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
//
// Matching a workspace that merely contains the project is safe because
// resolveSessionWorkspace only returns workspaces Antigravity stated itself —
// a monorepo root recorded in history.jsonl should match a sync run from one of
// its subdirectories.
func sessionMatchesProject(session *agSession, projectPath string) bool {
	if strings.TrimSpace(projectPath) == "" {
		return true
	}
	if workspacesOverlap(session.Workspace, projectPath) {
		return true
	}
	for _, p := range collectToolPaths(session.Steps) {
		if pathWithin(p, projectPath) {
			return true
		}
	}
	return false
}

// workspacesOverlap reports whether a stated workspace and a project directory
// are the same or nested either way. Both directions count: a session recorded
// against a subdirectory belongs to a sync run from the repo root, and one
// recorded against a monorepo root belongs to a sync run from a package inside
// it. An empty workspace never overlaps.
func workspacesOverlap(workspace, projectPath string) bool {
	if strings.TrimSpace(workspace) == "" {
		return false
	}
	return pathWithin(workspace, projectPath) || pathWithin(projectPath, workspace)
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

func appendUniqueAbsPath(paths *[]string, seen map[string]bool, value string) {
	p := stripFileURI(strings.TrimSpace(value))
	if p == "" || !filepath.IsAbs(p) || seen[p] {
		return
	}
	seen[p] = true
	*paths = append(*paths, p)
}

// stripFileURI removes a leading file:// scheme and decodes escaped path bytes
// so URIs like file:///tmp/my%20file.go become /tmp/my file.go. Non-URI values
// pass through unchanged. Conversion is delegated to spi.FileURIToPath so all
// providers translate drive-letter, UNC, and WSL URI forms identically.
func stripFileURI(value string) string {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "file:") {
		return trimmed
	}
	if path, err := spi.FileURIToPath(trimmed); err == nil {
		return path
	}
	return strings.TrimPrefix(trimmed, "file://")
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
