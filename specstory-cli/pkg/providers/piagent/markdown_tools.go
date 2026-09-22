package piagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// formatToolMarkdown preserves unrecognized fields even when a built-in has a
// bespoke rendering. Native inputs are never truncated; only displayed results
// are capped, leaving the complete structured output and RawData available.
func formatToolMarkdown(tool *schema.ToolInfo) (string, string) {
	var blocks []string
	in := tool.Input
	name := strings.ToLower(tool.Name)
	if failed, _ := tool.Output["is_error"].(bool); failed {
		// Error details can contain diagnostic diffs, not successfully applied edits.
		blocks = append(blocks, "**Error:**", renderToolOutput(tool, false))
		if args := spi.RenderGenericJSON(in); args != "" {
			blocks = append(blocks, "**Input:**\n"+args)
		}
		return "", strings.Join(blocks, "\n\n")
	}
	var consumed []string
	field := func(key, label, lang string) {
		value, exists := in[key]
		if !exists {
			return
		}
		switch value.(type) {
		case string, float64, bool, int, json.Number:
			blocks = append(blocks, "**"+label+":**\n"+spi.CodeFence(lang, spi.StringValue(in, key)))
			consumed = append(consumed, key)
		}
	}
	var summary string
	switch name {
	case "bash", "powershell":
		field("command", "Command", name)
		field("timeout", "Timeout (seconds)", "text")
		if cmd, ok := in["command"].(string); ok && cmd != "" && !strings.ContainsAny(cmd, "\n\r`") {
			summary = fmt.Sprintf("Tool use: **%s** `%s`", name, cmd)
		}
	case "read", "write", "edit", "grep", "find", "ls":
		field("path", "Path", "text")
		switch name {
		case "read":
			field("offset", "Offset (line)", "text")
			field("limit", "Limit", "text")
		case "write":
			field("content", "Content", spi.LanguageFromPath(spi.StringValue(in, "path")))
		case "edit":
			if old, ok := in["oldText"].(string); ok {
				if newText, ok := in["newText"].(string); ok {
					blocks = append(blocks, "**Edit:**\n"+spi.FormatDiffBlock(old, newText))
					consumed = append(consumed, "oldText", "newText")
				}
			}
			if edits, ok := in["edits"].([]any); ok {
				for i, edit := range edits {
					args, ok := edit.(map[string]any)
					if !ok {
						blocks = append(blocks, spi.RenderGenericJSON(map[string]any{"edit": edit}))
						continue
					}
					old, oldOK := args["oldText"].(string)
					newText, newOK := args["newText"].(string)
					var drop []string
					if oldOK && newOK {
						blocks = append(blocks, fmt.Sprintf("**Edit %d:**\n%s", i+1, spi.FormatDiffBlock(old, newText)))
						drop = []string{"oldText", "newText"}
					} else {
						blocks = append(blocks, spi.RenderGenericJSON(map[string]any{"edit": args}))
						continue
					}
					if extra := spi.RenderGenericJSON(args, drop...); extra != "" {
						blocks = append(blocks, extra)
					}
				}
				// An empty edits array is meaningful too: keep it in the generic fallback.
				if len(edits) > 0 {
					consumed = append(consumed, "edits")
				}
			}
		case "grep", "find":
			field("pattern", "Pattern", "text")
			field("limit", "Limit", "text")
			if name == "grep" {
				field("glob", "Glob", "text")
				field("ignoreCase", "Ignore case", "text")
				field("literal", "Literal", "text")
				field("context", "Context (lines)", "text")
			}
		case "ls":
			field("limit", "Limit", "text")
		}
	}
	if extra := spi.RenderGenericJSON(in, consumed...); extra != "" {
		blocks = append(blocks, "**Input:**\n"+extra)
	}
	if output := renderToolOutput(tool, true); output != "" {
		blocks = append(blocks, output)
	}
	return summary, strings.Join(blocks, "\n\n")
}

func renderToolOutput(tool *schema.ToolInfo, success bool) string {
	var blocks []string
	var consumed []string
	name := strings.ToLower(tool.Name)
	if content, ok := tool.Output["content"].(string); ok {
		lang := "text"
		if success && name == "read" {
			lang = spi.LanguageFromPath(spi.StringValue(tool.Input, "path"))
		}
		if name == "bash" || name == "powershell" {
			content = sanitizeShellOutput(content)
		}
		if content != "" {
			blocks = append(blocks, spi.CodeFence(lang, spi.CapRunes(content, 5000)))
		}
		consumed = append(consumed, "content")
	}
	if success && name == "edit" {
		if details, ok := tool.Output["details"].(map[string]any); ok {
			var rendered []string
			for _, key := range []string{"diff", "patch"} {
				if diff, ok := details[key].(string); ok {
					blocks = append(blocks, "**"+key+":**\n"+spi.CodeFence("diff", spi.CapRunes(diff, 5000)))
					rendered = append(rendered, key)
				}
			}
			if extra := cappedToolJSON(details, rendered...); extra != "" {
				blocks = append(blocks, "**Details:**\n"+extra)
			}
			consumed = append(consumed, "details")
		}
	}
	if _, ok := tool.Output["is_error"].(bool); ok {
		consumed = append(consumed, "is_error")
	}
	if extra := cappedToolJSON(tool.Output, consumed...); extra != "" {
		blocks = append(blocks, extra)
	}
	return strings.Join(blocks, "\n\n")
}

// Cap JSON before fencing it so truncation cannot remove the closing fence.
func cappedToolJSON(values map[string]any, drop ...string) string {
	kept := make(map[string]any, len(values))
	for key, value := range values {
		kept[key] = value
	}
	for _, key := range drop {
		delete(kept, key)
	}
	if len(kept) == 0 {
		return ""
	}
	data, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return ""
	}
	return spi.CodeFence("json", spi.CapRunes(string(data), 5000))
}

func sanitizeShellOutput(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(content))
}
