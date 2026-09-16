package piagent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"log/slog"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// resumeFlag is how pi continues an existing session on the command line:
// `pi --session-id <id>`. The shared flag helper replaces a configured id
// with the session requested for this run.
//
// Verified empirically against pi 0.85.1, the current npm package
// @earendil-works/pi-coding-agent, on 2026-09-05 (the older
// @mariozechner/pi-coding-agent package stopped at 0.73.1 and had no
// `--session-id` flag): `--session-id <id>` matches the exact project session
// id (the header `id` our parser reads and our serializer writes), reopens the
// SAME conversation, and appends to the same session file.
// The alternative `--session <path|id>` was rejected as the resume flag because
// it is selection/partial-UUID driven and does not create-if-missing; a caller
// resuming a specific id wants an exact match. `--session-id` additionally
// creates the session if missing, which is harmless for our flow (the
// reconstructed file is written to NativeSessionPath before pi launches, so the
// exact id already exists on disk).
const resumeFlag = "--session-id"

// parsePiRunCommand splits a custom run command into the pi binary and its base
// args (reusing parsePiCommand for the SplitCommandLine quoting + tilde
// expansion), then sets pi's resume flag when a session id is provided. An
// empty custom command falls back to the default `pi` binary. resumeSessionID is
// expected pre-trimmed by ExecAgentAndWatch; the guard here means a direct
// caller cannot append an empty `--session-id`.
func parsePiRunCommand(customCommand string, resumeSessionID string) (string, []string) {
	cmd, args := parsePiCommand(customCommand)
	if id := strings.TrimSpace(resumeSessionID); id != "" {
		args = spi.EnsureResumeFlagArgs(args, id, resumeFlag)
	}
	return cmd, args
}

// getDefaultPiCommand returns the default pi binary. Unlike Claude Code
// (~/.local/bin, npm) and Codex (Homebrew, npm), pi ships as a single `pi` on
// the PATH with no per-manager install locations worth probing, so a plain PATH
// lookup via exec.Command is the safe default and keeps the code DRY.
func getDefaultPiCommand() string {
	return defaultCmd
}

// ExecutePi runs pi with terminal passthrough and returns its exit status so
// the watcher can finish saving the last session update before the CLI exits.
func ExecutePi(customCommand string, resumeSessionID string) error {
	piCmd, args := parsePiRunCommand(customCommand, resumeSessionID)

	if strings.TrimSpace(resumeSessionID) != "" {
		slog.Info("ExecutePi: resuming session", "sessionId", resumeSessionID)
	}

	cmd := exec.Command(piCmd, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	slog.Info("ExecutePi: starting pi process", "command", piCmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start pi: %w", err)
	}

	slog.Info("ExecutePi: waiting for pi to exit")
	if err := cmd.Wait(); err != nil {
		// A non-zero exit is normal for an interactive CLI; hand pi's own exit
		// code up so the caller's shell sees it once the watcher has stopped.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode := exitErr.ExitCode()
			slog.Info("ExecutePi: pi exited", "exitCode", exitCode)
			return &spi.AgentExitError{Agent: providerName, Code: exitCode}
		}
		return fmt.Errorf("pi execution failed: %w", err)
	}

	slog.Info("ExecutePi: pi exited normally", "exitCode", 0)
	return nil
}
