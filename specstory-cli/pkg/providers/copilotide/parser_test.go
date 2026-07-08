package copilotide

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// rawMessages converts JSON literals to the raw response array shape.
func rawMessages(items ...string) []json.RawMessage {
	raw := make([]json.RawMessage, len(items))
	for i, s := range items {
		raw[i] = json.RawMessage(s)
	}
	return raw
}

func TestExtractTextFromResponseArray(t *testing.T) {
	tests := []struct {
		name      string
		responses []json.RawMessage
		want      string
	}{
		{
			// The real-world truncation case: markdown fragments split around an
			// inlineReference file chip, with tool/progress items interleaved.
			name: "fragments around inline file reference",
			responses: rawMessages(
				`{"kind": "mcpServersStarting", "didStartServerIds": []}`,
				`{"kind": "toolInvocationSerialized", "toolId": "create_file"}`,
				`{"kind": "textEditGroup", "uri": {"path": "/proj/NEW_README.md"}}`,
				`{"value": "A new file named ", "supportThemeIcons": false}`,
				`{"kind": "inlineReference", "inlineReference": {"fsPath": "/proj/NEW_README.md", "path": "/proj/NEW_README.md", "scheme": "file"}}`,
				`{"value": " has been created with a fresh overview."}`,
			),
			want: "A new file named `NEW_README.md` has been created with a fresh overview.",
		},
		{
			name: "symbol reference uses its name",
			responses: rawMessages(
				`{"value": "See "}`,
				`{"kind": "inlineReference", "name": "ParseResponseKind", "inlineReference": {"fsPath": "/proj/parser.go"}}`,
				`{"value": " for details."}`,
			),
			want: "See `ParseResponseKind` for details.",
		},
		{
			name: "single markdown item",
			responses: rawMessages(
				`{"value": "Plain answer."}`,
			),
			want: "Plain answer.",
		},
		{
			name: "reference without name or path renders nothing",
			responses: rawMessages(
				`{"value": "Before"}`,
				`{"kind": "inlineReference", "inlineReference": {}}`,
				`{"value": " after"}`,
			),
			want: "Before after",
		},
		{
			name:      "no markdown items",
			responses: rawMessages(`{"kind": "mcpServersStarting"}`),
			want:      "",
		},
		{
			name:      "empty array",
			responses: nil,
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractTextFromResponseArray(tt.responses); got != tt.want {
				t.Errorf("ExtractTextFromResponseArray() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFormatToolMarkdown covers the pre-rendered tool body: input key-value pairs
// (multiline values fenced, internal keys skipped) and the fenced result.
func TestFormatToolMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		tool     *schema.ToolInfo
		contains []string
		absent   []string
	}{
		{
			// The motivating case: a create_file whose input carries the whole
			// written file. The content must survive into FormattedMarkdown.
			name: "input with multiline and scalar values",
			tool: &schema.ToolInfo{
				Name: "create_file",
				Input: map[string]interface{}{
					"filePath": "/proj/NEW_README.md",
					"content":  "# Title\n\nBody line.",
					"_cwd":     "/proj", // internal field, must be skipped
				},
			},
			contains: []string{
				"**Input:**",
				"- filePath: `/proj/NEW_README.md`",
				"- content:\n\n```\n# Title\n\nBody line.\n```",
			},
			absent: []string{"_cwd", "**Result:**"},
		},
		{
			name: "result rendered fenced",
			tool: &schema.ToolInfo{
				Name:   "run_in_terminal",
				Input:  map[string]interface{}{"command": "ls"},
				Output: map[string]interface{}{"result": "file-a\nfile-b"},
			},
			contains: []string{"- command: `ls`", "**Result:**\n\n```\nfile-a\nfile-b\n```"},
		},
		{
			// Values containing backtick fences must be wrapped in a longer fence
			// so they cannot terminate the block early.
			name: "fence sizing adapts to embedded fences",
			tool: &schema.ToolInfo{
				Name:   "create_file",
				Input:  map[string]interface{}{"content": "# Readme\n\n```sh\nmake build\n```\n"},
				Output: map[string]interface{}{"result": "before\n````\nfour\n````\nafter"},
			},
			contains: []string{
				"- content:\n\n````\n# Readme\n\n```sh\nmake build\n```\n\n````",
				"**Result:**\n\n`````\nbefore\n````\nfour\n````\nafter\n`````",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatToolMarkdown(tt.tool)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
			for _, ban := range tt.absent {
				if strings.Contains(got, ban) {
					t.Errorf("unexpected %q in:\n%s", ban, got)
				}
			}
		})
	}
}

// TestFormatToolMarkdown_Empty verifies a tool with no structured data pre-renders
// nothing, so ToolInfo.FormattedMarkdown stays nil and downstream fallbacks apply.
func TestFormatToolMarkdown_Empty(t *testing.T) {
	if got := FormatToolMarkdown(&schema.ToolInfo{Name: "MysteryTool"}); got != "" {
		t.Errorf("expected empty markdown for tool without data, got:\n%s", got)
	}
}

// TestFormatToolMarkdown_ResultCap verifies oversized results are truncated (inputs
// are deliberately uncapped).
func TestFormatToolMarkdown_ResultCap(t *testing.T) {
	tool := &schema.ToolInfo{
		Name:   "read_file",
		Output: map[string]interface{}{"result": strings.Repeat("x", toolResultCap+500)},
	}
	got := FormatToolMarkdown(tool)
	if !strings.Contains(got, "… (output truncated)") {
		t.Error("oversized result should be marked truncated")
	}
	if len(got) > toolResultCap+200 {
		t.Errorf("result not capped: len=%d", len(got))
	}

	small := &schema.ToolInfo{Name: "read_file", Output: map[string]interface{}{"result": "short"}}
	if s := FormatToolMarkdown(small); strings.Contains(s, "truncated") {
		t.Errorf("small result should not be truncated: %s", s)
	}
}

func TestCodeFence(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"no backticks", "plain text", "```"},
		{"inline code only", "uses `go build` here", "```"},
		{"double backticks", "``x``", "```"},
		{"triple backtick fence", "```sh\nls\n```", "````"},
		{"four backticks", "````\nnested\n````", "`````"},
		{"empty", "", "```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codeFence(tt.value); got != tt.want {
				t.Errorf("codeFence(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

// TestBuildToolInfoFromInvocation_PreRenders verifies the forward pass populates
// Summary and FormattedMarkdown so tool payloads survive cross-agent resume.
func TestBuildToolInfoFromInvocation_PreRenders(t *testing.T) {
	invocation := VSCodeToolInvocationResponse{Kind: "toolInvocationSerialized", ToolCallID: "call-1"}
	toolCall := VSCodeToolCallInfo{
		ID:        "call-1",
		Name:      "create_file",
		Arguments: `{"filePath": "/proj/a.md", "content": "line one\nline two"}`,
	}
	results := map[string]VSCodeToolCallResult{
		"call-1": {Content: []VSCodeToolCallContent{{Value: "created"}}},
	}

	toolInfo := BuildToolInfoFromInvocation(invocation, toolCall, results)
	if toolInfo.Summary == nil || *toolInfo.Summary != "Tool use: **create_file**" {
		t.Errorf("summary = %v", toolInfo.Summary)
	}
	if toolInfo.FormattedMarkdown == nil {
		t.Fatal("expected formatted markdown")
	}
	for _, want := range []string{"**Input:**", "line one\nline two", "- filePath: `/proj/a.md`", "**Result:**", "created"} {
		if !strings.Contains(*toolInfo.FormattedMarkdown, want) {
			t.Errorf("missing %q in:\n%s", want, *toolInfo.FormattedMarkdown)
		}
	}
}
