//go:build windows

package mailbox

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockStateFile takes the lock of the file's first byte without waiting —
// exclusive, or shared when the caller is one of the consumers that may hold it
// together (statelock.go has the rule) — and reports errStateLockHeld when
// another holder stands in its way.
//
// LockFileEx is Windows' flock: the lock belongs to the file handle, so the
// system releases it when the process ends however it ends, and a command killed
// with no chance to clean up leaves no lock for a later one to judge stale. A
// byte-range lock is conflict-checked against the other handles' ranges, so a
// shared holder refuses an exclusive request and the other way round.
func lockStateFile(f *os.File, shared bool) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if !shared {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errStateLockHeld
	}
	return err
}

// unlockStateFile gives the lock back (closing the handle does this too).
func unlockStateFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
