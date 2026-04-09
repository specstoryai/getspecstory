package cursoride

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// composerBatchSize is the maximum number of composer IDs per SQL query.
// SQLite limits expression tree depth to ~1000. Each composer ID contributes
// two expressions (one IN entry + one LIKE condition), so 200 IDs keeps us
// well under that limit.
const composerBatchSize = 200

// OpenDatabase opens a SQLite database in read-only mode
func OpenDatabase(dbPath string) (*sql.DB, error) {
	// Open in read-only mode
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	slog.Debug("Successfully opened database", "path", dbPath)

	// Enable WAL mode for non-blocking reads (using cursorcli's simpler approach)
	// This prevents readers from blocking Cursor IDE's writers
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		// Log warning but continue - not fatal if WAL fails
		slog.Warn("Failed to enable WAL mode", "error", err)
	} else {
		slog.Debug("Enabled WAL mode for non-blocking reads")
	}

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

// LoadComposerDataBatch loads multiple composers and their bubbles from the global database
// This is the main function for workspace-filtered loading
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
	bubbles := make(map[string]map[string]*ComposerConversation) // composerID -> bubbleID -> bubble

	// Process IDs in chunks to stay within SQLite's expression tree depth limit.
	// See composerBatchSize for the reasoning behind the chunk size.
	for start := 0; start < len(composerIDs); start += composerBatchSize {
		end := start + composerBatchSize
		if end > len(composerIDs) {
			end = len(composerIDs)
		}
		chunk := composerIDs[start:end]

		slog.Debug("Processing composer chunk",
			"start", start,
			"end", end,
			"total", len(composerIDs))

		chunkComposers, chunkBubbles, err := queryComposerChunk(db, chunk)
		if err != nil {
			return nil, fmt.Errorf("failed to query composer chunk [%d:%d]: %w", start, end, err)
		}

		// Merge chunk results into the overall maps
		for id, c := range chunkComposers {
			composers[id] = c
		}
		for id, b := range chunkBubbles {
			bubbles[id] = b
		}
	}

	// Assemble conversations: merge bubbles into composers
	for composerID, composer := range composers {
		if composerBubbles, exists := bubbles[composerID]; exists {
			// If composer has fullConversationHeadersOnly, load those bubbles into conversation array
			if len(composer.FullConversationHeadersOnly) > 0 {
				composer.Conversation = make([]ComposerConversation, 0, len(composer.FullConversationHeadersOnly))
				for _, header := range composer.FullConversationHeadersOnly {
					if bubble, found := composerBubbles[header.BubbleID]; found {
						composer.Conversation = append(composer.Conversation, *bubble)
					}
				}
			} else if len(composer.Conversation) > 0 {
				// Composer has a conversation array - merge individual bubble data into it
				// The composerData record may have basic bubble info, but individual bubble
				// records have the complete data (including thinking blocks)
				for i := range composer.Conversation {
					bubbleID := composer.Conversation[i].BubbleID
					if fullBubble, found := composerBubbles[bubbleID]; found {
						// Merge the full bubble data into the conversation array
						// Keep the original if fields are not set in the full bubble
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
	}

	slog.Info("Loaded composers from global database",
		"composerCount", len(composers))

	return composers, nil
}

// queryComposerChunk executes a single batch query for a slice of composer IDs against
// an already-open database. It returns the raw composers and bubbles maps before assembly.
// Callers are responsible for keeping the chunk size within SQLite's expression tree limit.
func queryComposerChunk(db *sql.DB, composerIDs []string) (map[string]*ComposerData, map[string]map[string]*ComposerConversation, error) {
	// Part 1: Build IN clause for composerData keys
	composerDataKeys := make([]string, len(composerIDs))
	composerDataPlaceholders := make([]string, len(composerIDs))
	for i, id := range composerIDs {
		composerDataKeys[i] = "composerData:" + id
		composerDataPlaceholders[i] = "?"
	}

	// Part 2: Build LIKE conditions for bubbleId keys
	bubbleLikeConditions := make([]string, len(composerIDs))
	for i := range composerIDs {
		bubbleLikeConditions[i] = "key LIKE ?"
	}

	// Build the complete query
	query := fmt.Sprintf(`SELECT key, value FROM cursorDiskKV
		WHERE value IS NOT NULL AND (
			key IN (%s)
			OR (%s)
		)`,
		strings.Join(composerDataPlaceholders, ","),
		strings.Join(bubbleLikeConditions, " OR "))

	// Build params array: first all composerData keys, then all bubbleId patterns
	params := make([]interface{}, 0, len(composerIDs)*2)
	for _, key := range composerDataKeys {
		params = append(params, key)
	}
	for _, id := range composerIDs {
		params = append(params, "bubbleId:"+id+":%")
	}

	slog.Debug("Executing batch query chunk",
		"composerCount", len(composerIDs))

	rows, err := db.Query(query, params...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query composer data: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close query rows", "error", closeErr)
		}
	}()

	composers := make(map[string]*ComposerData)
	bubbles := make(map[string]map[string]*ComposerConversation) // composerID -> bubbleID -> bubble

	for rows.Next() {
		var key, valueJSON string
		if err := rows.Scan(&key, &valueJSON); err != nil {
			slog.Warn("Failed to scan row", "error", err)
			continue
		}

		// Determine key type and parse accordingly
		if strings.HasPrefix(key, "composerData:") {
			// This is a composer record
			composerID := strings.TrimPrefix(key, "composerData:")

			var composer ComposerData
			if err := json.Unmarshal([]byte(valueJSON), &composer); err != nil {
				slog.Warn("Failed to parse composer data",
					"composerID", composerID,
					"error", err)
				continue
			}

			composers[composerID] = &composer
			slog.Debug("Loaded composer",
				"composerID", composerID,
				"name", composer.Name)

		} else if strings.HasPrefix(key, "bubbleId:") {
			// This is a bubble record: bubbleId:{composerID}:{bubbleID}
			parts := strings.SplitN(key, ":", 3)
			if len(parts) != 3 {
				slog.Warn("Invalid bubble key format", "key", key)
				continue
			}

			composerID := parts[1]
			bubbleID := parts[2]

			var bubble ComposerConversation
			if err := json.Unmarshal([]byte(valueJSON), &bubble); err != nil {
				slog.Warn("Failed to parse bubble data",
					"composerID", composerID,
					"bubbleID", bubbleID,
					"error", err)
				continue
			}

			// Initialize bubble map for this composer if needed
			if bubbles[composerID] == nil {
				bubbles[composerID] = make(map[string]*ComposerConversation)
			}
			bubbles[composerID][bubbleID] = &bubble

			slog.Debug("Loaded bubble",
				"composerID", composerID,
				"bubbleID", bubbleID,
				"type", bubble.Type)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return composers, bubbles, nil
}
