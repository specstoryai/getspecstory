package cursorcli

import (
	"os"
	"os/exec"
	"testing"
)

func TestParseCursorCommand(t *testing.T) {
	tests := []struct {
		name          string
		customCommand string
		expectedCmd   string
		expectedArgs  []string
		setupMocks    func()
		restoreMocks  func()
	}{
		{
			name:          "empty command returns default",
			customCommand: "",
			expectedCmd:   "cursor-agent",
			expectedArgs:  nil,
			setupMocks: func() {
				// Mock exec.LookPath to return cursor-agent as found
				execLookPath = func(file string) (string, error) {
					if file == "cursor-agent" {
						return "cursor-agent", nil
					}
					return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
				}
			},
			restoreMocks: func() {
				execLookPath = exec.LookPath
			},
		},
		{
			name:          "custom command with arguments",
			customCommand: "cursor-agent --model gpt-4 --temperature 0.7",
			expectedCmd:   "cursor-agent",
			expectedArgs:  []string{"--model", "gpt-4", "--temperature", "0.7"},
			setupMocks:    func() {},
			restoreMocks:  func() {},
		},
		{
			name:          "custom command with tilde expansion",
			customCommand: "~/bin/cursor-agent --verbose",
			expectedCmd:   os.Getenv("HOME") + "/bin/cursor-agent",
			expectedArgs:  []string{"--verbose"},
			setupMocks:    func() {},
			restoreMocks:  func() {},
		},
		{
			name:          "command with quoted argument containing spaces",
			customCommand: `cursor-agent --config "~/Library/Application Support/cursor.json"`,
			expectedCmd:   "cursor-agent",
			expectedArgs:  []string{"--config", "~/Library/Application Support/cursor.json"},
			setupMocks:    func() {},
			restoreMocks:  func() {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			tt.setupMocks()
			defer tt.restoreMocks()

			cmd, args := parseCursorCommand(tt.customCommand)

			if cmd != tt.expectedCmd {
				t.Errorf("parseCursorCommand() cmd = %v, want %v", cmd, tt.expectedCmd)
			}

			if len(args) != len(tt.expectedArgs) {
				t.Errorf("parseCursorCommand() args length = %v, want %v", len(args), len(tt.expectedArgs))
			} else {
				for i := range args {
					if args[i] != tt.expectedArgs[i] {
						t.Errorf("parseCursorCommand() args[%d] = %v, want %v", i, args[i], tt.expectedArgs[i])
					}
				}
			}
		})
	}
}

func TestGetDefaultCursorCommand(t *testing.T) {
	tests := []struct {
		name         string
		setupMocks   func()
		restoreMocks func()
		expected     string
	}{
		{
			name: "finds cursor-agent in PATH",
			setupMocks: func() {
				execLookPath = func(file string) (string, error) {
					if file == "cursor-agent" {
						return "cursor-agent", nil
					}
					return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
				}
			},
			restoreMocks: func() {
				execLookPath = exec.LookPath
			},
			expected: "cursor-agent",
		},
		{
			name: "finds cursor-agent in .cursor/bin",
			setupMocks: func() {
				home := "/test/home"
				osUserHomeDir = func() (string, error) {
					return home, nil
				}
				osStat = func(name string) (os.FileInfo, error) {
					if name == home+"/.cursor/bin/cursor-agent" {
						return nil, nil // File exists
					}
					return nil, os.ErrNotExist
				}
				execLookPath = func(file string) (string, error) {
					return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
				}
			},
			restoreMocks: func() {
				osUserHomeDir = os.UserHomeDir
				osStat = os.Stat
				execLookPath = exec.LookPath
			},
			expected: "/test/home/.cursor/bin/cursor-agent",
		},
		{
			name: "finds cursor-agent in .local/bin",
			setupMocks: func() {
				home := "/test/home"
				osUserHomeDir = func() (string, error) {
					return home, nil
				}
				osStat = func(name string) (os.FileInfo, error) {
					if name == home+"/.local/bin/cursor-agent" {
						return nil, nil // File exists
					}
					return nil, os.ErrNotExist
				}
				execLookPath = func(file string) (string, error) {
					return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
				}
			},
			restoreMocks: func() {
				osUserHomeDir = os.UserHomeDir
				osStat = os.Stat
				execLookPath = exec.LookPath
			},
			expected: "/test/home/.local/bin/cursor-agent",
		},
		{
			name: "defaults to cursor-agent when not found",
			setupMocks: func() {
				osUserHomeDir = func() (string, error) {
					return "/test/home", nil
				}
				osStat = func(name string) (os.FileInfo, error) {
					return nil, os.ErrNotExist
				}
				execLookPath = func(file string) (string, error) {
					return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
				}
			},
			restoreMocks: func() {
				osUserHomeDir = os.UserHomeDir
				osStat = os.Stat
				execLookPath = exec.LookPath
			},
			expected: "cursor-agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			tt.setupMocks()
			defer tt.restoreMocks()

			result := getDefaultCursorCommand()

			if result != tt.expected {
				t.Errorf("getDefaultCursorCommand() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestExpandTilde(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME environment variable not set")
	}

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "expands tilde at start",
			path:     "~/bin/cursor-agent",
			expected: home + "/bin/cursor-agent",
		},
		{
			name:     "no tilde to expand",
			path:     "/usr/local/bin/cursor-agent",
			expected: "/usr/local/bin/cursor-agent",
		},
		{
			name:     "tilde not at start",
			path:     "path/~/cursor-agent",
			expected: "path/~/cursor-agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expandTilde(tt.path)
			if result != tt.expected {
				t.Errorf("expandTilde(%s) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestGetPathType(t *testing.T) {
	tests := []struct {
		name         string
		cursorCmd    string
		resolvedPath string
		expected     string
	}{
		{
			name:         "cursor bin directory",
			cursorCmd:    "cursor-agent",
			resolvedPath: "/home/user/.cursor/bin/cursor-agent",
			expected:     "cursor_bin",
		},
		{
			name:         "user local directory",
			cursorCmd:    "cursor-agent",
			resolvedPath: "/home/user/.local/bin/cursor-agent",
			expected:     "user_local",
		},
		{
			name:         "absolute path",
			cursorCmd:    "/opt/cursor/cursor-agent",
			resolvedPath: "/opt/cursor/cursor-agent",
			expected:     "absolute_path",
		},
		{
			name:         "system path",
			cursorCmd:    "cursor-agent",
			resolvedPath: "/usr/bin/cursor-agent",
			expected:     "system_path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getPathType(tt.cursorCmd, tt.resolvedPath)
			if result != tt.expected {
				t.Errorf("getPathType(%s, %s) = %v, want %v", tt.cursorCmd, tt.resolvedPath, result, tt.expected)
			}
		})
	}
}
