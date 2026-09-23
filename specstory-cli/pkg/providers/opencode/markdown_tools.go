package opencode

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// maxResultRunes caps a rendered tool result. Results are previews of what the
// tool returned (file bodies, fetched pages, skill documents); the full text
// stays in RawData.
const maxResultRunes = 20000

// Tool names as OpenCode 2.0.14 records them (see testdata/tools.txt).
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
	case toolQuestion:
		argument = firstQuestionHeader(tool.Input)
	}
	argument = strings.TrimSpace(argument)
	if argument == "" {
		return ""
	}
	return fmt.Sprintf("Tool use: **%s** %s", tool.Name, spi.InlineCode(argument))
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

// firstQuestionHeader returns the short label OpenCode's form shows above the
// first question, which names the call better than the bare tool name.
func firstQuestionHeader(input map[string]any) string {
	questions, _ := input["questions"].([]any)
	if len(questions) == 0 {
		return ""
	}
	question, _ := questions[0].(map[string]any)
	return spi.StringValue(question, "header")
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
			lines = append(lines, fmt.Sprintf("%s: %s", pairs[i+1], spi.InlineCode(value)))
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
	// The final newline is the file's line terminator; kept, it renders as a
	// blank line before the closing fence.
	return spi.CodeFence(spi.LanguageFromPath(path), strings.TrimSuffix(content, "\n"))
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
	case toolWebSearch:
		sections = append(sections, formatWebSearchResult(output, texts))
	case toolSkill:
		sections = append(sections, formatSkillResult(output, texts))
	case toolSubagent:
		sections = append(sections, formatSubagentResult(texts))
	case toolQuestion:
		sections = append(sections, formatQuestionAnswers(output, texts))
	case toolExecute:
		sections = append(sections, formatExecuteResult(output, texts))
	default:
		sections = append(sections, resultBlock("text", strings.Join(texts, "\n")))
	}

	if files, ok := output["files"].([]string); ok && len(files) > 0 {
		lines := make([]string, 0, len(files)+1)
		lines = append(lines, "Files:")
		for _, file := range files {
			lines = append(lines, "- "+spi.InlineCode(file))
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

// formatReadResult shows what OpenCode returned with its header ("Read file
// <path>, lines a-b" or "Read directory <path>, entries a-b") as a caption: a
// file in a fence tagged by its extension, a directory listing as text.
func formatReadResult(path string, texts []string) string {
	text := strings.Trim(strings.Join(texts, "\n"), "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	header, rest, found := strings.Cut(text, "\n")
	var lang string
	switch {
	case strings.HasPrefix(header, "Read file "):
		lang = spi.LanguageFromPath(path)
	case strings.HasPrefix(header, "Read directory "):
		lang = "text"
	default:
		return resultBlock(spi.LanguageFromPath(path), text)
	}
	if !found || strings.TrimSpace(rest) == "" {
		return header
	}
	return header + "\n\n" + spi.CodeFence(lang, spi.CapRunes(rest, maxResultRunes))
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
	if block := resultBlock("text", spi.SanitizeShellOutput(strings.Join(kept, "\n"))); block != "" {
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
	lines := []string{fmt.Sprintf("Subagent session: %s (%s)", spi.InlineCode(match[1]), match[2])}
	if block := resultBlock("markdown", match[3]); block != "" {
		lines = append(lines, block)
	}
	return strings.Join(lines, "\n\n")
}

// webSearchHitPattern matches the heading OpenCode's search provider puts
// above each hit: "## [Title](url)" and nothing else on the line. A page's own
// anchor headings ("## [](url#section)  Section") carry text after the link
// and so stay inside the excerpt they belong to.
var webSearchHitPattern = regexp.MustCompile(`^## \[(.*)\]\((\S+)\)$`)

// formatWebSearchResult renders the hits a search returned as a list of links,
// each followed by the page excerpt the provider attached to it. The payload is
// one markdown document, a "## [Title](url)" heading per hit, so a result that
// does not have that shape falls back to a plain fence.
func formatWebSearchResult(output map[string]any, texts []string) string {
	metadata, _ := output["metadata"].(map[string]any)
	var sections []string
	if provider := labeledLines(metadata, "provider", "Provider"); provider != "" {
		sections = append(sections, provider)
	}

	text := spi.CapRunes(strings.Trim(strings.Join(texts, "\n"), "\n"), maxResultRunes)
	hits := splitWebSearchHits(text)
	if len(hits) == 0 {
		sections = append(sections, resultBlock("text", text))
		return joinNonEmpty(sections)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d results:\n", len(hits))
	for _, hit := range hits {
		fmt.Fprintf(&b, "\n- [%s](%s)\n", hit.title, hit.url)
		if hit.excerpt != "" {
			// Indented so the excerpt stays inside its list item.
			b.WriteString("\n" + indentLines(spi.CodeFence("markdown", hit.excerpt), "  ") + "\n")
		}
	}
	sections = append(sections, strings.TrimRight(b.String(), "\n"))
	return joinNonEmpty(sections)
}

// webSearchHit is one search result: its link and the excerpt beneath it.
type webSearchHit struct {
	title, url, excerpt string
}

// splitWebSearchHits cuts the search document at each hit heading. Text before
// the first heading is not a hit and is dropped only when the document has
// hits at all; a document without any returns nil so the caller can fence it.
func splitWebSearchHits(text string) []webSearchHit {
	var hits []webSearchHit
	var excerpt []string
	flush := func() {
		if len(hits) > 0 {
			hits[len(hits)-1].excerpt = strings.Trim(strings.Join(excerpt, "\n"), "\n")
		}
		excerpt = nil
	}
	for _, line := range strings.Split(text, "\n") {
		match := webSearchHitPattern.FindStringSubmatch(line)
		if match == nil {
			excerpt = append(excerpt, line)
			continue
		}
		flush()
		// Link text can be a placeholder (a zero-width space was observed);
		// the URL then names the hit.
		title := strings.Join(strings.Fields(strings.ReplaceAll(match[1], "\u200b", " ")), " ")
		if title == "" {
			title = match[2]
		}
		hits = append(hits, webSearchHit{title: title, url: match[2]})
	}
	flush()
	return hits
}

// indentLines prefixes every line of text with indent.
func indentLines(text, indent string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}

// skillFooterMarker opens the footer OpenCode appends to a skill document for
// the model: the base directory, a note on path resolution, and a sampled
// <skill_files> listing. None of it is part of the skill.
const skillFooterMarker = "\nBase directory for this skill:"

// formatSkillResult renders the skill document without the model-facing
// wrapper and footer OpenCode adds, and names the directory it was loaded from.
func formatSkillResult(output map[string]any, texts []string) string {
	metadata, _ := output["metadata"].(map[string]any)
	document := stripSkillFooter(unwrapTag(strings.Join(texts, "\n"), "skill_content"))
	return joinNonEmpty([]string{
		labeledLines(metadata, "directory", "Directory"),
		resultBlock("markdown", document),
	})
}

// stripSkillFooter removes OpenCode's footer from a skill document. Both the
// footer's opening line and its closing </skill_files> tag must be present, so
// a skill that merely mentions a base directory keeps its text.
func stripSkillFooter(document string) string {
	trimmed := strings.TrimRight(document, "\n")
	if !strings.HasSuffix(trimmed, "</skill_files>") {
		return document
	}
	start := strings.LastIndex(trimmed, skillFooterMarker)
	if start < 0 {
		return document
	}
	return strings.TrimRight(trimmed[:start], "\n")
}

// formatExecuteResult renders what a Code Mode script returned, then the
// catalog calls it made. A script that threw still finishes with status
// "completed"; OpenCode flags it in metadata and the thrown message is the
// result text, so that flag is what marks the run as failed.
func formatExecuteResult(output map[string]any, texts []string) string {
	metadata, _ := output["metadata"].(map[string]any)
	text := strings.Trim(strings.Join(texts, "\n"), "\n")

	var sections []string
	switch failed, _ := metadata["error"].(bool); {
	case failed && strings.Contains(text, "\n"):
		sections = append(sections, "**Error:**", spi.CodeFence("text", spi.CapRunes(text, maxResultRunes)))
	case failed:
		sections = append(sections, "**Error:** "+text)
	default:
		// A returned object arrives as JSON text.
		lang := "text"
		if json.Valid([]byte(text)) {
			lang = "json"
		}
		sections = append(sections, resultBlock(lang, text))
	}
	return joinNonEmpty(append(sections, formatInnerToolCalls(output)))
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
		line := "- " + spi.InlineCode(name)
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
	case toolRead, toolWrite, toolEdit, toolGlob, toolGrep:
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
