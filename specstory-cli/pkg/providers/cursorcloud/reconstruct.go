package cursorcloud

import (
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// ReconstructSession is unsupported: a cloud agent's conversation lives in
// Cursor's backend, and the only way to create one from a transcript is to
// launch a new paid cloud run.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	return nil, spi.ErrReconstructionUnsupported
}

// NativeSessionPath is unsupported for the same reason: there is no local
// native session file for a cloud agent to resume from.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	return "", spi.ErrReconstructionUnsupported
}

// SupportsReconstruction reports false, agreeing with the two methods above.
func (p *Provider) SupportsReconstruction() bool {
	return false
}
