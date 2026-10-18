package spi

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Helpers shared by provider markdown renderers. Each provider decodes its own
// native tool payload into a map[string]any of arguments and then renders those
// arguments; the rendering itself is agent-agnostic, so it lives here rather
// than being re-derived per provider.

// StringValue returns the first of keys present in args, rendered as a plain
// string, or "" when no key is present or the value has an unsupported type.
//
// The type switch is wider than json.Unmarshal can actually produce (which is
// only string, float64, bool, nil, map and slice) so that providers decoding
// with json.Decoder.UseNumber, or building argument maps by hand, get a
// sensible rendering instead of a silently empty one.
func StringValue(args map[string]any, keys ...string) string {
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
	return ""
}

// NormalizeToolName reduces a tool name to a comparison key by lowercasing it
// and dropping spaces, hyphens and underscores, so that a renderer can match
// one tool whatever spelling the agent emitted ("read_file", "Read File",
// "read-file").
func NormalizeToolName(name string) string {
	cleaned := strings.ToLower(strings.TrimSpace(name))
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, "_", "")
	return cleaned
}

// TodoSymbol maps a todo-list item status to the character rendered inside a
// markdown checkbox. Unknown statuses render as an unchecked box rather than
// being dropped, so an agent adding a new status never loses the item.
func TodoSymbol(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "done":
		return "x"
	case "in_progress", "active":
		return "⚡"
	default:
		return " "
	}
}

// LanguageFromPath derives a code-fence info string from a file path's
// extension.
//
// Only extensions whose fence tag differs from the extension itself are listed;
// everything else (go, java, json, xml, sh, html, css, …) is already a valid tag
// and passes through. A path with no usable extension (Makefile, LICENSE)
// returns "", leaving the fence untagged rather than asserting plaintext, which
// lets the renderer decide how to treat it.
func LanguageFromPath(path string) string {
	if path == "" {
		return ""
	}
	// Lowercased so that a shouted name (README.MD, SCRIPT.PY) maps the same as
	// its lowercase form instead of falling through as an unusable tag.
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if ext == "" {
		return ""
	}
	switch ext {
	case "js", "jsx":
		return "javascript"
	case "ts", "tsx":
		return "typescript"
	case "py":
		return "python"
	case "rb":
		return "ruby"
	case "rs":
		return "rust"
	case "cs":
		return "csharp"
	case "h", "c":
		return "c"
	case "hpp", "cpp", "cc":
		return "cpp"
	case "yml":
		return "yaml"
	case "md":
		return "markdown"
	default:
		return ext
	}
}

// FormatDiffBlock renders a before/after pair as a diff-fenced block, marking
// every old line with "-" and every new line with "+". Either side may be empty
// (a file creation or deletion), in which case only the other side is emitted.
func FormatDiffBlock(oldText, newText string) string {
	var b strings.Builder
	if oldText != "" {
		for line := range strings.SplitSeq(oldText, "\n") {
			b.WriteString("-")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if newText != "" {
		for line := range strings.SplitSeq(newText, "\n") {
			b.WriteString("+")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return CodeFence("diff", strings.TrimRight(b.String(), "\n"))
}

// RenderGenericJSON is the fallback renderer for a tool whose arguments have no
// bespoke layout: it shows the raw arguments as pretty-printed JSON rather than
// guessing at a shape. Keys named in dropKeys are omitted, which lets a provider
// hide labels its agent injects into every call; when nothing survives the drop
// there is nothing worth showing and the result is "".
func RenderGenericJSON(args map[string]any, dropKeys ...string) string {
	kept := make(map[string]any, len(args))
	for key, val := range args {
		if slices.Contains(dropKeys, key) {
			continue
		}
		kept[key] = val
	}
	if len(kept) == 0 {
		return ""
	}
	// MarshalIndent sorts map keys, so the fallback is deterministic. Arguments
	// come from json.Unmarshal, so re-marshaling them cannot realistically fail;
	// an error still yields "" rather than a half-rendered block.
	data, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return ""
	}
	return CodeFence("json", string(data))
}

// ToolResultCap bounds how much tool output RenderToolResult shows. Inputs are
// not capped — they carry what the agent chose to do (e.g. the full content of
// a written file) — but results (file reads, command output) can be
// arbitrarily large and matter less once the agent has already responded to
// them.
const ToolResultCap = 2000

// RenderToolInputList renders tool arguments as a sorted "**Input:**" list of
// key-value pairs, fencing multi-line values. Keys starting with "_" are
// internal annotations and are skipped. Returns "" when nothing is left.
func RenderToolInputList(input map[string]any) string {
	keys := make([]string, 0, len(input))
	for key := range input {
		if !strings.HasPrefix(key, "_") {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString("\n**Input:**\n\n")
	for _, key := range keys {
		valueStr := toolInputValueString(input[key])
		switch {
		case strings.Contains(valueStr, "\n"):
			fmt.Fprintf(&b, "- %s:\n\n%s\n\n", key, CodeFence("", valueStr))
		case strings.Contains(valueStr, "`"):
			// A backtick would close a plain span early.
			fmt.Fprintf(&b, "- %s: %s\n", key, InlineCode(valueStr))
		default:
			fmt.Fprintf(&b, "- %s: `%s`\n", key, valueStr)
		}
	}
	return b.String()
}

// RenderToolResult renders a fenced "**Result:**" section, or "" when result is
// blank. Trailing blank lines are trimmed — terminal-style results carry dozens
// of them and they only pad out the fenced block — and the result is capped at
// ToolResultCap runes with a visible truncation marker.
func RenderToolResult(result string) string {
	if strings.TrimSpace(result) == "" {
		return ""
	}
	capped := CapRunes(strings.TrimRight(result, " \t\n"), ToolResultCap)
	return fmt.Sprintf("\n**Result:**\n\n%s\n", CodeFence("", capped))
}

// RenderShellCall renders a shell command the way it reads in a terminal
// session: the explanation as prose, the command in a bash fence (uncapped —
// it is what the agent chose to run), then the fenced, capped result.
func RenderShellCall(explanation, command, result string) string {
	var b strings.Builder
	if explanation != "" {
		fmt.Fprintf(&b, "\n%s\n", explanation)
	}
	fmt.Fprintf(&b, "\n%s\n", CodeFence("bash", command))
	b.WriteString(RenderToolResult(result))
	return b.String()
}

// toolInputValueString renders one tool-input value for RenderToolInputList.
// Scalars print as-is; maps and slices marshal as JSON — Go's %v formatting
// (map[args:[x] command:echo ...]) is meaningless to readers of the archive.
// Compact JSON for short values, indented (which the caller fences) for long.
func toolInputValueString(v any) string {
	switch v.(type) {
	case nil, string, bool, float64, int, int64:
		return fmt.Sprintf("%v", v)
	}
	compact, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	if len(compact) <= 80 {
		return string(compact)
	}
	if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
		return string(pretty)
	}
	return string(compact)
}

// FormatInputSchema lists a tool's parameters, required ones first, with their
// types and descriptions.
func FormatInputSchema(schema map[string]any) string {
	if schema == nil {
		return "Parameters: none"
	}
	var b strings.Builder
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) == 0 {
		b.WriteString("Parameters: none")
	} else {
		b.WriteString("Parameters:\n")
		writeSchemaProperties(&b, schema, 0)
	}
	text := strings.TrimRight(b.String(), "\n")
	// Keys such as additionalProperties constrain the whole input; "type" is
	// always "object" for a tool input and says nothing.
	if extra := formatSchemaExtras(WithoutKeys(schema, "type", "properties", "required")); extra != "" {
		text += "\n\n" + extra
	}
	return text
}

// writeSchemaProperties writes one bullet per property of an object schema,
// nesting the properties of object and array-of-object parameters beneath it.
func writeSchemaProperties(b *strings.Builder, schema map[string]any, depth int) {
	properties, _ := schema["properties"].(map[string]any)
	required, _ := StringList(schema["required"])
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		iRequired, jRequired := slices.Contains(required, names[i]), slices.Contains(required, names[j])
		if iRequired != jRequired {
			return iRequired
		}
		return names[i] < names[j]
	})

	indent := strings.Repeat("  ", depth)
	for _, name := range names {
		property, ok := properties[name].(map[string]any)
		if !ok {
			fmt.Fprintf(b, "%s- %s: %s\n", indent, InlineCode(name), InlineCode(JSONText(properties[name])))
			continue
		}
		var qualifiers []string
		if schemaType := schemaTypeName(property); schemaType != "" {
			qualifiers = append(qualifiers, schemaType)
		}
		if slices.Contains(required, name) {
			qualifiers = append(qualifiers, "required")
		}
		line := indent + "- " + InlineCode(name)
		if len(qualifiers) > 0 {
			line += " (" + strings.Join(qualifiers, ", ") + ")"
		}
		// A description can span paragraphs; one line keeps the list intact.
		if description := strings.Join(strings.Fields(StringValue(property, "description")), " "); description != "" {
			line += ": " + description
		}
		if values, ok := property["enum"].([]any); ok && len(values) > 0 {
			spans := make([]string, 0, len(values))
			for _, value := range values {
				spans = append(spans, InlineCode(JSONText(value)))
			}
			line += " One of: " + strings.Join(spans, ", ") + "."
		}
		consumed := []string{"type", "anyOf", "oneOf", "description", "enum", "properties", "required"}
		items, _ := property["items"].(map[string]any)
		if schemaTypeName(property) != "" {
			consumed = append(consumed, "items")
		}
		for _, key := range slices.Sorted(maps.Keys(WithoutKeys(property, consumed...))) {
			line += fmt.Sprintf("; %s: %s", key, InlineCode(JSONText(property[key])))
		}
		b.WriteString(line + "\n")

		if _, nested := property["properties"].(map[string]any); nested {
			writeSchemaProperties(b, property, depth+1)
		} else if _, nested := items["properties"].(map[string]any); nested {
			writeSchemaProperties(b, items, depth+1)
		}
	}
}

// schemaTypeName describes a JSON schema's type the way a reader would say it:
// "string", "array of string", "string | null".
func schemaTypeName(schema map[string]any) string {
	switch typed := schema["type"].(type) {
	case string:
		if items, ok := schema["items"].(map[string]any); typed == "array" && ok {
			if itemType := schemaTypeName(items); itemType != "" {
				return "array of " + itemType
			}
		}
		return typed
	case []any:
		if names, ok := StringList(typed); ok {
			return strings.Join(names, " | ")
		}
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		branches, ok := schema[key].([]any)
		if !ok {
			continue
		}
		var names []string
		for _, raw := range branches {
			branch, ok := raw.(map[string]any)
			if !ok {
				return ""
			}
			name := schemaTypeName(branch)
			if name == "" {
				return ""
			}
			names = append(names, name)
		}
		return strings.Join(names, " | ")
	}
	return ""
}

// StringList reads a JSON array whose items are all strings. The bool is false
// for any other value, so a caller can fall back to rendering it as JSON.
func StringList(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		items := make([]string, 0, len(typed))
		for _, raw := range typed {
			item, ok := raw.(string)
			if !ok {
				return nil, false
			}
			items = append(items, item)
		}
		return items, true
	}
	return nil, false
}

// WithoutKeys returns a copy of m without the named keys, leaving m intact.
func WithoutKeys(m map[string]any, keys ...string) map[string]any {
	kept := make(map[string]any, len(m))
	for key, value := range m {
		if !slices.Contains(keys, key) {
			kept[key] = value
		}
	}
	return kept
}

// JSONText renders a JSON value as its literal text: strings as-is, everything
// else encoded, including null.
func JSONText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

// formatSchemaExtras lists schema keywords that have no place in the parameter
// list, one "key: value" line each, so a constraint such as
// additionalProperties is shown rather than dropped.
func formatSchemaExtras(extras map[string]any) string {
	var b strings.Builder
	for _, key := range slices.Sorted(maps.Keys(extras)) {
		value := JSONText(extras[key])
		if strings.ContainsAny(value, "\n`") {
			fmt.Fprintf(&b, "%s:\n%s\n", key, CodeFence("text", value))
		} else {
			fmt.Fprintf(&b, "%s: %s\n", key, InlineCode(value))
		}
	}
	return strings.TrimSpace(b.String())
}
