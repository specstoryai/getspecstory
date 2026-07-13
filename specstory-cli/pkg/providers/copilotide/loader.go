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

		// Include both .json and .jsonl files
		if !strings.HasSuffix(file.Name(), ".json") && !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}

		sessionPath := filepath.Join(chatSessionsPath, file.Name())
		sessionFiles = append(sessionFiles, sessionPath)
	}

	slog.Debug("Found session files", "count", len(sessionFiles), "workspace", workspaceDir)
	return sessionFiles, nil
}

// LoadSessionFile reads and parses a single session JSON or JSONL file
func LoadSessionFile(sessionPath string) (*VSCodeComposer, error) {
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	// Determine format based on file extension
	isJSONL := strings.HasSuffix(sessionPath, ".jsonl")

	var composer VSCodeComposer

	if isJSONL {
		// JSONL format: each line is a separate JSON object
		composer, err = parseJSONL(data)
		if err != nil {
			return nil, fmt.Errorf("failed to parse JSONL: %w", err)
		}
	} else {
		// JSON format: single JSON object
		if err := json.Unmarshal(data, &composer); err != nil {
			return nil, fmt.Errorf("failed to parse session JSON: %w", err)
		}
	}

	slog.Debug("Loaded session file",
		"sessionId", composer.SessionID,
		"requestCount", len(composer.Requests),
		"isJSONL", isJSONL)

	return &composer, nil
}

// LoadSessionByID loads a specific session by ID from the workspace
func LoadSessionByID(workspaceDir, sessionID string) (*VSCodeComposer, error) {
	chatSessionsPath := GetChatSessionsPath(workspaceDir)

	// Try .jsonl first (newer format), then fall back to .json (older format)
	jsonlPath := filepath.Join(chatSessionsPath, sessionID+".jsonl")
	jsonPath := filepath.Join(chatSessionsPath, sessionID+".json")

	var sessionPath string
	if _, err := os.Stat(jsonlPath); err == nil {
		sessionPath = jsonlPath
	} else if _, err := os.Stat(jsonPath); err == nil {
		sessionPath = jsonPath
	} else {
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
		slog.Debug("No state file found (optional)", "sessionId", sessionID, "path", statePath)
		return nil, nil
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		slog.Warn("Failed to read state file, continuing without state",
			"sessionId", sessionID,
			"path", statePath,
			"error", err)
		return nil, nil // Return nil instead of error, state is optional
	}

	var state VSCodeStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		slog.Warn("Failed to parse state JSON, continuing without state",
			"sessionId", sessionID,
			"path", statePath,
			"error", err)
		return nil, nil // Return nil instead of error, state is optional
	}

	slog.Debug("Loaded state file", "sessionId", sessionID, "version", state.Version)
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

// parseJSONL parses JSONL format with incremental updates
// First line (kind: 0) contains initial state in "v" field
// Subsequent lines (kind: 1) contain updates with key path in "k" and value in "v"
func parseJSONL(data []byte) (VSCodeComposer, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return VSCodeComposer{}, fmt.Errorf("empty JSONL file")
	}
	lines := strings.Split(trimmed, "\n")

	// Parse first line - should be kind:0 with initial state
	var firstLine struct {
		Kind int             `json:"kind"`
		V    json.RawMessage `json:"v"`
	}

	if err := json.Unmarshal([]byte(lines[0]), &firstLine); err != nil {
		return VSCodeComposer{}, fmt.Errorf("failed to parse first JSONL line: %w", err)
	}

	if firstLine.Kind != 0 {
		return VSCodeComposer{}, fmt.Errorf("expected kind:0 in first line, got kind:%d", firstLine.Kind)
	}

	// Keep the session in map form while applying updates so each line mutates
	// in place; converting the whole composer to a map and back per update line
	// is O(session size) per line and dominates load time on long sessions.
	var composerMap map[string]any
	if err := json.Unmarshal(firstLine.V, &composerMap); err != nil {
		return VSCodeComposer{}, fmt.Errorf("failed to parse initial state in first JSONL line: %w", err)
	}

	// Apply subsequent updates.
	// kind:1 = incremental key-path update (small field changes, e.g. customTitle)
	// kind:2 = bulk key-path replacement (e.g. full requests array written after initial snapshot)
	// VS Code Copilot introduced kind:2 to split large payloads from the initial kind:0 snapshot.
	for i := 1; i < len(lines); i++ {
		var update struct {
			Kind int   `json:"kind"`
			K    []any `json:"k"` // Key path: string map keys and/or numeric array indices, e.g. ["requests", 3, "response"]
			V    any   `json:"v"` // Value
		}

		if err := json.Unmarshal([]byte(lines[i]), &update); err != nil {
			slog.Warn("Failed to parse JSONL update line", "line", i+1, "error", err)
			continue
		}

		switch update.Kind {
		case 1:
			if len(update.K) > 0 {
				// Replace the value at the key path
				if err := applyUpdate(composerMap, update.K, update.V); err != nil {
					slog.Warn("Failed to apply JSONL update", "line", i+1, "kind", update.Kind, "keyPath", update.K, "error", err)
				}
			}
		case 2:
			if len(update.K) > 0 {
				// Append items to the array at the key path.
				// VS Code writes one kind:2 per user turn, each containing only the new
				// request(s) for that turn — not the full history — so we must accumulate.
				if err := appendUpdate(composerMap, update.K, update.V); err != nil {
					slog.Warn("Failed to apply JSONL append", "line", i+1, "kind", update.Kind, "keyPath", update.K, "error", err)
				}
			}
		default:
			// Log unknown kinds so we detect future format changes early
			slog.Warn("Unknown JSONL kind, skipping line", "line", i+1, "kind", update.Kind)
		}
	}

	// Decode the fully-updated map into the typed composer once
	finalData, err := json.Marshal(composerMap)
	if err != nil {
		return VSCodeComposer{}, fmt.Errorf("failed to marshal updated session: %w", err)
	}

	var composer VSCodeComposer
	if err := json.Unmarshal(finalData, &composer); err != nil {
		return VSCodeComposer{}, fmt.Errorf("failed to parse updated session: %w", err)
	}

	return composer, nil
}

// pathIndex converts a numeric key path segment (JSON numbers decode as float64)
// to an integer array index.
func pathIndex(seg any) (int, bool) {
	f, ok := seg.(float64)
	if !ok || f != float64(int(f)) || f < 0 {
		return 0, false
	}
	return int(f), true
}

// setAtPath sets value at keyPath inside container, where string segments index
// maps (created when missing) and numeric segments index arrays (an index equal
// to the array length appends). It returns the possibly-new container because
// growing a slice reallocates it, so callers must store the result back.
func setAtPath(container any, keyPath []any, value any) (any, error) {
	if len(keyPath) == 0 {
		return value, nil
	}

	switch seg := keyPath[0].(type) {
	case string:
		m, ok := container.(map[string]any)
		if !ok {
			if container != nil {
				return nil, fmt.Errorf("cannot set key %q on non-object value of type %T", seg, container)
			}
			m = make(map[string]any)
		}
		child, err := setAtPath(m[seg], keyPath[1:], value)
		if err != nil {
			return nil, err
		}
		m[seg] = child
		return m, nil

	case float64:
		idx, ok := pathIndex(seg)
		if !ok {
			return nil, fmt.Errorf("invalid array index in key path: %v", seg)
		}
		s, isSlice := container.([]any)
		if !isSlice {
			if container != nil {
				return nil, fmt.Errorf("cannot index into non-array value of type %T", container)
			}
			s = []any{}
		}
		if idx > len(s) {
			return nil, fmt.Errorf("array index %d out of range (length %d)", idx, len(s))
		}
		if idx == len(s) {
			s = append(s, nil)
		}
		child, err := setAtPath(s[idx], keyPath[1:], value)
		if err != nil {
			return nil, err
		}
		s[idx] = child
		return s, nil

	default:
		return nil, fmt.Errorf("unsupported key path segment of type %T: %v", seg, seg)
	}
}

// getAtPath reads the value at keyPath inside container without modifying it.
// Returns nil when any segment is missing or of the wrong shape.
func getAtPath(container any, keyPath []any) any {
	current := container
	for _, seg := range keyPath {
		switch key := seg.(type) {
		case string:
			m, ok := current.(map[string]any)
			if !ok {
				return nil
			}
			current = m[key]
		case float64:
			idx, ok := pathIndex(key)
			if !ok {
				return nil
			}
			s, isSlice := current.([]any)
			if !isSlice || idx >= len(s) {
				return nil
			}
			current = s[idx]
		default:
			return nil
		}
	}
	return current
}

// appendUpdate appends items to the array at a specific key path in the map-form session.
// Used for kind:2 lines where VS Code writes only the newly added items (e.g. a single
// new request per turn, or new response parts within a request) rather than the full
// array, so each write must be accumulated.
// Falls back to replace semantics when the target or incoming value is not an array.
func appendUpdate(composerMap map[string]any, keyPath []any, value any) error {
	newItems, isArray := value.([]any)
	if !isArray {
		// Not an array delta — treat as a plain replace
		return applyUpdate(composerMap, keyPath, value)
	}

	// Append the new items to whatever is already there; fall back to replace if needed
	if existing, ok := getAtPath(composerMap, keyPath).([]any); ok {
		newItems = append(existing, newItems...)
	}
	return applyUpdate(composerMap, keyPath, newItems)
}

// applyUpdate sets value at a specific key path in the map-form session.
// keyPath mixes string map keys and numeric array indices
// (e.g., ["requests", 3, "response"]).
func applyUpdate(composerMap map[string]any, keyPath []any, value any) error {
	_, err := setAtPath(composerMap, keyPath, value)
	return err
}
