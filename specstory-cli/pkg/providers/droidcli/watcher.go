package droidcli

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

const droidWatchInterval = 1 * time.Second

type watchState struct {
	lastProcessed map[string]int64
}

func watchSessions(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	if sessionCallback == nil {
		return fmt.Errorf("session callback is required")
	}

	state := &watchState{lastProcessed: make(map[string]int64)}
	if err := scanAndProcessSessions(projectPath, debugRaw, sessionCallback, state); err != nil {
		return err
	}

	ticker := time.NewTicker(droidWatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := scanAndProcessSessions(projectPath, debugRaw, sessionCallback, state); err != nil {
				slog.Debug("droidcli: scan failed", "error", err)
			}
		}
	}
}

func scanAndProcessSessions(projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession), state *watchState) error {
	files, err := listSessionFiles()
	if err != nil {
		return err
	}

	for _, file := range files {
		if last, ok := state.lastProcessed[file.Path]; ok && last >= file.ModTime {
			continue
		}
		if projectPath != "" && !sessionMentionsProject(file.Path, projectPath) {
			continue
		}
		session, err := parseFactorySession(file.Path)
		if err != nil {
			slog.Debug("droidcli: skipping session", "path", file.Path, "error", err)
			continue
		}
		chat := convertToAgentSession(session, projectPath, debugRaw)
		if chat == nil {
			continue
		}
		state.lastProcessed[file.Path] = file.ModTime
		dispatchSession(sessionCallback, chat)
	}
	return nil
}

func dispatchSession(sessionCallback func(*spi.AgentChatSession), session *spi.AgentChatSession) {
	if sessionCallback == nil || session == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("droidcli: session callback panicked", "panic", r)
			}
		}()
		sessionCallback(session)
	}()
}
