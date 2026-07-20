package session

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	gosync "sync"

	"github.com/betterleaks/betterleaks/detect"
	"github.com/betterleaks/betterleaks/regexp"
	"github.com/betterleaks/betterleaks/regexp/re2"
	"github.com/betterleaks/betterleaks/report"
)

// Chunk sizing mirrors the betterleaks CLI's own file source, which scans
// ~100KB fragments: the keyword prefilter runs per fragment, confining each
// triggered rule's regex to the chunk containing its keyword instead of the
// whole payload. The overlap is re-scanned across each boundary so a secret
// spanning a split (e.g. a multi-line PEM block) is still seen whole by one
// of the two chunks; only secrets longer than the overlap can be missed at a
// boundary.
const (
	redactChunkSize    = 100_000
	redactChunkOverlap = 10_000
)

// detector is the shared betterleaks secret detector. It is built once from the
// embedded default ruleset because compiling the several hundred built-in rules
// is expensive and the result is safe to reuse across sessions.
var (
	detector     *detect.Detector
	detectorOnce gosync.Once
	detectorErr  error

	// warnOnce rate-limits the detector-failure warning: in run mode redaction
	// runs on every autosave event, and a broken detector cannot heal at
	// runtime, so repeating the warning would just flood the log.
	warnOnce gosync.Once
)

// getDetector lazily constructs the shared detector with betterleaks' built-in
// ruleset. The first caller pays the compilation cost; subsequent callers reuse
// the same instance.
func getDetector() (*detect.Detector, error) {
	detectorOnce.Do(func() {
		// RE2 (C++ engine via WASM, no cgo) scans our keyword-dense content
		// 20-70x faster than Go's stdlib regexp. Rule compilation is lazy, so
		// setting the engine here, before the first detection, covers all rules.
		regexp.SetEngine(re2.RE2{})
		detector, detectorErr = detect.NewDetectorDefaultConfig()
	})
	return detector, detectorErr
}

// detectChunked scans content in newline-aligned ~100KB fragments with a
// small boundary overlap, instead of one DetectString call over the whole
// payload. Overlap can report the same secret twice; applyRedactions
// deduplicates via its already-replaced check.
func detectChunked(d *detect.Detector, content string) []report.Finding {
	if len(content) <= redactChunkSize {
		return d.DetectString(content)
	}

	var findings []report.Finding
	start := 0
	for {
		end := start + redactChunkSize
		if end >= len(content) {
			findings = append(findings, d.DetectString(content[start:])...)
			break
		}
		// Prefer splitting at a line boundary; hard-split single lines larger
		// than the chunk size.
		if nl := strings.LastIndexByte(content[start:end], '\n'); nl > 0 {
			end = start + nl + 1
		}
		findings = append(findings, d.DetectString(content[start:end])...)

		next := end - redactChunkOverlap
		if next <= start {
			// Guarantee forward progress when a chunk shrank below the overlap.
			next = end
		}
		start = next
	}
	return findings
}

// RedactContent replaces secrets detected by betterleaks with labelled
// placeholders of the form [REDACTED:<rule-id>], where <rule-id> is the
// betterleaks rule that matched (e.g. github-oauth, gcp-api-key). Detection uses
// betterleaks' built-in ruleset, which covers API keys, tokens, private keys,
// and other high-entropy credentials for many providers. The returned count is
// the number of distinct secret values that were replaced.
//
// If the detector cannot be initialised, content is returned unchanged so that
// history is still written rather than lost.
func RedactContent(content string) (string, int) {
	d, err := getDetector()
	if err != nil {
		warnOnce.Do(func() {
			slog.Warn("Secret redaction unavailable, writing content unredacted", "error", err)
		})
		return content, 0
	}
	return applyRedactions(content, detectChunked(d, content))
}

// applyRedactions replaces each finding's secret value with a labelled
// placeholder, returning the redacted content and the number of distinct
// secret values replaced. It is split from RedactContent so the replacement
// semantics can be tested with fabricated findings, independent of the
// betterleaks ruleset.
func applyRedactions(content string, findings []report.Finding) (string, int) {
	// Replace longest secrets first so a finding whose secret is a substring of
	// another finding's secret can never leave a partial secret behind (replacing
	// the shorter one first would split the longer match and leak its remainder).
	// The rule-ID tie-break makes the placeholder deterministic when two rules
	// match same-length secrets.
	sort.Slice(findings, func(i, j int) bool {
		if len(findings[i].Secret) != len(findings[j].Secret) {
			return len(findings[i].Secret) > len(findings[j].Secret)
		}
		return findings[i].RuleID < findings[j].RuleID
	})

	count := 0
	var rules []string
	for _, f := range findings {
		// Skip secrets already removed by an earlier replacement (the same value
		// matched by a second rule, or a substring of a longer secret) so the
		// count reflects actual replacements.
		if f.Secret == "" || !strings.Contains(content, f.Secret) {
			continue
		}
		content = strings.ReplaceAll(content, f.Secret, fmt.Sprintf("[REDACTED:%s]", f.RuleID))
		count++
		rules = append(rules, f.RuleID)
	}
	if count > 0 {
		// Log rule IDs only — never the secret values themselves.
		slog.Debug("Redacted secrets from markdown", "count", count, "rules", rules)
	}
	return content, count
}
