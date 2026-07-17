//go:build linux

package cursoride

import (
	"os"
	"strconv"
	"syscall"
)

// workspaceIDStatSalt returns the salt Cursor mixes into a local folder's workspace
// storage ID on Linux: the folder's inode number (Cursor/VS Code uses stat.ino there;
// see getSingleFolderWorkspaceIdentifier in Cursor's main.js). Empty when the salt
// can't be derived, mirroring Cursor's own fallback of hashing the bare path.
func workspaceIDStatSalt(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	if stat.Ino == 0 {
		return ""
	}
	return strconv.FormatUint(stat.Ino, 10)
}
