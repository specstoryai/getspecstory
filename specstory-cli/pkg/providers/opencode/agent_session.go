package opencode

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Registry id and product name. The id doubles as ProviderInfo.ID.
const (
	providerID   = "opencode"
	providerName = "OpenCode"
)

// defaultSlug names a session whose first prompt yields no usable words.
const defaultSlug = "opencode-session"

// Message record types, from the Session.Message.Info union in OpenCode
// 2.0.14's OpenAPI schema.
const (
	recordUser             = "user"
	recordAssistant        = "assistant"
	recordShell            = "shell"
	recordSynthetic        = "synthetic"
	recordSystem           = "system"
	recordSkill            = "skill"
	recordCompaction       = "compaction"
	recordIdle             = "idle"
	recordAgentSwitched    = "agent-switched"
	recordModelSwitched    = "model-switched"
	recordLocationSwitched = "location-switched"
)

// Assistant content part types.
const (
	partText      = "text"
	partReasoning = "reasoning"
	partTool      = "tool"
)

// Compaction statuses.
const (
	compactionCompleted = "completed"
	compactionFailed    = "failed"
)

// modelRef identifies the model that produced an assistant step.
type modelRef struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	Variant    string `json:"variant,omitempty"`
}

// structuredError is OpenCode's error envelope for failed steps and tools.
type structuredError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Status  int    `json:"status,omitempty"`
}

// tokenUsage is an assistant step's token accounting. The schema declares the
// counts as JSON numbers, so they decode as float64.
type tokenUsage struct {
	Input     float64 `json:"input"`
	Output    float64 `json:"output"`
	Reasoning float64 `json:"reasoning"`
	Cache     struct {
		Read  float64 `json:"read"`
		Write float64 `json:"write"`
	} `json:"cache"`
}

type recordTime struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed,omitempty"`
}

// fileAttachment is a file the user attached to a prompt. data (the base64
// body) is deliberately not decoded.
type fileAttachment struct {
	Name        string `json:"name"`
	Mime        string `json:"mime"`
	Description string `json:"description"`
}

type agentAttachment struct {
	Name string `json:"name"`
}

type shellOutput struct {
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
}

// toolContent is one item of a tool result: text, or a file reference.
type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
	URI  string `json:"uri"`
	Mime string `json:"mime"`
	Name string `json:"name"`
}

// toolState carries a call's arguments and, once it finishes, its result.
// Input is an object except while streaming, when it is the partial argument
// string.
type toolState struct {
	Status   string           `json:"status"`
	Input    json.RawMessage  `json:"input"`
	Content  []toolContent    `json:"content"`
	Error    *structuredError `json:"error"`
	Metadata map[string]any   `json:"metadata"`
}

// contentPart is one element of an assistant step's content array.
type contentPart struct {
	Type  string      `json:"type"`
	Text  string      `json:"text"`
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	State *toolState  `json:"state"`
	Time  *recordTime `json:"time"`
}

// nativeMessage is the union of every session_message payload. Fields are
// shared across record types where OpenCode uses the same name (text, agent,
// model, status).
type nativeMessage struct {
	Time     recordTime     `json:"time"`
	Metadata map[string]any `json:"metadata"`

	// user, synthetic, system, skill
	Text   string            `json:"text"`
	Files  []fileAttachment  `json:"files"`
	Agents []agentAttachment `json:"agents"`

	// assistant
	Agent   string           `json:"agent"`
	Model   *modelRef        `json:"model"`
	Content []contentPart    `json:"content"`
	Finish  string           `json:"finish"`
	Error   *structuredError `json:"error"`
	Tokens  *tokenUsage      `json:"tokens"`

	// shell (status is shared with compaction)
	Command string          `json:"command"`
	Status  string          `json:"status"`
	Exit    json.RawMessage `json:"exit"`
	Output  *shellOutput    `json:"output"`

	// compaction
	Reason  string `json:"reason"`
	Summary string `json:"summary"`
}

// rawRecord is one row of the native transcript as carried in RawData and the
// debug export: the table it came from and every column it holds.
type rawRecord struct {
	Table string         `json:"table"`
	Row   map[string]any `json:"row"`
}

// convertSnapshot turns a session snapshot into the provider-agnostic session.
// It returns nil when the session has no user prompt yet (OpenCode creates the
// session row before the first message arrives).
func convertSnapshot(snapshot *sessionSnapshot, projectPath string, debugRaw bool) *spi.AgentChatSession {
	if snapshot == nil {
		return nil
	}
	sessionData, firstPrompt := buildSessionData(snapshot, projectPath)
	if firstPrompt == "" {
		slog.Debug("convertSnapshot: Session has no user prompt yet", "sessionId", snapshot.Session.ID)
		return nil
	}

	if debugRaw {
		if err := writeDebugRawFiles(snapshot); err != nil {
			slog.Warn("convertSnapshot: Failed to write debug files",
				"sessionId", snapshot.Session.ID,
				"path", spi.GetDebugDir(snapshot.Session.ID),
				"error", err)
		}
	}

	return &spi.AgentChatSession{
		SessionID:   snapshot.Session.ID,
		CreatedAt:   sessionData.CreatedAt,
		Slug:        sessionData.Slug,
		SessionData: sessionData,
		RawData:     buildRawData(snapshot),
	}
}

// buildSessionData converts the snapshot's records into SessionData and
// returns the first user prompt, which names the session.
func buildSessionData(snapshot *sessionSnapshot, projectPath string) (*schema.SessionData, string) {
	session := snapshot.Session
	workspaceRoot := resolveWorkspaceRoot(session.Directory, projectPath)

	version := strings.TrimSpace(session.Version)
	if version == "" {
		version = "unknown"
	}

	builder := &exchangeBuilder{sessionID: session.ID, workspaceRoot: workspaceRoot}
	for i := range snapshot.Messages {
		builder.addRecord(&snapshot.Messages[i])
	}
	builder.flush()

	slug := defaultSlug
	if builder.firstPrompt != "" {
		if generated := spi.GenerateFilenameFromUserMessage(builder.firstPrompt); generated != "" {
			slug = generated
		}
	}

	return &schema.SessionData{
		SchemaVersion: schema.CurrentSchemaVersion,
		Provider: schema.ProviderInfo{
			ID:      providerID,
			Name:    providerName,
			Version: version,
		},
		SessionID:     session.ID,
		CreatedAt:     formatMillis(session.TimeCreated),
		UpdatedAt:     formatMillis(session.TimeUpdated),
		Slug:          slug,
		WorkspaceRoot: workspaceRoot,
		Exchanges:     builder.exchanges,
	}, builder.firstPrompt
}

// resolveWorkspaceRoot prefers the directory OpenCode recorded, then the
// caller's project, then the process working directory as a last resort so
// the workspace root is never empty (path normalization depends on it).
func resolveWorkspaceRoot(recorded, projectPath string) string {
	if strings.TrimSpace(recorded) != "" {
		return recorded
	}
	if strings.TrimSpace(projectPath) != "" {
		return projectPath
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// exchangeBuilder groups records into exchanges: each user prompt or user
// shell command opens one, and everything the agent does until the next
// opens is appended to it.
type exchangeBuilder struct {
	sessionID     string
	workspaceRoot string
	exchanges     []schema.Exchange
	current       *schema.Exchange
	firstPrompt   string
}

func (b *exchangeBuilder) addRecord(record *messageRecord) {
	var msg nativeMessage
	if err := json.Unmarshal(record.Data, &msg); err != nil {
		slog.Warn("Skipping corrupted OpenCode message record",
			"sessionId", b.sessionID,
			"messageId", record.ID,
			"seq", record.Seq,
			"error", err)
		return
	}
	created := msg.Time.Created
	if created == 0 {
		created = record.TimeCreated
	}
	timestamp := formatMillis(created)

	switch record.Type {
	case recordUser:
		b.addUser(record.ID, timestamp, &msg)
	case recordShell:
		b.addShell(record.ID, timestamp, &msg)
	case recordAssistant:
		b.addAssistant(record.ID, timestamp, &msg)
	case recordCompaction:
		b.addCompaction(record.ID, timestamp, &msg)
	case recordSynthetic, recordSystem, recordSkill:
		// Text OpenCode injects for the model (shell output echoes, Plan-mode
		// reminders, skill bodies), not something the user or agent said.
		slog.Debug("Skipping OpenCode scaffolding record", "sessionId", b.sessionID, "type", record.Type, "seq", record.Seq)
	case recordIdle, recordAgentSwitched, recordModelSwitched, recordLocationSwitched:
		// Lifecycle and configuration markers with no conversational content;
		// the agent and model they announce are recorded on each assistant step.
		slog.Debug("Skipping OpenCode lifecycle record", "sessionId", b.sessionID, "type", record.Type, "seq", record.Seq)
	default:
		slog.Debug("Skipping unknown OpenCode record type", "sessionId", b.sessionID, "type", record.Type, "seq", record.Seq)
	}
}

// startExchange closes the current exchange and opens a new one.
func (b *exchangeBuilder) startExchange(id, timestamp string) {
	b.flush()
	b.current = &schema.Exchange{ExchangeID: id, StartTime: timestamp}
}

// appendMessage adds a message to the current exchange, opening one when the
// agent speaks before any prompt (a compaction or an error at the very start).
func (b *exchangeBuilder) appendMessage(id string, message schema.Message) {
	if b.current == nil {
		b.startExchange(id, message.Timestamp)
	}
	b.current.Messages = append(b.current.Messages, message)
	if message.Timestamp != "" {
		b.current.EndTime = message.Timestamp
	}
}

func (b *exchangeBuilder) flush() {
	if b.current != nil && len(b.current.Messages) > 0 {
		b.exchanges = append(b.exchanges, *b.current)
	}
	b.current = nil
}

func (b *exchangeBuilder) addUser(id, timestamp string, msg *nativeMessage) {
	var parts []schema.ContentPart
	if strings.TrimSpace(msg.Text) != "" {
		parts = append(parts, schema.ContentPart{Type: schema.ContentTypeText, Text: msg.Text})
	}
	if attachments := formatAttachments(msg.Files, msg.Agents); attachments != "" {
		parts = append(parts, schema.ContentPart{Type: schema.ContentTypeText, Text: attachments})
	}
	if len(parts) == 0 {
		slog.Debug("Skipping empty OpenCode user record", "sessionId", b.sessionID, "messageId", id)
		return
	}
	if b.firstPrompt == "" {
		b.firstPrompt = userPromptLabel(msg)
	}
	b.startExchange(id, timestamp)
	b.appendMessage(id, schema.Message{
		ID:        id,
		Timestamp: timestamp,
		Role:      schema.RoleUser,
		Content:   parts,
	})
}

// userPromptLabel names a user record for slugs and listings: its text, or,
// for a prompt that carries only attachments, the attachments themselves.
// Listing and conversion both use it so a session is named the same way by
// each.
func userPromptLabel(msg *nativeMessage) string {
	if text := strings.TrimSpace(msg.Text); text != "" {
		return text
	}
	var names []string
	for _, file := range msg.Files {
		if name := strings.TrimSpace(file.Name); name != "" {
			names = append(names, name)
		}
	}
	for _, agent := range msg.Agents {
		if name := strings.TrimSpace(agent.Name); name != "" {
			names = append(names, "@"+name)
		}
	}
	if len(names) == 0 && len(msg.Files) > 0 {
		names = append(names, "attachment")
	}
	return strings.Join(names, " ")
}

// formatAttachments lists the files and agents attached to a prompt. File
// bodies are base64 payloads and are not reproduced.
func formatAttachments(files []fileAttachment, agents []agentAttachment) string {
	var lines []string
	for _, file := range files {
		name := strings.TrimSpace(file.Name)
		if name == "" {
			name = "(unnamed)"
		}
		line := "- Attached file: " + inlineCode(name)
		if file.Mime != "" {
			line += fmt.Sprintf(" (%s)", file.Mime)
		}
		lines = append(lines, line)
	}
	for _, agent := range agents {
		if strings.TrimSpace(agent.Name) != "" {
			lines = append(lines, "- Mentioned agent: "+inlineCode("@"+agent.Name))
		}
	}
	return strings.Join(lines, "\n")
}

// addShell renders a user's `!command` as a labeled user message so it reads
// as historical activity rather than a new instruction.
func (b *exchangeBuilder) addShell(id, timestamp string, msg *nativeMessage) {
	if strings.TrimSpace(msg.Command) == "" {
		return
	}
	parts := []schema.ContentPart{{
		Type: schema.ContentTypeText,
		Text: "User ran a shell command:\n\n" + spi.CodeFence("bash", msg.Command),
	}}
	if result := formatUserShellResult(msg); result != "" {
		parts = append(parts, schema.ContentPart{Type: schema.ContentTypeText, Text: result})
	}
	b.startExchange(id, timestamp)
	b.appendMessage(id, schema.Message{
		ID:        id,
		Timestamp: timestamp,
		Role:      schema.RoleUser,
		Content:   parts,
	})
}

func formatUserShellResult(msg *nativeMessage) string {
	var sections []string
	if msg.Output != nil {
		if output := strings.TrimRight(sanitizeShellOutput(msg.Output.Output), "\n"); output != "" {
			sections = append(sections, "Output:\n\n"+spi.CodeFence("text", spi.CapRunes(output, maxResultRunes)))
		}
		if msg.Output.Truncated {
			sections = append(sections, "[Output truncated by OpenCode]")
		}
	}
	if exit := exitCodeText(msg.Exit); exit != "" {
		sections = append(sections, "Exit code: "+exit)
	}
	switch msg.Status {
	case "timeout":
		sections = append(sections, "Timed out.")
	case "killed":
		sections = append(sections, "Killed.")
	case "running":
		sections = append(sections, "Still running.")
	}
	return strings.Join(sections, "\n\n")
}

// exitCodeText renders a shell exit status, which OpenCode records as a number
// or, for a signal, as a string such as "Infinity".
func exitCodeText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.String()
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return ""
}

func (b *exchangeBuilder) addAssistant(id, timestamp string, msg *nativeMessage) {
	model := ""
	if msg.Model != nil {
		model = msg.Model.ID
	}

	var produced []schema.Message
	// Text parts carry no time of their own; they take the latest time seen
	// earlier in the step so timestamps never run backwards within it.
	partTimestamp := timestamp
	for i := range msg.Content {
		part := &msg.Content[i]
		if part.Time != nil && part.Time.Created != 0 {
			if own := formatMillis(part.Time.Created); own > partTimestamp {
				partTimestamp = own
			}
		}
		partID := fmt.Sprintf("%s:%d", id, i)

		switch part.Type {
		case partText:
			if strings.TrimSpace(part.Text) == "" {
				// OpenCode stores whitespace-only text between tool calls.
				continue
			}
			produced = append(produced, schema.Message{
				ID: partID, Timestamp: partTimestamp, Role: schema.RoleAgent, Model: model,
				Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: part.Text}},
			})
		case partReasoning:
			if strings.TrimSpace(part.Text) == "" {
				// Encrypted reasoning has only an opaque blob and no text.
				continue
			}
			produced = append(produced, schema.Message{
				ID: partID, Timestamp: partTimestamp, Role: schema.RoleAgent, Model: model,
				Content: []schema.ContentPart{{Type: schema.ContentTypeThinking, Text: part.Text}},
			})
		case partTool:
			produced = append(produced, b.toolMessage(partID, partTimestamp, model, part))
		default:
			slog.Debug("Skipping unknown OpenCode assistant part", "sessionId", b.sessionID, "messageId", id, "partType", part.Type)
		}
	}

	if msg.Error != nil && strings.TrimSpace(msg.Error.Message) != "" {
		produced = append(produced, schema.Message{
			ID: id + ":error", Timestamp: timestamp, Role: schema.RoleAgent, Model: model,
			Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "[error] " + msg.Error.Message}},
		})
	}

	if len(produced) == 0 {
		return
	}
	// Usage belongs to the whole step, so it is recorded once, on the step's
	// last message, rather than repeated on every part.
	produced[len(produced)-1].Usage = mapUsage(msg.Tokens)
	for _, message := range produced {
		b.appendMessage(id, message)
	}
}

// toolMessage converts a tool part into an agent message carrying the call,
// its result, and pre-rendered markdown.
func (b *exchangeBuilder) toolMessage(id, timestamp, model string, part *contentPart) schema.Message {
	input, output := toolInputOutput(part.State)
	tool := &schema.ToolInfo{
		Name:   part.Name,
		Type:   toolType(part.Name),
		UseID:  part.ID,
		Input:  input,
		Output: output,
	}
	formatted := formatToolAsMarkdown(tool)
	tool.FormattedMarkdown = &formatted

	return schema.Message{
		ID:        id,
		Timestamp: timestamp,
		Role:      schema.RoleAgent,
		Model:     model,
		Tool:      tool,
		PathHints: toolPathHints(part.Name, input, b.workspaceRoot),
	}
}

// toolInputOutput decodes a tool state into the ToolInfo input and output
// maps. The output map is the provider's own shape, read back by the
// renderers: status, joined text content, file references, error, metadata.
func toolInputOutput(state *toolState) (map[string]any, map[string]any) {
	if state == nil {
		return nil, nil
	}

	var input map[string]any
	if len(state.Input) > 0 {
		if err := json.Unmarshal(state.Input, &input); err != nil {
			// A streaming call's input is the partial argument string.
			var partial string
			if json.Unmarshal(state.Input, &partial) == nil && partial != "" {
				input = map[string]any{"partialInput": partial}
			}
		}
	}

	output := map[string]any{"status": state.Status}
	var texts []string
	var files []string
	for _, item := range state.Content {
		switch item.Type {
		case "text":
			texts = append(texts, item.Text)
		case "file":
			files = append(files, describeFileContent(item))
		}
	}
	if len(texts) > 0 {
		output["texts"] = texts
	}
	if len(files) > 0 {
		output["files"] = files
	}
	if state.Error != nil && strings.TrimSpace(state.Error.Message) != "" {
		output["error"] = state.Error.Message
	}
	if len(state.Metadata) > 0 {
		output["metadata"] = state.Metadata
	}
	return input, output
}

func describeFileContent(item toolContent) string {
	label := item.Name
	if label == "" {
		label = item.URI
	}
	if item.Mime != "" {
		return fmt.Sprintf("%s (%s)", label, item.Mime)
	}
	return label
}

// addCompaction renders a finished compaction's summary, which OpenCode shows
// in place of the compacted history, and a failed compaction's error.
func (b *exchangeBuilder) addCompaction(id, timestamp string, msg *nativeMessage) {
	var text string
	switch msg.Status {
	case compactionCompleted:
		if strings.TrimSpace(msg.Summary) == "" {
			return
		}
		text = fmt.Sprintf("Conversation compacted (%s). Summary:\n\n%s", msg.Reason, msg.Summary)
	case compactionFailed:
		if msg.Error == nil || strings.TrimSpace(msg.Error.Message) == "" {
			return
		}
		text = "[error] Conversation compaction failed: " + msg.Error.Message
	default:
		// A running compaction is rewritten in place once it finishes.
		return
	}
	model := ""
	if msg.Model != nil {
		model = msg.Model.ID
	}
	b.appendMessage(id, schema.Message{
		ID:        id,
		Timestamp: timestamp,
		Role:      schema.RoleAgent,
		Model:     model,
		Content:   []schema.ContentPart{{Type: schema.ContentTypeText, Text: text}},
		Usage:     mapUsage(msg.Tokens),
	})
}

// mapUsage keeps OpenCode's distinct token kinds: input excludes cache reads,
// and reasoning is counted separately from output.
func mapUsage(tokens *tokenUsage) *schema.Usage {
	if tokens == nil {
		return nil
	}
	usage := &schema.Usage{
		InputTokens:              int(tokens.Input),
		OutputTokens:             int(tokens.Output),
		ReasoningOutputTokens:    int(tokens.Reasoning),
		CacheReadInputTokens:     int(tokens.Cache.Read),
		CacheCreationInputTokens: int(tokens.Cache.Write),
	}
	if *usage == (schema.Usage{}) {
		return nil
	}
	return usage
}

// formatMillis renders an OpenCode Unix-millisecond timestamp as RFC 3339.
func formatMillis(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

// buildRawData serializes the snapshot as JSON lines: the session row, then
// every message row in sequence order.
func buildRawData(snapshot *sessionSnapshot) string {
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	// Keep <, > and & as written (shell commands, markup) instead of the
	// &-style escapes json.Marshal applies for HTML embedding.
	encoder.SetEscapeHTML(false)
	for _, record := range rawRecords(snapshot) {
		// Encode writes the record followed by a newline, giving JSON lines.
		if err := encoder.Encode(record); err != nil {
			slog.Warn("buildRawData: Failed to encode OpenCode record", "sessionId", snapshot.Session.ID, "table", record.Table, "error", err)
		}
	}
	return b.String()
}

func rawRecords(snapshot *sessionSnapshot) []rawRecord {
	records := make([]rawRecord, 0, len(snapshot.Messages)+1)
	records = append(records, rawRecord{Table: sessionTable, Row: snapshot.Session.Row})
	for _, message := range snapshot.Messages {
		records = append(records, rawRecord{Table: messageTable, Row: message.Row})
	}
	return records
}

// writeDebugRawFiles exports the snapshot as numbered, pretty-printed records:
// 1.json is the session row, followed by one file per message in sequence
// order, each with every column OpenCode stored.
func writeDebugRawFiles(snapshot *sessionSnapshot) error {
	return spi.WriteDebugRecords(spi.GetDebugDir(snapshot.Session.ID), rawRecords(snapshot))
}
