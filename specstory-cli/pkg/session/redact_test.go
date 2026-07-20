package session

import (
	"strings"
	"testing"
)

// The fixtures below are syntactically valid but fabricated credentials chosen
// because they are high-entropy enough to clear betterleaks' entropy gates and
// therefore detect reliably. betterleaks intentionally ignores obviously fake,
// low-entropy strings (e.g. sequential ABCDEF... tokens) to avoid false
// positives, so tests must use realistic-looking values.
const (
	fakeGitHubOAuth = "gho_16C7e42F292c6912E7710c838347Ae178B4a"
	fakeGCPAPIKey   = "AIzaSyD8xKq2mL9nP4rT7wZ0aB3cE6fH1jG5kM7"
)

func TestRedactContent_BuiltinPatterns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string // expected placeholder in output
	}{
		{
			name:     "GitHub OAuth token",
			input:    "token=" + fakeGitHubOAuth,
			contains: "[REDACTED:github-oauth]",
		},
		{
			name:     "Google API key",
			input:    "key=" + fakeGCPAPIKey,
			contains: "[REDACTED:gcp-api-key]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactContent(tt.input)
			if !strings.Contains(got, tt.contains) {
				t.Errorf("RedactContent(%q) = %q, want it to contain %q", tt.input, got, tt.contains)
			}
			// The original secret text must no longer be present.
			if got == tt.input {
				t.Errorf("RedactContent(%q): content was not modified", tt.input)
			}
		})
	}
}

func TestRedactContent_MultipleSecretsInOneString(t *testing.T) {
	input := "oauth: " + fakeGitHubOAuth + " and google: " + fakeGCPAPIKey
	got := RedactContent(input)
	if !strings.Contains(got, "[REDACTED:github-oauth]") {
		t.Errorf("expected github-oauth redacted, got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:gcp-api-key]") {
		t.Errorf("expected gcp-api-key redacted, got: %q", got)
	}
	if strings.Contains(got, fakeGitHubOAuth) || strings.Contains(got, fakeGCPAPIKey) {
		t.Errorf("original secrets still present, got: %q", got)
	}
}

func TestRedactContent_NoSecrets(t *testing.T) {
	input := "This is a normal conversation with no secrets."
	got := RedactContent(input)
	if got != input {
		t.Errorf("RedactContent(%q) = %q, want unchanged", input, got)
	}
}

func TestRedactContent_EmptyInput(t *testing.T) {
	got := RedactContent("")
	if got != "" {
		t.Errorf("RedactContent(\"\") = %q, want \"\"", got)
	}
}
