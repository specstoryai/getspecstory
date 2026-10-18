package spi

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// LookPathForCheck resolves a probe executable without hiding permission errors
// behind Unix LookPath's final ErrNotFound when no PATH candidate is executable.
// A runnable later candidate still wins, exactly as it does for exec.LookPath.
func LookPathForCheck(command string) (string, error) {
	path, err := exec.LookPath(command)
	if runtime.GOOS == "windows" || !errors.Is(err, exec.ErrNotFound) || strings.ContainsRune(command, '/') {
		return path, err
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, command)
		// Ensure a relative PATH entry is treated as a path, not another PATH lookup.
		candidate, absErr := filepath.Abs(candidate)
		if absErr != nil {
			continue
		}
		if _, candidateErr := exec.LookPath(candidate); candidateErr != nil && ClassifyCheckError(candidateErr) != CheckErrorNotFound {
			return "", candidateErr
		}
	}
	return path, err
}

// Error types reported by ClassifyCheckError. They are also the values a
// provider reports to analytics, so they are stable identifiers rather than
// display text.
const (
	CheckErrorNotFound         = "not_found"         // the agent binary or IDE storage was not found
	CheckErrorPermissionDenied = "permission_denied" // access to the binary or IDE storage is denied
	CheckErrorUnknown          = "unknown"           // the probe failed for a reason worth reading error_message for

	// CheckErrorNoOutput is reported by a provider directly rather than by
	// ClassifyCheckError: the probe exited cleanly but printed no version, which
	// is a successful run producing an unusable result, not an error to classify.
	CheckErrorNoOutput = "no_output"

	// CheckErrorUnexpectedOutput means the probe output did not identify the expected agent.
	CheckErrorUnexpectedOutput = "unexpected_output"
)

// ClassifyCheckError buckets a lookup or storage failure during Provider.Check
// into one of the CheckError* types, so that the caller can
// choose remediation advice and report a consistent error type to analytics.
// A nil error classifies as "" — nothing failed.
//
// CheckErrorUnknown is the residual: it means the failure could not be
// attributed to a missing binary or a permission problem, so error_message is
// the only thing that explains it. Callers may also treat it as "not fatal,
// worth retrying differently" — codexcli uses it that way to decide whether to
// try another version flag.
func ClassifyCheckError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, os.ErrNotExist):
		return CheckErrorNotFound
	case errors.Is(err, os.ErrPermission):
		return CheckErrorPermissionDenied
	default:
		return CheckErrorUnknown
	}
}

// ClassifyCheckExecutionError classifies a probe failure after the executable was
// found. ENOENT here can mean a broken interpreter or dynamic loader, so it must
// not make an installed but broken agent look like an absent optional agent.
func ClassifyCheckExecutionError(err error) string {
	errorType := ClassifyCheckError(err)
	if errorType == CheckErrorNotFound {
		return CheckErrorUnknown
	}
	return errorType
}
