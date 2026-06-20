package mdfmt

import (
	"fmt"
	"strings"
)

// CodeFence wraps content in a fenced code block whose backtick fence is long
// enough that no backtick run inside content can prematurely close it.
//
// CommonMark closes a fenced block only on a line whose backtick run is at
// least as long as the opening fence. Agent output (tool results, file
// contents, diffs, README excerpts) routinely contains its own ``` fences; a
// fixed 3-backtick wrapper lets those leak out — an unbalanced inner fence
// opens a code block that is never closed and swallows the rest of the
// document. Sizing the fence to one longer than the longest backtick run in
// content (minimum 3) embeds arbitrary content safely and, unlike
// backslash-escaping, leaves no artifact in the rendered output.
//
// lang is an optional info string (e.g. "text", "bash"); pass "" for none.
// The result has no leading or trailing newline.
func CodeFence(lang, content string) string {
	fence := strings.Repeat("`", fenceLen(content))
	return fmt.Sprintf("%s%s\n%s\n%s", fence, lang, content, fence)
}

// fenceLen returns the fence length needed to safely wrap content: at least 3,
// and always strictly greater than the longest run of consecutive backticks in
// content, so no line inside content can act as a closing fence.
func fenceLen(content string) int {
	longest, run := 0, 0
	for _, r := range content {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	if n := longest + 1; n > 3 {
		return n
	}
	return 3
}
