package qwencode

import (
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Compile-time assertion that Provider satisfies the full spi.Provider contract.
// The interface conformance is otherwise only exercised at registration in the
// factory package, so a locally missing method fails there rather than here;
// this guard surfaces the gap in the provider's own package.
var _ spi.Provider = (*Provider)(nil)

// ReconstructSession is not implemented for Qwen Code yet. Qwen Code resumes
// from the same JSONL transcripts this provider parses (~/.qwen/projects/
// <sanitized-cwd>/chats/<session-id>.jsonl), so a serializer is feasible in
// principle — it must reproduce the record envelope (uuid/parentUuid chain,
// sessionId, timestamps, provenance) faithfully enough for `qwen --resume` to
// accept the file. Until that serializer is written and verified against a
// live Qwen Code resume, this provider declines to reconstruct rather than
// risk writing transcripts Qwen Code rejects.
func (p *Provider) ReconstructSession(data *schema.SessionData, opts spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	return nil, spi.ErrReconstructionUnsupported
}

// NativeSessionPath is unsupported for the same reason as ReconstructSession:
// without a serializer there is no reconstructed file whose native location
// this provider could resolve.
func (p *Provider) NativeSessionPath(projectPath string, filename string) (string, error) {
	return "", spi.ErrReconstructionUnsupported
}

// SupportsReconstruction reports false: see ReconstructSession above — no
// serializer exists yet that produces a session Qwen Code will actually
// resume, so this provider must not be offered as a cross-agent or cloud
// resume target.
func (p *Provider) SupportsReconstruction() bool {
	return false
}
