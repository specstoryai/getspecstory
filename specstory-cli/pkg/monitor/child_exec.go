package monitor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// osExecutable is swappable in tests so SpawnWatch can launch a stub binary
// instead of recursively spawning real specstory processes. Mirrors the
// package-level mocking pattern used by pkg/providers/codexcli.
var osExecutable = os.Executable

// SpawnWatch starts a `specstory watch` child for projectPath and returns the
// started command (cmd.Start convention, matching the provider *_exec.go
// files — the caller reaps it with Wait). The child gets:
//
//   - cmd.Dir = projectPath, which is critical: watch scopes everything (its
//     provider watchers, project identity, history) to its own cwd.
//   - --output-dir inside the project so markdown lands in that repo's
//     .specstory/history, and NOTHING else — the child follows the user's own
//     config for cloud sync, redaction, logging, etc.
//   - stdout/stderr discarded (nil → /dev/null): children have their own
//     --log config; they must not spam the monitor's console.
func SpawnWatch(projectPath string) (*exec.Cmd, error) {
	exe, err := osExecutable()
	if err != nil {
		return nil, fmt.Errorf("failed to locate specstory executable: %w", err)
	}

	cmd := exec.Command(exe, "watch", "--output-dir", filepath.Join(projectPath, ".specstory", "history"))
	cmd.Dir = projectPath

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start watch child for %s: %w", projectPath, err)
	}
	return cmd, nil
}
