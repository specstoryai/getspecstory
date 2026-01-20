package droidcli

import (
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

func ExecuteDroid(customCommand string, resumeSessionID string) error {
	cmd, args := parseDroidCommand(customCommand)
	if resumeSessionID != "" {
		slog.Info("ExecuteDroid: resume requested but not supported", "sessionId", resumeSessionID)
	}

	command := exec.Command(cmd, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	return command.Run()
}
