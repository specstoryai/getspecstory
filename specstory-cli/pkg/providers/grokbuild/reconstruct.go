package grokbuild

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// grokTimeFormat matches the microsecond precision Grok writes in summary.json.
const grokTimeFormat = "2006-01-02T15:04:05.000000Z"

// ReconstructSession rebuilds a Grok Build session from the neutral SessionData
// so `grok --resume <id>` can carry the conversation on.
//
// Grok keys a session by directory, and a directory needs two files to load:
// chat_history.jsonl for the conversation and summary.json for the identity.
// Both were verified by hand: with only chat_history.jsonl grok reports the
// session as not found locally, and with both it answers questions about the
// synthesized conversation. This function produces the transcript;
// NativeSessionPath writes the summary alongside it.
//
// Only user and assistant text is emitted. Tool calls and thinking are already
// flattened into agent text by FlattenSessionData, and a tool_call without its
// matching tool_result would leave a dangling call in grok's history. Reasoning
// records are omitted too: their encrypted_content is bound to the model that
// produced it, and grok rejects a session whose reasoning does not match.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}

	newID := uuid.NewString()

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)

	// Grok's system prompt tells the model the request is inside <user_query>,
	// and our own parser uses the same tag to tell a real turn from injected
	// context, so the wrapper keeps the transcript readable in both directions.
	system := map[string]any{
		"type":    "system",
		"content": "You are Grok 4.6 released by xAI. You are an interactive CLI tool that helps users with software engineering tasks.",
	}
	if err := encoder.Encode(system); err != nil {
		return nil, fmt.Errorf("failed to encode the system record: %w", err)
	}

	promptIndex := 0
	for _, turn := range turns {
		var record map[string]any
		if turn.Role == schema.RoleUser {
			record = map[string]any{
				"type": "user",
				"content": []map[string]any{{
					"type": "text",
					"text": fmt.Sprintf("<user_query>\n%s\n</user_query>", turn.Text),
				}},
				"prompt_index": promptIndex,
			}
			promptIndex++
		} else {
			record = map[string]any{
				"type":     "assistant",
				"content":  turn.Text,
				"model_id": "grok-4.6",
			}
		}
		if err := encoder.Encode(record); err != nil {
			return nil, fmt.Errorf("failed to encode a conversation record: %w", err)
		}
	}

	// The filename carries the session directory because Grok keys a session by
	// directory rather than by file, matching the musecode precedent.
	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  filepath.Join(newID, chatHistoryFile),
		Content:   buf.Bytes(),
	}, nil
}

// NativeSessionPath resolves where a reconstructed transcript belongs and
// prepares the session directory around it.
//
// Preparing means writing summary.json, without which grok does not recognize
// the directory as a session at all. This mirrors the Gemini provider, whose
// NativeSessionPath writes the .project_root marker that makes its target
// directory usable. The summary is deliberately generic: the SPI hands this
// function only a filename, and grok accepts a summary with zero message counts
// and no title, which was verified by resuming a session written this way.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return "", err
	}

	groupDir, err := ResolveGrokProjectDir(projectPath)
	if err != nil {
		// Grok has never run in this project, so name the group directory the
		// way grok itself would.
		sessionsDir, dirErr := GetGrokSessionsDir()
		if dirErr != nil {
			return "", dirErr
		}
		groupDir = filepath.Join(sessionsDir, EncodeCwdDirname(projectPath))
	}

	sessionDir := filepath.Join(groupDir, filepath.Dir(filename))
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create the Grok session directory: %w", err)
	}

	if err := writeSessionSummary(sessionDir, projectPath); err != nil {
		return "", err
	}

	return filepath.Join(groupDir, filename), nil
}

// writeSessionSummary writes the metadata file that makes a directory a session
// grok will load. It is left alone if it already exists, so re-resolving a path
// never clobbers a real session's metadata.
func writeSessionSummary(sessionDir, projectPath string) error {
	path := filepath.Join(sessionDir, summaryFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	}

	now := time.Now().UTC().Format(grokTimeFormat)
	summary := map[string]any{
		"info": map[string]any{
			"id":  filepath.Base(sessionDir),
			"cwd": projectPath,
		},
		"session_summary":     spi.ResumedSessionTitle(""),
		"created_at":          now,
		"updated_at":          now,
		"num_messages":        0,
		"current_model_id":    "grok-4.6",
		"chat_format_version": 1,
	}

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode the Grok session summary: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("failed to write the Grok session summary: %w", err)
	}
	return nil
}

// SupportsReconstruction reports true: a reconstructed session directory was
// verified to resume in Grok Build 1.0.3.
func (p *Provider) SupportsReconstruction() bool {
	return true
}

// EncodeCwdDirname names the group directory for a working directory the way
// Grok does, percent-encoding every byte outside the RFC 3986 unreserved set
// with uppercase hex.
//
// Only reconstruction needs this. Discovery decodes directory names instead, so
// an encoder drift can never hide existing sessions; the worst case here is a
// new directory grok would spell differently.
func EncodeCwdDirname(cwd string) string {
	const unreserved = "-._~"

	var b strings.Builder
	for i := 0; i < len(cwd); i++ {
		c := cwd[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteByte(c)
		case strings.IndexByte(unreserved, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
