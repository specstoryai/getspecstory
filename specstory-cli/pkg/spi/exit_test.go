package spi

import (
	"errors"
	"fmt"
	"testing"
)

// TestAgentExitError_UnwrapsThroughWrapping asserts the CLI can recover the
// agent's exit status from an error a provider wrapped on the way up.
func TestAgentExitError_UnwrapsThroughWrapping(t *testing.T) {
	wrapped := fmt.Errorf("run failed: %w", &AgentExitError{Agent: "Pi", Code: 7})

	var agentExit *AgentExitError
	if !errors.As(wrapped, &agentExit) {
		t.Fatalf("errors.As did not find AgentExitError in %v", wrapped)
	}
	if agentExit.Code != 7 {
		t.Fatalf("Code = %d, want 7", agentExit.Code)
	}
	if got, want := agentExit.Error(), "Pi exited with status 7"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
