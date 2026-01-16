package geminicli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestHashProjectPathDeterministic(t *testing.T) {
	const (
		projectPath = "/tmp/specstory"
		expected    = "a1b4e20c87f45c8f60096db37201de7ebdf549f1b6b33012051046d782f9b67e"
	)

	hash, err := HashProjectPath(projectPath)
	if err != nil {
		t.Fatalf("HashProjectPath returned error: %v", err)
	}

	if hash != expected {
		t.Fatalf("HashProjectPath = %s, want %s", hash, expected)
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

	expected := filepath.Join(fakeHome, ".gemini", "tmp", "a1b4e20c87f45c8f60096db37201de7ebdf549f1b6b33012051046d782f9b67e")
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
	if pathErr.ProjectHash != "a1b4e20c87f45c8f60096db37201de7ebdf549f1b6b33012051046d782f9b67e" {
		t.Fatalf("ProjectHash = %s, want hashed value", pathErr.ProjectHash)
	}
}
