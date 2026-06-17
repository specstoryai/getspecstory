package cursorcli

import (
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// ReconstructSession is not yet implemented for Cursor CLI. The provider carries
// the responsibility (it is on the Provider interface) but has no native
// serializer yet, so it reports the capability as unsupported.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	return nil, spi.ErrReconstructionUnsupported
}

// NativeSessionPath is not implemented for Cursor CLI; reconstruction is unsupported.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	return "", spi.ErrReconstructionUnsupported
}
