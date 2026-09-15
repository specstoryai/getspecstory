package updater

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// WorkerArgument is internal: it avoids initializing providers, project config,
// telemetry, and agent processes in the detached updater.
const WorkerArgument = "__specstory_update"

// Worker runs in a separate, bounded process so short-lived sync commands do
// not cancel a download, and downloads never delay the user's coding agent.
func Worker(version string) error {
	m, err := New(version)
	if err != nil || !m.Due(time.Now()) {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	_, err = m.Run(ctx, false, true)
	return err
}

// StartBackground performs only local checks in the caller. A live run/watch
// schedules another bounded worker after the cache interval, without restarting.
func StartBackground(ctx context.Context, version string, disabled bool) {
	startBackground(ctx, version, disabled, New, (*Manager).spawn)
}

func startBackground(ctx context.Context, version string, disabled bool, newManager func(string) (*Manager, error), spawn func(*Manager) error) {
	if disabled || os.Getenv("SPECSTORY_NO_AUTO_UPDATE") == "1" || os.Getenv("CI") != "" {
		return
	}
	if _, err := stableVersion(version); err != nil {
		return
	}
	m, err := newManager(version)
	if err != nil || m.Kind != "native" {
		return
	}
	start := func() {
		if ctx.Err() == nil && m.Due(time.Now()) {
			if err := spawn(m); err != nil {
				slog.Debug("Could not start background updater", "error", err)
			}
		}
	}
	start()
	go func() {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				start()
			}
		}
	}()
}

func (m *Manager) spawn() error {
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = null.Close() }()
	command := exec.Command(m.Executable, WorkerArgument)
	command.Stdin, command.Stdout, command.Stderr = null, null, null
	command.Dir = filepath.Dir(m.Executable)
	detach(command)
	if err := command.Start(); err != nil {
		return err
	}
	// Reap while the parent is alive; detachment lets the child survive its exit.
	go func() { _ = command.Wait() }()
	return nil
}

// PrintStatus adds read-only installation diagnostics to `specstory check`.
func PrintStatus(out io.Writer, version string, disabled bool) {
	m, err := New(version)
	if err != nil {
		_, _ = fmt.Fprintln(out, "Could not inspect SpecStory installation:", err)
		return
	}
	_, _ = fmt.Fprintf(out, "SpecStory CLI %s\n  Location: %s\n  Installation: %s\n", version, m.Executable, m.Kind)
	s := m.Status()
	_, versionErr := stableVersion(version)
	switch {
	case versionErr != nil:
		_, _ = fmt.Fprintln(out, "  Automatic updates: off (development or prerelease build)")
	case m.Kind == "homebrew":
		_, _ = fmt.Fprintln(out, "  Updates: brew upgrade specstoryai/tap/specstory")
	case m.Kind != "native":
		_, _ = fmt.Fprintln(out, "  Automatic updates: off (package manager or custom installation)")
	case disabled || os.Getenv("SPECSTORY_NO_AUTO_UPDATE") == "1" || os.Getenv("CI") != "" || s.Paused || s.Pending != nil:
		_, _ = fmt.Fprintln(out, "  Automatic updates: off; run specstory update to update manually")
	default:
		_, _ = fmt.Fprintln(out, "  Automatic updates: on; new versions apply on the next launch")
	}
	if !s.CheckedAt.IsZero() {
		_, _ = fmt.Fprintln(out, "  Last update check:", s.CheckedAt.Format(time.RFC3339))
	}
	if s.Installed != "" {
		_, _ = fmt.Fprintln(out, "  Last recorded version:", s.Installed)
	}
	if s.Error != "" {
		_, _ = fmt.Fprintln(out, "  Last update attempt:", s.Error)
	}
}
