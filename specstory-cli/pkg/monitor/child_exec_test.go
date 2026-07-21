package monitor

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSpawnWatch(t *testing.T) {
	tests := []struct {
		name string
		// exe/exeErr are what the injected os.Executable stub returns. The
		// happy path uses /bin/echo so no real specstory child is spawned.
		exe     string
		exeErr  error
		wantErr bool
	}{
		{
			name: "builds expected argv and dir",
			exe:  "/bin/echo",
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
			orig := osExecutable
			defer func() { osExecutable = orig }()
			osExecutable = func() (string, error) { return tt.exe, tt.exeErr }

			projectPath := t.TempDir()
			cmd, err := SpawnWatch(projectPath)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SpawnWatch() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			wantArgs := []string{
				tt.exe,
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
