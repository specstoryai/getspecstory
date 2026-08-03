package qwencode

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestEncodeProjectDirName(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/Users/jy/app", "-Users-jy-app"},
		{"/Users/jy/projects/general", "-Users-jy-projects-general"},
		{"/tmp/my.project_v2", "-tmp-my-project-v2"},
		{"/", "-"},
	}

	for _, tc := range cases {
		if got := encodeProjectDirName(tc.path); got != tc.want {
			t.Errorf("encodeProjectDirName(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestGetQwenProjectsDir(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// Missing projects directory -> QwenPathError
	_, err := GetQwenProjectsDir()
	if err == nil {
		t.Fatal("GetQwenProjectsDir should fail when ~/.qwen/projects is missing")
	}
	var pathErr *QwenPathError
	if !errors.As(err, &pathErr) || pathErr.Kind != "projects_missing" {
		t.Errorf("error = %v, want QwenPathError{Kind: projects_missing}", err)
	}

	// Create it -> resolves
	projectsDir := filepath.Join(fakeHome, ".qwen", "projects")
	if err := mkdirAllForTest(t, projectsDir); err != nil {
		t.Fatalf("failed to create projects dir: %v", err)
	}
	got, err := GetQwenProjectsDir()
	if err != nil {
		t.Fatalf("GetQwenProjectsDir returned error: %v", err)
	}
	if got != projectsDir {
		t.Errorf("GetQwenProjectsDir = %q, want %q", got, projectsDir)
	}
}

func TestResolveQwenProjectDir(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// No ~/.qwen at all -> qwen_dir_missing
	_, err := ResolveQwenProjectDir("/tmp/some-project")
	var pathErr *QwenPathError
	if !errors.As(err, &pathErr) || pathErr.Kind != "qwen_dir_missing" {
		t.Fatalf("error = %v, want QwenPathError{Kind: qwen_dir_missing}", err)
	}

	// ~/.qwen exists but the project was never used -> project_missing
	if err := mkdirAllForTest(t, filepath.Join(fakeHome, ".qwen", "projects")); err != nil {
		t.Fatalf("failed to create projects dir: %v", err)
	}
	_, err = ResolveQwenProjectDir("/tmp/some-project")
	if !errors.As(err, &pathErr) || pathErr.Kind != "project_missing" {
		t.Fatalf("error = %v, want QwenPathError{Kind: project_missing}", err)
	}

	// Create the encoded project directory -> resolves
	projectDir := filepath.Join(fakeHome, ".qwen", "projects", "-tmp-some-project")
	if err := mkdirAllForTest(t, projectDir); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}
	got, err := ResolveQwenProjectDir("/tmp/some-project")
	if err != nil {
		t.Fatalf("ResolveQwenProjectDir returned error: %v", err)
	}
	if got != projectDir {
		t.Errorf("ResolveQwenProjectDir = %q, want %q", got, projectDir)
	}
}

func TestResolveQwenProjectDirUsesCwdWhenEmpty(t *testing.T) {
	originalUserHome := osUserHomeDir
	originalGetwd := osGetwd
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
		osGetwd = originalGetwd
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}
	osGetwd = func() (string, error) {
		return "/tmp/cwd-project", nil
	}

	projectDir := filepath.Join(fakeHome, ".qwen", "projects", "-tmp-cwd-project")
	if err := mkdirAllForTest(t, projectDir); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	got, err := ResolveQwenProjectDir("")
	if err != nil {
		t.Fatalf("ResolveQwenProjectDir returned error: %v", err)
	}
	if got != projectDir {
		t.Errorf("ResolveQwenProjectDir = %q, want %q", got, projectDir)
	}
}

func TestListQwenProjectDirs(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// No projects directory -> empty result, no error
	dirs, err := ListQwenProjectDirs()
	if err != nil {
		t.Fatalf("ListQwenProjectDirs returned error: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("dir count = %d, want 0", len(dirs))
	}

	if err := mkdirAllForTest(t, filepath.Join(fakeHome, ".qwen", "projects", "-tmp-a")); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}
	if err := mkdirAllForTest(t, filepath.Join(fakeHome, ".qwen", "projects", "-tmp-b")); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	dirs, err = ListQwenProjectDirs()
	if err != nil {
		t.Fatalf("ListQwenProjectDirs returned error: %v", err)
	}
	if len(dirs) != 2 {
		t.Errorf("dir count = %d, want 2", len(dirs))
	}
}
