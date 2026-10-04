//go:build unix

package config

import (
	"os"
	"syscall"
)

// openRegular opens path without following a final symlink and without
// blocking on a FIFO or device swapped in after the Lstat: O_NOFOLLOW makes a
// symlink swapped in fail, O_NONBLOCK makes opening a FIFO return at once.
func openRegular(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}
