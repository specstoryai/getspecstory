package spi

import (
	"strings"
	"testing"
)

func TestCodeFence(t *testing.T) {
	tests := []struct {
		name    string
		lang    string
		content string
		want    string
	}{
		{
			name:    "plain content gets three backticks",
			content: "hello\nworld",
			want:    "```\nhello\nworld\n```",
		},
		{
			name:    "lang becomes the info string",
			lang:    "bash",
			content: "ls -la",
			want:    "```bash\nls -la\n```",
		},
		{
			// The motivating case: content containing its own fence must not
			// close the wrapper early.
			name:    "embedded three-backtick fence forces four",
			content: "before\n```\ninner\n```\nafter",
			want:    "````\nbefore\n```\ninner\n```\nafter\n````",
		},
		{
			name:    "embedded four-backtick run forces five",
			content: "````",
			want:    "`````\n````\n`````",
		},
		{
			// Inline runs count too: CommonMark only closes on a fence LINE, but
			// sizing off the longest run anywhere keeps the rule simple and safe.
			name:    "inline backtick run sized past",
			content: "code span ```` here",
			want:    "`````\ncode span ```` here\n`````",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodeFence(tt.lang, tt.content); got != tt.want {
				t.Errorf("CodeFence(%q, %q) = %q, want %q", tt.lang, tt.content, got, tt.want)
			}
		})
	}
}

func TestCapRunes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"under cap unchanged", "short", 10, "short"},
		{"at cap unchanged", "12345", 5, "12345"},
		{"over cap truncated with marker", "1234567890", 5, "12345\n… (output truncated)"},
		{"multibyte not split", "héllo wörld", 6, "héllo \n… (output truncated)"},
		{"many multibyte runes under rune cap", "日本語テスト", 10, "日本語テスト"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CapRunes(tt.in, tt.max); got != tt.want {
				t.Errorf("CapRunes(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
	// A capped result must never split a rune (valid UTF-8 preserved).
	capped := CapRunes(strings.Repeat("é", 100), 50)
	if !strings.HasSuffix(capped, "… (output truncated)") || strings.ContainsRune(capped, '�') {
		t.Errorf("capped multibyte output invalid: %q", capped)
	}
}
