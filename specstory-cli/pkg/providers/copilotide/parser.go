package copilotide

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

// ExtractThinkingFromMetadata extracts thinking text from tool call rounds
// Should only be called when there are actual tool calls
func ExtractThinkingFromMetadata(metadata VSCodeResultMetadata) string {
	var thinking strings.Builder

	for _, round := range metadata.ToolCallRounds {
		// Only include response if there are tool calls in this round
		if len(round.ToolCalls) > 0 && round.Response != "" {
			thinking.WriteString(round.Response)
			thinking.WriteString("\n\n")
		}
	}

	result := strings.TrimSpace(thinking.String())
	if result == "" {
		return ""
	}

	return result
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

// ExtractTextFromResponseArray reassembles the agent's rendered text from the response
// array. VS Code splits the text into fragments: plain markdown items (objects with a
// "value" field and no "kind") interleaved with inlineReference items (file/symbol
// chips rendered inline). Taking only the first markdown fragment would truncate the
// message at the first chip, so all fragments are joined in order and each inline
// reference is rendered as its name.
func ExtractTextFromResponseArray(responses []json.RawMessage) string {
	var b strings.Builder
	for _, rawResp := range responses {
		var item struct {
			Kind            string    `json:"kind"`
			Value           string    `json:"value"`
			Name            string    `json:"name"` // symbol references carry a name
			InlineReference VSCodeUri `json:"inlineReference"`
		}
		if err := json.Unmarshal(rawResp, &item); err != nil {
			continue
		}
		switch {
		case item.Kind == "" && item.Value != "":
			b.WriteString(item.Value)
		case item.Kind == "inlineReference":
			b.WriteString(inlineReferenceText(item.Name, item.InlineReference))
		}
	}
	return b.String()
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
			valueStr := fmt.Sprintf("%v", tool.Input[key])
			if strings.Contains(valueStr, "\n") {
				fence := codeFence(valueStr)
				fmt.Fprintf(&b, "- %s:\n\n%s\n%s\n%s\n\n", key, fence, valueStr, fence)
			} else {
				fmt.Fprintf(&b, "- %s: `%s`\n", key, valueStr)
			}
		}
	}

	// Result from the output map (BuildToolInfoFromInvocation stores it under "result").
	if result, ok := tool.Output["result"].(string); ok && strings.TrimSpace(result) != "" {
		capped := capRunes(result, toolResultCap)
		fence := codeFence(capped)
		fmt.Fprintf(&b, "\n**Result:**\n\n%s\n%s\n%s\n", fence, capped, fence)
	}

	return b.String()
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
