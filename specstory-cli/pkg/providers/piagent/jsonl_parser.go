package piagent

import (
	"bufio"
	"bytes"
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
	KB = 1024
	MB = 1024 * 1024
)

// maxReasonableLineSize is the only sanity limit the pi parser applies.
// Aggregate session-wide byte/entry caps were considered and rejected: the
// full parse must retain the whole tree to reconstruct the transcript, and
// this is no worse than the Claude Code/Codex providers, which also cap only
// a single line's size. The metadata-only scan path (readScanEntries below)
// instead avoids the memory cost by never retaining message payloads it
// doesn't need, rather than by refusing large-but-valid sessions outright.
var maxReasonableLineSize = 250 * MB

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
	var longLine bytes.Buffer
	for {
		part, isPrefix, readErr := reader.ReadLine()
		if readErr != nil && readErr != io.EOF {
			return fmt.Errorf("pi: reading line %d of %s: %w", lineNum+1, path, readErr)
		}
		if len(part) > 0 {
			if longLine.Len()+len(part) > maxReasonableLineSize {
				slog.Warn("pi: line exceeds reasonable size limit",
					"lineNumber", lineNum+1, "sizeMB", (longLine.Len()+len(part))/MB,
					"limitMB", maxReasonableLineSize/MB, "file", filepath.Base(path))
				return fmt.Errorf("pi: line %d of %s exceeds %dMB (refusing to process potentially malformed file)",
					lineNum+1, path, maxReasonableLineSize/MB)
			}
			if _, wErr := longLine.Write(part); wErr != nil {
				return fmt.Errorf("pi: buffering line %d of %s: %w", lineNum+1, path, wErr)
			}
		}
		if readErr == io.EOF {
			if longLine.Len() == 0 {
				return nil
			}
			lineNum++
			trimmed := strings.TrimSpace(longLine.String())
			longLine.Reset()
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
			return nil
		}
		if isPrefix {
			continue
		}
		lineNum++
		trimmed := strings.TrimSpace(longLine.String())
		longLine.Reset()
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
	}
}

// decodeEntry unmarshals one non-header JSONL line into a rawEntry. prevID is
// the ID of the most recently accepted entry ("" if none yet) and n is how
// many entries have been accepted so far; both feed the same pi v1 legacy-id
// synthesis readEntries has always applied (unmigrated v1 files store a flat
// linear sequence with no id/parentId — pi's migrateV1ToV2 synthesizes them at
// load, so we chain such entries to the previous one here to see the same
// linear path pi would build). ok is false for a malformed line or one with no
// type (nothing the mapper can use), which callers should skip rather than
// abort the whole parse. This logic is shared verbatim by readEntries and
// readScanEntries: they must pick the same active leaf, or scan and full parse
// disagree on a session's slug/name.
func decodeEntry(line, path, prevID string, n int) (rawEntry, bool) {
	var e rawEntry
	if jErr := json.Unmarshal([]byte(line), &e); jErr != nil {
		return rawEntry{}, false
	}
	if e.Type == "" {
		slog.Debug("pi: skipping entry with empty type", "file", path)
		return rawEntry{}, false
	}
	if e.ID == "" {
		e.ID = fmt.Sprintf("legacy-%d", n+1)
		if prevID != "" {
			pid := prevID
			e.ParentID = &pid
		}
	}
	return e, true
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
		prevID := ""
		if len(entries) > 0 {
			prevID = entries[len(entries)-1].ID
		}
		e, ok := decodeEntry(line, path, prevID, len(entries))
		if !ok {
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

// scanEntry is the lightweight branch-walk shape used by the metadata-only
// scan path (readScanEntries/leafPathScanEntries below, driving
// scanPiSession in provider.go for `specstory list`/`reindex`). Unlike
// rawEntry, it never carries the entry's Message payload — assistant text,
// tool results, base64 images — which is the bulk of a session file's bytes
// and is not needed for listing. Only the extracted user-prompt text (when
// present) is kept.
type scanEntry struct {
	ID       string
	ParentID *string
	Type     string
	UserText string
}

// readScanEntries reads a pi session file for metadata-only scanning, sharing
// decodeEntry's per-line decoding and legacy-id logic with readEntries so scan
// and full parse always select the same active leaf. Each decoded rawEntry
// (including its Message payload) lives only for the duration of one loop
// iteration: readScanEntries copies out just the tree-walk fields plus, for a
// user message, its extracted text, so a scan of a large session never
// retains the full set of message bodies in memory the way a full parse must.
func readScanEntries(path string) ([]scanEntry, string, error) {
	var entries []scanEntry
	var sessionName string
	first := true
	err := readLines(path, func(line string) error {
		if first {
			first = false // header already parsed by readHeader
			return nil
		}
		prevID := ""
		if len(entries) > 0 {
			prevID = entries[len(entries)-1].ID
		}
		e, ok := decodeEntry(line, path, prevID, len(entries))
		if !ok {
			return nil
		}
		if e.Type == entrySessionInfo {
			sessionName = strings.TrimSpace(e.Name)
		}
		light := scanEntry{ID: e.ID, ParentID: e.ParentID, Type: e.Type}
		if e.Type == entryMessage {
			light.UserText = firstUserText(e)
		}
		entries = append(entries, light)
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return entries, sessionName, nil
}

// leafPathScanEntries is leafPathEntries' counterpart for the lightweight
// scanEntry shape: walk from the leaf (last entry in file order) to the root
// and reverse to chronological order, guarding against parentId cycles.
func leafPathScanEntries(entries []scanEntry) []scanEntry {
	byID := make(map[string]scanEntry, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	cur := entries[len(entries)-1]
	path := make([]scanEntry, 0, len(entries))
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
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
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
