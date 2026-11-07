//go:build unix

package report

import (
	"io/fs"
	"syscall"
)

// dirOwnedByUser reports whether the logs directory is this user's own. A
// directory another account owns decides where this run's log of its own
// secrets lands, and it can replace the file after the run: that is the one
// thing the mode on the file cannot defend against, so the check is the
// directory's owner and it is made before anything is written (F6a).
//
// It is a pair because Windows has no syscall.Stat_t at all;
// logsink_other.go says what that half does instead.
func dirOwnedByUser(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == euid()
}
