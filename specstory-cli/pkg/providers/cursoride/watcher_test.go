package cursoride

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Saves must reach the callback one at a time and finish in queue order, even when an
// earlier save is slow or panics. Concurrent delivery would let a slow, older partial
// snapshot finish after the newer complete one and overwrite it.
func TestSessionDelivery_OrderedAndSerial(t *testing.T) {
	var (
		mu          sync.Mutex
		finished    []string
		inFlight    atomic.Int32
		maxInFlight atomic.Int32
	)
	w := &CursorIDEWatcher{
		sessionQueue: make(chan *spi.AgentChatSession, 64),
		sessionCallback: func(s *spi.AgentChatSession) {
			n := inFlight.Add(1)
			for {
				m := maxInFlight.Load()
				if n <= m || maxInFlight.CompareAndSwap(m, n) {
					break
				}
			}
			// Recorded on the way out, so the order is completion order.
			defer func() {
				inFlight.Add(-1)
				mu.Lock()
				finished = append(finished, s.SessionID)
				mu.Unlock()
			}()
			switch s.SessionID {
			case "partial":
				time.Sleep(50 * time.Millisecond) // e.g. a slow cloud sync
			case "panics":
				panic("callback failure")
			}
		},
	}

	w.startSessionDelivery()
	queued := []string{"partial", "complete", "panics", "after-panic"}
	for _, id := range queued {
		w.sessionQueue <- &spi.AgentChatSession{SessionID: id}
	}
	close(w.sessionQueue)
	w.callbackWg.Wait()

	if !slices.Equal(finished, queued) {
		t.Errorf("completion order: got %v, want %v", finished, queued)
	}
	if got := maxInFlight.Load(); got != 1 {
		t.Errorf("max concurrent callbacks: got %d, want 1", got)
	}
}

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
			// The last processed state is unverifiable, so re-save rather than risk
			// keeping stale markdown.
			name:    "fingerprint recovered after an earlier failure is updated",
			known:   composerWatermark{lastUpdatedAt: ts},
			exists:  true,
			current: composerWatermark{lastUpdatedAt: ts, fingerprint: 7},
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
		t.Fatalf("fingerprint must never be 0, which means \"fingerprinting failed\"")
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
