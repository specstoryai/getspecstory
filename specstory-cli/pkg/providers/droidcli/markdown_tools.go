package droidcli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
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
	switch normalizeToolName(tool.Name) {
	case "execute", "run", "bash", "shell", "command", "exec":
		return formatExecuteInput(args)
	case "read", "cat", "readfile", "view", "viewimage":
		return renderReadInput(args)
	case "ls", "list", "listdir", "listdirectory":
		return renderListInput(args)
	case "glob":
		return renderGlobInput(args)
	case "grep", "rg", "ripgrep", "search", "filesearch":
		return renderGrepInput(args)
	case "websearch", "searchweb", "googlewebsearch":
		return renderWebSearchInput(args)
	case "fetchurl", "fetch", "webfetch":
		return renderWebFetchInput(args)
	case "applypatch":
		return renderApplyPatch(args)
	case "write", "create", "writefile", "createfile", "addfile":
		return renderWriteInput(args)
	case "edit", "multiedit", "replace", "strreplace", "applyedit":
		return renderEditInput(args)
	case "todowrite":
		return renderTodoWrite(args)
	case "askuser", "askuserquestion":
		return renderAskUserInput(args, tool.Result)
	default:
		return renderGenericJSON(args)
	}
}

func formatToolOutput(tool *fdToolCall) string {
	if tool.Result == nil {
		return ""
	}
	// AskUser output is already included in the input rendering
	name := normalizeToolName(tool.Name)
	if name == "askuser" || name == "askuserquestion" {
		return ""
	}
	content := strings.TrimSpace(tool.Result.Content)
	if content == "" {
		return ""
	}
	// Edit tool returns JSON with diffLines - convert to proper diff format
	if name == "edit" {
		if diff := formatEditDiffOutput(content); diff != "" {
			return diff
		}
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

// formatEditDiffOutput parses the Edit tool's JSON diff output and formats it as a diff block.
func formatEditDiffOutput(content string) string {
	if !strings.HasPrefix(content, "{") {
		return ""
	}
	var result struct {
		DiffLines []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"diffLines"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return ""
	}
	if len(result.DiffLines) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("```diff\n")
	for _, line := range result.DiffLines {
		switch line.Type {
		case "added":
			builder.WriteString("+")
			builder.WriteString(line.Content)
			builder.WriteString("\n")
		case "removed":
			builder.WriteString("-")
			builder.WriteString(line.Content)
			builder.WriteString("\n")
		case "unchanged":
			builder.WriteString(" ")
			builder.WriteString(line.Content)
			builder.WriteString("\n")
		}
	}
	builder.WriteString("```")
	return builder.String()
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

func normalizeToolName(name string) string {
	cleaned := strings.ToLower(strings.TrimSpace(name))
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, "_", "")
	return cleaned
}

// fieldSpec describes how to extract and format a single field from tool input.
type fieldSpec struct {
	keys    []string // Keys to try for extraction (first match wins)
	label   string   // Display label (e.g., "Path", "Query")
	isSlice bool     // If true, extract as slice and format with formatQuotedList
}

// renderFields extracts values from args using specs and formats them as labeled lines.
// Falls back to renderGenericJSON if no fields are found.
func renderFields(args map[string]any, specs []fieldSpec) string {
	var parts []string
	for _, spec := range specs {
		if spec.isSlice {
			values := stringSliceValue(args, spec.keys...)
			if len(values) > 0 {
				parts = append(parts, fmt.Sprintf("%s: %s", spec.label, formatQuotedList(values)))
			}
		} else {
			value := stringValue(args, spec.keys...)
			if value != "" {
				parts = append(parts, fmt.Sprintf("%s: `%s`", spec.label, value))
			}
		}
	}
	if len(parts) == 0 {
		return renderGenericJSON(args)
	}
	return strings.Join(parts, "\n")
}

func renderReadInput(args map[string]any) string {
	path := stringValue(args, "file_path", "path", "file", "filePath")
	if path == "" {
		paths := stringSliceValue(args, "file_paths", "paths")
		if len(paths) > 0 {
			path = strings.Join(paths, ", ")
		}
	}
	if path == "" {
		return renderGenericJSON(args)
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Path: `%s`", path)
	offset := stringValue(args, "offset", "start")
	limit := stringValue(args, "limit", "lines", "max_lines")
	if offset != "" || limit != "" {
		if offset == "" {
			offset = "0"
		}
		if limit == "" {
			limit = "?"
		}
		fmt.Fprintf(&builder, "\nLines: offset %s, limit %s", offset, limit)
	}
	return builder.String()
}

func renderListInput(args map[string]any) string {
	return renderFields(args, []fieldSpec{
		{keys: []string{"path", "dir", "directory", "directory_path", "target_directory"}, label: "Path"},
	})
}

func renderGlobInput(args map[string]any) string {
	patterns := stringSliceValue(args, "patterns", "glob_patterns")
	if len(patterns) == 0 {
		if pattern := stringValue(args, "glob_pattern", "pattern", "glob"); pattern != "" {
			patterns = []string{pattern}
		}
	}
	path := stringValue(args, "path", "dir", "directory")
	var parts []string
	if len(patterns) > 0 {
		label := "Pattern"
		if len(patterns) > 1 {
			label = "Patterns"
		}
		parts = append(parts, fmt.Sprintf("%s: %s", label, formatQuotedList(patterns)))
	}
	if path != "" {
		parts = append(parts, fmt.Sprintf("Path: `%s`", path))
	}
	if len(parts) == 0 {
		return renderGenericJSON(args)
	}
	return strings.Join(parts, "\n")
}

func renderGrepInput(args map[string]any) string {
	return renderFields(args, []fieldSpec{
		{keys: []string{"pattern", "query", "search", "regex"}, label: "Pattern"},
		{keys: []string{"path", "dir", "directory", "cwd"}, label: "Path"},
		{keys: []string{"include", "glob", "glob_pattern"}, label: "Glob"},
	})
}

func renderWebSearchInput(args map[string]any) string {
	return renderFields(args, []fieldSpec{
		{keys: []string{"query", "q", "search", "prompt"}, label: "Query"},
		{keys: []string{"category"}, label: "Category"},
		{keys: []string{"type"}, label: "Type"},
		{keys: []string{"numResults", "num_results", "limit"}, label: "Results"},
		{keys: []string{"includeDomains", "include_domains"}, label: "Include domains", isSlice: true},
		{keys: []string{"excludeDomains", "exclude_domains"}, label: "Exclude domains", isSlice: true},
	})
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
	var builder strings.Builder
	if path != "" {
		fmt.Fprintf(&builder, "Path: `%s`", path)
	}
	if content != "" {
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(formatContentBlock(content, path))
	}
	return builder.String()
}

func renderEditInput(args map[string]any) string {
	path := stringValue(args, "file_path", "path", "file", "filePath")
	diff := renderEdits(args)
	if path == "" && diff == "" {
		return renderGenericJSON(args)
	}
	var builder strings.Builder
	if path != "" {
		fmt.Fprintf(&builder, "Path: `%s`", path)
	}
	if diff != "" {
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(diff)
	}
	return builder.String()
}

func renderEdits(args map[string]any) string {
	if diff := renderSingleEdit(args); diff != "" {
		return diff
	}
	editsRaw, ok := args["edits"].([]interface{})
	if !ok || len(editsRaw) == 0 {
		return ""
	}
	var sections []string
	for _, raw := range editsRaw {
		edit, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if diff := renderSingleEdit(edit); diff != "" {
			sections = append(sections, diff)
		}
	}
	return strings.Join(sections, "\n\n")
}

func renderSingleEdit(args map[string]any) string {
	oldText := stringValue(args, "old_str", "old_text", "old_string", "old")
	newText := stringValue(args, "new_str", "new_text", "new_string", "new")
	if oldText == "" && newText == "" {
		return ""
	}
	return formatDiffBlock(oldText, newText)
}

func formatDiffBlock(oldText string, newText string) string {
	var builder strings.Builder
	builder.WriteString("```diff\n")
	if oldText != "" {
		for _, line := range strings.Split(oldText, "\n") {
			builder.WriteString("-")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}
	if newText != "" {
		for _, line := range strings.Split(newText, "\n") {
			builder.WriteString("+")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}
	builder.WriteString("```")
	return builder.String()
}

func renderApplyPatch(args map[string]any) string {
	patch := stringValue(args, "patch", "input")
	if patch == "" {
		return renderGenericJSON(args)
	}
	return fmt.Sprintf("```diff\n%s\n```", strings.TrimSpace(patch))
}

func renderTodoWrite(args map[string]any) string {
	if todoText, ok := args["todos"].(string); ok {
		trimmed := strings.TrimSpace(todoText)
		if trimmed == "" {
			return "Todo list unchanged"
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "todo") {
			return trimmed
		}
		return "Todo List:\n" + trimmed
	}
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

// renderAskUserInput formats the AskUser tool input with questions, options, and answers.
// It parses the questionnaire format and extracts answers from the tool result.
func renderAskUserInput(args map[string]any, result *fdToolResult) string {
	questionnaire := stringValue(args, "questionnaire", "questions")
	if questionnaire == "" {
		return renderGenericJSON(args)
	}

	// Parse the questionnaire format to extract questions and options
	questions := parseQuestionnaire(questionnaire)
	if len(questions) == 0 {
		return renderGenericJSON(args)
	}

	// Parse answers from the result if available
	var answers []string
	if result != nil && result.Content != "" {
		answers = parseAskUserAnswers(result.Content)
	}

	// Build the formatted output
	var builder strings.Builder
	for i, q := range questions {
		if i > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(fmt.Sprintf("**%s**\n\nOptions:\n", q.question))
		for _, opt := range q.options {
			builder.WriteString(fmt.Sprintf("- %s\n", opt))
		}
	}

	// Add the answers at the end if available
	if len(answers) > 0 {
		builder.WriteString("\n**Answer:** ")
		builder.WriteString(strings.Join(answers, ", "))
	}

	return strings.TrimSpace(builder.String())
}

// askUserQuestion represents a parsed question with its options
type askUserQuestion struct {
	question string
	options  []string
}

// parseQuestionnaire parses the Factory Droid questionnaire format.
// Format: "1. [question] Text\n[topic] Topic\n[option] Opt1\n[option] Opt2\n\n2. [question] ..."
func parseQuestionnaire(text string) []askUserQuestion {
	var questions []askUserQuestion
	var current *askUserQuestion

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check for question line (starts with number or contains [question])
		if strings.Contains(line, "[question]") {
			if current != nil && current.question != "" {
				questions = append(questions, *current)
			}
			// Extract question text after [question]
			idx := strings.Index(line, "[question]")
			questionText := strings.TrimSpace(line[idx+len("[question]"):])
			current = &askUserQuestion{question: questionText}
		} else if strings.HasPrefix(line, "[option]") {
			if current != nil {
				opt := strings.TrimSpace(strings.TrimPrefix(line, "[option]"))
				if opt != "" {
					current.options = append(current.options, opt)
				}
			}
		}
		// Skip [topic] lines as they're not needed in the output
	}

	// Don't forget the last question
	if current != nil && current.question != "" {
		questions = append(questions, *current)
	}

	return questions
}

// parseAskUserAnswers extracts answers from the Factory Droid AskUser result format.
// Format: "1. [question] Text\n[answer] Answer1\n\n2. [question] Text\n[answer] Answer2"
func parseAskUserAnswers(text string) []string {
	var answers []string
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[answer]") {
			answer := strings.TrimSpace(strings.TrimPrefix(line, "[answer]"))
			if answer != "" {
				answers = append(answers, answer)
			}
		}
	}
	return answers
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
			case float64:
				return strconv.FormatFloat(v, 'f', -1, 64)
			case float32:
				return strconv.FormatFloat(float64(v), 'f', -1, 32)
			case int:
				return strconv.Itoa(v)
			case int64:
				return strconv.FormatInt(v, 10)
			case int32:
				return strconv.FormatInt(int64(v), 10)
			case uint:
				return strconv.FormatUint(uint64(v), 10)
			case uint64:
				return strconv.FormatUint(v, 10)
			case bool:
				return strconv.FormatBool(v)
			}
		}
	}
	return ""
}

func stringSliceValue(args map[string]any, keys ...string) []string {
	for _, key := range keys {
		if val, ok := args[key]; ok {
			switch v := val.(type) {
			case []string:
				return filterStrings(v)
			case []interface{}:
				values := make([]string, 0, len(v))
				for _, entry := range v {
					if entry == nil {
						continue
					}
					switch item := entry.(type) {
					case string:
						if strings.TrimSpace(item) != "" {
							values = append(values, item)
						}
					default:
						values = append(values, fmt.Sprint(item))
					}
				}
				return filterStrings(values)
			case string:
				if strings.TrimSpace(v) != "" {
					return []string{v}
				}
			}
		}
	}
	return nil
}

func filterStrings(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return filtered
}

func formatQuotedList(values []string) string {
	if len(values) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("`%s`", value))
	}
	return strings.Join(quoted, ", ")
}

func formatContentBlock(content string, path string) string {
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

// toolType returns the tool category for a Droid tool.
// Droid has 14 tools: Read, LS, Execute, Edit, Grep, Glob, Create,
// ExitSpecMode, AskUser, WebSearch, TodoWrite, FetchUrl, GenerateDroid, Skill
func toolType(name string) string {
	switch normalizeToolName(name) {
	case "execute":
		return "shell"
	case "read", "fetchurl":
		return "read"
	case "ls", "glob", "grep", "websearch":
		return "search"
	case "edit", "create":
		return "write"
	case "todowrite":
		return "task"
	default:
		// ExitSpecMode, AskUser, GenerateDroid, Skill are generic
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
