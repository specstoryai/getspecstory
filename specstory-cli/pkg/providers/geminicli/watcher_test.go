package geminicli

import (
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// A panic in the consumer's callback must not escape delivery: it would unwind
// the fsnotify event goroutine and take the process down over one bad session.
func TestTriggerCallback_ContainsConsumerPanic(t *testing.T) {
	var called bool
	SetWatcherCallback(func(*spi.AgentChatSession) {
		called = true
		panic("consumer blew up")
	})
	t.Cleanup(func() { SetWatcherCallback(nil) })

	triggerCallback(&spi.AgentChatSession{SessionID: "s-1"})

	if !called {
		t.Fatal("callback was never invoked")
	}
}

// Delivery is skipped rather than panicking when either side is missing.
func TestTriggerCallback_NilSafe(t *testing.T) {
	SetWatcherCallback(nil)
	triggerCallback(&spi.AgentChatSession{SessionID: "s-1"})

	var called bool
	SetWatcherCallback(func(*spi.AgentChatSession) { called = true })
	t.Cleanup(func() { SetWatcherCallback(nil) })

	triggerCallback(nil)

	if called {
		t.Error("callback invoked for a nil session")
	}
}
