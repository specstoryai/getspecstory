package spi

import (
	"errors"
	"os"
	"os/exec"
)

// Error types reported by ClassifyCheckError. They are also the values a
// provider reports to analytics, so they are stable identifiers rather than
// display text.
const (
	CheckErrorNotFound         = "not_found"         // the agent binary is not installed or not on PATH
	CheckErrorPermissionDenied = "permission_denied" // the binary exists but cannot be executed
	CheckErrorUnknown          = "unknown"           // the probe failed for a reason worth reading error_message for

	// CheckErrorNoOutput is reported by a provider directly rather than by
	// ClassifyCheckError: the probe exited cleanly but printed no version, which
	// is a successful run producing an unusable result, not an error to classify.
	CheckErrorNoOutput = "no_output"
)

// ClassifyCheckError buckets a failure from running "<agent> --version" during
// Provider.Check into one of the CheckError* types, so that the caller can
// choose remediation advice and report a consistent error type to analytics.
// A nil error classifies as "" — nothing failed.
//
// CheckErrorUnknown is the residual: it means the failure could not be
// attributed to a missing binary or a permission problem, so error_message is
// the only thing that explains it. Callers may also treat it as "not fatal,
// worth retrying differently" — codexcli uses it that way to decide whether to
// try another version flag.
func ClassifyCheckError(err error) string {
	var execErr *exec.Error
	var pathErr *os.PathError

	// Each PathError arm re-tests errors.As so that a PathError which is neither
	// missing-file nor permission falls through to the plain os.ErrPermission
	// check below instead of short-circuiting the whole switch.
	errorType := CheckErrorUnknown
	switch {
	case err == nil:
		errorType = ""
	case errors.As(err, &execErr) && execErr.Err == exec.ErrNotFound:
		errorType = CheckErrorNotFound
	case errors.As(err, &pathErr) && errors.Is(pathErr.Err, os.ErrNotExist):
		errorType = CheckErrorNotFound
	case errors.As(err, &pathErr) && errors.Is(pathErr.Err, os.ErrPermission):
		errorType = CheckErrorPermissionDenied
	case errors.Is(err, os.ErrPermission):
		errorType = CheckErrorPermissionDenied
	}
	return errorType
}
