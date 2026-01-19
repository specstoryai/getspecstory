package droidcli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func writeFactoryDebugRaw(session *fdSession) error {
	if session == nil {
		return nil
	}
	dir := spi.GetDebugDir(session.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("droidcli: unable to create debug dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(session.RawData), 0o644); err != nil {
		return fmt.Errorf("droidcli: unable to write debug raw: %w", err)
	}
	meta := fmt.Sprintf("Session: %s\nTitle: %s\nCreated: %s\nBlocks: %d\n", session.ID, session.Title, session.CreatedAt, len(session.Blocks))
	return os.WriteFile(filepath.Join(dir, "summary.txt"), []byte(meta), 0o644)
}
