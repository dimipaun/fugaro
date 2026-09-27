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
	"slices"
	"strconv"
	"strings"
	"syscall"

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

// headerCost is what every entry after the manifest is charged against
// the size cap, on top of its content: a tar header is 512 bytes, and it
// also stands for the inode a restore creates. Without it an archive of
// millions of empty entries would compress to nearly nothing.
const headerCost = 512

// maxEntries caps the entries in one archive, whatever the size cap.
const maxEntries = 1 << 20

// maxHops caps the symlinks followed while resolving one path.
const maxHops = 40

// ErrBadArchive marks an archive that is corrupt or that Extract refused
// (an unsafe entry, the size cap, a bad manifest). It is never set for an
// I/O error reading the archive or writing the roots, so an archive that
// fails with it will fail every restore.
var ErrBadArchive = errors.New("bad cache archive")

// badf builds an error that matches ErrBadArchive.
func badf(format string, args ...any) error {
	return fmt.Errorf("%w: %w", ErrBadArchive, fmt.Errorf(format, args...))
}

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
// Other file types are skipped, and so are symlinks that resolve outside
// their root, which Extract would refuse (see writeArchive). It returns the
// bytes charged against maxBytes (content plus headerCost per entry), or
// ErrTooLarge once they would pass maxBytes.
func Write(w io.Writer, roots []string, maxBytes int64) (int64, error) {
	st, err := writeArchive(w, roots, maxBytes)
	return st.bytes, err
}

// writeStats is what writeArchive reports besides an error.
type writeStats struct {
	bytes        int64 // charged against the size cap
	skippedLinks int   // symlinks left out because they resolve outside their root
}

// writeArchive is Write, also counting the symlinks it left out so Save
// can warn: a cache restored without, say, node_modules/.bin links may
// break the build, and the log should say why.
func writeArchive(w io.Writer, roots []string, maxBytes int64) (writeStats, error) {
	zw, err := zstd.NewWriter(w)
	if err != nil {
		return writeStats{}, fmt.Errorf("starting zstd: %w", err)
	}
	tw := tar.NewWriter(zw)
	st, err := writeEntries(tw, roots, maxBytes)
	if err != nil {
		_ = zw.Close()
		return st, err
	}
	if err := tw.Close(); err != nil {
		_ = zw.Close()
		return st, fmt.Errorf("closing tar: %w", err)
	}
	if err := zw.Close(); err != nil {
		return st, fmt.Errorf("closing zstd: %w", err)
	}
	return st, nil
}

func writeEntries(tw *tar.Writer, roots []string, maxBytes int64) (writeStats, error) {
	var st writeStats
	m, err := json.Marshal(manifest{Version: manifestVersion, Roots: len(roots)})
	if err != nil {
		return st, err
	}
	if err := tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o644, Size: int64(len(m)), Typeflag: tar.TypeReg}); err != nil {
		return st, fmt.Errorf("writing manifest: %w", err)
	}
	if _, err := tw.Write(m); err != nil {
		return st, fmt.Errorf("writing manifest: %w", err)
	}
	entries := 0
	charge := func(size int64) error {
		entries++
		if entries > maxEntries || st.bytes+headerCost+size > maxBytes {
			return ErrTooLarge
		}
		st.bytes += headerCost
		return nil
	}
	for i, root := range roots {
		dir, ok := walkRoot(root)
		if !ok {
			continue
		}
		if err := writeRoot(tw, i, dir, charge, &st); err != nil {
			if errors.Is(err, ErrTooLarge) {
				return st, err
			}
			return st, fmt.Errorf("archiving %s: %w", root, err)
		}
	}
	return st, nil
}

func writeRoot(tw *tar.Writer, i int, dir string, charge func(int64) error, st *writeStats) error {
	rt, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer rt.Close()
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
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
			if err := charge(0); err != nil {
				return err
			}
			return tw.WriteHeader(&tar.Header{Name: name + "/", Mode: 0o755, Typeflag: tar.TypeDir})
		case t&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			// The same physical check Extract makes: a link Extract
			// would refuse would make every restore fail and poison the key.
			if symlinkEscapes(rel, target) {
				st.skippedLinks++
				return nil
			}
			if out, err := resolvesOutside(rt, rel); err != nil || out {
				st.skippedLinks++
				return nil
			}
			if err := charge(0); err != nil {
				return err
			}
			return tw.WriteHeader(&tar.Header{Name: name, Linkname: filepath.ToSlash(target), Typeflag: tar.TypeSymlink})
		case t.IsRegular():
			fi, err := d.Info()
			if err != nil {
				return err
			}
			if err := charge(fi.Size()); err != nil {
				return err
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
			st.bytes += n
			if err != nil {
				return fmt.Errorf("archiving %s: %w", p, err)
			}
		}
		return nil // sockets, devices, FIFOs: not cache content
	})
}

// fileMode masks a regular file's mode to 0o755 when any execute bit is
// set and 0o644 otherwise, so no setuid, setgid or sticky bit survives.
func fileMode(m fs.FileMode) fs.FileMode {
	if m&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// symlinkEscapes is the lexical first filter for a link at rel (relative
// to its root) pointing at target: absolute, empty, or leaving the root on
// its face. It misses escapes through other links; resolvesOutside does not.
func symlinkEscapes(rel, target string) bool {
	target = filepath.FromSlash(target)
	return target == "" || filepath.IsAbs(target) || !filepath.IsLocal(filepath.Join(filepath.Dir(rel), target))
}

// resolvesOutside reports whether name, resolved inside rt the way the
// kernel would (following every symlink, the last one included), leaves
// rt. A component that does not exist is taken lexically, so a dangling
// "missing/x/../.." is judged as if missing and x were directories, and
// ".." above the root or an absolute link target counts as outside. An
// error (too many links, an unreadable component) should be treated as
// outside: callers fail closed.
func resolvesOutside(rt *os.Root, name string) (bool, error) {
	todo := strings.Split(filepath.ToSlash(name), "/")
	var cur []string // physical components below the root, none of them a link
	hops := 0
	for len(todo) > 0 {
		c := todo[0]
		todo = todo[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			if len(cur) == 0 {
				return true, nil
			}
			cur = cur[:len(cur)-1]
			continue
		}
		p := filepath.Join(append(slices.Clone(cur), c)...)
		fi, err := rt.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
			cur = append(cur, c) // missing: lexical from here
			continue
		case err != nil:
			return false, err
		case fi.Mode()&fs.ModeSymlink == 0:
			cur = append(cur, c)
			continue
		}
		if hops++; hops > maxHops {
			return false, fmt.Errorf("resolving %s: too many levels of symbolic links", name)
		}
		target, err := rt.Readlink(p)
		if err != nil {
			return false, err
		}
		target = filepath.ToSlash(target)
		if target == "" || strings.HasPrefix(target, "/") {
			return true, nil
		}
		todo = append(strings.Split(target, "/"), todo...)
	}
	return false, nil
}

// trackedReader remembers the first non-EOF error its source returned, so
// a decode failure can be told apart from an I/O failure underneath it.
type trackedReader struct {
	r   io.Reader
	err error
}

func (t *trackedReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF && t.err == nil {
		t.err = err
	}
	return n, err
}

// link is a symlink Extract created, rechecked once all entries are in.
type link struct {
	idx int
	rel string
}

// extractor holds one Extract's state.
type extractor struct {
	roots   []*os.Root
	src     *trackedReader // the compressed archive as read
	links   []link         // in creation order
	linkSet map[link]bool  // the same links, to record each once
	safe    map[link]bool  // directories resolved inside their root; reset when a link changes
}

// readErr classifies an error reading the archive: an I/O error from the
// source is returned as is, anything else is a corrupt archive.
func (x *extractor) readErr(what string, err error) error {
	if x.src.err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return badf("%s: %w", what, err)
}

// Extract restores an archive written by Write into roots, which must be
// as many as the manifest says. It refuses, before writing it, any entry
// that could land outside its root: every write goes through an os.Root,
// which refuses ".." and symlink escapes at any path component, and each
// entry's parent is first resolved physically inside the root. It stops at
// the first refused entry, and refuses more than maxBytes of content plus
// headerCost per entry. Once all entries are in, or the restore has
// stopped, every symlink it created is resolved again, since a later entry
// can make an earlier link escape; one that resolves outside its root is
// removed and reported. A refusal or a corrupt archive matches
// ErrBadArchive.
func Extract(r io.Reader, roots []string, maxBytes int64) (err error) {
	x := &extractor{src: &trackedReader{r: r}, linkSet: map[link]bool{}, safe: map[link]bool{}}
	defer func() {
		for _, rt := range x.roots {
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
		x.roots = append(x.roots, rt)
	}
	// Registered after the roots are open, so it runs before they close,
	// and on every return, a partial restore included.
	defer func() {
		if lerr := x.checkLinks(); lerr != nil {
			err = errors.Join(err, lerr)
		}
	}()
	return x.extract(maxBytes)
}

func (x *extractor) extract(maxBytes int64) error {
	zr, err := zstd.NewReader(x.src, zstd.WithDecoderMaxWindow(maxWindow))
	if err != nil {
		return x.readErr("opening zstd", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)

	h, err := tr.Next()
	if err != nil {
		return x.readErr("reading manifest", err)
	}
	if h.Name != manifestName || h.Typeflag != tar.TypeReg {
		return badf("first entry is %q, not the manifest", h.Name)
	}
	var m manifest
	if err := json.NewDecoder(io.LimitReader(tr, 4096)).Decode(&m); err != nil {
		return x.readErr("reading manifest", err)
	}
	if m.Version != manifestVersion {
		return badf("unsupported cache archive version %d", m.Version)
	}
	if m.Roots != len(x.roots) {
		return badf("archive has %d roots, want %d", m.Roots, len(x.roots))
	}

	remaining, entries := maxBytes, 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return x.readErr("reading archive", err)
		}
		if entries++; entries > maxEntries || remaining < headerCost {
			return badf("refusing %q: %w", h.Name, ErrTooLarge)
		}
		remaining -= headerCost
		idx, rel, err := splitName(h.Name, len(x.roots))
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if rel == "" {
				continue // the root itself
			}
			if err := x.inside(idx, rel, h.Name); err != nil {
				return err
			}
			if err := x.roots[idx].MkdirAll(rel, 0o755); err != nil {
				return fmt.Errorf("restoring %q: %w", h.Name, err)
			}
		case tar.TypeReg:
			if rel == "" {
				return badf("refusing %q: a file at the root", h.Name)
			}
			if h.Size < 0 || h.Size > remaining {
				return badf("refusing %q: %w", h.Name, ErrTooLarge)
			}
			if err := x.inside(idx, filepath.Dir(rel), h.Name); err != nil {
				return err
			}
			in := &trackedReader{r: io.LimitReader(tr, remaining)}
			n, err := x.restoreFile(idx, rel, fileMode(fs.FileMode(h.Mode)), in)
			remaining -= n
			if err != nil {
				if in.err != nil {
					return x.readErr(fmt.Sprintf("restoring %q", h.Name), err)
				}
				return fmt.Errorf("restoring %q: %w", h.Name, err)
			}
		case tar.TypeSymlink:
			if rel == "" {
				return badf("refusing %q: a link at the root", h.Name)
			}
			if symlinkEscapes(rel, h.Linkname) {
				return badf("refusing %q: link target %q is outside its root", h.Name, h.Linkname)
			}
			if err := x.inside(idx, filepath.Dir(rel), h.Name); err != nil {
				return err
			}
			if err := x.restoreSymlink(idx, rel, filepath.FromSlash(h.Linkname)); err != nil {
				return fmt.Errorf("restoring %q: %w", h.Name, err)
			}
		default:
			return badf("refusing %q: entry type %q is not a file, directory or symlink", h.Name, h.Typeflag)
		}
	}
}

// inside refuses an entry whose directory dir resolves outside its root.
// os.Root would refuse the write anyway; checking first makes the refusal
// an ErrBadArchive rather than an opaque I/O error.
func (x *extractor) inside(idx int, dir, name string) error {
	k := link{idx, dir}
	if dir == "." || x.safe[k] {
		return nil
	}
	out, err := resolvesOutside(x.roots[idx], dir)
	if err != nil {
		return badf("refusing %q: resolving its directory: %w", name, err)
	}
	if out {
		return badf("refusing %q: its directory resolves outside its root", name)
	}
	x.safe[k] = true
	return nil
}

// checkLinks resolves every link this Extract created. It judges them all
// against the tree as restored before removing any: removing one first
// would leave a missing component that the next is judged through
// lexically (q -> ., r -> q/.., p -> r/..: with r gone, p looks local).
// It then removes each that resolves outside its root (or can't be
// resolved) and returns an ErrBadArchive naming them, and repeats until a
// pass removes nothing.
func (x *extractor) checkLinks() error {
	var errs []error
	pending := slices.Clone(x.links)
	for {
		var kept, refused []link
		for _, l := range pending {
			rt := x.roots[l.idx]
			fi, err := rt.Lstat(l.rel)
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || (err == nil && fi.Mode()&fs.ModeSymlink == 0) {
				continue // replaced or gone
			}
			var out bool
			if err == nil {
				out, err = resolvesOutside(rt, l.rel)
			}
			if err == nil && !out {
				kept = append(kept, l)
			} else {
				refused = append(refused, l)
			}
		}
		if len(refused) == 0 {
			return errors.Join(errs...)
		}
		for _, l := range refused {
			if rerr := x.roots[l.idx].Remove(l.rel); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("removing link %d/%s: %w", l.idx, filepath.ToSlash(l.rel), rerr))
			}
			errs = append(errs, badf("refused link %d/%s: it resolves outside its root", l.idx, filepath.ToSlash(l.rel)))
		}
		pending = kept
	}
}

// splitName maps an entry name "<i>/<rel>" to its root index and a local
// path. It refuses absolute names, any ".." component and an index outside
// [0,n). rel is "" for the root itself.
func splitName(name string, n int) (int, string, error) {
	idxStr, rel, _ := strings.Cut(strings.TrimSuffix(name, "/"), "/")
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idxStr != strconv.Itoa(idx) || idx < 0 || idx >= n {
		return 0, "", badf("refusing %q: not under a cache root", name)
	}
	if rel == "" {
		return idx, "", nil
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return 0, "", badf("refusing %q: contains ..", name)
		}
	}
	rel = filepath.FromSlash(rel)
	if !filepath.IsLocal(rel) {
		return 0, "", badf("refusing %q: not a local path", name)
	}
	return idx, rel, nil
}

// clearForReplace removes whatever non-directory sits at rel, so the next
// create makes a new entry instead of following an existing symlink.
func (x *extractor) clearForReplace(idx int, rel string) error {
	rt := x.roots[idx]
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
	if fi.Mode()&fs.ModeSymlink != 0 {
		clear(x.safe) // directories resolved through this link may now differ
	}
	return rt.Remove(rel)
}

func (x *extractor) restoreFile(idx int, rel string, mode fs.FileMode, src io.Reader) (int64, error) {
	if err := x.clearForReplace(idx, rel); err != nil {
		return 0, err
	}
	// O_EXCL fails rather than follow a link that appeared in between.
	f, err := x.roots[idx].OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
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

func (x *extractor) restoreSymlink(idx int, rel, target string) error {
	if err := x.clearForReplace(idx, rel); err != nil {
		return err
	}
	clear(x.safe) // a new link can redirect a directory already judged
	if err := x.roots[idx].Symlink(target, rel); err != nil {
		return err
	}
	if l := (link{idx, rel}); !x.linkSet[l] {
		x.linkSet[l] = true
		x.links = append(x.links, l)
	}
	return nil
}
