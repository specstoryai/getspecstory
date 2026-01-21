package cursoride

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

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

// LoadWorkspaceComposerIDs loads the composer IDs from a workspace database
// Returns a list of composer IDs that belong to this workspace
func LoadWorkspaceComposerIDs(workspaceDbPath string) ([]string, error) {
	db, err := OpenDatabase(workspaceDbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open workspace database: %w", err)
	}
	defer db.Close()

	// Query for the composer.composerData key in the ItemTable
	var valueJSON string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", "composer.composerData").Scan(&valueJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			// No composer data in this workspace
			slog.Debug("No composer data found in workspace database")
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to query workspace composer data: %w", err)
	}

	// Parse the JSON value
	var composerRefs WorkspaceComposerRefs
	if err := json.Unmarshal([]byte(valueJSON), &composerRefs); err != nil {
		return nil, fmt.Errorf("failed to parse workspace composer refs: %w", err)
	}

	// Extract composer IDs
	composerIDs := make([]string, 0, len(composerRefs.AllComposers))
	for _, ref := range composerRefs.AllComposers {
		composerIDs = append(composerIDs, ref.ComposerID)
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
	defer db.Close()

	// Build SQL query with placeholders for composer IDs
	// We need to query for both composerData:* keys and bubbleId:* keys

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

	slog.Debug("Executing batch query",
		"composerCount", len(composerIDs),
		"query", query)

	// Execute the query
	rows, err := db.Query(query, params...)
	if err != nil {
		return nil, fmt.Errorf("failed to query composer data: %w", err)
	}
	defer rows.Close()

	// Parse results
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
		return nil, fmt.Errorf("error iterating rows: %w", err)
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
			}
		}
	}

	slog.Info("Loaded composers from global database",
		"composerCount", len(composers))

	return composers, nil
}
