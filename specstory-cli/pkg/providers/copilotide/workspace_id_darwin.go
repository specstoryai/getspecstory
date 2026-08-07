//go:build darwin

package copilotide

import (
	"math"
	"os"
	"strconv"
	"syscall"
)

// workspaceIDStatSalt returns the salt VS Code mixes into a local folder's
// workspace storage ID on macOS: the folder's birthtime in milliseconds.
// Rounded to the nearest millisecond to match Node's fs.Stats#birthtime.getTime()
// as VS Code computes it — verified against real workspaceStorage entries,
// where truncation is off by one. Empty when the salt can't be derived,
// mirroring VS Code's own fallback of hashing the bare path.
func workspaceIDStatSalt(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	ts := stat.Birthtimespec
	ms := ts.Sec*1000 + int64(math.Round(float64(ts.Nsec)/1e6))
	if ms == 0 {
		return ""
	}
	return strconv.FormatInt(ms, 10)
}
