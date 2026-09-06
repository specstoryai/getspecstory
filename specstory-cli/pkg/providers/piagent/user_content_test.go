package piagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestUserContentString covers the first-user-text extraction the scan path
// (list, reindex) relies on. The full-parse path (sync) trims each text part
// and skips blank ones, so the scan must do the same: a whitespace-only block
// is skipped, not returned as an empty string.
func TestUserContentString(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "whitespace-only first block then text falls through to the text",
			raw:  `[{"type":"text","text":"   \n\t"},{"type":"text","text":"  fix the flaky test  "}]`,
			want: "fix the flaky test",
		},
		{
			name: "whitespace-only string is empty",
			raw:  `"   \n  "`,
			want: "",
		},
		{
			name: "whitespace-only single block is empty",
			raw:  `[{"type":"text","text":" \t "}]`,
			want: "",
		},
		{
			name: "normal block returns its trimmed text",
			raw:  `[{"type":"text","text":" summarize the readme "}]`,
			want: "summarize the readme",
		},
		{
			name: "normal string returns its trimmed text",
			raw:  `" hello there "`,
			want: "hello there",
		},
		{
			name: "image block before text is skipped",
			raw:  `[{"type":"image","data":"abc"},{"type":"text","text":"what is in this picture"}]`,
			want: "what is in this picture",
		},
		{
			name: "empty content is empty",
			raw:  ``,
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := userContentString(json.RawMessage(tc.raw))
			if got != tc.want {
				t.Errorf("userContentString(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestListReindexSyncAgreeOnWhitespaceFirstBlock writes a session whose first
// user message starts with a whitespace-only text block and asserts that the
// scan path (ListAgentChatSessions for `list pi`, ListAllAgentChatSessions for
// `reindex`) lists it with the same slug the full parse (`sync pi`) derives.
func TestListReindexSyncAgreeOnWhitespaceFirstBlock(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(envAgentDir, tmp)
	projectPath := filepath.FromSlash("/pi-blank-first-block")

	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	userLine := `{"type":"message","id":"m1","parentId":null,"timestamp":"2026-09-03T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"   \n"},{"type":"text","text":"rename the helper"}],"timestamp":1788450646425}}`
	content := piHeaderLine("sess-blank-first", projectPath) + "\n" +
		userLine + "\n" +
		piAssistantLine("m2", "m1", "done") + "\n"
	path := filepath.Join(targetDir, "2026-09-03T10-00-00-000Z_sess-blank-first.jsonl")
	if wErr := os.WriteFile(path, []byte(content), 0o600); wErr != nil {
		t.Fatalf("WriteFile: %v", wErr)
	}

	p := NewProvider()

	// sync path: full parse.
	full, err := p.GetAgentChatSession(projectPath, "sess-blank-first", false)
	if err != nil {
		t.Fatalf("GetAgentChatSession: %v", err)
	}
	if full == nil {
		t.Fatal("GetAgentChatSession returned nil for a session sync renders")
	}
	if full.Slug == "" {
		t.Fatal("full parse produced an empty slug")
	}

	// list path.
	listed, err := p.ListAgentChatSessions(projectPath)
	if err != nil {
		t.Fatalf("ListAgentChatSessions: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListAgentChatSessions listed %d sessions, want 1: %+v", len(listed), listed)
	}
	if listed[0].Slug != full.Slug {
		t.Errorf("list slug %q != sync slug %q", listed[0].Slug, full.Slug)
	}

	// reindex path.
	refs, err := p.ListAllAgentChatSessions()
	if err != nil {
		t.Fatalf("ListAllAgentChatSessions: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("ListAllAgentChatSessions returned %d refs, want 1: %+v", len(refs), refs)
	}
	if refs[0].Slug != full.Slug {
		t.Errorf("reindex slug %q != sync slug %q", refs[0].Slug, full.Slug)
	}
	if refs[0].SessionID != "sess-blank-first" {
		t.Errorf("reindex SessionID = %q, want sess-blank-first", refs[0].SessionID)
	}
	if refs[0].NativePath != path {
		t.Errorf("reindex NativePath = %q, want %q", refs[0].NativePath, path)
	}
}
