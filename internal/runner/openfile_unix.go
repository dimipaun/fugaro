//go:build unix

package runner

import (
	"os"
	"syscall"
)

// oNoFollow is O_NOFOLLOW where the platform has it: a create at a path a
// symlink has been planted on fails instead of writing through it.
const oNoFollow = syscall.O_NOFOLLOW

// openNoFollow opens path for reading without following a final symlink and
// without blocking on a FIFO, a device or a socket swapped in after the check:
// O_NOFOLLOW makes the open of a symlink fail, O_NONBLOCK makes the open of a
// FIFO that has no writer return at once rather than block forever.
// (internal/config/readfile_unix.go's openRegular is the same pair for the
// same reason; the runner cannot import it, so the two are twins by comment.)
//
// What the flags do NOT do is say what was opened: the caller still fstats the
// returned file and refuses anything that is not a regular file of the size it
// checked. A symlinked parent directory is still followed — the artifact step
// checks that separately, by resolving the whole path and comparing it with
// the resolved outputs root.
func openNoFollow(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

// openRootNoFollow is openNoFollow for a path inside an os.Root (session.go's
// readSessionFile). It passes the same flags, but the symlink half of them is
// a pass-through rather than the guard here: Go's os.Root resolves the path
// itself, refusing a link that leaves the root and following one that stays
// inside it (os/root_unix.go's checkSymlink), whatever the caller passes — so
// what refuses a swapped-in link at this call site is the caller's own Lstat,
// and what this adds is O_NONBLOCK (a FIFO swapped in returns at once) plus
// the fstat the caller makes on the handle. On Windows os.Root is the stricter
// of the two: it opens with O_NOFOLLOW_ANY and reports any symlink as an error
// (os/root_windows.go's openat). (The two helpers are twins by comment rather
// than by call: the root form cannot be spelled with syscall.Open, which knows
// nothing of the root.)
func openRootNoFollow(rt *os.Root, name string) (*os.File, error) {
	return rt.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
