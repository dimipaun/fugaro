// Package cache restores and writes back content-addressed dependency
// caches (design §4.1): cache/<slug>/<workflow>/<key>.tar.zst, where key is
// the SHA-256 of the key files' contents, the cached paths and the base
// image. Archives are immutable, so parallel runs never contend.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dimipaun/fugaro/internal/config"
)

// KeyOf computes e's key in the checkout at root. ok is false when none of
// the key files exist, which means the entry is not cached.
//
// Besides the key files' contents and e's paths, the key covers baseImage
// (the base image reference, a digest when the build pinned one) and
// toolchain, a hash the runner computes over the repo's image: settings
// (node, jdk, apt, setup). A toolchain change such as a Node bump then
// invalidates caches holding native binaries built for the old one.
func KeyOf(root string, e config.CacheEntry, baseImage, toolchain string) (string, bool, error) {
	var files []string
	for _, pattern := range e.Key {
		matches, err := doublestar.Glob(os.DirFS(root), pattern, doublestar.WithFilesOnly())
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
	h := sha256.New()
	fmt.Fprintf(h, "fugaro-cache-v1\nbase %s\ntoolchain %s\npaths %s\n", baseImage, toolchain, strings.Join(e.Paths, "\x00"))
	for _, f := range files {
		fh := sha256.New()
		file, err := os.Open(filepath.Join(root, f))
		if err != nil {
			return "", false, err
		}
		_, err = io.Copy(fh, file)
		file.Close()
		if err != nil {
			return "", false, err
		}
		fmt.Fprintf(h, "file %s %x\n", f, fh.Sum(nil))
	}
	return hex.EncodeToString(h.Sum(nil)), true, nil
}

// Resolve turns fugaro.yaml cache paths into absolute directories: "~/x"
// is under home, "x" under the checkout. Absolute paths, "..", any .git
// component (a restore must never write git hooks or config the runner's
// git then runs) and roots that repeat or nest inside one another (the
// same files would be archived twice) are refused, so a cache can never
// target the system or the repository's metadata.
func Resolve(paths []string, root, home string) ([]string, error) {
	out := make([]string, len(paths))
	for i, p := range paths {
		base, rel := root, p
		switch {
		case strings.HasPrefix(p, "~/"):
			base, rel = home, strings.TrimPrefix(p, "~/")
		case strings.HasPrefix(p, "~"): // "~" alone or "~user/…": not a checkout directory
			return nil, fmt.Errorf("cache path %q must be ~/<dir> or a directory inside the checkout", p)
		}
		clean := path.Clean(rel)
		if rel == "" || path.IsAbs(rel) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("cache path %q must be ~/<dir> or a directory inside the checkout", p)
		}
		for _, part := range strings.Split(clean, "/") {
			if strings.EqualFold(part, ".git") {
				return nil, fmt.Errorf("cache path %q must not be inside a .git directory", p)
			}
		}
		if base == home && holdsCredentials(clean) {
			return nil, fmt.Errorf("cache path %q may hold credentials or agent sessions, and caches are uploaded to the runs bucket", p)
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

// credentialPaths are home-relative paths that hold credentials or the
// agent's sessions (which can quote tool output). A cache must not be one
// of them, inside one, or contain one: every cache is uploaded to the runs
// bucket. A first component starting with ".claude" is refused as well.
var credentialPaths = [][]string{
	{".ssh"}, {".config", "gcloud"}, {".git-credentials"}, {".npmrc"}, {".netrc"}, {".docker"},
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
