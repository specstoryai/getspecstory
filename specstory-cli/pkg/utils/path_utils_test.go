package utils

import (
	"os"
	"path/filepath"
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

			config, err := NewOutputPathConfig(tt.dir)

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
	// --output-dir only redirects history/*.md; debug stays in the specstory base dir.
	t.Run("with output-dir only (BaseDir)", func(t *testing.T) {
		baseDir := t.TempDir()
		config := &OutputPathConfig{BaseDir: baseDir}

		// History goes directly to BaseDir (no /history suffix)
		historyDir := config.GetHistoryDir()
		if historyDir != baseDir {
			t.Errorf("GetHistoryDir() = %v, want %v", historyDir, baseDir)
		}

		// Debug falls back to {cwd}/.specstory/debug (BaseDir does not affect it)
		debugDir := config.GetDebugDir()
		if !strings.Contains(debugDir, SPECSTORY_DIR) || !strings.Contains(debugDir, DEBUG_DIR) {
			t.Errorf("GetDebugDir() = %v, want path containing %s/%s", debugDir, SPECSTORY_DIR, DEBUG_DIR)
		}
	})

	// --specstory-dir redirects all .specstory outputs: history/, debug/, .project.json.
	t.Run("with specstory-dir only (SpecstoryDir)", func(t *testing.T) {
		specstoryDir := t.TempDir()
		config := &OutputPathConfig{SpecstoryDir: specstoryDir}

		// History goes to {specstoryDir}/history/
		historyDir := config.GetHistoryDir()
		expectedHistoryDir := filepath.Join(specstoryDir, HISTORY_DIR)
		if historyDir != expectedHistoryDir {
			t.Errorf("GetHistoryDir() = %v, want %v", historyDir, expectedHistoryDir)
		}

		// Debug goes to {specstoryDir}/debug/
		debugDir := config.GetDebugDir()
		expectedDebugDir := filepath.Join(specstoryDir, DEBUG_DIR)
		if debugDir != expectedDebugDir {
			t.Errorf("GetDebugDir() = %v, want %v", debugDir, expectedDebugDir)
		}

		// Log file goes to {specstoryDir}/debug/debug.log
		logPath := config.GetLogPath()
		expectedLogPath := filepath.Join(expectedDebugDir, DEBUG_LOG_FILE)
		if logPath != expectedLogPath {
			t.Errorf("GetLogPath() = %v, want %v", logPath, expectedLogPath)
		}
	})

	// When both are set, --output-dir wins for history; --specstory-dir governs debug.
	t.Run("with both BaseDir and SpecstoryDir", func(t *testing.T) {
		baseDir := t.TempDir()
		specstoryDir := t.TempDir()
		config := &OutputPathConfig{BaseDir: baseDir, SpecstoryDir: specstoryDir}

		// History goes directly to BaseDir (--output-dir takes precedence)
		historyDir := config.GetHistoryDir()
		if historyDir != baseDir {
			t.Errorf("GetHistoryDir() = %v, want %v", historyDir, baseDir)
		}

		// Debug goes to {specstoryDir}/debug/
		debugDir := config.GetDebugDir()
		expectedDebugDir := filepath.Join(specstoryDir, DEBUG_DIR)
		if debugDir != expectedDebugDir {
			t.Errorf("GetDebugDir() = %v, want %v", debugDir, expectedDebugDir)
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
