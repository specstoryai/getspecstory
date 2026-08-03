package qwencode

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"log/slog"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Package-level variables for mocking in tests
var (
	execLookPath = exec.LookPath
)

// getDefaultQwenCommand returns the default qwen command.
// It checks for qwen in PATH.
func getDefaultQwenCommand() string {
	if _, err := execLookPath("qwen"); err == nil {
		slog.Info("Found Qwen Code in PATH")
		return "qwen"
	}
	slog.Info("Qwen Code not found in PATH, defaulting to 'qwen'")
	return "qwen"
}

// parseQwenCommand parses a custom command string into executable and arguments.
func parseQwenCommand(customCommand string) (string, []string) {
	if customCommand != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			return parts[0], parts[1:]
		}
	}
	return getDefaultQwenCommand(), nil
}

func ensureResumeArgs(args []string, resumeSessionID string) []string {
	if resumeSessionID == "" {
		return args
	}

	for i, arg := range args {
		if arg == "--resume" || arg == "-r" {
			if i+1 < len(args) {
				return args
			}
			// Trailing flag without a value: supply the session id directly
			return append(args, resumeSessionID)
		}
		if strings.HasPrefix(arg, "--resume=") {
			return args
		}
	}

	return append(args, "--resume", resumeSessionID)
}

// ExecuteQwen runs the Qwen Code CLI with the given arguments
func ExecuteQwen(customCommand string, resumeSessionID string) error {
	// Parse the command and any custom arguments
	qwenCmd, customArgs := parseQwenCommand(customCommand)

	customArgs = ensureResumeArgs(customArgs, resumeSessionID)
	if resumeSessionID != "" {
		slog.Info("ExecuteQwen: Passing resume argument", "sessionId", resumeSessionID)
	}

	// Create the command
	cmd := exec.Command(qwenCmd, customArgs...)

	// Set up stdin/stdout/stderr to match the parent process
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the command
	slog.Info("ExecuteQwen: Starting Qwen Code process")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start qwen: %v", err)
	}

	// Wait for the command to complete
	slog.Info("ExecuteQwen: Waiting for Qwen Code to exit")
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			slog.Info("ExecuteQwen: Qwen Code exited", "exitCode", exitCode)
			os.Exit(exitCode)
		}
		return fmt.Errorf("qwen execution failed: %v", err)
	}

	slog.Info("ExecuteQwen: Qwen Code exited normally", "exitCode", 0)
	return nil
}
