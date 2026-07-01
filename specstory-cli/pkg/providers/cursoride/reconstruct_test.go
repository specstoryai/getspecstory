package cursoride

import (
	"errors"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// TestReconstructSession_Unsupported mirrors the ReconstructSession tests in other
// providers, adjusted for the fact that Cursor IDE reconstruction is not yet supported.
// Sessions live in a shared global SQLite database (not per-session files), so callers
// must receive ErrReconstructionUnsupported and not a silent nil.
func TestReconstructSession_Unsupported(t *testing.T) {
	data := &schema.SessionData{
		SessionID: "test-id",
		Exchanges: []schema.Exchange{
			{Messages: []schema.Message{
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "hello"}}},
			}},
		},
	}
	_, err := NewProvider().ReconstructSession(data, spi.ReconstructOptions{})
	if !errors.Is(err, spi.ErrReconstructionUnsupported) {
		t.Errorf("ReconstructSession: expected ErrReconstructionUnsupported, got %v", err)
	}
}

// TestReconstructSession_Empty mirrors the same test in every other provider.
func TestReconstructSession_Empty(t *testing.T) {
	_, err := NewProvider().ReconstructSession(&schema.SessionData{SessionID: "x"}, spi.ReconstructOptions{})
	if !errors.Is(err, spi.ErrReconstructionUnsupported) {
		t.Errorf("ReconstructSession (empty): expected ErrReconstructionUnsupported, got %v", err)
	}
}

// TestNativeSessionPath_Unsupported mirrors TestNativeSessionPath in other providers.
// Cursor IDE has no per-session file path until reconstruction is implemented.
func TestNativeSessionPath_Unsupported(t *testing.T) {
	_, err := NewProvider().NativeSessionPath("/some/project", "abc.db")
	if !errors.Is(err, spi.ErrReconstructionUnsupported) {
		t.Errorf("NativeSessionPath: expected ErrReconstructionUnsupported, got %v", err)
	}
}
