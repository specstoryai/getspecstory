package piagent

import (
	"encoding/json"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// buildUserMessage maps a pi user message entry to a schema user Message.
// pi user content is either a plain string or an array of {type:text|image}.
func buildUserMessage(e rawEntry) schema.Message {
	var um userMessage
	_ = json.Unmarshal(e.Message, &um)
	parts := userContentParts(um.Content)
	return schema.Message{
		ID:        e.ID,
		Timestamp: e.Timestamp,
		Role:      schema.RoleUser,
		Content:   parts,
	}
}

// userContentParts decodes a pi user message's content (string or array) into
// schema ContentParts. Image blocks are dropped in v1 (recorded as a gap).
func userContentParts(raw json.RawMessage) []schema.ContentPart {
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return []schema.ContentPart{{Type: schema.ContentTypeText, Text: s}}
		}
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	var parts []schema.ContentPart
	for _, b := range blocks {
		if b.Type == schema.ContentTypeText && b.Text != "" {
			parts = append(parts, schema.ContentPart{Type: schema.ContentTypeText, Text: b.Text})
		}
	}
	return parts
}

// buildAgentMessages maps a pi assistant message entry to one or more schema
// Messages: one Message holds the text+thinking content parts; each toolCall
// block becomes its own agent Message carrying a ToolInfo.
func buildAgentMessages(e rawEntry) []schema.Message {
	var am assistantMessage
	if err := json.Unmarshal(e.Message, &am); err != nil {
		return nil
	}
	var parts []schema.ContentPart
	var messages []schema.Message
	for _, b := range am.Content {
		switch b.Type {
		case schema.ContentTypeText:
			if b.Text != "" {
				parts = append(parts, schema.ContentPart{Type: schema.ContentTypeText, Text: b.Text})
			}
		case schema.ContentTypeThinking:
			if b.Thinking != "" {
				parts = append(parts, schema.ContentPart{Type: schema.ContentTypeThinking, Text: b.Thinking})
			}
		case "toolCall":
			messages = append(messages, buildToolMessage(e, b, am))
		}
	}
	if len(parts) > 0 {
		head := schema.Message{
			ID:        e.ID,
			Timestamp: e.Timestamp,
			Role:      schema.RoleAgent,
			Model:     am.Model,
			Content:   parts,
			Usage:     mapUsage(am.Usage),
		}
		return append([]schema.Message{head}, messages...)
	}
	if len(messages) == 0 {
		return buildErrorMessage(e, am)
	}
	// Tool-call-only assistant message: carry model+usage once on the first
	// tool message so no metadata is lost and no schema-invalid empty message
	// is emitted.
	messages[0].Model = am.Model
	if messages[0].Usage == nil {
		messages[0].Usage = mapUsage(am.Usage)
	}
	return messages
}

// buildToolMessage builds an agent Message wrapping a ToolInfo from a toolCall.
// The parent assistant message's model is passed through so tool messages carry
// the same model metadata as text/thinking messages. The Message ID is derived
// from the parent entry id + the toolCall id, so multiple toolCalls in one
// assistant message get distinct Message IDs (downstream provenance keys use
// msg.ID as a deterministic component).
func buildToolMessage(e rawEntry, b contentBlock, am assistantMessage) schema.Message {
	return schema.Message{
		ID:        toolMessageID(e.ID, b.ID),
		Timestamp: e.Timestamp,
		Role:      schema.RoleAgent,
		Model:     am.Model,
		Tool: &schema.ToolInfo{
			Name:  b.Name,
			Type:  classifyToolType(b.Name),
			UseID: b.ID,
			Input: b.Args,
		},
	}
}

// toolMessageID builds a unique Message ID for a tool-call message from the
// parent entry id and the toolCall id. This avoids duplicate Message IDs when a
// single assistant entry contains multiple toolCall blocks.
func toolMessageID(entryID, callID string) string {
	if callID != "" {
		return entryID + ":" + callID
	}
	return entryID
}

// buildToolOutput constructs the ToolInfo.Output map from a toolResult's
// content, error flag, and optional details blob. details (e.g. exitCode) is
// decoded into a generic value so downstream consumers (markdown rendering,
// cloud) can surface structured tool metadata without re-parsing raw JSON.
func buildToolOutput(content string, tr toolResultMessage) map[string]any {
	out := map[string]any{"content": content, "is_error": tr.IsError}
	if len(tr.Details) > 0 {
		var details any
		if err := json.Unmarshal(tr.Details, &details); err == nil {
			out["details"] = details
		}
	}
	return out
}

// mapUsage converts a pi usage object to the schema Usage. pi's input/output map
// to InputTokens/OutputTokens; cacheRead/cacheWrite map to the Claude-style
// cache fields (pi uses the same semantics).
func mapUsage(u *piUsage) *schema.Usage {
	if u == nil {
		return nil
	}
	return &schema.Usage{
		InputTokens:              int(u.Input),
		OutputTokens:             int(u.Output),
		CacheReadInputTokens:     int(u.CacheRead),
		CacheCreationInputTokens: int(u.CacheWrite),
	}
}

// deriveSlug returns a filename-safe slug from the first user message text.
// The trimmed text is passed to GenerateFilenameFromUserMessage so slugs do not
// differ only by leading/trailing whitespace.
func deriveSlug(data *schema.SessionData) string {
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Role != schema.RoleUser {
				continue
			}
			for _, part := range msg.Content {
				if t := strings.TrimSpace(part.Text); t != "" {
					return spi.GenerateFilenameFromUserMessage(t)
				}
			}
		}
	}
	return ""
}

// classifyToolType maps pi tool names to the schema tool-type taxonomy.
func classifyToolType(name string) string {
	switch strings.ToLower(name) {
	case "read":
		return schema.ToolTypeRead
	case "edit", "write":
		return schema.ToolTypeWrite
	case "bash", "ls":
		return schema.ToolTypeShell
	case "grep", "find", "web_search", "fetch_content":
		return schema.ToolTypeSearch
	case "until_done_set", "until_done_plan", "until_done_task_update",
		"until_done_progress", "until_done_complete", "until_done_block",
		"until_done_replan", "until_done_distill":
		return schema.ToolTypeTask
	default:
		return schema.ToolTypeUnknown
	}
}
