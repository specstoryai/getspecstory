package piagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// writeDebugRaw exports non-header records from the parse snapshot. The CLI
// owns session-data.json; exporting here must never reopen a changing session.
func writeDebugRaw(sessionID string, entries []json.RawMessage) error {
	dir := spi.GetDebugDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeNumberedEntries(dir, entries)
}

// writeNumberedEntries preserves unknown native fields while pretty-printing
// the accepted records in their original file order.
func writeNumberedEntries(dir string, entries []json.RawMessage) error {
	for i, raw := range entries {
		filePath := filepath.Join(dir, fmt.Sprintf("%d.json", i+1))
		payload, err := json.MarshalIndent(raw, "", "  ")
		if err != nil {
			return fmt.Errorf("pi: formatting debug record %d: %w", i+1, err)
		}
		if err := os.WriteFile(filePath, payload, 0o644); err != nil {
			return err
		}
	}
	return nil
}
