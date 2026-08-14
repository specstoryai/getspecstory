package deepseektui

import (
	"fmt"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// formatToolCall renders a ToolInfo into a markdown body that the session
// renderer wraps in a <tool-use><details> block. The body combines a tool's
// input rendering with its (optional) result. DeepSeek attaches results in a
// later user message, so this function is called twice: once when the tool_use
// is converted (input only), then again from attachToolResults once the
// matching tool_result is in place.
func formatToolCall(tool *ToolInfo) string {
	if tool == nil {
		return ""
	}
	body := strings.TrimSpace(formatToolInput(tool))
	result := strings.TrimSpace(formatToolOutput(tool))

	var parts []string
	if body != "" {
		parts = append(parts, body)
	}
	if result != "" {
		parts = append(parts, result)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func formatToolInput(tool *ToolInfo) string {
	args := tool.Input
	switch spi.NormalizeToolName(tool.Name) {
	case "execshell", "execshellwait", "execinteract", "execshellinteract",
		"taskshellstart", "taskshellwait":
		return formatExecuteInput(args)
	case "readfile":
		return renderReadInput(args)
	case "listdir":
		return renderListInput(args)
	case "grepfiles":
		return renderGrepInput(args)
	case "filesearch":
		return renderFileSearchInput(args)
	case "websearch":
		return renderWebSearchInput(args)
	case "fetchurl":
		return renderWebFetchInput(args)
	case "writefile":
		return renderWriteInput(args)
	case "editfile":
		return renderEditInput(args)
	case "applypatch":
		return renderApplyPatch(args)
	case "todowrite", "updateplan", "checklistwrite":
		return renderTodoWrite(args)
	case "taskcreate", "taskread", "tasklist", "note":
		return spi.RenderGenericJSON(args)
	default:
		return spi.RenderGenericJSON(args)
	}
}

func formatToolOutput(tool *ToolInfo) string {
	if tool.Output == nil {
		return ""
	}
	content, _ := tool.Output["content"].(string)
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if strings.Contains(content, "\n") {
		return fmt.Sprintf("Output:\n```text\n%s\n```", content)
	}
	return fmt.Sprintf("Output: %s", content)
}

// extractPathHints surfaces filesystem paths referenced by a tool's input so
// downstream features (cloud sync, search) can index sessions by touched
// files. Mirrors droidcli's extractPathHints — same shell-command extraction
// via spi.ExtractShellPathHints.
func extractPathHints(input map[string]any, workspaceRoot string) []string {
	if input == nil {
		return nil
	}

	pathFields := []string{
		"path", "file_path", "filePath", "file",
		"dir", "directory", "directory_path", "target_directory",
		"workdir", "cwd", "target",
	}
	var hints []string
	for _, field := range pathFields {
		val, ok := input[field]
		if !ok {
			continue
		}
		switch v := val.(type) {
		case string:
			addPathHint(&hints, v, workspaceRoot)
		case []any:
			for _, entry := range v {
				if s, ok := entry.(string); ok {
					addPathHint(&hints, s, workspaceRoot)
				}
			}
		}
	}

	command, _ := input["command"].(string)
	if command == "" {
		command, _ = input["cmd"].(string)
	}
	if command != "" {
		cwd, _ := input["workdir"].(string)
		if cwd == "" {
			cwd, _ = input["cwd"].(string)
		}
		if cwd == "" {
			cwd = workspaceRoot
		}
		for _, sp := range spi.ExtractShellPathHints(command, cwd, workspaceRoot) {
			addPathHint(&hints, sp, workspaceRoot)
		}
	}

	return hints
}

func addPathHint(hints *[]string, value string, workspaceRoot string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	normalized := spi.NormalizePath(value, workspaceRoot)
	for _, existing := range *hints {
		if existing == normalized {
			return
		}
	}
	*hints = append(*hints, normalized)
}

// --- per-tool input renderers ---

func renderReadInput(args map[string]any) string {
	path := spi.StringValue(args, "file_path", "path", "file", "filePath")
	if path == "" {
		return spi.RenderGenericJSON(args)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Path: `%s`", path)
	offset := spi.StringValue(args, "offset", "start")
	limit := spi.StringValue(args, "limit", "lines", "max_lines")
	if offset != "" || limit != "" {
		if offset == "" {
			offset = "0"
		}
		if limit == "" {
			limit = "?"
		}
		fmt.Fprintf(&b, "\nLines: offset %s, limit %s", offset, limit)
	}
	return b.String()
}

func renderListInput(args map[string]any) string {
	path := spi.StringValue(args, "path", "dir", "directory", "directory_path", "target_directory")
	if path == "" {
		return spi.RenderGenericJSON(args)
	}
	return fmt.Sprintf("Path: `%s`", path)
}

func renderGrepInput(args map[string]any) string {
	parts := []string{}
	if pat := spi.StringValue(args, "pattern", "query", "regex"); pat != "" {
		parts = append(parts, fmt.Sprintf("Pattern: `%s`", pat))
	}
	if path := spi.StringValue(args, "path", "dir", "directory"); path != "" {
		parts = append(parts, fmt.Sprintf("Path: `%s`", path))
	}
	if glob := spi.StringValue(args, "include", "glob"); glob != "" {
		parts = append(parts, fmt.Sprintf("Glob: `%s`", glob))
	}
	if len(parts) == 0 {
		return spi.RenderGenericJSON(args)
	}
	return strings.Join(parts, "\n")
}

func renderFileSearchInput(args map[string]any) string {
	if pat := spi.StringValue(args, "pattern", "query", "name"); pat != "" {
		return fmt.Sprintf("Pattern: `%s`", pat)
	}
	return spi.RenderGenericJSON(args)
}

func renderWebSearchInput(args map[string]any) string {
	if q := spi.StringValue(args, "query", "q", "search"); q != "" {
		return fmt.Sprintf("Query: `%s`", q)
	}
	return spi.RenderGenericJSON(args)
}

func renderWebFetchInput(args map[string]any) string {
	url := spi.StringValue(args, "url", "uri")
	prompt := spi.StringValue(args, "prompt", "query")
	if url == "" && prompt == "" {
		return spi.RenderGenericJSON(args)
	}
	var parts []string
	if url != "" {
		parts = append(parts, fmt.Sprintf("URL: `%s`", url))
	}
	if prompt != "" {
		parts = append(parts, prompt)
	}
	return strings.Join(parts, "\n")
}

func renderWriteInput(args map[string]any) string {
	path := spi.StringValue(args, "file_path", "path", "file", "filePath")
	content := spi.StringValue(args, "content", "contents", "text", "data")
	if path == "" && content == "" {
		return spi.RenderGenericJSON(args)
	}
	var b strings.Builder
	if path != "" {
		fmt.Fprintf(&b, "Path: `%s`", path)
	}
	if content != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(formatContentBlock(content, path))
	}
	return b.String()
}

func renderEditInput(args map[string]any) string {
	path := spi.StringValue(args, "file_path", "path", "file", "filePath")
	oldText := spi.StringValue(args, "old_str", "old_text", "old_string", "old")
	newText := spi.StringValue(args, "new_str", "new_text", "new_string", "new")

	var b strings.Builder
	if path != "" {
		fmt.Fprintf(&b, "Path: `%s`", path)
	}
	if oldText != "" || newText != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(spi.FormatDiffBlock(oldText, newText))
	}
	if b.Len() == 0 {
		return spi.RenderGenericJSON(args)
	}
	return b.String()
}

func renderApplyPatch(args map[string]any) string {
	patch := spi.StringValue(args, "patch", "input")
	if patch == "" {
		return spi.RenderGenericJSON(args)
	}
	return spi.CodeFence("diff", strings.TrimSpace(patch))
}

func renderTodoWrite(args map[string]any) string {
	// A failed type assertion yields nil, and len(nil) == 0, so we don't need
	// to check the comma-ok bool separately.
	itemsRaw, _ := args["todos"].([]any)
	if len(itemsRaw) == 0 {
		// Fall back to plan/items keys used by update_plan / checklist_write.
		for _, key := range []string{"items", "plan", "steps"} {
			if v, ok := args[key].([]any); ok && len(v) > 0 {
				itemsRaw = v
				break
			}
		}
	}
	if len(itemsRaw) == 0 {
		return spi.RenderGenericJSON(args)
	}
	var b strings.Builder
	b.WriteString("Todo List:\n")
	for _, raw := range itemsRaw {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		status := strings.TrimSpace(spi.StringValue(item, "status"))
		desc := strings.TrimSpace(spi.StringValue(item, "description", "content", "text"))
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "- [%s] %s\n", spi.TodoSymbol(status), desc)
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatExecuteInput(args map[string]any) string {
	command := spi.StringValue(args, "command", "cmd")
	workdir := spi.StringValue(args, "workdir", "dir", "cwd")
	if command == "" && workdir == "" {
		return spi.RenderGenericJSON(args)
	}
	var b strings.Builder
	if workdir != "" {
		fmt.Fprintf(&b, "Directory: `%s`\n\n", workdir)
	}
	if command != "" {
		fmt.Fprintf(&b, "`%s`", command)
	}
	return b.String()
}

func formatContentBlock(content, path string) string {
	return spi.CodeFence(spi.LanguageFromPath(path), content)
}

// --- generic helpers (decoupled copy of droidcli's, scoped to this package) ---
