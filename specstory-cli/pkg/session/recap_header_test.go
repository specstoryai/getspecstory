package session

import (
	"strings"
	"testing"
)

func TestRenderRoleHeader_Recap(t *testing.T) {
	recap := Message{
		Role:      "agent",
		Timestamp: "2026-06-16T04:12:16Z",
		Metadata:  map[string]interface{}{"recap": true},
	}
	if got := renderRoleHeader(recap, true); got != "_**Recap (2026-06-16 04:12:16Z)**_\n\n" {
		t.Errorf("recap header = %q", got)
	}

	normal := Message{Role: "agent", Timestamp: "2026-06-16T04:12:16Z", Model: "claude"}
	if got := renderRoleHeader(normal, true); strings.Contains(got, "Recap") {
		t.Errorf("non-recap agent header should not say Recap, got %q", got)
	}
}
