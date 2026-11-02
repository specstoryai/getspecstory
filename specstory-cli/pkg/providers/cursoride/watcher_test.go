package cursoride

import "testing"

// composerChange is the whole decision of whether watch re-saves a Cursor session, so
// each case pins one way a session can (or must not) be picked up.
func TestComposerChange(t *testing.T) {
	const ts = int64(1793629548883)

	tests := []struct {
		name    string
		known   composerWatermark
		exists  bool
		current composerWatermark
		want    composerChangeKind
	}{
		{
			name:    "unknown composer is new",
			exists:  false,
			current: composerWatermark{lastUpdatedAt: ts, fingerprint: 7},
			want:    changeNew,
		},
		{
			name:    "timestamp advanced is updated",
			known:   composerWatermark{lastUpdatedAt: ts, fingerprint: 7},
			exists:  true,
			current: composerWatermark{lastUpdatedAt: ts + 1, fingerprint: 7},
			want:    changeUpdated,
		},
		{
			// Cursor >= 3.27.14 streams the agent's reply without bumping lastUpdatedAt.
			name:    "content changed under the same timestamp is updated",
			known:   composerWatermark{lastUpdatedAt: ts, fingerprint: 7},
			exists:  true,
			current: composerWatermark{lastUpdatedAt: ts, fingerprint: 8},
			want:    changeUpdated,
		},
		{
			name:    "nothing changed",
			known:   composerWatermark{lastUpdatedAt: ts, fingerprint: 7},
			exists:  true,
			current: composerWatermark{lastUpdatedAt: ts, fingerprint: 7},
			want:    changeNone,
		},
		{
			// Seeded sessions must not be re-emitted on the first check after startup.
			name:    "seeded session adopts its fingerprint without emitting",
			known:   composerWatermark{lastUpdatedAt: ts},
			exists:  true,
			current: composerWatermark{lastUpdatedAt: ts, fingerprint: 7},
			want:    changeAdoptFingerprint,
		},
		{
			name:    "seeded session whose timestamp advanced is updated",
			known:   composerWatermark{lastUpdatedAt: ts},
			exists:  true,
			current: composerWatermark{lastUpdatedAt: ts + 1, fingerprint: 7},
			want:    changeUpdated,
		},
		{
			name:    "failed fingerprint is not mistaken for a content change",
			known:   composerWatermark{lastUpdatedAt: ts, fingerprint: 7},
			exists:  true,
			current: composerWatermark{lastUpdatedAt: ts},
			want:    changeNone,
		},
		{
			name:    "failed fingerprint still honors an advanced timestamp",
			known:   composerWatermark{lastUpdatedAt: ts, fingerprint: 7},
			exists:  true,
			current: composerWatermark{lastUpdatedAt: ts + 1},
			want:    changeUpdated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := composerChange(tt.known, tt.exists, tt.current); got != tt.want {
				t.Errorf("composerChange() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The fingerprint must be stable for identical data and must move when a bubble is
// updated in place, which is how Cursor finishes a tool call without adding a bubble.
func TestConversationFingerprint(t *testing.T) {
	conversation := func(status string) []ComposerConversation {
		return []ComposerConversation{
			{BubbleID: "u1", Type: 1, Text: "How about a Hello World in D?"},
			{BubbleID: "u1/s:0", Type: 2, ToolFormerData: &BubbleConversation{Name: "edit_file_v2", Status: status}},
		}
	}

	loading := conversationFingerprint("c", conversation("loading"))
	if loading == 0 {
		t.Fatalf("fingerprint must never be 0, which means \"not fingerprinted yet\"")
	}

	tests := []struct {
		name     string
		other    []ComposerConversation
		wantSame bool
	}{
		{name: "identical conversation", other: conversation("loading"), wantSame: true},
		{name: "tool call completed in place", other: conversation("completed"), wantSame: false},
		{name: "bubble appended", other: append(conversation("loading"), ComposerConversation{BubbleID: "u1/s:1", Type: 2, Text: "Done."}), wantSame: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := conversationFingerprint("c", tt.other)
			if (got == loading) != tt.wantSame {
				t.Errorf("fingerprint equal = %v, want %v", got == loading, tt.wantSame)
			}
		})
	}
}
