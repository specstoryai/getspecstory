package cursorcli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Package-level variables for mocking in tests
var (
	osUserHomeDir = os.UserHomeDir
	osStat        = os.Stat
	execLookPath  = exec.LookPath
)

// getDefaultCursorCommand returns the default cursor-agent command.
// It checks for cursor-agent in common locations in this order:
// 1. cursor-agent in PATH
// 2. ~/.cursor/bin/cursor-agent
// 3. ~/.local/bin/cursor-agent
// This allows flexibility in installation locations.
func getDefaultCursorCommand() string {
	// Check if cursor-agent is in PATH first
	if _, err := execLookPath("cursor-agent"); err == nil {
		slog.Info("Found cursor-agent in PATH")
		return "cursor-agent"
	}

	// Check common installation locations
	homeDir, err := osUserHomeDir()
	if err == nil {
		// Check ~/.cursor/bin/cursor-agent
		cursorInstallPath := filepath.Join(homeDir, ".cursor", "bin", "cursor-agent")
		if _, err := osStat(cursorInstallPath); err == nil {
			slog.Info("Found cursor-agent", "path", cursorInstallPath)
			return cursorInstallPath
		}

		// Check ~/.local/bin/cursor-agent
		userLocalPath := filepath.Join(homeDir, ".local", "bin", "cursor-agent")
		if _, err := osStat(userLocalPath); err == nil {
			slog.Info("Found cursor-agent", "path", userLocalPath)
			return userLocalPath
		}
	}

	// Default to cursor-agent and hope it's in PATH
	slog.Info("Using cursor-agent from PATH (may not exist)")
	return "cursor-agent"
}

// expandTilde expands ~ to the user's home directory
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		homeDir, err := osUserHomeDir()
		if err == nil {
			return filepath.Join(homeDir, path[2:])
		}
	}
	return path
}

// parseCursorCommand parses a custom command string into executable and arguments.
// If customCommand is empty, returns the default command.
// Supports quoted strings with spaces: cursor-agent --arg "value with spaces"
// Example: "cursor-agent --model gpt-4" returns ("cursor-agent", ["--model", "gpt-4"])
func parseCursorCommand(customCommand string) (string, []string) {
	if customCommand != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			// Expand tilde in the command path
			return expandTilde(parts[0]), parts[1:]
		}
	}
	return getDefaultCursorCommand(), nil
}

// ExecuteCursorCLI runs the Cursor CLI with the given arguments and optional resume session
func ExecuteCursorCLI(customCommand string, resumeSessionId string) error {
	slog.Info("Preparing to execute Cursor CLI", "command", customCommand, "resumeSessionId", resumeSessionId)

	// Parse the command and any custom arguments
	cursorCmd, customArgs := parseCursorCommand(customCommand)

	// The requested session overrides any resume id in the configured command.
	if resumeSessionId != "" {
		customArgs = spi.EnsureResumeFlagArgs(customArgs, resumeSessionId, "--resume")
		slog.Info("ExecuteCursorCLI: Resuming session", "sessionId", resumeSessionId)
	}

	// Create the command
	cmd := exec.Command(cursorCmd, customArgs...)

	// Set up stdin/stdout/stderr to match the parent process
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Ensure cursor-agent runs with proper terminal settings
	cmd.Env = append(os.Environ(), "FORCE_COLOR=1")

	// Start the command
	slog.Info("ExecuteCursorCLI: Starting Cursor CLI process", "command", cursorCmd, "args", customArgs)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start cursor-agent: %w", err)
	}

	slog.Info("ExecuteCursorCLI: Cursor CLI started", "pid", cmd.Process.Pid)

	// Set up signal handling to properly terminate the child process
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	// Wait for the command to finish or for a signal
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var err error
	select {
	case err = <-done:
	case sig := <-sigChan:
		slog.Info("ExecuteCursorCLI: Received signal, terminating Cursor CLI", "signal", sig)
		if signalErr := cmd.Process.Signal(os.Interrupt); signalErr != nil {
			slog.Debug("ExecuteCursorCLI: Could not interrupt child", "error", signalErr)
			if killErr := cmd.Process.Kill(); killErr != nil {
				slog.Debug("ExecuteCursorCLI: Could not kill child", "error", killErr)
			}
		}
		// Join the child before stopping its watcher: it may still be writing
		// while handling the interrupt. Bound the grace period, then reap it.
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case err = <-done:
		case <-timer.C:
			if killErr := cmd.Process.Kill(); killErr != nil {
				slog.Debug("ExecuteCursorCLI: Could not kill child", "error", killErr)
			}
			err = <-done
		}
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &spi.AgentExitError{Agent: "Cursor CLI", Code: exitErr.ExitCode()}
		}
		return fmt.Errorf("cursor-agent execution failed: %w", err)
	}
	slog.Info("ExecuteCursorCLI: Cursor CLI exited normally", "exitCode", 0)
	return nil
}
