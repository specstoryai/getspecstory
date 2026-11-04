package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

const incrementalTestSessionID = "22222222-2222-2222-2222-222222222222"

func incrementalTestRecord(recordType, uuid string, second int, parent interface{}, content interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":        recordType,
		"uuid":        uuid,
		"sessionId":   incrementalTestSessionID,
		"timestamp":   time.Date(2026, 10, 1, 0, 0, second, 0, time.UTC).Format(time.RFC3339),
		"parentUuid":  parent,
		"isSidechain": false,
		"cwd":         "/workspace",
		"message": map[string]interface{}{
			"role":    recordType,
			"content": content,
		},
	}
}

func incrementalTestLine(t *testing.T, record map[string]interface{}) string {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal test record: %v", err)
	}
	return string(data) + "\n"
}

func incrementalTestFile(t *testing.T, lines ...string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, incrementalTestSessionID+".jsonl")
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatalf("create transcript: %v", err)
	}
	for _, line := range lines {
		appendIncrementalTestData(t, path, line)
	}
	return dir, path
}

func appendIncrementalTestData(t *testing.T, path, data string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("open transcript for append: %v", err)
	}
	if _, err := file.WriteString(data); err != nil {
		_ = file.Close()
		t.Fatalf("append transcript: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close transcript: %v", err)
	}
}

func incrementalFileState(t *testing.T, path string, offset int64, line int, lastUUID string) jsonlFileParseState {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat transcript: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer func() { _ = file.Close() }()
	prefix, err := computePrefixCheckpoint(file, offset)
	if err != nil {
		t.Fatalf("checkpoint transcript: %v", err)
	}
	return jsonlFileParseState{offset: offset, line: line, lastUUID: lastUUID, info: info, complete: true, stable: true, prefix: prefix}
}

func TestReadAppendedJSONL_ReadsFromStoredOffset(t *testing.T) {
	root := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, "first"))
	assistant := incrementalTestLine(t, incrementalTestRecord("assistant", "reply", 1, "root", []interface{}{
		map[string]interface{}{"type": "text", "text": "answer"},
	}))
	_, path := incrementalTestFile(t, root)
	state := incrementalFileState(t, path, int64(len(root)), 1, "root")
	appendIncrementalTestData(t, path, assistant)

	records, next, err := readAppendedJSONL(path, state)
	if err != nil {
		t.Fatalf("read appended JSONL: %v", err)
	}
	if len(records) != 1 || records[0].Data["uuid"] != "reply" || records[0].Line != 2 {
		t.Errorf("records = %#v, want only reply on line 2", records)
	}
	if next.offset != int64(len(root)+len(assistant)) {
		t.Errorf("next offset = %d, want %d", next.offset, len(root)+len(assistant))
	}
}

func TestReadAppendedJSONL_HoldsPartialTrailingLine(t *testing.T) {
	root := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, "first"))
	assistant := incrementalTestLine(t, incrementalTestRecord("assistant", "reply", 1, "root", []interface{}{
		map[string]interface{}{"type": "text", "text": "answer"},
	}))
	_, path := incrementalTestFile(t, root)
	state := incrementalFileState(t, path, int64(len(root)), 1, "root")
	partialAt := len(assistant) / 2
	appendIncrementalTestData(t, path, assistant[:partialAt])

	records, next, err := readAppendedJSONL(path, state)
	if err != nil {
		t.Fatalf("read partial line: %v", err)
	}
	if len(records) != 0 || next.offset != state.offset || next.line != state.line {
		t.Errorf("partial line advanced state: records=%d next=%+v state=%+v", len(records), next, state)
	}

	appendIncrementalTestData(t, path, assistant[partialAt:])
	records, next, err = readAppendedJSONL(path, next)
	if err != nil {
		t.Fatalf("read completed line: %v", err)
	}
	if len(records) != 1 || records[0].Data["uuid"] != "reply" || records[0].Line != 2 {
		t.Errorf("completed records = %#v, want reply on line 2", records)
	}
	if next.offset != int64(len(root)+len(assistant)) {
		t.Errorf("next offset = %d, want %d", next.offset, len(root)+len(assistant))
	}
}

func TestReadAppendedJSONL_ShrinkWithinPartialLineRequiresRebuild(t *testing.T) {
	root := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, "first"))
	assistant := incrementalTestLine(t, incrementalTestRecord("assistant", "reply", 1, "root", []interface{}{
		map[string]interface{}{"type": "text", "text": "answer"},
	}))
	_, path := incrementalTestFile(t, root)
	state := incrementalFileState(t, path, int64(len(root)), 1, "root")
	appendIncrementalTestData(t, path, assistant[:len(assistant)*3/4])

	_, next, err := readAppendedJSONL(path, state)
	if err != nil {
		t.Fatalf("read partial line: %v", err)
	}
	if err := os.WriteFile(path, []byte(root+assistant[:len(assistant)/4]), 0600); err != nil {
		t.Fatalf("shrink partial line: %v", err)
	}
	if _, _, err := readAppendedJSONL(path, next); !errors.Is(err, errIncrementalFullRebuild) {
		t.Errorf("read shrunken partial line error = %v, want full rebuild", err)
	}
}

func TestIncrementalSessionReader_ShrunkOrReplacedFileUsesFullRebuild(t *testing.T) {
	rootRecord := incrementalTestRecord("user", "root", 0, nil, "first")
	root := incrementalTestLine(t, rootRecord)
	reply := incrementalTestLine(t, incrementalTestRecord("assistant", "reply", 1, "root", []interface{}{
		map[string]interface{}{"type": "text", "text": "answer"},
	}))
	newPrompt := incrementalTestLine(t, incrementalTestRecord("user", "new-prompt", 2, "reply", "next"))

	tests := []struct {
		name         string
		initial      string
		replace      string
		wantRecords  int
		replacedFile bool
	}{
		{name: "shrunk", initial: root + reply, replace: root, wantRecords: 1},
		{name: "replaced with a larger file", initial: root + reply, replace: root + reply + newPrompt, wantRecords: 3, replacedFile: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, path := incrementalTestFile(t, tt.initial)
			reader := newIncrementalSessionReader()
			if _, err := reader.read(dir, path, incrementalTestSessionID, false); err != nil {
				t.Fatalf("initial full read: %v", err)
			}
			if tt.replacedFile {
				replacement := filepath.Join(dir, "replacement.jsonl")
				if err := os.WriteFile(replacement, []byte(tt.replace), 0600); err != nil {
					t.Fatalf("write replacement: %v", err)
				}
				if err := os.Rename(replacement, path); err != nil {
					t.Fatalf("replace transcript: %v", err)
				}
			} else if err := os.WriteFile(path, []byte(tt.replace), 0600); err != nil {
				t.Fatalf("shrink transcript: %v", err)
			}

			result, err := reader.read(dir, path, incrementalTestSessionID, false)
			if err != nil {
				t.Fatalf("read after file change: %v", err)
			}
			if result.incremental {
				t.Error("file change was applied incrementally, want full rebuild")
			}
			if len(result.session.Records) != tt.wantRecords {
				t.Errorf("records = %d, want %d", len(result.session.Records), tt.wantRecords)
			}
		})
	}
}

func TestIncrementalSessionReader_SameSizeRewriteUsesFullRebuild(t *testing.T) {
	for _, content := range []string{"first", "other"} {
		t.Run(content, func(t *testing.T) {
			root := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, "first"))
			dir, path := incrementalTestFile(t, root)
			reader := newIncrementalSessionReader()
			if _, err := reader.read(dir, path, incrementalTestSessionID, false); err != nil {
				t.Fatalf("initial full read: %v", err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			replacement := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, content))
			if len(replacement) != len(root) {
				t.Fatal("test replacement must preserve file size")
			}
			if err := os.WriteFile(path, []byte(replacement), 0600); err != nil {
				t.Fatalf("rewrite transcript: %v", err)
			}
			modTime := before.ModTime().Add(2 * time.Second)
			if err := os.Chtimes(path, modTime, modTime); err != nil {
				t.Fatalf("set rewrite mtime: %v", err)
			}

			result, err := reader.read(dir, path, incrementalTestSessionID, false)
			if err != nil {
				t.Fatalf("read rewritten transcript: %v", err)
			}
			if result.incremental {
				t.Error("same-size rewrite was treated as an empty append")
			}
		})
	}
}

func TestIncrementalSessionReader_MatchesFullRebuild(t *testing.T) {
	root := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, "first"))
	toolCall := incrementalTestLine(t, incrementalTestRecord("assistant", "tool-call", 1, "root", []interface{}{
		map[string]interface{}{"type": "tool_use", "id": "tool-1", "name": "Read", "input": map[string]interface{}{"file_path": "a.go"}},
	}))
	toolResult := incrementalTestLine(t, incrementalTestRecord("user", "tool-result", 2, "tool-call", []interface{}{
		map[string]interface{}{"type": "tool_result", "tool_use_id": "tool-1", "content": "contents", "is_error": false},
	}))
	newPrompt := incrementalTestLine(t, incrementalTestRecord("user", "new-prompt", 3, "tool-result", "next"))
	finalReply := incrementalTestLine(t, incrementalTestRecord("assistant", "final-reply", 4, "new-prompt", []interface{}{
		map[string]interface{}{"type": "text", "text": "done"},
	}))

	dir, path := incrementalTestFile(t, root)
	reader := newIncrementalSessionReader()
	if _, err := reader.read(dir, path, incrementalTestSessionID, false); err != nil {
		t.Fatalf("initial full read: %v", err)
	}
	for i, appended := range []string{toolCall + toolResult, newPrompt + finalReply} {
		appendIncrementalTestData(t, path, appended)
		result, err := reader.read(dir, path, incrementalTestSessionID, false)
		if err != nil {
			t.Fatalf("incremental read %d: %v", i+1, err)
		}
		if !result.incremental || !result.changed {
			t.Errorf("read %d: incremental=%t changed=%t, want true/true", i+1, result.incremental, result.changed)
		}
	}

	incremental, err := reader.read(dir, path, incrementalTestSessionID, false)
	if err != nil {
		t.Fatalf("cached read: %v", err)
	}
	parser := NewJSONLParser()
	if err := parser.ParseProjectSessionsForSession(dir, true, incrementalTestSessionID); err != nil {
		t.Fatalf("full reference parse: %v", err)
	}
	fullSession := parser.FindSession(incrementalTestSessionID)
	if fullSession == nil {
		t.Fatal("full reference session not found")
	}
	fullAgent := convertToAgentChatSession(*fullSession, "", false)
	if !reflect.DeepEqual(incremental.session.Records, fullSession.Records) {
		t.Errorf("incremental records differ from full rebuild")
	}
	if !reflect.DeepEqual(incremental.agentSession.SessionData, fullAgent.SessionData) {
		incrementalJSON, _ := json.MarshalIndent(incremental.agentSession.SessionData, "", "  ")
		fullJSON, _ := json.MarshalIndent(fullAgent.SessionData, "", "  ")
		t.Errorf("incremental SessionData differs from full rebuild\nincremental: %s\nfull: %s", incrementalJSON, fullJSON)
	}
	if incremental.agentSession.RawData != fullAgent.RawData {
		t.Errorf("incremental RawData differs from full rebuild")
	}
	if incremental.agentSession.CreatedAt != fullAgent.CreatedAt || incremental.agentSession.Slug != fullAgent.Slug {
		t.Errorf("incremental metadata differs from full rebuild")
	}
}

func TestIncrementalSessionReader_UUIDLessRecordDoesNotForceFullRebuild(t *testing.T) {
	root := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, "first"))
	metadata := incrementalTestLine(t, map[string]interface{}{
		"type": "ai-title", "sessionId": incrementalTestSessionID, "title": "metadata only",
	})
	reply := incrementalTestLine(t, incrementalTestRecord("assistant", "reply", 1, "root", []interface{}{
		map[string]interface{}{"type": "text", "text": "answer"},
	}))
	dir, path := incrementalTestFile(t, root)
	reader := newIncrementalSessionReader()
	if _, err := reader.read(dir, path, incrementalTestSessionID, false); err != nil {
		t.Fatalf("initial full read: %v", err)
	}

	appendIncrementalTestData(t, path, metadata+reply)
	result, err := reader.read(dir, path, incrementalTestSessionID, false)
	if err != nil {
		t.Fatalf("read UUID-less metadata append: %v", err)
	}
	if !result.incremental || !result.changed {
		t.Errorf("incremental=%t changed=%t, want true/true", result.incremental, result.changed)
	}
	if len(result.session.Records) != 2 || result.session.Records[1].Data["uuid"] != "reply" {
		t.Errorf("records = %#v, want root and reply only", result.session.Records)
	}
}

func TestIncrementalSessionReader_UUIDLessOnlyAppendDoesNotSave(t *testing.T) {
	root := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, "first"))
	metadata := incrementalTestLine(t, map[string]interface{}{
		"type": "ai-title", "sessionId": incrementalTestSessionID, "title": "metadata only",
	})
	reply := incrementalTestLine(t, incrementalTestRecord("assistant", "reply", 1, "root", []interface{}{
		map[string]interface{}{"type": "text", "text": "answer"},
	}))
	dir, path := incrementalTestFile(t, root)
	reader := newIncrementalSessionReader()
	if _, err := reader.read(dir, path, incrementalTestSessionID, false); err != nil {
		t.Fatalf("initial full read: %v", err)
	}

	appendIncrementalTestData(t, path, metadata)
	result, err := reader.read(dir, path, incrementalTestSessionID, false)
	if err != nil {
		t.Fatalf("read metadata-only append: %v", err)
	}
	if !result.incremental || result.changed {
		t.Errorf("incremental=%t changed=%t, want true/false", result.incremental, result.changed)
	}

	appendIncrementalTestData(t, path, reply)
	result, err = reader.read(dir, path, incrementalTestSessionID, false)
	if err != nil {
		t.Fatalf("read after metadata-only append: %v", err)
	}
	if !result.incremental || !result.changed || len(result.session.Records) != 2 {
		t.Errorf("next append: incremental=%t changed=%t records=%d, want true/true/2", result.incremental, result.changed, len(result.session.Records))
	}
}

func TestIncrementalSessionReader_OutOfOrderAppendMatchesFullRebuild(t *testing.T) {
	root := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, "first"))
	reply := incrementalTestLine(t, incrementalTestRecord("assistant", "reply", 1, "root", []interface{}{
		map[string]interface{}{"type": "text", "text": "first answer"},
	}))
	laterPrompt := incrementalTestLine(t, incrementalTestRecord("user", "later-prompt", 3, "reply", "later"))
	laterReply := incrementalTestLine(t, incrementalTestRecord("assistant", "later-reply", 5, "later-prompt", []interface{}{
		map[string]interface{}{"type": "text", "text": "later answer"},
	}))
	system := incrementalTestLine(t, map[string]interface{}{
		"type": "system", "uuid": "system", "parentUuid": nil,
		"sessionId": incrementalTestSessionID, "timestamp": time.Date(2026, 10, 1, 0, 0, 2, 0, time.UTC).Format(time.RFC3339),
	})
	middle := incrementalTestLine(t, incrementalTestRecord("assistant", "middle", 2, "reply", []interface{}{
		map[string]interface{}{"type": "text", "text": "middle answer"},
	}))

	dir, path := incrementalTestFile(t, root+reply+laterPrompt+laterReply)
	reader := newIncrementalSessionReader()
	if _, err := reader.read(dir, path, incrementalTestSessionID, false); err != nil {
		t.Fatalf("initial full read: %v", err)
	}
	appendIncrementalTestData(t, path, system+middle)
	result, err := reader.read(dir, path, incrementalTestSessionID, false)
	if err != nil {
		t.Fatalf("read out-of-order append: %v", err)
	}
	if !result.incremental || !result.changed {
		t.Errorf("incremental=%t changed=%t, want true/true", result.incremental, result.changed)
	}

	parser := NewJSONLParser()
	if err := parser.ParseProjectSessionsForSession(dir, true, incrementalTestSessionID); err != nil {
		t.Fatalf("full reference parse: %v", err)
	}
	fullSession := parser.FindSession(incrementalTestSessionID)
	if fullSession == nil {
		t.Fatal("full reference session not found")
	}
	fullAgent := convertToAgentChatSession(*fullSession, "", false)
	if !reflect.DeepEqual(result.session.Records, fullSession.Records) {
		t.Error("out-of-order records differ from full rebuild")
	}
	if !reflect.DeepEqual(result.agentSession.SessionData, fullAgent.SessionData) {
		t.Error("out-of-order SessionData differs from full rebuild")
	}
	if result.agentSession.RawData != fullAgent.RawData {
		t.Error("out-of-order RawData differs from full rebuild")
	}
	if len(result.agentSession.SessionData.Exchanges) != 2 ||
		len(result.agentSession.SessionData.Exchanges[0].Messages) != 3 ||
		len(result.agentSession.SessionData.Exchanges[1].Messages) != 2 {
		t.Errorf("unexpected exchanges: %#v", result.agentSession.SessionData.Exchanges)
	}
}

func TestIncrementalSessionReader_UnsafeAppendUsesFullRebuild(t *testing.T) {
	root := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, "first"))
	duplicate := incrementalTestLine(t, incrementalTestRecord("assistant", "root", 1, "root", []interface{}{
		map[string]interface{}{"type": "text", "text": "duplicate"},
	}))
	summaryData, err := json.Marshal(map[string]interface{}{
		"type": "summary", "summary": "new title", "leafUuid": "root",
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		append  string
		wantLen int
	}{
		{name: "duplicate uuid", append: duplicate, wantLen: 1},
		{name: "summary changes an older record", append: string(summaryData) + "\n", wantLen: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, path := incrementalTestFile(t, root)
			reader := newIncrementalSessionReader()
			if _, err := reader.read(dir, path, incrementalTestSessionID, false); err != nil {
				t.Fatal(err)
			}
			appendIncrementalTestData(t, path, tt.append)
			result, err := reader.read(dir, path, incrementalTestSessionID, false)
			if err != nil {
				t.Fatalf("read unsafe append: %v", err)
			}
			if result.incremental {
				t.Error("unsafe append did not use full rebuild")
			}
			if len(result.session.Records) != tt.wantLen {
				t.Errorf("records = %d, want %d", len(result.session.Records), tt.wantLen)
			}
			if tt.name == "summary changes an older record" {
				if _, ok := result.session.Records[0].Data["summary"]; !ok {
					t.Error("full rebuild did not attach summary to previous record")
				}
			}
		})
	}
}

func TestIncrementalSessionReader_CrossFileAppendUsesFullRebuild(t *testing.T) {
	root := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, "first"))
	dir, firstPath := incrementalTestFile(t, root)
	secondPath := filepath.Join(dir, fmt.Sprintf("%s-resumed.jsonl", incrementalTestSessionID))
	second := incrementalTestLine(t, incrementalTestRecord("assistant", "reply", 1, "root", []interface{}{
		map[string]interface{}{"type": "text", "text": "answer"},
	}))
	if err := os.WriteFile(secondPath, []byte(second), 0600); err != nil {
		t.Fatal(err)
	}
	reader := newIncrementalSessionReader()
	result, err := reader.read(dir, secondPath, incrementalTestSessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.session.Records) != 2 {
		t.Fatalf("initial records = %d, want 2", len(result.session.Records))
	}
	appendIncrementalTestData(t, firstPath, incrementalTestLine(t, incrementalTestRecord("user", "late", 2, "reply", "late")))
	result, err = reader.read(dir, firstPath, incrementalTestSessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.incremental {
		t.Error("cross-file resume append did not use full rebuild")
	}
}

func TestIncrementalSessionReader_ExistingFixturesMatchFullRebuild(t *testing.T) {
	fixtureRecords := []JSONLRecord{
		promptRecord(false),
		assistantRecord(false, [2]string{"shell-1", "Bash"}),
		toolResultRecord("shell-1"),
		assistantRecord(true, [2]string{"shell-2", "PowerShell"}),
		promptRecord(false),
	}
	lines := make([]string, len(fixtureRecords))
	for i, record := range fixtureRecords {
		record.Data["sessionId"] = incrementalTestSessionID
		record.Data["uuid"] = fmt.Sprintf("fixture-%d", i)
		record.Data["timestamp"] = time.Date(2026, 10, 1, 0, 0, i, 0, time.UTC).Format(time.RFC3339)
		record.Data["cwd"] = "/workspace"
		if i == 0 {
			record.Data["parentUuid"] = nil
		} else {
			record.Data["parentUuid"] = fmt.Sprintf("fixture-%d", i-1)
		}
		lines[i] = incrementalTestLine(t, record.Data)
	}

	dir, path := incrementalTestFile(t, lines[0])
	reader := newIncrementalSessionReader()
	for i, line := range lines {
		if i > 0 {
			appendIncrementalTestData(t, path, line)
		}
		result, err := reader.read(dir, path, incrementalTestSessionID, false)
		if err != nil {
			t.Fatalf("read fixture record %d: %v", i, err)
		}
		if i > 0 && !result.incremental {
			t.Errorf("fixture record %d used a full rebuild", i)
		}

		parser := NewJSONLParser()
		if err := parser.ParseProjectSessionsForSession(dir, true, incrementalTestSessionID); err != nil {
			t.Fatalf("full reference parse %d: %v", i, err)
		}
		fullSession := parser.FindSession(incrementalTestSessionID)
		if fullSession == nil {
			t.Fatalf("full reference session %d not found", i)
		}
		fullAgent := convertToAgentChatSession(*fullSession, "", false)
		if !reflect.DeepEqual(result.session.Records, fullSession.Records) {
			t.Errorf("fixture record %d: incremental records differ from full rebuild", i)
		}
		if !reflect.DeepEqual(result.agentSession.SessionData, fullAgent.SessionData) {
			t.Errorf("fixture record %d: incremental SessionData differs from full rebuild", i)
		}
		if result.agentSession.RawData != fullAgent.RawData {
			t.Errorf("fixture record %d: incremental RawData differs from full rebuild", i)
		}
		if got, want := result.openShellToolUses(), openShellToolUses(fullSession.Records); !reflect.DeepEqual(got, want) {
			t.Errorf("fixture record %d: open shell tool uses = %v, want %v", i, got, want)
		}
	}
}

// A rewrite that keeps the file's identity and grows it past the cached size
// must be detected by the prefix checkpoint, not read from the stale offset.
func TestIncrementalSessionReader_InPlaceLargerRewriteUsesFullRebuild(t *testing.T) {
	root := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, "first"))
	reply := incrementalTestLine(t, incrementalTestRecord("assistant", "reply", 1, "root", []interface{}{
		map[string]interface{}{"type": "text", "text": "answer"},
	}))
	rewrittenRoot := incrementalTestLine(t, incrementalTestRecord("user", "root", 0, nil, "a much longer rewritten first prompt"))
	newPrompt := incrementalTestLine(t, incrementalTestRecord("user", "new-prompt", 2, "reply", "next"))

	dir, path := incrementalTestFile(t, root+reply)
	reader := newIncrementalSessionReader()
	if _, err := reader.read(dir, path, incrementalTestSessionID, false); err != nil {
		t.Fatalf("initial full read: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// WriteFile truncates and rewrites the same inode.
	if err := os.WriteFile(path, []byte(rewrittenRoot+reply+newPrompt), 0600); err != nil {
		t.Fatalf("rewrite transcript in place: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || after.Size() <= before.Size() {
		t.Fatalf("test setup must keep identity and grow the file")
	}

	result, err := reader.read(dir, path, incrementalTestSessionID, false)
	if err != nil {
		t.Fatalf("read rewritten transcript: %v", err)
	}
	if result.incremental {
		t.Error("in-place larger rewrite was applied incrementally, want full rebuild")
	}
	if len(result.session.Records) != 3 {
		t.Errorf("records = %d, want 3", len(result.session.Records))
	}
	if got := result.session.Records[0].Data["message"].(map[string]interface{})["content"]; got != "a much longer rewritten first prompt" {
		t.Errorf("first record content = %v, want the rewritten prompt", got)
	}
}

// Inserting a record after the leading assistant record but before the first
// prompt rebuilds from record zero, so no old exchange may be retained.
func TestIncrementalSessionReader_AssistantFirstOutOfOrderDoesNotDuplicateLeadingExchange(t *testing.T) {
	text := func(s string) []interface{} {
		return []interface{}{map[string]interface{}{"type": "text", "text": s}}
	}
	lead := incrementalTestLine(t, incrementalTestRecord("assistant", "lead", 1, nil, text("leading")))
	prompt := incrementalTestLine(t, incrementalTestRecord("user", "prompt", 3, "lead", "question"))
	reply := incrementalTestLine(t, incrementalTestRecord("assistant", "reply", 4, "prompt", text("answer")))
	inserted := incrementalTestLine(t, incrementalTestRecord("assistant", "inserted", 2, "lead", text("inserted")))

	dir, path := incrementalTestFile(t, lead+prompt+reply)
	reader := newIncrementalSessionReader()
	if _, err := reader.read(dir, path, incrementalTestSessionID, false); err != nil {
		t.Fatalf("initial full read: %v", err)
	}
	appendIncrementalTestData(t, path, inserted)
	result, err := reader.read(dir, path, incrementalTestSessionID, false)
	if err != nil {
		t.Fatalf("read out-of-order append: %v", err)
	}
	parser := NewJSONLParser()
	if err := parser.ParseProjectSessionsForSession(dir, true, incrementalTestSessionID); err != nil {
		t.Fatalf("full reference parse: %v", err)
	}
	fullSession := parser.FindSession(incrementalTestSessionID)
	fullAgent := convertToAgentChatSession(*fullSession, "", false)
	if !reflect.DeepEqual(result.agentSession.SessionData.Exchanges, fullAgent.SessionData.Exchanges) {
		t.Errorf("assistant-first exchanges differ from full rebuild: got %d exchanges, want %d",
			len(result.agentSession.SessionData.Exchanges), len(fullAgent.SessionData.Exchanges))
	}
}
