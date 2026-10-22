//go:build unix

package cache

import (
	"os"
	"syscall"
)

// openRootNoFollow opens name inside rt for reading, without blocking on a
// FIFO.
//
// What refuses a symlink at these two call sites is **os.Root, not this
// flag**: Go's os.Root resolves the path itself, so it follows a final link
// whose target stays inside the root and refuses one that leaves it
// (os/root_unix.go's checkSymlink), and it does that whether or not
// O_NOFOLLOW is passed — this flag is a pass-through here, kept because it is
// what the call site always passed and because it costs nothing. What this
// helper does add is O_NONBLOCK, which does survive into the openat: a FIFO
// swapped in after the caller's Lstat returns at once instead of parking the
// read. The caller fstats the handle and refuses anything that is not a
// regular file either way.
//
// It is a pair because Windows has no O_NOFOLLOW; openfile_other.go says what
// that half drops, and it drops less here than the shape suggests.
func openRootNoFollow(rt *os.Root, name string) (*os.File, error) {
	return rt.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
