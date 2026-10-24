package monitor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// buildStubChild compiles a program that exits immediately, standing in for
// the specstory binary so no real watch child is spawned. A compiled stub,
// rather than a system binary such as /bin/echo, exists on every OS CI runs.
func buildStubChild(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "stub")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, source).CombinedOutput()
	if err != nil {
		t.Fatalf("build stub child: %v\n%s", err, output)
	}
	return binary
}

func TestSpawnWatch(t *testing.T) {
	tests := []struct {
		name string
		// exe/exeErr are what the injected os.Executable stub returns. The
		// happy path uses a compiled no-op stub (stub: true) so no real
		// specstory child is spawned.
		exe     string
		stub    bool
		exeErr  error
		wantErr bool
	}{
		{
			name: "builds expected argv and dir",
			stub: true,
		},
		{
			name:    "executable lookup failure",
			exeErr:  errors.New("no executable"),
			wantErr: true,
		},
		{
			name:    "start failure for missing binary",
			exe:     "/nonexistent/specstory-binary",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exe := tt.exe
			if tt.stub {
				exe = buildStubChild(t)
			}
			orig := osExecutable
			defer func() { osExecutable = orig }()
			osExecutable = func() (string, error) { return exe, tt.exeErr }

			projectPath := t.TempDir()
			cmd, err := SpawnWatch(projectPath)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SpawnWatch() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			wantArgs := []string{
				exe,
				"watch",
				"--output-dir", filepath.Join(projectPath, ".specstory", "history"),
			}
			if !reflect.DeepEqual(cmd.Args, wantArgs) {
				t.Errorf("SpawnWatch() args = %v, want %v", cmd.Args, wantArgs)
			}
			if cmd.Dir != projectPath {
				t.Errorf("SpawnWatch() Dir = %q, want %q", cmd.Dir, projectPath)
			}
			if cmd.Process == nil {
				t.Error("SpawnWatch() did not start the process (Process is nil)")
			}
			// Reap the stub so the test leaves no zombie behind.
			if err := cmd.Wait(); err != nil {
				t.Errorf("stub child exited with error: %v", err)
			}
		})
	}
}
