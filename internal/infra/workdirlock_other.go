//go:build !unix && !windows

package infra

import "os"

// This half is what the workdir lock can promise where the platform has
// neither flock nor LockFileEx (plan9, js, wasip1), and it is stated rather
// than implied: **no lock is taken**, so two converges started at once on one
// installation can still rebuild each other's workdir there, which is the
// failure TestTwoConvergesOnOneInstallationDoNotClobberEachOther exists for
// on the platforms that have one. (The wording is internal/procgroup's for
// the same reason: a platform whose only way to end a command is to kill the
// direct child says so, and this one says what it loses.)
//
// A platform that can hold an exclusive lock adds its half here instead of
// this no-op, as windows does in workdirlock_windows.go.

func lockWorkdirFile(*os.File) error { return nil }

func unlockWorkdirFile(*os.File) error { return nil }
