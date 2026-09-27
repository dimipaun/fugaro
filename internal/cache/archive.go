package cache

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// manifestName is the archive's first entry, which records the format
// version and how many roots the entries are indexed against.
const manifestName = "fugaro-cache.json"

// manifestVersion is the only archive format Extract accepts.
const manifestVersion = 1

// maxWindow caps the zstd window a restore will allocate for. Write uses
// the encoder's default level, whose window is far smaller.
const maxWindow = 128 << 20

type manifest struct {
	Version int `json:"version"`
	Roots   int `json:"roots"`
}

// walkRoot returns the directory to walk for root, following a symlink at
// the root itself (an image may link ~/.cache elsewhere). ok is false when
// root is missing or is not a directory.
func walkRoot(root string) (string, bool) {
	dir, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return "", false
	}
	return dir, true
}

// Write streams roots into w as a zstd-compressed tar: the manifest first,
// then every directory, regular file and symlink of root i as "<i>/<rel>".
// Other file types are skipped, and so are symlinks that Extract would
// refuse (absolute or leaving their root), so a saved archive always
// restores. It returns the regular-file bytes written, or ErrTooLarge once
// they would pass maxBytes.
func Write(w io.Writer, roots []string, maxBytes int64) (int64, error) {
	zw, err := zstd.NewWriter(w)
	if err != nil {
		return 0, fmt.Errorf("starting zstd: %w", err)
	}
	tw := tar.NewWriter(zw)
	total, err := writeEntries(tw, roots, maxBytes)
	if err != nil {
		_ = zw.Close()
		return total, err
	}
	if err := tw.Close(); err != nil {
		_ = zw.Close()
		return total, fmt.Errorf("closing tar: %w", err)
	}
	if err := zw.Close(); err != nil {
		return total, fmt.Errorf("closing zstd: %w", err)
	}
	return total, nil
}

func writeEntries(tw *tar.Writer, roots []string, maxBytes int64) (int64, error) {
	m, err := json.Marshal(manifest{Version: manifestVersion, Roots: len(roots)})
	if err != nil {
		return 0, err
	}
	if err := tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o644, Size: int64(len(m)), Typeflag: tar.TypeReg}); err != nil {
		return 0, fmt.Errorf("writing manifest: %w", err)
	}
	if _, err := tw.Write(m); err != nil {
		return 0, fmt.Errorf("writing manifest: %w", err)
	}
	var total int64
	for i, root := range roots {
		dir, ok := walkRoot(root)
		if !ok {
			continue
		}
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if p == dir {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			name := fmt.Sprintf("%d/%s", i, filepath.ToSlash(rel))
			switch t := d.Type(); {
			case t.IsDir():
				return tw.WriteHeader(&tar.Header{Name: name + "/", Mode: 0o755, Typeflag: tar.TypeDir})
			case t&fs.ModeSymlink != 0:
				target, err := os.Readlink(p)
				if err != nil {
					return err
				}
				if symlinkEscapes(rel, target) {
					return nil // Extract would refuse it and poison the key
				}
				return tw.WriteHeader(&tar.Header{Name: name, Linkname: filepath.ToSlash(target), Typeflag: tar.TypeSymlink})
			case t.IsRegular():
				fi, err := d.Info()
				if err != nil {
					return err
				}
				if total+fi.Size() > maxBytes {
					return ErrTooLarge
				}
				if err := tw.WriteHeader(&tar.Header{Name: name, Mode: int64(fileMode(fi.Mode())), Size: fi.Size(), Typeflag: tar.TypeReg}); err != nil {
					return err
				}
				f, err := os.Open(p)
				if err != nil {
					return err
				}
				n, err := io.CopyN(tw, f, fi.Size())
				f.Close()
				total += n
				if err != nil {
					return fmt.Errorf("archiving %s: %w", p, err)
				}
			}
			return nil // sockets, devices, FIFOs: not cache content
		})
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				return total, err
			}
			return total, fmt.Errorf("archiving %s: %w", root, err)
		}
	}
	return total, nil
}

// fileMode masks a regular file's mode to 0o755 when any execute bit is
// set and 0o644 otherwise, so no setuid, setgid or sticky bit survives.
func fileMode(m fs.FileMode) fs.FileMode {
	if m&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// symlinkEscapes reports whether a link at rel (relative to its root)
// pointing at target would resolve outside the root.
func symlinkEscapes(rel, target string) bool {
	target = filepath.FromSlash(target)
	return target == "" || filepath.IsAbs(target) || !filepath.IsLocal(filepath.Join(filepath.Dir(rel), target))
}

// Extract restores an archive written by Write into roots, which must be
// as many as the manifest says. It refuses, before writing it, any entry
// that could land outside its root: every write goes through an os.Root,
// which refuses ".." and symlink escapes at any path component. It stops at
// the first refused entry, and refuses more than maxBytes of content.
func Extract(r io.Reader, roots []string, maxBytes int64) error {
	zr, err := zstd.NewReader(r, zstd.WithDecoderMaxWindow(maxWindow))
	if err != nil {
		return fmt.Errorf("opening zstd: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)

	opened := make([]*os.Root, 0, len(roots))
	defer func() {
		for _, rt := range opened {
			rt.Close()
		}
	}()
	for _, dir := range roots {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating cache root: %w", err)
		}
		rt, err := os.OpenRoot(dir)
		if err != nil {
			return fmt.Errorf("opening cache root: %w", err)
		}
		opened = append(opened, rt)
	}

	h, err := tr.Next()
	if err != nil {
		return fmt.Errorf("reading manifest: %w", err)
	}
	if h.Name != manifestName || h.Typeflag != tar.TypeReg {
		return fmt.Errorf("first entry is %q, not the manifest", h.Name)
	}
	var m manifest
	if err := json.NewDecoder(io.LimitReader(tr, 4096)).Decode(&m); err != nil {
		return fmt.Errorf("reading manifest: %w", err)
	}
	if m.Version != manifestVersion {
		return fmt.Errorf("unsupported cache archive version %d", m.Version)
	}
	if m.Roots != len(roots) {
		return fmt.Errorf("archive has %d roots, want %d", m.Roots, len(roots))
	}

	remaining := maxBytes
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading archive: %w", err)
		}
		idx, rel, err := splitName(h.Name, len(roots))
		if err != nil {
			return err
		}
		rt := opened[idx]
		switch h.Typeflag {
		case tar.TypeDir:
			if rel == "" {
				continue // the root itself
			}
			if err := rt.MkdirAll(rel, 0o755); err != nil {
				return fmt.Errorf("restoring %q: %w", h.Name, err)
			}
		case tar.TypeReg:
			if rel == "" {
				return fmt.Errorf("refusing %q: a file at the root", h.Name)
			}
			if h.Size < 0 || h.Size > remaining {
				return fmt.Errorf("refusing %q: %w", h.Name, ErrTooLarge)
			}
			n, err := restoreFile(rt, rel, fileMode(fs.FileMode(h.Mode)), io.LimitReader(tr, remaining))
			remaining -= n
			if err != nil {
				return fmt.Errorf("restoring %q: %w", h.Name, err)
			}
		case tar.TypeSymlink:
			if rel == "" {
				return fmt.Errorf("refusing %q: a link at the root", h.Name)
			}
			if symlinkEscapes(rel, h.Linkname) {
				return fmt.Errorf("refusing %q: link target %q is outside its root", h.Name, h.Linkname)
			}
			if err := restoreSymlink(rt, rel, filepath.FromSlash(h.Linkname)); err != nil {
				return fmt.Errorf("restoring %q: %w", h.Name, err)
			}
		default:
			return fmt.Errorf("refusing %q: entry type %q is not a file, directory or symlink", h.Name, h.Typeflag)
		}
	}
}

// splitName maps an entry name "<i>/<rel>" to its root index and a local
// path. It refuses absolute names, any ".." component and an index outside
// [0,n). rel is "" for the root itself.
func splitName(name string, n int) (int, string, error) {
	idxStr, rel, _ := strings.Cut(strings.TrimSuffix(name, "/"), "/")
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idxStr != strconv.Itoa(idx) || idx < 0 || idx >= n {
		return 0, "", fmt.Errorf("refusing %q: not under a cache root", name)
	}
	if rel == "" {
		return idx, "", nil
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return 0, "", fmt.Errorf("refusing %q: contains ..", name)
		}
	}
	rel = filepath.FromSlash(rel)
	if !filepath.IsLocal(rel) {
		return 0, "", fmt.Errorf("refusing %q: not a local path", name)
	}
	return idx, rel, nil
}

// clearForReplace removes whatever non-directory sits at rel, so the next
// create makes a new entry instead of following an existing symlink.
func clearForReplace(rt *os.Root, rel string) error {
	if err := rt.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		return err
	}
	fi, err := rt.Lstat(rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case fi.IsDir():
		return nil // the create then fails, rather than delete a tree
	}
	return rt.Remove(rel)
}

func restoreFile(rt *os.Root, rel string, mode fs.FileMode, src io.Reader) (int64, error) {
	if err := clearForReplace(rt, rel); err != nil {
		return 0, err
	}
	// O_EXCL fails rather than follow a link that appeared in between.
	f, err := rt.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, src)
	if err == nil {
		err = f.Chmod(mode) // exact mode, whatever the umask
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func restoreSymlink(rt *os.Root, rel, target string) error {
	if err := clearForReplace(rt, rel); err != nil {
		return err
	}
	return rt.Symlink(target, rel)
}
