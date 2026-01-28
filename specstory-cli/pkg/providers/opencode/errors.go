package opencode

import (
	"errors"
	"fmt"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
)

// Error type constants for Check failures.
const (
	ErrTypeNotFound         = "not_found"
	ErrTypePermissionDenied = "permission_denied"
	ErrTypeVersionFailed    = "version_failed"
)

// buildCheckErrorMessage creates a user-facing error message for Check failures.
// errorType: The type of error (not_found, permission_denied, version_failed).
// opencodeCmd: The command path that was attempted.
// isCustom: Whether a custom command was provided via -c flag.
// stderr: Any stderr output from the failed command.
func buildCheckErrorMessage(errorType string, opencodeCmd string, isCustom bool, stderr string) string {
	var b strings.Builder

	switch errorType {
	case ErrTypeNotFound:
		b.WriteString("OpenCode could not be found.\n\n")
		if isCustom {
			b.WriteString("  Verify the path you supplied points to the `opencode` executable.\n")
			b.WriteString(fmt.Sprintf("  Provided command: %s\n", opencodeCmd))
		} else {
			b.WriteString("  Install OpenCode from https://opencode.ai\n")
			b.WriteString("  Ensure `opencode` is on your PATH or pass a custom command via:\n")
			b.WriteString("    specstory check opencode -c \"path/to/opencode\"\n")
		}

	case ErrTypePermissionDenied:
		b.WriteString("OpenCode exists but isn't executable.\n\n")
		b.WriteString(fmt.Sprintf("  Fix permissions: chmod +x %s\n", opencodeCmd))

	default:
		// version_failed or unknown error
		b.WriteString("`opencode --version` failed.\n\n")
		if stderr != "" {
			b.WriteString(fmt.Sprintf("Error output:\n%s\n\n", stderr))
		}
		b.WriteString("  Try running `opencode --version` directly in your terminal.\n")
		b.WriteString("  If you upgraded recently, reinstall OpenCode to refresh dependencies.\n")
	}

	return b.String()
}

// printDetectionHelp prints helpful guidance when OpenCode detection fails.
// It examines the error type and provides actionable advice for each scenario.
func printDetectionHelp(err error) {
	var pathErr *OpenCodePathError
	if errors.As(err, &pathErr) {
		printPathErrorHelp(pathErr)
		return
	}

	// Generic error fallback
	log.UserWarn("OpenCode detection failed: %v", err)
}

// printPathErrorHelp handles specific OpenCodePathError cases with detailed guidance.
func printPathErrorHelp(pathErr *OpenCodePathError) {
	switch pathErr.Kind {
	case "storage_missing":
		printStorageMissingHelp(pathErr)

	case "project_missing":
		printProjectMissingHelp(pathErr)

	case "global_session":
		printGlobalSessionHelp()

	default:
		log.UserWarn("OpenCode detection failed: %v", pathErr)
	}
}

// printStorageMissingHelp provides guidance when the OpenCode storage directory doesn't exist.
func printStorageMissingHelp(pathErr *OpenCodePathError) {
	log.UserWarn("OpenCode storage directory missing (%s).", pathErr.Path)
	log.UserMessage("\nThis usually means OpenCode hasn't been run on this machine yet.\n\n")
	log.UserMessage("To fix this:\n")
	log.UserMessage("  1. Install OpenCode from https://opencode.ai if not installed\n")
	log.UserMessage("  2. Run `opencode` once to initialize the storage directory\n")
	log.UserMessage("  3. Then run this command again\n\n")
	log.UserMessage("Expected path: %s\n", pathErr.Path)
}

// printProjectMissingHelp provides guidance when no OpenCode data exists for the current project.
func printProjectMissingHelp(pathErr *OpenCodePathError) {
	log.UserWarn("No OpenCode data found for this project.")
	log.UserMessage("\nOpenCode hasn't created a project folder for your current directory yet.\n")
	log.UserMessage("This happens when OpenCode hasn't been run in this directory.\n\n")

	// Show project hash info for debugging
	log.UserMessage("Project details:\n")
	log.UserMessage("  Expected hash: %s\n", pathErr.ProjectHash)
	log.UserMessage("  Expected path: %s\n\n", pathErr.Path)

	// Show known projects if any exist
	if len(pathErr.KnownHashes) > 0 {
		log.UserMessage("Known OpenCode project hashes:\n")
		for _, hash := range pathErr.KnownHashes {
			log.UserMessage("  - %s\n", hash)
		}
		log.UserMessage("\n")
	} else {
		log.UserMessage("No OpenCode projects detected yet under ~/.local/share/opencode/storage.\n\n")
	}

	log.UserMessage("To fix this:\n")
	log.UserMessage("  1. Run `specstory run` to start OpenCode in this directory\n")
	log.UserMessage("  2. Or run `opencode` directly, then try syncing again\n")
}

// printGlobalSessionHelp explains why global sessions aren't supported.
func printGlobalSessionHelp() {
	log.UserWarn("Global OpenCode sessions are not supported.")
	log.UserMessage("\nSpecStory is project-centric and requires a specific project context.\n")
	log.UserMessage("Global sessions (run without a project directory) cannot be synced.\n\n")
	log.UserMessage("To use SpecStory with OpenCode:\n")
	log.UserMessage("  1. Navigate to your project directory\n")
	log.UserMessage("  2. Run `opencode` from within the project\n")
	log.UserMessage("  3. Then run `specstory sync` to save your sessions\n")
}

// formatJSONParseError creates a user-friendly message for JSON parsing errors.
// This is used when session, message, or part files are invalid or corrupt.
func formatJSONParseError(filePath string, parseErr error) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Failed to parse OpenCode data file: %s\n\n", filePath))
	b.WriteString(fmt.Sprintf("  Error: %v\n\n", parseErr))
	b.WriteString("Possible causes:\n")
	b.WriteString("  - The file may be corrupt or incomplete\n")
	b.WriteString("  - OpenCode may have been interrupted while writing\n")
	b.WriteString("  - The file format may have changed in a newer OpenCode version\n\n")
	b.WriteString("Try running `opencode` again to regenerate the session data.\n")
	return b.String()
}
