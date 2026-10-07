package recipe

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ReadRepoFile reads RepoPath(name) under root. name must be a recipe name.
// Only a regular file is read: a symbolic link anywhere on the path
// (.fugaro, .fugaro/recipes or the file) or a directory is an error, as is a
// file over MaxBytes. The file is opened without following a link and its
// read is capped, so a file swapped in after the checks is still safe.
// found is false when there is no such file.
func ReadRepoFile(root, name string) (data []byte, found bool, err error) {
	if !NameRE.MatchString(name) {
		return nil, false, fmt.Errorf("%q is not a recipe name", name)
	}
	rel := RepoPath(name)
	parts := strings.Split(rel, "/")
	for i := 1; i <= len(parts); i++ {
		fi, err := os.Lstat(filepath.Join(append([]string{root}, parts[:i]...)...))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, false, nil
		case err != nil:
			return nil, false, err
		case i < len(parts) && !fi.IsDir():
			// A linked directory reports ModeSymlink, not a directory.
			return nil, false, fmt.Errorf("%s: %s is not a directory", rel, strings.Join(parts[:i], "/"))
		case i == len(parts) && !fi.Mode().IsRegular():
			return nil, false, fmt.Errorf("%s is not a regular file (a symbolic link or directory is refused)", rel)
		case i == len(parts) && fi.Size() > MaxBytes:
			return nil, false, fmt.Errorf("%s is %d bytes, over the 16 KiB limit", rel, fi.Size())
		}
	}
	f, err := os.OpenFile(filepath.Join(root, filepath.FromSlash(rel)), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%s is not a regular file (a symbolic link is refused): %w", rel, err)
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil {
		return nil, false, err
	} else if !fi.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a regular file (a symbolic link or directory is refused)", rel)
	}
	data, err = io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > MaxBytes {
		return nil, false, fmt.Errorf("%s is over the 16 KiB limit", rel)
	}
	return data, true, nil
}
