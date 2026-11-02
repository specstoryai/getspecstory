package cursoride

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/vscode"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// composerBatchSize is the maximum number of composer IDs per SQL query.
// SQLite limits expression tree depth to ~1000. Each composer ID contributes
// three expressions (one IN entry + two LIKE conditions), so 200 IDs keeps us
// well under that limit.
const composerBatchSize = 200

// Key prefixes of the cursorDiskKV rows that make up a conversation. They are shared
// by the readers and by InsertComposerSession, which must write keys Cursor will find.
const (
	// composerDataPrefix keys one row per conversation: composerData:<composerId>.
	composerDataPrefix = "composerData:"
	// bubbleIDPrefix keys one row per message: bubbleId:<composerId>:<bubbleId>.
	bubbleIDPrefix = "bubbleId:"
	// convHeadersPrefix keys the paged conversation-header rows that Cursor >= 3.27.14
	// writes instead of populating fullConversationHeadersOnly:
	//
	//	composerConvHeaders:<composerId>:000000 -> JSON array of conversation headers
	//	composerConvHeaders:<composerId>:000001 -> next page, ...
	//
	// Concatenating the pages in key order yields the full ordered header list. The
	// bubble rows are unchanged.
	convHeadersPrefix = "composerConvHeaders:"
)

// headerPage is one composerConvHeaders row: the zero-padded suffix from its key and
// the headers it holds. Pages are sorted by suffix before concatenation.
type headerPage struct {
	suffix  string
	headers []ComposerConversationHeader
}

// parseConvHeadersKey splits a composerConvHeaders key into its composer ID and page
// suffix. ok is false for keys that don't have the expected three parts.
func parseConvHeadersKey(key string) (composerID, suffix string, ok bool) {
	rest, found := strings.CutPrefix(key, convHeadersPrefix)
	if !found {
		return "", "", false
	}
	composerID, suffix, found = strings.Cut(rest, ":")
	if !found || composerID == "" || suffix == "" {
		return "", "", false
	}
	return composerID, suffix, true
}

// addHeaderPage parses one composerConvHeaders row into the pages map.
func addHeaderPage(pages map[string][]headerPage, key, valueJSON string) {
	composerID, suffix, ok := parseConvHeadersKey(key)
	if !ok {
		slog.Warn("Invalid conversation headers key format", "key", key)
		return
	}
	var headers []ComposerConversationHeader
	if err := json.Unmarshal([]byte(valueJSON), &headers); err != nil {
		slog.Warn("Failed to parse conversation headers page", "composerID", composerID, "suffix", suffix, "error", err)
		return
	}
	pages[composerID] = append(pages[composerID], headerPage{suffix: suffix, headers: headers})
}

// addComposerRow parses one composerData row into the composers map. It returns the
// parsed composer, or nil when the row is unusable, so callers can log what they need.
func addComposerRow(composers map[string]*ComposerData, key, valueJSON string) *ComposerData {
	composerID := strings.TrimPrefix(key, composerDataPrefix)
	var composer ComposerData
	if err := json.Unmarshal([]byte(valueJSON), &composer); err != nil {
		slog.Warn("Failed to parse composer data", "composerID", composerID, "error", err)
		return nil
	}
	composers[composerID] = &composer
	return &composer
}

// scanKV runs a cursorDiskKV key/value query and hands each row to handle. A row that
// fails to scan is logged and skipped so one bad row can't hide the rest of the history.
func scanKV(db *sql.DB, query string, args []any, handle func(key, valueJSON string)) error {
	rows, err := db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close query rows", "error", closeErr)
		}
	}()

	for rows.Next() {
		var key, valueJSON string
		if err := rows.Scan(&key, &valueJSON); err != nil {
			slog.Warn("Failed to scan row", "error", err)
			continue
		}
		handle(key, valueJSON)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating rows: %w", err)
	}
	return nil
}

// mergeHeaderPages fills FullConversationHeadersOnly from the paged rows for every
// composer whose inline header list is empty. Composers that still carry inline
// headers (Cursor < 3.27.14) are left untouched, so the inline list always wins.
func mergeHeaderPages(composers map[string]*ComposerData, pages map[string][]headerPage) {
	for composerID, composerPages := range pages {
		composer, exists := composers[composerID]
		if !exists || len(composer.FullConversationHeadersOnly) > 0 {
			continue
		}
		// Rows arrive in unspecified order. A plain string compare orders the pages
		// correctly only because Cursor zero-pads the suffix to a fixed width; if a
		// future Cursor drops the padding, page "10" would sort before page "2".
		slices.SortFunc(composerPages, func(a, b headerPage) int { return strings.Compare(a.suffix, b.suffix) })
		var headers []ComposerConversationHeader
		for _, p := range composerPages {
			headers = append(headers, p.headers...)
		}
		composer.FullConversationHeadersOnly = headers
		slog.Debug("Rebuilt conversation headers from paged rows",
			"composerID", composerID, "pages", len(composerPages), "headers", len(headers))
	}
}

// OpenDatabase opens a SQLite database in read-only mode with controlled
// connection pooling. Callers that need WAL mode guaranteed should call
// spi.EnsureWALMode once before using this function (e.g. at watcher startup).
func OpenDatabase(dbPath string) (*sql.DB, error) {
	// Open in read-only mode
	db, err := sql.Open("sqlite", dbPath+"?mode=ro&"+spi.BusyTimeoutPragma)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Limit the connection pool to prevent file descriptor accumulation.
	// SQLite serialises access anyway, so one connection is sufficient.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Second)

	slog.Debug("Successfully opened database", "path", dbPath)

	return db, nil
}

// LoadWorkspaceComposerIDs loads the composer IDs from a workspace database.
// Uses two complementary sources to handle both Cursor 2 and Cursor 3:
//
//  1. "composer.composerData" in ItemTable — Cursor 2 stores all IDs here (allComposers);
//     Cursor 3 stores only currently-open tabs (selectedComposerIds).
//
//  2. "workbench.panel.composerChatViewPane.*" in ItemTable — each entry's JSON value
//     contains keys of the form "workbench.panel.aichat.view.{UUID}" where the UUID is
//     the actual composer ID. This is the complete source in Cursor 3 and also present
//     in Cursor 2, so it works across both versions.
func LoadWorkspaceComposerIDs(workspaceDbPath string) ([]string, error) {
	db, err := OpenDatabase(workspaceDbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open workspace database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close workspace database", "error", closeErr)
		}
	}()

	seen := make(map[string]bool)
	var composerIDs []string

	// Method 1: composer.composerData key (Cursor 2: allComposers, Cursor 3: selectedComposerIds)
	var valueJSON string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", "composer.composerData").Scan(&valueJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Warn("Failed to query workspace composer data", "error", err)
	} else if err == nil {
		var composerRefs WorkspaceComposerRefs
		if jsonErr := json.Unmarshal([]byte(valueJSON), &composerRefs); jsonErr != nil {
			slog.Warn("Failed to parse workspace composer refs", "error", jsonErr)
		} else {
			for _, ref := range composerRefs.AllComposers {
				if !seen[ref.ComposerID] {
					seen[ref.ComposerID] = true
					composerIDs = append(composerIDs, ref.ComposerID)
				}
			}
			for _, id := range composerRefs.SelectedComposerIds {
				if !seen[id] {
					seen[id] = true
					composerIDs = append(composerIDs, id)
				}
			}
		}
	}

	// Method 2: workbench.panel.composerChatViewPane entries.
	for _, id := range loadPanelComposerIDs(db) {
		if !seen[id] {
			seen[id] = true
			composerIDs = append(composerIDs, id)
		}
	}

	slog.Debug("Loaded composer IDs from workspace",
		"count", len(composerIDs),
		"composerIDs", composerIDs)

	return composerIDs, nil
}

// loadPanelComposerIDs extracts composer IDs from the workbench.panel.composerChatViewPane
// entries in a workspace database. In Cursor 3 these are the authoritative source for all
// conversation IDs. Each JSON value is a map whose keys follow the pattern
// "workbench.panel.aichat.view.{composerUUID}".
// Two key formats exist:
//   - "workbench.panel.composerChatViewPane" (no suffix) — the main panel shared by all tabs
//   - "workbench.panel.composerChatViewPane.{paneUUID}" — one entry per open tab/pane
//
// Failures are logged and yield a partial (possibly empty) result rather than an error —
// method 1 in LoadWorkspaceComposerIDs may still have produced usable IDs.
func loadPanelComposerIDs(db *sql.DB) []string {
	rows, err := db.Query(
		"SELECT value FROM ItemTable WHERE (key = 'workbench.panel.composerChatViewPane' OR (key LIKE 'workbench.panel.composerChatViewPane.%' AND key NOT LIKE '%.hidden'))",
	)
	if err != nil {
		slog.Warn("Failed to query workspace panel entries", "error", err)
		return nil
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close panel query rows", "error", closeErr)
		}
	}()

	const viewPrefix = "workbench.panel.aichat.view."
	var composerIDs []string
	for rows.Next() {
		var panelJSON string
		if scanErr := rows.Scan(&panelJSON); scanErr != nil {
			slog.Warn("Failed to scan panel row", "error", scanErr)
			continue
		}
		var panelData map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(panelJSON), &panelData); jsonErr != nil {
			slog.Warn("Failed to parse panel JSON", "error", jsonErr)
			continue
		}
		for key := range panelData {
			if strings.HasPrefix(key, viewPrefix) {
				composerIDs = append(composerIDs, strings.TrimPrefix(key, viewPrefix))
			}
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		slog.Warn("Error iterating panel rows", "error", rowsErr)
	}
	return composerIDs
}

// LoadComposerDataBatch loads multiple composers and their bubbles from the global database.
// Queries are chunked to composerBatchSize to stay within SQLite's expression-tree depth limit.
func LoadComposerDataBatch(globalDbPath string, composerIDs []string) (map[string]*ComposerData, error) {
	if len(composerIDs) == 0 {
		return map[string]*ComposerData{}, nil
	}

	db, err := OpenDatabase(globalDbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open global database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close global database", "error", closeErr)
		}
	}()

	loaded := composerRows{
		composers: make(map[string]*ComposerData),
		bubbles:   make(map[string]map[string]*ComposerConversation),
		pages:     make(map[string][]headerPage),
	}

	for i := 0; i < len(composerIDs); i += composerBatchSize {
		end := min(i+composerBatchSize, len(composerIDs))
		chunk := composerIDs[i:end]
		if err := loadComposerChunk(db, chunk, &loaded); err != nil {
			return nil, err
		}
	}

	mergeHeaderPages(loaded.composers, loaded.pages)
	assembleComposerConversations(loaded.composers, loaded.bubbles)

	slog.Debug("Loaded composers from global database", "composerCount", len(loaded.composers))
	return loaded.composers, nil
}

// composerRows accumulates the rows read across all chunks of LoadComposerDataBatch.
// The pages and bubbles can only be merged into the composers once every chunk has
// been read, so they are collected here first.
type composerRows struct {
	composers map[string]*ComposerData
	bubbles   map[string]map[string]*ComposerConversation // composerID -> bubbleID -> bubble
	pages     map[string][]headerPage                     // composerID -> unsorted header pages
}

// loadComposerChunk runs one bounded query for a slice of composer IDs and adds the
// rows it reads to loaded.
func loadComposerChunk(db *sql.DB, composerIDs []string, loaded *composerRows) error {
	placeholders := make([]string, len(composerIDs))
	likeConditions := make([]string, 0, len(composerIDs)*2)
	params := make([]any, 0, len(composerIDs)*3)

	for i, id := range composerIDs {
		placeholders[i] = "?"
		params = append(params, composerDataPrefix+id)
	}
	for _, id := range composerIDs {
		likeConditions = append(likeConditions, "key LIKE ?")
		params = append(params, bubbleIDPrefix+id+":%")
	}
	for _, id := range composerIDs {
		likeConditions = append(likeConditions, "key LIKE ?")
		params = append(params, convHeadersPrefix+id+":%")
	}

	query := fmt.Sprintf(`SELECT key, value FROM cursorDiskKV
		WHERE value IS NOT NULL AND (
			key IN (%s)
			OR (%s)
		)`,
		strings.Join(placeholders, ","),
		strings.Join(likeConditions, " OR "))

	slog.Debug("Executing composer chunk query", "composerCount", len(composerIDs))

	err := scanKV(db, query, params, func(key, valueJSON string) {
		switch {
		case strings.HasPrefix(key, composerDataPrefix):
			if composer := addComposerRow(loaded.composers, key, valueJSON); composer != nil {
				slog.Debug("Loaded composer", "composerID", strings.TrimPrefix(key, composerDataPrefix), "name", composer.Name)
			}
		case strings.HasPrefix(key, bubbleIDPrefix):
			addBubbleRow(loaded.bubbles, key, valueJSON)
		case strings.HasPrefix(key, convHeadersPrefix):
			addHeaderPage(loaded.pages, key, valueJSON)
		}
	})
	if err != nil {
		return fmt.Errorf("failed to load composer chunk: %w", err)
	}
	return nil
}

// addBubbleRow parses one bubbleId row into the per-composer bubbles map.
func addBubbleRow(bubbles map[string]map[string]*ComposerConversation, key, valueJSON string) {
	// SplitN keeps everything after the second colon as the bubble ID, so suffixed IDs
	// such as "<uuid>/s:0" stay intact.
	parts := strings.SplitN(key, ":", 3)
	if len(parts) != 3 {
		slog.Warn("Invalid bubble key format", "key", key)
		return
	}
	composerID, bubbleID := parts[1], parts[2]
	var bubble ComposerConversation
	if err := json.Unmarshal([]byte(valueJSON), &bubble); err != nil {
		slog.Warn("Failed to parse bubble data", "composerID", composerID, "bubbleID", bubbleID, "error", err)
		return
	}
	if bubbles[composerID] == nil {
		bubbles[composerID] = make(map[string]*ComposerConversation)
	}
	bubbles[composerID][bubbleID] = &bubble
	slog.Debug("Loaded bubble", "composerID", composerID, "bubbleID", bubbleID, "type", bubble.Type)
}

// assembleComposerConversations merges the per-bubble map into each composer's Conversation slice.
func assembleComposerConversations(composers map[string]*ComposerData, bubbles map[string]map[string]*ComposerConversation) {
	for composerID, composer := range composers {
		composerBubbles, exists := bubbles[composerID]
		if !exists {
			continue
		}
		if len(composer.FullConversationHeadersOnly) > 0 {
			composer.Conversation = make([]ComposerConversation, 0, len(composer.FullConversationHeadersOnly))
			for _, header := range composer.FullConversationHeadersOnly {
				if bubble, found := composerBubbles[header.BubbleID]; found {
					composer.Conversation = append(composer.Conversation, *bubble)
				}
			}
		} else {
			// Cursor 2 inline format: merge full bubble data (thinking, timing, etc.) into the
			// conversation array that was already embedded in the composerData row.
			for i := range composer.Conversation {
				bubbleID := composer.Conversation[i].BubbleID
				fullBubble, found := composerBubbles[bubbleID]
				if !found {
					continue
				}
				if fullBubble.Thinking != nil {
					composer.Conversation[i].Thinking = fullBubble.Thinking
				}
				if fullBubble.Text != "" {
					composer.Conversation[i].Text = fullBubble.Text
				}
				if fullBubble.TimingInfo != nil {
					composer.Conversation[i].TimingInfo = fullBubble.TimingInfo
				}
				if fullBubble.ModelInfo != nil {
					composer.Conversation[i].ModelInfo = fullBubble.ModelInfo
				}
				if fullBubble.ToolFormerData != nil {
					composer.Conversation[i].ToolFormerData = fullBubble.ToolFormerData
				}
			}
		}
	}
}

// OpenDatabaseReadWrite opens a SQLite database in read-write mode with controlled
// connection pooling. It calls spi.EnsureWALMode before returning so the database is
// in WAL mode when the caller starts writing.
func OpenDatabaseReadWrite(dbPath string) (*sql.DB, error) {
	if err := spi.EnsureWALMode(dbPath); err != nil {
		slog.Warn("Failed to ensure WAL mode before opening read-write database", "error", err)
	}
	db, err := sql.Open("sqlite", dbPath+"?"+spi.BusyTimeoutPragma)
	if err != nil {
		return nil, fmt.Errorf("failed to open database for writing: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Second)
	return db, nil
}

// InsertComposerSession writes a reconstructed composer session into the global state.vscdb
// within a single transaction: one composerData:* row for the metadata and one
// bubbleId:*:* row per conversation turn. An existing row with the same key is replaced
// so re-runs of the same session ID are idempotent.
// composerJSON is the raw JSON to store verbatim; the caller is responsible for serializing
// all required Cursor fields (see ReconstructSession for the full set).
func InsertComposerSession(db *sql.DB, composerID string, composerJSON []byte, bubbles []ComposerConversation) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	// Rollback after a successful Commit is a documented no-op (ErrTxDone), so it can
	// be deferred unconditionally instead of tracking a separate error flag.
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO cursorDiskKV (key, value) VALUES (?, ?)",
		composerDataPrefix+composerID, string(composerJSON),
	); err != nil {
		return fmt.Errorf("failed to insert composer data: %w", err)
	}

	for i := range bubbles {
		bubbleJSON, err := json.Marshal(&bubbles[i])
		if err != nil {
			return fmt.Errorf("failed to marshal bubble %s: %w", bubbles[i].BubbleID, err)
		}
		key := bubbleIDPrefix + composerID + ":" + bubbles[i].BubbleID
		if _, err := tx.Exec(
			"INSERT OR REPLACE INTO cursorDiskKV (key, value) VALUES (?, ?)",
			key, string(bubbleJSON),
		); err != nil {
			return fmt.Errorf("failed to insert bubble %s: %w", bubbles[i].BubbleID, err)
		}
	}

	return tx.Commit()
}

// ComposerHeadMeta holds the session metadata needed to register a reconstructed session
// in the global DB's two composer indexes: the JSON allComposers array and the
// composerHeaders SQL table. Both live in globalStorage/state.vscdb, not the workspace DB.
type ComposerHeadMeta struct {
	ComposerID    string
	Name          string
	CreatedAt     int64
	LastUpdatedAt int64
	// WorkspaceID is the workspace storage directory hash (workspaceIdentifier.id).
	// Used as the workspaceId column in the composerHeaders SQL table.
	WorkspaceID string
}

// AppendToSelectedComposerIDs adds composerID to the selectedComposerIds list in the
// workspace DB's "composer.composerData" key. This makes the reconstructed session appear
// as an already-open tab in Cursor's tab bar — a UX convenience so the session is visible
// immediately on open rather than requiring the user to find it in the sidebar list.
// Sidebar visibility itself comes from composer.composerHeaders in the global DB.
func AppendToSelectedComposerIDs(workspaceDbPath, composerID string) error {
	db, err := OpenDatabaseReadWrite(workspaceDbPath)
	if err != nil {
		return fmt.Errorf("failed to open workspace database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close workspace database after selectedComposerIds update", "error", closeErr)
		}
	}()

	// Read-modify-write: preserve all existing fields in composer.composerData and only
	// update selectedComposerIds, deduplicating the new ID.
	blob := make(map[string]json.RawMessage)
	var existing string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'composer.composerData'").Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to read composer.composerData: %w", err)
	}
	if existing != "" {
		if jsonErr := json.Unmarshal([]byte(existing), &blob); jsonErr != nil {
			slog.Warn("Failed to parse composer.composerData, starting fresh", "error", jsonErr)
			blob = make(map[string]json.RawMessage)
		}
	}

	var ids []string
	if raw, ok := blob["selectedComposerIds"]; ok {
		_ = json.Unmarshal(raw, &ids)
	}
	for _, id := range ids {
		if id == composerID {
			return nil // already present
		}
	}
	ids = append(ids, composerID)
	encoded, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("failed to marshal selectedComposerIds: %w", err)
	}
	blob["selectedComposerIds"] = encoded

	refsJSON, err := json.Marshal(blob)
	if err != nil {
		return fmt.Errorf("failed to marshal composer.composerData: %w", err)
	}
	if _, err := db.Exec(
		"INSERT OR REPLACE INTO ItemTable (key, value) VALUES ('composer.composerData', ?)",
		string(refsJSON),
	); err != nil {
		return fmt.Errorf("failed to write composer.composerData: %w", err)
	}
	return nil
}

// WriteGlobalComposerHeader registers a reconstructed session in the GLOBAL DB's two
// composer indexes, which Cursor keeps in sync and which serve different readers:
//
//  1. The "composer.composerHeaders" key in ItemTable (JSON, allComposers array).
//  2. The composerHeaders SQL table, keyed by composerId and filtered by workspaceId.
//
// The Agent sidebar lists sessions from the SQL table, so a session missing there is
// invisible in the sidebar even though it loads correctly when opened directly by ID
// (e.g. via selectedComposerIds). Both writes therefore happen in one transaction.
// Entries in either index MUST have a non-empty "name" or Cursor silently hides them.
func WriteGlobalComposerHeader(globalDbPath string, meta ComposerHeadMeta, workspaceRoot string) error {
	db, err := OpenDatabaseReadWrite(globalDbPath)
	if err != nil {
		return fmt.Errorf("failed to open global database for composer header: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close global database after writing composer header", "error", closeErr)
		}
	}()

	// The two indexes must move together — a failure after the first write would
	// leave them diverged (a session listed in one index but not the other), so the
	// JSON read-modify-write and the table upsert run in one transaction and commit
	// only after both succeed.
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction for composer header: %w", err)
	}
	// Rollback after Commit is a no-op; deferring it releases the transaction on
	// every early error return below.
	defer func() { _ = tx.Rollback() }()

	// Read the existing blob; handle missing key gracefully.
	var raw string
	blob := make(map[string]json.RawMessage)
	err = tx.QueryRow("SELECT value FROM ItemTable WHERE key = 'composer.composerHeaders'").Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to read composer.composerHeaders: %w", err)
	}
	if raw != "" {
		if jsonErr := json.Unmarshal([]byte(raw), &blob); jsonErr != nil {
			slog.Warn("Failed to parse composer.composerHeaders, starting fresh", "error", jsonErr)
			blob = make(map[string]json.RawMessage)
		}
	}

	var allComposers []json.RawMessage
	if rawArr, ok := blob["allComposers"]; ok {
		_ = json.Unmarshal(rawArr, &allComposers)
	}

	// Track whether the blob already carries this composer (idempotent re-runs); the
	// table insert below is INSERT OR REPLACE, so it is idempotent on its own.
	alreadyInBlob := false
	for _, entry := range allComposers {
		var ref struct {
			ComposerID string `json:"composerId"`
		}
		if json.Unmarshal(entry, &ref) == nil && ref.ComposerID == meta.ComposerID {
			alreadyInBlob = true
			break
		}
	}

	headEntry := map[string]interface{}{
		"type":                      "head",
		"composerId":                meta.ComposerID,
		"name":                      meta.Name,
		"createdAt":                 meta.CreatedAt,
		"lastUpdatedAt":             meta.LastUpdatedAt,
		"unifiedMode":               "agent",
		"forceMode":                 "edit",
		"hasUnreadMessages":         false,
		"totalLinesAdded":           0,
		"totalLinesRemoved":         0,
		"filesChangedCount":         0,
		"subtitle":                  "",
		"hasBlockingPendingActions": false,
		"hasPendingPlan":            false,
		"isArchived":                false,
		"isDraft":                   false,
		"isWorktree":                false,
		"worktreeStartedReadOnly":   false,
		"isSpec":                    false,
		"isProject":                 false,
		"isBestOfNSubcomposer":      false,
		"numSubComposers":           0,
		"referencedPlans":           []interface{}{},
		"trackedGitRepos":           []interface{}{},
		"workspaceIdentifier": map[string]interface{}{
			"id":  meta.WorkspaceID,
			"uri": vscode.WorkspaceURIMap(workspaceRoot),
		},
	}

	entryJSON, err := json.Marshal(headEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal composer header entry: %w", err)
	}

	if !alreadyInBlob {
		// Prepend the new entry to match how Cursor itself maintains this array
		// (newest first); the sidebar's display order comes from lastUpdatedAt,
		// not from array position.
		allComposers = append([]json.RawMessage{entryJSON}, allComposers...)
		encoded, mErr := json.Marshal(allComposers)
		if mErr != nil {
			return fmt.Errorf("failed to marshal allComposers: %w", mErr)
		}
		blob["allComposers"] = encoded

		dataJSON, mErr := json.Marshal(blob)
		if mErr != nil {
			return fmt.Errorf("failed to marshal composer.composerHeaders: %w", mErr)
		}
		if _, execErr := tx.Exec(
			"INSERT OR REPLACE INTO ItemTable (key, value) VALUES ('composer.composerHeaders', ?)",
			string(dataJSON),
		); execErr != nil {
			return fmt.Errorf("failed to write composer.composerHeaders: %w", execErr)
		}
	}

	// Cursor >= 3.12 (composer.composerHeaders.tableGateEnabled) reads headers from the
	// dedicated composerHeaders TABLE instead of the JSON key above — the sidebar joins
	// that table against the glass membership map, so the session is invisible in newer
	// Cursors without this row. The table is created by Cursor itself; when absent
	// (older versions) the JSON key is the only source and the insert is skipped.
	if err := insertComposerHeaderRow(tx, meta, entryJSON); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit composer header writes: %w", err)
	}

	// Checkpoint the WAL so our write lands in the main DB file before Cursor opens it.
	// In WAL mode SQLite flushes commits lazily; without this Cursor may read a pre-write
	// snapshot and only see the session after its own first-open checkpoint.
	if _, err := db.Exec("PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
		slog.Warn("WAL checkpoint failed on global database", "error", err)
	}
	return nil
}

// insertComposerHeaderRow writes the head entry into the composerHeaders SQL table when
// it exists (see the call site in WriteGlobalComposerHeader for why). Column values
// mirror what Cursor writes for its own sessions: recency and checkpointAt track
// lastUpdatedAt, and isSubagent/isArchived are 0 for a normal visible session.
func insertComposerHeaderRow(tx *sql.Tx, meta ComposerHeadMeta, headJSON []byte) error {
	var tableName string
	err := tx.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='composerHeaders'").Scan(&tableName)
	if errors.Is(err, sql.ErrNoRows) {
		slog.Debug("composerHeaders table not present, skipping table registration (pre-3.12 Cursor)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to check for composerHeaders table: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO composerHeaders
			(composerId, workspaceId, createdAt, lastUpdatedAt, isArchived, isSubagent, recency, checkpointAt, value)
			VALUES (?, ?, ?, ?, 0, 0, ?, ?, ?)`,
		meta.ComposerID, meta.WorkspaceID, meta.CreatedAt, meta.LastUpdatedAt,
		meta.LastUpdatedAt, meta.LastUpdatedAt, string(headJSON),
	); err != nil {
		return fmt.Errorf("failed to insert composerHeaders row: %w", err)
	}

	slog.Debug("Inserted composerHeaders table row", "composerID", meta.ComposerID)
	return nil
}

// glassProjectsKey and glassMembershipKey are the ItemTable keys behind the Agent
// sidebar in Cursor >= 3.12 ("glass" is Cursor's name for that UI layer): the first
// holds project entities keyed by workspace, the second maps composerID -> projectID.
// Newer Cursors build the sidebar from these — composer.composerHeaders is no longer
// read for it — so a reconstructed session must be registered here or it stays
// invisible. Older Cursor versions ignore both keys, so writing them is always safe.
const (
	glassProjectsKey   = "glass.localAgentProjects.v1"
	glassMembershipKey = "glass.localAgentProjectMembership.v1"
)

// glassProject mirrors the fields of a glass.localAgentProjects.v1 entry needed for
// matching; existing entries are carried through verbatim as raw JSON when rewritten.
type glassProject struct {
	ID        string `json:"id"`
	Workspace struct {
		ID  string `json:"id"`
		URI struct {
			FsPath string `json:"fsPath"`
		} `json:"uri"`
	} `json:"workspace"`
}

// RegisterGlassProjectMembership adds a composer to the project-membership map that
// backs the Agent sidebar in Cursor >= 3.12, creating a project entry for the workspace
// when none exists yet (matched by workspace storage ID, then by canonical fsPath).
//
// Caveat: like every ItemTable write made while Cursor is running, a running instance
// can flush its own in-memory copy of these keys over ours. The resume flow already
// tells the user to restart Cursor; registration is reliable when Cursor is not
// running or is restarted afterwards.
func RegisterGlassProjectMembership(globalDbPath, composerID, workspaceID, workspaceRoot string) error {
	db, err := OpenDatabaseReadWrite(globalDbPath)
	if err != nil {
		return fmt.Errorf("failed to open global database for glass registration: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close global database after glass registration", "error", closeErr)
		}
	}()

	// Resolve (or create) the project entry for this workspace. Unknown fields of
	// existing entries are preserved by keeping them as raw JSON.
	var rawProjects []json.RawMessage
	var projectsRaw string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", glassProjectsKey).Scan(&projectsRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to read %s: %w", glassProjectsKey, err)
	}
	if projectsRaw != "" {
		if jsonErr := json.Unmarshal([]byte(projectsRaw), &rawProjects); jsonErr != nil {
			slog.Warn("Failed to parse glass projects, starting fresh", "error", jsonErr)
			rawProjects = nil
		}
	}

	canonicalRoot, cErr := vscode.NormalizePathForComparison(workspaceRoot)
	if cErr != nil {
		canonicalRoot = workspaceRoot
	}
	projectID := ""
	for _, entry := range rawProjects {
		var project glassProject
		if json.Unmarshal(entry, &project) != nil {
			continue
		}
		if project.Workspace.ID == workspaceID {
			projectID = project.ID
			break
		}
		if project.Workspace.URI.FsPath != "" {
			if canonical, pErr := vscode.NormalizePathForComparison(project.Workspace.URI.FsPath); pErr == nil && canonical == canonicalRoot {
				projectID = project.ID
				break
			}
		}
	}

	if projectID == "" {
		projectID = uuid.NewString()
		nowMs := time.Now().UnixMilli()
		// "New Project" mirrors the placeholder name Cursor writes for its own
		// freshly-created project entries.
		newEntry := map[string]interface{}{
			"id":   projectID,
			"name": "New Project",
			"workspace": map[string]interface{}{
				"id":  workspaceID,
				"uri": vscode.WorkspaceURIMap(workspaceRoot),
			},
			"createdAt":     nowMs,
			"lastUpdatedAt": nowMs,
			"isArchived":    false,
		}
		entryJSON, mErr := json.Marshal(newEntry)
		if mErr != nil {
			return fmt.Errorf("failed to marshal glass project entry: %w", mErr)
		}
		rawProjects = append(rawProjects, entryJSON)
		projectsJSON, mErr := json.Marshal(rawProjects)
		if mErr != nil {
			return fmt.Errorf("failed to marshal glass projects: %w", mErr)
		}
		if _, execErr := db.Exec(
			"INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)",
			glassProjectsKey, string(projectsJSON),
		); execErr != nil {
			return fmt.Errorf("failed to write %s: %w", glassProjectsKey, execErr)
		}
		slog.Debug("Created glass project entry for workspace",
			"projectID", projectID, "workspaceID", workspaceID)
	}

	// Add the composer -> project mapping, preserving all existing entries verbatim.
	membership := make(map[string]json.RawMessage)
	var membershipRaw string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", glassMembershipKey).Scan(&membershipRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to read %s: %w", glassMembershipKey, err)
	}
	if membershipRaw != "" {
		if jsonErr := json.Unmarshal([]byte(membershipRaw), &membership); jsonErr != nil {
			slog.Warn("Failed to parse glass membership, starting fresh", "error", jsonErr)
			membership = make(map[string]json.RawMessage)
		}
	}
	encodedProjectID, err := json.Marshal(projectID)
	if err != nil {
		return fmt.Errorf("failed to marshal project ID: %w", err)
	}
	membership[composerID] = encodedProjectID
	membershipJSON, err := json.Marshal(membership)
	if err != nil {
		return fmt.Errorf("failed to marshal glass membership: %w", err)
	}
	if _, err := db.Exec(
		"INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)",
		glassMembershipKey, string(membershipJSON),
	); err != nil {
		return fmt.Errorf("failed to write %s: %w", glassMembershipKey, err)
	}

	// Checkpoint so the write lands in the main DB file before Cursor next opens it
	// (same rationale as WriteGlobalComposerHeader).
	if _, err := db.Exec("PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
		slog.Warn("WAL checkpoint failed after glass registration", "error", err)
	}

	slog.Info("Registered composer in glass sidebar",
		"composerID", composerID, "projectID", projectID)
	return nil
}

// LoadAllComposerDataLightweight loads composer records for all sessions in the global database.
// Unlike LoadComposerDataBatch, it only reads composerData:* keys — no bubble records — so it
// is fast enough to run on every watcher check. Inline conversation turns are included when
// present (Cursor 2 format); for Cursor 3 only the inline header list is returned and
// slug/name derivation falls back to the composer Name field. Cursor >= 3.27.14 composers
// come back with an empty header list; callers that need to tell an empty composer from a
// populated one must use LoadAllComposerDataWithHeaders instead.
func LoadAllComposerDataLightweight(globalDbPath string) (map[string]*ComposerData, error) {
	return loadAllComposerData(globalDbPath, false)
}

// LoadAllComposerDataWithHeaders is LoadAllComposerDataLightweight plus the paged
// composerConvHeaders rows, merged into FullConversationHeadersOnly. It is kept separate
// because the pages span the whole chat history: discovery and watcher seeding only need
// workspace associations and timestamps, so they shouldn't pay to decode every page.
func LoadAllComposerDataWithHeaders(globalDbPath string) (map[string]*ComposerData, error) {
	return loadAllComposerData(globalDbPath, true)
}

// loadAllComposerData is the shared body of the two global loaders.
func loadAllComposerData(globalDbPath string, withHeaderPages bool) (map[string]*ComposerData, error) {
	db, err := OpenDatabase(globalDbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open global database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close global database", "error", closeErr)
		}
	}()

	const prefixQuery = "SELECT key, value FROM cursorDiskKV WHERE key LIKE ? AND value IS NOT NULL"

	composers := make(map[string]*ComposerData)
	err = scanKV(db, prefixQuery, []any{composerDataPrefix + "%"}, func(key, valueJSON string) {
		addComposerRow(composers, key, valueJSON)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load composer data: %w", err)
	}

	// Cursor >= 3.27.14 leaves fullConversationHeadersOnly empty and pages the headers
	// into composerConvHeaders rows. Without them, callers that use the header list to
	// tell an empty composer from a populated one would skip every new session.
	if withHeaderPages {
		pages := make(map[string][]headerPage)
		err := scanKV(db, prefixQuery, []any{convHeadersPrefix + "%"}, func(key, valueJSON string) {
			addHeaderPage(pages, key, valueJSON)
		})
		if err != nil {
			return nil, fmt.Errorf("failed to load conversation header pages: %w", err)
		}
		mergeHeaderPages(composers, pages)
	}

	slog.Debug("Loaded global composer data", "count", len(composers), "withHeaderPages", withHeaderPages)
	return composers, nil
}
