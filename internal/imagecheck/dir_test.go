package imagecheck

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/bmatcuk/doublestar/v4"
)

// Dir is a Tree over a plain directory, for tests that need no git
// repository. Its blob IDs are computed the way git computes them for a
// SHA-1 repository from the file's bytes, with no filters applied.
type Dir struct{ Root string }

func (d Dir) file(path string) (string, error) {
	if !fs.ValidPath(path) {
		return "", fmt.Errorf("%s: %w", path, fs.ErrNotExist)
	}
	full := filepath.Join(d.Root, filepath.FromSlash(path))
	// A symlink or anything but a regular file is not a key file, as the
	// cache's own key leaves it out.
	fi, err := os.Lstat(full)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file: %w", path, fs.ErrNotExist)
	}
	return full, nil
}

// BlobID is git's blob ID of the file's content.
func (d Dir) BlobID(path string) (string, error) {
	data, err := d.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha1.New()
	h.Write([]byte("blob " + strconv.Itoa(len(data)) + "\x00"))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Glob matches files only, and doesn't follow symlinks.
func (d Dir) Glob(pattern string) ([]string, error) {
	return doublestar.Glob(os.DirFS(d.Root), pattern, doublestar.WithFilesOnly(), doublestar.WithNoFollow())
}

// ReadFile reads a regular file of the tree.
func (d Dir) ReadFile(path string) ([]byte, error) {
	full, err := d.file(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}
