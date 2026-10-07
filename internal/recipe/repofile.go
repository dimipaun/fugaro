package recipe

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ReadRepoFile reads RepoPath(name) under root. Only a regular file is read:
// a symbolic link anywhere on the path (.fugaro, .fugaro/recipes or the file)
// or a directory is an error, as is a file over MaxBytes. found is false when
// there is no such file.
func ReadRepoFile(root, name string) (data []byte, found bool, err error) {
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
	data, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, false, err
	}
	if len(data) > MaxBytes {
		return nil, false, fmt.Errorf("%s is %d bytes, over the 16 KiB limit", rel, len(data))
	}
	return data, true, nil
}
