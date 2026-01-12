package spi

import (
	"strings"
	"testing"
)

func TestSplitCommandLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "simple command",
			input: "claude",
			want:  []string{"claude"},
		},
		{
			name:  "command with simple arguments",
			input: "claude --debug --verbose",
			want:  []string{"claude", "--debug", "--verbose"},
		},
		{
			name:  "double quoted argument with spaces",
			input: `claude --config "~/my settings/config.json"`,
			want:  []string{"claude", "--config", "~/my settings/config.json"},
		},
		{
			name:  "single quoted argument with spaces",
			input: `claude --log-file '/path with spaces/log.txt'`,
			want:  []string{"claude", "--log-file", "/path with spaces/log.txt"},
		},
		{
			name:  "mixed quotes",
			input: `claude --arg1 "value 1" --arg2 'value 2'`,
			want:  []string{"claude", "--arg1", "value 1", "--arg2", "value 2"},
		},
		{
			name:  "escaped quotes inside double quotes",
			input: `claude --msg "He said \"hello\""`,
			want:  []string{"claude", "--msg", `He said "hello"`},
		},
		{
			name:  "escaped quotes inside single quotes",
			input: `claude --msg 'It'\''s working'`,
			want:  []string{"claude", "--msg", "It's working"},
		},
		{
			name:  "escaped backslash",
			input: `claude --path "C:\\Users\\test"`,
			want:  []string{"claude", "--path", `C:\Users\test`},
		},
		{
			name:  "multiple spaces between arguments",
			input: "claude   --debug    --verbose",
			want:  []string{"claude", "--debug", "--verbose"},
		},
		{
			name:  "leading and trailing spaces",
			input: "  claude --debug  ",
			want:  []string{"claude", "--debug"},
		},
		{
			name:  "tabs and newlines",
			input: "claude\t--debug\n--verbose",
			want:  []string{"claude", "--debug", "--verbose"},
		},
		{
			name:  "empty string",
			input: "",
			want:  []string{},
		},
		{
			name:  "only whitespace",
			input: "   \t\n  ",
			want:  []string{},
		},
		{
			name:  "empty quotes",
			input: `claude --arg ""`,
			want:  []string{"claude", "--arg"},
		},
		{
			name:  "quoted empty string in middle",
			input: `claude "" --debug`,
			want:  []string{"claude", "--debug"},
		},
		{
			name:  "unclosed quote (treated as literal to end)",
			input: `claude --arg "value`,
			want:  []string{"claude", "--arg", "value"},
		},
		{
			name:  "complex real-world example",
			input: `claude --config "~/Library/Application Support/claude/config.json" --log-level debug --output-dir "/tmp/my logs"`,
			want:  []string{"claude", "--config", "~/Library/Application Support/claude/config.json", "--log-level", "debug", "--output-dir", "/tmp/my logs"},
		},
		{
			name:  "argument with equals and quotes",
			input: `claude --setting="value with spaces"`,
			want:  []string{"claude", `--setting=value with spaces`},
		},
		{
			name:  "codex command example",
			input: "codex",
			want:  []string{"codex"},
		},
		{
			name:  "cursor command example",
			input: "cursor-agent",
			want:  []string{"cursor-agent"},
		},
		{
			name:  "command with path",
			input: "/usr/local/bin/claude --debug",
			want:  []string{"/usr/local/bin/claude", "--debug"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitCommandLine(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("SplitCommandLine() returned %d args, want %d\nGot:  %#v\nWant: %#v", len(got), len(tt.want), got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("SplitCommandLine() arg[%d] = %q, want %q\nGot:  %#v\nWant: %#v", i, got[i], tt.want[i], got, tt.want)
				}
			}
		})
	}
}

// TestSplitCommandLine_EdgeCases tests additional edge cases
func TestSplitCommandLine_EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "consecutive empty quotes",
			input: `cmd "" "" --arg`,
			want:  []string{"cmd", "--arg"},
		},
		{
			name:  "quote after non-whitespace",
			input: `cmd arg"with quote"`,
			want:  []string{"cmd", "argwith quote"},
		},
		{
			name:  "backslash at end",
			input: `cmd arg\`,
			want:  []string{"cmd", "arg"},
		},
		{
			name:  "only escaped spaces",
			input: `cmd\ with\ spaces`,
			want:  []string{"cmd with spaces"},
		},
		{
			name:  "mixed escaped and quoted",
			input: `cmd "arg 1" arg\ 2 'arg 3'`,
			want:  []string{"cmd", "arg 1", "arg 2", "arg 3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitCommandLine(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("SplitCommandLine() returned %d args, want %d\nGot:  %#v\nWant: %#v", len(got), len(tt.want), got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("SplitCommandLine() arg[%d] = %q, want %q\nGot:  %#v\nWant: %#v", i, got[i], tt.want[i], got, tt.want)
				}
			}
		})
	}
}

// TestSplitCommandLine_Benchmarks provides benchmark data
func BenchmarkSplitCommandLine_Simple(b *testing.B) {
	input := "claude --debug --verbose"
	for i := 0; i < b.N; i++ {
		SplitCommandLine(input)
	}
}

func BenchmarkSplitCommandLine_Complex(b *testing.B) {
	input := `claude --config "~/Library/Application Support/claude/config.json" --log-level debug --output-dir "/tmp/my logs"`
	for i := 0; i < b.N; i++ {
		SplitCommandLine(input)
	}
}

func BenchmarkSplitCommandLine_LongCommand(b *testing.B) {
	// Simulate a very long command with many arguments
	var parts []string
	for i := 0; i < 100; i++ {
		parts = append(parts, "--arg", `"value with spaces"`)
	}
	input := "cmd " + strings.Join(parts, " ")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SplitCommandLine(input)
	}
}
