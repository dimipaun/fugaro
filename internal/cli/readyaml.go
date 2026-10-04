package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// maxFugaroYAML is the most a fugaro.yaml may hold: a real one is a few
// kilobytes, and a checkout is somebody else's until it is trusted.
const maxFugaroYAML = 1 << 20

// readFugaroYAML reads a fugaro.yaml (or any config file a checkout holds):
// only a regular file (never a symlink, which could name any file the user
// can read, nor a FIFO or a device, which could block the read or never end),
// of at most maxFugaroYAML bytes. A missing file is os.ErrNotExist, as with
// os.ReadFile. Its errors never carry any of the file's content.
func readFugaroYAML(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	switch m := fi.Mode(); {
	case m&os.ModeSymlink != 0:
		return nil, fmt.Errorf("%s is a symbolic link: not read (a checkout's fugaro.yaml must be a regular file)", path)
	case !m.IsRegular():
		return nil, fmt.Errorf("%s is not a regular file: not read", path)
	case fi.Size() > maxFugaroYAML:
		return nil, fmt.Errorf("%s is larger than %d bytes: not read", path, maxFugaroYAML)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// The file opened is the file inspected, and a file that grew since stays
	// within the cap.
	if fi2, err := f.Stat(); err != nil || !os.SameFile(fi, fi2) {
		return nil, errors.New(path + " changed while it was opened: not read")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFugaroYAML+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFugaroYAML {
		return nil, fmt.Errorf("%s is larger than %d bytes: not read", path, maxFugaroYAML)
	}
	return data, nil
}
