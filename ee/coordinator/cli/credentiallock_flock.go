// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

//go:build unix && (!solaris || illumos) && !aix

package cli

import (
	"os"
	"syscall"
)

// The constraint is internal/infra's for the same file-lock reason:
// syscall.Flock is not in every unix's syscall package (solaris and aix have no
// such call there), while GOOS=illumos carries the solaris build tag beside its
// own and does have it, so illumos is named back in where solaris is not.

// lockCredentialFile waits for the exclusive lock of f. It is the blocking form
// and not the non-blocking one the workdir lock takes: the holder keeps this
// one for a single token exchange, so the wait is the length of one call, and
// two writes at once are a queue to join rather than a second run to refuse.
func lockCredentialFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// unlockCredentialFile gives the lock back. Closing the file does this too; the
// explicit unlock is what makes release's promise readable at the call site.
func unlockCredentialFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
