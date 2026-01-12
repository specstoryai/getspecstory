package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	KB                    = 1024
	MB                    = 1024 * 1024
	maxReasonableLineSize = 250 * MB // 250MB sanity limit to prevent OOM from malformed or malicious files
)

// sessionIDRegex extracts sessionId from JSONL without full JSON parsing.
// Matches: "sessionId": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" (UUID format)
var sessionIDRegex = regexp.MustCompile(`"sessionId"\s*:\s*"([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})"`)

// JSONLRecord represents a single line from a JSONL file
type JSONLRecord struct {
	Data map[string]interface{}
	File string // Source file for this record
	Line int    // Line number in the source file
}

type Session struct {
	SessionUuid string
	Records     []JSONLRecord
}

// JSONLParser manages parsing and searching JSONL files
type JSONLParser struct {
	Sessions []Session
	Records  []JSONLRecord // All records for searching
}

// NewJSONLParser creates a new JSONL parser instance
func NewJSONLParser() *JSONLParser {
	return &JSONLParser{
		Sessions: make([]Session, 0),
		Records:  make([]JSONLRecord, 0),
	}
}

// formatDuration converts time.Duration to a human-readable string with words
func formatDuration(d time.Duration) string {
	if d < time.Microsecond {
		return fmt.Sprintf("%.0f nanoseconds", float64(d.Nanoseconds()))
	} else if d < time.Millisecond {
		return fmt.Sprintf("%.1f microseconds", float64(d.Nanoseconds())/1000)
	} else if d < time.Second {
		return fmt.Sprintf("%.1f milliseconds", float64(d.Nanoseconds())/1000000)
	} else if d < time.Minute {
		return fmt.Sprintf("%.1f seconds", d.Seconds())
	} else {
		return fmt.Sprintf("%.1f minutes", d.Minutes())
	}
}

// ParseProjectSessions implements the algorithm for parsing JSONL files:
// 1. Scan all JSONL files and process summaries and sidechains
// 2. Eliminate duplicates by uuid (keep earliest by timestamp)
// 3. Build parent/child DAGs from the JSON objects
// 4. Merge DAGs with the same session ID (handles resumed sessions)
// 5. Flatten each DAG into an array ordered by timestamp
// 6. For each array, the session is the sessionId of the head
func (p *JSONLParser) ParseProjectSessions(projectPath string, silent bool) error {
	return p.parseProjectSessions(projectPath, silent, "")
}

// ParseProjectSessionsForSession parses JSONL files that match a specific session ID.
// Files that don't contain the given sessionID are skipped for performance.
// If sessionID is empty, this behaves identically to ParseProjectSessions.
func (p *JSONLParser) ParseProjectSessionsForSession(projectPath string, silent bool, sessionID string) error {
	return p.parseProjectSessions(projectPath, silent, sessionID)
}

func (p *JSONLParser) parseProjectSessions(projectPath string, silent bool, sessionID string) error {
	// Track overall parsing time
	parseStartTime := time.Now()
	slog.Info("ParseProjectSessions: Starting JSONL parsing")

	// Step 1: Scan all JSONL files
	scanStartTime := time.Now()
	allRecords := []JSONLRecord{}
	err := filepath.Walk(projectPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if sessionID != "" {
			foundSessionID, matchErr := extractSessionIDFromFile(path)
			if matchErr != nil {
				slog.Warn("ParseProjectSessions: Failed to check session ID, parsing anyway",
					"file", path,
					"error", matchErr)
			} else if foundSessionID != sessionID {
				return nil
			}
		}
		records, err := p.parseSessionFile(path)
		if err != nil {
			return fmt.Errorf("failed to parse file %s: %w", path, err)
		}
		allRecords = append(allRecords, records...)
		// Print progress dot for each file when not in silent mode
		NoteProgress(silent)
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to scan project directory %s: %w", projectPath, err)
	}
	scanDuration := time.Since(scanStartTime)
	slog.Info("ParseProjectSessions: File scanning completed", "duration", formatDuration(scanDuration), "records", len(allRecords))

	if !silent {
		fmt.Print("\nProcessing JSONL files")
	}

	// Step 2: Eliminate duplicates by uuid (keep earliest by timestamp)
	dedupStartTime := time.Now()
	slog.Debug("ParseProjectSessions: Eliminating duplicates", "records", len(allRecords))
	uniqueRecords := p.eliminateDuplicates(allRecords)
	slog.Debug("ParseProjectSessions: After deduplication", "uniqueRecords", len(uniqueRecords))
	dedupDuration := time.Since(dedupStartTime)
	slog.Debug("ParseProjectSessions: Deduplication completed", "duration", formatDuration(dedupDuration))

	NoteProgress(silent)

	// Store all records for searching
	p.Records = uniqueRecords

	NoteProgress(silent)

	// Step 3: Build parent/child DAGs
	slog.Debug("ParseProjectSessions: Building DAGs")
	dagStartTime := time.Now()
	dags := p.buildDAGs(uniqueRecords)
	dagDuration := time.Since(dagStartTime)
	slog.Debug("ParseProjectSessions: DAG building completed", "duration", formatDuration(dagDuration), "dagCount", len(dags))

	// Step 4: Merge DAGs with the same session ID (handles resumed sessions)
	mergeStartTime := time.Now()
	mergedDags := p.mergeDagsWithSameSessionId(dags)
	mergeDuration := time.Since(mergeStartTime)
	slog.Debug("ParseProjectSessions: DAG merging completed", "duration", formatDuration(mergeDuration),
		"beforeCount", len(dags), "afterCount", len(mergedDags))

	// Step 5: Flatten each DAG into an array ordered by timestamp
	flattenStartTime := time.Now()
	sessions := []Session{}
	for _, dag := range mergedDags {
		NoteProgress(silent)
		flattenedDAG := p.flattenDAG(dag)
		if len(flattenedDAG) == 0 {
			continue
		}

		// Step 6: The session is the sessionId of the head
		sessionId := ""
		if sid, ok := flattenedDAG[0].Data["sessionId"].(string); ok {
			sessionId = sid
		} else {
			// If head doesn't have sessionId, find first one in the array
			for _, record := range flattenedDAG {
				if sid, ok := record.Data["sessionId"].(string); ok {
					sessionId = sid
					break
				}
			}
		}

		// Skip if no valid sessionId found
		if sessionId == "" {
			slog.Debug("Skipping DAG with no valid sessionId")
			continue
		}

		// Log session creation details
		if len(flattenedDAG) > 0 {
			rootTimestamp := ""
			if ts, ok := flattenedDAG[0].Data["timestamp"].(string); ok {
				rootTimestamp = ts
			}
			slog.Debug("Creating session",
				"sessionId", sessionId,
				"rootTimestamp", rootTimestamp,
				"recordCount", len(flattenedDAG))
		}

		sessions = append(sessions, Session{
			SessionUuid: sessionId,
			Records:     flattenedDAG,
		})
	}

	p.Sessions = sessions
	flattenDuration := time.Since(flattenStartTime)
	slog.Debug("ParseProjectSessions: Flattening completed", "duration", formatDuration(flattenDuration))

	// Log total parsing time
	totalDuration := time.Since(parseStartTime)
	slog.Info("ParseProjectSessions: Completed parsing", "sessions", len(p.Sessions), "duration", formatDuration(totalDuration))
	slog.Debug("ParseProjectSessions: Performance breakdown",
		"scan", formatDuration(scanDuration),
		"dedup", formatDuration(dedupDuration),
		"dag", formatDuration(dagDuration),
		"flatten", formatDuration(flattenDuration))
	return nil
}

func NoteProgress(silent bool) {
	if !silent {
		fmt.Print(".")
		_ = os.Stdout.Sync()
	}
}

// extractSessionIDFromFile reads a JSONL file and returns the first sessionId found.
// Uses regex for fast extraction without full JSON parsing.
// Returns empty string if no sessionId is found.
func extractSessionIDFromFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = file.Close() }()

	reader := bufio.NewReader(file)

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("error reading file: %w", err)
		}

		// Try to find sessionId via regex (faster than JSON parsing)
		if matches := sessionIDRegex.FindStringSubmatch(line); len(matches) > 1 {
			return matches[1], nil
		}

		if err == io.EOF {
			break
		}
	}

	return "", nil
}

// ParseSingleSession parses sessions and filters to a specific UUID
func (p *JSONLParser) ParseSingleSession(projectPath string, sessionUuid string) error {
	// Parse only files matching this session (silent=true since this is for single session)
	if err := p.ParseProjectSessionsForSession(projectPath, true, sessionUuid); err != nil {
		return err
	}

	// Filter to just the requested session
	filteredSessions := []Session{}
	for _, session := range p.Sessions {
		if session.SessionUuid == sessionUuid {
			filteredSessions = append(filteredSessions, session)
		}
	}

	if len(filteredSessions) == 0 {
		return fmt.Errorf("no session found for UUID %s", sessionUuid)
	}

	p.Sessions = filteredSessions
	return nil
}

// parseSessionFile parses a single JSONL file and handles summaries and sidechains
func (p *JSONLParser) parseSessionFile(filePath string) ([]JSONLRecord, error) {
	slog.Info("Parsing session file", "file", filePath)

	// Clean debug directory for jsonl-burst mode only
	// (specstory-burst cleaning happens later during markdown generation)
	if GetJsonlBurst() {
		uuid := ExtractUUIDFromFilename(filePath)
		if err := CleanDebugDirectory(uuid); err != nil {
			slog.Warn("Failed to clean debug directory", "error", err)
		}
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = file.Close() }() // Read-only file; close errors not actionable

	// Use bufio.Reader instead of Scanner to handle arbitrarily large lines
	// Scanner has a token size limit (even with custom buffer), but Reader does not
	reader := bufio.NewReader(file)

	lineNumber := 0
	records := []JSONLRecord{}
	var lastNonSummaryRecord *JSONLRecord
	// pendingSummary holds a summary string when it appears before any records in the file.
	// This happens when the summary is on line 1 - we can't attach it to a previous record
	// because none exists yet, so we hold it and attach it to the next record we process.
	var pendingSummary string

	for {
		// Read line using ReadString which has no size limit
		line, err := reader.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")

		// EOF is expected at end of file, other errors are genuine failures
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("error reading line %d: %w", lineNumber+1, err)
		}

		// Determine if we're at end of file and if we have content to process
		atEOF := err == io.EOF
		hasContent := len(line) > 0

		// Increment line number for every line read (including empty lines) to match text editor line numbers
		if hasContent || !atEOF {
			lineNumber++
		}

		// If no content, either skip empty line or exit at EOF
		if !hasContent {
			if atEOF {
				break // Reached end of file with no content
			}
			continue // Empty line in middle of file, skip it
		}

		// Sanity check to prevent OOM from pathological files
		if len(line) > maxReasonableLineSize {
			slog.Warn("line exceeds reasonable size limit",
				"lineNumber", lineNumber,
				"sizeMB", len(line)/MB,
				"limitMB", maxReasonableLineSize/MB,
				"file", filepath.Base(filePath))
			return nil, fmt.Errorf("line %d exceeds reasonable size limit (%d MB): refusing to process potentially malformed file",
				lineNumber, maxReasonableLineSize/MB)
		}

		// Log when processing unusually large lines (helps debug performance issues)
		if len(line) > 10*MB {
			slog.Debug("processing large JSONL line",
				"lineNumber", lineNumber,
				"sizeMB", len(line)/MB,
				"file", filepath.Base(filePath))
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(line), &data); err != nil {
			return nil, fmt.Errorf("failed to parse JSON on line %d: %w", lineNumber, err)
		}

		// Write debug JSON if jsonl-burst mode is enabled
		_ = WriteDebugJSON(filePath, lineNumber, data) // Debug feature; errors don't affect parsing

		// Check if this is a summary line
		if data["type"] == "summary" {
			if len(records) > 0 && lastNonSummaryRecord != nil {
				// Add summary to the last non-summary record
				lastNonSummaryRecord.Data["summary"] = data
			} else {
				// No previous record exists (summary is on line 1), so store it for the next record
				if summaryText, ok := data["summary"].(string); ok {
					pendingSummary = summaryText
				}
			}
			// After processing summary, check if we're done
			if atEOF {
				break
			}
			continue // Don't add summary as a separate record
		}

		// Handle sidechain parentUuid update
		if isSidechain, ok := data["isSidechain"].(bool); ok && isSidechain {
			if data["parentUuid"] == nil && lastNonSummaryRecord != nil {
				// Update parentUuid to the uuid of the prior record
				if uuid, ok := lastNonSummaryRecord.Data["uuid"].(string); ok {
					data["parentUuid"] = uuid
				}
			}
		}

		// If we have a pending summary from line 1, attach it to this first record
		if pendingSummary != "" {
			data["summary"] = map[string]interface{}{
				"summary": pendingSummary,
			}
			pendingSummary = "" // Clear it so we don't attach to multiple records
		}

		record := JSONLRecord{
			Data: data,
			File: filePath,
			Line: lineNumber,
		}

		records = append(records, record)

		// Track last non-summary record for sidechain processing
		if data["uuid"] != nil {
			lastNonSummaryRecord = &record
		}

		// After processing record, check if we're done
		if atEOF {
			break
		}
	}

	return records, nil
}

// eliminateDuplicates removes duplicate records by uuid, keeping the earliest by timestamp
func (p *JSONLParser) eliminateDuplicates(records []JSONLRecord) []JSONLRecord {
	// Map to track records by uuid
	recordMap := make(map[string]JSONLRecord)

	for _, record := range records {
		uuid, ok := record.Data["uuid"].(string)
		if !ok || uuid == "" {
			// Skip records without uuid
			continue
		}

		// If we haven't seen this uuid before, add it
		if existing, exists := recordMap[uuid]; !exists {
			recordMap[uuid] = record
		} else {
			// Compare timestamps and keep the earliest
			existingTS, _ := existing.Data["timestamp"].(string)
			currentTS, _ := record.Data["timestamp"].(string)
			if currentTS < existingTS {
				slog.Debug("eliminateDuplicates: Duplicate uuid - keeping newer timestamp",
					"uuid", uuid,
					"newTimestamp", currentTS,
					"oldTimestamp", existingTS)
				recordMap[uuid] = record
			} else if currentTS != existingTS {
				slog.Debug("eliminateDuplicates: Duplicate uuid - keeping older timestamp",
					"uuid", uuid,
					"oldTimestamp", existingTS,
					"newTimestamp", currentTS)
			}
		}
	}

	// Convert map back to slice
	uniqueRecords := make([]JSONLRecord, 0, len(recordMap))
	for _, record := range recordMap {
		uniqueRecords = append(uniqueRecords, record)
	}

	// Sort by timestamp for deterministic order
	sort.Slice(uniqueRecords, func(i, j int) bool {
		tsI, _ := uniqueRecords[i].Data["timestamp"].(string)
		tsJ, _ := uniqueRecords[j].Data["timestamp"].(string)
		return tsI < tsJ
	})

	return uniqueRecords
}

// mergeDagsWithSameSessionId merges DAGs that have the same session ID
// This handles cases where Claude Code resumes a session, creating multiple roots for the same session
func (p *JSONLParser) mergeDagsWithSameSessionId(dags [][]JSONLRecord) [][]JSONLRecord {
	// Map to group DAGs by session ID
	sessionDagMap := make(map[string][][]JSONLRecord)

	for _, dag := range dags {
		if len(dag) == 0 {
			continue
		}

		// Find the session ID for this DAG
		sessionId := ""
		for _, record := range dag {
			if sid, ok := record.Data["sessionId"].(string); ok && sid != "" {
				sessionId = sid
				break
			}
		}

		// If no session ID found, treat it as a unique DAG
		if sessionId == "" {
			sessionId = fmt.Sprintf("no-session-%p", &dag)
		}

		sessionDagMap[sessionId] = append(sessionDagMap[sessionId], dag)
	}

	// Merge DAGs with the same session ID
	mergedDags := [][]JSONLRecord{}
	for sessionId, dagsForSession := range sessionDagMap {
		if len(dagsForSession) == 1 {
			// No merging needed
			mergedDags = append(mergedDags, dagsForSession[0])
		} else {
			// Merge multiple DAGs for the same session
			slog.Debug("Merging multiple DAGs for session", "sessionId", sessionId, "dagCount", len(dagsForSession))
			mergedDag := []JSONLRecord{}
			for _, dag := range dagsForSession {
				mergedDag = append(mergedDag, dag...)
			}
			mergedDags = append(mergedDags, mergedDag)
		}
	}

	return mergedDags
}

// buildDAGs constructs parent/child DAGs from records
func (p *JSONLParser) buildDAGs(records []JSONLRecord) [][]JSONLRecord {
	// Create a map for quick lookup by uuid
	recordByUuid := make(map[string]JSONLRecord)
	for _, record := range records {
		if uuid, ok := record.Data["uuid"].(string); ok {
			recordByUuid[uuid] = record
		}
	}

	// Find all root nodes (parentUuid == null)
	roots := []JSONLRecord{}
	for _, record := range records {
		if record.Data["parentUuid"] == nil {
			roots = append(roots, record)
		}
	}

	// Build a DAG for each root
	dags := [][]JSONLRecord{}
	for _, root := range roots {
		dag := p.buildDAGFromRoot(root, recordByUuid)
		if len(dag) > 0 {
			dags = append(dags, dag)
		}
	}

	return dags
}

// buildDAGFromRoot builds a DAG starting from a root node
func (p *JSONLParser) buildDAGFromRoot(root JSONLRecord, recordByUuid map[string]JSONLRecord) []JSONLRecord {
	dag := []JSONLRecord{}
	visited := make(map[string]bool)

	var traverse func(node JSONLRecord)
	traverse = func(node JSONLRecord) {
		uuid, ok := node.Data["uuid"].(string)
		if !ok || visited[uuid] {
			return
		}

		visited[uuid] = true
		dag = append(dag, node)

		// Find all children and collect them first
		children := []JSONLRecord{}
		for _, record := range recordByUuid {
			parentUuid, ok := record.Data["parentUuid"].(string)
			if ok && parentUuid == uuid {
				// Check if this child exists in our map (to handle orphans)
				if _, exists := recordByUuid[record.Data["uuid"].(string)]; exists {
					children = append(children, record)
				}
			}
		}

		// Sort children by timestamp for deterministic traversal order
		sort.Slice(children, func(i, j int) bool {
			tsI, _ := children[i].Data["timestamp"].(string)
			tsJ, _ := children[j].Data["timestamp"].(string)
			return tsI < tsJ
		})

		// Traverse children in sorted order
		for _, child := range children {
			traverse(child)
		}
	}

	traverse(root)
	return dag
}

// flattenDAG flattens a DAG into an array ordered by timestamp
func (p *JSONLParser) flattenDAG(dag []JSONLRecord) []JSONLRecord {
	// Sort by timestamp, with parent-child relationships as tiebreaker
	sort.Slice(dag, func(i, j int) bool {
		tsI, _ := dag[i].Data["timestamp"].(string)
		tsJ, _ := dag[j].Data["timestamp"].(string)
		if tsI != tsJ {
			return tsI < tsJ
		}
		// If timestamps are equal, check parent-child relationship
		uuidI, _ := dag[i].Data["uuid"].(string)
		uuidJ, _ := dag[j].Data["uuid"].(string)
		parentI, _ := dag[i].Data["parentUuid"].(string)
		parentJ, _ := dag[j].Data["parentUuid"].(string)

		// If j is the parent of i, j should come first
		if parentI == uuidJ {
			return false
		}
		// If i is the parent of j, i should come first
		if parentJ == uuidI {
			return true
		}
		// Otherwise, sort by UUID for deterministic ordering
		return uuidI < uuidJ
	})

	return dag
}

// FindSession returns a pointer to the session with the given UUID
func (p *JSONLParser) FindSession(sessionUuid string) *Session {
	for i := range p.Sessions {
		if p.Sessions[i].SessionUuid == sessionUuid {
			return &p.Sessions[i]
		}
	}
	return nil
}

// FindRecord returns a pointer to the first record that matches the given filter function
func (p *JSONLParser) FindRecord(filter func(JSONLRecord) bool) *JSONLRecord {
	for i := range p.Records {
		if filter(p.Records[i]) {
			return &p.Records[i]
		}
	}
	return nil
}

// FilterRecords returns records that match the given filter function
func (p *JSONLParser) FilterRecords(filter func(JSONLRecord) bool) []JSONLRecord {
	var filtered []JSONLRecord
	for _, record := range p.Records {
		if filter(record) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

// Count returns the total number of parsed records
func (p *JSONLParser) Count() int {
	return len(p.Records)
}
