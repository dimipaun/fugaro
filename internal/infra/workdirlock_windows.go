//go:build windows

package infra

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockWorkdirFile takes the exclusive lock of the file's first byte without
// waiting, and reports errWorkdirLockHeld when another process holds it.
//
// LockFileEx is Windows' flock: the lock belongs to the file handle, so the
// system releases it when the process ends however it ends, and a run killed
// with no chance to clean up leaves no lock for a later run to judge stale.
// The lock is what holds the workdirs together; the file's own contents are
// only what the refusal prints.
func lockWorkdirFile(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errWorkdirLockHeld
	}
	return err
}

// unlockWorkdirFile gives the lock back (closing the handle does this too).
func unlockWorkdirFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
