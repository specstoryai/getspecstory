package piagent

import (
	"encoding/json"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// writeDebugRaw exports all accepted records, including the native header. The CLI
// owns session-data.json; exporting here must never reopen a changing session.
func writeDebugRaw(sessionID string, entries []json.RawMessage) error {
	return spi.WriteDebugRecords(spi.GetDebugDir(sessionID), entries)
}
