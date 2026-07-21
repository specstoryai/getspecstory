package antigravitycli

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite" // Pure Go SQLite driver, shared with the cursor providers
)

const (
	geminiConfigDirName           = "config"
	geminiProjectsDirName         = "projects"
	logDirName                    = "log"
	conversationSummariesFileName = "conversation_summaries.db"
)

var (
	projectIDFromUsingRe        = regexp.MustCompile(`project: using project .*\(id=([0-9a-fA-F-]{36})\)`)
	projectIDFromConversationRe = regexp.MustCompile(`Conversation using project ID:\s*([0-9a-fA-F-]{36})`)
	conversationIDLogRes        = []*regexp.Regexp{
		regexp.MustCompile(`Created conversation ([0-9a-fA-F-]{36})`),
		regexp.MustCompile(`Continuing last-used conversation(?: \(from cache\))?: ([0-9a-fA-F-]{36})`),
		regexp.MustCompile(`Print mode: resuming conversation ([0-9a-fA-F-]{36})`),
		regexp.MustCompile(`Print mode: conversation=([0-9a-fA-F-]{36})`),
		regexp.MustCompile(`conversationID="([0-9a-fA-F-]{36})"`),
	}
)

type antigravityProjectFile struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ProjectResources struct {
		Resources []struct {
			GitFolder struct {
				FolderURI string `json:"folderUri"`
			} `json:"gitFolder"`
			FolderURI string `json:"folderUri"`
			Path      string `json:"path"`
		} `json:"resources"`
	} `json:"projectResources"`
}

// conversationSummary is the subset of a conversation_summaries.db row this
// provider uses to enrich a session's display name. Antigravity generates title
// and preview asynchronously, so either (or the whole row) may be missing for a
// given conversation — callers must fall back to a prompt-derived name.
type conversationSummary struct {
	Title   string
	Preview string
}

// bestName returns the human-readable name from the summary, preferring the
// generated title over the preview. Empty when neither is populated yet.
func (s conversationSummary) bestName() string {
	if title := strings.TrimSpace(s.Title); title != "" {
		return title
	}
	return strings.TrimSpace(s.Preview)
}

func resolveConversationSummariesPath() (string, error) {
	base, err := resolveAntigravityDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, conversationSummariesFileName), nil
}

// loadConversationSummaryIndex reads conversation_summaries.db and returns a map
// conversationId → conversationSummary. It is a best-effort enrichment source:
// Antigravity populates the store asynchronously and does not index every
// conversation, so a missing file, missing table, or absent row is not an error
// — the caller falls back to a name derived from the first user prompt. The DB is
// opened read-only so a live `agy` writer is never blocked.
func loadConversationSummaryIndex() (map[string]conversationSummary, error) {
	empty := map[string]conversationSummary{}

	path, err := resolveConversationSummariesPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return empty, nil // no summaries DB yet — nothing to enrich with
	}

	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		slog.Debug("antigravity: cannot open conversation summaries db", "path", path, "error", err)
		return empty, nil
	}
	defer func() { _ = db.Close() }()

	// WAL mode keeps reads from blocking a live agy writer (best-effort).
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		slog.Debug("antigravity: failed to set WAL on summaries db", "error", err)
	}

	rows, err := db.Query("SELECT conversation_id, title, preview FROM conversation_summaries")
	if err != nil {
		slog.Debug("antigravity: cannot query conversation summaries", "error", err)
		return empty, nil
	}
	defer func() { _ = rows.Close() }()

	index := make(map[string]conversationSummary)
	for rows.Next() {
		var id, title, preview string
		if err := rows.Scan(&id, &title, &preview); err != nil {
			slog.Debug("antigravity: skipping malformed summary row", "error", err)
			continue
		}
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		index[id] = conversationSummary{Title: title, Preview: preview}
	}
	if err := rows.Err(); err != nil {
		slog.Debug("antigravity: error iterating summary rows", "error", err)
	}
	return index, nil
}

func resolveGeminiProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("antigravity: cannot resolve home dir: %w", err)
	}
	return filepath.Join(home, geminiRootDir, geminiConfigDirName, geminiProjectsDirName), nil
}

func resolveAntigravityLogDir() (string, error) {
	base, err := resolveAntigravityDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, logDirName), nil
}

// loadProjectWorkspaceIndex maps Antigravity project IDs to workspace roots from
// ~/.gemini/config/projects/*.json.
func loadProjectWorkspaceIndex() (map[string]string, error) {
	dir, err := resolveGeminiProjectsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("antigravity: unable to read projects dir: %w", err)
	}

	index := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Debug("antigravity: skipping unreadable project config", "path", path, "error", err)
			continue
		}
		var project antigravityProjectFile
		if err := json.Unmarshal(data, &project); err != nil {
			slog.Debug("antigravity: skipping malformed project config", "path", path, "error", err)
			continue
		}
		id := strings.ToLower(strings.TrimSpace(project.ID))
		if id == "" {
			id = strings.ToLower(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		}
		workspace := projectWorkspacePath(project)
		if id == "" || workspace == "" {
			continue
		}
		index[id] = workspace
	}
	return index, nil
}

func projectWorkspacePath(project antigravityProjectFile) string {
	for _, resource := range project.ProjectResources.Resources {
		for _, candidate := range []string{
			resource.GitFolder.FolderURI,
			resource.FolderURI,
			resource.Path,
		} {
			if path := cleanWorkspacePath(candidate); path != "" {
				return path
			}
		}
	}
	if path := cleanWorkspacePath(project.Name); path != "" {
		return path
	}
	return ""
}

func cleanWorkspacePath(value string) string {
	path := filepath.Clean(strings.TrimSpace(stripFileURI(value)))
	if path == "." || !filepath.IsAbs(path) {
		return ""
	}
	return path
}

// loadConversationWorkspaceIndex maps Antigravity conversation IDs to workspace
// roots by joining CLI log conversation/project IDs to config/projects/*.json.
func loadConversationWorkspaceIndex() (map[string]string, error) {
	projectWorkspaces, err := loadProjectWorkspaceIndex()
	if err != nil {
		return nil, err
	}
	conversationProjects, err := loadConversationProjectIndex()
	if err != nil {
		return nil, err
	}

	index := make(map[string]string)
	for conversationID, projectID := range conversationProjects {
		if workspace := strings.TrimSpace(projectWorkspaces[projectID]); workspace != "" {
			index[conversationID] = workspace
		}
	}
	return index, nil
}

func loadConversationProjectIndex() (map[string]string, error) {
	logDir, err := resolveAntigravityLogDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("antigravity: unable to read log dir: %w", err)
	}

	index := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		path := filepath.Join(logDir, entry.Name())
		if err := indexConversationProjectsFromLog(path, index); err != nil {
			slog.Debug("antigravity: skipping CLI log for project mapping", "path", path, "error", err)
		}
	}
	return index, nil
}

func indexConversationProjectsFromLog(path string, index map[string]string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var currentProjectID string
	var pendingConversationIDs []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxTranscriptLineSize)
	for scanner.Scan() {
		line := scanner.Text()
		if projectID := projectIDFromLogLine(line); projectID != "" {
			currentProjectID = projectID
			for _, conversationID := range pendingConversationIDs {
				index[conversationID] = currentProjectID
			}
			pendingConversationIDs = nil
		}
		for _, conversationID := range conversationIDsFromLogLine(line) {
			if currentProjectID == "" {
				pendingConversationIDs = append(pendingConversationIDs, conversationID)
				continue
			}
			index[conversationID] = currentProjectID
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func projectIDFromLogLine(line string) string {
	for _, re := range []*regexp.Regexp{projectIDFromConversationRe, projectIDFromUsingRe} {
		if m := re.FindStringSubmatch(line); len(m) == 2 {
			return strings.ToLower(strings.TrimSpace(m[1]))
		}
	}
	return ""
}

func conversationIDsFromLogLine(line string) []string {
	var ids []string
	seen := make(map[string]bool)
	for _, re := range conversationIDLogRes {
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			if len(m) != 2 {
				continue
			}
			id := strings.ToLower(strings.TrimSpace(m[1]))
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}
