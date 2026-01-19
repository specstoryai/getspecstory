package droidcli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func formatToolCall(tool *fdToolCall) string {
	if tool == nil {
		return ""
	}
	body := strings.TrimSpace(formatToolInput(tool))
	result := strings.TrimSpace(formatToolOutput(tool))

	var builder []string
	if body != "" {
		builder = append(builder, body)
	}
	if result != "" {
		builder = append(builder, result)
	}
	return strings.TrimSpace(strings.Join(builder, "\n\n"))
}

func formatToolInput(tool *fdToolCall) string {
	args := decodeInput(tool.Input)
	switch strings.ToLower(tool.Name) {
	case "execute", "run", "bash", "shell":
		return formatExecuteInput(args)
	case "read", "cat":
		return renderPathsList(args, "file_path", "path", "file", "filePath")
	case "ls", "glob":
		return renderPathsList(args, "path", "dir", "directory", "glob")
	case "applypatch", "apply_patch":
		return renderApplyPatch(args)
	case "todowrite", "todo_write":
		return renderTodoWrite(args)
	default:
		return renderGenericJSON(args)
	}
}

func formatToolOutput(tool *fdToolCall) string {
	if tool.Result == nil {
		return ""
	}
	content := strings.TrimSpace(tool.Result.Content)
	if content == "" {
		return ""
	}
	label := "Output"
	if tool.Result.IsError {
		label = "Output (error)"
	}
	if strings.Contains(content, "\n") {
		return fmt.Sprintf("%s:\n```text\n%s\n```", label, content)
	}
	return fmt.Sprintf("%s: %s", label, content)
}

func decodeInput(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return map[string]any{"raw": string(raw)}
	}
	return payload
}

func formatExecuteInput(args map[string]any) string {
	command := stringValue(args, "command", "cmd")
	workdir := stringValue(args, "workdir", "dir", "cwd")
	if command == "" && workdir == "" {
		return ""
	}
	var builder strings.Builder
	if workdir != "" {
		fmt.Fprintf(&builder, "Directory: `%s`\n\n", workdir)
	}
	if command != "" {
		fmt.Fprintf(&builder, "`%s`", command)
	}
	return builder.String()
}

func renderPathsList(args map[string]any, keys ...string) string {
	path := stringValue(args, keys...)
	if path == "" {
		return renderGenericJSON(args)
	}
	return fmt.Sprintf("Path: `%s`", path)
}

func renderApplyPatch(args map[string]any) string {
	patch := stringValue(args, "patch", "input")
	if patch == "" {
		return renderGenericJSON(args)
	}
	return fmt.Sprintf("```diff\n%s\n```", strings.TrimSpace(patch))
}

func renderTodoWrite(args map[string]any) string {
	itemsRaw, ok := args["todos"].([]interface{})
	if !ok || len(itemsRaw) == 0 {
		return "Todo list unchanged"
	}
	var builder strings.Builder
	builder.WriteString("Todo List:\n")
	for _, raw := range itemsRaw {
		item, _ := raw.(map[string]interface{})
		status := strings.TrimSpace(stringValue(item, "status"))
		desc := strings.TrimSpace(stringValue(item, "description"))
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&builder, "- [%s] %s\n", todoSymbol(status), desc)
	}
	return builder.String()
}

func renderGenericJSON(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	ordered := make([]string, 0, len(args))
	for key := range args {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	var builder strings.Builder
	builder.WriteString("```json\n{")
	for idx, key := range ordered {
		if idx > 0 {
			builder.WriteString(",")
		}
		builder.WriteString("\n  \"")
		builder.WriteString(key)
		builder.WriteString("\": ")
		builder.WriteString(renderJSONValue(args[key]))
	}
	builder.WriteString("\n}\n```")
	return builder.String()
}

func renderJSONValue(v any) string {
	switch val := v.(type) {
	case string:
		bytes, _ := json.Marshal(val)
		return string(bytes)
	case json.Number:
		return val.String()
	case []byte:
		bytes, _ := json.Marshal(string(val))
		return string(bytes)
	default:
		bytes, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("\"%s\"", fmt.Sprint(val))
		}
		return string(bytes)
	}
}

func stringValue(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if val, ok := args[key]; ok {
			switch v := val.(type) {
			case string:
				return v
			case json.Number:
				return v.String()
			case fmt.Stringer:
				return v.String()
			case []byte:
				return string(v)
			}
		}
	}
	return ""
}

func todoSymbol(status string) string {
	s := strings.ToLower(status)
	switch s {
	case "completed", "done":
		return "x"
	case "in_progress", "active":
		return "⚡"
	default:
		return " "
	}
}

func toolType(name string) string {
	switch strings.ToLower(name) {
	case "execute", "run", "bash", "shell":
		return "shell"
	case "read", "cat":
		return "read"
	case "ls", "glob":
		return "search"
	case "applypatch", "apply_patch":
		return "write"
	case "todowrite", "todo_write":
		return "task"
	default:
		return "generic"
	}
}

func formatTodoUpdate(todo *fdTodoState) string {
	if todo == nil || len(todo.Items) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Todo Update:\n")
	for _, item := range todo.Items {
		desc := strings.TrimSpace(item.Description)
		if desc == "" {
			desc = "(no description)"
		}
		status := strings.ToLower(strings.TrimSpace(item.Status))
		fmt.Fprintf(&builder, "- [%s] %s (%s)\n", todoSymbol(item.Status), desc, status)
	}
	return strings.TrimRight(builder.String(), "\n")
}
