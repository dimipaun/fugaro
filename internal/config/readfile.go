package config

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// MaxCheckoutFile is the most a file read from a checkout may hold: a
// fugaro.yaml, Dockerfile, package.json or .yarnrc.yml is a few kilobytes, and
// a checkout is somebody else's until it is trusted.
const MaxCheckoutFile = 1 << 20

// ReadRegular reads a file of a checkout: only a regular file (never a
// symlink, which could name any file the user can read, nor a FIFO or a
// device, which could block the read or never end), of at most max bytes. A
// missing file is os.ErrNotExist, as with os.ReadFile. Its errors never carry
// any of the file's content. The file is opened without following a symlink
// or blocking (O_NOFOLLOW|O_NONBLOCK on unix), so a swap between the check and
// the open cannot hang the read or read another file; the fstat after it must
// still be the regular file inspected. A symlinked parent directory is still
// followed: only the file itself is checked.
func ReadRegular(path string, max int64) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	switch m := fi.Mode(); {
	case m&os.ModeSymlink != 0:
		return nil, fmt.Errorf("%s is a symbolic link: not read (a checkout's file must be a regular file)", path)
	case !m.IsRegular():
		return nil, fmt.Errorf("%s is not a regular file: not read", path)
	case fi.Size() > max:
		return nil, fmt.Errorf("%s is larger than %d bytes: not read", path, max)
	}
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// The file opened is the file inspected, and a file that grew since stays
	// within the cap.
	if fi2, err := f.Stat(); err != nil || !os.SameFile(fi, fi2) {
		return nil, errors.New(path + " changed while it was opened: not read")
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes: not read", path, max)
	}
	return data, nil
}
