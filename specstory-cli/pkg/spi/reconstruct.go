package spi

import (
	"errors"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// ErrReconstructionUnsupported is returned by a provider that carries the
// ReconstructSession responsibility but does not yet have a native serializer.
// Reconstruction is on the Provider interface so every provider implements the
// method, but only some providers (initially Claude Code and Codex CLI) produce
// real native output.
var ErrReconstructionUnsupported = errors.New("session reconstruction not supported by this provider")

// ReconstructOptions controls how a session is reconstructed into native form.
type ReconstructOptions struct {
	// WorkspaceRoot is the target working directory the reconstructed session
	// should belong to. Native paths/IDs are derived from it. When empty, the
	// provider falls back to the SessionData's own WorkspaceRoot.
	WorkspaceRoot string

	// MigrationNote, when set, is prepended as a leading agent turn so the agent
	// understands the prior turns were imported from another session. Optional.
	MigrationNote string
}

// ReconstructedSession is the native output of reconstruction. It is data only;
// the caller decides where (and whether) to write it.
type ReconstructedSession struct {
	SessionID string // freshly minted, native-format session ID
	Filename  string // suggested native filename (base name, not a full path)
	Content   []byte // native session file bytes
}

// Turn is a single role-tagged text turn in the flattened transcript. The
// reconstruction model collapses everything an agent said, thought, or did into
// plain user/agent text turns (see docs/SESSION-PORTABILITY.md), which each
// provider then serializes into its native format.
type Turn struct {
	Role string // schema.RoleUser or schema.RoleAgent
	Text string
}

// FlattenSessionData reduces a SessionData into an ordered list of plain
// user/agent text turns. This is the single place the cross-provider flattening
// policy lives, so every provider's serializer consumes the same transcript.
//
// Rules (per the locked design):
//   - user/agent text content -> a turn of that role
//   - thinking content -> folded into the agent turn's text (not dropped)
//   - tool calls -> folded into the agent turn's text via the pre-rendered
//     Summary / FormattedMarkdown captured on the forward pass
//   - model, usage, and path hints -> dropped
//
// migrationNote, when non-empty, becomes a leading agent turn.
func FlattenSessionData(data *schema.SessionData, migrationNote string) []Turn {
	var turns []Turn

	if strings.TrimSpace(migrationNote) != "" {
		turns = append(turns, Turn{Role: schema.RoleAgent, Text: strings.TrimSpace(migrationNote)})
	}

	if data == nil {
		return turns
	}

	for i := range data.Exchanges {
		for j := range data.Exchanges[i].Messages {
			msg := &data.Exchanges[i].Messages[j]

			// Only user and agent roles produce turns; anything else is skipped.
			if msg.Role != schema.RoleUser && msg.Role != schema.RoleAgent {
				continue
			}

			text := messageTurnText(msg)
			if strings.TrimSpace(text) == "" {
				continue
			}

			// Drop synthetic, non-conversational turns (slash-command invocations,
			// local-command output, etc.) so they are not replayed into the resumed
			// session. This is intentionally reconstruction-only — archival markdown
			// stays faithful to the source session.
			if isSyntheticTurnText(text) {
				continue
			}

			turns = append(turns, Turn{Role: msg.Role, Text: text})
		}
	}

	return turns
}

// syntheticTurnMarkers are wrapper tags agents inject for non-conversational
// records — slash-command invocations, their stdout, and guard/caveat notes (e.g.
// Claude Code's <command-name>/<local-command-*> and <TEXTBLOCK> title-generation
// prompts). These are noise — and sometimes actively misleading — when replayed
// into a resumed session, so reconstruction drops any turn whose text contains
// one. Markdown generation does NOT use this filter and stays faithful to source.
var syntheticTurnMarkers = []string{
	"<command-name>",
	"<command-message>",
	"<command-args>",
	"<local-command-stdout>",
	"<local-command-caveat>",
	"<TEXTBLOCK>",
}

// isSyntheticTurnText reports whether a flattened turn is a synthetic,
// non-conversational artifact that should not be replayed into a resumed session.
func isSyntheticTurnText(text string) bool {
	for _, marker := range syntheticTurnMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// messageTurnText flattens a single message into its turn text: all text/thinking
// content parts joined, followed by the tool's pre-rendered markdown when present.
func messageTurnText(msg *schema.Message) string {
	var parts []string

	for _, part := range msg.Content {
		// Both "text" and "thinking" parts carry their content in Text; thinking
		// is intentionally treated as ordinary agent text.
		if strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}

	text := strings.Join(parts, "\n\n")

	if msg.Tool != nil {
		toolText := toolTurnText(msg.Tool)
		if toolText != "" {
			if text != "" {
				text += "\n\n" + toolText
			} else {
				text = toolText
			}
		}
	}

	return text
}

// toolTurnText renders a tool call as agent text, reusing the markdown the
// forward pass already produced (Summary + FormattedMarkdown). Falls back to a
// minimal description when neither is available.
func toolTurnText(tool *schema.ToolInfo) string {
	var b strings.Builder

	if tool.Summary != nil && strings.TrimSpace(*tool.Summary) != "" {
		b.WriteString(strings.TrimSpace(*tool.Summary))
	}

	if tool.FormattedMarkdown != nil && strings.TrimSpace(*tool.FormattedMarkdown) != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.TrimRight(*tool.FormattedMarkdown, "\n"))
	}

	if b.Len() == 0 {
		// Nothing pre-rendered; emit a minimal, non-empty description so the tool
		// activity is at least acknowledged in the transcript.
		b.WriteString("Tool use: " + tool.Name)
	}

	return b.String()
}
