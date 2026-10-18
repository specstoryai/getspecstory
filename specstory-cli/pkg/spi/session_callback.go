package spi

import "log/slog"

// DispatchSession hands session to cb on its own goroutine, recovering from a
// panic so that one malformed session cannot take down a running watcher.
// label names the provider in the panic log.
//
// Delivery is fire-and-forget: a watcher that must wait for in-flight callbacks
// before shutting down needs its own WaitGroup around cb instead.
func DispatchSession(label string, cb func(*AgentChatSession), session *AgentChatSession) {
	if cb == nil || session == nil {
		return
	}
	go DeliverSession(label, cb, session)
}

// DeliverSession invokes a callback synchronously and contains consumer panics.
// Watchers with one worker use it to preserve ordering and join all saves before
// returning an agent's exit status.
func DeliverSession(label string, cb func(*AgentChatSession), session *AgentChatSession) {
	if cb == nil || session == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Error(label+": session callback panicked", "panic", r)
		}
	}()
	cb(session)
}
