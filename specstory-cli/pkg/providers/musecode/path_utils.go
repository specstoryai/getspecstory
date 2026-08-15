package musecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Package-level variables for mocking in tests
var (
	osUserHomeDir = os.UserHomeDir
	osGetwd       = os.Getwd
)

const (
	// sessionFileName is the transcript inside every session directory.
	sessionFileName = "session.jsonl"

	// subagentDirName holds one transcript per spawned subagent. Those are not
	// independent sessions and must never be enumerated as such.
	subagentDirName = "subagent"

	// maxHeaderBytes bounds how much of a transcript is read to find its
	// workspace root. The metadata record is written at session open and is
	// always the first line, so a small prefix answers the question without
	// touching multi-megabyte transcripts.
	maxHeaderBytes = 256 * 1024
)

// museSessionFile is a discovered transcript plus the header fields needed to
// decide which project it belongs to, without parsing the whole file.
type museSessionFile struct {
	Path          string
	SessionID     string
	WorkspaceRoot string
}

// GetMuseSessionsDir returns the root of Muse Code's session store. Muse is an
// XDG-conformant application, so XDG_DATA_HOME wins when set and the store
// otherwise lives at ~/.local/share/muse/sessions.
func GetMuseSessionsDir() (string, error) {
	if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
		return filepath.Join(dataHome, "muse", "sessions"), nil
	}

	homeDir, err := osUserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(homeDir, ".local", "share", "muse", "sessions"), nil
}

// defaultProjectPath fills in an empty project path with the current working
// directory. Provider entry points call this once so the same resolved path is
// used both for matching sessions to the project and as the workspace root
// stamped into generated session data (stamping the raw empty string would fail
// schema validation and break path hint normalization).
func defaultProjectPath(projectPath string) (string, error) {
	if projectPath != "" {
		return projectPath, nil
	}
	cwd, err := osGetwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}
	return cwd, nil
}

// walkMuseSessionFiles calls visit for every top-level session transcript under
// the date-sharded store. Subagent directories are pruned outright: they hold
// full transcripts in the same format, so enumerating them would invent
// sessions the user never started.
func walkMuseSessionFiles(sessionsRoot string, visit func(path string)) error {
	return filepath.WalkDir(sessionsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than abort the sweep
		}
		if d.IsDir() {
			if d.Name() == subagentDirName {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != sessionFileName {
			return nil
		}
		visit(path)
		return nil
	})
}

// IsSubagentTranscript reports whether a transcript path sits under a subagent
// directory. Subagent transcripts share the session format but belong to their
// parent session, so every enumeration path filters them out.
//
// Matching happens on the slash form so the shape of the path decides, not the
// platform: Windows accepts forward slashes too, and a path can arrive from a
// record written on another OS.
func IsSubagentTranscript(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/"+subagentDirName+"/")
}

// workspaceMatchesProject reports whether a transcript's recorded workspace
// root is the given project, which must already be canonicalized.
//
// A transcript that records no workspace root never matches. The store is
// global, so a session that cannot name its own project cannot be claimed by
// one: treating "unknown" as "mine" would let any project adopt it.
func workspaceMatchesProject(workspaceRoot string, canonicalProject string) bool {
	return workspaceRoot != "" && spi.CanonicalizePathOrClean(workspaceRoot) == canonicalProject
}

// sessionFileBelongsToProject reports whether the transcript at path records
// this project as its workspace, reading only the file's bounded header rather
// than parsing it.
//
// The Muse store is global and keyed by session id alone, so possessing an id
// says nothing about which project the session belongs to. Callers that resolve
// a session by id must ask this before returning it, or a lookup made from one
// project will hand back another project's conversation.
func sessionFileBelongsToProject(path string, projectPath string) bool {
	workspaceRoot, err := readSessionWorkspaceRoot(path)
	if err != nil {
		slog.Debug("sessionFileBelongsToProject: Skipping unreadable transcript",
			"path", path, "error", err)
		return false
	}
	return workspaceMatchesProject(workspaceRoot, spi.CanonicalizePathOrClean(projectPath))
}

// readSessionWorkspaceRoot reads just enough of a transcript to recover the
// workspace root recorded in its metadata record. Returns "" when the file
// carries no metadata (a transcript truncated before its first record).
func readSessionWorkspaceRoot(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open session file: %w", err)
	}
	defer func() { _ = file.Close() }() // Read-only file; close errors not actionable

	header := make([]byte, maxHeaderBytes)
	n, err := io.ReadFull(file, header)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", fmt.Errorf("failed to read session header: %w", err)
	}

	// The final line of the prefix is usually cut mid-record; it fails to
	// unmarshal and is skipped like any other unreadable line.
	for _, line := range bytes.Split(header[:n], []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var record MuseRecord
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		if record.PayloadType != payloadTypeMetadata {
			continue
		}

		var payload museMetadataPayload
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			continue
		}
		if payload.Record.WorkspaceRoot != "" {
			return payload.Record.WorkspaceRoot, nil
		}
	}

	return "", nil
}

// findMuseSessions returns the transcripts whose recorded workspace root is the
// given project. The store is global and date-sharded rather than project
// scoped, so project association can only come from inside each file.
// Results are ordered most recent first (the YYYY/MM/DD path sorts
// chronologically).
func findMuseSessions(projectPath string) ([]museSessionFile, error) {
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return nil, err
	}

	sessionsRoot, err := GetMuseSessionsDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(sessionsRoot); err != nil {
		if os.IsNotExist(err) {
			slog.Debug("findMuseSessions: Muse sessions directory does not exist", "sessionsRoot", sessionsRoot)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read Muse sessions directory %q: %w", sessionsRoot, err)
	}

	targetRoot := spi.CanonicalizePathOrClean(projectPath)

	var paths []string
	if err := walkMuseSessionFiles(sessionsRoot, func(path string) {
		paths = append(paths, path)
	}); err != nil {
		return nil, fmt.Errorf("failed to walk Muse sessions directory: %w", err)
	}

	var matches []museSessionFile
	for _, path := range paths {
		workspaceRoot, err := readSessionWorkspaceRoot(path)
		if err != nil {
			slog.Debug("findMuseSessions: Skipping unreadable transcript", "path", path, "error", err)
			continue
		}
		if !workspaceMatchesProject(workspaceRoot, targetRoot) {
			continue
		}
		matches = append(matches, museSessionFile{
			Path:          path,
			SessionID:     sessionIDFromPath(path),
			WorkspaceRoot: workspaceRoot,
		})
	}

	// Descending path order is descending date order, so the newest sessions
	// come first without stat-ing every file.
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Path > matches[j].Path
	})

	slog.Debug("findMuseSessions: Completed scan",
		"sessionsRoot", sessionsRoot,
		"projectPath", projectPath,
		"transcripts", len(paths),
		"matches", len(matches))

	return matches, nil
}

// findMuseSessionPathByID locates a session directory by id without reading any
// transcript: the store is keyed by session id, so the id is a path component.
// Returns "" when no such session exists.
func findMuseSessionPathByID(sessionID string) (string, error) {
	if sessionID == "" {
		return "", nil
	}

	sessionsRoot, err := GetMuseSessionsDir()
	if err != nil {
		return "", err
	}

	// sessions/YYYY/MM/DD/<session-id>/session.jsonl
	pattern := filepath.Join(sessionsRoot, "*", "*", "*", sessionID, sessionFileName)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("failed to search Muse sessions for %q: %w", sessionID, err)
	}
	if len(matches) == 0 {
		return "", nil
	}

	// A session id is unique; more than one match means the same id was
	// re-shared across days, so take the most recent.
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	return matches[0], nil
}
