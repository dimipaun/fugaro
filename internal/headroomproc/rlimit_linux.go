//go:build linux

package headroomproc

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// limitAddressSpace caps the child's address space with prlimit. The child is
// already up when this runs: a runtime that reserves its address space at
// startup — Go's own, which needs well over a gigabyte of it before main —
// cannot start under a smaller RLIMIT_AS at all, so the cap bounds what the
// sidecar allocates from readiness on and nothing it had already mapped.
//
// A hard limit below the requested one is an error rather than a silent clamp:
// a bound that quietly becomes a smaller number is not the bound the caller
// asked for.
//
// Both limits are set, not just the soft one: a soft limit alone is no bound at
// all, because the sidecar — or any other process running as the same user,
// the agent included — can raise it back with one setrlimit call. The hard
// limit is what makes the cap hold.
func limitAddressSpace(pid int, bytes int64) error {
	var old unix.Rlimit
	if err := unix.Prlimit(pid, unix.RLIMIT_AS, nil, &old); err != nil {
		return fmt.Errorf("read RLIMIT_AS: %w", err)
	}
	if old.Max != unix.RLIM_INFINITY && old.Max < uint64(bytes) {
		return fmt.Errorf("RLIMIT_AS hard limit is %d bytes, below the requested %d", old.Max, bytes)
	}
	limit := unix.Rlimit{Cur: uint64(bytes), Max: uint64(bytes)}
	if err := unix.Prlimit(pid, unix.RLIMIT_AS, &limit, nil); err != nil {
		return fmt.Errorf("set RLIMIT_AS: %w", err)
	}
	return nil
}
