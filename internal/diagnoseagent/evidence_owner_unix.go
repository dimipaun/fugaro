//go:build unix

package diagnoseagent

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByUser reports whether a component of the evidence folder's path is this
// user's own. The path is attacker-influenced — an agent's session often points
// XDG_STATE_HOME at a temporary directory — and the names are predictable, so a
// directory another account owns would decide who can read the evidence this
// diagnosis writes, which is the failing command's own log. That is the one
// thing the mode the collector leaves behind cannot defend against (F6a,
// D1603), and the applier has no mode of its own to leave.
//
// It reads the uid out of the stat and compares it with euid rather than
// trusting the mode, because a 0700 directory another account owns is exactly
// the case the mode cannot see. It is the check internal/report, internal/cmdlog
// and internal/srctree each make for the directory they write; it is a pair
// because Windows has no syscall.Stat_t at all, and evidence_owner_other.go
// says what that half does instead.
func ownedByUser(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == euid()
}

// euid is os.Geteuid as a variable, so the ownership check is testable without
// a second account — the same shape internal/report's and internal/cmdlog's
// checks use.
var euid = os.Geteuid
