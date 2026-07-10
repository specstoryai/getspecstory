package piagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
// value, for debug-raw burst output. Uses bufio.Reader (via readLines) so
// arbitrarily large lines are captured without the 16MB bufio.Scanner cap.
func readRawEntries(path string) ([]json.RawMessage, error) {
	var out []json.RawMessage
	first := true
	err := readLines(path, func(line string) error {
		if first {
			first = false // skip the session header line
			return nil
		}
		out = append(out, json.RawMessage(line))
		return nil
	})
	return out, err
}
