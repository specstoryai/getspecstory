package antigravitycli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Antigravity CLI stores its data under ~/.gemini/antigravity-cli/. NOTE this
// shares the ~/.gemini/ root with the Gemini CLI provider, but the two never
// overlap: Antigravity lives entirely under the antigravity-cli/ subtree.
const (
	geminiRootDir      = ".gemini"
	antigravityDirName = "antigravity-cli"
	brainDirName       = "brain"
	historyFileName    = "history.jsonl"

	// Each conversation's transcript lives several levels deep under its brain
	// dir at the stable path <id>/.system_generated/logs/transcript_full.jsonl.
	systemGeneratedDir = ".system_generated"
	logsDirName        = "logs"

	// transcriptFileName is the primary source: tool-call args are native JSON.
	transcriptFileName = "transcript_full.jsonl"
	// fallbackTranscriptFileName is parsed only when the primary file is absent;
	// it carries the same steps but with each tool-arg value double-encoded.
	fallbackTranscriptFileName = "transcript.jsonl"
)

// conversationFile identifies one conversation's transcript on disk. It is the
// analog of deepseektui's sessionFile, but keyed by the conversationId (the
// brain dir name) rather than a flat file name.
type conversationFile struct {
	ConversationID string
	Path           string // absolute path to the transcript JSONL file
	ModTime        int64  // UnixNano, used for change detection in the watcher
}

// resolveAntigravityDir returns the path to ~/.gemini/antigravity-cli.
func resolveAntigravityDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("antigravity: cannot resolve home dir: %w", err)
	}
	return filepath.Join(home, geminiRootDir, antigravityDirName), nil
}

// resolveBrainDir returns the path to ~/.gemini/antigravity-cli/brain, the
// directory that holds one subdirectory per conversation.
func resolveBrainDir() (string, error) {
	base, err := resolveAntigravityDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, brainDirName), nil
}

// resolveHistoryPath returns the path to the interactive-session history index.
func resolveHistoryPath() (string, error) {
	base, err := resolveAntigravityDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, historyFileName), nil
}

// transcriptDirFor returns the directory that directly contains a conversation's
// transcript file. The watcher must add a watch on this exact directory because
// fsnotify is non-recursive and only reports events for a watched dir's direct
// children.
func transcriptDirFor(conversationID string) (string, error) {
	brain, err := resolveBrainDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(brain, conversationID, systemGeneratedDir, logsDirName), nil
}

// resolveTranscriptPath returns the best transcript file for a conversation,
// preferring transcript_full.jsonl and falling back to transcript.jsonl. It
// returns ("", nil) when neither file exists (i.e. not a usable conversation).
func resolveTranscriptPath(conversationID string) (string, error) {
	dir, err := transcriptDirFor(conversationID)
	if err != nil {
		return "", err
	}

	primary := filepath.Join(dir, transcriptFileName)
	if _, err := os.Stat(primary); err == nil {
		return primary, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	fallback := filepath.Join(dir, fallbackTranscriptFileName)
	if _, err := os.Stat(fallback); err == nil {
		return fallback, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	return "", nil
}

// listConversationFiles returns every conversation that has a readable
// transcript, sorted newest-first by transcript modification time. A missing
// brain directory is not an error — it just means Antigravity hasn't run yet.
func listConversationFiles() ([]conversationFile, error) {
	brain, err := resolveBrainDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(brain)
	if err != nil {
		if os.IsNotExist(err) {
			return []conversationFile{}, nil
		}
		return nil, fmt.Errorf("antigravity: unable to read brain dir: %w", err)
	}

	var files []conversationFile
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		conversationID := entry.Name()

		path, err := resolveTranscriptPath(conversationID)
		if err != nil {
			continue
		}
		if path == "" {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		files = append(files, conversationFile{
			ConversationID: conversationID,
			Path:           path,
			ModTime:        info.ModTime().UnixNano(),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime > files[j].ModTime
	})

	return files, nil
}

// findTranscriptByID resolves the transcript path for a specific conversation.
// Returns ("", nil) when the conversation has no transcript on disk — that's
// "not found", not an error.
func findTranscriptByID(conversationID string) (string, error) {
	return resolveTranscriptPath(conversationID)
}

// canonicalizePath resolves a path to its canonical form for comparison, so that
// symlinks and case-insensitive filesystems (macOS) compare equal.
func canonicalizePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if canonical, err := spi.GetCanonicalPath(trimmed); err == nil {
		return canonical
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(trimmed)
}

// pathWithin reports whether candidate is equal to or nested inside root, after
// canonicalizing both. Used to match a session's touched files against the
// project path the user is syncing from.
func pathWithin(candidate, root string) bool {
	cRoot := canonicalizePath(root)
	cCandidate := canonicalizePath(candidate)
	if cRoot == "" || cCandidate == "" {
		return false
	}
	if cCandidate == cRoot {
		return true
	}
	rel, err := filepath.Rel(cRoot, cCandidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
