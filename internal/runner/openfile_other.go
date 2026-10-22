//go:build !unix

package runner

import "os"

// oNoFollow is nothing where there is no O_NOFOLLOW; O_EXCL is what refuses a
// path something is already at.
const oNoFollow = 0

// openNoFollow is os.Open where there is no O_NOFOLLOW: the fstat the caller
// makes on the returned file is the only guard, and the bounded copy is what
// keeps a source that does not end from running on.
func openNoFollow(path string) (*os.File, error) { return os.Open(path) }

// openRootNoFollow is openNoFollow for a path inside an os.Root, with plain
// O_RDONLY. What is dropped is O_NONBLOCK, not the link refusal: os.Root's own
// resolution refuses a link that leaves the root on every platform, and on
// Windows it is stricter still than the Unix half (Go opens with
// O_NOFOLLOW_ANY and reports any symlink as an error, os/root_windows.go's
// openat). A FIFO swapped in after the caller's Lstat is refused by the fstat
// the caller makes on the handle, after an open that may wait for a writer.
func openRootNoFollow(rt *os.Root, name string) (*os.File, error) {
	return rt.OpenFile(name, os.O_RDONLY, 0)
}
