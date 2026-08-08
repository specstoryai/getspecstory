package musecode

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Package-level variable for mocking in tests
var execLookPath = exec.LookPath

// resumeSubcommand is how Muse Code continues a session: `muse resume <id>`,
// a subcommand rather than a flag (like Codex, unlike Qwen's --resume).
const resumeSubcommand = "resume"

// getDefaultMuseCommand returns the default muse command. It checks for muse in PATH.
func getDefaultMuseCommand() string {
	if _, err := execLookPath("muse"); err == nil {
		slog.Info("Found Muse Code in PATH")
		return "muse"
	}
	slog.Info("Muse Code not found in PATH, defaulting to 'muse'")
	return "muse"
}

// parseMuseCommand parses a custom command string into executable and arguments.
func parseMuseCommand(customCommand string) (string, []string) {
	if customCommand != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			return parts[0], parts[1:]
		}
	}
	return getDefaultMuseCommand(), nil
}

// ensureResumeArgs makes sure the command line carries `resume <id>`. A custom
// command that already names the resume subcommand keeps its position (the id
// is filled in after it when missing) rather than gaining a second one; any
// other custom subcommand is left alone and the resume pair is appended, which
// is the only thing that can be done without second-guessing the user.
func ensureResumeArgs(args []string, resumeSessionID string) []string {
	if resumeSessionID == "" {
		return args
	}

	for i, arg := range args {
		if arg != resumeSubcommand {
			continue
		}
		// An id already follows the subcommand: nothing to add.
		if i+1 < len(args) && strings.TrimSpace(args[i+1]) != "" && !strings.HasPrefix(args[i+1], "-") {
			return args
		}
		// slices.Concat always allocates a new backing array, so the caller's
		// slice is never mutated.
		return slices.Concat(args[:i+1], []string{resumeSessionID}, args[i+1:])
	}

	return slices.Concat(args, []string{resumeSubcommand, resumeSessionID})
}

// ExecuteMuse runs the Muse Code CLI, optionally resuming an existing session.
func ExecuteMuse(customCommand string, resumeSessionID string) error {
	museCmd, args := parseMuseCommand(customCommand)

	args = ensureResumeArgs(args, resumeSessionID)
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
