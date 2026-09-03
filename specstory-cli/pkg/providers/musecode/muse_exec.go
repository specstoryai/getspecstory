package musecode

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// resumeSubcommand is how Muse Code continues a session: `muse resume <id>`,
// a subcommand rather than a flag (like Codex, unlike Qwen's --resume).
const resumeSubcommand = "resume"

// defaultMuseCommand is used when no custom command is configured. No PATH
// probing here: Check reports a missing binary with remediation steps, and
// exec surfaces the OS error directly, so a lookup would only duplicate both.
const defaultMuseCommand = "muse"

// parseMuseCommand parses a custom command string into executable and arguments.
func parseMuseCommand(customCommand string) (string, []string) {
	if customCommand != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			return parts[0], parts[1:]
		}
	}
	return defaultMuseCommand, nil
}

// ExecuteMuse runs the Muse Code CLI, optionally resuming an existing session.
func ExecuteMuse(customCommand string, resumeSessionID string) error {
	museCmd, args := parseMuseCommand(customCommand)

	args = spi.EnsureResumeArgs(args, resumeSubcommand, resumeSessionID)
	if resumeSessionID != "" {
		slog.Info("ExecuteMuse: Resuming Muse Code session", "sessionId", resumeSessionID, "args", args)
	}

	cmd := exec.Command(museCmd, args...)

	// Interactive mode: connect to the user's terminal
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	slog.Info("ExecuteMuse: Starting Muse Code process", "command", museCmd, "args", args)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start muse: %w", err)
	}

	slog.Info("ExecuteMuse: Waiting for Muse Code to exit")
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode := exitErr.ExitCode()
			slog.Info("ExecuteMuse: Muse Code exited", "exitCode", exitCode)
			os.Exit(exitCode)
		}
		return fmt.Errorf("muse execution failed: %w", err)
	}

	slog.Info("ExecuteMuse: Muse Code exited normally", "exitCode", 0)
	return nil
}
