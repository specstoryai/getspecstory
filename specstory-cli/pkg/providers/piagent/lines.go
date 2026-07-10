package piagent

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Line-size sanity limits, mirroring claudecode/codex. bufio.Reader.ReadString
// has no line-size limit, so a 250MB cap guards against OOM from pathological or
// malicious files (a legitimate pi session line — a big tool result or base64
// image — can exceed the 16MB bufio.Scanner cap, so we do NOT use Scanner).
const (
	KB                    = 1024
	MB                    = 1024 * 1024
	maxReasonableLineSize = 250 * MB
)

// errStopRead is a sentinel a visit callback may return to stop iteration early
// (without error). readLines treats it as a clean stop.
var errStopRead = errors.New("stop reading")

// readLines reads a pi session file line-by-line via bufio.Reader (unbounded
// line size, unlike bufio.Scanner) and calls visit for each non-empty trimmed
// line. Lines exceeding maxReasonableLineSize are refused with an error to
// prevent OOM. Returning errStopRead from visit stops iteration cleanly.
func readLines(path string, visit func(line string) error) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("pi: opening session %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	reader := bufio.NewReader(f)
	lineNum := 0
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return fmt.Errorf("pi: reading line %d of %s: %w", lineNum+1, path, readErr)
		}
		line = strings.TrimSuffix(line, "\n")
		lineNum++
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if len(trimmed) > maxReasonableLineSize {
				slog.Warn("pi: line exceeds reasonable size limit",
					"lineNumber", lineNum, "sizeMB", len(trimmed)/MB,
					"limitMB", maxReasonableLineSize/MB, "file", filepath.Base(path))
				return fmt.Errorf("pi: line %d of %s exceeds %dMB (refusing to process potentially malformed file)",
					lineNum, path, maxReasonableLineSize/MB)
			}
			if vErr := visit(trimmed); vErr != nil {
				if errors.Is(vErr, errStopRead) {
					return nil
				}
				return vErr
			}
		}
		if readErr == io.EOF {
			return nil
		}
	}
}
