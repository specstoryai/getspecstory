package session

import (
	"strings"
	"testing"
)

func TestRenderRoleHeader_UserIsHeadingWithMarker(t *testing.T) {
	user := Message{Role: "user", Timestamp: "2026-06-19T05:04:33Z"}
	got := renderRoleHeader(user, true)

	if want := "### 👤 User (2026-06-19 05:04:33Z)\n\n"; got != want {
		t.Errorf("user header = %q, want %q", got, want)
	}
	// Default is plain Markdown (no inline HTML), which some readers (Quick Look)
	// would otherwise leak as a stray bracket.
	if strings.ContainsAny(got, "<>") {
		t.Errorf("default user header should be plain Markdown (no HTML), got %q", got)
	}
}

func TestRenderRoleHeader_UserColorOptIn(t *testing.T) {
	SetUserTurnColor("#2563eb")
	defer SetUserTurnColor("") // reset so other tests see the default

	user := Message{Role: "user", Timestamp: "2026-06-19T05:04:33Z"}
	got := renderRoleHeader(user, true)
	want := "### <span style=\"color:#2563eb\">👤 User (2026-06-19 05:04:33Z)</span>\n\n"
	if got != want {
		t.Errorf("colored user header = %q, want %q", got, want)
	}
}

func TestRenderRoleHeader_AgentStaysQuiet(t *testing.T) {
	agent := Message{Role: "agent", Model: "claude-opus-4-8", Timestamp: "2026-06-19T05:04:40Z"}
	got := renderRoleHeader(agent, true)
	if strings.HasPrefix(got, "#") {
		t.Errorf("agent header should not be a heading (user turns are the anchors), got %q", got)
	}
	if !strings.Contains(got, "🤖 Agent") {
		t.Errorf("agent header should carry the 🤖 marker, got %q", got)
	}
}
