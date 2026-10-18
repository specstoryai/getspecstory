package redact

import (
	"strings"
	"testing"

	"github.com/betterleaks/betterleaks/report"
)

// The fixtures below are syntactically valid but fabricated credentials chosen
// because they are high-entropy enough to clear betterleaks' entropy gates and
// therefore detect reliably. betterleaks intentionally ignores obviously fake,
// low-entropy strings (e.g. sequential ABCDEF... tokens) to avoid false
// positives, so tests must use realistic-looking values.
const (
	fakeGitHubOAuth = "gho_16C7e42F292c6912E7710c838347Ae178B4a"
	fakeGCPAPIKey   = "AIzaSyD8xKq2mL9nP4rT7wZ0aB3cE6fH1jG5kM7"
)

// fakePEM is a fabricated multi-line private key: real header/footer, body of
// realistic-looking base64. Multi-line so it can straddle chunk boundaries.
const fakePEM = "-----BEGIN RSA PRIVATE KEY-----\n" +
	"MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKj\n" +
	"MzEfYyjiWA4R4/M2bS1GB4t7NXp98C3SC6dVMvDuictGeurT8jNbvJZHtCSuYEvu\n" +
	"-----END RSA PRIVATE KEY-----"

func TestRedactContent(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantContains []string // placeholders that must appear in the output
		wantAbsent   []string // secret values that must not survive redaction
		wantCount    int      // distinct secret values replaced
		// When both want slices are empty, the input must pass through unchanged.
	}{
		{
			name:         "GitHub OAuth token",
			input:        "token=" + fakeGitHubOAuth,
			wantContains: []string{"[REDACTED:github-oauth]"},
			wantAbsent:   []string{fakeGitHubOAuth},
			wantCount:    1,
		},
		{
			name:         "Google API key",
			input:        "key=" + fakeGCPAPIKey,
			wantContains: []string{"[REDACTED:gcp-api-key]"},
			wantAbsent:   []string{fakeGCPAPIKey},
			wantCount:    1,
		},
		{
			name:         "multiple secrets in one string",
			input:        "oauth: " + fakeGitHubOAuth + " and google: " + fakeGCPAPIKey,
			wantContains: []string{"[REDACTED:github-oauth]", "[REDACTED:gcp-api-key]"},
			wantAbsent:   []string{fakeGitHubOAuth, fakeGCPAPIKey},
			wantCount:    2,
		},
		{
			name:  "no secrets",
			input: "This is a normal conversation with no secrets.",
		},
		{
			name:  "empty input",
			input: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, count := RedactContent(tt.input)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("RedactContent(%q) = %q, want it to contain %q", tt.input, got, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("RedactContent(%q) = %q, secret %q still present", tt.input, got, absent)
				}
			}
			if count != tt.wantCount {
				t.Errorf("RedactContent(%q) count = %d, want %d", tt.input, count, tt.wantCount)
			}
			if len(tt.wantContains) == 0 && len(tt.wantAbsent) == 0 && got != tt.input {
				t.Errorf("RedactContent(%q) = %q, want unchanged", tt.input, got)
			}
		})
	}
}

// TestRedactContent_Chunking exercises the ~100KB chunked scanning path:
// secrets positioned to straddle chunk boundaries must still be caught via
// the boundary overlap, in both the newline-split and hard-split (single
// huge line) cases.
func TestRedactContent_Chunking(t *testing.T) {
	// Multi-line content: ~99.5KB of padding lines puts the PEM astride the
	// first newline-aligned chunk boundary.
	paddingLine := strings.Repeat("plain filler text ", 5) + "\n" // 91 bytes
	multiLine := strings.Repeat(paddingLine, 1094) +              // ~99.5KB
		fakePEM + "\n" +
		strings.Repeat(paddingLine, 550) // ~50KB tail

	// Single-line content (no newlines anywhere): forces the hard-split
	// fallback. One token sits mid-chunk, another straddles the split offset.
	hugeLine := strings.Repeat("x", 99_990) +
		" " + fakeGitHubOAuth + " " + // straddles the 100KB hard split
		strings.Repeat("x", 19_000) +
		" " + fakeGCPAPIKey + " " + // mid-chunk in the second fragment
		strings.Repeat("x", 30_000)

	// Content just past the chunk size: after the first ~100KB chunk and the
	// overlap step-back, only a small tail fragment remains — the secret at
	// the very end must be caught by that final content[start:] scan.
	justOverChunkSize := strings.Repeat(paddingLine, 1100) + // ~100.1KB
		fakeGitHubOAuth

	// A short first line followed by a huge single line: the newline-aligned
	// first chunk is smaller than the overlap, so the overlap step-back would
	// move backwards — this drives the forward-progress guard (next = end).
	// If the guard ever regresses, this case hangs rather than fails, which
	// the test timeout converts into a failure. The trailing secret proves
	// scanning still reaches the end of the content past the guard.
	shortLineThenHuge := strings.Repeat("intro ", 100) + "\n" + // ~600B first line
		strings.Repeat("y", 210_000) +
		" " + fakeGCPAPIKey

	tests := []struct {
		name         string
		input        string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "multi-line secret straddles newline-split boundary",
			input:        multiLine,
			wantContains: []string{"[REDACTED:private-key]"},
			wantAbsent:   []string{fakePEM},
		},
		{
			name:         "secrets in and across hard-split huge line",
			input:        hugeLine,
			wantContains: []string{"[REDACTED:github-oauth]", "[REDACTED:gcp-api-key]"},
			wantAbsent:   []string{fakeGitHubOAuth, fakeGCPAPIKey},
		},
		{
			name:         "secret in small tail chunk just past chunk size",
			input:        justOverChunkSize,
			wantContains: []string{"[REDACTED:github-oauth]"},
			wantAbsent:   []string{fakeGitHubOAuth},
		},
		{
			name:         "tiny first chunk drives forward-progress guard",
			input:        shortLineThenHuge,
			wantContains: []string{"[REDACTED:gcp-api-key]"},
			wantAbsent:   []string{fakeGCPAPIKey},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, count := RedactContent(tt.input)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("chunked RedactContent missing %q in output", want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("chunked RedactContent left secret %q in output", absent)
				}
			}
			if count != len(tt.wantContains) {
				t.Errorf("count = %d, want %d", count, len(tt.wantContains))
			}
		})
	}
}

// TestApplyRedactions exercises the replacement semantics with fabricated
// findings, independent of the betterleaks ruleset: overlap ordering,
// duplicate-value dedup, and the empty-secret guard.
func TestApplyRedactions(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		findings  []report.Finding
		want      string
		wantCount int
	}{
		{
			name:    "substring secret cannot split a longer secret",
			content: "key=abcSECRETxyz",
			// Shorter finding listed first to prove longest-first sorting, not
			// input order, decides replacement order.
			findings: []report.Finding{
				{RuleID: "short-rule", Secret: "SECRET"},
				{RuleID: "long-rule", Secret: "abcSECRETxyz"},
			},
			want:      "key=[REDACTED:long-rule]",
			wantCount: 1,
		},
		{
			name:    "same secret matched by two rules counts once",
			content: "token=AAABBBCCC",
			findings: []report.Finding{
				{RuleID: "rule-b", Secret: "AAABBBCCC"},
				{RuleID: "rule-a", Secret: "AAABBBCCC"},
			},
			// Equal-length secrets tie-break on rule ID for deterministic output.
			want:      "token=[REDACTED:rule-a]",
			wantCount: 1,
		},
		{
			name:    "repeated secret value redacted everywhere",
			content: "first=SEC123 second=SEC123",
			findings: []report.Finding{
				{RuleID: "rule", Secret: "SEC123"},
			},
			want:      "first=[REDACTED:rule] second=[REDACTED:rule]",
			wantCount: 1,
		},
		{
			name:    "empty secret is ignored",
			content: "nothing to redact",
			findings: []report.Finding{
				{RuleID: "rule", Secret: ""},
			},
			want:      "nothing to redact",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, count := applyRedactions(tt.content, tt.findings)
			if got != tt.want {
				t.Errorf("applyRedactions(%q) = %q, want %q", tt.content, got, tt.want)
			}
			if count != tt.wantCount {
				t.Errorf("applyRedactions(%q) count = %d, want %d", tt.content, count, tt.wantCount)
			}
		})
	}
}
