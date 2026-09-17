package cmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/config"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
)

// TestEveryRegisteredProviderHasACommandOverride is the registry-side half of
// config.TestProvidersConfigIsFullyWired. That test proves every ProvidersConfig field
// reaches a provider; this one proves every registered provider has a field at all —
// the direction the Pi provider failed, shipping run and watch support while
// GetProviderCmd had no "pi" case, so pi_cmd in config.toml was silently ignored.
//
// It lives here rather than in pkg/config because only this package already depends on
// the registry; pkg/config must not, or building a config would drag in every provider.
func TestEveryRegisteredProviderHasACommandOverride(t *testing.T) {
	// Fill every field so a wired provider yields its sentinel and an unwired one still
	// falls through GetProviderCmd's default arm to "".
	filled := config.ProvidersConfig{}
	val := reflect.ValueOf(&filled).Elem()
	for i := range val.NumField() {
		if val.Field(i).Kind() == reflect.String {
			val.Field(i).SetString("sentinel")
		}
	}
	cfg := &config.Config{Providers: filled}

	for _, id := range factory.GetRegistry().ListIDs() {
		t.Run(id, func(t *testing.T) {
			if cfg.GetProviderCmd(id) == "" {
				t.Errorf("GetProviderCmd(%q) is empty with every ProvidersConfig field set: %q has no command override, so %s_cmd in config.toml would be silently ignored",
					id, id, strings.ReplaceAll(id, "-", "_"))
			}
		})
	}
}

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

func TestCheckFailurePresentation(t *testing.T) {
	for _, tt := range []struct {
		name, errorType, customCmd string
		summary, informational     bool
	}{
		{"optional absent agent", spi.CheckErrorNotFound, "", true, true},
		{"explicit absent agent", spi.CheckErrorNotFound, "", false, true},
		{"missing custom command", spi.CheckErrorNotFound, "/missing/agent", false, false},
		{"permission denied", spi.CheckErrorPermissionDenied, "", true, false},
		{"failed version probe", spi.CheckErrorUnknown, "", true, false},
		{"empty version output", spi.CheckErrorNoOutput, "", true, false},
		{"invalid version output", spi.CheckErrorUnexpectedOutput, "", true, false},
		{"unclassified failure", "", "", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := spi.CheckResult{ErrorType: tt.errorType, ErrorMessage: "Reason\nRemediation"}
			output := formatCheckFailure("Agent", result, tt.customCmd, tt.summary)
			if strings.Contains(output, "ℹ️") != tt.informational || strings.Contains(output, "❌") == tt.informational {
				t.Fatalf("wrong severity: %s", output)
			}
			if tt.summary {
				if strings.Contains(output, "Remediation") {
					t.Fatalf("summary includes full instructions: %s", output)
				}
				if tt.informational && (strings.Contains(output, "Error:") || !strings.Contains(output, "Optional")) {
					t.Fatalf("missing optional agent reads as an error: %s", output)
				}
			} else if !strings.Contains(output, "Remediation") {
				t.Fatalf("detailed check lost help: %s", output)
			}
		})
	}
}
