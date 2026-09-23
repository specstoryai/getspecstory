package opencode

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// maxResultRunes caps a rendered tool result. Results are previews of what the
// tool returned (file bodies, fetched pages, skill documents); the full text
// stays in RawData.
const maxResultRunes = 20000

// Tool names as OpenCode 2.0.14 records them (see tools.txt in examples/).
// They are compared after spi.NormalizeToolName.
const (
	toolRead      = "read"
	toolWrite     = "write"
	toolEdit      = "edit"
	toolGlob      = "glob"
	toolGrep      = "grep"
	toolShell     = "shell"
	toolWebFetch  = "webfetch"
	toolWebSearch = "websearch"
	toolSkill     = "skill"
	toolSubagent  = "subagent"
	toolExecute   = "execute"
	toolQuestion  = "question"
)

// Statuses of a tool call that has not finished yet.
const (
	statusRunning   = "running"
	statusStreaming = "streaming"
)

// exitNoticePattern matches the exit line OpenCode appends to shell results
// for the model; the exit code is rendered from metadata instead.
var exitNoticePattern = regexp.MustCompile(`^Command exited with code -?\d+\.$`)

// toolType classifies a tool by what it acts on. The execute tool runs model
// written code against OpenCode's tool catalog, so it has no single target.
func toolType(name string) string {
	switch spi.NormalizeToolName(name) {
	case toolRead, toolWebFetch:
		return schema.ToolTypeRead
	case toolWrite, toolEdit:
		return schema.ToolTypeWrite
	case toolGlob, toolGrep, toolWebSearch:
		return schema.ToolTypeSearch
	case toolShell:
		return schema.ToolTypeShell
	case toolSkill, toolSubagent, toolExecute, toolQuestion:
		return schema.ToolTypeGeneric
	default:
		return schema.ToolTypeUnknown
	}
}

// formatToolAsMarkdown renders a tool call's body and result (the <tool-use>
// wrapper and <summary> tag are added by pkg/session) and sets a summary that
// names the call's main argument.
func formatToolAsMarkdown(tool *schema.ToolInfo) string {
	if tool == nil {
		return ""
	}
	name := spi.NormalizeToolName(tool.Name)
	if summary := toolSummary(name, tool); summary != "" {
		tool.Summary = &summary
	}

	body := strings.TrimSpace(formatToolBody(name, tool.Input, tool.Output))
	result := strings.TrimSpace(formatToolResult(name, tool.Input, tool.Output))

	var sections []string
	if body != "" {
		sections = append(sections, body)
	}
	if result != "" {
		sections = append(sections, result)
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n" + strings.Join(sections, "\n\n") + "\n"
}

// toolSummary names the tool and its main argument; "" keeps the default
// "Tool use: **name**" summary.
func toolSummary(name string, tool *schema.ToolInfo) string {
	var argument string
	switch name {
	case toolRead, toolWrite, toolEdit:
		argument = spi.StringValue(tool.Input, "path")
	case toolGlob, toolGrep:
		argument = spi.StringValue(tool.Input, "pattern")
	case toolWebFetch:
		argument = spi.StringValue(tool.Input, "url")
	case toolWebSearch:
		argument = spi.StringValue(tool.Input, "query")
	case toolSkill:
		argument = spi.StringValue(tool.Input, "id")
	case toolSubagent:
		argument = spi.StringValue(tool.Input, "description")
	}
	argument = strings.TrimSpace(argument)
	if argument == "" {
		return ""
	}
	return fmt.Sprintf("Tool use: **%s** %s", tool.Name, inlineCode(argument))
}

// inlineCode wraps text in a backtick span long enough to contain any
// backticks inside it, keeping it on one line.
func inlineCode(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	fence := "`"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") {
		return fence + " " + text + " " + fence
	}
	return fence + text + fence
}

// formatToolBody renders a call's arguments as labeled lines.
func formatToolBody(name string, input, output map[string]any) string {
	if partial := spi.StringValue(input, "partialInput"); partial != "" {
		return "Arguments (still streaming):\n\n" + spi.CodeFence("text", partial)
	}

	switch name {
	case toolRead:
		return labeledLines(input, "offset", "Offset", "limit", "Limit")
	case toolWrite:
		return formatWriteBody(input)
	case toolEdit:
		return formatEditBody(input, output)
	case toolGlob:
		return labeledLines(input, "path", "Path")
	case toolGrep:
		return labeledLines(input, "path", "Path", "include", "Include")
	case toolShell:
		return formatShellBody(input)
	case toolWebFetch:
		return labeledLines(input, "format", "Format")
	case toolWebSearch, toolSkill:
		// The summary already shows the only argument.
		return ""
	case toolSubagent:
		return formatSubagentBody(input)
	case toolExecute:
		if code := spi.StringValue(input, "code"); code != "" {
			// Code Mode runs JavaScript against OpenCode's tool catalog.
			return spi.CodeFence("javascript", code)
		}
		return ""
	case toolQuestion:
		if body := formatQuestionBody(input); body != "" {
			return body
		}
		return spi.RenderGenericJSON(input)
	default:
		return spi.RenderGenericJSON(input)
	}
}

// formatQuestionBody lists each question the agent asked the user with its
// offered options. A malformed question or option is skipped.
func formatQuestionBody(input map[string]any) string {
	questions, _ := input["questions"].([]any)
	var sections []string
	for _, entry := range questions {
		question, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		text := strings.TrimSpace(spi.StringValue(question, "question"))
		if text == "" {
			continue
		}
		lines := []string{"**" + text + "**"}
		options, _ := question["options"].([]any)
		for _, rawOption := range options {
			option, ok := rawOption.(map[string]any)
			if !ok {
				continue
			}
			label := strings.TrimSpace(spi.StringValue(option, "label"))
			if label == "" {
				continue
			}
			line := "- " + label
			if description := strings.TrimSpace(spi.StringValue(option, "description")); description != "" {
				line += " — " + description
			}
			lines = append(lines, line)
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	return strings.Join(sections, "\n\n")
}

// labeledLines renders the named input keys, in the order given, as
// "Label: `value`" lines. pairs alternates key and label.
func labeledLines(input map[string]any, pairs ...string) string {
	var lines []string
	for i := 0; i+1 < len(pairs); i += 2 {
		if value := strings.TrimSpace(spi.StringValue(input, pairs[i])); value != "" {
			lines = append(lines, fmt.Sprintf("%s: %s", pairs[i+1], inlineCode(value)))
		}
	}
	return strings.Join(lines, "\n")
}

func formatWriteBody(input map[string]any) string {
	path := spi.StringValue(input, "path")
	content, hasContent := input["content"].(string)
	if !hasContent {
		return ""
	}
	return spi.CodeFence(spi.LanguageFromPath(path), content)
}

// formatEditBody prefers the unified diff OpenCode computed for each file,
// which shows context and line numbers, over reconstructing one from the
// replaced strings.
func formatEditBody(input, output map[string]any) string {
	var sections []string
	if replaceAll, ok := input["replaceAll"].(bool); ok && replaceAll {
		sections = append(sections, "Replace all: `true`")
	}

	if patches := editPatches(output); len(patches) > 0 {
		for _, patch := range patches {
			sections = append(sections, spi.CodeFence("diff", strings.TrimRight(patch, "\n")))
		}
		return strings.Join(sections, "\n\n")
	}

	oldText := spi.StringValue(input, "oldString")
	newText := spi.StringValue(input, "newString")
	if oldText != "" || newText != "" {
		sections = append(sections, spi.FormatDiffBlock(oldText, newText))
	}
	return strings.Join(sections, "\n\n")
}

// editPatches returns the unified diffs from an edit's metadata.files, in the
// order OpenCode recorded them.
func editPatches(output map[string]any) []string {
	metadata, _ := output["metadata"].(map[string]any)
	files, _ := metadata["files"].([]any)
	var patches []string
	for _, entry := range files {
		file, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if patch := spi.StringValue(file, "patch"); strings.TrimSpace(patch) != "" {
			patches = append(patches, patch)
		}
	}
	return patches
}

func formatShellBody(input map[string]any) string {
	var sections []string
	if command := spi.StringValue(input, "command"); command != "" {
		sections = append(sections, spi.CodeFence("bash", command))
	}
	if details := labeledLines(input, "workdir", "Working directory", "timeout", "Timeout"); details != "" {
		sections = append(sections, details)
	}
	return strings.Join(sections, "\n\n")
}

func formatSubagentBody(input map[string]any) string {
	var sections []string
	if agent := labeledLines(input, "agent", "Agent"); agent != "" {
		sections = append(sections, agent)
	}
	if prompt := spi.StringValue(input, "prompt"); strings.TrimSpace(prompt) != "" {
		sections = append(sections, "Prompt:\n\n"+spi.CodeFence("text", prompt))
	}
	return strings.Join(sections, "\n\n")
}

// formatToolResult renders a call's outcome. A failure takes priority over
// every tool's success rendering.
func formatToolResult(name string, input, output map[string]any) string {
	if output == nil {
		return ""
	}
	texts := outputTexts(output)
	var sections []string

	if errText := strings.TrimSpace(spi.StringValue(output, "error")); errText != "" {
		sections = append(sections, "**Error:** "+errText)
		if joined := strings.TrimSpace(strings.Join(texts, "\n")); joined != "" {
			sections = append(sections, resultBlock("text", joined))
		}
		return strings.Join(sections, "\n\n")
	}

	switch spi.StringValue(output, "status") {
	case statusRunning, statusStreaming:
		return "_Still running._"
	}

	switch name {
	case toolRead:
		sections = append(sections, formatReadResult(spi.StringValue(input, "path"), texts))
	case toolWrite, toolEdit:
		sections = append(sections, formatAcknowledgement(texts))
	case toolShell:
		sections = append(sections, formatShellResult(output, texts))
	case toolWebFetch:
		// The page arrives converted to markdown unless the call asked for
		// another format.
		lang := "markdown"
		if spi.StringValue(input, "format") == "html" {
			lang = "html"
		}
		sections = append(sections, resultBlock(lang, strings.Join(texts, "\n")))
	case toolSkill:
		sections = append(sections, resultBlock("markdown", unwrapTag(strings.Join(texts, "\n"), "skill_content")))
	case toolSubagent:
		sections = append(sections, formatSubagentResult(texts))
	case toolQuestion:
		sections = append(sections, formatQuestionAnswers(output, texts))
	case toolExecute:
		// Code Mode returns whatever the script returned; a returned object
		// arrives as JSON text.
		text := strings.Join(texts, "\n")
		lang := "text"
		if json.Valid([]byte(strings.TrimSpace(text))) {
			lang = "json"
		}
		sections = append(sections, resultBlock(lang, text), formatInnerToolCalls(output))
	default:
		sections = append(sections, resultBlock("text", strings.Join(texts, "\n")))
	}

	if files, ok := output["files"].([]string); ok && len(files) > 0 {
		lines := make([]string, 0, len(files)+1)
		lines = append(lines, "Files:")
		for _, file := range files {
			lines = append(lines, "- "+inlineCode(file))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}

	return joinNonEmpty(sections)
}

func outputTexts(output map[string]any) []string {
	texts, _ := output["texts"].([]string)
	return texts
}

// resultBlock renders a result in a fence tagged lang, capped with a visible
// marker. Empty results render nothing.
func resultBlock(lang, text string) string {
	text = strings.Trim(text, "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "Result:\n\n" + spi.CodeFence(lang, spi.CapRunes(text, maxResultRunes))
}

// formatReadResult shows the file OpenCode returned in a fence tagged by the
// file's extension, with OpenCode's "Read file ..." header as a caption.
func formatReadResult(path string, texts []string) string {
	text := strings.Trim(strings.Join(texts, "\n"), "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	header, rest, found := strings.Cut(text, "\n")
	if !strings.HasPrefix(header, "Read file ") {
		return resultBlock(spi.LanguageFromPath(path), text)
	}
	if !found || strings.TrimSpace(rest) == "" {
		return header
	}
	return header + "\n\n" + spi.CodeFence(spi.LanguageFromPath(path), spi.CapRunes(rest, maxResultRunes))
}

// formatAcknowledgement renders a one-line confirmation inline and anything
// longer in a fence.
func formatAcknowledgement(texts []string) string {
	text := strings.TrimSpace(strings.Join(texts, "\n"))
	if text == "" {
		return ""
	}
	if !strings.Contains(text, "\n") {
		return "Result: " + text
	}
	return resultBlock("text", text)
}

// formatShellResult renders shell output with terminal control bytes removed,
// then the exit code from metadata.
func formatShellResult(output map[string]any, texts []string) string {
	metadata, _ := output["metadata"].(map[string]any)
	exit := spi.StringValue(metadata, "exit")

	var kept []string
	for _, text := range texts {
		if exit != "" && exitNoticePattern.MatchString(strings.TrimSpace(text)) {
			continue
		}
		kept = append(kept, text)
	}

	var sections []string
	if block := resultBlock("text", sanitizeShellOutput(strings.Join(kept, "\n"))); block != "" {
		sections = append(sections, block)
	}
	if truncated, ok := metadata["truncated"].(bool); ok && truncated {
		sections = append(sections, "[Output truncated by OpenCode]")
	}
	if exit != "" {
		sections = append(sections, "Exit code: "+exit)
	}
	return strings.Join(sections, "\n\n")
}

// subagentTagPattern matches the wrapper OpenCode puts around a subagent's
// answer: <subagent sessionID="..." state="...">.
var subagentTagPattern = regexp.MustCompile(`(?s)^<subagent\s+sessionID="([^"]*)"\s+state="([^"]*)">\n?(.*?)\n?</subagent>$`)

func formatSubagentResult(texts []string) string {
	text := strings.TrimSpace(strings.Join(texts, "\n"))
	if text == "" {
		return ""
	}
	match := subagentTagPattern.FindStringSubmatch(text)
	if match == nil {
		return resultBlock("markdown", text)
	}
	lines := []string{fmt.Sprintf("Subagent session: %s (%s)", inlineCode(match[1]), match[2])}
	if block := resultBlock("markdown", match[3]); block != "" {
		lines = append(lines, block)
	}
	return strings.Join(lines, "\n\n")
}

// unwrapTag strips an XML-style wrapper OpenCode adds for the model, such as
// <skill_content name="...">...</skill_content>, leaving the content itself.
func unwrapTag(text, tag string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "<"+tag) || !strings.HasSuffix(trimmed, "</"+tag+">") {
		return text
	}
	_, inner, found := strings.Cut(trimmed, ">")
	if !found {
		return text
	}
	return strings.TrimSuffix(inner, "</"+tag+">")
}

// formatQuestionAnswers renders the user's answers from metadata.answers (one
// list of chosen labels per question). The text result restates them for the
// model, so it is shown only when no answers were recorded.
func formatQuestionAnswers(output map[string]any, texts []string) string {
	metadata, _ := output["metadata"].(map[string]any)
	answers, _ := metadata["answers"].([]any)
	var chosen []string
	for _, entry := range answers {
		labels, _ := entry.([]any)
		var picked []string
		for _, label := range labels {
			if text, ok := label.(string); ok && strings.TrimSpace(text) != "" {
				picked = append(picked, text)
			}
		}
		if len(picked) > 0 {
			chosen = append(chosen, strings.Join(picked, ", "))
		}
	}
	if len(chosen) == 0 {
		return resultBlock("text", strings.Join(texts, "\n"))
	}
	if len(chosen) == 1 {
		return "Answer: " + chosen[0]
	}
	lines := []string{"Answers:"}
	for _, answer := range chosen {
		lines = append(lines, "- "+answer)
	}
	return strings.Join(lines, "\n")
}

// formatInnerToolCalls lists the catalog calls an execute run made, from
// metadata.toolCalls.
func formatInnerToolCalls(output map[string]any) string {
	metadata, _ := output["metadata"].(map[string]any)
	calls, _ := metadata["toolCalls"].([]any)
	var lines []string
	for _, entry := range calls {
		call, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name := spi.StringValue(call, "tool")
		if name == "" {
			continue
		}
		line := "- " + inlineCode(name)
		if status := spi.StringValue(call, "status"); status != "" {
			line += " — " + status
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return "Tool calls:\n\n" + strings.Join(lines, "\n")
}

func joinNonEmpty(sections []string) string {
	var kept []string
	for _, section := range sections {
		if strings.TrimSpace(section) != "" {
			kept = append(kept, section)
		}
	}
	return strings.Join(kept, "\n\n")
}

// sanitizeShellOutput strips ANSI escape sequences and the remaining control
// bytes so terminal output cannot corrupt the markdown fence.
func sanitizeShellOutput(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(content))
}

// toolPathHints records the workspace paths a call touched, normalized
// against the workspace root.
func toolPathHints(name string, input map[string]any, workspaceRoot string) []string {
	var hints []string
	add := func(path string) {
		if path = strings.TrimSpace(path); path == "" {
			return
		}
		if normalized := spi.NormalizePath(path, workspaceRoot); normalized != "" {
			hints = append(hints, normalized)
		}
	}

	switch spi.NormalizeToolName(name) {
	case toolRead, toolWrite, toolEdit:
		add(spi.StringValue(input, "path"))
	case toolGlob, toolGrep:
		add(spi.StringValue(input, "path"))
	case toolShell:
		cwd := spi.StringValue(input, "workdir")
		if cwd == "" {
			cwd = workspaceRoot
		}
		hints = append(hints, spi.ExtractShellPathHints(spi.StringValue(input, "command"), cwd, workspaceRoot)...)
	}

	if len(hints) == 0 {
		return nil
	}
	slices.Sort(hints)
	return slices.Compact(hints)
}
