package copilotide

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// LoadAllSessionFiles returns paths to all session JSON files in the workspace
func LoadAllSessionFiles(workspaceDir string) ([]string, error) {
	chatSessionsPath := GetChatSessionsPath(workspaceDir)

	// Check if directory exists
	if _, err := os.Stat(chatSessionsPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("chatSessions directory not found: %s", chatSessionsPath)
	}

	files, err := os.ReadDir(chatSessionsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read chat sessions directory: %w", err)
	}

	var sessionFiles []string
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// Only include .json files
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		sessionPath := filepath.Join(chatSessionsPath, file.Name())
		sessionFiles = append(sessionFiles, sessionPath)
	}

	slog.Debug("Found session files", "count", len(sessionFiles), "workspace", workspaceDir)
	return sessionFiles, nil
}

// LoadSessionFile reads and parses a single session JSON file
func LoadSessionFile(sessionPath string) (*VSCodeComposer, error) {
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	var composer VSCodeComposer
	if err := json.Unmarshal(data, &composer); err != nil {
		return nil, fmt.Errorf("failed to parse session JSON: %w", err)
	}

	slog.Debug("Loaded session file",
		"sessionId", composer.SessionID,
		"requestCount", len(composer.Requests))

	return &composer, nil
}

// LoadSessionByID loads a specific session by ID from the workspace
func LoadSessionByID(workspaceDir, sessionID string) (*VSCodeComposer, error) {
	chatSessionsPath := GetChatSessionsPath(workspaceDir)
	sessionPath := filepath.Join(chatSessionsPath, sessionID+".json")

	// Check if file exists
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	return LoadSessionFile(sessionPath)
}

// LoadStateFile loads the optional state file for a session
// Returns nil if the state file doesn't exist (not an error)
func LoadStateFile(workspaceDir, sessionID string) (*VSCodeStateFile, error) {
	statePath := GetStateFilePath(workspaceDir, sessionID)

	// State file is optional
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		slog.Debug("No state file found (optional)", "sessionId", sessionID)
		return nil, nil
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state VSCodeStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state JSON: %w", err)
	}

	slog.Debug("Loaded state file", "sessionId", sessionID)
	return &state, nil
}

// WriteDebugFiles writes raw session data to debug directory
func WriteDebugFiles(composer *VSCodeComposer, sessionID string) error {
	debugDir, err := EnsureDebugDirectory(sessionID)
	if err != nil {
		return fmt.Errorf("failed to create debug directory: %w", err)
	}

	// Write raw session JSON (pretty-printed)
	rawSessionPath := filepath.Join(debugDir, "raw-session.json")
	rawData, err := json.MarshalIndent(composer, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session data: %w", err)
	}

	if err := os.WriteFile(rawSessionPath, rawData, 0644); err != nil {
		return fmt.Errorf("failed to write raw session file: %w", err)
	}

	slog.Debug("Wrote debug files", "sessionId", sessionID, "debugDir", debugDir)
	return nil
}
