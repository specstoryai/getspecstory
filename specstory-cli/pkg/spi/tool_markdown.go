package spi

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
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
