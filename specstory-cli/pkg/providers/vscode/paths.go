package vscode

import (
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// URIToPath converts a workspace URI to a file path. Both the vscode-remote://
// forms (wsl+distro, ssh-remote+config, tunnel+host, dev-container+config) and
// plain file:// URIs — including the WSL and Windows drive-letter/UNC shapes —
// are converted by shared spi helpers so every consumer translates them
// identically. Remote URIs yield the path on the remote machine / inside the
// container; matching those against a local project path is the basename
// fallback's job in FindWorkspaces.
func URIToPath(uri string) (string, error) {
	// Handle vscode-remote:// URIs before url.Parse because Go's URL parser
	// rejects percent-encoded characters like %2B in the host component
	// (e.g., vscode-remote://wsl%2Bubuntu/home/user/project).
	if strings.HasPrefix(uri, "vscode-remote://") {
		return spi.ParseVSCodeRemoteURI(uri)
	}
	return spi.FileURIToPath(uri)
}

// PathToFileURI builds the file:// URI the IDE stores in workspace.json, matching
// the IDE's own encoding — percent-encoding (e.g. spaces) plus the Windows
// drive-letter handling described on fileURIParts. Raw concatenation would
// mis-associate paths containing encodable characters.
func PathToFileURI(path string) string {
	_, _, external := fileURIParts(path)
	return external
}

// WorkspaceURIMap builds the serialized URI object the IDE stores in
// workspaceIdentifier.uri for a workspace root. Shared by every writer of those
// rows so reconstructed sessions carry byte-identical, IDE-native encoding —
// a mismatch here can mis-associate a reconstructed session with its workspace.
func WorkspaceURIMap(workspaceRoot string) map[string]interface{} {
	fsPath, uriPath, external := fileURIParts(workspaceRoot)
	uri := map[string]interface{}{
		"$mid":     1,
		"fsPath":   fsPath,
		"external": external,
		"path":     uriPath,
		"scheme":   "file",
	}
	// The IDE stamps "_sep": 1 alongside a cached fsPath on Windows only
	// (_pathSepMarker = isWindows ? 1 : undefined). URI.revive() discards the cached
	// fsPath unless _sep matches that marker, so native Windows rows always carry it
	// and Unix rows never do. Emitting it keeps reconstructed rows byte-identical to
	// the IDE's own.
	if isWindowsDrivePath(workspaceRoot) {
		uri["_sep"] = 1
	}
	return uri
}

// fileURIParts converts an absolute filesystem path into the three related values a
// VS Code-style serialized URI carries:
//
//	fsPath   — the OS path ("c:\Users\x\proj" on Windows, "/home/x/proj" on Unix)
//	uriPath  — the URI path component ("/c:/Users/x/proj"), forward slashes, decoded
//	external — the full percent-encoded URI ("file:///c%3A/Users/x/proj")
//
// The IDE normalizes drive letters to lowercase and percent-encodes the drive colon
// in external (Go's URL encoder leaves ':' bare in paths, so it is encoded by hand).
// UNC paths (\\server\share) are not handled — workspace roots are local directories.
func fileURIParts(osPath string) (fsPath, uriPath, external string) {
	fsPath = osPath
	p := osPath

	// Lowercase the drive and use forward slashes in the URI path, backslashes in fsPath.
	if isWindowsDrivePath(p) {
		drive := strings.ToLower(p[:1])
		fsPath = drive + strings.ReplaceAll(p[1:], "/", `\`)
		p = "/" + drive + strings.ReplaceAll(p[1:], `\`, "/")
	}
	uriPath = p

	u := url.URL{Scheme: "file", Path: p}
	external = u.String()
	// Percent-encode the drive colon ("file:///c:/..." -> "file:///c%3A/...") to match
	// the IDE's serialization.
	const pfx = "file:///"
	if len(external) > len(pfx)+1 && external[len(pfx)+1] == ':' {
		external = external[:len(pfx)+1] + "%3A" + external[len(pfx)+2:]
	}
	return fsPath, uriPath, external
}

// isWindowsDrivePath reports whether osPath is a Windows-shaped absolute path
// (leading drive letter, e.g. "C:\proj" or "c:/proj"). Detected by shape rather than
// runtime.GOOS so the conversion is deterministic and testable on any platform — a
// Windows-written URI can be read on macOS and vice versa.
func isWindowsDrivePath(osPath string) bool {
	return len(osPath) >= 3 && osPath[1] == ':' &&
		(osPath[2] == '\\' || osPath[2] == '/') &&
		('a' <= osPath[0]|0x20 && osPath[0]|0x20 <= 'z')
}

// NormalizePathForComparison normalizes a path for workspace matching.
// Handles three cases:
//  1. Windows UNC WSL paths: \\wsl.localhost\Ubuntu\... -> /home/user/...
//  2. Unix-style paths on Windows (WSL/SSH remotes): preserved as-is
//  3. Normal paths: resolved to canonical form with symlinks and case normalization
//
// The Windows shapes matter even though the CLI itself only runs on macOS and
// Linux: workspace URIs written by an IDE on Windows can reference them.
func NormalizePathForComparison(path string) (string, error) {
	originalPath := path

	// Step 1: Normalize Windows UNC WSL paths (\\wsl.localhost\... or \\wsl$\...)
	// to Unix format. Only triggered for actual WSL UNC paths, not ordinary
	// Windows paths like C:\Users\...
	if runtime.GOOS == "windows" && isWindowsWSLUNCPath(path) {
		path = normalizeWindowsWSLPath(path)
		if path != originalPath {
			slog.Debug("Normalized Windows UNC WSL path",
				"original", originalPath,
				"normalized", path)
		}
	}

	// Step 2: A Unix-style path on Windows (WSL/SSH remote) must not go
	// through filepath.Abs or GetCanonicalPath — they would corrupt it by
	// treating "/home/user/project" as relative to the current drive.
	if isUnixStylePathOnWindows(path) {
		cleaned := filepath.Clean(path)
		slog.Debug("Preserved Unix-style path on Windows", "path", cleaned)
		return cleaned, nil
	}

	// Step 3: Normal path handling — resolve to canonical form.
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	canonicalPath, err := spi.GetCanonicalPath(absPath)
	if err != nil {
		slog.Warn("Failed to get canonical path, using absolute path",
			"path", path,
			"error", err)
		return absPath, nil
	}

	return canonicalPath, nil
}

// isUnixStylePathOnWindows detects if a path represents a remote (WSL/SSH)
// filesystem location while running on Windows. The IDE's fsPath returns these
// in two forms:
//   - "/home/user/project"  — forward-slash form (the raw fsPath value)
//   - "\home\user\project"  — backslash form (Windows filepath.Clean applied)
//
// Both forms have no drive/volume name and must not be passed through
// filepath.Abs, which would corrupt them by prepending the current drive.
func isUnixStylePathOnWindows(path string) bool {
	if runtime.GOOS != "windows" || filepath.VolumeName(path) != "" {
		return false
	}
	// Forward-slash prefix: /home/user/project
	if strings.HasPrefix(path, "/") {
		return true
	}
	// Backslash prefix without UNC double-backslash: \home\user\project
	if strings.HasPrefix(path, `\`) && !strings.HasPrefix(path, `\\`) {
		return true
	}
	return false
}

// isWindowsWSLUNCPath reports whether path is a Windows UNC path pointing into
// WSL, i.e. \\wsl.localhost\<distro>\... or \\wsl$\<distro>\...
func isWindowsWSLUNCPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasPrefix(lower, `\\wsl.localhost\`) || strings.HasPrefix(lower, `\\wsl$\`)
}

// normalizeWindowsWSLPath converts Windows UNC WSL paths to Unix format:
//   - \\wsl.localhost\Ubuntu\home\user\project -> /home/user/project
//   - \\wsl$\Ubuntu\home\user\project -> /home/user/project
func normalizeWindowsWSLPath(path string) string {
	if !strings.Contains(path, "\\") {
		return path
	}

	normalized := strings.ReplaceAll(path, "\\", "/")

	// Strip Windows UNC WSL prefixes and the distro name.
	lower := strings.ToLower(normalized)
	for _, prefix := range []string{"//wsl.localhost/", "//wsl$/"} {
		if strings.HasPrefix(lower, prefix) {
			remainder := normalized[len(prefix):]
			// Skip the distro name (e.g., "Ubuntu/home/user" -> "/home/user")
			if slashIdx := strings.Index(remainder, "/"); slashIdx >= 0 {
				return remainder[slashIdx:]
			}
			return "/"
		}
	}

	return normalized
}
