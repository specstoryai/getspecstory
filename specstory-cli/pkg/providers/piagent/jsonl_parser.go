package piagent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

const (
	KB = 1024
	MB = 1024 * 1024
)

// maxRecordLineSize is the per-record cap, shared with every other provider. A
// var only so a test can shrink it; treat it as spi.MaxRecordLineSize.
//
// Aggregate session-wide byte or entry caps were considered and rejected: the
// full parse must retain the whole tree to reconstruct the transcript. The
// metadata-only scan path (readScanEntries below) instead avoids the memory
// cost by never retaining message payloads it doesn't need, rather than by
// refusing large-but-valid sessions outright.
var maxRecordLineSize = spi.MaxRecordLineSize

// readRecordLines preserves each bounded record's original bytes. Consumers
// retaining them own the buffers returned by ReadRecordLine, including newlines.
func readRecordLines(path string, visit func(line []byte, lineNumber int) error) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("pi: opening session %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	reader := bufio.NewReader(f)
	for lineNum := 1; ; lineNum++ {
		line, oversized, readErr := spi.ReadRecordLine(reader, maxRecordLineSize)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("pi: reading line %d of %s: %w", lineNum, path, readErr)
		}
		if oversized {
			slog.Warn("pi: skipping oversized JSONL line",
				"lineNumber", lineNum, "limit", maxRecordLineSize, "file", path)
		} else if trimmed := strings.TrimSpace(string(line)); trimmed != "" {
			if vErr := visit(line, lineNum); vErr != nil {
				return vErr
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
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
func decodeEntry(line, path, prevID string, n, lineNumber int) (rawEntry, bool) {
	var e rawEntry
	if jErr := json.Unmarshal([]byte(line), &e); jErr != nil {
		slog.Warn("pi: skipping corrupted JSONL line", "file", path, "lineNumber", lineNumber, "error", jErr)
		return rawEntry{}, false
	}
	if e.Type == "" {
		slog.Warn("pi: skipping entry with empty type", "file", path, "lineNumber", lineNumber)
		return rawEntry{}, false
	}
	e.sourcePath, e.lineNumber = path, lineNumber
	diagnoseEntry(e)
	if e.ID == "" {
		e.ID = fmt.Sprintf("legacy-%d", n+1)
		if prevID != "" {
			pid := prevID
			e.ParentID = &pid
		}
	}
	return e, true
}

// diagnoseEntry distinguishes known non-conversation data from format changes.
// Keep every valid envelope in the tree: dropping a control or unknown entry
// would disconnect its descendants from their recorded parents.
func diagnoseEntry(e rawEntry) {
	switch e.Type {
	case entryMessage:
		var role struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(e.Message, &role); err != nil {
			slog.Warn("pi: corrupted message envelope", "file", e.sourcePath, "lineNumber", e.lineNumber, "error", err)
			return
		}
		switch role.Role {
		case roleUser, roleAssistant, roleToolResult:
			// Conversation messages are rendered or merged into their matching call.
		case roleBashExecution:
			// User-invoked shell executions become labeled user text with results.
		case roleCustom:
			// Extension context has no unified conversation role.
		case roleBranchSummary, roleCompaction:
			// Context-only summaries are distinct from durable compaction entries.
		default:
			slog.Warn("pi: unknown message role", "file", e.sourcePath, "lineNumber", e.lineNumber, "role", role.Role)
		}
	case entryCompaction:
		// Rendered as a transcript marker without dropping earlier turns.
	case entryModelChange, entryThinkingLevelChange:
		// Settings changes; assistant entries carry the model actually used.
	case entrySessionInfo, entryLabel:
		// Session name and tree labels belong to navigation, not conversation.
	case entryCustom, entryCustomMessage:
		// Extension state and injected context are not user/assistant turns.
	case entryBranchSummary:
		// Context about a branch the user left, not conversation on the active path.
	default:
		slog.Warn("pi: unknown entry kind", "file", e.sourcePath, "lineNumber", e.lineNumber, "kind", e.Type)
	}
}

// sessionSnapshot keeps decoded records and their original bytes together so
// normalized data, raw uploads and debug exports describe the same read.
type sessionSnapshot struct {
	header  *sessionHeader
	entries []rawEntry
	records []json.RawMessage
}

// readEntries retains accepted, bounded records, skipping malformed body lines
// without aborting the rest of the session.
func readEntries(path string) (*sessionSnapshot, error) {
	var header *sessionHeader
	var entries []rawEntry
	var records []json.RawMessage
	err := readRecordLines(path, func(raw []byte, lineNumber int) error {
		line := strings.TrimSpace(string(raw))
		if header == nil {
			h := sessionHeader{}
			if jErr := json.Unmarshal([]byte(line), &h); jErr != nil {
				return fmt.Errorf("pi: parsing session header of %s at line %d: %w", path, lineNumber, jErr)
			}
			if h.Type != entrySession || h.ID == "" {
				return fmt.Errorf("pi: invalid session header in %s at line %d (type %q, id %q)", path, lineNumber, h.Type, h.ID)
			}
			header = &h
			records = append(records, json.RawMessage(raw))
			return nil
		}
		prevID := ""
		if len(entries) > 0 {
			prevID = entries[len(entries)-1].ID
		}
		e, ok := decodeEntry(line, path, prevID, len(entries), lineNumber)
		if !ok {
			return nil
		}
		entries = append(entries, e)
		records = append(records, json.RawMessage(raw))
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Only accepted, bounded records enter the snapshot. Unknown native fields
	// remain intact because records contain original bytes, not reserialized structs.
	return &sessionSnapshot{header: header, entries: entries, records: records}, nil
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
	err := readRecordLines(path, func(raw []byte, lineNumber int) error {
		line := strings.TrimSpace(string(raw))
		if first {
			first = false // header already parsed by readHeader
			return nil
		}
		prevID := ""
		if len(entries) > 0 {
			prevID = entries[len(entries)-1].ID
		}
		e, ok := decodeEntry(line, path, prevID, len(entries), lineNumber)
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
