package cursoride

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// composerBatchSize is the maximum number of composer IDs per SQL query.
// SQLite limits expression tree depth to ~1000. Each composer ID contributes
// two expressions (one IN entry + one LIKE condition), so 200 IDs keeps us
// well under that limit.
const composerBatchSize = 200

// OpenDatabase opens a SQLite database in read-only mode with controlled
// connection pooling. Callers that need WAL mode guaranteed should call
// EnsureWALMode once before using this function (e.g. at watcher startup).
func OpenDatabase(dbPath string) (*sql.DB, error) {
	// Open in read-only mode
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
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

// EnsureWALMode ensures the database is running in WAL journal mode.
// WAL mode is required so that the -wal file exists for fsnotify to watch.
// This must be called with a read-write connection because PRAGMA journal_mode
// cannot change the mode on a read-only connection. It is intended to be called
// once at watcher startup, not on every query.
func EnsureWALMode(dbPath string) error {
	// Open read-write to be able to change journal mode
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database for WAL check: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close database after WAL check", "error", closeErr)
		}
	}()

	// Check current journal mode
	var currentMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&currentMode); err != nil {
		return fmt.Errorf("failed to query journal mode: %w", err)
	}

	if strings.EqualFold(currentMode, "wal") {
		slog.Debug("Database already in WAL mode", "path", dbPath)
		return nil
	}

	// Switch to WAL mode
	var newMode string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&newMode); err != nil {
		return fmt.Errorf("failed to set WAL mode: %w", err)
	}

	if !strings.EqualFold(newMode, "wal") {
		return fmt.Errorf("failed to enable WAL mode: got %q instead", newMode)
	}

	slog.Info("Enabled WAL mode on database", "path", dbPath)
	return nil
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
	if err != nil && err != sql.ErrNoRows {
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

	// Method 2: workbench.panel.composerChatViewPane entries
	// In Cursor 3 these are the authoritative source for all conversation IDs.
	// Each JSON value is a map whose keys follow the pattern
	// "workbench.panel.aichat.view.{composerUUID}".
	// Two key formats exist:
	//   - "workbench.panel.composerChatViewPane" (no suffix) — the main panel shared by all tabs
	//   - "workbench.panel.composerChatViewPane.{paneUUID}" — one entry per open tab/pane
	rows, queryErr := db.Query(
		"SELECT value FROM ItemTable WHERE (key = 'workbench.panel.composerChatViewPane' OR (key LIKE 'workbench.panel.composerChatViewPane.%' AND key NOT LIKE '%.hidden'))",
	)
	if queryErr != nil {
		slog.Warn("Failed to query workspace panel entries", "error", queryErr)
	} else {
		defer func() {
			if closeErr := rows.Close(); closeErr != nil {
				slog.Warn("Failed to close panel query rows", "error", closeErr)
			}
		}()

		const viewPrefix = "workbench.panel.aichat.view."
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
					composerID := strings.TrimPrefix(key, viewPrefix)
					if !seen[composerID] {
						seen[composerID] = true
						composerIDs = append(composerIDs, composerID)
					}
				}
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			slog.Warn("Error iterating panel rows", "error", rowsErr)
		}
	}

	slog.Debug("Loaded composer IDs from workspace",
		"count", len(composerIDs),
		"composerIDs", composerIDs)

	return composerIDs, nil
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

	composers := make(map[string]*ComposerData)
	bubbles := make(map[string]map[string]*ComposerConversation)

	for i := 0; i < len(composerIDs); i += composerBatchSize {
		end := min(i+composerBatchSize, len(composerIDs))
		chunk := composerIDs[i:end]
		if err := loadComposerChunk(db, chunk, composers, bubbles); err != nil {
			return nil, err
		}
	}

	assembleComposerConversations(composers, bubbles)

	slog.Debug("Loaded composers from global database", "composerCount", len(composers))
	return composers, nil
}

// loadComposerChunk runs one bounded query for a slice of composer IDs and merges
// the results into the caller-provided composers and bubbles maps.
func loadComposerChunk(db *sql.DB, composerIDs []string, composers map[string]*ComposerData, bubbles map[string]map[string]*ComposerConversation) error {
	placeholders := make([]string, len(composerIDs))
	likeConditions := make([]string, len(composerIDs))
	params := make([]interface{}, 0, len(composerIDs)*2)

	for i, id := range composerIDs {
		placeholders[i] = "?"
		likeConditions[i] = "key LIKE ?"
		params = append(params, "composerData:"+id)
	}
	for _, id := range composerIDs {
		params = append(params, "bubbleId:"+id+":%")
	}

	query := fmt.Sprintf(`SELECT key, value FROM cursorDiskKV
		WHERE value IS NOT NULL AND (
			key IN (%s)
			OR (%s)
		)`,
		strings.Join(placeholders, ","),
		strings.Join(likeConditions, " OR "))

	slog.Debug("Executing composer chunk query", "composerCount", len(composerIDs))

	rows, err := db.Query(query, params...)
	if err != nil {
		return fmt.Errorf("failed to query composer chunk: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close chunk query rows", "error", closeErr)
		}
	}()

	for rows.Next() {
		var key, valueJSON string
		if err := rows.Scan(&key, &valueJSON); err != nil {
			slog.Warn("Failed to scan row", "error", err)
			continue
		}

		if strings.HasPrefix(key, "composerData:") {
			composerID := strings.TrimPrefix(key, "composerData:")
			var composer ComposerData
			if err := json.Unmarshal([]byte(valueJSON), &composer); err != nil {
				slog.Warn("Failed to parse composer data", "composerID", composerID, "error", err)
				continue
			}
			composers[composerID] = &composer
			slog.Debug("Loaded composer", "composerID", composerID, "name", composer.Name)
		} else if strings.HasPrefix(key, "bubbleId:") {
			parts := strings.SplitN(key, ":", 3)
			if len(parts) != 3 {
				slog.Warn("Invalid bubble key format", "key", key)
				continue
			}
			composerID, bubbleID := parts[1], parts[2]
			var bubble ComposerConversation
			if err := json.Unmarshal([]byte(valueJSON), &bubble); err != nil {
				slog.Warn("Failed to parse bubble data", "composerID", composerID, "bubbleID", bubbleID, "error", err)
				continue
			}
			if bubbles[composerID] == nil {
				bubbles[composerID] = make(map[string]*ComposerConversation)
			}
			bubbles[composerID][bubbleID] = &bubble
			slog.Debug("Loaded bubble", "composerID", composerID, "bubbleID", bubbleID, "type", bubble.Type)
		}
	}
	return rows.Err()
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
// connection pooling. It calls EnsureWALMode before returning so the database is
// in WAL mode when the caller starts writing.
func OpenDatabaseReadWrite(dbPath string) (*sql.DB, error) {
	if err := EnsureWALMode(dbPath); err != nil {
		slog.Warn("Failed to ensure WAL mode before opening read-write database", "error", err)
	}
	db, err := sql.Open("sqlite", dbPath)
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
	var txErr error
	defer func() {
		if txErr != nil {
			_ = tx.Rollback()
		}
	}()

	if _, txErr = tx.Exec(
		"INSERT OR REPLACE INTO cursorDiskKV (key, value) VALUES (?, ?)",
		"composerData:"+composerID, string(composerJSON),
	); txErr != nil {
		return fmt.Errorf("failed to insert composer data: %w", txErr)
	}

	for i := range bubbles {
		bubbleJSON, err := json.Marshal(&bubbles[i])
		if err != nil {
			txErr = fmt.Errorf("failed to marshal bubble %s: %w", bubbles[i].BubbleID, err)
			return txErr
		}
		key := "bubbleId:" + composerID + ":" + bubbles[i].BubbleID
		if _, txErr = tx.Exec(
			"INSERT OR REPLACE INTO cursorDiskKV (key, value) VALUES (?, ?)",
			key, string(bubbleJSON),
		); txErr != nil {
			return fmt.Errorf("failed to insert bubble %s: %w", bubbles[i].BubbleID, txErr)
		}
	}

	txErr = tx.Commit()
	return txErr
}

// ComposerHeadMeta holds the session metadata needed to register a reconstructed session
// in the workspace-level indexes (both the JSON allComposers array and the SQL composerHeaders table).
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
	if err != nil && err != sql.ErrNoRows {
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

// WriteGlobalComposerHeader adds a lightweight "head" entry for the reconstructed session to
// the "composer.composerHeaders" key in the GLOBAL DB's ItemTable. This is the authoritative
// source from which composerDataService.allComposersData.allComposers is populated on startup.
// Cursor's Agent sidebar SWC component reads allComposersData.allComposers and filters by name,
// so the entry MUST have a non-empty "name" field or it is silently hidden.
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

	// Read the existing blob; handle missing key gracefully.
	var raw string
	blob := make(map[string]json.RawMessage)
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'composer.composerHeaders'").Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
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

	// Skip if already registered (idempotent).
	for _, entry := range allComposers {
		var ref struct {
			ComposerID string `json:"composerId"`
		}
		if json.Unmarshal(entry, &ref) == nil && ref.ComposerID == meta.ComposerID {
			return nil
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
			"id": meta.WorkspaceID,
			"uri": map[string]interface{}{
				"$mid":     1,
				"fsPath":   workspaceRoot,
				"external": "file://" + workspaceRoot,
				"path":     workspaceRoot,
				"scheme":   "file",
			},
		},
	}

	entryJSON, err := json.Marshal(headEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal composer header entry: %w", err)
	}

	// Prepend so the newest session sorts to the top (Cursor sorts by lastUpdatedAt DESC).
	allComposers = append([]json.RawMessage{entryJSON}, allComposers...)
	encoded, err := json.Marshal(allComposers)
	if err != nil {
		return fmt.Errorf("failed to marshal allComposers: %w", err)
	}
	blob["allComposers"] = encoded

	dataJSON, err := json.Marshal(blob)
	if err != nil {
		return fmt.Errorf("failed to marshal composer.composerHeaders: %w", err)
	}
	if _, err := db.Exec(
		"INSERT OR REPLACE INTO ItemTable (key, value) VALUES ('composer.composerHeaders', ?)",
		string(dataJSON),
	); err != nil {
		return fmt.Errorf("failed to write composer.composerHeaders: %w", err)
	}

	// Checkpoint the WAL so our write lands in the main DB file before Cursor opens it.
	// In WAL mode SQLite flushes commits lazily; without this Cursor may read a pre-write
	// snapshot and only see the session after its own first-open checkpoint.
	if _, err := db.Exec("PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
		slog.Warn("WAL checkpoint failed on global database", "error", err)
	}
	return nil
}

// LoadAllComposerDataLightweight loads composer records for all sessions in the global database.
// Unlike LoadComposerDataBatch, it only reads composerData:* keys — no bubble records — so it
// is fast enough for global enumeration (ListAllAgentChatSessions). Inline conversation turns
// are included when present (Cursor 2 format); for Cursor 3 only the header list is returned
// and slug/name derivation falls back to the composer Name field.
func LoadAllComposerDataLightweight(globalDbPath string) (map[string]*ComposerData, error) {
	db, err := OpenDatabase(globalDbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open global database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close global database", "error", closeErr)
		}
	}()

	rows, err := db.Query("SELECT key, value FROM cursorDiskKV WHERE key LIKE 'composerData:%' AND value IS NOT NULL")
	if err != nil {
		return nil, fmt.Errorf("failed to query composer data: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close query rows", "error", closeErr)
		}
	}()

	composers := make(map[string]*ComposerData)
	for rows.Next() {
		var key, valueJSON string
		if err := rows.Scan(&key, &valueJSON); err != nil {
			slog.Warn("Failed to scan composer row", "error", err)
			continue
		}
		composerID := strings.TrimPrefix(key, "composerData:")
		var composer ComposerData
		if err := json.Unmarshal([]byte(valueJSON), &composer); err != nil {
			slog.Warn("Failed to parse composer data", "composerID", composerID, "error", err)
			continue
		}
		composers[composerID] = &composer
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating composer rows: %w", err)
	}

	slog.Debug("Loaded lightweight composer data from global database", "count", len(composers))
	return composers, nil
}
