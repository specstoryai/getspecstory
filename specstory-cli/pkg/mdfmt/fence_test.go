package mdfmt

import (
	"strings"
	"testing"
)

func TestFenceLen(t *testing.T) {
	cases := []struct {
		content string
		want    int
	}{
		{"", 3},
		{"no backticks here", 3},
		{"one ` backtick", 3},
		{"two `` backticks", 3},
		{"a ``` fence", 4},
		{"```text\nhi\n```", 4},
		{"a ```` four-run", 5},
		{"```sql\n```\n````", 5}, // longest run is 4
	}
	for _, c := range cases {
		if got := fenceLen(c.content); got != c.want {
			t.Errorf("fenceLen(%q) = %d, want %d", c.content, got, c.want)
		}
	}
}

// assertSingleBlock verifies that result is one well-formed fenced code block:
// the first line opens the fence, the last line closes it with the same number
// of backticks, and no interior line could prematurely close it.
func assertSingleBlock(t *testing.T, content, result string) {
	t.Helper()
	lines := strings.Split(result, "\n")
	if len(lines) < 2 {
		t.Fatalf("result has too few lines: %q", result)
	}
	open := lines[0]
	fence := open[:len(open)-len(strings.TrimLeft(open, "`"))]
	if len(fence) < 3 {
		t.Fatalf("opening fence too short: %q", open)
	}
	if last := lines[len(lines)-1]; last != fence {
		t.Errorf("closing line %q != opening fence %q", last, fence)
	}
	// No interior line may be a run of >= len(fence) backticks (which would
	// close the block early and leak the rest of the document).
	for _, ln := range lines[1 : len(lines)-1] {
		trimmed := strings.TrimRight(ln, " ")
		if trimmed != "" && strings.Trim(trimmed, "`") == "" && len(trimmed) >= len(fence) {
			t.Errorf("interior line %q would close a %d-backtick fence", ln, len(fence))
		}
	}
	if !strings.Contains(result, content) {
		t.Errorf("result does not contain original content verbatim")
	}
}

func TestCodeFence_PlainContent(t *testing.T) {
	r := CodeFence("text", "hello\nworld")
	if !strings.HasPrefix(r, "```text\n") {
		t.Errorf("expected 3-backtick text fence, got %q", r)
	}
	assertSingleBlock(t, "hello\nworld", r)
}

func TestCodeFence_EmbeddedFenceDoesNotBreakOut(t *testing.T) {
	// The real-world fold: tool output that itself contains code fences,
	// truncated mid-block so the inner fences are unbalanced.
	content := "intro\n```sql\nSELECT 1\n```\nmid\n```bash\necho hi\n```\ntail:\n```"
	r := CodeFence("text", content)
	if !strings.HasPrefix(r, "````text\n") {
		t.Errorf("expected 4-backtick fence for ```-containing content, got prefix %q", r[:min(12, len(r))])
	}
	assertSingleBlock(t, content, r)
}

func TestCodeFence_FourBacktickRun(t *testing.T) {
	content := "a ```` b" // 4-run inside
	r := CodeFence("", content)
	if !strings.HasPrefix(r, "`````\n") {
		t.Errorf("expected 5-backtick fence, got prefix %q", r[:min(8, len(r))])
	}
	assertSingleBlock(t, content, r)
}

func TestCodeFence_NoBackslashArtifact(t *testing.T) {
	r := CodeFence("text", "```\ncode\n```")
	if strings.Contains(r, "\\```") {
		t.Errorf("CodeFence should not inject backslash escapes, got %q", r)
	}
}
