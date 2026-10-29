//go:build !linux

package headroomproc

// limitAddressSpace has no prlimit to reach for off Linux, and RLIMIT_AS is
// not a portable resource: the memory bound is a loss here, stated rather than
// implied. The liveness probe is unaffected — a hang is caught on every
// platform — and the bound is best effort on Linux too (a container's own
// limit is what actually holds a run's memory).
func limitAddressSpace(pid int, bytes int64) error { return nil }
