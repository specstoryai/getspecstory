package deepseek

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

const (
	deepSeekRootDir     = ".deepseek"
	deepSeekSessionsDir = "sessions"
)

type sessionFile struct {
	Path     string
	ModTime  int64
	FileName string
}

// resolveSessionsDir returns the path to DeepSeek TUI's sessions directory.
func resolveSessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("deepseek: cannot resolve home dir: %w", err)
	}
	return filepath.Join(home, deepSeekRootDir, deepSeekSessionsDir), nil
}

// listSessionFiles returns all JSON session files in ~/.deepseek/sessions/,
// sorted by modification time (newest first).
func listSessionFiles() ([]sessionFile, error) {
	dir, err := resolveSessionsDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []sessionFile{}, nil
		}
		return nil, fmt.Errorf("deepseek: unable to read sessions dir: %w", err)
	}

	var files []sessionFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		files = append(files, sessionFile{
			Path:     filepath.Join(dir, entry.Name()),
			ModTime:  info.ModTime().UnixNano(),
			FileName: entry.Name(),
		})
	}

	// Sort newest first.
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime > files[j].ModTime
	})

	return files, nil
}

// findSessionFileByID finds a session file by its ID (UUID).
// The ID should match a filename like <uuid>.json.
func findSessionFileByID(sessionID string) (string, error) {
	files, err := listSessionFiles()
	if err != nil {
		return "", err
	}

	for _, file := range files {
		candidate := trimExt(file.FileName)
		if candidate == sessionID || file.FileName == sessionID {
			return file.Path, nil
		}
	}
	return "", nil
}

func trimExt(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return name
	}
	return name[:len(name)-len(ext)]
}

// sessionMentionsProject checks whether a session file's workspace metadata
// matches the given project path.
func sessionMentionsProject(filePath string, projectPath string) bool {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return false
	}

	// Quick check: read only the metadata portion of the JSON to avoid
	// parsing the full messages array.
	workspace := extractWorkspaceFast(filePath)
	if workspace == "" {
		return false
	}

	canonicalProject := canonicalizePath(projectPath)
	canonicalWorkspace := canonicalizePath(workspace)

	return canonicalProject == canonicalWorkspace
}

// extractWorkspaceFast reads just the workspace field from a session file
// without parsing the full JSON. The metadata block is at the top of the file,
// so we stream and bail on the first match — important on the watcher hot
// path where this is called per fs event.
func extractWorkspaceFast(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	// DeepSeek session files can contain individual lines well over the default
	// 64KB scanner limit (long tool_result blobs); raise to 1MB which still
	// bounds memory but covers everything seen in practice.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if ws := matchWorkspace(line); ws != "" {
			return ws
		}
		// Once we cross into the messages array the metadata is behind us.
		if strings.Contains(line, `"messages"`) {
			return ""
		}
	}
	return ""
}

// matchWorkspace pulls the value out of a `"workspace": "<value>"` JSON line.
func matchWorkspace(line string) string {
	idx := strings.Index(line, `"workspace"`)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(`"workspace"`):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	rest = strings.TrimSpace(rest[colon+1:])
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// canonicalizePath resolves a path to its canonical form for comparison.
func canonicalizePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	canonical, err := spi.GetCanonicalPath(trimmed)
	if err == nil {
		return canonical
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(trimmed)
}
