package antigravitycli

import (
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Compile-time assertion that Provider satisfies the full spi.Provider contract.
// The interface conformance is otherwise only exercised at registration in the
// factory package, so a locally missing method fails there rather than here;
// this guard surfaces the gap in the provider's own package.
var _ spi.Provider = (*Provider)(nil)

// ReconstructSession is unsupported for Antigravity. On resume, `agy` rebuilds
// conversation state from the per-conversation SQLite store
// (conversations/<id>.db, protobuf step payloads) — NOT from the plaintext
// transcript this provider parses (verified empirically; see
// docs/antigravity-format-spec.md §1.1). A resumable reconstruction would
// therefore have to synthesize that protobuf-in-SQLite store, so the
// text-serializer approach the other providers use cannot produce a session
// `agy` will actually resume. Until such a serializer exists, this provider
// carries the interface method but declines to reconstruct.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	return nil, spi.ErrReconstructionUnsupported
}

// NativeSessionPath is unsupported for the same reason as ReconstructSession:
// there is no plaintext session file this provider can write that `agy` would
// resume from, so there is no native path to resolve.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	return "", spi.ErrReconstructionUnsupported
}
