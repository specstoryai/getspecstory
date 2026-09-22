package grokbuild

import (
	"bytes"
	"encoding/json"
	"errors"
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
// A new imported session needs chat_history.jsonl for its conversation and
// summary.json for identity. Native sessions can also recover a missing
// transcript from update history, which an imported session does not have.
// This function produces the transcript; NativeSessionPath writes its summary.
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

	// The native loader supplies its current system prompt and accepts imported
	// assistant text without historical model metadata.

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
				"prompt_index":             promptIndex,
				"specstorySourceSessionId": data.SessionID,
			}
			promptIndex++
		} else {
			record = map[string]any{
				"type":    "assistant",
				"content": turn.Text,
			}
		}
		if err := encoder.Encode(record); err != nil {
			return nil, fmt.Errorf("failed to encode a conversation record: %w", err)
		}
	}

	// The filename carries the directory because Grok keys sessions by directory.
	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  filepath.Join(newID, chatHistoryFile),
		Content:   buf.Bytes(),
	}, nil
}

// NativeSessionPath resolves where a reconstructed transcript belongs and
// prepares the session directory around it.
//
// Preparing means writing summary.json, without which Grok does not recognize
// the directory as a session. The summary is deliberately generic: the SPI hands this
// function only a filename, and grok accepts a summary with zero message counts
// and no title, which was verified by resuming a session written this way.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	if filepath.Base(filename) != chatHistoryFile || !uuidLike.MatchString(filepath.Dir(filename)) {
		return "", fmt.Errorf("invalid Grok reconstructed filename %q", filename)
	}
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return "", err
	}

	projectPath = spi.CanonicalizePathOrClean(projectPath)
	groupDir, err := ResolveGrokProjectDir(projectPath)
	if err != nil {
		var missing *GrokPathError
		if !errors.As(err, &missing) {
			return "", err
		}
		// Grok has never run in this project, so name the group directory the
		// way grok itself would.
		sessionsDir, dirErr := GetGrokSessionsDir()
		if dirErr != nil {
			return "", dirErr
		}
		groupDir = filepath.Join(sessionsDir, EncodeCwdDirname(projectPath))
	}

	sessionDir := filepath.Join(groupDir, filepath.Dir(filename))
	if err := os.MkdirAll(filepath.Dir(groupDir), 0o700); err != nil {
		return "", fmt.Errorf("failed to create the Grok store directory: %w", err)
	}
	for _, dir := range []string{groupDir, sessionDir} {
		if err := ensureNativeDirectory(dir); err != nil {
			return "", err
		}
	}

	if err := writeSessionSummary(sessionDir, projectPath); err != nil {
		return "", err
	}

	return filepath.Join(groupDir, filename), nil
}

// ensureNativeDirectory accepts only a real group or session directory.
func ensureNativeDirectory(dir string) error {
	// Mkdir does not follow an existing final-component link. If another
	// creator wins, inspect its entry with Lstat before accepting it; MkdirAll
	// would silently accept a symlink to a directory here.
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("failed to create Grok directory %q: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("failed to inspect Grok directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("grok path %q is not a directory", dir)
	}
	return nil
}

// writeSessionSummary writes the metadata file that makes a directory a session
// grok will load. It is left alone if it already exists, so re-resolving a path
// never clobbers a real session's metadata.
func writeSessionSummary(sessionDir, projectPath string) error {
	path := filepath.Join(sessionDir, summaryFile)
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
		"current_model_id":    "",
		"chat_format_version": 1,
	}

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode the Grok session summary: %w", err)
	}
	// An agent or another reconstruction may create native metadata at any
	// time. Exclusive creation preserves it without a check-then-write race.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		// Preserve existing metadata (including a user's link to a regular
		// file), but do not advertise a directory or missing link target as a
		// usable native summary. This check never writes through the entry.
		info, statErr := os.Stat(path)
		if statErr != nil {
			return fmt.Errorf("failed to inspect existing Grok summary: %w", statErr)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("existing Grok summary %q is not a regular file", path)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to create the Grok session summary: %w", err)
	}
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("failed to write the Grok session summary: %w", err)
	}
	return nil
}

// SupportsReconstruction reports true: a reconstructed session directory was
// verified to resume in Grok Build 1.0.34.
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
