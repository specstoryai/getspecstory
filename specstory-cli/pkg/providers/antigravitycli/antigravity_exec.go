package antigravitycli

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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

	err := command.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &spi.AgentExitError{Agent: "Antigravity CLI", Code: exitErr.ExitCode()}
	}
	return err
}

// ensureResumeArgs makes the requested conversation override configured flags.
func ensureResumeArgs(args []string, resumeSessionID string) []string {
	return spi.EnsureResumeFlagArgs(args, resumeSessionID, resumeFlag)
}
