package piagent

import (
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// buildErrorMessage surfaces an assistant entry with stopReason=error and an
// empty content array (no text/thinking/toolCalls) as an agent text message so
// the error event is not dropped from the transcript (and compaction boundaries
// stay interpretable). Returns nil if there is no errorMessage to surface.
func buildErrorMessage(e rawEntry, am assistantMessage) []schema.Message {
	if strings.TrimSpace(am.ErrorMessage) == "" {
		return nil
	}
	return []schema.Message{{
		ID:        e.ID,
		Timestamp: e.Timestamp,
		Role:      schema.RoleAgent,
		Model:     am.Model,
		Content:   []schema.ContentPart{{Type: schema.ContentTypeText, Text: "[error] " + am.ErrorMessage}},
		Usage:     mapUsage(am.Usage),
	}}
}
