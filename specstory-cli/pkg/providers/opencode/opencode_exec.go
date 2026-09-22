package opencode

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// defaultOpenCodeCommand is used when no custom command is configured.
const defaultOpenCodeCommand = "opencode"

// sessionFlag and sessionFlagShort are OpenCode's flags for continuing a
// session by id (`opencode --session <id>`, `opencode -s <id>`).
const (
	sessionFlag      = "--session"
	sessionFlagShort = "-s"
)

// importStagingDirName is the directory under the system temp dir where a
// reconstructed session waits to be imported. OpenCode reads sessions only
// from its database, through its own service, so a reconstructed session is
// handed over as an export file and loaded with `opencode session import`.
const importStagingDirName = "specstory-opencode-import"

// parseOpenCodeCommand splits a custom command into executable and arguments.
func parseOpenCodeCommand(customCommand string) (string, []string) {
	if customCommand != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			return parts[0], parts[1:]
		}
	}
	return defaultOpenCodeCommand, nil
}

// stagedImportPath returns where a reconstructed session named filename is
// staged for import.
func stagedImportPath(filename string) string {
	return filepath.Join(os.TempDir(), importStagingDirName, filename)
}

// stagedImportFilename is the staging filename for a reconstructed session.
func stagedImportFilename(sessionID string) string {
	return sessionID + ".json"
}

// importStagedSession loads a reconstructed session into OpenCode when one is
// staged for sessionID, then removes the staged file. Sessions that were not
// reconstructed have nothing staged and are left alone.
//
// Only the executable of a custom command is used: its arguments configure an
// interactive launch and do not apply to the import subcommand.
func importStagedSession(customCommand, projectPath, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	stagedPath := stagedImportPath(stagedImportFilename(sessionID))
	if _, err := os.Stat(stagedPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to read staged OpenCode session %s: %w", stagedPath, err)
	}

	command, _ := parseOpenCodeCommand(customCommand)
	args := []string{"session", "import", "--directory", projectPath, stagedPath}
	slog.Info("importStagedSession: Importing reconstructed session into OpenCode",
		"command", command, "args", args, "sessionId", sessionID)

	cmd := exec.Command(command, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		slog.Error("importStagedSession: OpenCode import failed",
			"sessionId", sessionID, "error", err, "output", strings.TrimSpace(output.String()))
		return fmt.Errorf("failed to import the reconstructed session into OpenCode (%s %s): %w\n%s",
			command, strings.Join(args, " "), err, strings.TrimSpace(output.String()))
	}
	slog.Info("importStagedSession: Imported reconstructed session", "sessionId", sessionID,
		"output", strings.TrimSpace(output.String()))

	if err := os.Remove(stagedPath); err != nil {
		// The import succeeded. A leftover file is harmless: importing an id
		// that already exists is a no-op ("Session already exists", exit 0,
		// observed with 2.0.14).
		slog.Warn("importStagedSession: Failed to remove staged session", "path", stagedPath, "error", err)
	}
	return nil
}

// executeOpenCode runs OpenCode attached to the terminal, continuing
// resumeSessionID when one is given, and returns its exit status as an
// spi.AgentExitError so the caller can finish saving before exiting.
func executeOpenCode(customCommand, projectPath, resumeSessionID string) error {
	command, args := parseOpenCodeCommand(customCommand)
	// A requested session wins over one pinned in the configured command.
	args = spi.EnsureResumeFlagArgs(args, resumeSessionID, sessionFlag, sessionFlagShort)

	cmd := exec.Command(command, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	slog.Info("executeOpenCode: Starting OpenCode process",
		"command", command, "args", args, "projectPath", projectPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start opencode: %w", err)
	}

	slog.Info("executeOpenCode: Waiting for OpenCode to exit")
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode := exitErr.ExitCode()
			slog.Info("executeOpenCode: OpenCode exited", "exitCode", exitCode)
			return &spi.AgentExitError{Agent: providerName, Code: exitCode}
		}
		return fmt.Errorf("opencode execution failed: %w", err)
	}

	slog.Info("executeOpenCode: OpenCode exited normally", "exitCode", 0)
	return nil
}
