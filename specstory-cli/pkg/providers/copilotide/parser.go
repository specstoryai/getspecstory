package copilotide

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// ParseResponseKind identifies the response type without fully parsing
func ParseResponseKind(rawResponse json.RawMessage) (string, error) {
	var kindOnly struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(rawResponse, &kindOnly); err != nil {
		return "", fmt.Errorf("failed to parse response kind: %w", err)
	}
	return kindOnly.Kind, nil
}

// BuildToolCallSequence creates an ordered list of tool calls from metadata
// This is used for sequence-based matching since VS Code IDs don't match OpenAI IDs
func BuildToolCallSequence(metadata VSCodeResultMetadata) []VSCodeToolCallInfo {
	var sequence []VSCodeToolCallInfo

	for _, round := range metadata.ToolCallRounds {
		sequence = append(sequence, round.ToolCalls...)
	}

	return sequence
}

// HasToolCalls checks if there are any tool calls in the metadata
func HasToolCalls(metadata VSCodeResultMetadata) bool {
	for _, round := range metadata.ToolCallRounds {
		if len(round.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// ExtractThinkingFromMetadata extracts thinking text from tool call rounds.
// Should only be called when there are actual tool calls.
//
// excludeText is the turn's final rendered text: VS Code duplicates each
// round's response verbatim into the response array (it is the visible streamed
// narration, not hidden reasoning), so any round already present there is
// skipped — otherwise the same paragraphs appear twice, once in the thinking
// block and once in the message body.
func ExtractThinkingFromMetadata(metadata VSCodeResultMetadata, excludeText string) string {
	var thinking strings.Builder

	for _, round := range metadata.ToolCallRounds {
		// Only include response if there are tool calls in this round
		if len(round.ToolCalls) == 0 || round.Response == "" {
			continue
		}
		if trimmed := strings.TrimSpace(round.Response); trimmed == "" || containsIgnoringWhitespace(excludeText, trimmed) {
			continue
		}
		thinking.WriteString(round.Response)
		thinking.WriteString("\n\n")
	}

	return strings.TrimSpace(thinking.String())
}

// containsIgnoringWhitespace reports whether haystack contains needle when all
// whitespace is stripped from both. The same narration is stored with different
// whitespace in the two places it appears (tool-call rounds fuse streamed
// sentences with no separator; response fragments carry their own newlines), so
// a plain substring check misses duplicates that differ only in spacing.
func containsIgnoringWhitespace(haystack, needle string) bool {
	strip := func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}
	return strings.Contains(strings.Map(strip, haystack), strings.Map(strip, needle))
}

// ExtractResponseFromToolCallRounds gets the response from tool call rounds when no tool calls present
// This is used when toolCallRounds exists but toolCalls is empty - the response is the final output
func ExtractResponseFromToolCallRounds(metadata VSCodeResultMetadata) string {
	for _, round := range metadata.ToolCallRounds {
		if round.Response != "" {
			return round.Response
		}
	}
	return ""
}

// responseBodyItem is one ordered element of a turn's rendered body: a text
// fragment (plain markdown or an inline reference chip, text set), a tool
// invocation (inv set), or a pre-built synthetic tool block (synthTool set —
// used for edit groups, which are not tool invocations but render best as
// collapsible blocks).
type responseBodyItem struct {
	text      string
	isRef     bool
	inv       *VSCodeToolInvocationResponse
	synthTool *schema.ToolInfo
}

// collectResponseBodyItems parses the response array into ordered body items —
// the single source of truth for what a turn rendered, and in what order.
//
// Tool invocations are deduplicated: VS Code appends a fresh serialization of
// the same invocation each time its state updates (running → completed), so the
// same toolCallId can appear several times. One item is kept at the first
// occurrence's position (that is where the tool ran chronologically), carrying
// the last serialization's data (that is the one with the final resultDetails).
//
// Text fragments that are only a bare code fence are dropped: VS Code emits
// them as delimiters around structured code-block items (codeblockUri /
// textEditGroup) that are not rendered as text, and keeping them would leave
// empty code blocks in the output.
func collectResponseBodyItems(responses []json.RawMessage) []responseBodyItem {
	var items []responseBodyItem
	invIndexByCallID := make(map[string]int)

	for _, rawResp := range responses {
		kind, err := ParseResponseKind(rawResp)
		if err != nil {
			slog.Debug("Failed to parse response kind", "error", err)
			continue
		}

		switch kind {
		case "toolInvocationSerialized":
			var invocation VSCodeToolInvocationResponse
			if err := json.Unmarshal(rawResp, &invocation); err != nil {
				slog.Debug("Failed to parse tool invocation", "error", err)
				continue
			}
			key := invocation.ToolCallID
			if key == "" {
				key = invocation.ID
			}
			if key != "" {
				if pos, seen := invIndexByCallID[key]; seen {
					items[pos].inv = &invocation
					continue
				}
				invIndexByCallID[key] = len(items)
			}
			items = append(items, responseBodyItem{inv: &invocation})

		case "":
			// Plain markdown fragment (no kind at all — the normal text body).
			var frag struct {
				Value string `json:"value"`
			}
			if err := json.Unmarshal(rawResp, &frag); err != nil || frag.Value == "" || isBareFenceFragment(frag.Value) {
				continue
			}
			items = append(items, responseBodyItem{text: frag.Value})

		case "inlineReference":
			var ref struct {
				Name            string    `json:"name"` // symbol references carry a name
				InlineReference VSCodeUri `json:"inlineReference"`
			}
			if err := json.Unmarshal(rawResp, &ref); err != nil {
				continue
			}
			// Kept even when it renders no text: the chip splits a sentence, so
			// it must still mark its neighbors as adjacent-to-a-reference or
			// they would get a spurious paragraph break between them.
			items = append(items, responseBodyItem{text: inlineReferenceText(ref.Name, ref.InlineReference), isRef: true})

		case "textEditGroup", "notebookEditGroup":
			// The actual edits Copilot applied: replacement text plus target
			// ranges. Rendered as a collapsible edit block — without it, inline
			// edits are invisible in turns whose edit tool went unrecorded.
			var group VSCodeTextEditGroupResponse
			if err := json.Unmarshal(rawResp, &group); err != nil {
				slog.Debug("Failed to parse edit group", "kind", kind, "error", err)
				continue
			}
			if tool := editGroupToolInfo(kind, group); tool != nil {
				items = append(items, responseBodyItem{synthTool: tool})
			}

		case "confirmation":
			// A confirmation prompt the user saw; render it as a quoted line so
			// the turn records that the flow paused for an answer.
			var confirmation VSCodeConfirmationResponse
			if err := json.Unmarshal(rawResp, &confirmation); err != nil {
				continue
			}
			line := confirmation.Title
			if msg := confirmation.GetMessageText(); msg != "" {
				if line != "" {
					line += " — "
				}
				line += msg
			}
			if line != "" {
				items = append(items, responseBodyItem{text: "> ❓ " + sanitizeInvocationMessage(line)})
			}

		case "warning":
			// User-visible warnings explain otherwise-dead turns (e.g. "Chat
			// took too long to get ready...") — keep them in the record.
			var warning struct {
				Content any `json:"content"`
			}
			if err := json.Unmarshal(rawResp, &warning); err != nil {
				continue
			}
			if msg := sanitizeInvocationMessage(markdownStringText(warning.Content)); msg != "" {
				items = append(items, responseBodyItem{text: "> ⚠️ " + msg})
			}

		case "codeblockUri":
			// Labels the file a following code block belongs to; the content
			// itself arrives as the adjacent textEditGroup, which is rendered.
		case "thinking", "undoStop", "mcpServersStarting", "autoModeResolution", "progressMessage", "command":
			// Known kinds with nothing to render: opaque thinking blobs, undo
			// markers, MCP startup notices, auto-model-selection metadata (read
			// separately for the turn's model), transient progress spinner text,
			// and UI command buttons.
		default:
			slog.Debug("Unknown response kind", "kind", kind)
		}
	}

	return items
}

// editGroupToolInfo builds a synthetic collapsible block for a text or notebook
// edit group: "Edit: **file**" with each edit chunk fenced under an @@ line
// header. There is no before-text in the data, so this is the applied content,
// not a true diff — but it is the only record of inline edits, and the full
// content is what the agent chose to write, so chunks are not capped. Returns
// nil when the group carries neither a target file nor any chunks.
func editGroupToolInfo(kind string, group VSCodeTextEditGroupResponse) *schema.ToolInfo {
	filePath := group.Uri.FSPath
	if filePath == "" {
		filePath = group.Uri.Path
	}
	fileName := ""
	if filePath != "" {
		fileName = filepath.Base(filePath)
	}

	var b strings.Builder
	chunks := 0
	for _, editSet := range group.Edits {
		for _, edit := range editSet {
			if edit.Text == "" && edit.Range == (VSCodeRange{}) {
				continue
			}
			chunks++
			if edit.Text == "" {
				fmt.Fprintf(&b, "\n@@ lines %d-%d @@ (deleted)\n", edit.Range.StartLineNumber, edit.Range.EndLineNumber)
				continue
			}
			fence := codeFence(edit.Text)
			fmt.Fprintf(&b, "\n@@ lines %d-%d @@\n\n%s\n%s\n%s\n",
				edit.Range.StartLineNumber, edit.Range.EndLineNumber, fence, edit.Text, fence)
		}
	}

	if fileName == "" && chunks == 0 {
		return nil
	}

	label := "Edit"
	if kind == "notebookEditGroup" {
		label = "Notebook edit"
	}
	summary := fmt.Sprintf("%s: **%s**", label, fileName)
	toolInfo := &schema.ToolInfo{
		Name:    kind,
		Type:    schema.ToolTypeWrite,
		Summary: &summary,
	}
	if filePath != "" {
		toolInfo.Input = map[string]any{"filePath": filePath}
	}
	if formatted := b.String(); strings.TrimSpace(formatted) != "" {
		toolInfo.FormattedMarkdown = &formatted
	}
	return toolInfo
}

// ExtractResolvedModel returns the model that Copilot's automatic model
// selection resolved to for this turn — recorded in autoModeResolution response
// items — or "" when the turn carries none. The last resolution wins: a turn
// can re-resolve across rounds and the final one is what produced the visible
// response. Callers prefer this over the request's modelId, which for auto mode
// is the uninformative "copilot/auto".
func ExtractResolvedModel(responses []json.RawMessage) string {
	resolved := ""
	for _, rawResp := range responses {
		var item struct {
			Kind          string `json:"kind"`
			ResolvedModel string `json:"resolvedModel"`
		}
		if err := json.Unmarshal(rawResp, &item); err != nil {
			continue
		}
		if item.Kind == "autoModeResolution" && item.ResolvedModel != "" {
			resolved = item.ResolvedModel
		}
	}
	return resolved
}

// invocationMessageLine returns the invocation's human description (past tense
// preferred), sanitized for plain markdown. VS Code writes one for nearly every
// tool ("Searched for files matching `**/*.txt`, 3 matches") — it is the best
// one-line account of what the call did.
func invocationMessageLine(invocation VSCodeToolInvocationResponse) string {
	message := markdownStringText(invocation.PastTenseMessage)
	if message == "" {
		message = markdownStringText(invocation.InvocationMessage)
	}
	return sanitizeInvocationMessage(message)
}

// joinTextItems joins consecutive text items into rendered markdown. Fragments
// adjacent to an inlineReference concatenate directly (the chip splits a
// sentence), while adjacent plain fragments get a paragraph break — they are
// separate progress notes streamed between tool batches, and butting them
// together mid-sentence ("...report.I'll create...") is unreadable.
func joinTextItems(items []responseBodyItem) string {
	var b strings.Builder
	for i, item := range items {
		if i > 0 && !item.isRef && !items[i-1].isRef {
			// Paragraph break between adjacent plain fragments — unless one of
			// them already carries its own newline at the boundary.
			prev := items[i-1].text
			if !strings.HasSuffix(prev, "\n") && !strings.HasPrefix(item.text, "\n") {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(item.text)
	}
	return b.String()
}

// ExtractTextFromResponseArray reassembles the agent's full rendered text from
// the response array, ignoring tool invocations. VS Code splits the text into
// fragments: plain markdown items interleaved with inlineReference items
// (file/symbol chips rendered inline); taking only the first fragment would
// truncate the message at the first chip.
func ExtractTextFromResponseArray(responses []json.RawMessage) string {
	var textOnly []responseBodyItem
	for _, item := range collectResponseBodyItems(responses) {
		if item.inv == nil && item.synthTool == nil {
			textOnly = append(textOnly, item)
		}
	}
	return joinTextItems(textOnly)
}

// isBareFenceFragment reports whether a markdown fragment is nothing but a code
// fence delimiter (``` with an optional language token) — the shape VS Code uses
// to bracket structured code-block response items. A real fenced code example
// arrives as a single fragment whose content spans multiple lines, so requiring
// the whole trimmed fragment to be the fence line keeps those intact.
func isBareFenceFragment(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return false
	}
	rest := strings.TrimLeft(t, "`")
	return !strings.ContainsAny(rest, " \t\n`")
}

// markdownLinkPattern matches a markdown link [text](target).
var markdownLinkPattern = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)\)`)

// sanitizeInvocationMessage rewrites a VS Code invocation/past-tense message
// for plain-markdown output. VS Code writes file references as markdown links
// whose label its own renderer fills in at display time — on disk the label is
// often empty ("Viewed image [](file:///...)") — and whose vscode-*/command:
// URLs only resolve inside the IDE. Rendered verbatim those show as bare
// "[](file:///…)" text, so each link is replaced with its label (or the link
// target's file name when the label is empty) in backticks.
func sanitizeInvocationMessage(s string) string {
	return markdownLinkPattern.ReplaceAllStringFunc(s, func(link string) string {
		m := markdownLinkPattern.FindStringSubmatch(link)
		label, target := m[1], m[2]
		if label == "" {
			label = linkTargetName(target)
		}
		if label == "" {
			return ""
		}
		return "`" + label + "`"
	})
}

// linkTargetName derives a display name from a link target: the decoded final
// path segment, without scheme, query, or fragment (matching how VS Code
// itself labels bare file links).
func linkTargetName(target string) string {
	if u, err := url.Parse(target); err == nil && u.Path != "" {
		if base := filepath.Base(u.Path); base != "/" && base != "." {
			return base
		}
	}
	// Unparseable target: fall back to the text after the last slash.
	target = strings.TrimSuffix(target, "/")
	if i := strings.LastIndex(target, "/"); i >= 0 {
		target = target[i+1:]
	}
	return target
}

// markdownStringText extracts the text of a VS Code "markdown string" field,
// which is serialized either as a bare string or as an object with a value key.
func markdownStringText(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if m, ok := v.(map[string]any); ok {
		if s, ok := m["value"].(string); ok {
			return s
		}
	}
	return ""
}

// inlineReferenceText renders an inlineReference response item the way it reads in the
// chat: the symbol name when present, otherwise the referenced file's base name, in
// backticks. Returns empty when the reference carries neither (nothing to render).
func inlineReferenceText(name string, uri VSCodeUri) string {
	text := name
	if text == "" {
		path := uri.FSPath
		if path == "" {
			path = uri.Path
		}
		if path == "" {
			return ""
		}
		text = filepath.Base(path)
	}
	return "`" + text + "`"
}

// ExtractFinalAgentMessage gets the final text response from metadata
func ExtractFinalAgentMessage(metadata VSCodeResultMetadata) string {
	// VS Code stores final messages in metadata.messages
	for _, msg := range metadata.Messages {
		if msg.Role == "assistant" {
			return msg.Content
		}
	}
	return ""
}

// toolResultCap bounds how much tool output is pre-rendered into FormattedMarkdown.
// Inputs are not capped — they carry what the agent chose to do (e.g. the full content
// of a written file) — but results (file reads, command output) can be arbitrarily
// large and matter less once the agent has already responded to them.
const toolResultCap = 2000

// FormatToolMarkdown pre-renders a tool call's Input/Output as markdown for
// ToolInfo.FormattedMarkdown. The markdown generator would fall back to an equivalent
// generic rendering on its own, but cross-agent resume would not: the flattener
// (spi.FlattenSessionData) carries only Summary/FormattedMarkdown, so without this the
// tool's payload (e.g. a written file's content) would collapse to a bare tool name in
// the resumed session. Returns empty when the tool has nothing to render.
//
// Tools with a dedicated formatter render through it; everything else gets the
// generic key/value Input plus fenced Result.
func FormatToolMarkdown(tool *schema.ToolInfo) string {
	if custom := formatCustomToolMarkdown(tool); custom != "" {
		return custom
	}

	var b strings.Builder

	// Input as key-value pairs, multiline values fenced (mirrors the generic renderer).
	keys := make([]string, 0, len(tool.Input))
	for key := range tool.Input {
		// Skip internal fields like _cwd (matches the markdown generator).
		if !strings.HasPrefix(key, "_") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		b.WriteString("\n**Input:**\n\n")
		for _, key := range keys {
			valueStr := inputValueString(tool.Input[key])
			if strings.Contains(valueStr, "\n") {
				fence := codeFence(valueStr)
				fmt.Fprintf(&b, "- %s:\n\n%s\n%s\n%s\n\n", key, fence, valueStr, fence)
			} else {
				fmt.Fprintf(&b, "- %s: `%s`\n", key, valueStr)
			}
		}
	}

	b.WriteString(resultSection(tool))

	return b.String()
}

// resultSection renders the fenced **Result:** block from the output map
// (BuildToolInfoFromInvocation stores it under "result"); "" when there is
// none. Trailing blank lines are trimmed — terminal-style results carry dozens
// of them and they only pad out the fenced block — and the result is capped.
func resultSection(tool *schema.ToolInfo) string {
	result, ok := tool.Output["result"].(string)
	if !ok || strings.TrimSpace(result) == "" {
		return ""
	}
	capped := capRunes(strings.TrimRight(result, " \t\n"), toolResultCap)
	fence := codeFence(capped)
	return fmt.Sprintf("\n**Result:**\n\n%s\n%s\n%s\n", fence, capped, fence)
}

// formatCustomToolMarkdown returns tool-specific markdown for tools whose
// generic key/value rendering obscures their meaning. Returns "" to use the
// generic rendering — including when the tool's input lacks the expected
// shape, so a format drift degrades gracefully instead of hiding data.
func formatCustomToolMarkdown(tool *schema.ToolInfo) string {
	switch tool.Name {
	case "manage_todo_list":
		return formatTodoListMarkdown(tool)
	case "run_in_terminal":
		return formatTerminalMarkdown(tool)
	}
	return ""
}

// formatTerminalMarkdown renders a terminal command the way it reads in a
// shell session: the explanation as prose, the command in a bash fence
// (uncapped — it is what the agent chose to run), then the fenced result.
func formatTerminalMarkdown(tool *schema.ToolInfo) string {
	command, _ := tool.Input["command"].(string)
	if command == "" {
		return ""
	}
	var b strings.Builder
	if explanation, _ := tool.Input["explanation"].(string); explanation != "" {
		fmt.Fprintf(&b, "\n%s\n", explanation)
	}
	fence := codeFence(command)
	fmt.Fprintf(&b, "\n%sbash\n%s\n%s\n", fence, command, fence)
	b.WriteString(resultSection(tool))
	return b.String()
}

// formatTodoListMarkdown renders a todo-list write as a markdown checklist.
// The input follows VS Code's manage_todo_list schema: a todoList of entries
// with title and status (not-started / in-progress / completed). Read
// operations carry no todoList and fall back to the generic rendering.
func formatTodoListMarkdown(tool *schema.ToolInfo) string {
	list, _ := tool.Input["todoList"].([]any)
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	rendered := 0
	for _, entry := range list {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		title, _ := item["title"].(string)
		if title == "" {
			continue
		}
		rendered++
		status, _ := item["status"].(string)
		switch status {
		case "completed":
			fmt.Fprintf(&b, "- [x] %s\n", title)
		case "in-progress":
			fmt.Fprintf(&b, "- [ ] %s _(in progress)_\n", title)
		default:
			fmt.Fprintf(&b, "- [ ] %s\n", title)
		}
	}
	if rendered == 0 {
		return ""
	}
	b.WriteString(resultSection(tool))
	return b.String()
}

// inputValueString renders one tool-input value for the markdown Input list.
// Scalars print as-is; maps and slices marshal as JSON — Go's %v formatting
// (map[args:[x] command:echo ...]) is meaningless to readers of the archive.
// Compact JSON for short values, indented (which the caller fences) for long.
func inputValueString(v any) string {
	switch v.(type) {
	case nil, string, bool, float64, int, int64:
		return fmt.Sprintf("%v", v)
	}
	compact, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	if len(compact) <= 80 {
		return string(compact)
	}
	if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
		return string(pretty)
	}
	return string(compact)
}

// codeFence returns a backtick fence long enough to safely wrap s: one backtick more
// than the longest backtick run inside it (a value containing ``` would otherwise
// terminate a plain three-backtick fence early), never shorter than the standard three.
func codeFence(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	size := longest + 1
	if size < 3 {
		size = 3
	}
	return strings.Repeat("`", size)
}

// capRunes truncates s to at most max runes, marking the cut. Rune-based so a cap
// never splits a multi-byte character. Scans rune boundaries instead of converting
// to []rune, which would allocate O(len(s)) for exactly the oversized tool results
// this cap protects against.
func capRunes(s string, max int) string {
	if len(s) <= max {
		return s // fast path: byte length bounds rune length
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i] + "\n… (output truncated)"
		}
		count++
	}
	return s // more bytes than max but fewer runes (multi-byte characters)
}

// GenerateSlug creates a filesystem-safe slug from the composer title, name, or
// first request message, using the spi.GenerateFilenameFromUserMessage convention
// shared by every other provider (word cap, punctuation as separators, markdown
// heading stripping) so slugs stay consistent and bounded across code paths.
func GenerateSlug(composer VSCodeComposer) string {
	if composer.CustomTitle != "" {
		if slug := spi.GenerateFilenameFromUserMessage(composer.CustomTitle); slug != "" {
			return slug
		}
	}
	if composer.Name != "" {
		if slug := spi.GenerateFilenameFromUserMessage(composer.Name); slug != "" {
			return slug
		}
	}

	// Fall back to first request message
	if len(composer.Requests) > 0 {
		if slug := spi.GenerateFilenameFromUserMessage(composer.Requests[0].Message.Text); slug != "" {
			return slug
		}
	}

	return "untitled"
}

// FormatTimestamp converts Unix timestamp (ms) to ISO 8601. A zero or negative
// timestamp means the field was absent from the session file; fall back to the
// current time rather than emitting a misleading 1970 epoch date (which would
// also sort the session before every real one), matching the Cursor IDE provider.
func FormatTimestamp(unixMs int64) string {
	if unixMs <= 0 {
		return time.Now().Format(time.RFC3339)
	}
	t := time.Unix(0, unixMs*int64(time.Millisecond))
	return t.Format(time.RFC3339)
}
