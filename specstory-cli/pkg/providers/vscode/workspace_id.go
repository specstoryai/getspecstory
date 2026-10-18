package vscode

import (
	"crypto/md5" // #nosec G501 -- not used for security; mirrors the IDE's own workspace-ID scheme
	"encoding/hex"
	"os"
)

// WorkspaceID computes the workspace storage directory name the IDE derives
// for a local single-folder workspace: md5(folder path + platform stat salt).
// Mirrors getSingleFolderWorkspaceIdentifier in the IDE's main.js and was
// verified byte-for-byte against real workspaceStorage entries of both VS Code
// and Cursor.
//
// The path is hashed exactly as given — callers choose the spelling policy:
// Cursor mints per literal spelling (its verified native behavior, where the
// same folder opened via differently-spelled paths genuinely gets distinct
// entries), while VS Code resolves the on-disk case when opening a folder, so
// its callers pass the canonical path.
func WorkspaceID(folderPath string) (string, error) {
	info, err := os.Stat(folderPath)
	if err != nil {
		return "", err
	}
	sum := md5.Sum([]byte(folderPath + workspaceIDStatSalt(info))) // #nosec G401 -- not used for security; mirrors the IDE's own workspace-ID scheme
	return hex.EncodeToString(sum[:]), nil
}
