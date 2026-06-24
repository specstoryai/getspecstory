package antigravitycli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// classifyToolType maps an Antigravity tool name to a SpecStory tool type.
// Unknown tools fall back to "generic" (not "unknown") so that tools we know the
// provider can emit still render sensibly.
func classifyToolType(name string) string {
	writeTools := map[string]bool{
		"write_to_file": true, "replace_file_content": true,
		"create_file": true, "edit_file": true, "apply_patch": true,
	}
	readTools := map[string]bool{
		"view_file": true, "read_file": true,
		"list_dir": true, "read_directory": true,
	}
	searchTools := map[string]bool{
		"grep_search": true, "codebase_search": true, "file_search": true,
		"find_files": true, "web_search": true, "read_url": true,
		"fetch_url": true, "execute_url": true,
	}
	shellTools := map[string]bool{
		"run_command": true, "run_terminal_command": true,
		"exec": true, "shell": true,
	}
	taskTools := map[string]bool{
		"update_plan": true, "todo_write": true, "create_plan": true,
		"task_list": true,
	}

	switch {
	case writeTools[name]:
		return schema.ToolTypeWrite
	case readTools[name]:
		return schema.ToolTypeRead
	case searchTools[name]:
		return schema.ToolTypeSearch
	case shellTools[name]:
		return schema.ToolTypeShell
	case taskTools[name]:
		return schema.ToolTypeTask
	default:
		return schema.ToolTypeGeneric
	}
}

// formatToolCall renders a ToolInfo into a markdown body the session renderer
// wraps in a <tool-use><details> block. It is called twice for tools whose
// result arrives in a later step: once at conversion (input only), then again
// from attachToolResult once the output is in place.
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
	case "runcommand", "runterminalcommand", "exec", "shell":
		return renderRunCommandInput(args)
	case "viewfile", "readfile":
		return renderReadInput(args)
	case "listdir", "readdirectory":
		return renderListInput(args)
	case "grepsearch":
		return renderGrepInput(args)
	case "codebasesearch", "filesearch", "findfiles":
		return renderFileSearchInput(args)
	case "websearch":
		return renderWebSearchInput(args)
	case "readurl", "fetchurl", "executeurl":
		return renderWebFetchInput(args)
	case "writetofile", "createfile":
		return renderWriteInput(args)
	case "replacefilecontent", "editfile":
		return renderEditInput(args)
	case "applypatch":
		return renderApplyPatch(args)
	case "updateplan", "todowrite", "createplan":
		return renderTodoWrite(args)
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
// downstream features (cloud sync, search) can index sessions by touched files.
func extractPathHints(input map[string]any, workspaceRoot string) []string {
	if input == nil {
		return nil
	}

	pathFields := []string{
		// Antigravity PascalCase keys.
		"AbsolutePath", "TargetFile", "Cwd", "DirectoryPath", "SearchPath",
		// Common variants, for forward-compatibility with unseen tools.
		"path", "file_path", "file", "dir", "directory", "cwd", "workdir",
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

	command := stringValue(input, "CommandLine", "command", "cmd")
	if command != "" {
		cwd := stringValue(input, "Cwd", "workdir", "cwd")
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
	value = stripFileURI(strings.TrimSpace(value))
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

func renderRunCommandInput(args map[string]any) string {
	command := stringValue(args, "CommandLine", "command", "cmd")
	workdir := stringValue(args, "Cwd", "workdir", "dir", "cwd")
	if command == "" && workdir == "" {
		return renderGenericJSON(args)
	}
	var b strings.Builder
	if workdir != "" {
		fmt.Fprintf(&b, "Directory: `%s`\n\n", stripFileURI(workdir))
	}
	if command != "" {
		fmt.Fprintf(&b, "`%s`", command)
	}
	return b.String()
}

func renderReadInput(args map[string]any) string {
	path := stringValue(args, "AbsolutePath", "file_path", "path", "file")
	if path == "" {
		return renderGenericJSON(args)
	}
	return fmt.Sprintf("Path: `%s`", stripFileURI(path))
}

func renderListInput(args map[string]any) string {
	path := stringValue(args, "DirectoryPath", "path", "dir", "directory")
	if path == "" {
		return renderGenericJSON(args)
	}
	return fmt.Sprintf("Path: `%s`", stripFileURI(path))
}

func renderGrepInput(args map[string]any) string {
	var parts []string
	if pat := stringValue(args, "Query", "pattern", "query", "regex"); pat != "" {
		parts = append(parts, fmt.Sprintf("Pattern: `%s`", pat))
	}
	if path := stringValue(args, "SearchPath", "path", "dir", "directory"); path != "" {
		parts = append(parts, fmt.Sprintf("Path: `%s`", stripFileURI(path)))
	}
	if len(parts) == 0 {
		return renderGenericJSON(args)
	}
	return strings.Join(parts, "\n")
}

func renderFileSearchInput(args map[string]any) string {
	if pat := stringValue(args, "Query", "pattern", "query", "name"); pat != "" {
		return fmt.Sprintf("Pattern: `%s`", pat)
	}
	return renderGenericJSON(args)
}

func renderWebSearchInput(args map[string]any) string {
	if q := stringValue(args, "Query", "query", "q", "search"); q != "" {
		return fmt.Sprintf("Query: `%s`", q)
	}
	return renderGenericJSON(args)
}

func renderWebFetchInput(args map[string]any) string {
	url := stringValue(args, "Url", "URL", "url", "uri")
	prompt := stringValue(args, "Prompt", "prompt", "query")
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
	path := stripFileURI(stringValue(args, "TargetFile", "file_path", "path", "file"))
	content := stringValue(args, "CodeContent", "content", "contents", "text", "data")
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
	path := stripFileURI(stringValue(args, "TargetFile", "file_path", "path", "file"))
	oldText := stringValue(args, "TargetContent", "old_str", "old_text", "old_string", "old")
	newText := stringValue(args, "ReplacementContent", "new_str", "new_text", "new_string", "new")

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
	itemsRaw, _ := args["todos"].([]any)
	if len(itemsRaw) == 0 {
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

func formatDiffBlock(oldText, newText string) string {
	var b strings.Builder
	b.WriteString("```diff\n")
	if oldText != "" {
		for _, line := range strings.Split(oldText, "\n") {
			b.WriteString("-")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if newText != "" {
		for _, line := range strings.Split(newText, "\n") {
			b.WriteString("+")
			b.WriteString(line)
			b.WriteString("\n")
		}
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

// --- generic helpers ---

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
