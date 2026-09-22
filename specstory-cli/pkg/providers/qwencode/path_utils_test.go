package qwencode

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
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
		{name: "astral uses two replacements", path: "/tmp/😀", want: "-tmp---"},
		{name: "Windows", path: `C:\Users\Dev\project`, want: "C--Users-Dev-project"},
		{name: "unicode replaced", path: "/tmp/héllo", want: "-tmp-h-llo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				tt.want = strings.ToLower(tt.want)
			}
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
	t.Setenv("QWEN_HOME", "")
	t.Setenv("QWEN_RUNTIME_DIR", "")
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

func TestStorageOverrides(t *testing.T) {
	home := withFakeHome(t)
	for _, tt := range []struct{ name, qwenHome, runtimeDir, want string }{
		{"default", "", "", filepath.Join(home, ".qwen", "projects")},
		{"home", filepath.Join(home, "custom"), "", filepath.Join(home, "custom", "projects")},
		{"runtime wins", filepath.Join(home, "custom"), filepath.Join(home, "runtime"), filepath.Join(home, "runtime", "projects")},
		{"tilde", "~/qwen-data", "", filepath.Join(home, "qwen-data", "projects")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("QWEN_HOME", tt.qwenHome)
			t.Setenv("QWEN_RUNTIME_DIR", tt.runtimeDir)
			got, err := GetQwenProjectsDir()
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestNativeSessionPathCanonicalProject(t *testing.T) {
	home := withFakeHome(t)
	root := t.TempDir()
	project := filepath.Join(root, "project space_😀")
	if err := os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(project, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks require Windows developer mode")
		}
		t.Fatal(err)
	}
	p := NewProvider()
	realPath, err := p.NativeSessionPath(project, "s.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	linkPath, err := p.NativeSessionPath(link, "s.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".qwen", "projects", SanitizeQwenCwd(spi.CanonicalizePathOrClean(project)), "chats", "s.jsonl")
	if realPath != want || linkPath != want {
		t.Fatalf("real=%q link=%q want=%q", realPath, linkPath, want)
	}
	if _, err := os.Stat(filepath.Join(home, ".qwen")); !os.IsNotExist(err) {
		t.Fatal("resolver wrote to store")
	}
}
