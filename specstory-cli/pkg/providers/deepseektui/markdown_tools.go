package deepseektui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
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
	switch normalizeToolName(tool.Name) {
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
		return renderGenericJSON(args)
	default:
		return renderGenericJSON(args)
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
	path := stringValue(args, "file_path", "path", "file", "filePath")
	if path == "" {
		return renderGenericJSON(args)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Path: `%s`", path)
	offset := stringValue(args, "offset", "start")
	limit := stringValue(args, "limit", "lines", "max_lines")
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
	path := stringValue(args, "path", "dir", "directory", "directory_path", "target_directory")
	if path == "" {
		return renderGenericJSON(args)
	}
	return fmt.Sprintf("Path: `%s`", path)
}

func renderGrepInput(args map[string]any) string {
	parts := []string{}
	if pat := stringValue(args, "pattern", "query", "regex"); pat != "" {
		parts = append(parts, fmt.Sprintf("Pattern: `%s`", pat))
	}
	if path := stringValue(args, "path", "dir", "directory"); path != "" {
		parts = append(parts, fmt.Sprintf("Path: `%s`", path))
	}
	if glob := stringValue(args, "include", "glob"); glob != "" {
		parts = append(parts, fmt.Sprintf("Glob: `%s`", glob))
	}
	if len(parts) == 0 {
		return renderGenericJSON(args)
	}
	return strings.Join(parts, "\n")
}

func renderFileSearchInput(args map[string]any) string {
	if pat := stringValue(args, "pattern", "query", "name"); pat != "" {
		return fmt.Sprintf("Pattern: `%s`", pat)
	}
	return renderGenericJSON(args)
}

func renderWebSearchInput(args map[string]any) string {
	if q := stringValue(args, "query", "q", "search"); q != "" {
		return fmt.Sprintf("Query: `%s`", q)
	}
	return renderGenericJSON(args)
}

func renderWebFetchInput(args map[string]any) string {
	url := stringValue(args, "url", "uri")
	prompt := stringValue(args, "prompt", "query")
	if url == "" && prompt == "" {
		return renderGenericJSON(args)
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
	path := stringValue(args, "file_path", "path", "file", "filePath")
	content := stringValue(args, "content", "contents", "text", "data")
	if path == "" && content == "" {
		return renderGenericJSON(args)
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
	path := stringValue(args, "file_path", "path", "file", "filePath")
	oldText := stringValue(args, "old_str", "old_text", "old_string", "old")
	newText := stringValue(args, "new_str", "new_text", "new_string", "new")

	var b strings.Builder
	if path != "" {
		fmt.Fprintf(&b, "Path: `%s`", path)
	}
	if oldText != "" || newText != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(formatDiffBlock(oldText, newText))
	}
	if b.Len() == 0 {
		return renderGenericJSON(args)
	}
	return b.String()
}

func renderApplyPatch(args map[string]any) string {
	patch := stringValue(args, "patch", "input")
	if patch == "" {
		return renderGenericJSON(args)
	}
	return fmt.Sprintf("```diff\n%s\n```", strings.TrimSpace(patch))
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
		return renderGenericJSON(args)
	}
	var b strings.Builder
	b.WriteString("Todo List:\n")
	for _, raw := range itemsRaw {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		status := strings.TrimSpace(stringValue(item, "status"))
		desc := strings.TrimSpace(stringValue(item, "description", "content", "text"))
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "- [%s] %s\n", todoSymbol(status), desc)
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatExecuteInput(args map[string]any) string {
	command := stringValue(args, "command", "cmd")
	workdir := stringValue(args, "workdir", "dir", "cwd")
	if command == "" && workdir == "" {
		return renderGenericJSON(args)
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

func formatDiffBlock(oldText, newText string) string {
	var b strings.Builder
	b.WriteString("```diff\n")
	for _, line := range strings.Split(oldText, "\n") {
		if oldText == "" {
			break
		}
		b.WriteString("-")
		b.WriteString(line)
		b.WriteString("\n")
	}
	for _, line := range strings.Split(newText, "\n") {
		if newText == "" {
			break
		}
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("```")
	return b.String()
}

func formatContentBlock(content, path string) string {
	lang := languageFromPath(path)
	escaped := strings.ReplaceAll(content, "```", "\\```")
	if lang == "" {
		return fmt.Sprintf("```\n%s\n```", escaped)
	}
	return fmt.Sprintf("```%s\n%s\n```", lang, escaped)
}

func languageFromPath(path string) string {
	if path == "" {
		return ""
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if ext == "" {
		return ""
	}
	switch ext {
	case "yml":
		return "yaml"
	case "md":
		return "markdown"
	default:
		return ext
	}
}

// --- generic helpers (decoupled copy of droidcli's, scoped to this package) ---

func renderGenericJSON(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("```json\n{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n  \"")
		b.WriteString(k)
		b.WriteString("\": ")
		b.WriteString(renderJSONValue(args[k]))
	}
	b.WriteString("\n}\n```")
	return b.String()
}

func renderJSONValue(v any) string {
	switch val := v.(type) {
	case string:
		bytes, _ := json.Marshal(val)
		return string(bytes)
	case json.Number:
		return val.String()
	default:
		bytes, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%q", fmt.Sprint(val))
		}
		return string(bytes)
	}
}

func stringValue(args map[string]any, keys ...string) string {
	for _, key := range keys {
		val, ok := args[key]
		if !ok {
			continue
		}
		switch v := val.(type) {
		case string:
			return v
		case json.Number:
			return v.String()
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			return strconv.Itoa(v)
		case int64:
			return strconv.FormatInt(v, 10)
		case bool:
			return strconv.FormatBool(v)
		}
	}
	return ""
}

func normalizeToolName(name string) string {
	cleaned := strings.ToLower(strings.TrimSpace(name))
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, "_", "")
	return cleaned
}

func todoSymbol(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "done":
		return "x"
	case "in_progress", "active":
		return "⚡"
	default:
		return " "
	}
}
