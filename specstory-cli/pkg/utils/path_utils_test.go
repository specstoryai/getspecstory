package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Note: GetCanonicalPath tests are in pkg/spi/path_utils_test.go

func TestNewOutputPathConfig(t *testing.T) {
	tests := []struct {
		name        string
		dir         string
		setup       func(t *testing.T) string
		cleanup     func(t *testing.T, dir string)
		wantErr     bool
		errContains string
	}{
		{
			name: "empty string uses defaults",
			dir:  "",
			setup: func(t *testing.T) string {
				return ""
			},
			cleanup: func(t *testing.T, dir string) {},
			wantErr: false,
		},
		{
			name: "relative path converts to absolute",
			dir:  "./test-output",
			setup: func(t *testing.T) string {
				return "./test-output"
			},
			cleanup: func(t *testing.T, dir string) {
				_ = os.RemoveAll("./test-output")
			},
			wantErr: false,
		},
		{
			name: "deeply nested directory creation",
			dir:  "./very/deeply/nested/directory/structure/for/testing",
			setup: func(t *testing.T) string {
				return "./very/deeply/nested/directory/structure/for/testing"
			},
			cleanup: func(t *testing.T, dir string) {
				_ = os.RemoveAll("./very")
			},
			wantErr: false,
		},
		{
			name: "path with special characters",
			dir:  "./test-output with spaces & special@chars!",
			setup: func(t *testing.T) string {
				return "./test-output with spaces & special@chars!"
			},
			cleanup: func(t *testing.T, dir string) {
				_ = os.RemoveAll("./test-output with spaces & special@chars!")
			},
			wantErr: false,
		},
		{
			name: "path with unicode characters",
			dir:  "./测试目录-テスト-🚀",
			setup: func(t *testing.T) string {
				return "./测试目录-テスト-🚀"
			},
			cleanup: func(t *testing.T, dir string) {
				_ = os.RemoveAll("./测试目录-テスト-🚀")
			},
			wantErr: false,
		},
		{
			name: "existing directory with write permissions",
			dir:  "",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				return dir
			},
			cleanup: func(t *testing.T, dir string) {},
			wantErr: false,
		},
		{
			name: "existing file not directory",
			dir:  "",
			setup: func(t *testing.T) string {
				tmpFile, err := os.CreateTemp("", "test-file-*")
				if err != nil {
					t.Fatal(err)
				}
				_ = tmpFile.Close()
				return tmpFile.Name()
			},
			cleanup: func(t *testing.T, dir string) {
				_ = os.Remove(dir)
			},
			wantErr:     true,
			errContains: "not a directory",
		},
		{
			name: "directory without write permissions",
			dir:  "",
			setup: func(t *testing.T) string {
				// Unix permission bits don't restrict directories on Windows —
				// 0555 stays writable there, so the failure can't be provoked.
				if runtime.GOOS == "windows" {
					t.Skip("directory write permissions cannot be revoked via mode bits on Windows")
				}
				dir := t.TempDir()
				noWriteDir := filepath.Join(dir, "no-write")
				if err := os.Mkdir(noWriteDir, 0555); err != nil {
					t.Fatal(err)
				}
				return noWriteDir
			},
			cleanup: func(t *testing.T, dir string) {
				parent := filepath.Dir(dir)
				_ = os.Chmod(dir, 0755)
				_ = os.RemoveAll(parent)
			},
			wantErr:     true,
			errContains: "not writable",
		},
		{
			name: "parent directory without write permissions",
			dir:  "",
			setup: func(t *testing.T) string {
				// See the skip above — mode bits don't restrict Windows directories.
				if runtime.GOOS == "windows" {
					t.Skip("directory write permissions cannot be revoked via mode bits on Windows")
				}
				dir := t.TempDir()
				noWriteDir := filepath.Join(dir, "no-write")
				if err := os.Mkdir(noWriteDir, 0555); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(noWriteDir, "child")
			},
			cleanup: func(t *testing.T, dir string) {
				parent := filepath.Dir(filepath.Dir(dir))
				_ = os.Chmod(filepath.Dir(dir), 0755)
				_ = os.RemoveAll(parent)
			},
			wantErr:     true,
			errContains: "failed to create",
		},
		{
			name: "absolute path preserved",
			dir:  "",
			setup: func(t *testing.T) string {
				absPath, _ := filepath.Abs("./test-absolute")
				return absPath
			},
			cleanup: func(t *testing.T, dir string) {
				_ = os.RemoveAll("./test-absolute")
			},
			wantErr: false,
		},
		{
			name: "path with double dots",
			dir:  "../../../test-output",
			setup: func(t *testing.T) string {
				return "../../../test-output"
			},
			cleanup: func(t *testing.T, dir string) {
				absPath, _ := filepath.Abs("../../../test-output")
				_ = os.RemoveAll(absPath)
			},
			wantErr: false,
		},
		{
			name: "path with single dot",
			dir:  "./././test-output",
			setup: func(t *testing.T) string {
				return "./././test-output"
			},
			cleanup: func(t *testing.T, dir string) {
				_ = os.RemoveAll("./test-output")
			},
			wantErr: false,
		},
		{
			name: "symlink to directory",
			dir:  "",
			setup: func(t *testing.T) string {
				realDir := t.TempDir()
				linkPath := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(realDir, linkPath); err != nil {
					t.Fatal(err)
				}
				return linkPath
			},
			cleanup: func(t *testing.T, dir string) {
				_ = os.Remove(dir)
			},
			wantErr: false,
		},
		{
			name: "path with trailing slashes",
			dir:  "./test-output///",
			setup: func(t *testing.T) string {
				return "./test-output///"
			},
			cleanup: func(t *testing.T, dir string) {
				_ = os.RemoveAll("./test-output")
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tt.setup(t)
			if dir != "" {
				tt.dir = dir
			}
			defer tt.cleanup(t, tt.dir)

			config, err := NewOutputPathConfig(tt.dir, "")

			if tt.wantErr {
				if err == nil {
					t.Errorf("NewOutputPathConfig() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("NewOutputPathConfig() error = %v, want error containing %v", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("NewOutputPathConfig() unexpected error = %v", err)
				return
			}

			// Verify config is not nil
			if config == nil {
				t.Fatal("NewOutputPathConfig() returned nil config")
			}

			// For non-empty dir, verify it's absolute
			if tt.dir != "" && !filepath.IsAbs(config.BaseDir) {
				t.Errorf("NewOutputPathConfig() BaseDir = %v, want absolute path", config.BaseDir)
			}
		})
	}
}

func TestOutputPathConfigMethods(t *testing.T) {
	// When --output-dir is set, all outputs go there: history directly in BaseDir,
	// .project.json / statistics.json at BaseDir, debug/ inside BaseDir.
	t.Run("with output-dir set (BaseDir)", func(t *testing.T) {
		baseDir := t.TempDir()
		config := &OutputPathConfig{BaseDir: baseDir}

		// History goes directly to BaseDir (no /history suffix)
		historyDir := config.GetHistoryDir()
		if historyDir != baseDir {
			t.Errorf("GetHistoryDir() = %v, want %v", historyDir, baseDir)
		}

		// Specstory dir is also BaseDir (.project.json and statistics.json land here)
		specstoryDir := config.GetSpecstoryDir()
		if specstoryDir != baseDir {
			t.Errorf("GetSpecstoryDir() = %v, want %v", specstoryDir, baseDir)
		}

		// Debug goes to {BaseDir}/debug/
		debugDir := config.GetDebugDir()
		expectedDebugDir := filepath.Join(baseDir, DEBUG_DIR)
		if debugDir != expectedDebugDir {
			t.Errorf("GetDebugDir() = %v, want %v", debugDir, expectedDebugDir)
		}

		// Log file goes to {BaseDir}/debug/debug.log
		logPath := config.GetLogPath()
		expectedLogPath := filepath.Join(expectedDebugDir, DEBUG_LOG_FILE)
		if logPath != expectedLogPath {
			t.Errorf("GetLogPath() = %v, want %v", logPath, expectedLogPath)
		}
	})

	t.Run("with custom debug base directory", func(t *testing.T) {
		baseDir := t.TempDir()
		debugBaseDir := t.TempDir()
		config := &OutputPathConfig{BaseDir: baseDir, DebugBaseDir: debugBaseDir}

		// Test GetDebugDir returns the custom debug base dir directly
		debugDir := config.GetDebugDir()
		if debugDir != debugBaseDir {
			t.Errorf("GetDebugDir() = %v, want %v", debugDir, debugBaseDir)
		}

		// Test GetHistoryDir still uses BaseDir
		historyDir := config.GetHistoryDir()
		if historyDir != baseDir {
			t.Errorf("GetHistoryDir() = %v, want %v", historyDir, baseDir)
		}

		// Test GetLogPath uses the custom debug base dir
		logPath := config.GetLogPath()
		expectedLogPath := filepath.Join(debugBaseDir, DEBUG_LOG_FILE)
		if logPath != expectedLogPath {
			t.Errorf("GetLogPath() = %v, want %v", logPath, expectedLogPath)
		}
	})

	t.Run("with empty base directory", func(t *testing.T) {
		config := &OutputPathConfig{}

		// Test GetHistoryDir - should include .specstory/history
		historyDir := config.GetHistoryDir()
		if !strings.Contains(historyDir, SPECSTORY_DIR) || !strings.Contains(historyDir, HISTORY_DIR) {
			t.Errorf("GetHistoryDir() = %v, want path containing %s/%s", historyDir, SPECSTORY_DIR, HISTORY_DIR)
		}

		// Test GetDebugDir - should include .specstory/debug
		debugDir := config.GetDebugDir()
		if !strings.Contains(debugDir, SPECSTORY_DIR) || !strings.Contains(debugDir, DEBUG_DIR) {
			t.Errorf("GetDebugDir() = %v, want path containing %s/%s", debugDir, SPECSTORY_DIR, DEBUG_DIR)
		}

		// Test GetLogPath
		logPath := config.GetLogPath()
		if !strings.Contains(logPath, DEBUG_LOG_FILE) {
			t.Errorf("GetLogPath() = %v, want path containing %s", logPath, DEBUG_LOG_FILE)
		}
	})
}

// TestResolveProjectPath covers the platform-independent behavior: empty-override
// fallback, cleaning of absolute overrides, absolutization of relative overrides,
// and canonicalization to the on-disk spelling. The Windows-only branch that
// preserves rootless remote-workspace paths (VS Code fsPaths like /home/u/proj on
// a Windows host) is gated on runtime.GOOS and exercised only on Windows builds,
// and the fallback-to-cwd error path requires os.Getwd to fail, which is not
// portably testable.
func TestResolveProjectPath(t *testing.T) {
	// Results are canonicalized (symlinks resolved), so expectations must start
	// from symlink-free fixture paths — t.TempDir() is a symlink on macOS
	// (/var -> /private/var).
	canonicalDir := func(t *testing.T) string {
		t.Helper()
		dir, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatalf("Failed to resolve temp dir symlinks: %v", err)
		}
		return dir
	}

	fallbackCwd := canonicalDir(t)
	absDir := canonicalDir(t)

	// Pin the process cwd for the relative-override case so the expectation is
	// built from a known-canonical base rather than whatever spelling the test
	// runner was launched from.
	processCwd := canonicalDir(t)
	t.Chdir(processCwd)

	tests := []struct {
		name     string
		override string
		want     string
	}{
		{
			name:     "empty override falls back to cwd",
			override: "",
			want:     fallbackCwd,
		},
		{
			name:     "absolute override returned as-is",
			override: absDir,
			want:     absDir,
		},
		{
			name:     "absolute override is cleaned",
			override: filepath.Join(absDir, "sub") + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "proj",
			want:     filepath.Join(absDir, "proj"),
		},
		{
			name:     "relative override resolved against the process working directory",
			override: filepath.Join("some", "relative", "dir"),
			want:     filepath.Join(processCwd, "some", "relative", "dir"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveProjectPath(tt.override, fallbackCwd); got != tt.want {
				t.Errorf("ResolveProjectPath(%q) = %q, want %q", tt.override, got, tt.want)
			}
		})
	}

	// A cwd spelled with the wrong case (possible on case-insensitive
	// filesystems, where the shell preserves whatever the user typed) must
	// resolve to the on-disk spelling — the exact prefix comparisons that
	// relativize recorded paths in generated markdown depend on it. Detected at
	// runtime rather than by GOOS: macOS volumes can be formatted either way.
	t.Run("cwd case corrected to on-disk spelling", func(t *testing.T) {
		base := canonicalDir(t)
		onDisk := filepath.Join(base, "MixedCase")
		if err := os.Mkdir(onDisk, 0755); err != nil {
			t.Fatalf("Failed to create MixedCase dir: %v", err)
		}
		misspelled := filepath.Join(base, "mixedcase")
		if _, err := os.Stat(misspelled); err != nil {
			t.Skip("filesystem is case-sensitive; misspelled path does not resolve")
		}

		if got := ResolveProjectPath("", misspelled); got != onDisk {
			t.Errorf("ResolveProjectPath cwd %q = %q, want on-disk spelling %q", misspelled, got, onDisk)
		}
	})
}
