package cmd

import (
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
)

func TestResolveProviderIDs(t *testing.T) {
	registry := factory.GetRegistry()

	tests := []struct {
		name          string
		args          []string
		providersFlag []string
		wantIDs       []string // nil means "all providers"
		wantErrSubstr string   // non-empty means an error is expected containing this substring
	}{
		// ── Neither specified ───────────────────────────────────────────────────
		{
			name:    "neither arg nor flag returns nil",
			wantIDs: nil,
		},

		// ── Positional arg ──────────────────────────────────────────────────────
		{
			name:    "positional arg returned as-is without validation",
			args:    []string{"claude"},
			wantIDs: []string{"claude"},
		},
		{
			// Callers handle validation for positional args, so even unknown values
			// should pass through.
			name:    "unknown positional arg passed through without error",
			args:    []string{"unknown-provider"},
			wantIDs: []string{"unknown-provider"},
		},

		// ── Conflict ────────────────────────────────────────────────────────────
		{
			name:          "positional arg and providers flag together is an error",
			args:          []string{"claude"},
			providersFlag: []string{"codex"},
			wantErrSubstr: "cannot use both",
		},

		// ── --providers flag: happy paths ────────────────────────────────────────
		{
			name:          "single valid provider",
			providersFlag: []string{"claude"},
			wantIDs:       []string{"claude"},
		},
		{
			name:          "multiple valid providers preserves order",
			providersFlag: []string{"codex", "claude"},
			wantIDs:       []string{"codex", "claude"},
		},
		{
			name:          "mixed case is normalised to lower",
			providersFlag: []string{"Claude", "CODEX"},
			wantIDs:       []string{"claude", "codex"},
		},
		{
			name:          "leading and trailing whitespace is trimmed",
			providersFlag: []string{"  claude  ", " codex"},
			wantIDs:       []string{"claude", "codex"},
		},

		// ── Deduplication ────────────────────────────────────────────────────────
		{
			name:          "exact duplicate is removed keeping first occurrence",
			providersFlag: []string{"claude", "codex", "claude"},
			wantIDs:       []string{"claude", "codex"},
		},
		{
			name:          "case-variant duplicate is removed after normalisation",
			providersFlag: []string{"Claude", "claude"},
			wantIDs:       []string{"claude"},
		},
		{
			name:          "whitespace-variant duplicate is removed after trimming",
			providersFlag: []string{"claude", "  claude  "},
			wantIDs:       []string{"claude"},
		},
		{
			name:          "all duplicates collapsed to single entry",
			providersFlag: []string{"gemini", "GEMINI", "  gemini  "},
			wantIDs:       []string{"gemini"},
		},
		{
			name:          "three providers with one duplicate preserves remaining order",
			providersFlag: []string{"cursor", "claude", "cursor", "codex"},
			wantIDs:       []string{"cursor", "claude", "codex"},
		},

		// ── Empty / blank entries ─────────────────────────────────────────────────
		{
			name:          "blank entries in flag slice are silently skipped",
			providersFlag: []string{"", "  ", "claude"},
			wantIDs:       []string{"claude"},
		},
		{
			name:          "only blank entries is an error",
			providersFlag: []string{"", "  "},
			wantErrSubstr: "--providers requires at least one",
		},

		// ── Invalid provider ID ───────────────────────────────────────────────────
		{
			name:          "unknown provider ID is an error",
			providersFlag: []string{"notaprovider"},
			wantErrSubstr: "not a valid provider ID",
		},
		{
			name:          "unknown provider mixed with valid is still an error",
			providersFlag: []string{"claude", "notaprovider"},
			wantErrSubstr: "not a valid provider ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids, err := ResolveProviderIDs(registry, tt.args, tt.providersFlag)

			if tt.wantErrSubstr != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErrSubstr)
					return
				}
				if !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrSubstr)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(ids) != len(tt.wantIDs) {
				t.Errorf("got %v, want %v", ids, tt.wantIDs)
				return
			}
			for i := range tt.wantIDs {
				if ids[i] != tt.wantIDs[i] {
					t.Errorf("ids[%d] = %q, want %q", i, ids[i], tt.wantIDs[i])
				}
			}
		})
	}
}
