//go:build unix && (!solaris || illumos) && !aix

package mailbox

import (
	"errors"
	"os"
	"syscall"
)

// The constraint is not the whole of unix, and statelock_other.go is what is
// left out and why: syscall.Flock is not in every unix's syscall package
// (solaris and aix have no such call there), while GOOS=illumos carries the
// solaris build tag beside its own and does have it, so illumos is named back in
// where solaris is not.
//
// lockStateFile takes f's lock without waiting — exclusive, or shared when the
// caller is one of the consumers that may hold it together (statelock.go has the
// rule) — and reports errStateLockHeld when another holder stands in its way. A
// shared request is refused by an exclusive holder and the other way round,
// which is what makes a sweep and a consumer refuse each other.
//
// flock is what makes the lock survive the holder: it is held on the open file
// description, so the kernel drops it when that description closes — when the
// process exits, and when it is killed with no chance to clean up. Nothing here
// leaves a record a later command has to decide is stale. The request does not
// depend on the file's access mode, which is what lets the `--dry-run` probe
// (StateLockHeld) take it on a file opened read-only.
func lockStateFile(f *os.File, shared bool) error {
	how := syscall.LOCK_EX
	if shared {
		how = syscall.LOCK_SH
	}
	err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errStateLockHeld
	}
	return err
}

// unlockStateFile gives the lock back. Closing the file does this too; the
// explicit unlock is what makes Release's promise readable at the call site.
func unlockStateFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
