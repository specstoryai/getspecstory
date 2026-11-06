package droidcli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"log/slog"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func parseDroidCommand(customCommand string) (string, []string) {
	if strings.TrimSpace(customCommand) != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			return expandTilde(parts[0]), parts[1:]
		}
	}
	return defaultFactoryCommand, nil
}

func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(homeDir, path[2:])
		}
	}
	return path
}

// ExecuteDroid runs the Droid CLI in projectPath and blocks until it exits.
func ExecuteDroid(projectPath string, customCommand string, resumeSessionID string) error {
	cmd, args := parseDroidCommand(customCommand)
	args = ensureResumeArgs(args, resumeSessionID)
	if resumeSessionID != "" {
		slog.Info("ExecuteDroid: resuming session", "sessionId", resumeSessionID)
	}

	command := exec.Command(cmd, args...)
	spi.RunInProject(command, projectPath)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	err := command.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &spi.AgentExitError{Agent: "Factory Droid", Code: exitErr.ExitCode()}
	}
	return err
}

func ensureResumeArgs(args []string, resumeSessionID string) []string {
	return spi.EnsureResumeFlagArgs(args, resumeSessionID, "--resume", "-r")
}
