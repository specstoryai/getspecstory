package spi

import "fmt"

// AgentExitError reports that the agent launched by ExecAgentAndWatch exited
// with a non-zero status of its own. It is the agent's status, not a specstory
// failure: the provider returns it only after its watcher has stopped and every
// in-flight session save has been joined, and the CLI then exits with Code so
// the user's shell sees the agent's status unchanged. The CLI prints nothing
// for it (the agent already reported its own error).
//
// Returning this instead of calling os.Exit inside the exec helper is what keeps
// the session the agent wrote just before failing: os.Exit there leaves the
// process before ExecAgentAndWatch can stop the watcher, and the save for that
// last write never runs.
type AgentExitError struct {
	Agent string // human-readable agent name, for logs
	Code  int    // the agent's exit status
}

// Error implements error.
func (e *AgentExitError) Error() string {
	return fmt.Sprintf("%s exited with status %d", e.Agent, e.Code)
}
