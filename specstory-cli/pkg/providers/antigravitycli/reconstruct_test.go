package antigravitycli

import (
	"errors"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Antigravity resume rebuilds context from the per-conversation SQLite store, not
// the transcript this provider writes, so reconstruction is intentionally
// unsupported (see reconstruct.go). Both methods must report that consistently:
// ReconstructSession returns (nil, ErrReconstructionUnsupported) and
// NativeSessionPath returns ("", ErrReconstructionUnsupported).
func TestReconstructSession_Unsupported(t *testing.T) {
	data := &schema.SessionData{SessionID: "conv-1"}
	got, err := NewProvider().ReconstructSession(data, spi.ReconstructOptions{})
	if !errors.Is(err, spi.ErrReconstructionUnsupported) {
		t.Errorf("expected ErrReconstructionUnsupported, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil reconstructed session, got %+v", got)
	}
}

func TestNativeSessionPath_Unsupported(t *testing.T) {
	got, err := NewProvider().NativeSessionPath("/proj", "session.json")
	if !errors.Is(err, spi.ErrReconstructionUnsupported) {
		t.Errorf("expected ErrReconstructionUnsupported, got %v", err)
	}
	if got != "" {
		t.Errorf("expected empty path, got %q", got)
	}
}
