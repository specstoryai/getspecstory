package grokbuild

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

// ensureResumeArgs makes the command resume the requested session.
//
// A bare `--resume` or `-r` is meaningful to grok on its own: it opens the
// session picker. So a custom command can legitimately end with the flag and no
// value, and the fix is to fill in the missing id rather than append a second
// flag, which would leave grok reading "--resume" as the session id.
func ensureResumeArgs(args []string, resumeSessionID string) []string {
	if resumeSessionID == "" {
		return args
	}

	for i, arg := range args {
		if arg == "--resume" || arg == "-r" {
			// A following value that is not another flag means the caller already
			// chose a session.
			if i+1 < len(args) && strings.TrimSpace(args[i+1]) != "" && !strings.HasPrefix(args[i+1], "-") {
				return args
			}
			// slices.Concat always allocates a new backing array, so the caller's
			// slice is never mutated.
			return slices.Concat(args[:i+1], []string{resumeSessionID}, args[i+1:])
		}
		if strings.HasPrefix(arg, "--resume=") {
			if strings.TrimSpace(strings.TrimPrefix(arg, "--resume=")) != "" {
				return args
			}
			repaired := slices.Clone(args)
			repaired[i] = "--resume=" + resumeSessionID
			return repaired
		}
	}

	return append(args, "--resume", resumeSessionID)
}

// ExecuteGrok runs the Grok Build CLI, optionally resuming a session.
func ExecuteGrok(customCommand string, resumeSessionID string) error {
	grokCmd, customArgs := parseGrokCommand(customCommand)

	customArgs = ensureResumeArgs(customArgs, resumeSessionID)
	if resumeSessionID != "" {
		slog.Info("ExecuteGrok: passing resume argument", "sessionId", resumeSessionID)
	}

	cmd := exec.Command(grokCmd, customArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	slog.Info("ExecuteGrok: starting Grok Build process")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start grok: %w", err)
	}

	slog.Info("ExecuteGrok: waiting for Grok Build to exit")
	if err := cmd.Wait(); err != nil {
		// Mirror grok's own exit code rather than wrapping it, so scripts
		// wrapping `specstory run grok` see what they would without SpecStory.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode := exitErr.ExitCode()
			slog.Info("ExecuteGrok: Grok Build exited", "exitCode", exitCode)
			os.Exit(exitCode)
		}
		return fmt.Errorf("grok execution failed: %w", err)
	}

	slog.Info("ExecuteGrok: Grok Build exited normally", "exitCode", 0)
	return nil
}
