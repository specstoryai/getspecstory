package claudecode

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"log/slog"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Package-level variables for mocking in tests
var (
	osUserHomeDir = os.UserHomeDir
	osStat        = os.Stat
	execLookPath  = exec.LookPath
)

// getDefaultClaudeCommand returns the default claude command.
// It checks for installations in this order:
// 1. ~/.local/bin/claude (preferred native installation)
// 2. "claude" resolved from PATH
// 3. ~/.claude/local/claude (legacy npm installation)
// 4. "claude" bare fallback
func getDefaultClaudeCommand() string {
	homeDir, homeDirErr := osUserHomeDir()
	if homeDirErr == nil {
		// Preferred native installation
		nativeClaude := filepath.Join(homeDir, ".local", "bin", "claude")
		if _, err := osStat(nativeClaude); err == nil {
			slog.Info("Found Claude Code", "path", nativeClaude)
			return nativeClaude
		}
	}

	// Check if claude is available on PATH
	if path, err := execLookPath("claude"); err == nil {
		slog.Info("Found Claude Code on PATH", "path", path)
		return "claude"
	}

	if homeDirErr == nil {
		// Legacy npm installation
		legacyClaude := filepath.Join(homeDir, ".claude", "local", "claude")
		if _, err := osStat(legacyClaude); err == nil {
			slog.Info("Found Claude Code", "path", legacyClaude)
			return legacyClaude
		}
	}

	slog.Info("Using Claude Code bare fallback")
	return "claude"
}

// expandTilde expands ~ to the user's home directory
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(homeDir, path[2:])
		}
	}
	return path
}

// parseClaudeCommand parses a custom command string into executable and arguments.
// If customCommand is empty, returns the default command.
// If resumeSessionId is provided, appends "--resume <sessionId>" to the arguments.
// Supports quoted strings with spaces: claude --arg "value with spaces"
// Example: "claude --model gpt-4" returns ("claude", ["--model", "gpt-4"])
func parseClaudeCommand(customCommand string, resumeSessionId string) (string, []string) {
	var cmd string
	var args []string

	if customCommand == "" {
		cmd = getDefaultClaudeCommand()
		args = nil
	} else {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) == 0 {
			cmd = getDefaultClaudeCommand()
			args = nil
		} else {
			// Expand tilde in the command path
			cmd = expandTilde(parts[0])
			args = parts[1:]
		}
	}

	// Append --resume flag if sessionId is provided
	if resumeSessionId != "" {
		args = append(args, "--resume", resumeSessionId)
	}

	return cmd, args
}

// ExecuteClaude runs the Claude CLI with the given arguments
func ExecuteClaude(customCommand string, resumeSessionId string) error {
	// Parse the command and any custom arguments
	claudeCmd, customArgs := parseClaudeCommand(customCommand, resumeSessionId)

	// Log if resuming a session
	if resumeSessionId != "" {
		slog.Info("ExecuteClaude: Resuming session", "sessionId", resumeSessionId)
	}

	// Create the command
	cmd := exec.Command(claudeCmd, customArgs...)

	// Set up stdin/stdout/stderr to match the parent process
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the command
	slog.Info("ExecuteClaude: Starting Claude Code process")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start claude: %v", err)
	}

	// Wait for the command to complete
	slog.Info("ExecuteClaude: Waiting for Claude Code to exit")
	if err := cmd.Wait(); err != nil {
		// Don't return error if the command exited with a non-zero status
		// This is normal for many CLI applications
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			slog.Info("ExecuteClaude: Claude Code exited", "exitCode", exitCode)
			os.Exit(exitCode)
		}
		return fmt.Errorf("claude execution failed: %v", err)
	}

	slog.Info("ExecuteClaude: Claude Code exited normally", "exitCode", 0)
	return nil
}
