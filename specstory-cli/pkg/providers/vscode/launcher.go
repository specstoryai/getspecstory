package vscode

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// ErrCLIMissing signals that the IDE's shell command is not on PATH. On macOS
// the command is opt-in (installed from the app's command palette), so its
// absence is an expected condition, not a failure — callers use this to print
// installation guidance instead of a generic error.
var ErrCLIMissing = errors.New("the IDE's shell command is not installed")

// OpenApp launches the IDE at the given project path via its CLI launcher —
// the only launcher that reliably opens the directory as a workspace window
// (`open -a` on macOS mostly just activates an already-running instance on its
// home screen, so it is deliberately not used as a fallback). A custom command
// overrides the launcher binary and prepends any extra arguments before the
// project path.
//
// The path is canonicalized before handing it to the IDE: the IDE derives the
// workspace identity from the path string it is given, so launching with the
// user's typed spelling (~/source vs ~/Source on a case-insensitive
// filesystem) would mint a second workspace entry for the same folder,
// splitting its sessions across entries.
//
// The launcher is started without waiting for it to exit: the stock CLI forks
// and returns immediately, but a custom command can keep running for the whole
// IDE session (e.g. `code --wait`), and blocking would stall the caller's
// watcher before it ever starts. Post-spawn failures are logged from the
// reaper goroutine instead of returned.
//
// When the default CLI isn't on PATH, ErrCLIMissing is returned so the caller
// can tell the user how to install it; a missing custom launcher returns a
// plain error, since the install guidance only applies to the IDE's own
// command.
func OpenApp(appName, launcher, customCommand, projectPath string) error {
	var args []string
	if customCommand != "" {
		if parts := spi.SplitCommandLine(customCommand); len(parts) > 0 {
			launcher = parts[0]
			args = parts[1:]
		}
	}

	if _, err := exec.LookPath(launcher); err != nil {
		if customCommand == "" {
			return ErrCLIMissing
		}
		return fmt.Errorf("configured %s launcher %q not found on PATH: %w", appName, launcher, err)
	}

	if canonical, err := spi.GetCanonicalPath(projectPath); err == nil {
		projectPath = canonical
	}
	args = append(args, projectPath)

	cmd := exec.Command(launcher, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s launcher %q failed to start: %w", appName, launcher, err)
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			slog.Warn("IDE launcher exited with error",
				"app", appName, "launcher", launcher,
				"error", err, "output", strings.TrimSpace(out.String()))
		}
	}()
	return nil
}
