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

// BuildToolCallMap creates a lookup map from toolCallId to tool call info
// NOTE: This is kept for backward compatibility but sequence-based matching is preferred
func BuildToolCallMap(metadata VSCodeResultMetadata) map[string]VSCodeToolCallInfo {
	toolCalls := make(map[string]VSCodeToolCallInfo)

	for _, round := range metadata.ToolCallRounds {
		for _, call := range round.ToolCalls {
			toolCalls[call.ID] = call
		}
	}

	return toolCalls
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

// responseBodyItem is one ordered element of a turn's rendered body: either a
// text fragment (plain markdown or an inline reference chip, text set) or a
// tool invocation (inv set).
type responseBodyItem struct {
	text  string
	isRef bool
	inv   *VSCodeToolInvocationResponse
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

		// Structured kinds with nothing to render as text yet (phase 2).
		case "textEditGroup":
			slog.Debug("Skipping textEditGroup response (phase 2)", "kind", kind)
		case "codeblockUri":
			slog.Debug("Skipping codeblockUri response (phase 2)", "kind", kind)
		case "confirmation":
			slog.Debug("Skipping confirmation response (phase 2)", "kind", kind)
		case "thinking", "undoStop", "mcpServersStarting", "notebookEditGroup":
			// Known kinds with nothing to render: opaque thinking blobs, undo
			// markers, MCP startup notices, and notebook edit groups (phase 2).
		default:
			slog.Debug("Unknown response kind", "kind", kind)
		}
	}

	return items
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
		if item.inv == nil {
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
func FormatToolMarkdown(tool *schema.ToolInfo) string {
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

	// Result from the output map (BuildToolInfoFromInvocation stores it under
	// "result"). Trailing blank lines are trimmed — terminal-style results carry
	// dozens of them and they only pad out the fenced block.
	if result, ok := tool.Output["result"].(string); ok && strings.TrimSpace(result) != "" {
		capped := capRunes(strings.TrimRight(result, " \t\n"), toolResultCap)
		fence := codeFence(capped)
		fmt.Fprintf(&b, "\n**Result:**\n\n%s\n%s\n%s\n", fence, capped, fence)
	}

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
