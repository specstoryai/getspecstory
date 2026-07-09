package piagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// writeDebugRaw writes numbered JSON files (one per non-header entry) plus the
// central session-data.json via the shared spi helper. This matches the
// --debug-raw contract used by every provider.
func writeDebugRaw(sessionPath string, data *schema.SessionData) error {
	if err := spi.WriteDebugSessionData(data.SessionID, data); err != nil {
		return err
	}
	dir := spi.GetDebugDir(data.SessionID)
	entries, err := readRawEntries(sessionPath)
	if err != nil {
		return err
	}
	return writeNumberedEntries(dir, entries)
}

// writeNumberedEntries writes each raw entry as 1.json, 2.json, ... in dir.
func writeNumberedEntries(dir string, entries []json.RawMessage) error {
	for i, raw := range entries {
		filePath := filepath.Join(dir, fmt.Sprintf("%d.json", i+1))
		payload, mErr := json.MarshalIndent(raw, "", "  ")
		if mErr != nil {
			continue
		}
		if wErr := os.WriteFile(filePath, payload, 0o644); wErr != nil {
			return wErr
		}
	}
	return nil
}

// readRawEntries returns each non-header line of a session file as a raw JSON
// value, for debug-raw burst output.
func readRawEntries(path string) ([]json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("pi: opening session %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	var out []json.RawMessage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	first := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if first {
			first = false
			continue
		}
		out = append(out, json.RawMessage(line))
	}
	return out, scanner.Err()
}
