//go:build !windows && (aix || (solaris && !illumos) || !unix)

package mailbox

import "os"

// This half is what the state lock can promise where the platform has neither
// flock nor LockFileEx, and it is stated rather than implied: **no lock is
// taken**, so on those platforms a `fugaro mailbox sweep` run beside a consumer
// can still drop a record the consumer accepted in between, which is the failure
// statelock.go exists to prevent. (The wording is internal/infra's workdir lock
// for the same reason: a platform whose lock cannot be taken says what it
// loses rather than pretending.)
//
// The platforms are plan9, js and wasip1, which have no such call at all, and
// solaris and aix, where Go's syscall package has none either. illumos is not
// among them: it carries the solaris build tag beside its own and does have
// syscall.Flock, so it takes the lock in statelock_flock.go.
//
// A platform that can hold a lock adds its half here instead of this no-op, as
// windows does in statelock_windows.go.

func lockStateFile(*os.File, bool) error { return nil }

func unlockStateFile(*os.File) error { return nil }
