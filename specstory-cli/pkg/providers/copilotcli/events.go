package copilotcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

const (
	providerID   = "copilot"
	providerName = "Copilot CLI"
	defaultSlug  = "copilot-session"

	// homeEnvVar relocates Copilot CLI's whole state directory, exactly as the
	// CLI itself honors it (see `copilot help environment`).
	homeEnvVar = "COPILOT_HOME"

	// Copilot CLI keeps one directory per session under session-state, each
	// holding an append-only events.jsonl transcript. The Chronicle database
	// (session-store.db) is not used: it is a search index that omits most
	// assistant replies and every tool call.
	sessionStateDirName = "session-state"
	eventsFileName      = "events.jsonl"

	// taskCompleteTool is how Copilot ends an autonomous turn; its summary
	// argument is the closing answer shown to the user.
	taskCompleteTool = "task_complete"
)

// Event types the transcript is built from. Everything else in events.jsonl
// (hooks, permissions, model call telemetry, context snapshots) is plumbing.
const (
	eventSessionStart  = "session.start"
	eventUserMessage   = "user.message"
	eventAssistantMsg  = "assistant.message"
	eventToolCompleted = "tool.execution_complete"
)

// rawEvent is the envelope every events.jsonl line shares. Data stays raw so
// only the event types the transcript needs pay for a full decode.
type rawEvent struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

type sessionStartData struct {
	SessionID      string `json:"sessionId"`
	CopilotVersion string `json:"copilotVersion"`
	StartTime      string `json:"startTime"`
	Context        struct {
		Cwd string `json:"cwd"`
	} `json:"context"`
}

type userMessageData struct {
	Content string `json:"content"`
	Source  string `json:"source"`
	// TransformedContent is the message as the model received it. A message
	// from another Copilot session is wrapped there in a
	// <cross_session_message> envelope; its content and source look exactly
	// like a typed prompt.
	TransformedContent string `json:"transformedContent"`
}

type toolRequest struct {
	ToolCallID       string          `json:"toolCallId"`
	Name             string          `json:"name"`
	Arguments        json.RawMessage `json:"arguments"`
	IntentionSummary string          `json:"intentionSummary"`
}

type assistantMessageData struct {
	Model            string        `json:"model"`
	Content          string        `json:"content"`
	ReasoningText    string        `json:"reasoningText"`
	ToolRequests     []toolRequest `json:"toolRequests"`
	OutputTokens     int           `json:"outputTokens"`
	ParentToolCallID string        `json:"parentToolCallId"`
}

type toolCompleteData struct {
	ToolCallID string `json:"toolCallId"`
	Success    bool   `json:"success"`
	Result     *struct {
		Content string `json:"content"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// sessionHeader is what the first record (session.start) says about a session:
// enough to list it and match it to a project without reading the transcript.
type sessionHeader struct {
	SessionID      string
	Cwd            string
	StartTime      string
	CopilotVersion string
}

// sessionFile is one session's transcript on disk.
type sessionFile struct {
	SessionID string
	Path      string
	Size      int64
	ModTime   time.Time
}

// errNotSession marks a file whose first record is not session.start, which
// callers skip rather than report as a failure.
var errNotSession = errors.New("not a Copilot CLI session transcript")

// copilotHome returns Copilot CLI's state directory, honoring COPILOT_HOME.
func copilotHome() (string, error) {
	if override := strings.TrimSpace(os.Getenv(homeEnvVar)); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, ".copilot"), nil
}

// sessionsRoot returns the directory holding one sub-directory per session.
func sessionsRoot() (string, error) {
	home, err := copilotHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, sessionStateDirName), nil
}

// isValidSessionID guards path construction: a session ID names a directory
// directly under the sessions root and must not be able to escape it.
func isValidSessionID(sessionID string) bool {
	return sessionID != "" && sessionID != "." && sessionID != ".." &&
		!strings.ContainsAny(sessionID, `/\`)
}

// listSessionFiles returns every session transcript, newest first. A missing
// root means Copilot CLI has not run yet: no sessions, not an error. The
// directory is read rather than globbed because the root comes from
// $COPILOT_HOME or $HOME, and glob syntax there ("C:\Users\Jane [Work]")
// would silently match nothing.
func listSessionFiles() ([]sessionFile, error) {
	root, err := sessionsRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list Copilot CLI sessions in %s: %w", root, err)
	}
	files := make([]sessionFile, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if file, ok := statSessionFile(filepath.Join(root, entry.Name(), eventsFileName)); ok {
			files = append(files, file)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime.After(files[j].ModTime) })
	return files, nil
}

// readEvents streams a transcript's records to visit until it returns false.
// visit receives each record decoded and as its raw line, so callers can keep
// the native record without a second read. Malformed or oversized records are
// skipped so one bad line cannot cost the user the rest of a session. live
// marks a read of a transcript Copilot may still be writing.
func readEvents(path string, live bool, visit func(event rawEvent, line []byte) bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			slog.Debug("Failed to close Copilot CLI transcript", "path", path, "error", closeErr)
		}
	}()

	reader := bufio.NewReader(file)
	for lineNumber := 1; ; lineNumber++ {
		line, oversized, readErr := spi.ReadRecordLine(reader, spi.MaxRecordLineSize)
		switch {
		case oversized:
			slog.Warn("Skipping oversized Copilot CLI record", "path", path, "line", lineNumber)
		case len(bytes.TrimSpace(line)) > 0:
			var event rawEvent
			if err := json.Unmarshal(line, &event); err != nil {
				// During a watch, a final line with no newline is a record
				// Copilot is still writing; the next read picks it up whole,
				// so it is not worth a warning. Anywhere else it was cut off.
				inFlight := live && errors.Is(readErr, io.EOF) && !bytes.HasSuffix(line, []byte("\n"))
				level := slog.LevelWarn
				if inFlight {
					level = slog.LevelDebug
				}
				slog.Log(context.Background(), level, "Skipping corrupted JSONL line",
					"path", path, "line", lineNumber, "inFlight", inFlight, "error", err)
			} else if !visit(event, line) {
				return nil
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("failed to read %s: %w", path, readErr)
		}
	}
}

// Record kinds Copilot CLI writes besides the conversation itself, as observed
// in sessions written by version 1.0.95. They are classified so a genuinely
// new kind is noticed.
var (
	// harnessKindPrefixes and harnessKinds are machinery around the
	// conversation, with nothing to render: hook runs, permission prompts,
	// per-model-call telemetry whose snapshots repeat the whole context, the
	// system prompt Copilot re-sends on every turn, and token counters.
	harnessKindPrefixes = []string{"hook.", "permission.", "model."}
	harnessKinds        = map[string]bool{
		"system.message":           true,
		"session.usage_checkpoint": true,
		"session.usage_record":     true,
	}

	// unrenderedKindPrefixes and unrenderedKinds have nothing to render
	// either: session lifecycle (start, resume, mode and model
	// changes, compaction, shutdown), sub-agent and skill bookkeeping whose
	// content arrives as messages, turn boundaries, tool start and external
	// tool records duplicated by the tool request and its completion, and
	// notices the user saw in the terminal rather than in the conversation.
	unrenderedKindPrefixes = []string{"session.", "subagent.", "skill.", "external_tool.", "assistant.turn_"}
	unrenderedKinds        = map[string]bool{
		"tool.execution_start": true,
		"system.notification":  true,
		"abort":                true,
	}
)

// isHarnessKind reports whether a record kind is harness machinery.
func isHarnessKind(kind string) bool {
	return harnessKinds[kind] || hasAnyPrefix(kind, harnessKindPrefixes)
}

// isKnownKind reports whether a record kind has been observed, rendered or not.
func isKnownKind(kind string) bool {
	switch kind {
	case eventSessionStart, eventUserMessage, eventAssistantMsg, eventToolCompleted:
		return true
	}
	return isHarnessKind(kind) || unrenderedKinds[kind] || hasAnyPrefix(kind, unrenderedKindPrefixes)
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// decodeHeader decodes a session.start record.
func decodeHeader(event rawEvent) (*sessionHeader, error) {
	if event.Type != eventSessionStart {
		return nil, errNotSession
	}
	var data sessionStartData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return nil, fmt.Errorf("malformed session.start record: %w", err)
	}
	return &sessionHeader{
		SessionID:      data.SessionID,
		Cwd:            data.Context.Cwd,
		StartTime:      data.StartTime,
		CopilotVersion: data.CopilotVersion,
	}, nil
}

// readSessionHeader reads only the first record, which keeps project matching
// cheap across hundreds of sessions. live is as for readEvents.
func readSessionHeader(path string, live bool) (*sessionHeader, error) {
	var header *sessionHeader
	headerErr := errNotSession
	err := readEvents(path, live, func(event rawEvent, _ []byte) bool {
		header, headerErr = decodeHeader(event)
		return false
	})
	if err != nil {
		return nil, err
	}
	return header, headerErr
}

// inputKind classifies a user.message.
type inputKind int

const (
	// machinery is recorded as a user message but is not conversation.
	machinery inputKind = iota
	// userPrompt is something the user typed.
	userPrompt
	// sessionMessage was sent by another Copilot session (its
	// send_session_message tool). It is real input: an orchestrator drives the
	// sessions it creates entirely through these, and a child reports back
	// through them.
	sessionMessage
	// subagentPrompt is the prompt the agent hands a sub-agent ("agent-<id>"
	// source, written into the parent's own transcript). It is the parent
	// agent talking, not the user.
	subagentPrompt
)

// crossSessionTag wraps a message from another session in the transformed
// content the model receives; content and source look exactly like a prompt.
const crossSessionTag = "<cross_session_message>"

// sidechainKey is the message metadata flag the markdown generator renders as
// "Agent - sidechain", which sets a sub-agent's work apart from the main
// conversation it is interleaved with.
const sidechainKey = "isSidechain"

// classifyUserMessage decides what a user.message is. Copilot records a lot of
// machinery as user messages: autopilot and system nudges, skill-context and
// instruction injections. Prompts handed to a sub-agent carry an "agent-<id>"
// source. Otherwise only an untagged or "user" source is real input, so this
// is an allowlist; a denylist would let each new kind of injection split the
// conversation. Among untagged messages, runtime notifications (a background
// agent finishing, say) are machinery and session messages are recognized by
// their envelope. The envelope is looked for outside the typed text, which
// transformedContent repeats word for word, so a prompt that merely quotes the
// tag cannot pass as a session message. The parentAgentTaskId field is no
// signal: real prompts carry it too.
func classifyUserMessage(data userMessageData) inputKind {
	content := strings.TrimSpace(data.Content)
	switch {
	case content == "":
		return machinery
	case strings.HasPrefix(content, "<system_notification>"):
		// Checked before the agent- source: Copilot also delivers runtime
		// notices (a child session archived, say) under an
		// "agent-workspace-<id>" source.
		return machinery
	case strings.HasPrefix(data.Source, "agent-"):
		return subagentPrompt
	case data.Source != "" && data.Source != "user":
		return machinery
	case strings.Contains(strings.Replace(data.TransformedContent, data.Content, "", 1), crossSessionTag):
		return sessionMessage
	default:
		return userPrompt
	}
}

// namesSession reports whether an input can name the session: the user's
// prompts and messages from another session can, sub-agent prompts and
// machinery cannot.
func namesSession(kind inputKind) bool {
	return kind == userPrompt || kind == sessionMessage
}

// sidechainMetadata flags a sub-agent's message for the markdown generator, or
// returns nil for the main conversation.
func sidechainMetadata(isSidechain bool) map[string]any {
	if !isSidechain {
		return nil
	}
	return map[string]any{sidechainKey: true}
}

// sessionMessageText labels a message from another session with its sender,
// so the transcript never presents an agent's report as something the user
// typed.
func sessionMessageText(data userMessageData) string {
	sender := "another Copilot session"
	envelope := ""
	if start := strings.Index(data.TransformedContent, crossSessionTag); start >= 0 {
		envelope = data.TransformedContent[start:]
	}
	for line := range strings.Lines(envelope) {
		if id, ok := strings.CutPrefix(strings.TrimSpace(line), "from_session_id:"); ok && strings.TrimSpace(id) != "" {
			sender = "Copilot session " + spi.InlineCode(strings.TrimSpace(id))
			break
		}
	}
	return fmt.Sprintf("_Message from %s:_\n\n%s", sender, strings.TrimSpace(data.Content))
}

// sessionSummary is a header plus the text that names the session.
type sessionSummary struct {
	Header *sessionHeader
	Name   string
}

// readSessionSummary reads up to the session's first input, which names it, so
// listing a session does not decode its whole transcript.
func readSessionSummary(path string) (*sessionSummary, error) {
	summary := &sessionSummary{}
	headerErr := errNotSession
	err := readEvents(path, false, func(event rawEvent, _ []byte) bool {
		if summary.Header == nil {
			summary.Header, headerErr = decodeHeader(event)
			return headerErr == nil
		}
		if event.Type != eventUserMessage {
			return true
		}
		var data userMessageData
		if json.Unmarshal(event.Data, &data) == nil && namesSession(classifyUserMessage(data)) {
			summary.Name = data.Content
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	if headerErr != nil {
		return nil, headerErr
	}
	return summary, nil
}

// metadata builds listing metadata, or nil for a session with no input yet.
// The slug is derived exactly as conversion derives it so listings name
// sessions the way their markdown files are named.
func (s *sessionSummary) metadata(sessionID string) *spi.SessionMetadata {
	if s.Name == "" {
		return nil
	}
	return &spi.SessionMetadata{
		SessionID: sessionID,
		CreatedAt: s.Header.StartTime,
		Slug:      slugFromPrompt(s.Name),
		Name:      spi.GenerateReadableName(s.Name),
	}
}

func slugFromPrompt(prompt string) string {
	if slug := spi.GenerateFilenameFromUserMessage(prompt); slug != "" {
		return slug
	}
	return defaultSlug
}

// convertSession reads a whole transcript into an AgentChatSession, or returns
// nil when the session has no input yet.
func convertSession(file sessionFile, header *sessionHeader, debugRaw, live bool) (*spi.AgentChatSession, error) {
	builder := newTranscriptBuilder(header.Cwd)
	var rawData strings.Builder
	var debugRecords []json.RawMessage
	unknownKinds := map[string]bool{}
	err := readEvents(file.Path, live, func(event rawEvent, line []byte) bool {
		if debugRaw {
			debugRecords = append(debugRecords, json.RawMessage(bytes.TrimSpace(line)))
		}
		rawData.Write(line)
		if !bytes.HasSuffix(line, []byte("\n")) {
			rawData.WriteByte('\n')
		}
		if !isKnownKind(event.Type) && !unknownKinds[event.Type] {
			unknownKinds[event.Type] = true
			slog.Debug("convertSession: Unknown Copilot CLI record kind", "sessionId", file.SessionID, "kind", event.Type)
		}
		builder.add(event)
		return true
	})
	if err != nil {
		return nil, err
	}
	if builder.name == "" {
		slog.Debug("convertSession: Copilot CLI session has no input yet", "sessionId", file.SessionID)
		return nil, nil
	}

	sessionID := header.SessionID
	if sessionID == "" {
		sessionID = file.SessionID
	}
	version := header.CopilotVersion
	if version == "" {
		version = "unknown"
	}
	createdAt := header.StartTime
	if createdAt == "" {
		createdAt = file.ModTime.UTC().Format(time.RFC3339)
	}

	sessionData := &schema.SessionData{
		SchemaVersion: schema.CurrentSchemaVersion,
		Provider:      schema.ProviderInfo{ID: providerID, Name: providerName, Version: version},
		SessionID:     sessionID,
		CreatedAt:     createdAt,
		UpdatedAt:     builder.lastTimestamp,
		Slug:          slugFromPrompt(builder.name),
		WorkspaceRoot: header.Cwd,
		Exchanges:     builder.finish(),
	}

	if debugRaw {
		debugDir := spi.GetDebugDir(sessionID)
		if err := spi.WriteDebugRecords(debugDir, debugRecords); err != nil {
			slog.Warn("convertSession: Failed to write debug files", "sessionId", sessionID, "path", debugDir, "error", err)
		}
	}

	return &spi.AgentChatSession{
		SessionID:   sessionID,
		CreatedAt:   createdAt,
		Slug:        sessionData.Slug,
		SessionData: sessionData,
		RawData:     rawData.String(),
	}, nil
}

// transcriptBuilder folds transcript events into exchanges: each input (a user
// prompt, or a message from another session) opens an exchange that collects
// the agent's replies and tool calls until the next input.
type transcriptBuilder struct {
	workspaceRoot string
	exchanges     []schema.Exchange
	current       *schema.Exchange
	// pendingTools pairs a tool call with its result, which arrives in a
	// later tool.execution_complete record.
	pendingTools map[string]*pendingTool
	// name is the session's first input, user prompt or session message
	// alike. It names the markdown file, so it must never change as the
	// append-only log grows: a later rename would leave the earlier save
	// behind as a stale duplicate.
	name          string
	lastTimestamp string
}

func newTranscriptBuilder(workspaceRoot string) *transcriptBuilder {
	return &transcriptBuilder{
		workspaceRoot: workspaceRoot,
		pendingTools:  make(map[string]*pendingTool),
	}
}

// add folds one event into the transcript and reports whether it contributed.
func (b *transcriptBuilder) add(event rawEvent) bool {
	var used bool
	var err error
	switch event.Type {
	case eventUserMessage:
		var data userMessageData
		if err = json.Unmarshal(event.Data, &data); err == nil {
			used = b.addUserMessage(event, data)
		}
	case eventAssistantMsg:
		var data assistantMessageData
		if err = json.Unmarshal(event.Data, &data); err == nil {
			used = b.addAssistantMessage(event, data)
		}
	case eventToolCompleted:
		var data toolCompleteData
		if err = json.Unmarshal(event.Data, &data); err == nil {
			used = b.addToolResult(data)
		}
	}
	if err != nil {
		slog.Warn("Skipping Copilot CLI record with malformed data", "id", event.ID, "kind", event.Type, "error", err)
	}
	if used && event.Timestamp != "" {
		b.lastTimestamp = event.Timestamp
		if b.current != nil {
			b.current.EndTime = event.Timestamp
		}
	}
	return used
}

func (b *transcriptBuilder) addUserMessage(event rawEvent, data userMessageData) bool {
	kind := classifyUserMessage(data)
	text := data.Content
	switch kind {
	case machinery:
		return false
	case subagentPrompt:
		// The parent agent's instructions to a sub-agent are part of the
		// current turn, so they join the current exchange as a sidechain
		// agent message rather than opening a new exchange as user input does.
		exchange := b.ensureExchange(event)
		exchange.Messages = append(exchange.Messages, schema.Message{
			ID:        event.ID,
			Timestamp: event.Timestamp,
			Role:      schema.RoleAgent,
			Content:   []schema.ContentPart{{Type: schema.ContentTypeText, Text: data.Content}},
			Metadata:  sidechainMetadata(true),
		})
		return true
	case sessionMessage:
		text = sessionMessageText(data)
	}
	b.flush()
	b.current = &schema.Exchange{ExchangeID: event.ID, StartTime: event.Timestamp}
	b.current.Messages = append(b.current.Messages, schema.Message{
		ID:        event.ID,
		Timestamp: event.Timestamp,
		Role:      schema.RoleUser,
		Content:   []schema.ContentPart{{Type: schema.ContentTypeText, Text: text}},
	})
	if b.name == "" {
		b.name = data.Content
	}
	return true
}

func (b *transcriptBuilder) addAssistantMessage(event rawEvent, data assistantMessageData) bool {
	// A sub-agent's turns are recorded inline, tagged with the tool call that
	// launched it. They are kept in log order, where they interleave with the
	// parent's and any parallel sub-agent's, and flagged as sidechain so they
	// stay distinguishable from the main conversation.
	metadata := sidechainMetadata(data.ParentToolCallID != "")

	var content []schema.ContentPart
	if text := strings.TrimSpace(data.ReasoningText); text != "" {
		content = append(content, schema.ContentPart{Type: schema.ContentTypeThinking, Text: text})
	}
	if text := strings.TrimSpace(data.Content); text != "" {
		content = append(content, schema.ContentPart{Type: schema.ContentTypeText, Text: text})
	}
	if len(content) == 0 && len(data.ToolRequests) == 0 {
		// Chunk placeholders carry no text of their own.
		return false
	}

	exchange := b.ensureExchange(event)
	if len(content) > 0 {
		message := schema.Message{
			ID:        event.ID,
			Timestamp: event.Timestamp,
			Role:      schema.RoleAgent,
			Model:     data.Model,
			Content:   content,
			Metadata:  metadata,
		}
		if data.OutputTokens > 0 {
			message.Usage = &schema.Usage{OutputTokens: data.OutputTokens}
		}
		exchange.Messages = append(exchange.Messages, message)
	}
	for _, request := range data.ToolRequests {
		arguments := decodeArguments(request.Arguments)
		// task_complete carries the agent's closing answer: Copilot shows its
		// summary to the user as the final message of the turn, so it reads as
		// agent text rather than as a tool block.
		if request.Name == taskCompleteTool {
			if summary := strings.TrimSpace(spi.StringValue(arguments, "summary")); summary != "" {
				exchange.Messages = append(exchange.Messages, schema.Message{
					ID:        request.ToolCallID,
					Timestamp: event.Timestamp,
					Role:      schema.RoleAgent,
					Model:     data.Model,
					Content:   []schema.ContentPart{{Type: schema.ContentTypeText, Text: summary}},
					Metadata:  metadata,
				})
				continue
			}
		}
		pending := newPendingTool(request, arguments)
		exchange.Messages = append(exchange.Messages, schema.Message{
			ID:        request.ToolCallID,
			Timestamp: event.Timestamp,
			Role:      schema.RoleAgent,
			Model:     data.Model,
			Tool:      pending.tool,
			PathHints: toolPathHints(pending.tool, b.workspaceRoot),
			Metadata:  metadata,
		})
		if request.ToolCallID != "" {
			b.pendingTools[request.ToolCallID] = pending
		}
	}
	return true
}

func (b *transcriptBuilder) addToolResult(data toolCompleteData) bool {
	// Tool call IDs are unique across the parent and its sub-agents, so one
	// lookup pairs results on either side of the sidechain.
	pending, ok := b.pendingTools[data.ToolCallID]
	if !ok {
		return false
	}
	delete(b.pendingTools, data.ToolCallID)

	switch {
	case !data.Success:
		message := "Tool call failed"
		if data.Error != nil && data.Error.Message != "" {
			message = data.Error.Message
		}
		pending.tool.Output = map[string]any{"result": message, "is_error": true}
	case data.Result != nil:
		pending.tool.Output = map[string]any{"result": data.Result.Content}
	}
	pending.render()
	return true
}

// ensureExchange returns the open exchange, opening one for agent output that
// arrives before any prompt (a resumed session whose prompt predates its log).
func (b *transcriptBuilder) ensureExchange(event rawEvent) *schema.Exchange {
	if b.current == nil {
		b.current = &schema.Exchange{ExchangeID: event.ID, StartTime: event.Timestamp}
	}
	return b.current
}

func (b *transcriptBuilder) flush() {
	if b.current != nil && len(b.current.Messages) > 0 {
		b.exchanges = append(b.exchanges, *b.current)
	}
	b.current = nil
}

func (b *transcriptBuilder) finish() []schema.Exchange {
	b.flush()
	return b.exchanges
}

// pendingTool is a tool call awaiting its result. intent is Copilot's one-line
// account of the call, kept so the markdown can be re-rendered once the
// result lands.
type pendingTool struct {
	tool   *schema.ToolInfo
	intent string
}

// newPendingTool converts a tool request. It is rendered up front so a call
// still reads well if the session ends before its result arrives.
func newPendingTool(request toolRequest, arguments map[string]any) *pendingTool {
	// The summary matches the markdown generator's default, so archives keep
	// the familiar <summary> line; it is set explicitly because cross-agent
	// resume flattens tool calls from Summary/FormattedMarkdown only.
	summary := fmt.Sprintf("Tool use: **%s**", request.Name)
	pending := &pendingTool{
		tool: &schema.ToolInfo{
			Name:    request.Name,
			Type:    toolType(request.Name),
			UseID:   request.ToolCallID,
			Input:   arguments,
			Summary: &summary,
		},
		intent: strings.Join(strings.Fields(request.IntentionSummary), " "),
	}
	pending.render()
	return pending
}

// render pre-renders the tool body: Copilot's one-line intent as prose, then a
// shell command in a bash fence or the arguments as an Input list, then the
// capped, fenced result. A failed call's error text is its result.
func (p *pendingTool) render() {
	result, _ := p.tool.Output["result"].(string)
	var formatted string
	if command := spi.StringValue(p.tool.Input, "command"); p.tool.Type == schema.ToolTypeShell && command != "" {
		formatted = spi.RenderShellCall(p.intent, command, result)
	} else {
		formatted = spi.RenderToolInputList(p.tool.Input) + spi.RenderToolResult(result)
		if p.intent != "" {
			formatted = "\n" + p.intent + "\n" + formatted
		}
	}
	p.tool.FormattedMarkdown = nil
	if formatted != "" {
		p.tool.FormattedMarkdown = &formatted
	}
}

// decodeArguments returns tool arguments as a map. A non-object payload (the
// patch text apply_patch takes) is kept under "input", the name the patch
// tool's own schema gives that argument.
func decodeArguments(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var arguments map[string]any
	if err := json.Unmarshal(raw, &arguments); err == nil {
		return arguments
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{"input": string(raw)}
	}
	return map[string]any{"input": value}
}

// toolType classifies a tool from Copilot's inventory (testdata/tools.txt) by
// what it acts on: workspace files are read, write or search; shell commands
// are shell; the todo list is task; and agent state (sub-agents, sessions,
// skills, the session database, canvases, workflows, pull requests, the
// editor host's browser) is generic. A remote read, such as a URL fetch or a
// GitHub file, is read; a remote query is search. A tool outside the inventory
// (a user-installed MCP server's, or one added in a later version) still
// renders, typed unknown.
func toolType(name string) string {
	if toolType, ok := toolTypes[name]; ok {
		return toolType
	}
	return schema.ToolTypeUnknown
}

var toolTypes = map[string]string{
	"bash":      schema.ToolTypeShell,
	"read_bash": schema.ToolTypeShell,
	"stop_bash": schema.ToolTypeShell,
	"list_bash": schema.ToolTypeShell,

	"view":                                schema.ToolTypeRead,
	"web_fetch":                           schema.ToolTypeRead,
	"fetch_copilot_cli_documentation":     schema.ToolTypeRead,
	"github-mcp-server-get_file_contents": schema.ToolTypeRead,
	"github-mcp-server-get_commit":        schema.ToolTypeRead,
	"github-mcp-server-issue_read":        schema.ToolTypeRead,
	"github-mcp-server-pull_request_read": schema.ToolTypeRead,
	"readPage":                            schema.ToolTypeRead,

	"create":      schema.ToolTypeWrite,
	"edit":        schema.ToolTypeWrite,
	"apply_patch": schema.ToolTypeWrite,

	"rg":                                     schema.ToolTypeSearch,
	"grep":                                   schema.ToolTypeSearch,
	"glob":                                   schema.ToolTypeSearch,
	"javaFindSymbol":                         schema.ToolTypeSearch,
	"usages":                                 schema.ToolTypeSearch,
	"github-mcp-server-search_code":          schema.ToolTypeSearch,
	"github-mcp-server-search_issues":        schema.ToolTypeSearch,
	"github-mcp-server-search_pull_requests": schema.ToolTypeSearch,
	"github-mcp-server-search_repositories":  schema.ToolTypeSearch,
	"github-mcp-server-search_users":         schema.ToolTypeSearch,
	"github-mcp-server-list_commits":         schema.ToolTypeSearch,
	"github-mcp-server-list_issues":          schema.ToolTypeSearch,

	"update_todo": schema.ToolTypeTask,

	"task":                                  schema.ToolTypeGeneric,
	"read_agent":                            schema.ToolTypeGeneric,
	"write_agent":                           schema.ToolTypeGeneric,
	"list_agents":                           schema.ToolTypeGeneric,
	"skill":                                 schema.ToolTypeGeneric,
	"sql":                                   schema.ToolTypeGeneric,
	"session_store_sql":                     schema.ToolTypeGeneric,
	"run_dynamic_workflow":                  schema.ToolTypeGeneric,
	"dynamic_workflows_manage":              schema.ToolTypeGeneric,
	"list_workflows":                        schema.ToolTypeGeneric,
	"save_workflow":                         schema.ToolTypeGeneric,
	"save_session_automation":               schema.ToolTypeGeneric,
	"task_complete":                         schema.ToolTypeGeneric,
	"ask_user":                              schema.ToolTypeGeneric,
	"exit_plan_mode":                        schema.ToolTypeGeneric,
	"tool_search_tool":                      schema.ToolTypeGeneric,
	"extensions_reload":                     schema.ToolTypeGeneric,
	"github-mcp-server-get_copilot_space":   schema.ToolTypeGeneric,
	"github-mcp-server-list_copilot_spaces": schema.ToolTypeGeneric,
	"create_session":                        schema.ToolTypeGeneric,
	"get_session":                           schema.ToolTypeGeneric,
	"fork_session":                          schema.ToolTypeGeneric,
	"rename_session":                        schema.ToolTypeGeneric,
	"archive_session":                       schema.ToolTypeGeneric,
	"send_session_message":                  schema.ToolTypeGeneric,
	"send_message":                          schema.ToolTypeGeneric,
	"list_sessions":                         schema.ToolTypeGeneric,
	"list_sessions_and_chats":               schema.ToolTypeGeneric,
	"get_current_session":                   schema.ToolTypeGeneric,
	"get_session_context":                   schema.ToolTypeGeneric,
	"create_project":                        schema.ToolTypeGeneric,
	"list_projects":                         schema.ToolTypeGeneric,
	"open_canvas":                           schema.ToolTypeGeneric,
	"invoke_canvas_action":                  schema.ToolTypeGeneric,
	"list_canvas_capabilities":              schema.ToolTypeGeneric,
	"add_artifact":                          schema.ToolTypeGeneric,
	"add_artifact_or_reference":             schema.ToolTypeGeneric,
	"remove_artifact_or_reference":          schema.ToolTypeGeneric,
	"list_artifacts":                        schema.ToolTypeGeneric,
	"list_artifacts_and_references":         schema.ToolTypeGeneric,
	"delete_item":                           schema.ToolTypeGeneric,
	"create_pull_request":                   schema.ToolTypeGeneric,
	"update_pull_request":                   schema.ToolTypeGeneric,
	"open_pr_session":                       schema.ToolTypeGeneric,
	"reply_and_resolve_review_thread":       schema.ToolTypeGeneric,
	"get_changes_overview":                  schema.ToolTypeGeneric,
	"rename_branch":                         schema.ToolTypeGeneric,
	"addComment":                            schema.ToolTypeGeneric,
	"listComments":                          schema.ToolTypeGeneric,
	"navigate_to":                           schema.ToolTypeGeneric,
	"openBrowserPage":                       schema.ToolTypeGeneric,
	"navigatePage":                          schema.ToolTypeGeneric,
	"clickElement":                          schema.ToolTypeGeneric,
	"screenshotPage":                        schema.ToolTypeGeneric,
	"runPlaywrightCode":                     schema.ToolTypeGeneric,
	"runTests":                              schema.ToolTypeGeneric,
}

// toolPathHints names the files a tool touched so the markdown can link them.
func toolPathHints(tool *schema.ToolInfo, workspaceRoot string) []string {
	if command := spi.StringValue(tool.Input, "command"); tool.Type == schema.ToolTypeShell && command != "" {
		return spi.ExtractShellPathHints(command, workspaceRoot, workspaceRoot)
	}
	if path := spi.StringValue(tool.Input, "path", "filePath"); path != "" {
		return []string{spi.NormalizePath(path, workspaceRoot)}
	}
	return nil
}
