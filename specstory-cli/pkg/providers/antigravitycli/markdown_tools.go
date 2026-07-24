package antigravitycli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// classifyToolType maps an Antigravity tool name to a SpecStory tool type. The
// cases below are Antigravity's complete tool set as of agy 1.1.x, captured by
// asking the agent to enumerate its own tools (see
// docs/antigravity-format-spec.md §3.5). Everything else — the tools that carry
// no useful type (ask_permission, ask_question, define_subagent,
// generate_image, invoke_subagent, list_permissions, manage_subagents,
// send_message), MCP tools, and anything a later release adds — falls back to
// "generic" (not "unknown") so it still renders sensibly.
func classifyToolType(name string) string {
	switch name {
	case "write_to_file", "replace_file_content", "multi_replace_file_content":
		return schema.ToolTypeWrite
	case "view_file", "list_dir":
		return schema.ToolTypeRead
	case "grep_search", "search_web", "read_url_content":
		return schema.ToolTypeSearch
	case "run_command":
		return schema.ToolTypeShell
	case "manage_task", "schedule":
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

// formatToolInput renders a tool call's arguments. The cases are Antigravity's
// real tool names (§3.5); a tool with no case — ask_permission, whose argument
// shape has never been captured, or anything a later release adds — falls
// through to the raw-argument fallback rather than a guessed layout.
func formatToolInput(tool *ToolInfo) string {
	args := tool.Input
	switch normalizeToolName(tool.Name) {
	case "runcommand":
		return renderRunCommandInput(args)
	case "viewfile":
		return renderReadInput(args)
	case "listdir":
		return renderListInput(args)
	case "grepsearch":
		return renderGrepInput(args)
	case "searchweb":
		return renderWebSearchInput(args)
	case "readurlcontent":
		return renderWebFetchInput(args)
	case "writetofile":
		return renderWriteInput(args)
	case "replacefilecontent":
		return renderEditInput(args)
	case "multireplacefilecontent":
		return renderMultiEditInput(args)
	case "generateimage":
		return renderGenerateImageInput(args)
	case "definesubagent":
		return renderDefineSubagentInput(args)
	case "invokesubagent":
		return renderInvokeSubagentInput(args)
	case "managesubagents":
		return renderManageInput(args, "Conversations", "ConversationIds")
	case "managetask":
		return renderManageInput(args, "Task", "TaskId")
	case "sendmessage":
		return renderSendMessageInput(args)
	case "schedule":
		return renderScheduleInput(args)
	case "askquestion":
		return renderAskQuestionInput(args)
	case "listpermissions":
		// No meaningful args (only toolAction/toolSummary); the result text is
		// self-describing, so render input as empty and let the output stand alone.
		return ""
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

	// A few tools return a JSON blob (agy's protobuf-JSON, double-spaced and full
	// of internal ids) that reads far better as a short summary. These are scoped
	// by tool name so JSON-lines results (list_dir, grep_search) are never touched.
	switch normalizeToolName(tool.Name) {
	case "invokesubagent":
		if s := formatInvokeSubagentOutput(content); s != "" {
			return s
		}
	case "managesubagents":
		if s := formatManageSubagentsOutput(content); s != "" {
			return s
		}
	}

	// agy's edit tools wrap the applied diff in [diff_block_start]/[diff_block_end];
	// render it as a diff fence (any tool whose result carries the markers).
	if s := formatDiffBlockOutput(content); s != "" {
		return s
	}

	if strings.Contains(content, "\n") {
		return "Output:\n" + codeFence("text", content)
	}
	return fmt.Sprintf("Output: %s", content)
}

const (
	diffBlockStartMarker = "[diff_block_start]"
	diffBlockEndMarker   = "[diff_block_end]"
)

// formatDiffBlockOutput reformats an agy edit-tool result — a lead sentence
// followed by a unified diff wrapped in [diff_block_start]/[diff_block_end]
// markers — into a ```diff fenced block. The lead line ("The following changes
// were made by the … tool to: <path>.") is dropped as redundant: the input block
// already shows the target path and the diff. Returns "" when no diff block is
// present, so the caller falls back to the raw output.
func formatDiffBlockOutput(content string) string {
	start := strings.Index(content, diffBlockStartMarker)
	end := strings.Index(content, diffBlockEndMarker)
	if start == -1 || end == -1 || end < start {
		return ""
	}
	diff := strings.TrimSpace(content[start+len(diffBlockStartMarker) : end])
	if diff == "" {
		return ""
	}
	return "Output:\n" + codeFence("diff", diff)
}

// formatInvokeSubagentOutput summarizes invoke_subagent's result JSON as one
// bullet per spawned subagent (its conversation id), dropping the internal log
// URI and workspace paths. Returns "" when the output is not the expected
// lead-in + JSON shape, so the caller falls back to the raw text.
func formatInvokeSubagentOutput(content string) string {
	lead, objs, ok := splitLeadJSON(content)
	if !ok {
		return ""
	}
	var bullets []string
	for _, o := range objs {
		if id := stringValue(o, "conversationId"); id != "" {
			bullets = append(bullets, fmt.Sprintf("- conversation `%s`", id))
		}
	}
	if len(bullets) == 0 {
		return ""
	}
	return joinOutput(defaultLead(lead, "Created subagents:"), bullets)
}

// formatManageSubagentsOutput summarizes manage_subagents' result JSON as one
// bullet per active subagent (role, type, conversation id), dropping the model
// placeholder, tier, initial prompt, and internal log URIs. Returns "" when the
// output is not the expected lead-in + JSON shape (e.g. the "kill" action's plain
// text), so the caller falls back to the raw text.
func formatManageSubagentsOutput(content string) string {
	lead, objs, ok := splitLeadJSON(content)
	if !ok {
		return ""
	}
	var bullets []string
	for _, o := range objs {
		spec, _ := o["spec"].(map[string]any)
		result, _ := o["result"].(map[string]any)
		role := stringValue(spec, "role")
		typeName := stringValue(spec, "typeName")
		id := stringValue(result, "conversationId")

		label := role
		if label == "" {
			label = typeName
		}
		if label == "" && id == "" {
			continue
		}
		line := "- "
		if label != "" {
			line += "**" + label + "**"
		}
		if typeName != "" && typeName != label {
			line += " (`" + typeName + "`)"
		}
		if id != "" {
			if label != "" {
				line += " — "
			}
			line += "conversation `" + id + "`"
		}
		bullets = append(bullets, line)
	}
	if len(bullets) == 0 {
		return ""
	}
	return joinOutput(defaultLead(lead, "Active subagents:"), bullets)
}

// splitLeadJSON splits a tool result into its leading prose and the JSON
// value(s) that follow. It scans for the first line beginning a JSON object or
// array, treats everything before as the lead, and decodes the remainder as a
// stream of JSON values (tolerating agy's occasional multi-object output). It
// returns ok=false when no parseable JSON is present, so callers can fall back.
func splitLeadJSON(content string) (lead string, objs []map[string]any, ok bool) {
	lines := strings.Split(content, "\n")
	start := -1
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
			start = i
			break
		}
	}
	if start == -1 {
		return "", nil, false
	}
	lead = strings.TrimSpace(strings.Join(lines[:start], "\n"))

	dec := json.NewDecoder(strings.NewReader(strings.Join(lines[start:], "\n")))
	for {
		var v any
		err := dec.Decode(&v)
		if err == io.EOF {
			break
		}
		if err != nil {
			if len(objs) == 0 {
				return "", nil, false
			}
			break // stop at the first non-JSON trailing content, keep what parsed
		}
		switch t := v.(type) {
		case map[string]any:
			objs = append(objs, t)
		case []any:
			for _, e := range t {
				if m, ok := e.(map[string]any); ok {
					objs = append(objs, m)
				}
			}
		}
	}
	if len(objs) == 0 {
		return "", nil, false
	}
	return lead, objs, true
}

// defaultLead returns lead, or fallback when lead is empty.
func defaultLead(lead, fallback string) string {
	if lead != "" {
		return lead
	}
	return fallback
}

// joinOutput assembles a summarized tool output: the "Output:" label, the lead
// sentence, then the bullet lines.
func joinOutput(lead string, bullets []string) string {
	return fmt.Sprintf("Output: %s\n%s", lead, strings.Join(bullets, "\n"))
}

// extractPathHints surfaces filesystem paths referenced by a tool's input so
// downstream features (cloud sync, search) can index sessions by touched files.
func extractPathHints(input map[string]any, workspaceRoot string) []string {
	if input == nil {
		return nil
	}

	// Antigravity's own PascalCase keys come from the canonical list in
	// agent_session.go so the two stay in step; the lowercase variants are
	// forward-compatibility for tools we have not seen emitted.
	pathFields := append(slices.Clone(toolPathArgKeys),
		"path", "file_path", "file", "dir", "directory", "cwd", "workdir")

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
//
// Each renderer reads exactly the arg keys its tool has been observed to emit
// (the format spec §3.5 and captured sessions) — no speculative aliases. Every
// renderer is dispatched by the tool's real name, so a key that isn't present
// means agy changed its arg shape; in that case the renderer falls back to
// renderGenericJSON, which shows the raw args rather than guessing wrong.
// NOTE: key casing varies by tool — the file tools are PascalCase, but e.g.
// search_web is lowercase and define_subagent/ask_question are snake_case.

func renderRunCommandInput(args map[string]any) string {
	command := stringValue(args, "CommandLine")
	workdir := stringValue(args, "Cwd")
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
	path := stringValue(args, "AbsolutePath")
	if path == "" {
		return renderGenericJSON(args)
	}
	return fmt.Sprintf("Path: `%s`", stripFileURI(path))
}

func renderListInput(args map[string]any) string {
	path := stringValue(args, "DirectoryPath")
	if path == "" {
		return renderGenericJSON(args)
	}
	return fmt.Sprintf("Path: `%s`", stripFileURI(path))
}

func renderGrepInput(args map[string]any) string {
	var parts []string
	if pat := stringValue(args, "Query"); pat != "" {
		parts = append(parts, fmt.Sprintf("Pattern: `%s`", pat))
	}
	if path := stringValue(args, "SearchPath"); path != "" {
		parts = append(parts, fmt.Sprintf("Path: `%s`", stripFileURI(path)))
	}
	if len(parts) == 0 {
		return renderGenericJSON(args)
	}
	return strings.Join(parts, "\n")
}

func renderWebSearchInput(args map[string]any) string {
	// search_web's observed arg key is lowercase, unlike the file tools.
	if q := stringValue(args, "query"); q != "" {
		return fmt.Sprintf("Query: `%s`", q)
	}
	return renderGenericJSON(args)
}

func renderWebFetchInput(args map[string]any) string {
	url := stringValue(args, "Url")
	prompt := stringValue(args, "Prompt")
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
		b.WriteString(codeFence(languageFromPath(path), content))
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

// renderMultiEditInput renders a multi_replace_file_content call: the target
// file, the optional instruction, then one diff block per replacement chunk.
func renderMultiEditInput(args map[string]any) string {
	path := stripFileURI(stringValue(args, "TargetFile", "file_path", "path", "file"))
	chunks, _ := args["ReplacementChunks"].([]any)

	var b strings.Builder
	if path != "" {
		fmt.Fprintf(&b, "Path: `%s`", path)
	}
	if instr := strings.TrimSpace(stringValue(args, "Instruction")); instr != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(instr)
	}

	var diffs []string
	for _, raw := range chunks {
		chunk, _ := raw.(map[string]any)
		if chunk == nil {
			continue
		}
		oldText := stringValue(chunk, "TargetContent")
		newText := stringValue(chunk, "ReplacementContent")
		if oldText == "" && newText == "" {
			continue
		}
		diffs = append(diffs, formatDiffBlock(oldText, newText))
	}
	if len(diffs) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.Join(diffs, "\n\n"))
	}

	if b.Len() == 0 {
		return renderGenericJSON(args)
	}
	return b.String()
}

// renderGenerateImageInput renders a generate_image call: the image name and
// aspect ratio, then the generation prompt.
func renderGenerateImageInput(args map[string]any) string {
	prompt := strings.TrimSpace(stringValue(args, "Prompt", "prompt"))
	name := stringValue(args, "ImageName", "name")
	aspect := stringValue(args, "AspectRatio", "aspect_ratio")
	if prompt == "" && name == "" {
		return renderGenericJSON(args)
	}

	var parts []string
	switch {
	case name != "" && aspect != "":
		parts = append(parts, fmt.Sprintf("Image: `%s` (aspect ratio `%s`)", name, aspect))
	case name != "":
		parts = append(parts, fmt.Sprintf("Image: `%s`", name))
	case aspect != "":
		parts = append(parts, fmt.Sprintf("Aspect ratio: `%s`", aspect))
	}
	if prompt != "" {
		parts = append(parts, "Prompt: "+prompt)
	}
	return strings.Join(parts, "\n\n")
}

// renderDefineSubagentInput renders a define_subagent call: the subagent name and
// description, then its system prompt as a blockquote.
func renderDefineSubagentInput(args map[string]any) string {
	name := stringValue(args, "name")
	desc := strings.TrimSpace(stringValue(args, "description"))
	sysPrompt := strings.TrimSpace(stringValue(args, "system_prompt"))
	if name == "" && desc == "" && sysPrompt == "" {
		return renderGenericJSON(args)
	}

	var b strings.Builder
	if name != "" {
		fmt.Fprintf(&b, "Subagent: `%s`", name)
	}
	if desc != "" {
		if b.Len() > 0 {
			b.WriteString(" — ")
		}
		b.WriteString(desc)
	}
	if sysPrompt != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("System prompt:\n")
		b.WriteString(blockquote(sysPrompt))
	}
	return b.String()
}

// renderInvokeSubagentInput renders an invoke_subagent call as a bullet per
// spawned subagent: its role, type, model, and prompt.
func renderInvokeSubagentInput(args map[string]any) string {
	subs, _ := args["Subagents"].([]any)
	if len(subs) == 0 {
		return renderGenericJSON(args)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Invoking %d subagent(s):\n", len(subs))
	for _, raw := range subs {
		sub, _ := raw.(map[string]any)
		if sub == nil {
			continue
		}
		role := stringValue(sub, "Role", "role")
		typeName := stringValue(sub, "TypeName", "type_name")
		model := stringValue(sub, "Model", "model")
		prompt := strings.TrimSpace(stringValue(sub, "Prompt", "prompt"))

		label := role
		if label == "" {
			label = typeName
		}
		var meta []string
		if typeName != "" && typeName != label {
			meta = append(meta, "`"+typeName+"`")
		}
		if model != "" {
			meta = append(meta, "model `"+model+"`")
		}

		line := "- **" + label + "**"
		if len(meta) > 0 {
			line += " (" + strings.Join(meta, ", ") + ")"
		}
		if prompt != "" {
			line += ": " + prompt
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderManageInput renders an Action-based management call (manage_task,
// manage_subagents): the action verb and the target ids it operates on. idKeys
// lists the argument keys that may carry the target id(s), each a string or a
// list of strings.
func renderManageInput(args map[string]any, idLabel string, idKeys ...string) string {
	var parts []string
	if action := stringValue(args, "Action", "action"); action != "" {
		parts = append(parts, fmt.Sprintf("Action: `%s`", action))
	}
	if ids := collectStringList(args, idKeys...); len(ids) > 0 {
		quoted := make([]string, len(ids))
		for i, id := range ids {
			quoted[i] = "`" + id + "`"
		}
		parts = append(parts, fmt.Sprintf("%s: %s", idLabel, strings.Join(quoted, ", ")))
	}
	if len(parts) == 0 {
		return renderGenericJSON(args)
	}
	return strings.Join(parts, "\n")
}

// renderSendMessageInput renders a send_message call: the recipient and the
// message body.
func renderSendMessageInput(args map[string]any) string {
	recipient := stringValue(args, "Recipient", "recipient", "to")
	message := strings.TrimSpace(stringValue(args, "Message", "message", "text"))
	if recipient == "" && message == "" {
		return renderGenericJSON(args)
	}

	var parts []string
	if recipient != "" {
		parts = append(parts, fmt.Sprintf("To: `%s`", recipient))
	}
	if message != "" {
		parts = append(parts, message)
	}
	return strings.Join(parts, "\n\n")
}

// renderScheduleInput renders a schedule call: the timer duration and condition,
// then the prompt the timer will fire.
func renderScheduleInput(args map[string]any) string {
	duration := stringValue(args, "DurationSeconds", "duration_seconds", "duration")
	prompt := strings.TrimSpace(stringValue(args, "Prompt", "prompt"))
	condition := stringValue(args, "TimerCondition", "timer_condition", "condition")
	if duration == "" && prompt == "" {
		return renderGenericJSON(args)
	}

	var parts []string
	if duration != "" {
		label := fmt.Sprintf("Timer: %ss", duration)
		if condition != "" {
			label += fmt.Sprintf(" (condition: `%s`)", condition)
		}
		parts = append(parts, label)
	}
	if prompt != "" {
		parts = append(parts, "Prompt: "+prompt)
	}
	return strings.Join(parts, "\n\n")
}

// renderAskQuestionInput renders an ask_question call: each question in bold
// followed by its selectable options as a bullet list. The user's answer arrives
// in the tool output and is rendered by formatToolOutput.
func renderAskQuestionInput(args map[string]any) string {
	questions, _ := args["questions"].([]any)
	if len(questions) == 0 {
		return renderGenericJSON(args)
	}

	var b strings.Builder
	for qi, raw := range questions {
		q, _ := raw.(map[string]any)
		if q == nil {
			continue
		}
		text := strings.TrimSpace(stringValue(q, "question"))
		if text == "" {
			continue
		}
		if qi > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "**%s**", text)
		if multi, ok := q["is_multi_select"].(bool); ok && multi {
			b.WriteString(" _(select multiple)_")
		}
		b.WriteString("\n")
		options, _ := q["options"].([]any)
		for _, opt := range options {
			if s, ok := opt.(string); ok {
				fmt.Fprintf(&b, "- %s\n", s)
			}
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	if out == "" {
		return renderGenericJSON(args)
	}
	return out
}

func formatDiffBlock(oldText, newText string) string {
	var b strings.Builder
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
	return codeFence("diff", strings.TrimSuffix(b.String(), "\n"))
}

// codeFence wraps body in a fenced code block tagged with lang. The fence is
// one backtick longer than the longest backtick run inside body (never fewer
// than the standard three), because a backslash cannot escape a backtick inside
// a fence — lengthening the fence is the only way to keep an embedded ```
// (e.g. a written markdown file) from terminating the block early. The body is
// never altered.
func codeFence(lang, body string) string {
	longest, run := 0, 0
	for _, r := range body {
		if r != '`' {
			run = 0
			continue
		}
		run++
		if run > longest {
			longest = run
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + lang + "\n" + body + "\n" + fence
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
	// toolAction/toolSummary are agy-injected human labels present on every
	// call, not real arguments; drop them so the fallback shows only real args.
	real := make(map[string]any, len(args))
	for k, v := range args {
		if k == "toolAction" || k == "toolSummary" {
			continue
		}
		real[k] = v
	}
	if len(real) == 0 {
		return ""
	}
	// MarshalIndent emits map keys sorted, so the fallback is deterministic.
	// Args come from json.Unmarshal, so re-marshaling them cannot fail.
	data, err := json.MarshalIndent(real, "", "  ")
	if err != nil {
		return ""
	}
	return codeFence("json", string(data))
}

// collectStringList gathers string values from args across the given keys. Each
// key's value may be a single string or a list of strings; empties are skipped.
func collectStringList(args map[string]any, keys ...string) []string {
	var out []string
	for _, key := range keys {
		val, ok := args[key]
		if !ok {
			continue
		}
		switch v := val.(type) {
		case string:
			if v != "" {
				out = append(out, v)
			}
		case []any:
			for _, entry := range v {
				if s, ok := entry.(string); ok && s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// blockquote prefixes each line of text with "> " so multi-line prompts render as
// a markdown blockquote.
func blockquote(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n")
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
