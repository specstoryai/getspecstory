package spi

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// CodeFence wraps content in a fenced code block whose backtick fence is long
// enough that no backtick run inside content can prematurely close it.
//
// CommonMark closes a fenced block only on a line whose backtick run is at
// least as long as the opening fence. Agent output (tool results, file
// contents, diffs, README excerpts) routinely contains its own ``` fences; a
// fixed 3-backtick wrapper lets those leak out — an unbalanced inner fence
// opens a code block that is never closed and swallows the rest of the
// rendered document. Sizing the fence to one longer than the longest backtick
// run in content (minimum 3) embeds arbitrary content safely and, unlike
// backslash-escaping, leaves no artifact in the rendered output.
//
// lang is an optional info string (e.g. "bash", "diff"); pass "" for none.
// The result has no leading or trailing newline; call sites keep their own
// spacing.
func CodeFence(lang, content string) string {
	fence := strings.Repeat("`", fenceLen(content))
	return fmt.Sprintf("%s%s\n%s\n%s", fence, lang, content, fence)
}

// fenceLen returns the fence length needed to safely wrap content: at least 3,
// and always strictly greater than the longest run of consecutive backticks in
// content, so no line inside content can act as a closing fence.
func fenceLen(content string) int {
	return max(longestBacktickRun(content)+1, 3)
}

// longestBacktickRun returns the length of the longest run of consecutive
// backticks in text, which is what decides how long a fence or code span's
// own backtick run must be to contain it.
func longestBacktickRun(text string) int {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

// InlineCode wraps text in an inline code span whose backtick run is longer
// than any inside text, so a path, tool name or schema value that contains
// backticks cannot end the span early. A code span is a single line, so runs
// of whitespace (including line breaks) collapse to one space.
//
// When text contains backticks the span is padded with one space on each
// side: CommonMark strips one such space from each end, which is what lets
// the content itself begin or end with a backtick.
func InlineCode(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	longest := longestBacktickRun(text)
	if longest == 0 {
		return "`" + text + "`"
	}
	fence := strings.Repeat("`", longest+1)
	return fence + " " + text + " " + fence
}

// SanitizeShellOutput strips ANSI escape sequences and the remaining control
// bytes from terminal output so it cannot corrupt the markdown fence it is
// rendered in. Newlines and tabs are kept; CRLF line endings become LF.
func SanitizeShellOutput(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(content))
}

// CapRunes truncates s to at most max runes, marking the cut. Rune-based so a
// cap never splits a multi-byte character. Scans rune boundaries instead of
// converting to []rune, which would allocate O(len(s)) for exactly the
// oversized tool results this cap protects against.
func CapRunes(s string, max int) string {
	if len(s) <= max {
		return s // fast path: byte length bounds rune length
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i] + "\n… (output truncated)"
		}
		count++
	}
	return s // more bytes than max but fewer runes (multi-byte characters)
}
