package session

import (
	"fmt"
	"log/slog"
	"strings"
	gosync "sync"

	"github.com/betterleaks/betterleaks/detect"
)

// detector is the shared betterleaks secret detector. It is built once from the
// embedded default ruleset because compiling the several hundred built-in rules
// is expensive and the result is safe to reuse across sessions.
var (
	detector     *detect.Detector
	detectorOnce gosync.Once
	detectorErr  error
)

// getDetector lazily constructs the shared detector with betterleaks' built-in
// ruleset. The first caller pays the compilation cost; subsequent callers reuse
// the same instance.
func getDetector() (*detect.Detector, error) {
	detectorOnce.Do(func() {
		detector, detectorErr = detect.NewDetectorDefaultConfig()
	})
	return detector, detectorErr
}

// RedactContent replaces secrets detected by betterleaks with labelled
// placeholders of the form [REDACTED:<rule-id>], where <rule-id> is the
// betterleaks rule that matched (e.g. github-oauth, gcp-api-key). Detection uses
// betterleaks' built-in ruleset, which covers API keys, tokens, private keys,
// and other high-entropy credentials for many providers.
//
// If the detector cannot be initialised, content is returned unchanged so that
// history is still written rather than lost.
func RedactContent(content string) string {
	d, err := getDetector()
	if err != nil {
		slog.Warn("Secret redaction unavailable, writing content unredacted", "error", err)
		return content
	}

	// Replace each detected secret by value. betterleaks findings do not overlap
	// in practice, so a value replacement is order-independent and avoids having
	// to reconcile line/column offsets against the source string.
	for _, f := range d.DetectString(content) {
		if f.Secret == "" {
			continue
		}
		content = strings.ReplaceAll(content, f.Secret, fmt.Sprintf("[REDACTED:%s]", f.RuleID))
	}
	return content
}
