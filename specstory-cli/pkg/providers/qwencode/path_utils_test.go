package qwencode

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeQwenCwd(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "simple path", path: "/Users/alice/app", want: "-Users-alice-app"},
		{name: "spaces and dots", path: "/Users/alice/my app.v2", want: "-Users-alice-my-app-v2"},
		{name: "underscores", path: "/home/dev/my_project", want: "-home-dev-my-project"},
		{name: "empty", path: "", want: ""},
		{name: "alphanumeric preserved", path: "abc123XYZ", want: "abc123XYZ"},
		{name: "unicode replaced", path: "/tmp/héllo", want: "-tmp-h-llo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeQwenCwd(tt.path); got != tt.want {
				t.Errorf("SanitizeQwenCwd(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// withFakeHome points the package's home dir lookup at a temp dir for the test.
func withFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	origHome := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = origHome })
	return home
}

func TestResolveQwenProjectDir_Found(t *testing.T) {
	home := withFakeHome(t)

	projectPath := t.TempDir()
	canonical, err := filepath.EvalSymlinks(projectPath)
	if err != nil {
		canonical = projectPath
	}

	projectDir := filepath.Join(home, ".qwen", "projects", SanitizeQwenCwd(canonical))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	dir, err := ResolveQwenProjectDir(projectPath)
	if err != nil {
		t.Fatalf("ResolveQwenProjectDir failed: %v", err)
	}
	if dir != projectDir {
		t.Errorf("resolved dir = %q, want %q", dir, projectDir)
	}
}

func TestResolveQwenProjectDir_ProjectsMissing(t *testing.T) {
	withFakeHome(t)

	_, err := ResolveQwenProjectDir("/some/project")
	var pathErr *QwenPathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected QwenPathError, got %v", err)
	}
	if pathErr.Kind != "projects_missing" {
		t.Errorf("error kind = %q, want projects_missing", pathErr.Kind)
	}
}

func TestResolveQwenProjectDir_ProjectMissing(t *testing.T) {
	home := withFakeHome(t)

	projectsDir := filepath.Join(home, ".qwen", "projects")
	if err := os.MkdirAll(filepath.Join(projectsDir, "-some-other-project"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveQwenProjectDir("/some/project")
	var pathErr *QwenPathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected QwenPathError, got %v", err)
	}
	if pathErr.Kind != "project_missing" {
		t.Errorf("error kind = %q, want project_missing", pathErr.Kind)
	}
	if len(pathErr.KnownDirs) != 1 || pathErr.KnownDirs[0] != "-some-other-project" {
		t.Errorf("known dirs = %v", pathErr.KnownDirs)
	}
}
