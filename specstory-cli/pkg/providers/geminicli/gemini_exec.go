package geminicli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"log/slog"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
)

// Package-level variables for mocking in tests
var (
	osUserHomeDir = os.UserHomeDir
	osStat        = os.Stat
	execLookPath  = exec.LookPath
)

// getDefaultGeminiCommand returns the default gemini command.
// It checks for gemini in PATH.
func getDefaultGeminiCommand() string {
	if _, err := execLookPath("gemini"); err == nil {
		slog.Info("Found Gemini CLI in PATH")
		return "gemini"
	}
	slog.Info("Gemini CLI not found in PATH, defaulting to 'gemini'")
	return "gemini"
}

// parseGeminiCommand parses a custom command string into executable and arguments.
func parseGeminiCommand(customCommand string) (string, []string) {
	if customCommand != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			return parts[0], parts[1:]
		}
	}
	return getDefaultGeminiCommand(), nil
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
		}
		if strings.HasPrefix(arg, "--resume=") {
			return args
		}
	}

	return append(args, "--resume", resumeSessionID)
}

// ExecuteGemini runs the Gemini CLI with the given arguments
func ExecuteGemini(customCommand string, resumeSessionID string) error {
	// Parse the command and any custom arguments
	geminiCmd, customArgs := parseGeminiCommand(customCommand)

	customArgs = ensureResumeArgs(customArgs, resumeSessionID)
	if resumeSessionID != "" {
		slog.Info("ExecuteGemini: Passing resume argument", "sessionId", resumeSessionID)
	}

	// Create the command
	cmd := exec.Command(geminiCmd, customArgs...)

	// Set up stdin/stdout/stderr to match the parent process
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the command
	slog.Info("ExecuteGemini: Starting Gemini CLI process")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start gemini: %v", err)
	}

	// Wait for the command to complete
	slog.Info("ExecuteGemini: Waiting for Gemini CLI to exit")
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			slog.Info("ExecuteGemini: Gemini CLI exited", "exitCode", exitCode)
			os.Exit(exitCode)
		}
		return fmt.Errorf("gemini execution failed: %v", err)
	}

	slog.Info("ExecuteGemini: Gemini CLI exited normally", "exitCode", 0)
	return nil
}
