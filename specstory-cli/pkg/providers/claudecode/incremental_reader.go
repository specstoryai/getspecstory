package claudecode

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

var errIncrementalFullRebuild = errors.New("incremental append requires full rebuild")

// jsonlFileParseState is the resume position for one append-only transcript.
// info supplies portable file identity through os.SameFile on macOS, Linux, and Windows.
type jsonlFileParseState struct {
	offset   int64
	line     int
	lastUUID string
	info     os.FileInfo
	complete bool
	stable   bool
}

type incrementalReadResult struct {
	session      Session
	agentSession *spi.AgentChatSession
	shellGate    *shellGateState
	changed      bool
	incremental  bool
	appendOnly   bool
}

type incrementalSessionReader struct {
	sessions map[string]*incrementalSessionState
}

type toolOwner struct {
	exchangeIndex int
	messageIndex  int
}

type incrementalSessionState struct {
	session      Session
	agentSession *spi.AgentChatSession
	files        map[string]jsonlFileParseState
	recordIDs    map[string]struct{}
	toolOwners   map[string]toolOwner
	shellGate    *shellGateState
	rawData      strings.Builder
	rawRecords   []string
}

func newIncrementalSessionReader() *incrementalSessionReader {
	return &incrementalSessionReader{sessions: make(map[string]*incrementalSessionState)}
}

func incrementalSessionKey(projectDir, sessionID string) string {
	return filepath.Clean(projectDir) + "\x00" + sessionID
}

// read returns the cached session after a safe append, or performs the same
// full parse and conversion used before incremental reading existed.
func (r *incrementalSessionReader) read(projectDir, changedFile, sessionID string, debugRaw bool) (incrementalReadResult, error) {
	key := incrementalSessionKey(projectDir, sessionID)
	if !debugRaw && !GetJsonlBurst() {
		if state, ok := r.sessions[key]; ok {
			changed, appendOnly, err := state.applyAppend(changedFile)
			if err == nil {
				return incrementalReadResult{
					session:      state.session,
					agentSession: state.agentSession,
					shellGate:    state.shellGate,
					changed:      changed,
					incremental:  true,
					appendOnly:   appendOnly,
				}, nil
			}
			slog.Debug("Incremental transcript append rejected; rebuilding session",
				"sessionId", sessionID,
				"file", changedFile,
				"reason", err)
			delete(r.sessions, key)
		}
	}

	parser := NewJSONLParser()
	if err := parser.ParseProjectSessionsForSession(projectDir, true, sessionID); err != nil {
		return incrementalReadResult{}, err
	}
	parsedSession := parser.FindSession(sessionID)
	if parsedSession == nil {
		delete(r.sessions, key)
		return incrementalReadResult{}, nil
	}

	result := incrementalReadResult{
		session:   *parsedSession,
		shellGate: newShellGateState(parsedSession.Records),
		changed:   true,
	}
	result.agentSession = convertToAgentChatSession(*parsedSession, "", debugRaw)
	if result.agentSession == nil {
		delete(r.sessions, key)
		return result, nil
	}
	if !debugRaw && !GetJsonlBurst() {
		if state := newIncrementalSessionState(*parsedSession, result.agentSession, parser.fileStates); state != nil {
			state.shellGate = result.shellGate
			r.sessions[key] = state
		}
	}
	return result, nil
}

// openShellToolUses reads the state advanced by applyAppend. The fallback is
// for uncached results and keeps the full rebuild as the reference behavior.
func (r incrementalReadResult) openShellToolUses() []string {
	if r.shellGate != nil {
		return r.shellGate.ids()
	}
	return openShellToolUses(r.session.Records)
}

// newIncrementalSessionState caches only stable files ending at a newline. A
// partial final record remains uncached so the next scan still uses the full
// reference parser and cannot skip the record's remaining bytes.
func newIncrementalSessionState(session Session, agentSession *spi.AgentChatSession, parsedFiles map[string]jsonlFileParseState) *incrementalSessionState {
	files := make(map[string]jsonlFileParseState, len(parsedFiles))
	for path, state := range parsedFiles {
		if !state.complete || !state.stable || state.info == nil {
			return nil
		}
		files[path] = state
	}
	rawRecords := strings.Split(strings.TrimSuffix(agentSession.RawData, "\n"), "\n")
	if len(rawRecords) != len(session.Records) {
		return nil
	}
	state := &incrementalSessionState{
		session:      session,
		agentSession: agentSession,
		files:        files,
		recordIDs:    make(map[string]struct{}, len(session.Records)),
		toolOwners:   make(map[string]toolOwner),
		rawRecords:   rawRecords,
	}
	for _, record := range session.Records {
		if uuid, ok := record.Data["uuid"].(string); ok && uuid != "" {
			state.recordIDs[uuid] = struct{}{}
		}
	}
	for exchangeIndex := range agentSession.SessionData.Exchanges {
		exchange := &agentSession.SessionData.Exchanges[exchangeIndex]
		for messageIndex := range exchange.Messages {
			tool := exchange.Messages[messageIndex].Tool
			if tool != nil && tool.UseID != "" {
				state.toolOwners[tool.UseID] = toolOwner{exchangeIndex: exchangeIndex, messageIndex: messageIndex}
			}
		}
	}
	state.rawData.WriteString(agentSession.RawData)
	return state
}

// applyAppend validates the whole delta before mutating cached state. If any
// record needs a rebuild, earlier records from the same delta cannot leak into
// the cached session or into a cloud snapshot already queued by the callback.
func (s *incrementalSessionState) applyAppend(changedFile string) (bool, bool, error) {
	if len(s.session.Records) == 0 {
		return false, false, errIncrementalFullRebuild
	}
	lastRecord := s.session.Records[len(s.session.Records)-1]
	if filepath.Clean(changedFile) != filepath.Clean(lastRecord.File) {
		return false, false, fmt.Errorf("%w: append crosses resumed file ordering", errIncrementalFullRebuild)
	}
	fileState, ok := s.files[changedFile]
	if !ok {
		return false, false, fmt.Errorf("%w: no stored offset for changed file", errIncrementalFullRebuild)
	}
	records, nextFileState, err := readAppendedJSONL(changedFile, fileState)
	if err != nil {
		return false, false, err
	}
	if len(records) == 0 {
		s.files[changedFile] = nextFileState
		return false, true, nil
	}

	newIDs := make(map[string]struct{}, len(records))
	validRecords := records[:0]
	appendOnly := true
	for _, record := range records {
		uuid, ok := record.Data["uuid"].(string)
		if !ok || uuid == "" {
			// The full parser ignores UUID-less metadata, so skip it here too while
			// retaining nextFileState to avoid reading the same records again.
			continue
		}
		if _, exists := s.recordIDs[uuid]; exists {
			return false, false, fmt.Errorf("%w: duplicate uuid %s", errIncrementalFullRebuild, uuid)
		}
		if _, exists := newIDs[uuid]; exists {
			return false, false, fmt.Errorf("%w: duplicate uuid %s in delta", errIncrementalFullRebuild, uuid)
		}
		if parentUUID, ok := record.Data["parentUuid"].(string); ok {
			if parentUUID == "" {
				return false, false, fmt.Errorf("%w: appended record has an empty parent", errIncrementalFullRebuild)
			}
			_, knownParent := s.recordIDs[parentUUID]
			if !knownParent {
				_, knownParent = newIDs[parentUUID]
			}
			if !knownParent {
				return false, false, fmt.Errorf("%w: appended record has unknown parent", errIncrementalFullRebuild)
			}
		} else if record.Data["parentUuid"] != nil {
			return false, false, fmt.Errorf("%w: appended record has an invalid parent", errIncrementalFullRebuild)
		}
		if !recordLess(lastRecord, record) {
			appendOnly = false
		}
		newIDs[uuid] = struct{}{}
		lastRecord = record
		validRecords = append(validRecords, record)
	}
	records = validRecords
	if len(records) == 0 {
		s.files[changedFile] = nextFileState
		return false, true, nil
	}
	if !appendOnly {
		changed, err := s.applyOutOfOrderAppend(changedFile, records, nextFileState)
		return changed, false, err
	}

	validator := newNormalizedAppendValidator(s)
	for _, record := range records {
		if !validator.validate(record) {
			return false, false, fmt.Errorf("%w: appended record changes an older exchange", errIncrementalFullRebuild)
		}
	}

	var rawDelta strings.Builder
	for _, record := range records {
		s.shellGate.apply(record)
		s.applyNormalizedRecord(record)
		s.session.Records = append(s.session.Records, record)
		uuid, _ := record.Data["uuid"].(string)
		s.recordIDs[uuid] = struct{}{}
		encoded, _ := json.Marshal(record.Data)
		rawDelta.Write(encoded)
		rawDelta.WriteByte('\n')
		s.rawRecords = append(s.rawRecords, string(encoded))
	}
	s.files[changedFile] = nextFileState
	s.rawData.WriteString(rawDelta.String())
	s.agentSession.RawData = s.rawData.String()
	return true, true, nil
}

// applyOutOfOrderAppend merges a small out-of-order delta into the sorted cache.
// Only exchanges from the insertion point onward need normalization; the raw
// JSONL prefix is copied as bytes rather than parsed and marshaled again.
func (s *incrementalSessionState) applyOutOfOrderAppend(changedFile string, records []JSONLRecord, nextFileState jsonlFileParseState) (bool, error) {
	if len(s.session.Records) != len(s.rawRecords) {
		return false, fmt.Errorf("%w: cached raw records are misaligned", errIncrementalFullRebuild)
	}
	sort.Slice(records, func(i, j int) bool { return recordLess(records[i], records[j]) })
	oldRecords := s.session.Records
	mergedRecords := make([]JSONLRecord, 0, len(oldRecords)+len(records))
	mergedRawRecords := make([]string, 0, len(s.rawRecords)+len(records))
	firstNewPosition := -1
	oldIndex := 0
	for _, record := range records {
		position := oldIndex + sort.Search(len(oldRecords)-oldIndex, func(i int) bool {
			return !recordLess(oldRecords[oldIndex+i], record)
		})
		mergedRecords = append(mergedRecords, oldRecords[oldIndex:position]...)
		mergedRawRecords = append(mergedRawRecords, s.rawRecords[oldIndex:position]...)
		if firstNewPosition < 0 {
			firstNewPosition = len(mergedRecords)
		}
		mergedRecords = append(mergedRecords, record)
		encoded, _ := json.Marshal(record.Data)
		mergedRawRecords = append(mergedRawRecords, string(encoded))
		oldIndex = position
	}
	mergedRecords = append(mergedRecords, oldRecords[oldIndex:]...)
	mergedRawRecords = append(mergedRawRecords, s.rawRecords[oldIndex:]...)

	// Include the exchange preceding the insertion because an inserted prompt can
	// split it, and an inserted message can change its message order.
	tailStart := 0
	for i := firstNewPosition - 1; i >= 0; i-- {
		if isIncrementalExchangeStart(mergedRecords[i]) {
			tailStart = i
			break
		}
	}
	affectedIndex := 0
	if firstNewPosition > 0 {
		boundary := mergedRecords[tailStart]
		oldStart := sort.Search(len(oldRecords), func(i int) bool {
			return !recordLess(oldRecords[i], boundary)
		})
		if oldStart == len(oldRecords) || oldRecords[oldStart].Data["uuid"] != boundary.Data["uuid"] {
			return false, fmt.Errorf("%w: cannot locate cached exchange boundary", errIncrementalFullRebuild)
		}
		exchangeStarts := 0
		for _, record := range oldRecords[oldStart:] {
			if isIncrementalExchangeStart(record) {
				exchangeStarts++
			}
		}
		affectedIndex = len(s.agentSession.SessionData.Exchanges) - exchangeStarts
		if exchangeStarts == 0 {
			affectedIndex = len(s.agentSession.SessionData.Exchanges) - 1
		}
		if affectedIndex < 0 {
			affectedIndex = 0
		}
		if affectedIndex > len(s.agentSession.SessionData.Exchanges) {
			return false, fmt.Errorf("%w: invalid cached exchange boundary", errIncrementalFullRebuild)
		}
	}

	workspaceRoot := s.agentSession.SessionData.WorkspaceRoot
	conversionRecords := mergedRecords[tailStart:]
	var filteredSession Session
	if tailStart == 0 {
		filteredSession = Session{SessionUuid: s.session.SessionUuid, Records: filterWarmupMessages(mergedRecords)}
		if len(filteredSession.Records) == 0 {
			return false, fmt.Errorf("%w: session contains only warmup records", errIncrementalFullRebuild)
		}
		if _, ok := filteredSession.Records[0].Data["timestamp"].(string); !ok {
			return false, fmt.Errorf("%w: root record has no timestamp", errIncrementalFullRebuild)
		}
		conversionRecords = filteredSession.Records
		workspaceRoot = extractWorkspaceRootFromRecords(filteredSession.Records)
	}
	tailExchanges, err := buildExchangesFromRecords(conversionRecords, workspaceRoot)
	if err != nil {
		return false, fmt.Errorf("%w: %v", errIncrementalFullRebuild, err)
	}
	for i := range tailExchanges {
		tailExchanges[i].ExchangeID = fmt.Sprintf("%s:%d", s.session.SessionUuid, affectedIndex+i)
		for j := range tailExchanges[i].Messages {
			tool := tailExchanges[i].Messages[j].Tool
			if tool != nil {
				formattedMarkdown := formatToolAsMarkdown(tool, workspaceRoot)
				tool.FormattedMarkdown = &formattedMarkdown
			}
		}
	}
	mergedExchanges := make([]Exchange, 0, affectedIndex+len(tailExchanges))
	mergedExchanges = append(mergedExchanges, s.agentSession.SessionData.Exchanges[:affectedIndex]...)
	mergedExchanges = append(mergedExchanges, tailExchanges...)
	newShellGate := newShellGateState(mergedRecords[tailStart:])

	for _, exchange := range s.agentSession.SessionData.Exchanges[affectedIndex:] {
		for _, message := range exchange.Messages {
			if message.Tool != nil && message.Tool.UseID != "" {
				delete(s.toolOwners, message.Tool.UseID)
			}
		}
	}
	for exchangeIndex := affectedIndex; exchangeIndex < len(mergedExchanges); exchangeIndex++ {
		for messageIndex, message := range mergedExchanges[exchangeIndex].Messages {
			if message.Tool != nil && message.Tool.UseID != "" {
				s.toolOwners[message.Tool.UseID] = toolOwner{exchangeIndex: exchangeIndex, messageIndex: messageIndex}
			}
		}
	}
	for _, record := range records {
		uuid, _ := record.Data["uuid"].(string)
		s.recordIDs[uuid] = struct{}{}
	}

	s.rawData.Reset()
	s.rawData.Grow(len(s.agentSession.RawData))
	for _, rawRecord := range mergedRawRecords {
		s.rawData.WriteString(rawRecord)
		s.rawData.WriteByte('\n')
	}
	s.session.Records = mergedRecords
	s.rawRecords = mergedRawRecords
	s.agentSession.RawData = s.rawData.String()
	s.agentSession.SessionData.Exchanges = mergedExchanges
	s.shellGate = newShellGate
	s.files[changedFile] = nextFileState
	if tailStart == 0 {
		s.agentSession.CreatedAt, _ = filteredSession.Records[0].Data["timestamp"].(string)
		s.agentSession.SessionData.CreatedAt = extractCreatedAtFromRecords(filteredSession.Records)
		s.agentSession.SessionData.WorkspaceRoot = workspaceRoot
		s.agentSession.Slug = FileSlugFromRootRecord(filteredSession)
		s.agentSession.SessionData.Slug = s.agentSession.Slug
	}
	return true, nil
}

func isIncrementalExchangeStart(record JSONLRecord) bool {
	recordType, _ := record.Data["type"].(string)
	if recordType != "user" {
		return false
	}
	isSidechain, _ := record.Data["isSidechain"].(bool)
	message, _ := record.Data["message"].(map[string]interface{})
	content, _ := message["content"].([]interface{})
	return !isSidechain && !isToolResultContent(content)
}

// normalizedAppendValidator simulates exchange and message indexes across the
// delta so tool results can be checked without mutating the cached session.
type normalizedAppendValidator struct {
	state         *incrementalSessionState
	exchangeIndex int
	messageCounts map[int]int
	newTools      map[string]toolOwner
}

func newNormalizedAppendValidator(state *incrementalSessionState) *normalizedAppendValidator {
	validator := &normalizedAppendValidator{
		state:         state,
		exchangeIndex: len(state.agentSession.SessionData.Exchanges) - 1,
		messageCounts: make(map[int]int),
		newTools:      make(map[string]toolOwner),
	}
	if validator.exchangeIndex >= 0 {
		validator.messageCounts[validator.exchangeIndex] = len(state.agentSession.SessionData.Exchanges[validator.exchangeIndex].Messages)
	}
	return validator
}

func (v *normalizedAppendValidator) validate(record JSONLRecord) bool {
	recordType, _ := record.Data["type"].(string)
	isSidechain, _ := record.Data["isSidechain"].(bool)
	switch recordType {
	case "user":
		message, _ := record.Data["message"].(map[string]interface{})
		content, _ := message["content"].([]interface{})
		if isToolResultContent(content) {
			toolUseID, _, ok := parseToolResult(content)
			if !ok || v.exchangeIndex < 0 {
				return false
			}
			owner, ok := v.newTools[toolUseID]
			if !ok {
				owner, ok = v.state.toolOwners[toolUseID]
			}
			return ok && owner.exchangeIndex == v.exchangeIndex
		}
		if isSidechain {
			if v.exchangeIndex < 0 {
				return false
			}
			v.messageCounts[v.exchangeIndex]++
			return true
		}
		v.exchangeIndex++
		v.messageCounts[v.exchangeIndex] = 1
		return true
	case "assistant":
		if v.exchangeIndex < 0 {
			v.exchangeIndex = 0
		}
		message := buildAgentMessage(record, v.state.agentSession.SessionData.WorkspaceRoot, isSidechain)
		messageIndex := v.messageCounts[v.exchangeIndex]
		v.messageCounts[v.exchangeIndex] = messageIndex + 1
		if message.Tool != nil && message.Tool.UseID != "" {
			v.newTools[message.Tool.UseID] = toolOwner{exchangeIndex: v.exchangeIndex, messageIndex: messageIndex}
		}
	}
	return true
}

// applyNormalizedRecord performs the update already validated above. New
// records touch only their own messages or the current exchange's tool result.
func (s *incrementalSessionState) applyNormalizedRecord(record JSONLRecord) {
	sessionData := s.agentSession.SessionData
	recordType, _ := record.Data["type"].(string)
	timestamp, _ := record.Data["timestamp"].(string)
	isSidechain, _ := record.Data["isSidechain"].(bool)

	switch recordType {
	case "user":
		message, _ := record.Data["message"].(map[string]interface{})
		content, _ := message["content"].([]interface{})
		if isToolResultContent(content) {
			toolUseID, output, _ := parseToolResult(content)
			owner := s.toolOwners[toolUseID]
			exchange := &sessionData.Exchanges[owner.exchangeIndex]
			toolMessage := &exchange.Messages[owner.messageIndex]
			toolMessage.Tool.Output = output
			formattedMarkdown := formatToolAsMarkdown(toolMessage.Tool, sessionData.WorkspaceRoot)
			toolMessage.Tool.FormattedMarkdown = &formattedMarkdown
			toolMessage.Timestamp = timestamp
			exchange.EndTime = timestamp
			return
		}
		if isSidechain {
			exchange := &sessionData.Exchanges[len(sessionData.Exchanges)-1]
			exchange.Messages = append(exchange.Messages, buildUserMessage(record, true))
			exchange.EndTime = timestamp
			return
		}
		sessionData.Exchanges = append(sessionData.Exchanges, Exchange{
			ExchangeID: fmt.Sprintf("%s:%d", sessionData.SessionID, len(sessionData.Exchanges)),
			StartTime:  timestamp,
			Messages:   []Message{buildUserMessage(record, false)},
		})
	case "assistant":
		if len(sessionData.Exchanges) == 0 {
			sessionData.Exchanges = append(sessionData.Exchanges, Exchange{
				ExchangeID: fmt.Sprintf("%s:0", sessionData.SessionID),
				StartTime:  timestamp,
			})
		}
		exchangeIndex := len(sessionData.Exchanges) - 1
		exchange := &sessionData.Exchanges[exchangeIndex]
		message := buildAgentMessage(record, sessionData.WorkspaceRoot, isSidechain)
		if message.Tool != nil {
			formattedMarkdown := formatToolAsMarkdown(message.Tool, sessionData.WorkspaceRoot)
			message.Tool.FormattedMarkdown = &formattedMarkdown
		}
		messageIndex := len(exchange.Messages)
		exchange.Messages = append(exchange.Messages, message)
		exchange.EndTime = timestamp
		if message.Tool != nil && message.Tool.UseID != "" {
			s.toolOwners[message.Tool.UseID] = toolOwner{exchangeIndex: exchangeIndex, messageIndex: messageIndex}
		}
	}
}

func isToolResultContent(content []interface{}) bool {
	if len(content) == 0 {
		return false
	}
	first, ok := content[0].(map[string]interface{})
	if !ok {
		return false
	}
	contentType, _ := first["type"].(string)
	return contentType == "tool_result"
}

// readAppendedJSONL reads only complete lines after the stored offset. A
// trailing partial line remains unconsumed until its newline arrives.
func readAppendedJSONL(filePath string, state jsonlFileParseState) ([]JSONLRecord, jsonlFileParseState, error) {
	next := state
	records := []JSONLRecord{}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, next, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, next, err
	}
	if state.info == nil || !os.SameFile(state.info, info) {
		return nil, next, fmt.Errorf("%w: file was replaced", errIncrementalFullRebuild)
	}
	if info.Size() < state.info.Size() {
		return nil, next, fmt.Errorf("%w: file shrank", errIncrementalFullRebuild)
	}
	if info.Size() == state.info.Size() && !info.ModTime().Equal(state.info.ModTime()) {
		return nil, next, fmt.Errorf("%w: file was rewritten without an append", errIncrementalFullRebuild)
	}
	next.info = info
	if _, err := file.Seek(state.offset, io.SeekStart); err != nil {
		return nil, next, err
	}

	// Never chase a writer indefinitely: bytes appended after this stat belong to
	// the next dirty scan, which also keeps shutdown responsive.
	reader := bufio.NewReader(io.LimitReader(file, info.Size()-state.offset))
	for {
		lineStart := next.offset
		var rawLine []byte
		oversized := false
		lineBytes := 0
		terminated := false
		var readErr error
		for {
			fragment, err := reader.ReadSlice('\n')
			lineBytes += len(fragment)
			terminated = len(fragment) > 0 && fragment[len(fragment)-1] == '\n'
			if !oversized {
				if len(rawLine)+len(fragment) > spi.MaxRecordLineSize {
					oversized = true
					rawLine = nil
				} else {
					rawLine = append(rawLine, fragment...)
				}
			}
			if err != bufio.ErrBufferFull {
				readErr = err
				break
			}
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, next, readErr
		}
		if lineBytes == 0 || (errors.Is(readErr, io.EOF) && !terminated) {
			return records, next, nil
		}

		next.offset = lineStart + int64(lineBytes)
		next.line++
		if oversized {
			slog.Warn("Skipping oversized appended JSONL line", "file", filePath, "line", next.line, "limit", spi.MaxRecordLineSize)
			continue
		}
		line := strings.TrimSuffix(string(rawLine), "\n")
		if line == "" {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(line), &data); err != nil {
			slog.Warn("Skipping corrupted appended JSONL line", "file", filePath, "line", next.line, "error", err)
			continue
		}
		if data["type"] == "summary" {
			return nil, next, fmt.Errorf("%w: summary updates an earlier record", errIncrementalFullRebuild)
		}
		if isSidechain, _ := data["isSidechain"].(bool); isSidechain && data["parentUuid"] == nil && next.lastUUID != "" {
			data["parentUuid"] = next.lastUUID
		}
		record := JSONLRecord{Data: data, File: filePath, Line: next.line}
		records = append(records, record)
		if uuid, ok := data["uuid"].(string); ok {
			next.lastUUID = uuid
		}
	}
}
