package grokbuild

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Package-level variable for mocking in tests
var execLookPath = exec.LookPath

// getDefaultGrokCommand returns the default grok command.
func getDefaultGrokCommand() string {
	if _, err := execLookPath("grok"); err == nil {
		slog.Info("Found Grok Build in PATH")
		return "grok"
	}
	slog.Info("Grok Build not found in PATH, defaulting to 'grok'")
	return "grok"
}

// parseGrokCommand splits a custom command string into an executable and arguments.
func parseGrokCommand(customCommand string) (string, []string) {
	if customCommand != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			return parts[0], parts[1:]
		}
	}
	return getDefaultGrokCommand(), nil
}

// ensureResumeArgs makes the selected session override a configured session.
func ensureResumeArgs(args []string, resumeSessionID string) []string {
	return spi.EnsureResumeFlagArgs(args, resumeSessionID, "--resume", "-r")
}

// ExecuteGrok runs the Grok Build CLI, optionally resuming a session.
func ExecuteGrok(projectPath string, customCommand string, resumeSessionID string) error {
	grokCmd, customArgs := parseGrokCommand(customCommand)

	customArgs = ensureResumeArgs(customArgs, resumeSessionID)
	if resumeSessionID != "" {
		slog.Info("ExecuteGrok: passing resume argument", "sessionId", resumeSessionID)
	}

	cmd := exec.Command(grokCmd, customArgs...)
	// Native discovery and the watcher must refer to the same project even
	// when the CLI was launched elsewhere with --project-path.
	cmd.Dir = projectPath
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	slog.Info("ExecuteGrok: starting Grok Build process")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start grok: %w", err)
	}

	slog.Info("ExecuteGrok: waiting for Grok Build to exit")
	if err := cmd.Wait(); err != nil {
		// Return the status so the caller can finish watcher and cloud cleanup
		// before the CLI exits with the agent's code.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &spi.AgentExitError{Agent: "Grok Build", Code: exitErr.ExitCode()}
		}
		return fmt.Errorf("grok execution failed: %w", err)
	}

	slog.Info("ExecuteGrok: Grok Build exited normally", "exitCode", 0)
	return nil
}
