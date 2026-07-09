package piagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// piSessionsRoot returns ~/.pi/agent/sessions, the directory under which pi
// stores one subdirectory per working directory (each named with the encoded
// cwd, see EncodeCwd). It does not require the directory to exist.
func piSessionsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("pi: cannot resolve home dir: %w", err)
	}
	return filepath.Join(home, ".pi", "agent", "sessions"), nil
}

// EncodeCwd maps a working directory to pi's session-subdirectory name.
// pi replaces every '/' in the absolute path with '-' and wraps the result with
// a leading '-' and a trailing '--', producing e.g. "/Users/jane/proj" ->
// "--Users-jane-proj--". The encoding is derived from real session dirs:
// "/Users/jakelevirne" -> "--Users-jakelevirne--" and
// "/Users/jakelevirne/dev/getspecstory" -> "--Users-jakelevirne-dev-getspecstory--".
func EncodeCwd(cwd string) string {
	encoded := strings.ReplaceAll(cwd, "/", "-")
	return "-" + encoded + "--"
}

// ProjectSessionDir returns the absolute path to pi's session subdirectory for
// the given project, WITHOUT requiring it to exist. Callers (DetectAgent, sync)
// check existence separately.
func ProjectSessionDir(projectPath string) (string, error) {
	root, err := piSessionsRoot()
	if err != nil {
		return "", err
	}
	cwd := strings.TrimSpace(projectPath)
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("pi: cannot get working dir: %w", err)
		}
	}
	return filepath.Join(root, EncodeCwd(cwd)), nil
}

// SessionFilesInProject lists the *.jsonl files in the project's pi session
// directory. Returns an empty slice (no error) if the directory does not exist.
func SessionFilesInProject(projectPath string) ([]string, error) {
	dir, err := ProjectSessionDir(projectPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("pi: reading session dir %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	return files, nil
}
