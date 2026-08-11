//go:build windows

package vscode

import (
	"math"
	"os"
	"strconv"
	"syscall"
)

// workspaceIDStatSalt returns the salt the IDE mixes into a local folder's workspace
// storage ID on Windows: the folder's creation time in milliseconds, matching Node's
// fs.Stats#birthtime.getTime() as the IDE computes it — NTFS reliably exposes creation
// time (unlike Linux, where the inode is used instead; see workspace_id_linux.go). The
// nanosecond total is split into seconds/sub-second parts before rounding so the
// conversion stays exact in float64 (a raw nanoseconds-since-1970 value has too many
// significant digits to round-trip through float64 without losing precision). Empty
// when the salt can't be derived, mirroring the IDE's own fallback of hashing the bare
// path.
func workspaceIDStatSalt(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return ""
	}
	ns := stat.CreationTime.Nanoseconds()
	if ns <= 0 {
		return ""
	}
	sec := ns / 1e9
	subNs := ns % 1e9
	ms := sec*1000 + int64(math.Round(float64(subNs)/1e6))
	if ms == 0 {
		return ""
	}
	return strconv.FormatInt(ms, 10)
}
