package cursoride

import (
	"runtime"
	"testing"
)

func TestUriToPath(t *testing.T) {
	tests := []struct {
		name      string
		uri       string
		wantPath  string
		wantError string
	}{
		// Standard file:// URIs (Linux/macOS)
		{
			name:     "standard Linux file URI",
			uri:      "file:///home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "standard Linux file URI with spaces",
			uri:      "file:///home/user/my%20project",
			wantPath: "/home/user/my project",
		},

		// WSL file://wsl.localhost URIs
		{
			name:     "WSL wsl.localhost URI with Ubuntu",
			uri:      "file://wsl.localhost/Ubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "WSL wsl.localhost URI with different distro",
			uri:      "file://wsl.localhost/Debian/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "WSL wsl.localhost URI case insensitive host",
			uri:      "file://WSL.LOCALHOST/Ubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "WSL wsl.localhost URI with deep path",
			uri:      "file://wsl.localhost/Ubuntu/home/user/code/specstory-monorepo",
			wantPath: "/home/user/code/specstory-monorepo",
		},
		{
			name:      "WSL wsl.localhost URI with only distro (no path)",
			uri:       "file://wsl.localhost/Ubuntu",
			wantError: "malformed WSL URI path",
		},

		// WSL wsl$ URIs
		{
			name:     "WSL wsl$ URI",
			uri:      "file://wsl$/Ubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "WSL wsl$ URI case insensitive",
			uri:      "file://WSL$/Ubuntu/home/user/project",
			wantPath: "/home/user/project",
		},

		// vscode-remote:// URIs (delegated to parseVSCodeRemoteURI)
		{
			name:     "vscode-remote URI with percent-encoded host",
			uri:      "vscode-remote://wsl%2Bubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "vscode-remote URI with plus in host",
			uri:      "vscode-remote://wsl+ubuntu/home/user/project",
			wantPath: "/home/user/project",
		},

		// Unsupported schemes
		{
			name:      "unsupported http scheme",
			uri:       "http://example.com/path",
			wantError: "unsupported URI scheme",
		},
		{
			name:      "unsupported https scheme",
			uri:       "https://example.com/path",
			wantError: "unsupported URI scheme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := uriToPath(tt.uri)

			if tt.wantError != "" {
				if err == nil {
					t.Errorf("uriToPath(%q) expected error containing %q, got nil", tt.uri, tt.wantError)
					return
				}
				if got := err.Error(); !contains(got, tt.wantError) {
					t.Errorf("uriToPath(%q) error = %q, want error containing %q", tt.uri, got, tt.wantError)
				}
				return
			}

			if err != nil {
				t.Errorf("uriToPath(%q) unexpected error: %v", tt.uri, err)
				return
			}

			if got != tt.wantPath {
				t.Errorf("uriToPath(%q) = %q, want %q", tt.uri, got, tt.wantPath)
			}
		})
	}
}

func TestUriToPath_WindowsPaths(t *testing.T) {
	// Windows-specific path handling only runs on Windows
	if runtime.GOOS != "windows" {
		t.Skip("Windows path tests only run on Windows")
	}

	tests := []struct {
		name     string
		uri      string
		wantPath string
	}{
		{
			name:     "Windows file URI",
			uri:      "file:///c%3A/Users/Admin/project",
			wantPath: "c:\\Users\\Admin\\project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := uriToPath(tt.uri)
			if err != nil {
				t.Errorf("uriToPath(%q) unexpected error: %v", tt.uri, err)
				return
			}
			if got != tt.wantPath {
				t.Errorf("uriToPath(%q) = %q, want %q", tt.uri, got, tt.wantPath)
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

		// Error cases
		{
			name:      "no path component",
			uri:       "vscode-remote://wsl%2Bubuntu",
			wantError: "malformed vscode-remote URI (no path)",
		},
		{
			name:      "unsupported SSH host",
			uri:       "vscode-remote://ssh-remote+myserver/home/user/project",
			wantError: "unsupported vscode-remote host",
		},
		{
			name:      "unsupported dev container host",
			uri:       "vscode-remote://dev-container+abc123/workspace",
			wantError: "unsupported vscode-remote host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVSCodeRemoteURI(tt.uri)

			if tt.wantError != "" {
				if err == nil {
					t.Errorf("parseVSCodeRemoteURI(%q) expected error containing %q, got nil", tt.uri, tt.wantError)
					return
				}
				if got := err.Error(); !contains(got, tt.wantError) {
					t.Errorf("parseVSCodeRemoteURI(%q) error = %q, want error containing %q", tt.uri, got, tt.wantError)
				}
				return
			}

			if err != nil {
				t.Errorf("parseVSCodeRemoteURI(%q) unexpected error: %v", tt.uri, err)
				return
			}

			if got != tt.wantPath {
				t.Errorf("parseVSCodeRemoteURI(%q) = %q, want %q", tt.uri, got, tt.wantPath)
			}
		})
	}
}

// contains checks if s contains substr (simple helper to avoid importing strings in tests)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
