package spi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStringValue(t *testing.T) {
	args := map[string]any{
		"str":    "hello",
		"num":    json.Number("42"),
		"float":  3.5,
		"whole":  float64(7), // JSON has no integer type, so 7 decodes as a float
		"flag":   true,
		"bytes":  []byte("raw"),
		"nested": map[string]any{"a": 1},
	}

	tests := []struct {
		name string
		keys []string
		want string
	}{
		{name: "plain string", keys: []string{"str"}, want: "hello"},
		{name: "json.Number keeps its literal", keys: []string{"num"}, want: "42"},
		{name: "float renders without exponent", keys: []string{"float"}, want: "3.5"},
		{name: "whole float has no trailing decimal", keys: []string{"whole"}, want: "7"},
		{name: "bool", keys: []string{"flag"}, want: "true"},
		{name: "byte slice renders as text", keys: []string{"bytes"}, want: "raw"},
		{name: "first present key wins", keys: []string{"missing", "str"}, want: "hello"},
		{name: "missing keys yield empty", keys: []string{"nope"}, want: ""},
		{name: "no keys yield empty", keys: nil, want: ""},
		{name: "unsupported type yields empty", keys: []string{"nested"}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StringValue(args, tt.keys...); got != tt.want {
				t.Errorf("StringValue(%v) = %q, want %q", tt.keys, got, tt.want)
			}
		})
	}
}

func TestNormalizeToolName(t *testing.T) {
	// All of these spellings name the same tool across agents, so they must
	// collapse to one comparison key.
	for _, name := range []string{"read_file", "Read File", "read-file", "  READ_FILE  "} {
		if got := NormalizeToolName(name); got != "readfile" {
			t.Errorf("NormalizeToolName(%q) = %q, want %q", name, got, "readfile")
		}
	}
	if got := NormalizeToolName(""); got != "" {
		t.Errorf("NormalizeToolName(\"\") = %q, want empty", got)
	}
}

func TestTodoSymbol(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{status: "completed", want: "x"},
		{status: "done", want: "x"},
		{status: "COMPLETED", want: "x"},
		{status: " completed ", want: "x"}, // agents pad status values inconsistently
		{status: "in_progress", want: "⚡"},
		{status: "active", want: "⚡"},
		{status: "pending", want: " "},
		{status: "", want: " "},
		{status: "something_new", want: " "},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := TodoSymbol(tt.status); got != tt.want {
				t.Errorf("TodoSymbol(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestLanguageFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "main.go", want: "go"},            // already a valid tag, passes through
		{path: "/a/b/script.PY", want: "python"}, // shouted extensions map like lowercase
		{path: "app.js", want: "javascript"},
		{path: "app.jsx", want: "javascript"},
		{path: "app.ts", want: "typescript"},
		{path: "app.tsx", want: "typescript"},
		{path: "lib.rb", want: "ruby"},
		{path: "lib.rs", want: "rust"},
		{path: "Program.cs", want: "csharp"},
		{path: "util.h", want: "c"},
		{path: "util.cpp", want: "cpp"},
		{path: "util.cc", want: "cpp"},
		{path: "config.yml", want: "yaml"},
		{path: "README.md", want: "markdown"},
		{path: "data.json", want: "json"},               // already a valid tag
		{path: "file.test.spec.ts", want: "typescript"}, // only the final extension counts
		{path: "file.xyz", want: "xyz"},                 // unknown extension passes through
		{path: "Makefile", want: ""},                    // no extension: leave the fence untagged
		{path: "", want: ""},
		{path: "archive.tar.gz", want: "gz"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := LanguageFromPath(tt.path); got != tt.want {
				t.Errorf("LanguageFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestFormatDiffBlock(t *testing.T) {
	t.Run("both sides", func(t *testing.T) {
		got := FormatDiffBlock("a\nb", "c")
		want := "```diff\n-a\n-b\n+c\n```"
		if got != want {
			t.Errorf("FormatDiffBlock = %q, want %q", got, want)
		}
	})

	t.Run("creation emits only additions", func(t *testing.T) {
		got := FormatDiffBlock("", "new")
		want := "```diff\n+new\n```"
		if got != want {
			t.Errorf("FormatDiffBlock = %q, want %q", got, want)
		}
	})

	t.Run("deletion emits only removals", func(t *testing.T) {
		got := FormatDiffBlock("old", "")
		want := "```diff\n-old\n```"
		if got != want {
			t.Errorf("FormatDiffBlock = %q, want %q", got, want)
		}
	})

	t.Run("empty both sides", func(t *testing.T) {
		got := FormatDiffBlock("", "")
		want := "```diff\n\n```"
		if got != want {
			t.Errorf("FormatDiffBlock = %q, want %q", got, want)
		}
	})

	t.Run("trailing newline yields one marked blank line", func(t *testing.T) {
		// The trailing "\n" splits into a final empty line, which is marked and
		// then left as the last line — the block must not gain a blank tail.
		got := FormatDiffBlock("", "x\n")
		want := "```diff\n+x\n+\n```"
		if got != want {
			t.Errorf("FormatDiffBlock = %q, want %q", got, want)
		}
	})

	t.Run("embedded fence is outrun", func(t *testing.T) {
		got := FormatDiffBlock("", "```go")
		if !strings.HasPrefix(got, "````diff\n") {
			t.Errorf("fence must outrun backticks in the diff body: %q", got)
		}
	})
}

func TestRenderGenericJSON(t *testing.T) {
	t.Run("keys are sorted", func(t *testing.T) {
		got := RenderGenericJSON(map[string]any{"z": 1, "a": "first"})
		if !strings.Contains(got, "\"a\"") || strings.Index(got, "\"a\"") > strings.Index(got, "\"z\"") {
			t.Errorf("RenderGenericJSON should sort keys, got:\n%s", got)
		}
	})

	t.Run("empty args render nothing", func(t *testing.T) {
		if got := RenderGenericJSON(map[string]any{}); got != "" {
			t.Errorf("expected empty for no args, got %q", got)
		}
		if got := RenderGenericJSON(nil); got != "" {
			t.Errorf("expected empty for nil args, got %q", got)
		}
	})

	t.Run("dropped keys are omitted", func(t *testing.T) {
		got := RenderGenericJSON(map[string]any{"toolAction": "x", "Real": "keep"}, "toolAction", "toolSummary")
		if strings.Contains(got, "toolAction") || !strings.Contains(got, "Real") {
			t.Errorf("expected dropped key omitted and real key kept, got %q", got)
		}
	})

	t.Run("nothing left after drops renders nothing", func(t *testing.T) {
		got := RenderGenericJSON(map[string]any{"toolAction": "x", "toolSummary": "y"}, "toolAction", "toolSummary")
		if got != "" {
			t.Errorf("expected empty when only dropped keys remain, got %q", got)
		}
	})

	t.Run("caller args are not mutated", func(t *testing.T) {
		args := map[string]any{"toolAction": "x", "Real": "keep"}
		RenderGenericJSON(args, "toolAction")
		if _, ok := args["toolAction"]; !ok {
			t.Error("RenderGenericJSON must not delete keys from the caller's map")
		}
	})

	t.Run("value with quotes stays valid JSON", func(t *testing.T) {
		// Regression guard: a hand-rolled renderer previously emitted this value
		// without escaping, producing a malformed JSON block.
		got := RenderGenericJSON(map[string]any{"q": `he said "hi"\ok`})
		body := strings.TrimSuffix(strings.TrimPrefix(got, "```json\n"), "\n```")
		var round map[string]any
		if err := json.Unmarshal([]byte(body), &round); err != nil {
			t.Fatalf("rendered block is not valid JSON: %v\n%s", err, body)
		}
		if round["q"] != `he said "hi"\ok` {
			t.Errorf("value did not survive rendering: %q", round["q"])
		}
	})
}
