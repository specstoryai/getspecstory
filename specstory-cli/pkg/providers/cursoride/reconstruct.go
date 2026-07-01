package cursoride

import (
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// ReconstructSession is not yet supported for Cursor IDE.
// Cursor IDE stores sessions as key-value rows in a shared global SQLite database
// rather than per-session files, which requires a different insertion model than
// file-based providers. See docs/CURSOR-IDE.md for the planned approach.
func (p *Provider) ReconstructSession(_ *schema.SessionData, _ spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	return nil, spi.ErrReconstructionUnsupported
}

// NativeSessionPath is not yet supported for Cursor IDE.
// See ReconstructSession for the reason.
func (p *Provider) NativeSessionPath(_ string, _ string) (string, error) {
	return "", spi.ErrReconstructionUnsupported
}
