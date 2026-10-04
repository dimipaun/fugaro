//go:build unix

package cli

import "syscall"

// noCoreDumps turns core dumps off while a value is held, so a crash does not
// write it to disk, and returns what restores the limit.
func noCoreDumps() (restore func()) {
	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CORE, &old); err != nil {
		return func() {}
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{Cur: 0, Max: old.Max}); err != nil {
		return func() {}
	}
	return func() { _ = syscall.Setrlimit(syscall.RLIMIT_CORE, &old) }
}
