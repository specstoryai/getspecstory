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
			changed, err := state.applyAppend(changedFile)
			if err == nil {
				return incrementalReadResult{
					session:      state.session,
					agentSession: state.agentSession,
					shellGate:    state.shellGate,
					changed:      changed,
					incremental:  true,
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
	state := &incrementalSessionState{
		session:      session,
		agentSession: agentSession,
		files:        files,
		recordIDs:    make(map[string]struct{}, len(session.Records)),
		toolOwners:   make(map[string]toolOwner),
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
func (s *incrementalSessionState) applyAppend(changedFile string) (bool, error) {
	if len(s.session.Records) == 0 {
		return false, errIncrementalFullRebuild
	}
	lastRecord := s.session.Records[len(s.session.Records)-1]
	if filepath.Clean(changedFile) != filepath.Clean(lastRecord.File) {
		return false, fmt.Errorf("%w: append crosses resumed file ordering", errIncrementalFullRebuild)
	}
	fileState, ok := s.files[changedFile]
	if !ok {
		return false, fmt.Errorf("%w: no stored offset for changed file", errIncrementalFullRebuild)
	}
	records, nextFileState, err := readAppendedJSONL(changedFile, fileState)
	if err != nil {
		return false, err
	}
	if len(records) == 0 {
		s.files[changedFile] = nextFileState
		return false, nil
	}

	newIDs := make(map[string]struct{}, len(records))
	validator := newNormalizedAppendValidator(s)
	for _, record := range records {
		uuid, ok := record.Data["uuid"].(string)
		if !ok || uuid == "" {
			return false, fmt.Errorf("%w: appended record has no uuid", errIncrementalFullRebuild)
		}
		if _, exists := s.recordIDs[uuid]; exists {
			return false, fmt.Errorf("%w: duplicate uuid %s", errIncrementalFullRebuild, uuid)
		}
		if _, exists := newIDs[uuid]; exists {
			return false, fmt.Errorf("%w: duplicate uuid %s in delta", errIncrementalFullRebuild, uuid)
		}
		parentUUID, ok := record.Data["parentUuid"].(string)
		if !ok || parentUUID == "" {
			return false, fmt.Errorf("%w: appended record starts or orphans a DAG", errIncrementalFullRebuild)
		}
		_, knownParent := s.recordIDs[parentUUID]
		if !knownParent {
			_, knownParent = newIDs[parentUUID]
		}
		if !knownParent {
			return false, fmt.Errorf("%w: appended record has unknown parent", errIncrementalFullRebuild)
		}
		if !recordLess(lastRecord, record) {
			return false, fmt.Errorf("%w: appended record orders before cached tail", errIncrementalFullRebuild)
		}
		if !validator.validate(record) {
			return false, fmt.Errorf("%w: appended record changes an older exchange", errIncrementalFullRebuild)
		}
		newIDs[uuid] = struct{}{}
		lastRecord = record
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
	}
	s.files[changedFile] = nextFileState
	s.rawData.WriteString(rawDelta.String())
	s.agentSession.RawData = s.rawData.String()
	return true, nil
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
