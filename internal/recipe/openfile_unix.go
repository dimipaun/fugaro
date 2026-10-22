//go:build unix

package recipe

import (
	"os"
	"syscall"
)

// openNoFollow opens a recipe file for reading without following a final
// symlink and without blocking on a FIFO: ReadRepoFile has already Lstat'ed
// every component of the path, and this is the guard for the window between
// that and the read — O_NOFOLLOW fails the open of a link planted in it, and
// O_NONBLOCK makes a FIFO swapped in at least not block. What neither does is
// say what was opened: the caller fstats the handle and refuses anything that
// is not a regular file.
//
// It exists as a pair because Windows has no O_NOFOLLOW; see
// openfile_other.go for what that half keeps and loses.
// (internal/blobx/openfile_unix.go's OpenSource is the same pair for the same
// reason.)
func openNoFollow(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}
