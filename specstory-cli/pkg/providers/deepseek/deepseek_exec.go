package deepseek

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// expandTilde expands a leading ~ to the user's home directory so users can
// configure custom commands like "~/bin/deepseek".
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

// parseCommand splits a custom command string into executable name and args.
// Returns default command if customCommand is empty.
func parseCommand(customCommand string) (string, []string) {
	if strings.TrimSpace(customCommand) != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			return expandTilde(parts[0]), parts[1:]
		}
	}
	return defaultCommand, nil
}

// ExecuteDeepSeek launches the DeepSeek TUI process and blocks until it exits.
func ExecuteDeepSeek(customCommand string, resumeSessionID string) error {
	cmdName, args := parseCommand(customCommand)
	args = ensureResumeArgs(args, resumeSessionID)
	if resumeSessionID != "" {
		slog.Info("ExecuteDeepSeek: resuming session", "sessionId", resumeSessionID)
	}

	command := exec.Command(cmdName, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	return command.Run()
}

// ensureResumeArgs adds --resume <sessionID> to args if not already present.
// DeepSeek TUI uses --resume to continue an existing session.
func ensureResumeArgs(args []string, resumeSessionID string) []string {
	if resumeSessionID == "" {
		return args
	}

	for i, arg := range args {
		if arg == "--resume" || arg == "-r" {
			// Already has --resume; keep existing value.
			if i+1 < len(args) {
				return args
			}
		}
		if strings.HasPrefix(arg, "--resume=") {
			return args
		}
	}

	return append(args, "--resume", resumeSessionID)
}
