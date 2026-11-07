//go:build unix

package srctree

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByUser reports whether a component of the cache path is this user's
// own. The path is attacker-influenced and its names are predictable, so a
// directory another account owns could decide what the agent reads, and it
// can be replaced after the check: that is the one thing the 0700 mode on
// the directory cannot defend against (the output plan's rule for the log
// directory, F6a).
//
// It is a pair because Windows has no syscall.Stat_t at all; owner_other.go
// says what that half does instead.
func ownedByUser(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}
