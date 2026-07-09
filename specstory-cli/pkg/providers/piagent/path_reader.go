package piagent

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// GetAgentChatSessionByPath parses a single pi session directly from its native
// file path, skipping the by-id discovery search. originCwd is the session's
// originating working directory (GlobalSessionRef.OriginCwd), passed through
// as the workspace root for path normalization — matching what
// GetAgentChatSession receives as projectPath. Implements spi.PathSessionReader
// so `specstory reindex` uses the O(N) path-keyed fast path instead of the
// O(N²) by-id lookup.
func (p *Provider) GetAgentChatSessionByPath(nativePath, originCwd string, debugRaw bool) (*spi.AgentChatSession, error) {
	data, err := ParseSession(nativePath)
	if err != nil {
		return nil, err
	}
	if data.WorkspaceRoot == "" && originCwd != "" {
		data.WorkspaceRoot = originCwd
	}
	if debugRaw {
		if dErr := writeDebugRaw(nativePath, data); dErr != nil {
			slog.Warn("pi: debug-raw write failed", "error", dErr)
		}
	}
	raw, err := os.ReadFile(nativePath)
	if err != nil {
		return nil, fmt.Errorf("pi: reading raw session: %w", err)
	}
	return &spi.AgentChatSession{
		SessionID:   data.SessionID,
		CreatedAt:   data.CreatedAt,
		Slug:        deriveSlug(data),
		SessionData: data,
		RawData:     string(raw),
	}, nil
}
