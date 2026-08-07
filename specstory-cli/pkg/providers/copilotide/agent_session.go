package copilotide

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// ConvertToSessionData converts VS Code raw format to CLI's unified schema.
// A Provider method so the generated session data carries the variant's
// provider identity (VS Code vs VS Code Insiders).
func (p *Provider) ConvertToSessionData(composer VSCodeComposer, projectPath string, state *VSCodeStateFile) spi.AgentChatSession {
	// Format timestamps
	createdAt := FormatTimestamp(composer.CreationDate)
	updatedAt := FormatTimestamp(composer.LastMessageDate)

	// Generate slug
	slug := GenerateSlug(composer)

	// Handle editing-only sessions (no chat requests but has file operations)
	requests := composer.Requests
	if len(requests) == 0 && state != nil {
		syntheticRequests := createSyntheticRequestsFromEditingState(composer, state)
		if len(syntheticRequests) > 0 {
			requests = syntheticRequests
		}
	}

	// Build SessionData
	sessionData := &schema.SessionData{
		SchemaVersion: "1.0",
		Provider: schema.ProviderInfo{
			ID:      p.variant.ID,
			Name:    p.Name(),
			Version: "1.0",
		},
		SessionID:     composer.SessionID,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		Slug:          slug,
		WorkspaceRoot: projectPath,
		Exchanges:     ConvertRequestsToExchanges(requests),
	}

	// Marshal to JSON for raw data
	rawDataJSON, err := json.Marshal(composer)
	if err != nil {
		slog.Warn("Failed to marshal raw data", "sessionId", composer.SessionID, "error", err)
		rawDataJSON = []byte("{}")
	}

	return spi.AgentChatSession{
		SessionID:   composer.SessionID,
		CreatedAt:   createdAt,
		Slug:        slug,
		SessionData: sessionData,
		RawData:     string(rawDataJSON),
	}
}

// ConvertRequestsToExchanges converts VS Code request blocks to exchanges
func ConvertRequestsToExchanges(requests []VSCodeRequestBlock) []schema.Exchange {
	var exchanges []schema.Exchange

	for _, req := range requests {
		exchange := schema.Exchange{
			ExchangeID: req.RequestID,
			StartTime:  FormatTimestamp(req.Timestamp),
			Messages:   ConvertRequestToMessages(req),
		}
		exchanges = append(exchanges, exchange)
	}

	return exchanges
}

// ConvertRequestToMessages converts one request block to message array
// Returns: [user message, thinking message (if unique), then agent text and
// tool messages interleaved in the order the response array records them]
func ConvertRequestToMessages(req VSCodeRequestBlock) []schema.Message {
	var messages []schema.Message

	// 1. User message with timestamp
	userMsg := schema.Message{
		Role:      schema.RoleUser,
		Timestamp: FormatTimestamp(req.Timestamp),
		Content: []schema.ContentPart{
			{Type: schema.ContentTypeText, Text: req.Message.Text},
		},
	}
	messages = append(messages, userMsg)

	// Check if there are tool calls
	hasToolCalls := HasToolCalls(req.Result.Metadata)

	// The request's modelId for auto mode is the uninformative "copilot/auto";
	// the autoModeResolution response items record the model that actually ran.
	modelID := req.ModelID
	if resolved := ExtractResolvedModel(req.Response); resolved != "" {
		modelID = resolved
	}

	// 2. The turn's body: agent text and tool calls in recorded order.
	body, bodyText := ConvertResponsesToMessages(req.Response, req.Result.Metadata, modelID)

	// When the response array yielded no text at all, fall back to the metadata
	// sources (final assistant message, then round responses on tool-less turns).
	if strings.TrimSpace(bodyText) == "" {
		fallback := ExtractFinalAgentMessage(req.Result.Metadata)
		if fallback == "" && !hasToolCalls {
			fallback = ExtractResponseFromToolCallRounds(req.Result.Metadata)
		}
		if fallback != "" {
			body = append(body, schema.Message{
				Role:  schema.RoleAgent,
				Model: modelID,
				Content: []schema.ContentPart{
					{Type: schema.ContentTypeText, Text: fallback},
				},
			})
			bodyText = fallback
		}
	}

	// 3. Extract thinking from tool call rounds (only if there are actual tool
	// calls), skipping rounds whose text already appears in the body.
	if hasToolCalls {
		thinking := ExtractThinkingFromMetadata(req.Result.Metadata, bodyText)
		if thinking != "" {
			thinkingMsg := schema.Message{
				Role:  schema.RoleAgent,
				Model: modelID,
				Content: []schema.ContentPart{
					{Type: schema.ContentTypeThinking, Text: thinking},
				},
			}
			messages = append(messages, thinkingMsg)
		}
	}

	// 4. Body after the thinking block, mirroring how the turn played out.
	messages = append(messages, body...)

	return messages
}

// ConvertResponsesToMessages renders a turn's body — agent text and tool calls
// interleaved in the order the response array records them, which is the order
// the user actually saw. It also returns the turn's full rendered text (all
// text messages joined), which callers use for thinking deduplication and
// fallback decisions.
//
// Each tool invocation is resolved against metadata by ID first: a metadata
// tool call's ID is the invocation's toolCallId plus a "__vscode-<n>" suffix,
// so stripping the suffix pairs them exactly. Invocations without an ID match
// (older sessions serialize a VS Code UUID instead) claim the next unclaimed
// metadata call in order — the previous sequence behavior. Invocations with no
// metadata at all (canceled turns store an empty metadata object) render from
// their own resultDetails.
func ConvertResponsesToMessages(responses []json.RawMessage, metadata VSCodeResultMetadata, modelID string) ([]schema.Message, string) {
	items := collectResponseBodyItems(responses)

	// Ordered metadata calls with claim tracking. byInvocationID maps the
	// suffix-stripped metadata ID back to its position for exact pairing.
	calls := BuildToolCallSequence(metadata)
	claimed := make([]bool, len(calls))
	byInvocationID := make(map[string]int, len(calls))
	for i, call := range calls {
		prefix, _, _ := strings.Cut(call.ID, "__vscode-")
		if _, dup := byInvocationID[prefix]; !dup {
			byInvocationID[prefix] = i
		}
	}

	// Metadata resolution runs in two passes over the whole turn. Pass 1: exact
	// ID matches claim their calls. Pass 2: invocations without an exact match
	// (older sessions serialize a VS Code UUID instead) claim the remaining
	// calls in order — the old sequence behavior, now restricted to leftovers.
	// The pass split matters: a single in-order pass would let an early
	// UUID-style invocation with no metadata at all (e.g. a hidden todo-list
	// update) steal a call that a later invocation ID-matches exactly,
	// mislabeling every tool after it.
	assignments := make(map[int]*VSCodeToolCallInfo)
	for idx := range items {
		inv := items[idx].inv
		if inv == nil {
			continue
		}
		if i, ok := byInvocationID[inv.ToolCallID]; ok && !claimed[i] {
			claimed[i] = true
			assignments[idx] = &calls[i]
		}
	}
	nextUnclaimed := 0
	for idx := range items {
		if items[idx].inv == nil {
			continue
		}
		if _, ok := assignments[idx]; ok {
			continue
		}
		for nextUnclaimed < len(calls) && claimed[nextUnclaimed] {
			nextUnclaimed++
		}
		if nextUnclaimed < len(calls) {
			claimed[nextUnclaimed] = true
			assignments[idx] = &calls[nextUnclaimed]
		}
	}

	var messages []schema.Message
	var fullText strings.Builder
	var textRun []responseBodyItem

	flushTextRun := func() {
		if len(textRun) == 0 {
			return
		}
		text := joinTextItems(textRun)
		textRun = nil
		if strings.TrimSpace(text) == "" {
			return
		}
		if fullText.Len() > 0 {
			fullText.WriteString("\n\n")
		}
		fullText.WriteString(text)
		messages = append(messages, schema.Message{
			Role:  schema.RoleAgent,
			Model: modelID,
			Content: []schema.ContentPart{
				{Type: schema.ContentTypeText, Text: text},
			},
		})
	}

	for idx, item := range items {
		// Pre-built synthetic blocks (edit groups) emit directly.
		if item.synthTool != nil {
			flushTextRun()
			messages = append(messages, schema.Message{
				Role:  schema.RoleAgent,
				Model: modelID,
				Tool:  item.synthTool,
			})
			continue
		}
		if item.inv == nil {
			textRun = append(textRun, item)
			continue
		}

		// Assignment happened above the emit loop (hidden tools included, so
		// they can't leave a call to mispair with a later invocation). Hidden
		// tools emit no message and don't split the surrounding text run.
		call := assignments[idx]
		if item.inv.Presentation == "hidden" {
			continue
		}
		flushTextRun()

		var toolInfo *schema.ToolInfo
		if call != nil {
			toolInfo = BuildToolInfoFromInvocation(*item.inv, *call, metadata.ToolCallResults)
		} else {
			slog.Debug("Tool invocation has no metadata match; using invocation resultDetails",
				"toolId", item.inv.ToolID,
				"toolCallId", item.inv.ToolCallID)
			toolInfo = BuildToolInfoFromInvocationOnly(*item.inv)
		}
		if toolInfo != nil {
			messages = append(messages, schema.Message{
				Role:  schema.RoleAgent,
				Model: modelID,
				Tool:  toolInfo,
			})
		}
	}
	flushTextRun()

	return messages, fullText.String()
}

// BuildToolInfoFromInvocation creates ToolInfo from VS Code invocation + tool call
// Uses sequence-based matching: toolCall is passed directly instead of looked up by ID
func BuildToolInfoFromInvocation(
	invocation VSCodeToolInvocationResponse,
	toolCall VSCodeToolCallInfo,
	toolResults map[string]VSCodeToolCallResult,
) *schema.ToolInfo {
	toolInfo := &schema.ToolInfo{
		Name:  toolCall.Name,
		Type:  MapToolType(toolCall.Name),
		UseID: invocation.ToolCallID,
	}

	// Parse arguments if present
	if toolCall.Arguments != "" {
		var args map[string]any
		if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err == nil {
			toolInfo.Input = args
		}
	}

	// Add output from results map. metadata.toolCallResults is keyed by the
	// same OpenAI-style IDs as toolCallRounds (verified on real session files),
	// not by the invocation's VS Code UUID — so look up with the matched
	// toolCall's ID.
	if result, ok := toolResults[toolCall.ID]; ok {
		output := make(map[string]any)
		if len(result.Content) > 0 {
			var contentParts []string
			for _, content := range result.Content {
				// Value can be string or object - convert to string
				valueStr := valueToString(content.Value)
				if valueStr != "" {
					contentParts = append(contentParts, valueStr)
				}
			}
			if len(contentParts) > 0 {
				output["result"] = strings.Join(contentParts, "\n")
			}
		}
		if len(output) > 0 {
			toolInfo.Output = output
		}
	}

	// Pre-render Summary/FormattedMarkdown: cross-agent resume flattens tool calls
	// from these fields only, so without them the tool's payload (e.g. a written
	// file's content) would collapse to a bare tool name in the resumed session.
	// The summary matches the markdown generator's default, so archival markdown
	// keeps its familiar <summary> line. VS Code's own description of the call
	// ("Searched for files matching `**/*.txt`, 3 matches") leads the body — it
	// is the best one-line account of what happened and exists for nearly every
	// tool.
	summary := fmt.Sprintf("Tool use: **%s**", toolInfo.Name)
	toolInfo.Summary = &summary
	formatted := FormatToolMarkdown(toolInfo)
	if message := invocationMessageLine(invocation); message != "" {
		formatted = "\n" + message + "\n" + formatted
	}
	if formatted != "" {
		toolInfo.FormattedMarkdown = &formatted
	}

	return toolInfo
}

// BuildToolInfoFromInvocationOnly creates ToolInfo from an invocation that has no
// matching metadata tool call — the whole turn when it was canceled (VS Code then
// stores empty metadata), or the tail of a turn with more invocations than recorded
// calls. The invocation's resultDetails still carries the tool's input (a JSON
// string) and output (a list of embeds), so those render instead of dropping the
// tool from the markdown entirely.
func BuildToolInfoFromInvocationOnly(invocation VSCodeToolInvocationResponse) *schema.ToolInfo {
	name := invocation.ToolID
	if name == "" {
		name = "unknown"
	}
	toolInfo := &schema.ToolInfo{
		Name:  name,
		Type:  MapToolType(name),
		UseID: invocation.ToolCallID,
	}

	details, _ := invocation.ResultDetails.(map[string]any)

	// Input: resultDetails.input is the arguments JSON as a string.
	if inputJSON, ok := details["input"].(string); ok && inputJSON != "" {
		var args map[string]any
		if err := json.Unmarshal([]byte(inputJSON), &args); err == nil {
			toolInfo.Input = args
		}
	}

	// Result: the readable text of resultDetails (embeds or URI lists).
	if result := resultDetailsText(invocation.ResultDetails); result != "" {
		toolInfo.Output = map[string]any{"result": result}
	}

	// Same pre-rendering rationale as BuildToolInfoFromInvocation: cross-agent
	// resume flattens tool calls from Summary/FormattedMarkdown only. The
	// invocation's own message (e.g. "Updated todo list") leads the markdown —
	// it is the only human description these tools have.
	summary := fmt.Sprintf("Tool use: **%s**", toolInfo.Name)
	toolInfo.Summary = &summary
	formatted := FormatToolMarkdown(toolInfo)
	if message := invocationMessageLine(invocation); message != "" {
		formatted = "\n" + message + "\n" + formatted
	}
	if formatted != "" {
		toolInfo.FormattedMarkdown = &formatted
	}

	return toolInfo
}

// resultDetailsText extracts readable text from an invocation's resultDetails.
// Two shapes exist: an object whose "output" is a list of embeds (text embeds
// carry the content; binary embeds like screenshots are elided to a mime-type
// placeholder), and a bare list of URI objects (file-result tools), rendered as
// their paths.
func resultDetailsText(details any) string {
	var parts []string
	switch d := details.(type) {
	case map[string]any:
		outputs, _ := d["output"].([]any)
		for _, out := range outputs {
			embed, ok := out.(map[string]any)
			if !ok {
				continue
			}
			if mime, ok := embed["mimeType"].(string); ok && mime != "" {
				parts = append(parts, fmt.Sprintf("[%s embed]", mime))
				continue
			}
			if value, ok := embed["value"].(string); ok && value != "" {
				parts = append(parts, value)
			}
		}
	case []any:
		for _, item := range d {
			uri, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if path, ok := uri["fsPath"].(string); ok && path != "" {
				parts = append(parts, path)
			} else if path, ok := uri["path"].(string); ok && path != "" {
				parts = append(parts, path)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// valueToString converts a tool result value (which can be string or object) to
// a human-readable string. VS Code frequently stores results as a serialized
// renderer tree ({"node":{"children":[...],"text":...}}) whose readable content
// lives in the "text" leaves — extracting those turns an opaque JSON blob into
// the text the user actually saw. Other objects marshal to JSON as before.
func valueToString(value any) string {
	if value == nil {
		return ""
	}

	// Try string first
	if str, ok := value.(string); ok {
		return str
	}

	if m, ok := value.(map[string]any); ok {
		if node, ok := m["node"]; ok {
			if text := renderNodeTreeText(node); strings.TrimSpace(text) != "" {
				return text
			}
		}
	}

	// If it's an object, marshal to JSON
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		slog.Debug("Failed to marshal tool result value", "type", fmt.Sprintf("%T", value), "error", err)
		return ""
	}
	return string(jsonBytes)
}

// renderNodeTreeText walks a serialized VS Code renderer node, concatenating its
// "text" leaves in document order and inserting the line break the renderer
// would when a node asks for one.
func renderNodeTreeText(node any) string {
	var b strings.Builder
	var walk func(n any)
	walk = func(n any) {
		m, ok := n.(map[string]any)
		if !ok {
			return
		}
		if text, ok := m["text"].(string); ok && text != "" {
			if lb, _ := m["lineBreakBefore"].(bool); lb && b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
				b.WriteString("\n")
			}
			b.WriteString(text)
		}
		if children, ok := m["children"].([]any); ok {
			for _, child := range children {
				walk(child)
			}
		}
	}
	walk(node)
	return b.String()
}

// MapToolType maps VS Code Copilot tool names to schema.ToolType constants
func MapToolType(toolName string) string {
	// Handle MCP tools (any tool starting with "mcp_")
	// Note: MCP tools use generic type until schema.ToolTypeMCP is added
	if strings.HasPrefix(toolName, "mcp_") {
		return schema.ToolTypeGeneric
	}

	mapping := map[string]string{
		// VS Code Copilot tools (OpenAI API names)
		"grep_search":           schema.ToolTypeSearch,
		"apply_patch":           schema.ToolTypeWrite,
		"read_file":             schema.ToolTypeRead,
		"insert_edit_into_file": schema.ToolTypeWrite,
		"create_file":           schema.ToolTypeWrite,
		"file_search":           schema.ToolTypeSearch,
		"semantic_search":       schema.ToolTypeSearch,
		"list_dir":              schema.ToolTypeGeneric,
		"manage_todo_list":      schema.ToolTypeTask,
		"get_errors":            schema.ToolTypeGeneric,

		// Legacy tool names (kept for compatibility)
		"bash":               schema.ToolTypeShell,
		"search_files":       schema.ToolTypeSearch,
		"write_to_file":      schema.ToolTypeWrite,
		"str_replace_editor": schema.ToolTypeWrite,
		"list_files":         schema.ToolTypeSearch,
		"grep":               schema.ToolTypeSearch,
		"find":               schema.ToolTypeSearch,
	}

	if toolType, ok := mapping[toolName]; ok {
		return toolType
	}

	slog.Debug("Unknown tool type, mapping to generic", "toolName", toolName)
	return schema.ToolTypeGeneric
}

// createSyntheticRequestsFromEditingState creates synthetic request blocks from editing operations
// when there are no chat requests but file operations exist
func createSyntheticRequestsFromEditingState(composer VSCodeComposer, state *VSCodeStateFile) []VSCodeRequestBlock {
	if state == nil {
		return nil
	}

	// Detect state version
	version := state.Version
	if version == 0 {
		version = 1 // Default to version 1
	}

	slog.Debug("Processing editing state", "version", version, "sessionId", composer.SessionID)

	var fileOperationSummaries []string

	if version >= 2 {
		// Version 2: Extract from timeline.operations
		fileOperationSummaries = extractOperationsFromV2State(state)
		slog.Debug("Extracted operations from v2 state", "count", len(fileOperationSummaries))
	} else {
		// Version 1: Extract from recentSnapshot/pendingSnapshot
		fileOperationSummaries = extractOperationsFromV1State(state)
		slog.Debug("Extracted operations from v1 state", "count", len(fileOperationSummaries))
	}

	// Fallback: If we couldn't extract operations but state exists, show generic message
	if len(fileOperationSummaries) == 0 {
		// Check if there's any indication of editing activity
		hasRecentSnapshot := state.RecentSnapshot != nil
		hasPendingSnapshot := state.PendingSnapshot != nil
		hasTimeline := state.Timeline != nil

		if !hasRecentSnapshot && !hasPendingSnapshot && !hasTimeline {
			return nil // No editing activity detected
		}

		fileOperationSummaries = []string{"File editing session (details not available)"}
	}

	// Get user input text if available (from customTitle)
	userText := composer.CustomTitle
	if userText == "" {
		userText = "File editing session"
	}

	// Build synthetic text for assistant message
	var assistantText string
	if len(fileOperationSummaries) > 0 {
		plural := ""
		if len(fileOperationSummaries) > 1 {
			plural = "s"
		}
		assistantText = fmt.Sprintf("Performed %d file operation%s:\n\n%s",
			len(fileOperationSummaries),
			plural,
			strings.Join(fileOperationSummaries, "\n"))
	} else {
		assistantText = "Performed file editing operations"
	}

	// Create synthetic request block. ModelID is deliberately left empty: an
	// editing-only session records no model, and the responder username
	// ("GitHub Copilot") is not a model identifier — stamping it would mislabel
	// the model on every exported message.
	syntheticRequest := VSCodeRequestBlock{
		RequestID: composer.SessionID + "-synthetic",
		Timestamp: composer.CreationDate,
		Message: VSCodeMessage{
			Text: userText,
		},
		Response: []json.RawMessage{},
		Result: VSCodeResult{
			Metadata: VSCodeResultMetadata{
				Messages: []VSCodeMetadataMessage{
					{
						Role:    "assistant",
						Content: assistantText,
					},
				},
			},
		},
	}

	return []VSCodeRequestBlock{syntheticRequest}
}

// extractOperationsFromV2State extracts file operations from version 2 state format (timeline.operations)
func extractOperationsFromV2State(state *VSCodeStateFile) []string {
	if state.Timeline == nil || len(state.Timeline.Operations) == 0 {
		return nil
	}

	var summaries []string
	for _, op := range state.Timeline.Operations {
		var fileName string
		if op.URI != nil {
			// Extract file name from URI
			path := op.URI.FSPath
			if path == "" {
				path = op.URI.Path
			}
			if path != "" {
				parts := strings.Split(path, "/")
				fileName = parts[len(parts)-1]
			} else {
				fileName = "unknown file"
			}
		} else {
			fileName = "unknown file"
		}

		switch op.Type {
		case "create":
			summaries = append(summaries, fmt.Sprintf("Created file: `%s`", fileName))
		case "textEdit":
			editCount := len(op.Edits)
			if editCount == 0 {
				editCount = 1
			}
			plural := ""
			if editCount > 1 {
				plural = "s"
			}
			summaries = append(summaries, fmt.Sprintf("Edited `%s` (%d edit%s)", fileName, editCount, plural))
		case "delete":
			summaries = append(summaries, fmt.Sprintf("Deleted file: `%s`", fileName))
		default:
			summaries = append(summaries, fmt.Sprintf("%s: `%s`", op.Type, fileName))
		}
	}

	return summaries
}

// extractOperationsFromV1State extracts file operations from version 1 state format (recentSnapshot/pendingSnapshot)
func extractOperationsFromV1State(state *VSCodeStateFile) []string {
	filesSummary := make(map[string]bool)

	// Try recentSnapshot (can be array or object)
	if state.RecentSnapshot != nil {
		entries := extractEntriesFromSnapshot(state.RecentSnapshot)
		for _, entry := range entries {
			if fileName := extractFileNameFromEntry(entry); fileName != "" {
				filesSummary[fmt.Sprintf("Modified `%s`", fileName)] = true
			}
		}
	}

	// Try pendingSnapshot
	if state.PendingSnapshot != nil {
		entries := extractEntriesFromSnapshot(state.PendingSnapshot)
		for _, entry := range entries {
			if fileName := extractFileNameFromEntry(entry); fileName != "" {
				filesSummary[fmt.Sprintf("Modified `%s`", fileName)] = true
			}
		}
	}

	// Convert map to slice, sorted: map iteration order is randomized, and an
	// unsorted list would reorder the exported assistant text between runs.
	summaries := make([]string, 0, len(filesSummary))
	for summary := range filesSummary {
		summaries = append(summaries, summary)
	}
	sort.Strings(summaries)

	return summaries
}

// extractEntriesFromSnapshot extracts entries from a snapshot (handles both array and object formats)
func extractEntriesFromSnapshot(snapshot any) []VSCodeStopEntry {
	if snapshot == nil {
		return nil
	}

	var entries []VSCodeStopEntry

	// Try to unmarshal as VSCodeStop object
	if stopMap, ok := snapshot.(map[string]any); ok {
		if entriesData, ok := stopMap["entries"].([]any); ok {
			for _, entryData := range entriesData {
				if entryMap, ok := entryData.(map[string]any); ok {
					if resource, ok := entryMap["resource"].(string); ok {
						entries = append(entries, VSCodeStopEntry{Resource: resource})
					}
				}
			}
			return entries
		}
	}

	// Try to unmarshal as array of VSCodeStop objects
	if stopsArray, ok := snapshot.([]any); ok {
		for _, stopData := range stopsArray {
			if stopMap, ok := stopData.(map[string]any); ok {
				if entriesData, ok := stopMap["entries"].([]any); ok {
					for _, entryData := range entriesData {
						if entryMap, ok := entryData.(map[string]any); ok {
							if resource, ok := entryMap["resource"].(string); ok {
								entries = append(entries, VSCodeStopEntry{Resource: resource})
							}
						}
					}
				}
			}
		}
	}

	return entries
}

// extractFileNameFromEntry extracts the file name from an entry object
func extractFileNameFromEntry(entry VSCodeStopEntry) string {
	if entry.Resource == "" {
		return ""
	}

	// Handle both URI strings and file paths
	resource := entry.Resource
	parts := strings.Split(resource, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}

	return resource
}
