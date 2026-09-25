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

func TestInlineCode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text gets one backtick", in: "hello.py", want: "`hello.py`"},
		{
			// The motivating case: a backtick inside the span must not close it.
			name: "one embedded backtick forces a double run with padding",
			in:   "a`b",
			want: "`` a`b ``",
		},
		{name: "a run is sized past, not just counted", in: "x```y", want: "```` x```y ````"},
		{
			// CommonMark strips the padding, so the rendered span still begins
			// with the backtick.
			name: "leading backtick survives through padding",
			in:   "`quoted`",
			want: "`` `quoted` ``",
		},
		{name: "line breaks and runs of spaces collapse to one space", in: "  a \n\t b  ", want: "`a b`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InlineCode(tt.in); got != tt.want {
				t.Errorf("InlineCode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeShellOutput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "color codes are removed", in: "\x1b[31mred\x1b[0m text", want: "red text"},
		{name: "cursor movement is removed", in: "line\x1b[2K\x1b[1Gnext", want: "linenext"},
		{name: "CRLF becomes LF and newlines and tabs survive", in: "a\r\nb\tc", want: "a\nb\tc"},
		{name: "bare control bytes are dropped", in: "a\x07b\x00c\x7fd", want: "abcd"},
		{name: "multi-byte text is untouched", in: "héllo → wörld", want: "héllo → wörld"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeShellOutput(tt.in); got != tt.want {
				t.Errorf("SanitizeShellOutput(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
