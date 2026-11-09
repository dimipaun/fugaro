//go:build !windows && (aix || (solaris && !illumos) || !unix)

package infra

import "os"

// This half is what the workdir lock can promise where the platform has no
// flock to take and no LockFileEx either, and it is stated rather than
// implied: **no lock is taken**, so two converges started at once on one
// installation can still rebuild each other's workdir there, which is the
// failure TestTwoUpdatesOnOneInstallationDoNotClobberEachOther exists for on
// the platforms that have one. (The wording is internal/procgroup's for the
// same reason: a platform whose only way to end a command is to kill the
// direct child says so, and this one says what it loses.)
//
// The platforms are plan9, js and wasip1, which have no such call at all, and
// solaris and aix, where Go's syscall package has none either: it offers
// FcntlFlock alone, and a record lock belongs to the process rather than to
// the open description — a second run inside one process would take it, which
// is the pair the lock's own tests are made of. illumos is not among them: it
// carries the solaris build tag beside its own and does have syscall.Flock,
// so it takes the lock in workdirlock_flock.go.
//
// A platform that can hold an exclusive lock adds its half here instead of
// this no-op, as windows does in workdirlock_windows.go.

func lockWorkdirFile(*os.File) error { return nil }

func unlockWorkdirFile(*os.File) error { return nil }
