// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

//go:build windows

package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockCredentialFile waits for the exclusive lock of the file's first byte.
// LockFileEx is Windows' flock, and without LOCKFILE_FAIL_IMMEDIATELY it is the
// blocking form: the lock belongs to the handle, so the system releases it when
// the process ends however it ends, and nothing here leaves a record a later
// command has to judge stale.
func lockCredentialFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &windows.Overlapped{})
}

// unlockCredentialFile gives the lock back (closing the handle does this too).
func unlockCredentialFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
