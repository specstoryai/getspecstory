package geminicli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// geminiTimestamp formats a time the way Gemini chat records do (RFC3339 millis, Z).
func geminiTimestamp(t time.Time) string {
	return t.Format("2006-01-02T15:04:05.000Z")
}

// ReconstructSession rebuilds a Gemini CLI native chat session from the neutral
// SessionData so `gemini --resume <id>` can continue the conversation.
//
// Gemini stores a session as a single JSON document: a top-level object with
// `sessionId`/`projectHash`/`startTime`/`lastUpdated`/`kind` and a `messages`
// array. Each message is `{id, timestamp, type, content}` where a user message's
// content is `[{text}]` and a gemini message's content is a plain string. Tool
// calls and thinking are already flattened into agent text by FlattenSessionData,
// so reconstruction emits only `user`/`gemini` text messages. See
// docs/SESSION-PORTABILITY.md.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}
	cwd := spi.ResolveWorkspaceRoot(opts, data)

	newID := uuid.NewString()
	base := time.Now().UTC()

	// Best-effort project hash; Gemini stores it but resume keys off the project dir.
	projectHash, _ := HashProjectPath(cwd)

	messages := make([]map[string]interface{}, 0, len(turns))
	for i, turn := range turns {
		msg := map[string]interface{}{
			"id":        uuid.NewString(),
			"timestamp": geminiTimestamp(base.Add(time.Duration(i) * time.Second)),
		}
		if turn.Role == schema.RoleUser {
			msg["type"] = "user"
			msg["content"] = []map[string]interface{}{{"text": turn.Text}}
		} else {
			msg["type"] = "gemini"
			msg["content"] = turn.Text
		}
		messages = append(messages, msg)
	}

	session := map[string]interface{}{
		"sessionId":   newID,
		"projectHash": projectHash,
		"startTime":   geminiTimestamp(base),
		"lastUpdated": geminiTimestamp(base.Add(time.Duration(len(turns)) * time.Second)),
		"kind":        "main",
		"messages":    messages,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(session); err != nil {
		return nil, fmt.Errorf("failed to encode gemini session: %w", err)
	}

	// Filename: session-<YYYY-MM-DDTHH-MM>-<first 8 of id>.json (Gemini convention).
	filename := fmt.Sprintf("session-%s-%s.json", base.Format("2006-01-02T15-04"), newID[:8])

	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  filename,
		Content:   buf.Bytes(),
	}, nil
}

// NativeSessionPath returns where a reconstructed chat belongs in Gemini's store
// and, unlike the Claude/Codex resolvers, prepares the project directory: Gemini
// associates a tmp dir with a project via a `.project_root` marker, so for a
// project Gemini has never seen we create `~/.gemini/tmp/<name>/` with that marker
// and a `chats/` subdir. Returns `<projectDir>/chats/<filename>`.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	if projectPath == "" {
		var err error
		projectPath, err = osGetwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %w", err)
		}
	}

	// Reuse the project dir Gemini already associates with this project, if any.
	dir, err := ResolveGeminiProjectDir(projectPath)
	if err != nil {
		dir, err = prepareGeminiProjectDir(projectPath)
		if err != nil {
			return "", err
		}
	}

	return filepath.Join(dir, "chats", filename), nil
}

// prepareGeminiProjectDir creates a Gemini project directory for a project that
// Gemini has not seen yet: ~/.gemini/tmp/<basename>/ (falling back to the hash dir
// if that basename is taken by a different project), with a `.project_root` marker
// and a `chats/` subdirectory.
func prepareGeminiProjectDir(projectPath string) (string, error) {
	tmpDir, err := GetGeminiTmpDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create gemini tmp dir: %w", err)
	}

	canonical := canonicalizeProjectPath(projectPath)
	dir := filepath.Join(tmpDir, filepath.Base(projectPath))

	// If the basename dir exists but belongs to a different project, key off the hash instead.
	if info, statErr := osStat(dir); statErr == nil && info.IsDir() && !matchesProjectRoot(dir, canonical) {
		if dir, err = GetGeminiProjectDir(projectPath); err != nil {
			return "", err
		}
	}

	if err := os.MkdirAll(filepath.Join(dir, "chats"), 0o755); err != nil {
		return "", fmt.Errorf("failed to create gemini chats dir: %w", err)
	}

	// Write the project-root marker so Gemini maps this dir to the project on resume.
	rootFile := filepath.Join(dir, ".project_root")
	if _, statErr := osStat(rootFile); os.IsNotExist(statErr) {
		if err := os.WriteFile(rootFile, []byte(canonical), 0o644); err != nil {
			return "", fmt.Errorf("failed to write gemini .project_root: %w", err)
		}
	}

	return dir, nil
}
