package cursorcli

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// BlobRecord represents a raw blob from the Cursor SQLite database
type BlobRecord struct {
	RowID int             `json:"rowid"`
	ID    string          `json:"id"`
	Data  json.RawMessage `json:"data"` // Keep as raw JSON to preserve structure
}

// extractSlugFromBlobs extracts a slug from the first suitable user message
func extractSlugFromBlobs(blobRecords []BlobRecord) string {
	// Regex to match non-alphanumeric characters
	nonAlphaNum := regexp.MustCompile(`[^a-zA-Z0-9]+`)

	for _, record := range blobRecords {
		// Try to parse the data to check role and content
		var data struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}

		if err := json.Unmarshal(record.Data, &data); err != nil {
			continue // Skip if can't parse
		}

		// Check if this is a user message
		if data.Role != "user" {
			continue
		}

		// Check if we have content with text
		if len(data.Content) == 0 || data.Content[0].Type != "text" {
			continue
		}

		text := data.Content[0].Text

		// Strip <user_query> tags if present
		text = stripUserQueryTags(text)

		// Skip if starts with <user_info> (may be JSON-encoded as \u003cuser_info\u003e)
		if strings.HasPrefix(text, "<user_info>") || strings.HasPrefix(text, "\u003cuser_info\u003e") {
			continue
		}

		// Extract first 4 words
		words := strings.Fields(text)
		if len(words) == 0 {
			continue
		}

		// Take up to 4 words
		if len(words) > 4 {
			words = words[:4]
		}

		// Join words and convert to slug format
		slug := strings.Join(words, " ")
		slug = strings.ToLower(slug)
		slug = nonAlphaNum.ReplaceAllString(slug, "-")
		slug = strings.Trim(slug, "-") // Remove leading/trailing hyphens

		slog.Debug("Extracted slug from user message", "slug", slug, "rowid", record.RowID)
		return slug
	}

	slog.Debug("No suitable user message found for slug extraction")
	return ""
}

// validateCursorDatabase validates that the SQLite database has the expected Cursor schema.
// This ensures we're working with a genuine Cursor database and not some other SQLite file.
func validateCursorDatabase(db *sql.DB) error {
	// Define the expected schema for a Cursor database
	type tableSchema struct {
		name    string
		columns []string // Required columns
	}

	requiredTables := []tableSchema{
		{
			name:    "blobs",
			columns: []string{"id", "data"},
		},
		{
			name:    "meta",
			columns: []string{"key", "value"},
		},
	}

	// Validate each required table exists and has expected columns
	for _, table := range requiredTables {
		// Check if table exists
		var tableExists int
		err := db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
			table.name,
		).Scan(&tableExists)
		if err != nil {
			return fmt.Errorf("failed to check for table %s: %w", table.name, err)
		}
		if tableExists == 0 {
			return fmt.Errorf("invalid Cursor database: missing required table '%s'", table.name)
		}

		// Validate columns exist in the table
		// Note: pragma_table_info doesn't support parameter binding, so we use fmt.Sprintf
		// This is safe because table.name comes from our hardcoded schema, not user input
		for _, column := range table.columns {
			var columnExists int
			query := fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name=?", table.name)
			err := db.QueryRow(query, column).Scan(&columnExists)
			if err != nil {
				return fmt.Errorf("failed to check column %s.%s: %w", table.name, column, err)
			}
			if columnExists == 0 {
				return fmt.Errorf("invalid Cursor database: table '%s' missing required column '%s'", table.name, column)
			}
		}
	}

	slog.Debug("Database schema validation passed")
	return nil
}

// getSessionTimestamp retrieves the session creation timestamp from the meta table
func getSessionTimestamp(db *sql.DB) (string, error) {
	var rawValue string
	err := db.QueryRow("SELECT value FROM meta WHERE key='0'").Scan(&rawValue)
	if err != nil {
		slog.Debug("Failed to get metadata from meta table", "error", err)
		return "", fmt.Errorf("failed to get metadata: %w", err)
	}

	// The value is hex-encoded
	jsonBytes, err := hex.DecodeString(rawValue)
	if err != nil {
		slog.Debug("Failed to decode hex metadata", "error", err)
		return "", fmt.Errorf("failed to decode hex: %w", err)
	}

	// Parse the JSON
	var metadata struct {
		CreatedAt int64 `json:"createdAt"`
	}
	if err := json.Unmarshal(jsonBytes, &metadata); err != nil {
		slog.Debug("Failed to parse metadata JSON", "error", err)
		return "", fmt.Errorf("failed to parse metadata: %w", err)
	}

	slog.Debug("Found createdAt in metadata", "createdAt", metadata.CreatedAt)

	if metadata.CreatedAt > 0 {
		// Convert milliseconds to time
		ts := time.Unix(metadata.CreatedAt/1000, (metadata.CreatedAt%1000)*1000000)
		slog.Debug("Converted timestamp", "timestamp", ts)
		// Return as ISO 8601 format
		return ts.Format(time.RFC3339), nil
	}

	// Return current time as fallback
	return time.Now().Format(time.RFC3339), nil
}

// ReadSessionData reads all blobs from a Cursor session's store.db file
// Returns the creation timestamp, slug, blob records in topologically sorted order, and orphaned blobs
func ReadSessionData(sessionPath string) (string, string, []BlobRecord, []BlobRecord, error) {
	dbPath := filepath.Join(sessionPath, "store.db")

	slog.Debug("Opening Cursor CLI SQLite database", "path", dbPath)

	// Open the database in read-only mode
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("Failed to close database", "error", err)
		}
	}()

	slog.Debug("Successfully opened database", "path", dbPath)

	// Enable WAL mode for non-blocking reads
	// This prevents readers from blocking the cursor-agent writer
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		// Log warning but continue - not fatal if WAL fails
		slog.Warn("Failed to enable WAL mode", "error", err)
	} else {
		slog.Debug("Enabled WAL mode for non-blocking reads")
	}

	// Validate that this is actually a Cursor database with the expected schema
	if err := validateCursorDatabase(db); err != nil {
		return "", "", nil, nil, err
	}

	// Get the session timestamp from metadata
	createdAt, err := getSessionTimestamp(db)
	if err != nil {
		// Log but don't fail - use current time as fallback
		slog.Debug("Failed to get session timestamp, using current time", "error", err)
		createdAt = time.Now().Format(time.RFC3339)
	}

	// Query all blobs
	rows, err := db.Query("SELECT rowid, id, data FROM blobs")
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("failed to query blobs: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("Failed to close rows", "error", err)
		}
	}()

	// Count total blobs for logging
	var totalBlobs, jsonBlobs, skippedBlobs int

	slog.Debug("Starting to process blobs from database")

	// Collect all blobs and build a map for sorting
	blobMap := make(map[string]*CursorBlob)
	var allRowIDs []int // Collect all rowids for debugging

	for rows.Next() {
		var rowid int
		var id string
		var data []byte

		totalBlobs++

		if err := rows.Scan(&rowid, &id, &data); err != nil {
			slog.Debug("Failed to scan row", "error", err)
			continue // Skip problematic rows
		}

		// Collect rowid for debugging
		allRowIDs = append(allRowIDs, rowid)

		// Log every single blob we process
		idDisplay := id
		if len(idDisplay) > 16 {
			idDisplay = idDisplay[:16] + "..."
		}
		slog.Debug("Processing blob",
			"rowid", rowid,
			"id", idDisplay,
			"data_len", len(data))

		// Parse references from the binary data
		references := parseReferences(data, id, rowid)

		// Try to extract JSON from the binary data
		jsonData := extractJSONFromBinary(data)
		if jsonData != nil {
			jsonBlobs++
			blobMap[id] = &CursorBlob{
				RowID:      rowid,
				ID:         id,
				Data:       json.RawMessage(jsonData),
				RawData:    data,
				References: references,
			}
		} else {
			// If no JSON found, skip for now
			// We may want to include these as reference-only blobs in the future
			skippedBlobs++
			// Uncomment this to see JSON detection in action when debugging the sqlite reader
			idDisplay := id
			if len(idDisplay) > 16 {
				idDisplay = idDisplay[:16] + "..."
			}
			slog.Debug("Skipping blob (no JSON content found)",
				"blob_id", idDisplay,
				"rowid", rowid,
				"has_references", len(references) > 0,
				"reference_count", len(references))
		}
	}

	if err := rows.Err(); err != nil {
		return "", "", nil, nil, fmt.Errorf("error iterating rows: %w", err)
	}

	// Sort rowids for easier debugging
	sort.Ints(allRowIDs)

	// Log all rowids we got from the database - check if 1555-1560 are present
	slog.Debug("All rowids from database query",
		"count", len(allRowIDs),
		"min", allRowIDs[0],
		"max", allRowIDs[len(allRowIDs)-1],
		"rowids", allRowIDs)

	slog.Debug("Blob processing summary",
		"totalBlobs", totalBlobs,
		"jsonBlobs", jsonBlobs,
		"skippedBlobs", skippedBlobs)

	// Perform topological sort on the blobs
	sortedBlobs, orphanedBlobs, err := topologicalSort(blobMap)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("failed to sort blobs: %w", err)
	}

	// Convert sorted blobs to BlobRecord array (without References and RawData)
	var blobRecords []BlobRecord
	for _, blob := range sortedBlobs {
		blobRecords = append(blobRecords, BlobRecord{
			RowID: blob.RowID,
			ID:    blob.ID,
			Data:  blob.Data,
		})
	}

	// Convert orphaned blobs to BlobRecord array
	var orphanRecords []BlobRecord
	var orphanRowIDs []int
	for _, blob := range orphanedBlobs {
		orphanRecords = append(orphanRecords, BlobRecord{
			RowID: blob.RowID,
			ID:    blob.ID,
			Data:  blob.Data,
		})
		orphanRowIDs = append(orphanRowIDs, blob.RowID)
	}

	if len(orphanRowIDs) > 0 {
		sort.Ints(orphanRowIDs)
		slog.Debug("Orphaned blob rowids", "rowids", orphanRowIDs, "count", len(orphanRowIDs))
	}

	// Log final DAG rowids before markdown generation
	slog.Debug("Final DAG-sorted blob rowids before markdown generation:")
	var rowids []int
	for _, blob := range blobRecords {
		rowids = append(rowids, blob.RowID)
	}
	slog.Debug("DAG rowids", "rowids", rowids, "count", len(rowids))

	// Extract slug from the final blob records
	slug := extractSlugFromBlobs(blobRecords)

	return createdAt, slug, blobRecords, orphanRecords, nil
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
