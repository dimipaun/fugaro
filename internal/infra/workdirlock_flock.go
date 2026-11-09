//go:build unix

package infra

import (
	"errors"
	"os"
	"syscall"
)

// lockWorkdirFile takes the exclusive lock of f without waiting, and reports
// errWorkdirLockHeld when another process (or another open description of the
// same file, this process's own second run included) holds it.
//
// flock is what makes the lock survive the holder: it is held on the open
// file description, so the kernel drops it when that description closes —
// when the process exits, and when it is killed with no chance to clean up.
// Nothing here leaves a record a later run has to decide is stale, which is
// the whole reason it is flock and not a created-exclusively PID file.
func lockWorkdirFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errWorkdirLockHeld
	}
	return err
}

// unlockWorkdirFile gives the lock back. Closing the file does this too; the
// explicit unlock is what makes Release's promise readable at the call site.
func unlockWorkdirFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
