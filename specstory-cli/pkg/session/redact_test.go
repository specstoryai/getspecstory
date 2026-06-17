package session

import (
	"strings"
	"testing"
)

func TestRedactContent_BuiltinPatterns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		label    string
		contains string // expected label in output
	}{
		{
			name:     "GitHub PAT",
			input:    "token: ghp_FakePATTokenForTestingOnlyNotRealXXXX",
			contains: "[REDACTED:GITHUB_PAT]",
		},
		{
			name:     "GitHub Models Token",
			input:    "GITHUB_MODELS_TOKEN=gghp_FakeModelsTokenForTestingNotRealXXXX",
			contains: "[REDACTED:GITHUB_MODELS_TOKEN]",
		},
		{
			name:     "GitHub fine-grained PAT",
			input:    "auth: github_pat_11ABCDEFGH0123456789abcdefghijklmnop",
			contains: "[REDACTED:GITHUB_FINE_GRAINED_PAT]",
		},
		{
			name:     "GitHub OAuth token",
			input:    "token=gho_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef",
			contains: "[REDACTED:GITHUB_OAUTH_TOKEN]",
		},
		{
			name:     "GitHub Actions token",
			input:    "GITHUB_TOKEN=ghs_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef",
			contains: "[REDACTED:GITHUB_ACTIONS_TOKEN]",
		},
		{
			name:     "Groq API key",
			input:    "GROQ_API_KEY=\"gsk_TestFakeKeyForUnitTestingPurposesOnlyNotARealKeyXXXXXXXXXX\"",
			contains: "[REDACTED:GROQ_API_KEY]",
		},
		{
			name:     "Anthropic API key",
			input:    "key: sk-ant-api03-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789",
			contains: "[REDACTED:ANTHROPIC_API_KEY]",
		},
		{
			name:     "OpenAI project key",
			input:    "OPENAI_API_KEY=sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwx",
			contains: "[REDACTED:OPENAI_PROJECT_KEY]",
		},
		{
			name:     "OpenAI legacy key",
			input:    "sk-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz01234567",
			contains: "[REDACTED:OPENAI_API_KEY]",
		},
		{
			name:     "Google API key",
			input:    "key=AIzaSyC0Da1ABCDEFGHIJKLMNOPQRSTUVWXYZabc",
			contains: "[REDACTED:GOOGLE_API_KEY]",
		},
		{
			name:     "AWS access key ID",
			input:    "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			contains: "[REDACTED:AWS_ACCESS_KEY_ID]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactContent(tt.input, nil)
			if !strings.Contains(got, tt.contains) {
				t.Errorf("RedactContent(%q) = %q, want it to contain %q", tt.input, got, tt.contains)
			}
			// Original secret text should be gone
			if got == tt.input {
				t.Errorf("RedactContent(%q): content was not modified", tt.input)
			}
		})
	}
}

func TestRedactContent_MultipleSecretsInOneString(t *testing.T) {
	input := "PAT: ghp_FakePATTokenForTestingOnlyNotRealXXXX and groq: gsk_TestFakeKeyForUnitTestingPurposesOnlyNotARealKeyXXXXXXXXXX"
	got := RedactContent(input, nil)
	if !strings.Contains(got, "[REDACTED:GITHUB_PAT]") {
		t.Errorf("expected GITHUB_PAT redacted, got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:GROQ_API_KEY]") {
		t.Errorf("expected GROQ_API_KEY redacted, got: %q", got)
	}
}

func TestRedactContent_NoSecrets(t *testing.T) {
	input := "This is a normal conversation with no secrets."
	got := RedactContent(input, nil)
	if got != input {
		t.Errorf("RedactContent(%q) = %q, want unchanged", input, got)
	}
}

func TestRedactContent_CustomPattern(t *testing.T) {
	input := "my-token-abc123DEF456ghi789JKL012"
	got := RedactContent(input, []string{`my-token-[A-Za-z0-9]{24,}`})
	if !strings.Contains(got, "[REDACTED:custom]") {
		t.Errorf("RedactContent with custom pattern: got %q, want [REDACTED:custom]", got)
	}
}

func TestRedactContent_InvalidCustomPattern(t *testing.T) {
	// Invalid regex should be skipped, not panic
	input := "some content with no-token-12345"
	got := RedactContent(input, []string{`[invalid(`})
	// Content should be unchanged since the pattern was invalid
	if got != input {
		t.Errorf("RedactContent with invalid pattern: got %q, want unchanged %q", got, input)
	}
}

func TestRedactContent_AnthropicNotMatchedByOpenAI(t *testing.T) {
	// sk-ant- should be caught by ANTHROPIC_API_KEY, not OPENAI_API_KEY
	input := "sk-ant-api03-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	got := RedactContent(input, nil)
	if !strings.Contains(got, "[REDACTED:ANTHROPIC_API_KEY]") {
		t.Errorf("expected ANTHROPIC_API_KEY, got: %q", got)
	}
	if strings.Contains(got, "[REDACTED:OPENAI_API_KEY]") {
		t.Errorf("Anthropic key should not match OPENAI_API_KEY pattern, got: %q", got)
	}
}

func TestRedactContent_EmptyInput(t *testing.T) {
	got := RedactContent("", nil)
	if got != "" {
		t.Errorf("RedactContent(\"\") = %q, want \"\"", got)
	}
}
