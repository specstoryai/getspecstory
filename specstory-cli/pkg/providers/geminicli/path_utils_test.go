package geminicli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestHashProjectPathDeterministic(t *testing.T) {
	const projectPath = "/tmp/specstory"

	hash, err := HashProjectPath(projectPath)
	if err != nil {
		t.Fatalf("HashProjectPath returned error: %v", err)
	}

	// The pinned value guards cross-version stability: the hash must keep
	// matching what Gemini CLI itself computes for the same directory, or
	// existing sessions become unfindable. The fixture is a Unix path, which a
	// Windows host legitimately canonicalizes differently — and real inputs
	// there are native paths — so the pin only holds off Windows.
	if runtime.GOOS != "windows" {
		const expected = "a1b4e20c87f45c8f60096db37201de7ebdf549f1b6b33012051046d782f9b67e"
		if hash != expected {
			t.Fatalf("HashProjectPath = %s, want %s", hash, expected)
		}
		return
	}

	// On Windows assert the properties instead: shape, determinism, and
	// sensitivity to the input path.
	if len(hash) != 64 {
		t.Fatalf("HashProjectPath = %s, want 64-char sha256 hex", hash)
	}
	again, err := HashProjectPath(projectPath)
	if err != nil || again != hash {
		t.Fatalf("HashProjectPath not deterministic: %s vs %s (err %v)", hash, again, err)
	}
	other, err := HashProjectPath("/tmp/other")
	if err != nil || other == hash {
		t.Fatalf("HashProjectPath should differ for different paths (err %v)", err)
	}
}

func TestGetGeminiProjectDirWithDefaultPath(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
	})

	osGetwd = func() (string, error) {
		return "/tmp/specstory", nil
	}

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	dir, err := GetGeminiProjectDir("")
	if err != nil {
		t.Fatalf("GetGeminiProjectDir returned error: %v", err)
	}

	// Composition check: the dir must be {home}/.gemini/tmp/{hash of the cwd}.
	// The hash value itself is pinned by TestHashProjectPathDeterministic.
	hash, err := HashProjectPath("/tmp/specstory")
	if err != nil {
		t.Fatalf("HashProjectPath returned error: %v", err)
	}
	expected := filepath.Join(fakeHome, ".gemini", "tmp", hash)
	if dir != expected {
		t.Fatalf("GetGeminiProjectDir returned %q, want %q", dir, expected)
	}
}

func TestListGeminiProjectHashes(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	tmpDir := filepath.Join(fakeHome, ".gemini", "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("failed to create fake tmp dir: %v", err)
	}

	hashes := []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	for _, h := range hashes {
		path := filepath.Join(tmpDir, h)
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatalf("failed to create hash dir %q: %v", h, err)
		}
	}

	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	got, err := ListGeminiProjectHashes()
	if err != nil {
		t.Fatalf("ListGeminiProjectHashes returned error: %v", err)
	}

	if !reflect.DeepEqual(got, hashes) {
		t.Fatalf("ListGeminiProjectHashes = %v, want %v", got, hashes)
	}
}

func TestResolveGeminiProjectDirTmpMissing(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
	})

	osGetwd = func() (string, error) {
		return "/tmp/specstory", nil
	}
	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	_, err := ResolveGeminiProjectDir("")
	if err == nil {
		t.Fatalf("expected error but got nil")
	}

	var pathErr *GeminiPathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected GeminiPathError, got %T", err)
	}

	if pathErr.Kind != "tmp_missing" {
		t.Fatalf("Kind = %s, want tmp_missing", pathErr.Kind)
	}
}

func TestFindProjectDirByProjectRoot(t *testing.T) {
	tests := []struct {
		name        string
		projectPath string
		setup       func(t *testing.T, tmpDir string)
		wantMatch   bool   // true if we expect a non-empty result
		wantSuffix  string // expected directory name suffix in the result
	}{
		{
			name:        "match found via .project_root file",
			projectPath: "/tmp/my-project",
			setup: func(t *testing.T, tmpDir string) {
				dir := filepath.Join(tmpDir, "my-project")
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatalf("failed to create dir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".project_root"), []byte("/tmp/my-project\n"), 0o644); err != nil {
					t.Fatalf("failed to write .project_root: %v", err)
				}
			},
			wantMatch:  true,
			wantSuffix: "my-project",
		},
		{
			name:        "no match with different project path",
			projectPath: "/tmp/unrelated-project",
			setup: func(t *testing.T, tmpDir string) {
				dir := filepath.Join(tmpDir, "other-project")
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatalf("failed to create dir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".project_root"), []byte("/tmp/other-project\n"), 0o644); err != nil {
					t.Fatalf("failed to write .project_root: %v", err)
				}
			},
			wantMatch: false,
		},
		{
			name:        "empty tmp directory",
			projectPath: "/tmp/any-project",
			setup:       func(t *testing.T, tmpDir string) {},
			wantMatch:   false,
		},
		{
			name:        "directory without .project_root file skipped",
			projectPath: "/tmp/any-project",
			setup: func(t *testing.T, tmpDir string) {
				dir := filepath.Join(tmpDir, "abcdef1234567890")
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatalf("failed to create dir: %v", err)
				}
			},
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testTmpDir := t.TempDir()
			tt.setup(t, testTmpDir)

			got, err := findProjectDirByProjectRoot(testTmpDir, tt.projectPath)
			if err != nil {
				t.Fatalf("findProjectDirByProjectRoot returned error: %v", err)
			}

			if tt.wantMatch {
				wantDir := filepath.Join(testTmpDir, tt.wantSuffix)
				if got != wantDir {
					t.Fatalf("got %q, want %q", got, wantDir)
				}
			} else {
				if got != "" {
					t.Fatalf("expected empty string, got %q", got)
				}
			}
		})
	}
}

func TestResolveGeminiProjectDirViaProjectRoot(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	originalStat := osStat
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
		osStat = originalStat
	})

	// Use a real temp dir as the project path so canonical path resolution works
	projectPath := t.TempDir()

	osGetwd = func() (string, error) {
		return projectPath, nil
	}
	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	tmpDir := filepath.Join(fakeHome, ".gemini", "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}

	// Create a project-name-based directory with .project_root
	nameBasedDir := filepath.Join(tmpDir, "my-project")
	if err := os.Mkdir(nameBasedDir, 0o755); err != nil {
		t.Fatalf("failed to create name-based dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nameBasedDir, ".project_root"), []byte(projectPath+"\n"), 0o644); err != nil {
		t.Fatalf("failed to write .project_root: %v", err)
	}

	// ResolveGeminiProjectDir should find the project via .project_root fallback
	got, err := ResolveGeminiProjectDir("")
	if err != nil {
		t.Fatalf("expected fallback to find project dir, got error: %v", err)
	}

	if got != nameBasedDir {
		t.Fatalf("got %q, want %q", got, nameBasedDir)
	}
}

func TestResolveGeminiProjectDirProjectMissing(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
	})

	osGetwd = func() (string, error) {
		return "/tmp/specstory", nil
	}
	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	tmpDir := filepath.Join(fakeHome, ".gemini", "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}

	_, err := ResolveGeminiProjectDir("")
	if err == nil {
		t.Fatalf("expected error but got nil")
	}

	var pathErr *GeminiPathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected GeminiPathError, got %T", err)
	}

	if pathErr.Kind != "project_missing" {
		t.Fatalf("Kind = %s, want project_missing", pathErr.Kind)
	}
	// The error must carry the hash of the project the caller asked about;
	// the hash value itself is pinned by TestHashProjectPathDeterministic.
	wantHash, err := HashProjectPath("/tmp/specstory")
	if err != nil {
		t.Fatalf("HashProjectPath returned error: %v", err)
	}
	if pathErr.ProjectHash != wantHash {
		t.Fatalf("ProjectHash = %s, want %s", pathErr.ProjectHash, wantHash)
	}
}
