//go:build unix

package blobx

import (
	"os"
	"syscall"
)

// openSource opens a file PutFile is about to stream, without following a
// final symlink and without blocking on a FIFO or a device: the caller is
// given a path, and between whatever it checked about that path and this open
// anything the same user is running can have replaced it. O_NOFOLLOW fails the
// open of a symlink (a link to /dev/zero is a stream that never ends);
// O_NONBLOCK returns at once for a FIFO that has no writer instead of
// blocking the copy forever.
// (internal/config/readfile_unix.go's openRegular and internal/runner's
// openNoFollow are the same pair for the same reason.)
func openSource(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}
