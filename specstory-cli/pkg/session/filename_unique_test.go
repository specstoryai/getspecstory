package session

import (
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// A /branch fork shares the parent's start time + first message (→ same slug),
// so without the session-id suffix the two would collide on one filename.
func TestBuildSessionFilePath_BranchDoesNotCollide(t *testing.T) {
	parent := &spi.AgentChatSession{
		SessionID: "26791918-7e65-4217-878d-c33751b1cce9",
		CreatedAt: "2026-06-19T05:04:33.976Z",
		Slug:      "so-you-can-read",
	}
	branch := &spi.AgentChatSession{
		SessionID: "2e951ffb-1111-2222-3333-444455556666",
		CreatedAt: "2026-06-19T05:04:33.976Z",
		Slug:      "so-you-can-read",
	}

	p := BuildSessionFilePath(parent, "/h", true)
	b := BuildSessionFilePath(branch, "/h", true)

	if p == b {
		t.Fatalf("branch collided with parent: both -> %s", p)
	}
	if !strings.HasSuffix(p, "-26791918.md") {
		t.Errorf("parent path missing session-id suffix: %s", p)
	}
	if !strings.HasSuffix(b, "-2e951ffb.md") {
		t.Errorf("branch path missing session-id suffix: %s", b)
	}
}
