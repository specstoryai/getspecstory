package session

import "testing"

func TestRenderRoleHeader_QueuedMarker(t *testing.T) {
	queued := Message{
		Role:      "user",
		Timestamp: "2026-06-16T06:39:53Z",
		Metadata:  map[string]interface{}{"queued": true},
	}
	header := renderRoleHeader(queued, true)
	if want := "_**User - queued, not sent (2026-06-16 06:39:53Z)**_\n\n"; header != want {
		t.Errorf("queued header = %q, want %q", header, want)
	}

	normal := Message{Role: "user", Timestamp: "2026-06-16T06:39:53Z"}
	if header := renderRoleHeader(normal, true); header != "_**User (2026-06-16 06:39:53Z)**_\n\n" {
		t.Errorf("non-queued user header should not carry the marker, got %q", header)
	}
}
