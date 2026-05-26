package antigravitycli

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

const (
	// defaultCommand is the Antigravity CLI launcher. The installed binary is
	// `agy` (an `antigravity` alias exists but is not normally on PATH).
	defaultCommand = "agy"
	versionFlag    = "--version"
	// resumeFlag continues a specific conversation by id; `-c` (most recent) is
	// the alternative the CLI offers but we always target a known id.
	resumeFlag = "--conversation"
)

// expandTilde expands a leading ~ to the user's home directory so users can
// configure custom commands like "~/bin/agy".
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

// parseCommand splits a custom command string into executable name and args,
// returning the default command when customCommand is empty.
func parseCommand(customCommand string) (string, []string) {
	if strings.TrimSpace(customCommand) != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			return expandTilde(parts[0]), parts[1:]
		}
	}
	return defaultCommand, nil
}

// ExecuteAntigravity launches the Antigravity CLI and blocks until it exits.
func ExecuteAntigravity(customCommand string, resumeSessionID string) error {
	cmdName, args := parseCommand(customCommand)
	args = ensureResumeArgs(args, resumeSessionID)
	if resumeSessionID != "" {
		slog.Info("ExecuteAntigravity: resuming conversation", "conversationId", resumeSessionID)
	}

	command := exec.Command(cmdName, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	return command.Run()
}

// ensureResumeArgs adds --conversation <id> to args when resuming, unless the
// caller already supplied a conversation flag with a value.
func ensureResumeArgs(args []string, resumeSessionID string) []string {
	if resumeSessionID == "" {
		return args
	}

	for i, arg := range args {
		if arg == resumeFlag {
			if i+1 < len(args) && strings.TrimSpace(args[i+1]) != "" && !strings.HasPrefix(args[i+1], "-") {
				return args
			}
			// slices.Concat always allocates a new backing array, so the caller's
			// slice is never mutated.
			return slices.Concat(args[:i+1], []string{resumeSessionID}, args[i+1:])
		}
		if strings.HasPrefix(arg, resumeFlag+"=") {
			if strings.TrimSpace(strings.TrimPrefix(arg, resumeFlag+"=")) != "" {
				return args
			}
			repaired := slices.Clone(args)
			repaired[i] = resumeFlag + "=" + resumeSessionID
			return repaired
		}
	}

	return append(args, resumeFlag, resumeSessionID)
}
