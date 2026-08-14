package droidcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// ReconstructSession rebuilds a Factory Droid native JSONL session from the neutral
// SessionData so `droid --resume <id>` can continue the conversation.
//
// Droid stores a session as a `session_start` header followed by `parentId`-chained
// `message` records, each `{message:{role, content:[{type:"text",text}]}}`. Tool
// calls and thinking are already flattened into agent text by FlattenSessionData,
// so reconstruction emits only `user`/`assistant` text messages. The store is
// project-scoped (see NativeSessionPath). See docs/SESSION-PORTABILITY.md.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	turns, err := spi.PrepareTurns(data, opts)
	if err != nil {
		return nil, err
	}
	cwd := spi.ResolveWorkspaceRoot(opts, data)

	newID := uuid.NewString()
	base := time.Now().UTC()

	title := spi.ResumedSessionTitle(data.Slug)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	// session_start header (also carries provenance).
	if err := enc.Encode(map[string]interface{}{
		"type":                      "session_start",
		"id":                        newID,
		"title":                     title,
		"sessionTitle":              title,
		"owner":                     currentUsername(),
		"version":                   2,
		"cwd":                       cwd,
		"isSessionTitleManuallySet": false,
		"sessionTitleAutoStage":     "first_message",
		"specstorySourceSessionId":  data.SessionID,
	}); err != nil {
		return nil, fmt.Errorf("failed to encode session_start: %w", err)
	}

	var prevID string
	for i, turn := range turns {
		msgID := uuid.NewString()
		rec := map[string]interface{}{
			"type":      "message",
			"id":        msgID,
			"timestamp": spi.RFC3339Millis(base.Add(time.Duration(i) * time.Second)),
			"message": map[string]interface{}{
				"role":    spi.ReconstructRole(turn.Role),
				"content": []map[string]interface{}{{"type": "text", "text": turn.Text}},
			},
		}
		// Linear chain: the first message roots the thread (no parentId).
		if prevID != "" {
			rec["parentId"] = prevID
		}

		if err := enc.Encode(rec); err != nil {
			return nil, fmt.Errorf("failed to encode message: %w", err)
		}
		prevID = msgID
	}

	return &spi.ReconstructedSession{
		SessionID: newID,
		Filename:  newID + ".jsonl",
		Content:   buf.Bytes(),
	}, nil
}

// NativeSessionPath returns where a reconstructed session belongs in Droid's store:
// ~/.factory/sessions/<cwd-encoded>/<filename>. The directory is not required to
// exist (the caller creates it).
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	if projectPath == "" {
		var err error
		projectPath, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %w", err)
		}
	}

	dir, err := resolveProjectSessionDir(projectPath)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", fmt.Errorf("droidcli: cannot resolve session directory for project path")
	}
	return filepath.Join(dir, filename), nil
}

// currentUsername returns the login name for the session_start owner field.
// os/user is the portable source of truth; on Windows it reports "DOMAIN\name",
// so the domain prefix is stripped to keep the bare login name Droid records.
// Environment variables are the fallback for the rare lookup failure (e.g. a
// stripped-down container with no passwd database) — USER on Unix, USERNAME
// being the Windows equivalent.
func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		name := u.Username
		if i := strings.LastIndexByte(name, '\\'); i >= 0 {
			name = name[i+1:]
		}
		return name
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return os.Getenv("USERNAME")
}

// SupportsReconstruction reports true: this provider has a native serializer
// (see ReconstructSession), so it can be a cross-agent resume target.
func (p *Provider) SupportsReconstruction() bool {
	return true
}
