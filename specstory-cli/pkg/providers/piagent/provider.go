package piagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

const (
	providerID    = "pi"
	providerName  = "Pi"
	defaultCmd    = "pi"
	versionFlag   = "--version"
	notYetSupport = "pi: %s not yet supported for the pi provider (v1 ships sync/list/search/reindex only)"
)

// Provider implements spi.Provider for the pi coding agent.
// pi stores sessions as JSONL v3 trees under ~/.pi/agent/sessions/--<encoded-cwd>--/.
type Provider struct{}

// NewProvider returns a new pi provider instance.
func NewProvider() *Provider { return &Provider{} }

// Name returns the human-readable provider name.
func (p *Provider) Name() string { return providerName }

// Check verifies the pi binary is on PATH and reports its version.
func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName := strings.TrimSpace(customCommand)
	isCustom := cmdName != ""
	if cmdName == "" {
		cmdName = defaultCmd
	}
	resolved, err := exec.LookPath(cmdName)
	if err != nil {
		slog.Info("pi: Check binary not found", "command", cmdName, "error", err)
		trackCheckFailure(isCustom, cmdName, "", "not_found", err.Error())
		return spi.CheckResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("pi binary '%s' not found on PATH", cmdName),
		}
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(resolved, versionFlag)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		slog.Info("pi: Check version probe failed", "resolved", resolved, "error", err)
		trackCheckFailure(isCustom, cmdName, resolved, "version_probe_failed", err.Error())
		return spi.CheckResult{
			Success:      false,
			Location:     resolved,
			ErrorMessage: fmt.Sprintf("pi version probe failed: %v", err),
		}
	}
	version := strings.TrimSpace(stdout.String())
	if version == "" {
		version = "unknown"
	}
	trackCheckSuccess(isCustom, cmdName, resolved, version)
	return spi.CheckResult{Success: true, Version: version, Location: resolved}
}

// DetectAgent reports whether pi has created sessions for the given project.
func (p *Provider) DetectAgent(projectPath string, _ bool) bool {
	files, err := SessionFilesInProject(projectPath)
	if err != nil {
		slog.Debug("pi: DetectAgent error", "error", err)
		return false
	}
	return len(files) > 0
}

// ExecAgentAndWatch is the `specstory run pi` wrapper — out of v1 scope.
func (p *Provider) ExecAgentAndWatch(_ string, _ string, _ string, _ bool, _ func(*spi.AgentChatSession)) error {
	return fmt.Errorf(notYetSupport, "ExecAgentAndWatch (specstory run pi)")
}

// WatchAgent is live watch — out of v1 scope.
func (p *Provider) WatchAgent(_ context.Context, _ string, _ bool, _ func(*spi.AgentChatSession)) error {
	return fmt.Errorf(notYetSupport, "WatchAgent")
}

// ReconstructSession is out of v1 scope; pi has no native serializer yet.
func (p *Provider) ReconstructSession(_ *schema.SessionData, _ spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	return nil, errors.Join(spi.ErrReconstructionUnsupported, fmt.Errorf(notYetSupport, "ReconstructSession"))
}

// NativeSessionPath is out of v1 scope (no native serializer).
func (p *Provider) NativeSessionPath(_ string, _ string) (string, error) {
	return "", errors.Join(spi.ErrReconstructionUnsupported, fmt.Errorf(notYetSupport, "NativeSessionPath"))
}

// trackCheckSuccess emits the standard install-check success analytics event,
// matching the shape other providers use.
func trackCheckSuccess(custom bool, commandPath, resolvedPath, version string) {
	analytics.TrackEvent(analytics.EventCheckInstallSuccess, analytics.Properties{
		"provider":       providerID,
		"custom_command": custom,
		"command_path":   commandPath,
		"resolved_path":  resolvedPath,
		"version":        version,
		"version_flag":   versionFlag,
	})
}

// trackCheckFailure emits the standard install-check failure analytics event.
func trackCheckFailure(custom bool, commandPath, resolvedPath, errorType, message string) {
	analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
		"provider":       providerID,
		"custom_command": custom,
		"command_path":   commandPath,
		"resolved_path":  resolvedPath,
		"version_flag":   versionFlag,
		"error_type":     errorType,
		"error_message":  message,
	})
}
