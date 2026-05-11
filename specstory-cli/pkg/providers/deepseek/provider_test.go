package deepseek

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	if versionFlag != "--version" {
		t.Errorf("versionFlag = %q, want --version", versionFlag)
	}
}

func TestProviderName(t *testing.T) {
	p := NewProvider()
	if got := p.Name(); got != "DeepSeek TUI" {
		t.Errorf("Name() = %q, want %q", got, "DeepSeek TUI")
	}
}

func TestClassifyCheckError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil error returns version_failed",
			err:  errors.New("boom"),
			want: "version_failed",
		},
		{
			name: "permission denied via os.ErrPermission",
			err:  os.ErrPermission,
			want: "permission_denied",
		},
		{
			name: "wrapped permission denied",
			err:  &os.PathError{Op: "exec", Path: "/x", Err: os.ErrPermission},
			want: "permission_denied",
		},
		{
			name: "generic error falls back to version_failed",
			err:  errors.New("the binary crashed"),
			want: "version_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyCheckError(tt.err)
			if got != tt.want {
				t.Errorf("classifyCheckError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestBuildCheckErrorMessage(t *testing.T) {
	tests := []struct {
		name      string
		errorType string
		command   string
		isCustom  bool
		stderr    string
		mustHave  []string
	}{
		{
			name:      "not_found default command suggests install",
			errorType: "not_found",
			command:   "deepseek",
			isCustom:  false,
			mustHave:  []string{"DeepSeek TUI", "PATH", "Install"},
		},
		{
			name:      "not_found custom command echoes path",
			errorType: "not_found",
			command:   "/opt/foo",
			isCustom:  true,
			mustHave:  []string{"DeepSeek TUI", "/opt/foo", "executable"},
		},
		{
			name:      "permission_denied includes chmod hint",
			errorType: "permission_denied",
			command:   "/usr/bin/deepseek",
			mustHave:  []string{"chmod", "/usr/bin/deepseek"},
		},
		{
			name:      "version_failed includes stderr verbatim",
			errorType: "version_failed",
			command:   "deepseek",
			stderr:    "deepseek: bad runtime, no biscuit",
			mustHave:  []string{"--version", "deepseek: bad runtime, no biscuit"},
		},
		{
			name:      "version_failed without stderr still gives diagnosis hint",
			errorType: "version_failed",
			command:   "deepseek",
			mustHave:  []string{"--version"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCheckErrorMessage(tt.errorType, tt.command, tt.isCustom, tt.stderr)
			for _, want := range tt.mustHave {
				if !strings.Contains(got, want) {
					t.Errorf("buildCheckErrorMessage missing %q\nfull message:\n%s", want, got)
				}
			}
		})
	}
}

func TestDetectAgent_NoSessionsReturnsFalse(t *testing.T) {
	// When ~/.deepseek/sessions doesn't exist (or is empty), DetectAgent must
	// return false without panicking. This guards against regressions where
	// listSessionFiles starts erroring on missing dirs.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	p := NewProvider()
	if got := p.DetectAgent("/some/project", false); got {
		t.Errorf("DetectAgent on empty home = true, want false")
	}
}

func TestDetectAgent_EmptyProjectMatchesAnySession(t *testing.T) {
	// An empty projectPath means "do you have any sessions at all?" — if yes,
	// return true regardless of which workspace they're tied to. This matches
	// the convention used by droid/gemini.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.MkdirAll(tmp+"/.deepseek/sessions", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sessionPath := tmp + "/.deepseek/sessions/abc.json"
	if err := os.WriteFile(sessionPath, []byte(`{"metadata":{"id":"abc","workspace":"/somewhere"},"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p := NewProvider()
	if got := p.DetectAgent("", false); !got {
		t.Errorf("DetectAgent with empty projectPath and one session = false, want true")
	}
}
