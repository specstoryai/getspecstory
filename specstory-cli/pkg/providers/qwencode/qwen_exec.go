package qwencode

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// parseQwenCommand parses a custom command string into executable and arguments.
func parseQwenCommand(customCommand string) (string, []string) {
	if customCommand != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			return parts[0], parts[1:]
		}
	}
	return "qwen", nil
}

// ensureResumeArgs gives the requested id precedence over configured resume
// flags, repairs bare flags, and never changes the caller's backing array.
func ensureResumeArgs(args []string, resumeSessionID string) []string {
	return spi.EnsureResumeFlagArgs(args, resumeSessionID, "--resume", "-r")
}

// ExecuteQwen runs the Qwen Code CLI with the given arguments
func ExecuteQwen(projectPath string, customCommand string, resumeSessionID string) error {
	// Parse the command and any custom arguments
	qwenCmd, customArgs := parseQwenCommand(customCommand)

	customArgs = ensureResumeArgs(customArgs, resumeSessionID)
	if resumeSessionID != "" {
		slog.Info("ExecuteQwen: Passing resume argument", "sessionId", resumeSessionID)
	}

	// Create the command
	cmd := exec.Command(qwenCmd, customArgs...)
	cmd.Dir = projectPath
	// Relative storage paths must keep the same meaning for discovery and the
	// child even when --project-path launches Qwen in a different directory.
	// os/exec keeps the last occurrence of a duplicated key, so appending the
	// resolved value overrides the inherited one without rebuilding the slice.
	cmd.Env = cmd.Environ()
	for _, key := range []string{"QWEN_HOME", "QWEN_RUNTIME_DIR"} {
		if value := os.Getenv(key); value != "" {
			resolved, err := resolveQwenStoragePath(value)
			if err != nil {
				return err
			}
			cmd.Env = append(cmd.Env, key+"="+resolved)
		}
	}

	// Set up stdin/stdout/stderr to match the parent process
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the command
	slog.Info("ExecuteQwen: Starting Qwen Code process")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start qwen: %w", err)
	}

	// Wait for the command to complete
	slog.Info("ExecuteQwen: Waiting for Qwen Code to exit")
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode := exitErr.ExitCode()
			slog.Info("ExecuteQwen: Qwen Code exited", "exitCode", exitCode)
			return &spi.AgentExitError{Agent: "Qwen Code", Code: exitCode}
		}
		return fmt.Errorf("qwen execution failed: %w", err)
	}

	slog.Info("ExecuteQwen: Qwen Code exited normally", "exitCode", 0)
	return nil
}
