//go:build unix

package pluginwire

import (
	"os"
	"syscall"
)

// openNoFollow opens path for reading without following a final symlink and
// without blocking on a FIFO: O_NOFOLLOW makes the open of a symlink fail
// rather than read what it points at, and O_NONBLOCK makes the open of a FIFO
// with no writer return at once instead of parking the read forever. What the
// flags do not do is say what was opened — the caller fstats the handle it
// gets back and refuses anything that is not a regular file — so this is one
// of readFile's guards, not the only one.
//
// It exists as a pair because Windows has no O_NOFOLLOW; see
// openfile_other.go for what that half keeps and loses.
// (internal/blobx/openfile_unix.go's OpenSource and
// internal/runner/openfile_unix.go's openNoFollow are the same pair for the
// same reason.)
func openNoFollow(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}
