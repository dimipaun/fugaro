//go:build unix && (!solaris || illumos) && !aix

package endpoint

import (
	"errors"
	"os"
	"syscall"
)

// The constraint is internal/infra's for the workdir lock, and it is the same
// call: syscall.Flock is not in every unix's syscall package (solaris and aix
// have none there), while GOOS=illumos carries the solaris build tag beside its
// own and does have it, so illumos is named back in where solaris is not.
// lock_other.go is what is left out, and it says what a platform without this
// half loses.
//
// lockFile takes the exclusive lock of f without waiting, and reports
// errLockHeld when another open description holds it -- another process's, or
// this process's own second open of the same file, which is what makes a
// reader's check against a daemon in the same process a real check rather than
// a shortcut.
//
// flock is what makes the lock outlive the holder in the only direction that
// matters: the kernel drops it when the open description closes, which is when
// the process exits and when it is killed with no chance to clean up. A pid in
// a file survives both, which is why the registry's pid decides nothing
// (D1359).
func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errLockHeld
	}
	return err
}

// openNoFollow is O_NOFOLLOW where the platform has one: the flag that makes an
// open of a path that is a symlink fail rather than follow it. The registry
// file and its lock are opened with it, so a link planted where one of them
// belongs cannot send a write, or a lock, to a file of somebody else's
// choosing.
const openNoFollow = syscall.O_NOFOLLOW

// fileOwner is the uid that owns a file, and whether this platform can say.
// false is "do not refuse": a check the platform cannot make must not be the
// reason an endpoint does not start.
func fileOwner(info os.FileInfo) (uint32, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}
