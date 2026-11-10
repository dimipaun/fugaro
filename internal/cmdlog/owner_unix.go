//go:build unix

package cmdlog

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByUser reports whether a component of the store's path is this user's
// own. The path is attacker-influenced and its names are predictable, so a
// directory another account owns decides which log ID Latest answers and what
// Open hands the evidence collector: that is the one thing the mode the writer
// leaves behind cannot defend against, and the reader has no mode of its own to
// leave (F6a, D1603 — the same rule the writer applies in
// internal/report/logsink.go and internal/srctree applies to its cache).
//
// It reads the uid out of the stat and compares it with euid rather than
// trusting the mode, because a 0700 directory another account owns is exactly
// the case the mode cannot see.
//
// It is a pair because Windows has no syscall.Stat_t at all; owner_other.go
// says what that half does instead.
func ownedByUser(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == euid()
}

// euid is os.Geteuid as a variable, so the ownership check is testable without
// a second account — the same shape internal/report's and internal/srctree's
// checks use.
var euid = os.Geteuid
