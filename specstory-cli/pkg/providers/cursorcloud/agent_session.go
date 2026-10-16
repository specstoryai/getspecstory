package cursorcloud

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// ProviderID is the registry id, also used as ProviderInfo.ID.
const ProviderID = "cursorcloud"

// providerName is the product's own name.
const providerName = "Cursor Cloud Agent"

// vmWorkspaceRoot is where the cloud VM checks out the repository; every
// observed tool path and workingDirectory is under it.
const vmWorkspaceRoot = "/workspace"

// segmentKind orders what an agent produced within one run.
type segmentKind int

const (
	segmentThinking segmentKind = iota
	segmentText
	segmentTool
)

// segment is one contiguous piece of agent output: a run of thinking deltas,
// a run of assistant deltas, or one tool call.
type segment struct {
	kind segmentKind
	text strings.Builder
	time string
	call *toolCall
}

// toolCall is the merged state of every tool_call event sharing a callId.
type toolCall struct {
	callID string
	name   string
	status string
	args   map[string]any
	result map[string]any
	time   string
}

// runTranscript is one run's stream reduced to what renders.
type runTranscript struct {
	userText   string
	userTime   string
	segments   []*segment
	startTime  string
	resultTime string
}

// interactionTypesWithoutContent are interaction_update types observed in
// every capture that carry nothing to render: the *-delta types duplicate the
// thinking and assistant events, the rest are step and tool lifecycle markers.
var interactionTypesWithoutContent = []string{
	"token-delta", "text-delta", "thinking-delta", "thinking-completed",
	"step-started", "step-completed", "tool-call-started", "tool-call-completed",
	"tool-requests-listed", "partial-tool-call", "turn-ended",
}

// parseRunEvents folds a run's events, in stream order, into a transcript.
func parseRunEvents(events []sseEvent, source string) runTranscript {
	var t runTranscript
	calls := map[string]*toolCall{}

	for i, ev := range events {
		ts := eventTime(ev.ID)
		if t.startTime == "" && ts != "" {
			t.startTime = ts
		}
		switch ev.Event {
		case "status", "heartbeat", "done":
			// status opens and closes a run (the run object carries it), heartbeat
			// keeps a quiet live stream open, done marks the end of the stream.
		case "interaction_update":
			var update struct {
				Type        string `json:"type"`
				UserMessage struct {
					Text string `json:"text"`
				} `json:"userMessage"`
			}
			if err := json.Unmarshal(ev.Data, &update); err != nil {
				slog.Warn("Skipping undecodable stream event", "source", source, "index", i, "event", ev.Event, "error", err)
				continue
			}
			switch {
			case update.Type == "user-message-appended":
				// The run's prompt; the only place the stream records it.
				t.userText = update.UserMessage.Text
				t.userTime = ts
			case slices.Contains(interactionTypesWithoutContent, update.Type):
			default:
				slog.Debug("Unknown interaction_update type", "source", source, "type", update.Type)
			}
		case "thinking", "assistant":
			var delta struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Data, &delta); err != nil {
				slog.Warn("Skipping undecodable stream event", "source", source, "index", i, "event", ev.Event, "error", err)
				continue
			}
			kind := segmentText
			if ev.Event == "thinking" {
				kind = segmentThinking
			}
			t.appendDelta(kind, delta.Text, ts)
		case "tool_call":
			t.mergeToolCall(calls, ev, ts, source, i)
		case "result":
			// The result text repeats the last assistant text, so only its time is used.
			t.resultTime = ts
		default:
			slog.Debug("Unknown stream event", "source", source, "event", ev.Event)
		}
	}
	return t
}

// appendDelta extends the current segment when it is of the same kind, so
// consecutive deltas become one part, and opens a new segment otherwise.
func (t *runTranscript) appendDelta(kind segmentKind, text, ts string) {
	if n := len(t.segments); n > 0 && t.segments[n-1].kind == kind {
		t.segments[n-1].text.WriteString(text)
		return
	}
	seg := &segment{kind: kind, time: ts}
	seg.text.WriteString(text)
	t.segments = append(t.segments, seg)
}

// mergeToolCall applies one tool_call event. A call is placed where its first
// event appears; later events update it field by field, so the last event
// wins. A result on a "running" event is provisional: the stream attaches a
// placeholder error ("Invalid arguments") while arguments are still streaming
// and replaces it when the call completes, so only results from other
// statuses are kept.
func (t *runTranscript) mergeToolCall(calls map[string]*toolCall, ev sseEvent, ts, source string, index int) {
	var payload struct {
		CallID string          `json:"callId"`
		Name   string          `json:"name"`
		Status string          `json:"status"`
		Args   json.RawMessage `json:"args"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(ev.Data, &payload); err != nil || payload.CallID == "" {
		slog.Warn("Skipping undecodable tool_call event", "source", source, "index", index, "error", err)
		return
	}
	call, seen := calls[payload.CallID]
	if !seen {
		call = &toolCall{callID: payload.CallID, time: ts}
		calls[payload.CallID] = call
		t.segments = append(t.segments, &segment{kind: segmentTool, time: ts, call: call})
	}
	if payload.Name != "" {
		call.name = payload.Name
	}
	if payload.Status != "" {
		call.status = payload.Status
	}
	if len(payload.Args) > 0 {
		var args map[string]any
		if err := json.Unmarshal(payload.Args, &args); err == nil {
			call.args = args
		}
	}
	if len(payload.Result) > 0 && payload.Status != "running" {
		var result map[string]any
		if err := json.Unmarshal(payload.Result, &result); err == nil {
			call.result = result
		}
	}
}

// buildSessionData converts a mirrored agent into SessionData. It returns nil
// when no run yields a user prompt, which means there is nothing to save yet.
func buildSessionData(snap *agentSnapshot, workspaceRoot string) (*schema.SessionData, string) {
	agent := snap.agent
	data := &schema.SessionData{
		SchemaVersion: schema.CurrentSchemaVersion,
		// The API exposes no agent version, so the honest value is "unknown".
		Provider:      schema.ProviderInfo{ID: ProviderID, Name: providerName, Version: "unknown"},
		SessionID:     agent.ID,
		CreatedAt:     agent.CreatedAt,
		UpdatedAt:     agent.UpdatedAt,
		WorkspaceRoot: workspaceRoot,
		Exchanges:     []schema.Exchange{},
	}

	conv := alignedConversation(snap)
	firstPrompt := ""
	textOnly := 0
	for i, run := range snap.runs {
		exchange := buildExchange(snap, conv, i, run, workspaceRoot)
		if exchange == nil {
			continue
		}
		if firstPrompt == "" {
			firstPrompt = firstUserText(exchange)
		}
		if exchange.Metadata["source"] == v0Source {
			textOnly++
		}
		data.Exchanges = append(data.Exchanges, *exchange)
	}
	if firstPrompt == "" {
		return nil, ""
	}
	if note := textOnlyNote(textOnly, len(data.Exchanges)); note != "" {
		data.Notes = []string{note}
	}
	data.Slug = spi.GenerateFilenameFromUserMessage(firstPrompt)
	return data, firstPrompt
}

// buildExchange builds one run's exchange. The stream is the full-fidelity
// source; the v0 conversation conv (text only, nil when unusable) fills in when
// the stream is missing, or when a finished run's capture is partial and the
// stream has since expired.
func buildExchange(snap *agentSnapshot, conv *v0Conversation, index int, run runSnapshot, workspaceRoot string) *schema.Exchange {
	source := snap.dir + "/runs/" + run.run.ID
	useStream := len(run.events) > 0
	if useStream && run.run.isTerminal() && !eventsAreWhole(run.events) && len(v0TurnMessages(conv, index)) > 0 {
		useStream = false
	}

	var exchange *schema.Exchange
	if useStream {
		exchange = exchangeFromStream(parseRunEvents(run.events, source), run, workspaceRoot)
	}
	if exchange == nil {
		exchange = exchangeFromConversation(conv, index, run)
	}
	if exchange == nil {
		slog.Debug("Run has no recorded content yet", "agentId", snap.agent.ID, "runId", run.run.ID)
		return nil
	}
	exchange.Metadata = exchangeMetadata(snap.agent, run.run, exchange.Metadata)
	return exchange
}

// exchangeFromStream turns a transcript into messages: the prompt, then agent
// messages in stream order. Thinking and text share one agent message until a
// tool call interrupts; each tool call is its own message.
func exchangeFromStream(t runTranscript, run runSnapshot, workspaceRoot string) *schema.Exchange {
	if t.userText == "" && len(t.segments) == 0 {
		return nil
	}
	runID := run.run.ID
	exchange := &schema.Exchange{ExchangeID: runID, StartTime: t.startTime, EndTime: t.resultTime}

	if t.userText != "" {
		userTime := t.userTime
		if userTime == "" {
			userTime = run.run.CreatedAt
		}
		exchange.Messages = append(exchange.Messages, schema.Message{
			ID:        runID + ":user",
			Timestamp: userTime,
			Role:      schema.RoleUser,
			Content:   []schema.ContentPart{{Type: schema.ContentTypeText, Text: t.userText}},
		})
	}

	var current *schema.Message
	flush := func() {
		if current != nil && len(current.Content) > 0 {
			exchange.Messages = append(exchange.Messages, *current)
		}
		current = nil
	}
	for _, seg := range t.segments {
		switch seg.kind {
		case segmentThinking, segmentText:
			text := seg.text.String()
			if strings.TrimSpace(text) == "" {
				continue
			}
			if current == nil {
				current = &schema.Message{
					ID:        runID + ":" + strconv.Itoa(len(exchange.Messages)),
					Timestamp: seg.time,
					Role:      schema.RoleAgent,
				}
			}
			partType := schema.ContentTypeText
			if seg.kind == segmentThinking {
				partType = schema.ContentTypeThinking
			}
			current.Content = append(current.Content, schema.ContentPart{Type: partType, Text: text})
		case segmentTool:
			flush()
			exchange.Messages = append(exchange.Messages, toolMessage(seg.call, workspaceRoot))
		}
	}
	flush()
	return exchange
}

// toolMessage converts one merged tool call.
func toolMessage(call *toolCall, workspaceRoot string) schema.Message {
	tool := &schema.ToolInfo{
		Name:   call.name,
		Type:   toolType(call.name),
		UseID:  call.callID,
		Input:  cleanToolInput(call.args),
		Output: call.result,
	}
	if markdown := formatTool(tool); markdown != "" {
		tool.FormattedMarkdown = &markdown
	}
	msg := schema.Message{
		ID:        call.callID,
		Timestamp: call.time,
		Role:      schema.RoleAgent,
		Tool:      tool,
		PathHints: pathHints(call.name, tool.Input, workspaceRoot),
	}
	if call.status != "" {
		msg.Metadata = map[string]any{"status": call.status}
	}
	return msg
}

// pathHints lists the files a call wrote. Paths are recorded under the VM's
// /workspace; rewriting that prefix onto the local project lets the shared
// normalizer make them project-relative like every other provider's hints.
func pathHints(name string, input map[string]any, workspaceRoot string) []string {
	var hints []string
	switch toolType(name) {
	case schema.ToolTypeWrite:
		if path := spi.StringValue(input, "path"); path != "" {
			hints = append(hints, spi.NormalizePath(localPath(path, workspaceRoot), workspaceRoot))
		}
	case schema.ToolTypeShell:
		command := spi.StringValue(input, "command")
		cwd := localPath(spi.StringValue(input, "workingDirectory"), workspaceRoot)
		if cwd == "" {
			cwd = workspaceRoot
		}
		for _, hint := range spi.ExtractShellPathHints(command, cwd, workspaceRoot) {
			if !slices.Contains(hints, hint) {
				hints = append(hints, hint)
			}
		}
	}
	return hints
}

// localPath maps a VM path onto the local project root. The root is joined by
// shape with "/" because recorded paths are POSIX; NormalizePath accepts
// either separator.
func localPath(path, workspaceRoot string) string {
	if workspaceRoot == "" {
		return path
	}
	root := strings.TrimRight(workspaceRoot, `/\`)
	if path == vmWorkspaceRoot {
		return root
	}
	if rest, ok := strings.CutPrefix(path, vmWorkspaceRoot+"/"); ok {
		return root + "/" + rest
	}
	return path
}

// displayPath shows a VM path relative to the repository.
func displayPath(path string) string {
	if path == vmWorkspaceRoot {
		return "."
	}
	if rest, ok := strings.CutPrefix(path, vmWorkspaceRoot+"/"); ok {
		return rest
	}
	return path
}

// alignedConversation returns the snapshot's v0 conversation when its turns
// line up one-to-one with the mirrored runs, else nil. v0 messages carry no run
// id, only a turn index, so turn N is taken to be the Nth run in creation
// order. A run missing from the mirror (undecodable, or a refresh interrupted
// between files) would shift that pairing and put text under the wrong
// exchange; a missing exchange is better than a misattributed one.
func alignedConversation(snap *agentSnapshot) *v0Conversation {
	conv := snap.conversation
	if conv == nil {
		return nil
	}
	turns := 0
	for _, msg := range conv.Messages {
		turns = max(turns, v0MessageTurn(msg.ID)+1)
	}
	if turns != len(snap.runs) {
		slog.Warn("Ignoring v0 conversation whose turns do not match the mirrored runs",
			"agentId", snap.agent.ID, "turns", turns, "runs", len(snap.runs))
		return nil
	}
	return conv
}

// v0TurnMessages returns the v0 messages belonging to run index turn. Ids are
// "turn-N:user" and "turn-N:step:M:assistant"; the agent's very first user
// message has a bare UUID id and belongs to turn 0.
func v0TurnMessages(conv *v0Conversation, turn int) []v0Message {
	if conv == nil {
		return nil
	}
	var out []v0Message
	for _, msg := range conv.Messages {
		if v0MessageTurn(msg.ID) == turn {
			out = append(out, msg)
		}
	}
	return out
}

// v0MessageTurn parses the turn index from a v0 message id.
func v0MessageTurn(id string) int {
	rest, ok := strings.CutPrefix(id, "turn-")
	if !ok {
		return 0
	}
	digits, _, _ := strings.Cut(rest, ":")
	n, err := strconv.Atoi(digits)
	if err != nil {
		return -1
	}
	return n
}

// exchangeFromConversation builds a text-only exchange from the v0
// conversation. v0 records no times, so the run's own times stand in.
func exchangeFromConversation(conv *v0Conversation, index int, run runSnapshot) *schema.Exchange {
	messages := v0TurnMessages(conv, index)
	if len(messages) == 0 {
		return nil
	}
	exchange := &schema.Exchange{
		ExchangeID: run.run.ID,
		StartTime:  run.run.CreatedAt,
		EndTime:    run.run.UpdatedAt,
		Metadata:   map[string]any{"source": v0Source},
	}
	for _, msg := range messages {
		if strings.TrimSpace(msg.Text) == "" {
			continue
		}
		role := schema.RoleAgent
		timestamp := ""
		switch msg.Type {
		case "user_message":
			role = schema.RoleUser
			timestamp = run.run.CreatedAt
		case "assistant_message":
		default:
			slog.Debug("Unknown v0 message type", "type", msg.Type, "id", msg.ID)
		}
		exchange.Messages = append(exchange.Messages, schema.Message{
			ID:        msg.ID,
			Timestamp: timestamp,
			Role:      role,
			Content:   []schema.ContentPart{{Type: schema.ContentTypeText, Text: msg.Text}},
		})
	}
	if len(exchange.Messages) == 0 {
		return nil
	}
	return exchange
}

// v0Source marks an exchange built from the text-only v0 conversation.
const v0Source = "v0-conversation"

// textOnlyNote explains why textOnly of total runs show no tool calls, which
// otherwise looks like a parser bug rather than data the API no longer serves.
func textOnlyNote(textOnly, total int) string {
	const reason = "Cursor keeps a run's full transcript for only 24 hours, so only the conversation text is shown."
	switch {
	case textOnly == 0:
		return ""
	case textOnly == total && total == 1:
		return "Tool calls for this run are not available. " + reason
	case textOnly == total:
		return "Tool calls for this session's runs are not available. " + reason
	default:
		return fmt.Sprintf("Tool calls for %d of this session's %d runs are not available. %s", textOnly, total, reason)
	}
}

// exchangeMetadata records what a reader wants to click: the agent page, the
// branch and pull request, and how the run ended. Token usage lives in the
// usage endpoint per run, not per message, so it stays in RawData rather than
// being forced into the per-message Usage field.
func exchangeMetadata(agent apiAgent, run apiRun, existing map[string]any) map[string]any {
	meta := existing
	if meta == nil {
		meta = map[string]any{}
	}
	meta["runId"] = run.ID
	if run.Status != "" {
		meta["status"] = run.Status
	}
	if run.DurationMs > 0 {
		meta["durationMs"] = run.DurationMs
	}
	if len(run.Git.Branches) > 0 {
		branch := run.Git.Branches[0]
		if branch.Branch != "" {
			meta["branch"] = branch.Branch
		}
		if branch.PrURL != "" {
			meta["prUrl"] = branch.PrURL
		}
	}
	if agent.URL != "" {
		meta["agentUrl"] = agent.URL
	}
	return meta
}

// firstUserText returns the text of an exchange's first user message.
func firstUserText(exchange *schema.Exchange) string {
	for _, msg := range exchange.Messages {
		if msg.Role != schema.RoleUser {
			continue
		}
		for _, part := range msg.Content {
			if part.Type == schema.ContentTypeText && strings.TrimSpace(part.Text) != "" {
				return part.Text
			}
		}
	}
	return ""
}

// sessionName prefers the agent's own name (Cursor sets one, derived from the
// prompt when the launcher gave none) over the shared generator.
func sessionName(agent apiAgent, firstPrompt string) string {
	if name := strings.TrimSpace(agent.Name); name != "" {
		return name
	}
	return spi.GenerateReadableName(firstPrompt)
}

// rawSession is the RawData envelope: the native objects of one mirror read.
type rawSession struct {
	Agent        json.RawMessage `json:"agent"`
	Runs         []rawRun        `json:"runs"`
	Conversation json.RawMessage `json:"conversation,omitempty"`
	Usage        json.RawMessage `json:"usage,omitempty"`
}

type rawRun struct {
	Run    json.RawMessage `json:"run"`
	Events []sseEvent      `json:"events"`
}

// buildRawData serializes the snapshot the session was built from, so RawData
// never describes more than the converted session saw.
func buildRawData(snap *agentSnapshot) (string, error) {
	raw := rawSession{Agent: snap.agentRaw, Conversation: snap.conversationRaw, Usage: snap.usageRaw, Runs: []rawRun{}}
	for _, run := range snap.runs {
		events := run.events
		if events == nil {
			events = []sseEvent{}
		}
		raw.Runs = append(raw.Runs, rawRun{Run: run.runRaw, Events: events})
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return "", fmt.Errorf("marshal raw data: %w", err)
	}
	return string(data), nil
}
