package droidcli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// nonAlphanumericDash matches any character that is not alphanumeric or a dash,
// mirroring the naming convention Factory CLI uses for session subdirectories.
var nonAlphanumericDash = regexp.MustCompile(`[^a-zA-Z0-9-]`)

// projectPathToSessionDirName converts a project path to the factory session directory name.
// Factory CLI uses the same convention as Claude Code: non-alphanumeric characters (except
// dashes) are replaced with dashes, and a leading dash is prepended.
func projectPathToSessionDirName(projectPath string) string {
	name := nonAlphanumericDash.ReplaceAllString(projectPath, "-")
	if !strings.HasPrefix(name, "-") {
		name = "-" + name
	}
	return name
}

// resolveProjectSessionDir returns the factory sessions subdirectory for a specific project path.
// Returns "" if projectPath is empty (caller should watch all sessions).
func resolveProjectSessionDir(projectPath string) (string, error) {
	if strings.TrimSpace(projectPath) == "" {
		return "", nil
	}

	// Canonicalize the path to match Factory CLI's real-path behaviour, resolving both
	// symlinks and case differences (important on macOS's case-insensitive filesystem,
	// where os.Getwd may return a different case than what the CLI recorded on disk).
	realPath, err := spi.GetCanonicalPath(projectPath)
	if err != nil {
		// Path may not exist yet; proceed with the given path.
		realPath = projectPath
	}

	sessionsDir, err := resolveSessionsDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(sessionsDir, projectPathToSessionDirName(realPath)), nil
}

// isTargetProjectDir returns true if path is a session directory that should be watched.
// When projectPath is set, only the project's derived directory qualifies; otherwise
// all directories qualify (the no-filter / sync-all case).
func isTargetProjectDir(path, projectPath string) bool {
	if projectPath == "" {
		return true
	}
	expected, err := resolveProjectSessionDir(projectPath)
	if err != nil || expected == "" {
		// Can't derive expected dir; allow everything to avoid missing sessions.
		return true
	}
	return path == expected
}

const (
	factoryRootDir     = ".factory"
	factorySessionsDir = "sessions"
)

type sessionFile struct {
	Path     string
	ModTime  int64
	FileName string
}

func resolveSessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("droidcli: cannot resolve home dir: %w", err)
	}
	dir := filepath.Join(home, factoryRootDir, factorySessionsDir)
	return dir, nil
}

func listSessionFiles() ([]sessionFile, error) {
	dir, err := resolveSessionsDir()
	if err != nil {
		return nil, err
	}
	files := make([]sessionFile, 0, 64)
	walker := func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(d.Name()) != ".jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, sessionFile{
			Path:     path,
			ModTime:  info.ModTime().UnixNano(),
			FileName: d.Name(),
		})
		return nil
	}
	if err := filepath.WalkDir(dir, walker); err != nil {
		if os.IsNotExist(err) {
			return []sessionFile{}, nil
		}
		return nil, fmt.Errorf("droidcli: unable to read sessions dir: %w", err)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime > files[j].ModTime
	})
	return files, nil
}

func findSessionFileByID(sessionID string) (string, error) {
	files, err := listSessionFiles()
	if err != nil {
		return "", err
	}
	for _, file := range files {
		candidate := file.FileName
		if candidate == sessionID || trimExt(candidate) == sessionID {
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

// settingsPathFromSession returns the path to the .settings.json file
// that corresponds to a session JSONL file.
// Example: /path/to/session.jsonl -> /path/to/session.settings.json
func settingsPathFromSession(sessionPath string) string {
	dir := filepath.Dir(sessionPath)
	base := filepath.Base(sessionPath)
	ext := filepath.Ext(base)
	nameWithoutExt := base[:len(base)-len(ext)]
	return filepath.Join(dir, nameWithoutExt+".settings.json")
}
