package piagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Line-size sanity limits, mirroring claudecode/codex. bufio.Reader.ReadString
// has no line-size limit, so a 250MB cap guards against OOM from pathological or
// malicious files (a legitimate pi session line — a big tool result or base64
// image — can exceed the 16MB bufio.Scanner cap, so we do NOT use Scanner).
const (
	KB                    = 1024
	MB                    = 1024 * 1024
	maxReasonableLineSize = 250 * MB
)

// readLines reads a pi session file line-by-line via bufio.Reader (unbounded
// line size, unlike bufio.Scanner) and calls visit for each non-empty trimmed
// line. Lines exceeding maxReasonableLineSize are refused with an error to
// prevent OOM.
func readLines(path string, visit func(line string) error) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("pi: opening session %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	reader := bufio.NewReader(f)
	lineNum := 0
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return fmt.Errorf("pi: reading line %d of %s: %w", lineNum+1, path, readErr)
		}
		line = strings.TrimSuffix(line, "\n")
		lineNum++
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if len(trimmed) > maxReasonableLineSize {
				slog.Warn("pi: line exceeds reasonable size limit",
					"lineNumber", lineNum, "sizeMB", len(trimmed)/MB,
					"limitMB", maxReasonableLineSize/MB, "file", filepath.Base(path))
				return fmt.Errorf("pi: line %d of %s exceeds %dMB (refusing to process potentially malformed file)",
					lineNum, path, maxReasonableLineSize/MB)
			}
			if vErr := visit(trimmed); vErr != nil {
				return vErr
			}
		}
		if readErr == io.EOF {
			return nil
		}
	}
}

// readEntries parses every line of the session file into a header (line 1) and
// a list of message/control entries (the rest). Malformed lines are skipped
// rather than aborting the whole parse.
func readEntries(path string) (*sessionHeader, []rawEntry, error) {
	var header *sessionHeader
	var entries []rawEntry
	err := readLines(path, func(line string) error {
		if header == nil {
			h := sessionHeader{}
			if jErr := json.Unmarshal([]byte(line), &h); jErr != nil {
				return fmt.Errorf("pi: parsing session header: %w", jErr)
			}
			header = &h
			return nil
		}
		var e rawEntry
		if jErr := json.Unmarshal([]byte(line), &e); jErr != nil {
			return nil // skip malformed line, keep going
		}
		// An entry with no type carries nothing the mapper can use; skip it so
		// a stray malformed line cannot mislead leaf selection.
		if e.Type == "" {
			slog.Debug("pi: skipping entry with empty type", "file", path)
			return nil
		}
		// Unmigrated pi v1 files store a flat linear sequence with no id/parentId
		// (pi's migrateV1ToV2 synthesizes them at load). Chain such entries to the
		// previous one so the tree walk sees the same linear path pi would build.
		if e.ID == "" {
			e.ID = fmt.Sprintf("legacy-%d", len(entries)+1)
			if len(entries) > 0 {
				pid := entries[len(entries)-1].ID
				e.ParentID = &pid
			}
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return header, entries, nil
}

// leafPathEntries walks from the leaf (last entry in file order) to the root
// and reverses to chronological order. Compaction entries on the path are NOT
// applied as truncation: pi's buildContextEntries drops pre-compaction entries
// only to fit the LLM context window, but SpecStory's job is preserving the
// full transcript, so every entry on the active branch is kept (the compaction
// summary itself is rendered as a marker by buildExchanges).
func leafPathEntries(entries []rawEntry) []rawEntry {
	byID := indexByID(entries)
	leaf := entries[len(entries)-1]
	path := walkToRoot(leaf, byID)
	reverse(path)
	return path
}

// indexByID builds an id -> entry lookup for the tree walk.
func indexByID(entries []rawEntry) map[string]rawEntry {
	m := make(map[string]rawEntry, len(entries))
	for _, e := range entries {
		m[e.ID] = e
	}
	return m
}

// walkToRoot collects entries from the given leaf up to the root (parentId
// null). A visited set guards against parentId cycles in corrupted sessions so
// the walk terminates instead of looping forever.
func walkToRoot(leaf rawEntry, byID map[string]rawEntry) []rawEntry {
	var path []rawEntry
	cur := leaf
	visited := make(map[string]bool)
	for !visited[cur.ID] {
		visited[cur.ID] = true
		path = append(path, cur)
		if cur.ParentID == nil {
			break
		}
		parent, ok := byID[*cur.ParentID]
		if !ok {
			break
		}
		cur = parent
	}
	return path
}

// reverse reverses a slice of rawEntry in place.
func reverse(s []rawEntry) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
