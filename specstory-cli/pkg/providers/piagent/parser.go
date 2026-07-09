package piagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// ParseSession reads a pi JSONL v3 session file and maps its current leaf-path
// branch into the unified schema.SessionData. It honors compaction entries:
// when a compaction entry is on the leaf path, entries before firstKeptEntryId
// are dropped from the conversation (matching pi's buildContextEntries).
func ParseSession(path string) (*schema.SessionData, error) {
	header, entries, err := readEntries(path)
	if err != nil {
		return nil, err
	}
	if header == nil {
		return nil, fmt.Errorf("pi: no session header in %s", path)
	}
	if header.Type != entrySession {
		return nil, fmt.Errorf("pi: %s is not a pi session (header type %q)", path, header.Type)
	}
	if header.ID == "" {
		return nil, fmt.Errorf("pi: session header in %s has no id", path)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("pi: session %s has no entries", header.ID)
	}
	ordered := leafPathEntries(entries)
	return buildSessionData(header, ordered), nil
}

// readEntries parses every line of the session file into a header (line 1) and
// a list of message/control entries (the rest). Malformed lines are skipped
// rather than aborting the whole parse.
func readEntries(path string) (*sessionHeader, []rawEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("pi: opening session %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var header *sessionHeader
	var entries []rawEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if header == nil {
			h := sessionHeader{}
			if err := json.Unmarshal([]byte(line), &h); err != nil {
				return nil, nil, fmt.Errorf("pi: parsing session header: %w", err)
			}
			header = &h
			continue
		}
		var e rawEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		// Skip malformed entries missing the envelope fields the tree walk and
		// exchange grouping rely on; a stray entry with no id can mislead leaf
		// selection (the last file-order entry is treated as the leaf).
		if e.Type == "" || e.ID == "" {
			slog.Debug("pi: skipping entry with empty type or id", "file", path)
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("pi: reading session %s: %w", path, err)
	}
	return header, entries, nil
}

// piProviderVersion derives the provider version string recorded on SessionData
// from the pi session header's format version (e.g. v3). Always non-empty so
// schema.Validate() does not warn about a missing provider.version.
func piProviderVersion(header *sessionHeader) string {
	if header.Version > 0 {
		return fmt.Sprintf("v%d", header.Version)
	}
	return "v1"
}

// buildSessionData maps the ordered leaf-path entries into schema.SessionData.
func buildSessionData(header *sessionHeader, ordered []rawEntry) *schema.SessionData {
	return &schema.SessionData{
		SchemaVersion: "1.0",
		Provider: schema.ProviderInfo{
			ID:      providerID,
			Name:    providerName,
			Version: piProviderVersion(header),
		},
		SessionID:     header.ID,
		CreatedAt:     header.Timestamp,
		WorkspaceRoot: header.Cwd,
		Exchanges:     buildExchanges(ordered),
	}
}

// buildExchanges groups ordered entries into schema exchanges. A new user
// message starts a new exchange; assistant messages append to the current
// exchange; toolResults merge into the matching ToolInfo. Control entries
// (model_change, custom, etc.) are skipped from the conversation body.
func buildExchanges(ordered []rawEntry) []schema.Exchange {
	var exchanges []schema.Exchange
	var current *schema.Exchange
	commit := func() {
		if current != nil && len(current.Messages) > 0 {
			exchanges = append(exchanges, *current)
		}
	}
	for _, e := range ordered {
		if e.Type != entryMessage {
			continue
		}
		switch messageRole(e) {
		case roleUser:
			commit()
			current = &schema.Exchange{
				ExchangeID: e.ID,
				StartTime:  e.Timestamp,
				Messages:   []schema.Message{buildUserMessage(e)},
			}
		case roleAssistant:
			current = appendAssistant(current, e)
		case roleToolResult:
			mergeToolResult(current, e)
		}
	}
	commit()
	return exchanges
}

// appendAssistant appends an assistant message to the current exchange, creating
// one if none exists yet.
func appendAssistant(current *schema.Exchange, e rawEntry) *schema.Exchange {
	if current == nil {
		current = &schema.Exchange{ExchangeID: e.ID, StartTime: e.Timestamp}
	}
	current.Messages = append(current.Messages, buildAgentMessages(e)...)
	current.EndTime = e.Timestamp
	return current
}

// messageRole extracts .message.role from a message entry.
func messageRole(e rawEntry) string {
	var m struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(e.Message, &m)
	return m.Role
}

// mergeToolResult folds a toolResult entry into the matching agent ToolInfo in
// the current exchange, keyed by toolCallId == ToolInfo.UseID. It also advances
// the exchange EndTime to the toolResult's timestamp so downstream stats that
// read the last exchange's EndTime report the real final-event time.
func mergeToolResult(current *schema.Exchange, e rawEntry) {
	if current == nil {
		return
	}
	var tr toolResultMessage
	if err := json.Unmarshal(e.Message, &tr); err != nil {
		return
	}
	content := toolResultContent(tr)
	for i := range current.Messages {
		msg := &current.Messages[i]
		if msg.Tool != nil && msg.Tool.UseID == tr.ToolCallID {
			msg.Tool.Output = buildToolOutput(content, tr)
			msg.Timestamp = e.Timestamp
			current.EndTime = e.Timestamp
			return
		}
	}
}

// toolResultContent joins the text blocks of a toolResult message.
func toolResultContent(tr toolResultMessage) string {
	var parts []string
	for _, b := range tr.Content {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
