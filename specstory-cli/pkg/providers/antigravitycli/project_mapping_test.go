package antigravitycli

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	testProjectID      = "11111111-1111-4111-8111-111111111111"
	testConversationID = "22222222-2222-4222-8222-222222222222"
)

func TestLoadProjectWorkspaceIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "repo with space")

	writeProjectConfig(t, home, testProjectID, `{
		"id":"`+testProjectID+`",
		"name":"ignored-relative-name",
		"projectResources":{"resources":[
			{"gitFolder":{"folderUri":"file://`+filepath.ToSlash(workspace)+`"}}
		]}
	}`)

	index, err := loadProjectWorkspaceIndex()
	if err != nil {
		t.Fatalf("loadProjectWorkspaceIndex: %v", err)
	}
	if got := index[testProjectID]; got != workspace {
		t.Errorf("project workspace = %q, want %q", got, workspace)
	}
}

func TestLoadConversationWorkspaceIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "repo")

	writeProjectConfig(t, home, testProjectID, `{
		"id":"`+testProjectID+`",
		"name":"`+workspace+`",
		"projectResources":{"resources":[]}
	}`)
	writeAntigravityLog(t, home, "cli-test.log",
		`I common.go:156] project: using project "`+workspace+`" (id=`+testProjectID+`) at 2026-05-26`,
		`I server.go:726] Conversation using project ID: `+testProjectID,
		`I server.go:747] Created conversation `+testConversationID,
	)

	index, err := loadConversationWorkspaceIndex()
	if err != nil {
		t.Fatalf("loadConversationWorkspaceIndex: %v", err)
	}
	if got := index[testConversationID]; got != workspace {
		t.Errorf("conversation workspace = %q, want %q", got, workspace)
	}
}

func TestIndexConversationProjectsFromLog_PendingConversationBeforeProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.log")
	if err := os.WriteFile(path, []byte(
		`I server.go:747] Created conversation `+testConversationID+"\n"+
			`I server.go:726] Conversation using project ID: `+testProjectID+"\n",
	), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	index := make(map[string]string)
	if err := indexConversationProjectsFromLog(path, index); err != nil {
		t.Fatalf("indexConversationProjectsFromLog: %v", err)
	}
	if got := index[testConversationID]; got != testProjectID {
		t.Errorf("conversation project = %q, want %q", got, testProjectID)
	}
}

func TestStripFileURI_DecodesEscapedPath(t *testing.T) {
	if got := stripFileURI("file:///tmp/repo%20with%20space/main.go"); got != "/tmp/repo with space/main.go" {
		t.Errorf("stripFileURI decoded path = %q", got)
	}
}

func writeProjectConfig(t *testing.T, home, projectID, body string) {
	t.Helper()
	dir := filepath.Join(home, geminiRootDir, geminiConfigDirName, geminiProjectsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, projectID+".json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
}

func writeAntigravityLog(t *testing.T, home, name string, lines ...string) {
	t.Helper()
	dir := filepath.Join(home, geminiRootDir, antigravityDirName, logDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	body := ""
	for _, line := range lines {
		body += line + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write antigravity log: %v", err)
	}
}
