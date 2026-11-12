// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

//go:build !windows && (aix || (solaris && !illumos) || !unix)

package cli

import (
	"os"
	"sync"
)

// This half is what the credential lock can promise where the platform has no
// flock to take and no LockFileEx either, and it is stated rather than implied:
// **one command at a time within this process, and nothing across two**. On
// these platforms two fugaro processes started at once can still present the
// same refresh token and cost the person a sign-in. The platforms are the ones
// internal/infra's workdir lock leaves out too: plan9, js and wasip1 have no
// such call at all, and solaris and aix have none in Go's syscall package.
var credentialLockMu sync.Mutex

func lockCredentialFile(*os.File) error {
	credentialLockMu.Lock()
	return nil
}

func unlockCredentialFile(*os.File) error {
	credentialLockMu.Unlock()
	return nil
}
