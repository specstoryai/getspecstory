package piagent

import (
	"bufio"
	"encoding/json"
	"errors"
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

// errStopRead is a sentinel a visit callback may return to stop iteration early
// (without error). readLines treats it as a clean stop.
var errStopRead = errors.New("stop reading")

// readLines reads a pi session file line-by-line via bufio.Reader (unbounded
// line size, unlike bufio.Scanner) and calls visit for each non-empty trimmed
// line. Lines exceeding maxReasonableLineSize are refused with an error to
// prevent OOM. Returning errStopRead from visit stops iteration cleanly.
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
				if errors.Is(vErr, errStopRead) {
					return nil
				}
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
		// Skip entries missing the envelope fields the tree walk and exchange
		// grouping rely on; a stray entry with no id can mislead leaf selection.
		if e.Type == "" || e.ID == "" {
			slog.Debug("pi: skipping entry with empty type or id", "file", path)
			return nil
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return header, entries, nil
}

// leafPathEntries walks from the leaf (last entry in file order) to the root,
// reverses to chronological order, and applies compaction: if a compaction
// entry is on the path, entries before its firstKeptEntryId are dropped.
func leafPathEntries(entries []rawEntry) []rawEntry {
	byID := indexByID(entries)
	leaf := entries[len(entries)-1]
	path := walkToRoot(leaf, byID)
	reverse(path)
	return applyCompaction(path)
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

// applyCompaction drops entries before the most recent compaction point when
// one or more compaction entries are on the path. pi's buildContextEntries
// builds context from the LATEST compaction's firstKeptEntryId forward (each
// new compaction summarizes prior compactions into itself), so when multiple
// compactions exist we use the last one in chronological order, not the first.
// If firstKeptEntryId is missing from the path, we keep from the compaction
// entry forward so pre-compaction entries are never accidentally retained.
func applyCompaction(path []rawEntry) []rawEntry {
	last := -1
	for i, e := range path {
		if e.Type == entryCompaction {
			last = i
		}
	}
	if last < 0 {
		return path
	}
	keptID := compactionFirstKept(path[last])
	if keptID == "" {
		return path[last:]
	}
	if idx := keepFromIndex(path, keptID); idx >= 0 {
		return path[idx:]
	}
	return path[last:]
}

// compactionFirstKept returns the firstKeptEntryId of a compaction entry. pi
// stores this as a top-level field on the entry (not inside a message
// payload), so it is decoded directly into rawEntry.FirstKeptEntryID.
func compactionFirstKept(e rawEntry) string {
	return e.FirstKeptEntryID
}

// keepFromIndex returns the index of the entry with id==keptID in path, or -1
// if not found.
func keepFromIndex(path []rawEntry, keptID string) int {
	for i, e := range path {
		if e.ID == keptID {
			return i
		}
	}
	return -1
}
