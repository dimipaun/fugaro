// Package cache restores and writes back content-addressed dependency
// caches (design §4.1): cache/<slug>/<workflow>/<key>.tar.zst, where key is
// the SHA-256 of the key files' contents, the cached paths and the base
// image. Archives are immutable, so parallel runs never contend.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dimipaun/fugaro/internal/config"
)

// Key-file caps. A key file is hashed whole, so an oversized one (or one
// that grows while it is read) makes the entry not cached rather than
// holding the run.
const (
	maxKeyFileBytes = 64 << 20  // one key file
	maxKeyBytes     = 256 << 20 // every key file of one entry
)

// ErrKeyTooLarge means an entry's key files pass maxKeyFileBytes or
// maxKeyBytes; the entry is not cached.
var ErrKeyTooLarge = errors.New("cache key files are too large to hash")

// errNotRegular marks a key file that is left out of the key.
var errNotRegular = errors.New("not a regular file")

// KeyOf computes e's key in the checkout at root. ok is false when none of
// the key files exist, which means the entry is not cached.
//
// Besides the key files' contents and e's paths, the key covers baseImage
// (the base image reference, a digest when the build pinned one) and
// toolchain, a hash the runner computes over the repo's image: settings
// (node, jdk, apt, setup). A toolchain change such as a Node bump then
// invalidates caches holding native binaries built for the old one.
//
// The committed tree and the agent both control the key files, so each is
// opened through an os.Root of root, with O_NOFOLLOW and O_NONBLOCK, and
// hashed only if it is a regular file and not a symlink: a symlink (to
// /dev/zero, say), a
// FIFO or a device is left out, with a warning through warn (which may be
// nil). Reads stop when ctx ends, and past the size caps (ErrKeyTooLarge).
func KeyOf(ctx context.Context, root string, e config.CacheEntry, baseImage, toolchain string, warn func(msg string, args ...any)) (string, bool, error) {
	var files []string
	for _, pattern := range e.Key {
		// WithNoFollow: the agent can plant directory links that loop, and
		// following them makes a "**" walk grow without bound.
		matches, err := doublestar.Glob(os.DirFS(root), pattern, doublestar.WithFilesOnly(), doublestar.WithNoFollow())
		if err != nil {
			return "", false, fmt.Errorf("cache key %q: %w", pattern, err)
		}
		files = append(files, matches...)
	}
	slices.Sort(files)
	files = slices.Compact(files)
	if len(files) == 0 {
		return "", false, nil
	}
	rt, err := os.OpenRoot(root)
	if err != nil {
		return "", false, err
	}
	defer rt.Close()
	h := sha256.New()
	fmt.Fprintf(h, "fugaro-cache-v1\nbase %s\ntoolchain %s\npaths %s\n", baseImage, toolchain, strings.Join(e.Paths, "\x00"))
	var total int64
	hashed := 0
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		sum, n, err := hashKeyFile(ctx, rt, f, maxKeyBytes-total)
		switch {
		case errors.Is(err, errNotRegular):
			if warn != nil {
				warn("cache key file left out", "file", f, "err", err.Error())
			}
			continue
		case err != nil:
			return "", false, fmt.Errorf("cache key file %s: %w", f, err)
		}
		total += n
		hashed++
		fmt.Fprintf(h, "file %s %x\n", f, sum)
	}
	if hashed == 0 {
		return "", false, nil
	}
	return hex.EncodeToString(h.Sum(nil)), true, nil
}

// hashKeyFile hashes the key file name inside rt, reading at most
// min(maxKeyFileBytes, budget) bytes. errNotRegular marks a file that
// can't be opened as, or isn't, a regular file.
func hashKeyFile(ctx context.Context, rt *os.Root, name string, budget int64) ([]byte, int64, error) {
	name = filepath.FromSlash(name)
	// os.Root follows a final symlink that stays inside it, O_NOFOLLOW or
	// not, so a link is refused on Lstat first; one swapped in after that
	// still can't leave the root, and the fstat below still wants a
	// regular file.
	if fi, err := rt.Lstat(name); err != nil || fi.Mode()&fs.ModeSymlink != 0 {
		if err == nil {
			err = errors.New("a symlink")
		}
		return nil, 0, fmt.Errorf("%w: %w", errNotRegular, err)
	}
	f, err := rt.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", errNotRegular, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !fi.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%w (%s)", errNotRegular, fi.Mode().Type())
	}
	limit := min(int64(maxKeyFileBytes), budget)
	if fi.Size() > limit {
		return nil, 0, ErrKeyTooLarge
	}
	fh := sha256.New()
	n, err := io.Copy(fh, io.LimitReader(ctxReader{ctx, f}, limit+1))
	switch {
	case err != nil:
		return nil, n, err
	case n > limit:
		return nil, n, ErrKeyTooLarge // it grew while being read
	}
	return fh.Sum(nil), n, nil
}

// ctxReader stops reading once ctx ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// Resolve turns fugaro.yaml cache paths into absolute directories: "~/x"
// is under home, "x" under the checkout. Absolute paths, "..", any .git
// component (a restore must never write git hooks or config the runner's
// git then runs) and roots that repeat or nest inside one another (the
// same files would be archived twice) are refused, so a cache can never
// target the system or the repository's metadata.
//
// The checks are lexical. A symlink at or above a cached directory would
// defeat them, so CheckLinks must pass too, at restore and again at
// write-back, before a root is used.
func Resolve(paths []string, root, home string) ([]string, error) {
	out := make([]string, len(paths))
	for i, p := range paths {
		base, clean, err := resolveOne(p, root, home)
		if err != nil {
			return nil, err
		}
		out[i] = filepath.Join(base, filepath.FromSlash(clean))
	}
	for i, a := range out {
		for j, b := range out[:i] {
			if a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator)) {
				return nil, fmt.Errorf("cache paths %q and %q overlap", paths[j], paths[i])
			}
		}
	}
	return out, nil
}

// resolveOne splits the fugaro.yaml cache path p into its base (home or
// the checkout root) and its clean slash-separated path below that base,
// refusing what Resolve refuses.
func resolveOne(p, root, home string) (base, clean string, err error) {
	base, rel := root, p
	switch {
	case strings.HasPrefix(p, "~/"):
		base, rel = home, strings.TrimPrefix(p, "~/")
	case strings.HasPrefix(p, "~"): // "~" alone or "~user/…": not a checkout directory
		return "", "", fmt.Errorf("cache path %q must be ~/<dir> or a directory inside the checkout", p)
	}
	clean = path.Clean(rel)
	if rel == "" || path.IsAbs(rel) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", "", fmt.Errorf("cache path %q must be ~/<dir> or a directory inside the checkout", p)
	}
	for _, part := range strings.Split(clean, "/") {
		if strings.EqualFold(part, ".git") {
			return "", "", fmt.Errorf("cache path %q must not be inside a .git directory", p)
		}
	}
	if base == home && holdsCredentials(clean) {
		return "", "", fmt.Errorf("cache path %q may hold credentials or agent sessions, and caches are uploaded to the runs bucket; cache a tool's own directory instead, such as ~/.config/<tool> or ~/.cache/<tool>", p)
	}
	return base, clean, nil
}

// ErrLinkedRoot means a cache root is a symlink or lies under one, below
// its base. Such a root is never followed: a committed link, or one the
// agent made, could point a cache at ~/.claude, the runner's credentials
// or .git, past every check Resolve makes.
var ErrLinkedRoot = errors.New("cache path is a symlink or lies under one")

// CheckLinks refuses (ErrLinkedRoot) any fugaro.yaml cache path that is a
// symlink, or has one on its way down from its base (home or the checkout
// root, which are trusted as they are). Each component is judged with
// Lstat inside an os.Root of the base, so nothing is followed. A path
// that does not exist yet passes: a restore creates it as a directory.
// It refuses what Resolve refuses too.
func CheckLinks(paths []string, root, home string) error {
	for _, p := range paths {
		base, clean, err := resolveOne(p, root, home)
		if err != nil {
			return err
		}
		if err := linkFree(base, clean); err != nil {
			return fmt.Errorf("cache path %q: %w", p, err)
		}
	}
	return nil
}

// linkFree walks clean down from base, refusing the first symlink.
func linkFree(base, clean string) error {
	rt, err := os.OpenRoot(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // nothing there to follow
	}
	if err != nil {
		return err
	}
	defer rt.Close()
	parts := strings.Split(clean, "/")
	for i := range parts {
		p := filepath.Join(parts[:i+1]...)
		fi, err := rt.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return err
		case fi.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%w (%s)", ErrLinkedRoot, filepath.ToSlash(p))
		}
	}
	return nil
}

// credentialPaths are home-relative paths that hold credentials or the
// agent's sessions (which can quote tool output). A cache must not be one
// of them, inside one, or contain one: every cache is uploaded to the runs
// bucket. A first component starting with ".claude" is refused as well.
var credentialPaths = [][]string{
	{".ssh"}, {".config", "gcloud"}, {".config", "gh"}, {".git-credentials"}, {".npmrc"}, {".netrc"},
	{".docker"}, {".aws"}, {".kube"}, {".gnupg"},
}

// holdsCredentials reports whether the home-relative clean path is,
// is inside, or contains a credential path.
func holdsCredentials(clean string) bool {
	parts := strings.Split(clean, "/")
	if strings.HasPrefix(strings.ToLower(parts[0]), ".claude") {
		return true
	}
	for _, c := range credentialPaths {
		n := min(len(parts), len(c))
		match := true
		for i := range n {
			if !strings.EqualFold(parts[i], c[i]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// ResolveWarnings returns a warning for each cache path that Resolve
// accepts but that may still hold credentials: anything under ~/.config
// (such as gh's hosts.yml). Callers log them; they do not refuse the path.
func ResolveWarnings(paths []string) []string {
	var out []string
	for _, p := range paths {
		rel, ok := strings.CutPrefix(p, "~/")
		if !ok {
			continue
		}
		parts := strings.Split(path.Clean(rel), "/")
		if strings.EqualFold(parts[0], ".config") {
			out = append(out, fmt.Sprintf("cache path %q is under ~/.config, which often holds credentials; it is uploaded to the runs bucket", p))
		}
	}
	return out
}

// ObjectKey is where key's archive lives.
func ObjectKey(slug, workflow, key string) string {
	return "cache/" + slug + "/" + workflow + "/" + key + ".tar.zst"
}

// ErrTooLarge means the cached paths exceed the size cap.
var ErrTooLarge = errors.New("cache archive exceeds the size cap")

// DefaultMaxBytes caps a cache's uncompressed size.
const DefaultMaxBytes int64 = 8 << 30
