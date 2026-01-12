package codexcli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

var errNoVersionOutput = errors.New("codex CLI version command produced no output")

// parseCodexCommand splits a custom command string into the binary path and its arguments.
// An empty custom command falls back to the detected default binary.
// Supports quoted strings with spaces: codex --arg "value with spaces"
func parseCodexCommand(customCommand string) (string, []string) {
	if customCommand != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			return expandTilde(parts[0]), parts[1:]
		}
	}

	return getDefaultCodexCommand(), nil
}

// getDefaultCodexCommand looks for the codex binary in common installation locations.
func getDefaultCodexCommand() string {
	if path, ok := findHomebrewCodex(); ok {
		return path
	}

	if path, ok := findNpmCodex(); ok {
		return path
	}

	return "codex"
}

// findHomebrewCodex returns the codex binary path from Homebrew if available.
func findHomebrewCodex() (string, bool) {
	// Respect HOMEBREW_PREFIX first if set.
	if prefix := strings.TrimSpace(os.Getenv("HOMEBREW_PREFIX")); prefix != "" {
		candidate := filepath.Join(prefix, "bin", "codex")
		if isExecutable(candidate) {
			slog.Debug("Codex CLI: Found Homebrew binary via HOMEBREW_PREFIX", "path", candidate)
			return candidate, true
		}
	}

	brewPath, err := execLookPath("brew")
	if err == nil {
		var out bytes.Buffer
		cmd := exec.Command(brewPath, "--prefix")
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err == nil {
			prefix := strings.TrimSpace(out.String())
			if prefix != "" {
				candidate := filepath.Join(prefix, "bin", "codex")
				if isExecutable(candidate) {
					slog.Debug("Codex CLI: Found Homebrew binary via brew --prefix", "path", candidate)
					return candidate, true
				}
			}
		}
	}

	// Fallback to the default Homebrew prefix on Apple Silicon if brew detection failed.
	defaultPath := "/opt/homebrew/bin/codex"
	if isExecutable(defaultPath) {
		slog.Debug("Codex CLI: Found Homebrew binary via default path", "path", defaultPath)
		return defaultPath, true
	}

	return "", false
}

// findNpmCodex returns the codex binary from global npm locations if present.
func findNpmCodex() (string, bool) {
	// Prefer explicit NVM_BIN if it points to the codex executable.
	if nvmBin := strings.TrimSpace(os.Getenv("NVM_BIN")); nvmBin != "" {
		candidate := filepath.Join(nvmBin, "codex")
		if isExecutable(candidate) {
			slog.Debug("Codex CLI: Found npm binary via NVM_BIN", "path", candidate)
			return candidate, true
		}
	}

	npmPath, err := execLookPath("npm")
	if err == nil {
		var out bytes.Buffer
		cmd := exec.Command(npmPath, "bin", "-g")
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err == nil {
			binDir := strings.TrimSpace(out.String())
			if binDir != "" {
				candidate := filepath.Join(binDir, "codex")
				if isExecutable(candidate) {
					slog.Debug("Codex CLI: Found npm binary via npm bin -g", "path", candidate)
					return candidate, true
				}
			}
		}
	}

	// Fallback to common NVM layout if we know NVM_DIR.
	if nvmDir := strings.TrimSpace(os.Getenv("NVM_DIR")); nvmDir != "" {
		candidate := filepath.Join(nvmDir, "versions", "node", "current", "bin", "codex")
		if isExecutable(candidate) {
			slog.Debug("Codex CLI: Found npm binary via NVM_DIR fallback", "path", candidate)
			return candidate, true
		}
	}

	return "", false
}

// expandTilde expands a leading tilde in a path to the user's home directory.
func expandTilde(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}

	home, err := osUserHomeDir()
	if err != nil {
		return path
	}

	if path == "~" {
		return home
	}

	// Handle both Unix-style (~/) and Windows-style (~\) paths even though we don't support Windows.
	// This check exists for defensive programming but won't be exercised in practice.
	if len(path) >= 2 && (path[1] == '/' || path[1] == '\\') {
		return filepath.Join(home, path[2:])
	}

	return path
}

// isExecutable returns true if the file exists and has execute permissions.
func isExecutable(path string) bool {
	info, err := osStat(path)
	if err != nil {
		return false
	}

	return !info.IsDir() && info.Mode()&0o111 != 0
}

// classifyCodexPath returns a string describing how the codex binary was resolved for analytics.
func classifyCodexPath(command string, resolvedPath string) string {
	if resolvedPath == "" {
		return "unknown"
	}

	// Check npm/nvm paths before homebrew to correctly identify node-based installations.
	if strings.Contains(resolvedPath, ".nvm") || strings.Contains(resolvedPath, "node") {
		return "npm_global"
	}

	if strings.Contains(resolvedPath, "/homebrew/") || strings.Contains(resolvedPath, "homebrew") {
		return "homebrew"
	}

	if filepath.IsAbs(command) || filepath.IsAbs(resolvedPath) {
		return "absolute"
	}

	// Binary was found in PATH but doesn't match other known patterns.
	return "path"
}

// runCodexVersionCommand tries common version flags and returns the first successful output.
func runCodexVersionCommand(command string) (string, string, string, error) {
	flags := []string{"--version", "-V"}
	var lastErr error
	var lastStderr string

	for idx, flag := range flags {
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		cmd := exec.Command(command, flag)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		stderrStr := strings.TrimSpace(stderr.String())
		if err != nil {
			lastErr = err
			lastStderr = stderrStr

			// For fatal errors (binary missing/permission issues) or last attempt, stop immediately.
			if classifyCheckError(err) != "unknown" || idx == len(flags)-1 {
				return "", flag, lastStderr, err
			}
			continue
		}

		output := strings.TrimSpace(stdout.String())
		if output == "" {
			return "", flag, stderrStr, errNoVersionOutput
		}

		return output, flag, stderrStr, nil
	}

	if lastErr != nil {
		return "", flags[len(flags)-1], lastStderr, lastErr
	}

	return "", "", lastStderr, errors.New("failed to execute codex version command")
}

// classifyCheckError buckets common error categories for user guidance and analytics.
func classifyCheckError(err error) string {
	if err == nil {
		return ""
	}

	var execErr *exec.Error
	if errors.As(err, &execErr) && execErr.Err == exec.ErrNotFound {
		return "not_found"
	}

	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		if errors.Is(pathErr.Err, os.ErrNotExist) {
			return "not_found"
		}
		if errors.Is(pathErr.Err, os.ErrPermission) {
			return "permission_denied"
		}
	}

	if errors.Is(err, os.ErrPermission) {
		return "permission_denied"
	}

	if errors.Is(err, errNoVersionOutput) {
		return "no_output"
	}

	return "unknown"
}

// ExecuteCodex executes the Codex CLI in interactive mode and blocks until it exits.
// The customCommand parameter specifies the codex binary and optional arguments to use.
// If customCommand is empty, falls back to the detected default codex binary.
// If resumeSessionID is provided, runs "codex resume <sessionId>" to continue that session.
// Otherwise, starts a new codex session.
func ExecuteCodex(customCommand string, resumeSessionID string) error {
	var cmd *exec.Cmd

	if resumeSessionID != "" {
		// Parse custom command to get binary and args
		codexCmd, customArgs := parseCodexCommand(customCommand)

		// Build args: custom args + resume subcommand + sessionID
		args := append(customArgs, "resume", resumeSessionID)

		slog.Info("ExecuteCodex: Resuming Codex session",
			"command", codexCmd,
			"sessionID", resumeSessionID,
			"customArgs", customArgs)
		cmd = exec.Command(codexCmd, args...)
	} else {
		// Parse the command and get binary + args
		codexCmd, args := parseCodexCommand(customCommand)

		slog.Info("ExecuteCodex: Starting new Codex session",
			"command", codexCmd,
			"args", args)
		cmd = exec.Command(codexCmd, args...)
	}

	// Configure interactive mode - connect to user's terminal
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run the command and wait for it to complete
	slog.Info("ExecuteCodex: Executing Codex CLI (blocking until exit)")
	if err := cmd.Run(); err != nil {
		slog.Error("ExecuteCodex: Codex execution failed", "error", err)
		return fmt.Errorf("codex execution failed: %w", err)
	}

	slog.Info("ExecuteCodex: Codex CLI exited successfully")
	return nil
}
