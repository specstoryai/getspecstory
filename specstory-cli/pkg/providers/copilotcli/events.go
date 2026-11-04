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
	"sync"
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

	// applyPatchTool edits files from raw patch text rather than a path
	// argument, so the files it wrote are read from the patch's headers.
	applyPatchTool = "apply_patch"

	// editTool and createTool carry everything needed to show the change in
	// their arguments, as observed in sessions written by version 1.0.105.
	editTool   = "edit"
	createTool = "create"

	// viewTool returns a file's own bytes, which must not be reformatted.
	viewTool = "view"

	// powerShellTool is the tool that replaces bash on Windows, so its command
	// is fenced as PowerShell.
	powerShellTool = "powershell"

	// exitCodeKey holds a shell command's non-zero exit code in a tool's
	// output, beside its result text.
	exitCodeKey = "exit_code"
)

// Event types the transcript is built from. Everything else in events.jsonl
// (hooks, permissions, model call telemetry, context snapshots) is plumbing.
const (
	eventSessionStart      = "session.start"
	eventUserMessage       = "user.message"
	eventAssistantMsg      = "assistant.message"
	eventToolCompleted     = "tool.execution_complete"
	eventToolUserRequested = "tool.user_requested"
	eventUsageRecord       = "session.usage_record"
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
	// APICallID names the model call that produced this message, which is how
	// its session.usage_record is found.
	APICallID string `json:"apiCallId"`
}

// usageRecordData is one model call's token counts. Only some models write
// outputTokens on the assistant message; this record covers them all.
type usageRecordData struct {
	Usage struct {
		APICallID        string `json:"apiCallId"`
		InputTokens      int    `json:"inputTokens"`
		OutputTokens     int    `json:"outputTokens"`
		CacheReadTokens  int    `json:"cacheReadTokens"`
		CacheWriteTokens int    `json:"cacheWriteTokens"`
		ReasoningTokens  int    `json:"reasoningTokens"`
	} `json:"usage"`
}

// userToolRequestData is a tool call the user started themselves, such as a
// `!command`, rather than one the agent asked for.
type userToolRequestData struct {
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Arguments  json.RawMessage `json:"arguments"`
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
	// ShellExecution is present on shell tool results. A command that exits
	// non-zero is still recorded as a successful call, so its exit code is the
	// only structured sign that it failed.
	ShellExecution *struct {
		ExitCode *int `json:"exitCode"`
	} `json:"shellExecution"`
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

// copilotHome returns Copilot CLI's state directory, honoring COPILOT_HOME. A
// relative override is made absolute here because transcript paths are built
// from it and persisted by reindex, where they must outlive this directory.
func copilotHome() (string, error) {
	if override := strings.TrimSpace(os.Getenv(homeEnvVar)); override != "" {
		absolute, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("failed to resolve %s: %w", homeEnvVar, err)
		}
		return absolute, nil
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
	// Stat first: Windows reports ReadDir of a regular file as "path not found",
	// which would pass off an invalid store as one that does not exist yet.
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err == nil && !info.IsDir() {
		err = fmt.Errorf("%s is not a directory", root)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list Copilot CLI sessions in %s: %w", root, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("failed to list Copilot CLI sessions in %s: %w", root, err)
	}
	files := make([]sessionFile, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		file, ok, err := statSessionFile(filepath.Join(root, entry.Name(), eventsFileName))
		if err != nil {
			slog.Debug("Skipping unreadable Copilot CLI session", "path", entry.Name(), "error", err)
			continue
		}
		if ok {
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
// in sessions written through version 1.0.105. They are classified so a genuinely
// new kind is noticed.
var (
	// harnessKindPrefixes and harnessKinds are machinery around the
	// conversation, with nothing to render: hook runs, permission prompts,
	// per-model-call telemetry whose snapshots repeat the whole context, the
	// system prompt Copilot re-sends on every turn, and token counters, whose
	// per-call counts are read into Usage but never rendered.
	harnessKindPrefixes = []string{"hook.", "permission.", "model."}
	harnessKinds        = map[string]bool{
		"system.message":           true,
		"session.usage_checkpoint": true,
		eventUsageRecord:           true,
	}

	// unrenderedKindPrefixes and unrenderedKinds have nothing to render
	// either: session lifecycle (start, resume, mode and model
	// changes, compaction, shutdown), sub-agent and skill bookkeeping whose
	// content arrives as messages, turn boundaries, tool start and external
	// tool records duplicated by the tool request and its completion, a typed
	// answer that the question's own tool result repeats, and notices the user
	// saw in the terminal rather than in the conversation.
	unrenderedKindPrefixes = []string{"session.", "subagent.", "skill.", "external_tool.", "assistant.turn_"}
	unrenderedKinds        = map[string]bool{
		"tool.execution_start":    true,
		"human_response.recorded": true,
		"system.notification":     true,
		"abort":                   true,
	}
)

// isPrivateKind reports whether a record kind is left out of RawData, which
// SpecStory Cloud stores: the system prompt (the user's custom instructions
// included) and the model-call records, which carry account quota and hidden
// side calls and only repeat the conversation otherwise. Nothing rendered
// comes from them, and --debug-raw still exports them.
func isPrivateKind(kind string) bool {
	return kind == "system.message" || strings.HasPrefix(kind, "model.")
}

// isHarnessKind reports whether a record kind is harness machinery.
func isHarnessKind(kind string) bool {
	return harnessKinds[kind] || hasAnyPrefix(kind, harnessKindPrefixes)
}

// isKnownKind reports whether a record kind has been observed, rendered or not.
func isKnownKind(kind string) bool {
	switch kind {
	case eventSessionStart, eventUserMessage, eventAssistantMsg, eventToolCompleted, eventToolUserRequested:
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

// Runtime notifications arrive as a user message wholly wrapped in these tags.
const (
	notificationOpenTag  = "<system_notification>"
	notificationCloseTag = "</system_notification>"
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
// signal: real prompts carry it too. A typed prompt has no source to tell it
// from a notification, so a notification must be wrapped in its tags end to
// end: a prompt that only starts with the opening tag is kept.
func classifyUserMessage(data userMessageData) inputKind {
	content := strings.TrimSpace(data.Content)
	switch {
	case content == "":
		return machinery
	case strings.HasPrefix(content, notificationOpenTag) && strings.HasSuffix(content, notificationCloseTag):
		// Checked before the agent- source: Copilot also delivers runtime
		// notices (a child session archived, say) under an
		// "agent-workspace-<id>" source.
		return machinery
	case strings.HasPrefix(data.Source, "agent-"):
		return subagentPrompt
	case data.Source != "" && data.Source != "user":
		logMachinerySource(data.Source)
		return machinery
	case strings.Contains(strings.Replace(data.TransformedContent, data.Content, "", 1), crossSessionTag):
		return sessionMessage
	default:
		return userPrompt
	}
}

// Sources Copilot uses for injected input, as observed in sessions written by
// version 1.0.95, besides the skill-<name> family.
var knownMachinerySources = map[string]bool{
	"autopilot":             true,
	"system":                true,
	"instruction-discovery": true,
}

// warnedSources remembers the unfamiliar sources already warned about. It lasts
// for the process because a watch re-reads a session on every change, which
// would otherwise repeat the warning for as long as the session runs.
var warnedSources sync.Map

// logMachinerySource records input left out because of its source. An unfamiliar
// source is worth a warning: the allowlist drops it, so if Copilot starts tagging
// typed prompts with a new source, whole sessions would go missing and this
// would be the only sign.
func logMachinerySource(source string) {
	if knownMachinerySources[source] || strings.HasPrefix(source, "skill-") {
		slog.Debug("Not rendering Copilot CLI input from a non-user source", "source", source)
		return
	}
	if _, warned := warnedSources.LoadOrStore(source, true); !warned {
		slog.Warn("Not rendering Copilot CLI input from an unfamiliar source", "source", source)
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
//
// fallbackRoot is the caller's project path, used as the workspace root for a
// session that recorded no working directory.
func convertSession(file sessionFile, header *sessionHeader, fallbackRoot string, debugRaw, live bool) (*spi.AgentChatSession, error) {
	workspaceRoot := resolveWorkspaceRoot(header.Cwd, fallbackRoot)
	builder := newTranscriptBuilder(workspaceRoot)
	var rawData strings.Builder
	var debugRecords []json.RawMessage
	unknownKinds := map[string]bool{}
	err := readEvents(file.Path, live, func(event rawEvent, line []byte) bool {
		if debugRaw {
			debugRecords = append(debugRecords, json.RawMessage(bytes.TrimSpace(line)))
		}
		if !isPrivateKind(event.Type) {
			rawData.Write(line)
			if !bytes.HasSuffix(line, []byte("\n")) {
				rawData.WriteByte('\n')
			}
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

	// The directory is the session's identity: it is what listings show and
	// what `copilot --resume` opens, and a transcript copied into another
	// session's directory keeps its old header ID.
	sessionID := file.SessionID
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
		WorkspaceRoot: workspaceRoot,
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

// resolveWorkspaceRoot picks the root paths are normalized against: the recorded
// directory, then the caller's project path, then the process working directory
// as a last resort, because session data must never have an empty root. The
// fallbacks only serve rendering. They say nothing about which project owns the
// session, which is why ownership and OriginCwd read the recorded value alone.
func resolveWorkspaceRoot(recorded, fallbackRoot string) string {
	if recorded != "" {
		return recorded
	}
	if fallbackRoot != "" {
		return fallbackRoot
	}
	cwd, err := os.Getwd()
	if err != nil {
		slog.Warn("Copilot CLI session has no workspace root", "error", err)
	}
	return cwd
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
	// userShells pairs a command the user ran with its result in the same way.
	userShells map[string]*userShell
	// callUsage holds a model call's token counts until its message arrives;
	// Copilot writes the usage record first.
	callUsage map[string]*schema.Usage
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
		userShells:    make(map[string]*userShell),
		callUsage:     make(map[string]*schema.Usage),
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
			used = b.addToolResult(event, data)
		}
	case eventToolUserRequested:
		var data userToolRequestData
		if err = json.Unmarshal(event.Data, &data); err == nil {
			used = b.addUserToolRequest(event, data)
		}
	case eventUsageRecord:
		// Not "used": a counter is not conversation, so it must not move the
		// session's update time.
		var data usageRecordData
		if err = json.Unmarshal(event.Data, &data); err == nil {
			b.addUsageRecord(data)
		}
	}
	if err != nil {
		slog.Warn("Skipping Copilot CLI record with malformed data", "id", event.ID, "kind", event.Type, "error", err)
	}
	if used && event.Timestamp != "" {
		b.lastTimestamp = event.Timestamp
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
	b.openUserExchange(event, text)
	if b.name == "" {
		b.name = data.Content
	}
	return true
}

// openUserExchange closes the current exchange and opens one with this user
// message.
func (b *transcriptBuilder) openUserExchange(event rawEvent, text string) {
	b.flush()
	b.current = &schema.Exchange{ExchangeID: event.ID, StartTime: event.Timestamp}
	b.current.Messages = append(b.current.Messages, schema.Message{
		ID:        event.ID,
		Timestamp: event.Timestamp,
		Role:      schema.RoleUser,
		Content:   []schema.ContentPart{{Type: schema.ContentTypeText, Text: text}},
	})
}

// addUserToolRequest records a shell command the user ran as a labeled user
// message, so it reads as past activity rather than a new instruction. It does
// not name the session: a command is not what the session is about. Other
// user-started tools are left to the raw data.
func (b *transcriptBuilder) addUserToolRequest(event rawEvent, data userToolRequestData) bool {
	command := spi.StringValue(decodeArguments(data.Arguments), "command")
	if toolType(data.ToolName) != schema.ToolTypeShell || strings.TrimSpace(command) == "" {
		slog.Debug("Not rendering a user-started Copilot CLI tool", "toolName", data.ToolName)
		return false
	}
	b.openUserExchange(event, "User ran a shell command:\n\n"+spi.CodeFence(shellFenceLanguage(data.ToolName), command))
	if data.ToolCallID != "" {
		b.userShells[data.ToolCallID] = &userShell{exchange: b.current}
	}
	return true
}

// addUsageRecord keeps a model call's counts for its message. Copilot's input
// count already includes cached tokens, the same shape as the cached-input field.
func (b *transcriptBuilder) addUsageRecord(data usageRecordData) {
	usage := data.Usage
	if usage.APICallID == "" {
		return
	}
	b.callUsage[usage.APICallID] = &schema.Usage{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CachedInputTokens:        usage.CacheReadTokens,
		CacheCreationInputTokens: usage.CacheWriteTokens,
		ReasoningOutputTokens:    usage.ReasoningTokens,
	}
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
	first := len(exchange.Messages)
	if len(content) > 0 {
		exchange.Messages = append(exchange.Messages, schema.Message{
			ID:        event.ID,
			Timestamp: event.Timestamp,
			Role:      schema.RoleAgent,
			Model:     data.Model,
			Content:   content,
			Metadata:  metadata,
		})
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
		pending.exchange = exchange
		pending.index = len(exchange.Messages)
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
	// The usage record is the full account of the call. Sessions from versions
	// that do not write one fall back on the message's own output count.
	usage := b.callUsage[data.APICallID]
	delete(b.callUsage, data.APICallID)
	if usage == nil && data.OutputTokens > 0 {
		usage = &schema.Usage{OutputTokens: data.OutputTokens}
	}
	// One native message can become several schema messages, and a turn that
	// only calls tools has no text message at all. The count covers the whole
	// native message, so it goes on the first one: stats sum per message, and
	// repeating it would double count.
	if usage != nil && len(exchange.Messages) > first {
		exchange.Messages[first].Usage = usage
	}
	return true
}

func (b *transcriptBuilder) addToolResult(event rawEvent, data toolCompleteData) bool {
	if shell, ok := b.userShells[data.ToolCallID]; ok {
		delete(b.userShells, data.ToolCallID)
		shell.addResult(data)
		return true
	}

	// Tool call IDs are unique across the parent and its sub-agents, so one
	// lookup pairs results on either side of the sidechain.
	pending, ok := b.pendingTools[data.ToolCallID]
	if !ok {
		return false
	}
	delete(b.pendingTools, data.ToolCallID)

	switch {
	case !data.Success:
		pending.tool.Output = map[string]any{"result": data.failureMessage(), "is_error": true}
	case data.Result != nil:
		pending.tool.Output = map[string]any{"result": data.Result.Content}
	}
	if exitCode := data.exitCode(); exitCode != 0 && pending.tool.Output != nil {
		pending.tool.Output[exitCodeKey] = exitCode
	}
	pending.render()

	// The call is stamped with when it finished, not when the agent asked for
	// it: provenance matches a file change to the write that caused it within
	// a few seconds, and a permission prompt can hold a write far longer.
	if event.Timestamp != "" {
		pending.exchange.Messages[pending.index].Timestamp = event.Timestamp
	}
	return true
}

// failureMessage returns a failed call's error text.
func (data toolCompleteData) failureMessage() string {
	if data.Error != nil && data.Error.Message != "" {
		return data.Error.Message
	}
	return "Tool call failed"
}

// exitCode returns a shell command's exit code, or 0 when the result is not
// from a shell command or did not record one.
func (data toolCompleteData) exitCode() int {
	if data.ShellExecution == nil || data.ShellExecution.ExitCode == nil {
		return 0
	}
	return *data.ShellExecution.ExitCode
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

// finish returns the transcript. End times are set here, from each exchange's
// own messages, because a tool result can land after the next prompt and move a
// message in an exchange that has already been flushed.
func (b *transcriptBuilder) finish() []schema.Exchange {
	b.flush()
	for i := range b.exchanges {
		b.exchanges[i].EndTime = latestTimestamp(b.exchanges[i].Messages)
	}
	return b.exchanges
}

// latestTimestamp returns the latest message time, which is not always the last
// message's: parallel tool calls can finish out of order. Timestamps compare as
// strings because Copilot and reconstruction both write fixed-width UTC
// milliseconds.
func latestTimestamp(messages []schema.Message) string {
	latest := ""
	for _, message := range messages {
		latest = max(latest, message.Timestamp)
	}
	return latest
}

// userShell locates a user-run command's message so its result can join it. The
// message is always the first of the exchange the command opened, which stays
// reachable after it is flushed for the same reason as pendingTool's.
type userShell struct {
	exchange *schema.Exchange
}

// addResult appends the command's output and exit status to its message.
func (s *userShell) addResult(data toolCompleteData) {
	var output string
	switch {
	case !data.Success:
		output = data.failureMessage()
	case data.Result != nil:
		output = data.Result.Content
	}
	var sections []string
	if output = strings.TrimRight(spi.SanitizeShellOutput(output), " \t\n"); output != "" {
		sections = append(sections, "Output:\n\n"+spi.CodeFence("text", spi.CapRunes(output, spi.ToolResultCap)))
	}
	if exitCode := data.exitCode(); exitCode != 0 {
		sections = append(sections, fmt.Sprintf("Exit code: %d", exitCode))
	}
	if len(sections) > 0 {
		message := &s.exchange.Messages[0]
		message.Content = append(message.Content, schema.ContentPart{Type: schema.ContentTypeText, Text: strings.Join(sections, "\n\n")})
	}
}

// pendingTool is a tool call awaiting its result. intent is Copilot's one-line
// account of the call, kept so the markdown can be re-rendered once the
// result lands.
type pendingTool struct {
	tool   *schema.ToolInfo
	intent string
	// exchange and index locate the call's message so its result can restamp
	// it. An index rather than a message pointer, because later appends can
	// move the slice; the exchange stays reachable after it is flushed, since
	// the flushed copy shares its message array.
	exchange *schema.Exchange
	index    int
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

// render pre-renders the tool body: Copilot's one-line intent as prose, then the
// call and its capped, fenced result. A failed call's error text is its result.
func (p *pendingTool) render() {
	formatted := renderToolCall(p.tool) + renderToolOutput(p.tool)
	if p.intent != "" {
		formatted = "\n" + p.intent + "\n" + formatted
	}
	// Shown outside the capped result: Copilot writes the exit status as the
	// output's last line, which the cap cuts off on a long output.
	if exitCode, ok := p.tool.Output[exitCodeKey].(int); ok {
		formatted += fmt.Sprintf("\nExit code: %d\n", exitCode)
	}
	p.tool.FormattedMarkdown = nil
	if formatted != "" {
		p.tool.FormattedMarkdown = &formatted
	}
}

// renderToolCall renders what a call asked for: a shell command in a fence, a
// file change as a path and its content, and anything else as an Input list.
func renderToolCall(tool *schema.ToolInfo) string {
	if command := spi.StringValue(tool.Input, "command"); tool.Type == schema.ToolTypeShell && command != "" {
		return "\n" + spi.CodeFence(shellFenceLanguage(tool.Name), command) + "\n"
	}
	if call := renderFileChange(tool); call != "" {
		return call
	}
	return spi.RenderToolInputList(tool.Input)
}

// renderFileChange renders an edit as a diff and a created file in a fence
// tagged by its extension, or returns "" for any other tool. A call missing what
// that takes returns "" too, so it degrades to the Input list rather than losing
// arguments, and any argument not shown here stays listed after the change.
func renderFileChange(tool *schema.ToolInfo) string {
	path := spi.StringValue(tool.Input, "path")
	var body string
	var shown []string
	switch tool.Name {
	case editTool:
		oldText := spi.StringValue(tool.Input, "old_str")
		newText := spi.StringValue(tool.Input, "new_str")
		if oldText != "" || newText != "" {
			body = spi.FormatDiffBlock(oldText, newText)
		}
		shown = []string{"path", "old_str", "new_str"}
	case createTool:
		if text := strings.TrimRight(spi.StringValue(tool.Input, "file_text"), "\n"); text != "" {
			body = spi.CodeFence(spi.LanguageFromPath(path), text)
		}
		shown = []string{"path", "file_text"}
	}
	if path == "" || body == "" {
		return ""
	}
	return "\nPath: " + spi.InlineCode(path) + "\n\n" + body + "\n" + spi.RenderToolInputList(spi.WithoutKeys(tool.Input, shown...))
}

// renderToolOutput renders a call's result. Shell output is terminal text, so it
// goes in a text fence with its control bytes removed, and a JSON document, which
// MCP tools return on one long line, is indented in a json fence.
func renderToolOutput(tool *schema.ToolInfo) string {
	result, _ := tool.Output["result"].(string)
	if tool.Type == schema.ToolTypeShell {
		output := strings.TrimRight(spi.SanitizeShellOutput(result), " \t\n")
		if output == "" {
			return ""
		}
		return renderResultFence("text", output)
	}
	if indented, ok := indentJSONDocument(result); ok && tool.Name != viewTool {
		return renderResultFence("json", indented)
	}
	return spi.RenderToolResult(result)
}

func renderResultFence(language, result string) string {
	return "\n**Result:**\n\n" + spi.CodeFence(language, spi.CapRunes(result, spi.ToolResultCap)) + "\n"
}

// indentJSONDocument indents a result that is a whole JSON object or array. Bare
// values are left alone: a sub-agent's "27" is valid JSON but is plain text to a
// reader. json.Indent keeps the document's key order, so output stays stable.
func indentJSONDocument(result string) (string, bool) {
	trimmed := strings.TrimSpace(result)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, []byte(trimmed), "", "  "); err != nil {
		return "", false
	}
	return indented.String(), true
}

// shellFenceLanguage labels a command's fence with the shell that ran it.
func shellFenceLanguage(toolName string) string {
	if toolName == powerShellTool {
		return "powershell"
	}
	return "bash"
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
// are shell, under their bash names, the powershell names Copilot uses on
// Windows, or local_shell, the name it records for a user's `!command`; the todo
// list is task; and agent state (sub-agents, sessions,
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
	"bash":        schema.ToolTypeShell,
	"read_bash":   schema.ToolTypeShell,
	"stop_bash":   schema.ToolTypeShell,
	"list_bash":   schema.ToolTypeShell,
	"write_bash":  schema.ToolTypeShell,
	"local_shell": schema.ToolTypeShell,

	"powershell":       schema.ToolTypeShell,
	"read_powershell":  schema.ToolTypeShell,
	"write_powershell": schema.ToolTypeShell,
	"stop_powershell":  schema.ToolTypeShell,
	"list_powershell":  schema.ToolTypeShell,

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
	"semantic_code_search":                   schema.ToolTypeSearch,
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
	// apply_patch has no path argument: its raw patch text, kept under "input"
	// by decodeArguments, names each file it writes in a header line.
	if patch := spi.StringValue(tool.Input, "input"); tool.Name == applyPatchTool && patch != "" {
		return spi.ExtractPatchPathHints(patch, workspaceRoot)
	}
	return nil
}
