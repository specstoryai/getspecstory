package spi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetCanonicalPath(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T) string
		wantErr  bool
		validate func(t *testing.T, input, result string)
	}{
		{
			name: "absolute path with correct case",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				return dir
			},
			wantErr: false,
			validate: func(t *testing.T, input, result string) {
				// After symlink resolution, the path might be different
				// (e.g., on macOS /var -> /private/var)
				// We should verify that the result is a valid canonical form
				// by checking that it resolves to the same location as the input
				inputResolved, _ := filepath.EvalSymlinks(input)
				if inputResolved == "" {
					inputResolved = input
				}
				// Both result and inputResolved should point to the same location
				// Compare them after cleaning
				if filepath.Clean(result) != filepath.Clean(inputResolved) {
					t.Errorf("GetCanonicalPath(%q) = %q, want %q (after symlink resolution)", input, result, inputResolved)
				}
			},
		},
		{
			name: "relative path converts to absolute",
			setup: func(t *testing.T) string {
				return "."
			},
			wantErr: false,
			validate: func(t *testing.T, input, result string) {
				if !filepath.IsAbs(result) {
					t.Errorf("GetCanonicalPath(%q) = %q, want absolute path", input, result)
				}
			},
		},
		{
			name: "non-existent path appends remaining components",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				return filepath.Join(dir, "nonexistent", "path", "components")
			},
			wantErr: false,
			validate: func(t *testing.T, input, result string) {
				if !strings.HasSuffix(result, "nonexistent/path/components") {
					t.Errorf("GetCanonicalPath(%q) = %q, want path ending with nonexistent/path/components", input, result)
				}
			},
		},
		{
			name: "deeply nested directory",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				nested := filepath.Join(dir, "level1", "level2", "level3")
				if err := os.MkdirAll(nested, 0755); err != nil {
					t.Fatal(err)
				}
				return nested
			},
			wantErr: false,
			validate: func(t *testing.T, input, result string) {
				if !strings.HasSuffix(result, "level1/level2/level3") {
					t.Errorf("GetCanonicalPath(%q) = %q, want path ending with level1/level2/level3", input, result)
				}
			},
		},
		{
			name: "path with special characters",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				special := filepath.Join(dir, "test with spaces", "special@chars!")
				if err := os.MkdirAll(special, 0755); err != nil {
					t.Fatal(err)
				}
				return special
			},
			wantErr: false,
			validate: func(t *testing.T, input, result string) {
				if !strings.Contains(result, "test with spaces") || !strings.Contains(result, "special@chars!") {
					t.Errorf("GetCanonicalPath(%q) = %q, want path containing special characters", input, result)
				}
			},
		},
		{
			name: "path with unicode characters",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				unicode := filepath.Join(dir, "测试目录", "テスト", "🚀")
				if err := os.MkdirAll(unicode, 0755); err != nil {
					t.Fatal(err)
				}
				return unicode
			},
			wantErr: false,
			validate: func(t *testing.T, input, result string) {
				if !strings.Contains(result, "测试目录") || !strings.Contains(result, "テスト") || !strings.Contains(result, "🚀") {
					t.Errorf("GetCanonicalPath(%q) = %q, want path containing unicode characters", input, result)
				}
			},
		},
		{
			name: "case-insensitive matching on macOS",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				// Create directory with specific case
				testDir := filepath.Join(dir, "TestDirectory")
				if err := os.Mkdir(testDir, 0755); err != nil {
					t.Fatal(err)
				}
				// Return path with different case
				return filepath.Join(dir, "testdirectory")
			},
			wantErr: false,
			validate: func(t *testing.T, input, result string) {
				// On case-insensitive filesystems (macOS), should return the actual case
				// On case-sensitive filesystems (Linux), it depends on what exists
				if !strings.HasSuffix(result, "TestDirectory") && !strings.HasSuffix(result, "testdirectory") {
					t.Errorf("GetCanonicalPath(%q) = %q, want path ending with TestDirectory or testdirectory", input, result)
				}
			},
		},
		{
			name: "path with trailing slashes",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				return dir + "///"
			},
			wantErr: false,
			validate: func(t *testing.T, input, result string) {
				if strings.HasSuffix(result, "/") {
					t.Errorf("GetCanonicalPath(%q) = %q, want path without trailing slashes", input, result)
				}
			},
		},
		{
			name: "partially existing path",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				existing := filepath.Join(dir, "existing")
				if err := os.Mkdir(existing, 0755); err != nil {
					t.Fatal(err)
				}
				// Return path where first part exists but second doesn't
				return filepath.Join(existing, "nonexistent", "deeper")
			},
			wantErr: false,
			validate: func(t *testing.T, input, result string) {
				if !strings.Contains(result, "existing") || !strings.Contains(result, "nonexistent") {
					t.Errorf("GetCanonicalPath(%q) = %q, want path containing both existing and nonexistent parts", input, result)
				}
			},
		},
		{
			name: "symlink resolution",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				// Create a real directory
				realDir := filepath.Join(dir, "real", "directory")
				if err := os.MkdirAll(realDir, 0755); err != nil {
					t.Fatal(err)
				}
				// Create a symlink pointing to the real directory
				symlinkPath := filepath.Join(dir, "symlink")
				if err := os.Symlink(realDir, symlinkPath); err != nil {
					t.Fatal(err)
				}
				// Return the symlink path
				return symlinkPath
			},
			wantErr: false,
			validate: func(t *testing.T, input, result string) {
				// The result should be the real path, not the symlink path
				// It should contain "real/directory" and NOT end with "symlink"
				if strings.HasSuffix(result, "symlink") {
					t.Errorf("GetCanonicalPath(%q) = %q, symlink was not resolved (result still ends with 'symlink')", input, result)
				}
				if !strings.Contains(result, "real") || !strings.Contains(result, "directory") {
					t.Errorf("GetCanonicalPath(%q) = %q, want path containing 'real' and 'directory'", input, result)
				}
			},
		},
		{
			name: "nested symlinks resolution",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				// Create a real directory
				realDir := filepath.Join(dir, "actual", "target")
				if err := os.MkdirAll(realDir, 0755); err != nil {
					t.Fatal(err)
				}
				// Create first level symlink
				symlink1 := filepath.Join(dir, "link1")
				if err := os.Symlink(realDir, symlink1); err != nil {
					t.Fatal(err)
				}
				// Create second level symlink pointing to first symlink
				symlink2 := filepath.Join(dir, "link2")
				if err := os.Symlink(symlink1, symlink2); err != nil {
					t.Fatal(err)
				}
				// Return the nested symlink path
				return symlink2
			},
			wantErr: false,
			validate: func(t *testing.T, input, result string) {
				// The result should be the real path, resolving all symlinks
				if strings.Contains(result, "link1") || strings.Contains(result, "link2") {
					t.Errorf("GetCanonicalPath(%q) = %q, nested symlinks were not fully resolved", input, result)
				}
				if !strings.Contains(result, "actual") || !strings.Contains(result, "target") {
					t.Errorf("GetCanonicalPath(%q) = %q, want path containing 'actual' and 'target'", input, result)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := tt.setup(t)

			result, err := GetCanonicalPath(input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("GetCanonicalPath() error = nil, wantErr %v", tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("GetCanonicalPath() unexpected error = %v", err)
				return
			}

			// Result should always be absolute
			if !filepath.IsAbs(result) {
				t.Errorf("GetCanonicalPath(%q) = %q, want absolute path", input, result)
			}

			// Run custom validation if provided
			if tt.validate != nil {
				tt.validate(t, input, result)
			}
		})
	}
}

// TestSetDebugBaseDir tests the debug base dir override mechanism
func TestSetDebugBaseDir(t *testing.T) {
	// Clean up after test to avoid affecting other tests
	defer SetDebugBaseDir("")

	t.Run("default path without override", func(t *testing.T) {
		SetDebugBaseDir("")
		result := GetDebugDir("test-session")
		expected := filepath.Join(".specstory", "debug", "test-session")
		if result != expected {
			t.Errorf("GetDebugDir() = %q, want %q", result, expected)
		}
	})

	t.Run("override changes output path", func(t *testing.T) {
		SetDebugBaseDir("/custom/debug")
		result := GetDebugDir("test-session")
		expected := filepath.Join("/custom/debug", "test-session")
		if result != expected {
			t.Errorf("GetDebugDir() = %q, want %q", result, expected)
		}
	})

	t.Run("empty override restores default", func(t *testing.T) {
		SetDebugBaseDir("/custom/debug")
		SetDebugBaseDir("")
		result := GetDebugDir("test-session")
		expected := filepath.Join(".specstory", "debug", "test-session")
		if result != expected {
			t.Errorf("GetDebugDir() = %q, want %q", result, expected)
		}
	})
}

// TestGenerateReadableName tests the generation of human-readable session names
func TestGenerateReadableName(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected string
	}{
		{
			name:     "Empty message returns empty",
			message:  "",
			expected: "",
		},
		{
			name:     "Short message returned as-is",
			message:  "Let's create a session!",
			expected: "Let's create a session!",
		},
		{
			name:     "Message with newlines normalized",
			message:  "First line\nSecond line\nThird line",
			expected: "First line Second line Third line",
		},
		{
			name:     "Message with multiple spaces normalized",
			message:  "Hello    world     how   are   you",
			expected: "Hello world how are you",
		},
		{
			name:     "Long message truncated at word boundary",
			message:  "This is a very long message that exceeds one hundred characters and should be truncated at a word boundary to avoid breaking words in the middle",
			expected: "This is a very long message that exceeds one hundred characters and should be truncated at a word...",
		},
		{
			name:     "Long message without spaces truncated at exactly 100 chars",
			message:  strings.Repeat("a", 150),
			expected: strings.Repeat("a", 100) + "...",
		},
		{
			name:     "Exactly 100 characters not truncated",
			message:  strings.Repeat("a", 100),
			expected: strings.Repeat("a", 100),
		},
		{
			name:     "Message with tabs and mixed whitespace",
			message:  "Hello\tworld\n\nHow\t\tare   you?",
			expected: "Hello world How are you?",
		},
		{
			name:     "IDE tag prefix stripped before real message",
			message:  "<ide_opened_file>The user opened /Users/foo/bar.go</ide_opened_file>\n\nFix the login bug",
			expected: "Fix the login bug",
		},
		{
			name:     "Only IDE tags returns empty",
			message:  "<ide_opened_file>bar.go</ide_opened_file>",
			expected: "",
		},
		{
			name:     "Multiple IDE tags stripped before real message",
			message:  "<ide_opened_file>foo.go</ide_opened_file>\n<ide_selection>some code</ide_selection>\n\nRefactor the auth module",
			expected: "Refactor the auth module",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateReadableName(tt.message)
			if result != tt.expected {
				t.Errorf("GenerateReadableName() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestFileURIToPath covers the shared file-URI converter on the POSIX forms
// (the Windows drive-letter/UNC branches are separator-gated and can only run
// on a Windows build; cursoride's TestUriToPath_WindowsPaths documents them).
func TestFileURIToPath(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		want    string
		wantErr bool
	}{
		{name: "plain posix path", uri: "file:///tmp/project/main.go", want: "/tmp/project/main.go"},
		{name: "percent escapes decoded", uri: "file:///tmp/my%20file.go", want: "/tmp/my file.go"},
		{name: "literal percent sequence decoded exactly once", uri: "file:///tmp/literal%2520pct", want: "/tmp/literal%20pct"},
		{name: "wsl.localhost strips host and distro", uri: "file://wsl.localhost/Ubuntu/home/u/proj", want: "/home/u/proj"},
		{name: "wsl host without in-distro path is malformed", uri: "file://wsl.localhost/Ubuntu", wantErr: true},
		{name: "non-wsl host dropped on posix", uri: "file://server/share/dir", want: "/share/dir"},
		{name: "non-file scheme rejected", uri: "https://example.com/x", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FileURIToPath(tt.uri)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("FileURIToPath(%q) = %q, want error", tt.uri, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("FileURIToPath(%q) unexpected error: %v", tt.uri, err)
			}
			if got != tt.want {
				t.Errorf("FileURIToPath(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

func TestParseVSCodeRemoteURI(t *testing.T) {
	tests := []struct {
		name      string
		uri       string
		wantPath  string
		wantError string
	}{
		// Valid WSL URIs
		{
			name:     "percent-encoded wsl+ubuntu",
			uri:      "vscode-remote://wsl%2Bubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "unencoded wsl+ubuntu",
			uri:      "vscode-remote://wsl+ubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "percent-encoded wsl+Debian",
			uri:      "vscode-remote://wsl%2BDebian/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "case insensitive WSL host",
			uri:      "vscode-remote://WSL%2BUbuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "wsl host without distro name",
			uri:      "vscode-remote://wsl/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "deep path",
			uri:      "vscode-remote://wsl%2Bubuntu/home/user/code/specstory-monorepo",
			wantPath: "/home/user/code/specstory-monorepo",
		},
		{
			name:     "path with spaces encoded",
			uri:      "vscode-remote://wsl%2Bubuntu/home/user/my%20project",
			wantPath: "/home/user/my project",
		},
		{
			name:     "root path",
			uri:      "vscode-remote://wsl%2Bubuntu/",
			wantPath: "/",
		},

		// Valid SSH remote URIs
		{
			name:     "ssh-remote with simple config",
			uri:      "vscode-remote://ssh-remote+myserver/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "ssh-remote with hex-encoded config",
			uri:      "vscode-remote://ssh-remote%2B7b22686f73744e616d65223a226d61632d6d696e69227d/Users/bago/code/getspecstory",
			wantPath: "/Users/bago/code/getspecstory",
		},
		{
			name:     "ssh-remote case insensitive",
			uri:      "vscode-remote://SSH-REMOTE+myserver/home/user/project",
			wantPath: "/home/user/project",
		},

		// Valid tunnel URIs
		{
			name:     "tunnel with simple host",
			uri:      "vscode-remote://tunnel+myhost/work/group/user/myproject",
			wantPath: "/work/group/user/myproject",
		},
		{
			name:     "tunnel with percent-encoded host",
			uri:      "vscode-remote://tunnel%2Bmyhost/work/group/user/myproject",
			wantPath: "/work/group/user/myproject",
		},
		{
			name:     "tunnel case insensitive",
			uri:      "vscode-remote://TUNNEL+myhost/home/user/project",
			wantPath: "/home/user/project",
		},

		// Dev container URIs - path returned as-is (container-internal path)
		{
			name:     "dev container URI with hex-encoded config",
			uri:      "vscode-remote://dev-container%2B7b2273657474696e6754797065223a22636f6e7461696e6572222c22636f6e7461696e65724964223a22656335613261653766636632227d/workspace",
			wantPath: "/workspace",
		},
		{
			name:     "dev container URI case insensitive",
			uri:      "vscode-remote://DEV-CONTAINER%2Babc123/home/user/project",
			wantPath: "/home/user/project",
		},

		// Error cases
		{
			name:      "no path component",
			uri:       "vscode-remote://wsl%2Bubuntu",
			wantError: "malformed vscode-remote URI (no path)",
		},
		{
			name:      "unsupported host",
			uri:       "vscode-remote://codespaces%2Babc/home/user/project",
			wantError: "unsupported vscode-remote host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseVSCodeRemoteURI(tt.uri)

			if tt.wantError != "" {
				if err == nil {
					t.Errorf("ParseVSCodeRemoteURI(%q) expected error containing %q, got nil", tt.uri, tt.wantError)
					return
				}
				if got := err.Error(); !strings.Contains(got, tt.wantError) {
					t.Errorf("ParseVSCodeRemoteURI(%q) error = %q, want error containing %q", tt.uri, got, tt.wantError)
				}
				return
			}

			if err != nil {
				t.Errorf("ParseVSCodeRemoteURI(%q) unexpected error: %v", tt.uri, err)
				return
			}

			if got != tt.wantPath {
				t.Errorf("ParseVSCodeRemoteURI(%q) = %q, want %q", tt.uri, got, tt.wantPath)
			}
		})
	}
}

func TestIsRemoteURIRequiringBasenameMatch(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want bool
	}{
		{
			name: "ssh-remote URI matches",
			uri:  "vscode-remote://ssh-remote+myserver/home/user/project",
			want: true,
		},
		{
			name: "ssh-remote URI case insensitive",
			uri:  "vscode-remote://SSH-REMOTE+myserver/home/user/project",
			want: true,
		},
		{
			name: "tunnel URI matches",
			uri:  "vscode-remote://tunnel+myhost/work/group/user/myproject",
			want: true,
		},
		{
			name: "tunnel URI case insensitive",
			uri:  "vscode-remote://TUNNEL+myhost/work/group/user/myproject",
			want: true,
		},
		{
			name: "dev-container URI matches",
			uri:  "vscode-remote://dev-container%2Babc123/workspace",
			want: true,
		},
		{
			name: "dev-container URI case insensitive",
			uri:  "vscode-remote://DEV-CONTAINER%2Babc123/home/user/project",
			want: true,
		},
		{
			name: "wsl URI does not match",
			uri:  "vscode-remote://wsl%2Bubuntu/home/user/project",
			want: false,
		},
		{
			name: "local file URI does not match",
			uri:  "file:///Users/bago/code/getspecstory",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRemoteURIRequiringBasenameMatch(tt.uri); got != tt.want {
				t.Errorf("IsRemoteURIRequiringBasenameMatch(%q) = %v, want %v", tt.uri, got, tt.want)
			}
		})
	}
}
