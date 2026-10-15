package cursorcloud

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// maxResultRunes caps a rendered tool result. Results (file contents, command
// output, fetched pages) can be arbitrarily large; the mirror and RawData keep
// the full value, so the markdown preview is capped with a visible marker.
const maxResultRunes = 50000

// plumbingInputKeys are identifiers the harness copies into tool arguments on
// every call (observed on run_terminal_cmd); they say nothing about the call.
var plumbingInputKeys = []string{"toolCallId", "conversationId", "requestId"}

// shellInputNoise are run_terminal_cmd arguments that restate the command
// (parsingResult, simpleCommands) or are harness execution settings identical
// on every observed call (timeouts, output thresholds, stdin handling).
var shellInputNoise = []string{
	"parsingResult", "simpleCommands", "timeout", "hardTimeout",
	"timeoutBehavior", "fileOutputThresholdBytes", "closeStdin",
}

// toolType classifies a tool by the name recorded in tool_call events. Every
// name here was observed in a captured stream; see testdata/tools.txt.
func toolType(name string) string {
	switch name {
	case "run_terminal_cmd":
		return schema.ToolTypeShell
	case "read_file", "web_fetch":
		return schema.ToolTypeRead
	case "edit_file", "delete_file":
		return schema.ToolTypeWrite
	case "file_search", "grep_search", "web_search":
		return schema.ToolTypeSearch
	case "todo_write":
		return schema.ToolTypeTask
	// Agent-state and harness tools: MCP plumbing, background waits, canvases,
	// generated assets.
	case "mcp", "get_mcp_tools", "await", "generate_image",
		"write_canvas", "read_canvas":
		return schema.ToolTypeGeneric
	default:
		return schema.ToolTypeUnknown
	}
}

// cleanToolInput returns a copy of the recorded args without plumbing keys.
// Shell noise stays in the stored Input (it is real data) and is only hidden
// from the rendered body.
func cleanToolInput(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	cleaned := make(map[string]any, len(args))
	for key, value := range args {
		if !slices.Contains(plumbingInputKeys, key) {
			cleaned[key] = value
		}
	}
	return cleaned
}

// formatTool renders a tool call. It sets tool.Summary and returns the body
// that becomes FormattedMarkdown: the rendered input, then the rendered result.
// The VM's /workspace prefix is shown relative so paths read like the repo.
func formatTool(tool *schema.ToolInfo) string {
	if tool == nil {
		return ""
	}
	if summary := toolSummary(tool); summary != "" {
		tool.Summary = &summary
	}

	body := strings.TrimSpace(formatToolInput(tool))
	result := strings.TrimSpace(formatToolResult(tool))

	var b strings.Builder
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
	}
	if result != "" {
		if body != "" {
			b.WriteString("\n\n")
		} else {
			b.WriteString("\n")
		}
		b.WriteString(result)
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	return b.String()
}

// toolSummary names the call and its key argument.
func toolSummary(tool *schema.ToolInfo) string {
	in := tool.Input
	var key string
	switch tool.Name {
	case "read_file", "edit_file", "delete_file":
		key = displayPath(spi.StringValue(in, "path"))
	case "run_terminal_cmd":
		key = spi.StringValue(in, "description")
	case "file_search":
		key = spi.StringValue(in, "globPattern")
	case "grep_search":
		key = spi.StringValue(in, "pattern")
	case "web_search":
		key = spi.StringValue(in, "searchTerm")
	case "web_fetch":
		key = spi.StringValue(in, "url")
	case "mcp":
		key = spi.StringValue(in, "name")
	case "get_mcp_tools":
		key = strings.Trim(spi.StringValue(in, "server")+"/"+spi.StringValue(in, "toolName"), "/")
	case "write_canvas":
		key = spi.StringValue(in, "title")
	case "generate_image":
		key = displayPath(spi.StringValue(in, "filePath"))
	}
	if key == "" {
		return ""
	}
	return fmt.Sprintf("Tool use: **%s** %s", tool.Name, spi.InlineCode(key))
}

// formatToolInput renders the arguments as labeled lines. Unenumerated tools
// fall back to pretty-printed JSON so no input is ever dropped.
func formatToolInput(tool *schema.ToolInfo) string {
	in := tool.Input
	switch tool.Name {
	case "run_terminal_cmd":
		return formatShellInput(in)
	case "read_file", "delete_file", "web_fetch", "web_search", "file_search":
		return formatLabeledInput(in, tool.Name)
	case "edit_file":
		return formatEditInput(tool)
	case "grep_search":
		return formatLabeledInput(in, tool.Name)
	case "todo_write":
		return formatTodos(in["todos"])
	case "mcp":
		return formatMCPInput(in)
	case "get_mcp_tools":
		return labeledLines(in, [][2]string{{"server", "Server"}, {"toolName", "Tool"}})
	case "await":
		return labeledLines(in, [][2]string{{"blockUntilMs", "Wait (ms)"}})
	case "generate_image":
		return labeledLines(in, [][2]string{{"description", "Description"}, {"aspectRatio", "Aspect ratio"}, {"filePath", "File"}})
	case "write_canvas":
		var b strings.Builder
		if title := spi.StringValue(in, "title"); title != "" {
			fmt.Fprintf(&b, "Title: %s\n\n", title)
		}
		if contents := spi.StringValue(in, "contents"); contents != "" {
			b.WriteString(spi.CodeFence("jsx", contents))
		}
		return b.String()
	case "read_canvas":
		return labeledLines(in, [][2]string{{"canvasId", "Canvas"}})
	default:
		return spi.RenderGenericJSON(in)
	}
}

// formatLabeledInput covers the tools whose summary already names the key
// argument; only the secondary arguments are worth a line.
func formatLabeledInput(in map[string]any, name string) string {
	switch name {
	case "file_search":
		return labeledLines(in, [][2]string{{"targetDirectory", "Directory"}})
	case "grep_search":
		var b strings.Builder
		b.WriteString(labeledLines(in, [][2]string{{"path", "Path"}, {"glob", "Glob"}}))
		var flags []string
		if v, _ := in["caseInsensitive"].(bool); v {
			flags = append(flags, "case-insensitive")
		}
		if v, _ := in["multiline"].(bool); v {
			flags = append(flags, "multiline")
		}
		if len(flags) > 0 {
			fmt.Fprintf(&b, "Options: %s\n", strings.Join(flags, ", "))
		}
		return b.String()
	default:
		// read_file, delete_file, web_fetch, web_search: the summary carries it.
		return ""
	}
}

// labeledLines renders the given keys, in order, as "Label: value" lines.
// Path-valued labels are shown relative to the VM workspace.
func labeledLines(in map[string]any, keys [][2]string) string {
	var b strings.Builder
	for _, kv := range keys {
		value := spi.StringValue(in, kv[0])
		if value == "" {
			continue
		}
		if kv[0] == "path" || kv[0] == "targetDirectory" || kv[0] == "filePath" {
			value = displayPath(value)
		}
		fmt.Fprintf(&b, "%s: %s\n", kv[1], spi.InlineCode(value))
	}
	return b.String()
}

// formatShellInput shows the description, the command, and the directory
// when it is not the workspace root every observed call uses.
func formatShellInput(in map[string]any) string {
	var b strings.Builder
	command := spi.StringValue(in, "command")
	if dir := spi.StringValue(in, "workingDirectory"); dir != "" && dir != vmWorkspaceRoot {
		fmt.Fprintf(&b, "Directory: %s\n\n", spi.InlineCode(displayPath(dir)))
	}
	if command != "" {
		b.WriteString(spi.CodeFence("bash", command))
	}
	// Anything beyond the known arguments is shown so a new argument is not lost.
	extra := spi.RenderGenericJSON(in, append([]string{"command", "workingDirectory", "description"}, shellInputNoise...)...)
	if extra != "" {
		b.WriteString("\n\n")
		b.WriteString(extra)
	}
	return b.String()
}

// formatEditInput prefers the diff the harness computed; while a call is still
// streaming (no result yet) the new content is the only record of the edit.
func formatEditInput(tool *schema.ToolInfo) string {
	if success := resultBranch(tool.Output, "success"); success != nil {
		if diff := spi.StringValue(success, "diffString"); diff != "" {
			return spi.CodeFence("diff", spi.CapRunes(diff, maxResultRunes))
		}
	}
	content := spi.StringValue(tool.Input, "streamContent")
	if content == "" {
		return ""
	}
	return spi.CodeFence(spi.LanguageFromPath(spi.StringValue(tool.Input, "path")), content)
}

// formatMCPInput names the server and tool; any further arguments render as JSON.
func formatMCPInput(in map[string]any) string {
	var b strings.Builder
	b.WriteString(labeledLines(in, [][2]string{{"serverIdentifier", "Server"}, {"toolName", "Tool"}}))
	if extra := spi.RenderGenericJSON(in, "name", "serverIdentifier", "providerIdentifier", "toolName"); extra != "" {
		b.WriteString("\n")
		b.WriteString(extra)
	}
	return b.String()
}

// todoStatus maps the TODO_STATUS_* enum recorded by todo_write onto the
// shared checkbox vocabulary.
func todoStatus(status string) string {
	return strings.ToLower(strings.TrimPrefix(status, "TODO_STATUS_"))
}

// formatTodos renders a todo list; a malformed item is skipped.
func formatTodos(raw any) string {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Todo List:\n")
	for _, item := range items {
		todo, ok := item.(map[string]any)
		if !ok {
			continue
		}
		content := strings.TrimSpace(spi.StringValue(todo, "content"))
		if content == "" {
			continue
		}
		fmt.Fprintf(&b, "- [%s] %s\n", spi.TodoSymbol(todoStatus(spi.StringValue(todo, "status"))), content)
	}
	return b.String()
}

// formatToolResult renders the result. Errors take priority for every tool;
// a shell command that exited non-zero records a "failure" branch instead.
func formatToolResult(tool *schema.ToolInfo) string {
	if tool.Output == nil {
		return ""
	}
	if errText := toolErrorText(tool.Output); errText != "" {
		return resultBlock("Error", errText)
	}

	if tool.Name == "run_terminal_cmd" {
		return formatShellResult(tool.Output)
	}

	success := resultBranch(tool.Output, "success")
	if success == nil {
		return formatGenericResult(tool.Output)
	}
	switch tool.Name {
	case "read_file":
		return formatReadResult(tool.Input, success)
	case "edit_file", "todo_write":
		// The body already shows the diff or the list.
		return ""
	case "delete_file":
		prev := spi.StringValue(success, "prevContent")
		if prev == "" {
			return "Result: deleted"
		}
		return "Result: deleted\n\n" + spi.FormatDiffBlock(spi.CapRunes(prev, maxResultRunes), "")
	case "file_search":
		return formatFileSearchResult(success)
	case "grep_search":
		return formatGrepResult(success)
	case "web_search":
		return formatWebSearchResult(success)
	case "web_fetch":
		if md := spi.StringValue(success, "markdown"); md != "" {
			return "Result:\n" + spi.CodeFence("markdown", spi.CapRunes(md, maxResultRunes))
		}
		return ""
	case "mcp":
		return formatMCPResult(success)
	case "get_mcp_tools":
		return formatMCPToolsResult(success)
	case "await":
		if complete, ok := success["complete"].(map[string]any); ok {
			if ms := spi.StringValue(complete, "runtimeMs"); ms != "" {
				return fmt.Sprintf("Result: completed after %s ms", ms)
			}
		}
		return formatGenericResult(tool.Output)
	case "generate_image":
		if path := spi.StringValue(success, "filePath"); path != "" {
			return "Result: saved to " + spi.InlineCode(path)
		}
		return ""
	case "write_canvas", "read_canvas":
		return formatCanvasResult(success)
	default:
		return formatGenericResult(tool.Output)
	}
}

// toolErrorText extracts a failed call's message. Observed shapes:
// {"error":{"error":"..."}} (delete_file, grep_search, web_fetch) and
// {"error":{"errorMessage":"..."}} (read_file). An error object with neither
// key is shown as JSON rather than dropped.
func toolErrorText(output map[string]any) string {
	errObj, ok := output["error"]
	if !ok {
		return ""
	}
	m, ok := errObj.(map[string]any)
	if !ok {
		if s, isString := errObj.(string); isString {
			return s
		}
		return compactJSON(errObj)
	}
	if text := spi.StringValue(m, "error", "errorMessage", "message"); text != "" {
		return text
	}
	return compactJSON(m)
}

// formatShellResult renders command output. A non-zero exit is recorded as a
// "failure" branch with exitCode and stderr; success carries stdout. The
// interleaved stream is preferred because it keeps stdout and stderr in the
// order the terminal showed them.
func formatShellResult(output map[string]any) string {
	var b strings.Builder
	branch := resultBranch(output, "success")
	if failure := resultBranch(output, "failure"); failure != nil {
		branch = failure
		fmt.Fprintf(&b, "Exit code: %s\n\n", spi.StringValue(failure, "exitCode"))
	}
	if branch == nil {
		return formatGenericResult(output)
	}
	if background, _ := output["isBackground"].(bool); background {
		b.WriteString("Running in the background.\n\n")
	}
	text := spi.StringValue(branch, "interleavedOutput")
	if text == "" {
		text = spi.StringValue(branch, "stdout") + spi.StringValue(branch, "stderr")
	}
	text = strings.Trim(spi.SanitizeShellOutput(text), "\n")
	if text == "" {
		b.WriteString("Result: (no output)")
		return b.String()
	}
	b.WriteString("Result:\n")
	b.WriteString(spi.CodeFence("text", spi.CapRunes(text, maxResultRunes)))
	return b.String()
}

// formatReadResult fences the file content by the file's language and notes
// when only part of the file was read.
func formatReadResult(in, success map[string]any) string {
	content := spi.StringValue(success, "content")
	if content == "" {
		return "Result: (empty file)"
	}
	var b strings.Builder
	if rng, ok := success["readRange"].(map[string]any); ok {
		start, end := spi.StringValue(rng, "startLine"), spi.StringValue(rng, "endLine")
		total := spi.StringValue(success, "totalLines")
		if start != "" && end != "" && (start != "1" || end != total) {
			fmt.Fprintf(&b, "Lines %s–%s of %s\n\n", start, end, total)
		}
	}
	path := spi.StringValue(in, "path")
	b.WriteString(spi.CodeFence(spi.LanguageFromPath(path), spi.CapRunes(strings.TrimRight(content, "\n"), maxResultRunes)))
	return b.String()
}

// formatFileSearchResult lists matched files.
func formatFileSearchResult(success map[string]any) string {
	files, _ := success["files"].([]any)
	if len(files) == 0 {
		return "Result: no files found"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Result: %d %s\n", len(files), plural(len(files), "file", "files"))
	for _, f := range files {
		if name, ok := f.(string); ok {
			fmt.Fprintf(&b, "- %s\n", spi.InlineCode(name))
		}
	}
	return b.String()
}

// formatGrepResult flattens workspaceResults into file:line: text lines. The
// map is keyed by workspace root; roots are sorted so output is deterministic.
func formatGrepResult(success map[string]any) string {
	roots, _ := success["workspaceResults"].(map[string]any)
	var lines []string
	for _, root := range sortedKeys(roots) {
		rootResult, _ := roots[root].(map[string]any)
		content, _ := rootResult["content"].(map[string]any)
		files, _ := content["matches"].([]any)
		for _, f := range files {
			file, ok := f.(map[string]any)
			if !ok {
				continue
			}
			name := spi.StringValue(file, "file")
			matches, _ := file["matches"].([]any)
			for _, m := range matches {
				match, ok := m.(map[string]any)
				if !ok {
					continue
				}
				lines = append(lines, fmt.Sprintf("%s:%s: %s", name, spi.StringValue(match, "lineNumber"), spi.StringValue(match, "content")))
			}
		}
	}
	if len(lines) == 0 {
		if roots == nil {
			return formatGenericResult(map[string]any{"success": success})
		}
		return "Result: no matches"
	}
	return "Result:\n" + spi.CodeFence("text", spi.CapRunes(strings.Join(lines, "\n"), maxResultRunes))
}

// formatWebSearchResult renders each reference. The chunk is already a
// markdown link list followed by a synthesis paragraph, so it is shown as is.
func formatWebSearchResult(success map[string]any) string {
	refs, _ := success["references"].([]any)
	var b strings.Builder
	for _, r := range refs {
		ref, ok := r.(map[string]any)
		if !ok {
			continue
		}
		chunk := strings.TrimSpace(spi.StringValue(ref, "chunk"))
		if chunk == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		// The chunk's trailing "Highlights:" section quotes page excerpts in
		// <result><title><url><content> tags, which markdown would read as
		// HTML; it is fenced so the excerpts survive as text.
		links, highlights, found := strings.Cut(chunk, "\n\nHighlights:\n")
		b.WriteString(spi.CapRunes(links, maxResultRunes))
		if found && strings.TrimSpace(highlights) != "" {
			b.WriteString("\n\nHighlights:\n")
			b.WriteString(spi.CodeFence("text", spi.CapRunes(strings.TrimSpace(highlights), maxResultRunes)))
		}
	}
	if b.Len() == 0 {
		return formatGenericResult(map[string]any{"success": success})
	}
	return "Result:\n\n" + b.String()
}

// formatMCPResult shows the text content parts. structuredContent repeats the
// text in every observed result, so it is shown only when there is no text.
func formatMCPResult(success map[string]any) string {
	parts, _ := success["content"].([]any)
	var texts []string
	for _, p := range parts {
		part, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if textObj, ok := part["text"].(map[string]any); ok {
			if text := spi.StringValue(textObj, "text"); text != "" {
				texts = append(texts, text)
			}
		}
	}
	if len(texts) == 0 {
		return formatGenericResult(map[string]any{"success": success})
	}
	return "Result:\n" + spi.CodeFence("text", spi.CapRunes(strings.Join(texts, "\n\n"), maxResultRunes))
}

// formatMCPToolsResult shows a tool schema lookup. The content is a JSON
// string (a schema, or a note that output went to a file).
func formatMCPToolsResult(success map[string]any) string {
	content := spi.StringValue(success, "content")
	var b strings.Builder
	if path := spi.StringValue(success, "outputFilePath"); path != "" {
		fmt.Fprintf(&b, "Output file: %s\n\n", spi.InlineCode(path))
	}
	if content != "" {
		lang := "text"
		if json.Valid([]byte(content)) {
			lang = "json"
		}
		b.WriteString("Result:\n")
		b.WriteString(spi.CodeFence(lang, spi.CapRunes(content, maxResultRunes)))
	}
	return b.String()
}

// formatCanvasResult links the canvas and shows its source when returned.
func formatCanvasResult(success map[string]any) string {
	var b strings.Builder
	title := spi.StringValue(success, "title")
	if url := spi.StringValue(success, "url"); url != "" {
		if title == "" {
			title = url
		}
		fmt.Fprintf(&b, "Result: [%s](%s)\n", markdownLinkTextEscaper.Replace(title), url)
	}
	if source := spi.StringValue(success, "source"); source != "" {
		b.WriteString("\n")
		b.WriteString(spi.CodeFence("jsx", spi.CapRunes(source, maxResultRunes)))
	}
	return b.String()
}

// formatGenericResult is the fallback for an unfamiliar result shape.
func formatGenericResult(output map[string]any) string {
	rendered := spi.RenderGenericJSON(output)
	if rendered == "" {
		return ""
	}
	return "Result:\n" + rendered
}

// resultBlock renders a labeled one-line or fenced multi-line result.
func resultBlock(label, text string) string {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "\n") {
		return label + ":\n" + spi.CodeFence("text", spi.CapRunes(text, maxResultRunes))
	}
	return label + ": " + text
}

// resultBranch returns output[key] as an object, or nil.
func resultBranch(output map[string]any, key string) map[string]any {
	branch, _ := output[key].(map[string]any)
	return branch
}

// markdownLinkTextEscaper neutralizes the characters that would end a link's
// text early.
var markdownLinkTextEscaper = strings.NewReplacer("[", `\[`, "]", `\]`)

func compactJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
