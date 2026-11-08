// Package srctree keeps the Fugaro source of the installed version on disk
// for the troubleshoot agent to read.
//
// Fugaro, never the agent, fetches and writes everything here (D1602): the
// agent has Read, Grep and Glob and nothing else, so this package also writes
// the "changes since your version" file the agent reads. There are two
// transports and one contract. A shallow git checkout of the exact commit the
// binary was built from, when git is installed (D1610); otherwise the source
// tarball of that commit. Whatever is served carries a Record of what it
// holds, and a tree whose record does not name what was asked for is replaced
// rather than served, because a diagnosis is only as good as the version it
// read.
package srctree

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// SourceURL is the public repository the source comes from, without a suffix:
// the git transport appends ".git" and the tarball transport
// "/archive/<ref>.tar.gz". pluginwire.Repo is the same identity the release
// line verifies the installed tag against (internal/cli/releaseinfo.go).
const SourceURL = "https://github.com/" + pluginwire.Repo

// KindGit and KindTarball name the two transports a Record records.
const (
	KindGit     = "git"
	KindTarball = "tarball"
)

const (
	// recordName and treeName are the two names inside the cache directory.
	recordName = "record.json"
	treeName   = "tree"
	// stageName is where the next tree is built — an archive unpacked, or a
	// fetch — before it replaces the tree. It is fixed rather than random
	// because the directory holding it is this user's own and 0700, and it is
	// removed and recreated, never reused.
	stageName = ".stage"

	// maxArchive is what one archive may unpack to. The repository's source
	// is a few tens of megabytes; an archive that unpacks to more is not
	// this repository's source, whatever it says its name is (F21).
	maxArchive = 256 << 20
	// maxEntries caps the entries of one archive, so an archive of millions
	// of empty entries cannot cost more than its bytes suggest.
	maxEntries = 1 << 20
	// maxHops caps the links followed while resolving one link target.
	maxHops = 40

	// fetchTimeout bounds one download, gitWaitDelay how long a git command's
	// Wait drains output after git itself exited. A leaked descendant that
	// inherits the output pipe would otherwise hang the command (the same
	// guard internal/gitops and internal/testutil make).
	fetchTimeout  = 2 * time.Minute
	gitWaitDelay  = 5 * time.Second
	cacheDirMode  = 0o700
	treeDirMode   = 0o555
	treeWritable  = 0o755
	recordFileMod = 0o600
)

// gitCommitRE is what counts as a commit this package can fetch by: the
// full forty hex digits internal/cli.Commit carries, and nothing else. A
// short sha, a branch name or "unknown" is a build without an anchor.
var gitCommitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Record is what the cache holds: the version and commit the tree is of,
// which transport fetched it, and whether it could be tied to this build at
// all (D1611).
type Record struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Kind    string `json:"kind"`
	// MayNotMatch is set when the tree could not be anchored to the running
	// binary — a build with no commit and no release version — so the caller
	// says that the source may not match the build.
	MayNotMatch bool `json:"may_not_match"`
}

// Runner runs git with args in dir and returns its standard output. args
// already carry the hardening every call here makes; the caller's context
// bounds the command.
type Runner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// Fetcher returns the body of url. The caller's context bounds it.
type Fetcher func(ctx context.Context, url string) (io.ReadCloser, error)

// Options is one Ensure or ChangesSince call. Every field but Version and
// Commit has a working default: the environment and the platform this
// process runs as, the public repository, git from PATH and the network.
// Tests replace them, and nothing else does.
type Options struct {
	// Version is the installed version (internal/cli.Version): a release
	// like "v0.5.2", or "dev".
	Version string
	// Commit is the commit the binary was built from (internal/cli.Commit),
	// or "" / "unknown" for a build without one.
	Commit string
	// Getenv and GOOS decide where the cache lives (localcfg.UserDir).
	Getenv func(string) string
	GOOS   string
	// SourceURL overrides the repository the source comes from.
	SourceURL string
	// LookPath decides whether git can be run at all; a machine without it
	// takes the tarball.
	LookPath func(string) (string, error)
	// Run runs git; Fetch downloads a tarball.
	Run   Runner
	Fetch Fetcher
	// MaxArchive overrides maxArchive, the bytes one archive may unpack to.
	MaxArchive int64
}

// want is what this call is after: the Record it will serve, plus the ref
// to fetch for it. ref is "" when only the remote can name it (D1611).
type want struct {
	Record
	ref string
}

// Ensure returns the directory holding the source of the installed version,
// fetching it when the cache does not already hold exactly that version, and
// the Record of what it holds. The tree is left read-only: nothing, the agent
// included, writes into the source it is reading. Another version is built in
// a directory of its own and replaces the tree only once it is there, so the
// tree is never widened at all.
func Ensure(ctx context.Context, o Options) (string, Record, error) {
	o = o.withDefaults()
	w := targetFor(o)
	if _, err := o.LookPath("git"); err != nil {
		w.Kind = KindTarball
	} else {
		w.Kind = KindGit
	}
	root, err := cacheRoot(o)
	if err != nil {
		return "", Record{}, err
	}
	if w.ref == "" {
		// A build with no commit and no release version: only the remote can
		// name what to fetch, and the newest release is the answer (D1611).
		// It is asked once per call, so the recorded version is compared
		// against it like any other.
		tag, err := newestTag(ctx, o, root)
		if err != nil {
			return "", Record{}, err
		}
		w.ref, w.Version = tag, tag
	}
	dir := filepath.Join(root, treeName)
	if rec, ok := readRecord(root); ok && matches(rec, w) {
		if fi, err := os.Lstat(dir); err == nil && fi.IsDir() {
			// A tree git fetched is read only when its ".git" is this cache's
			// own: a link, a "gitdir:" file or a commondir file there would
			// have the check below ask another repository what its HEAD is,
			// and a tree of another version would then be served.
			if (rec.Kind != KindGit || isRepository(dir)) && treeIsAt(ctx, o, dir, rec) {
				return dir, rec, nil
			}
		}
	}
	commit, err := fetch(ctx, o, root, w)
	if err != nil {
		return "", Record{}, err
	}
	w.Commit = commit
	if err := writeRecord(root, w.Record); err != nil {
		return "", Record{}, err
	}
	return dir, w.Record, nil
}

// treeIsAt reports whether a tree the cache already holds is the one the
// record beside it names. The record is written after the tree is installed,
// so a run that dies in between — or two runs at once — leaves a record
// naming a version the tree is not, and a cache that answered from the record
// alone would serve the wrong source for a diagnosis. The commit HEAD is at
// is the identity the record keeps, so it is asked for rather than assumed
// (D1610). A tree from the tarball transport carries nothing to ask, and
// git's answer is taken as it comes: a tree that cannot name its commit is
// one to fetch again, not an error to stop on.
func treeIsAt(ctx context.Context, o Options, dir string, rec Record) bool {
	if rec.Kind != KindGit {
		return true
	}
	out, err := o.Run(ctx, dir, gitArgs("rev-parse", "HEAD")...)
	return err == nil && strings.TrimSpace(string(out)) == rec.Commit
}

// matches reports whether a record already names what this call is after. A
// recorded version that is not the requested one is never served: the whole
// point of the cache is that what it holds is the source of the version the
// person is running. A want without a commit is one the remote alone can
// name — a release tag, or a development build — so the commit the tree
// happened to land on does not decide it.
func matches(rec Record, w want) bool {
	return rec.Kind == w.Kind && rec.Version == w.Version && rec.MayNotMatch == w.MayNotMatch &&
		(w.Commit == "" || rec.Commit == w.Commit)
}

// targetFor is the record this call will serve and the ref to fetch, without
// touching the network: a build that carries a commit is fetched at exactly
// that commit, a release version at its own tag, and a build with neither
// (a development build) is anchored at the newest release tag only if the
// cache has to be filled (D1611).
func targetFor(o Options) want {
	if gitCommitRE.MatchString(o.Commit) {
		return want{Record: Record{Version: o.Version, Commit: o.Commit}, ref: o.Commit}
	}
	if tag, err := pluginwire.Tag(o.Version); err == nil {
		return want{Record: Record{Version: tag}, ref: tag}
	}
	return want{Record: Record{Version: o.Version, MayNotMatch: true}}
}

// fetch fills the cache and returns the commit the tree ended up at.
func fetch(ctx context.Context, o Options, root string, w want) (string, error) {
	if w.Kind == KindGit {
		return gitTree(ctx, o, root, w)
	}
	return tarTree(ctx, o, root, w)
}

// newestTag is the newest release tag the remote publishes, for a build that
// carries neither a commit nor a release version (D1611). git orders the
// tags, so this package needs no version comparison of its own. Without git
// there is nothing to ask, and a tarball has no version to name. The question
// is asked in a repository this call makes, like every other git call here
// (stageRepo): git run in the working directory would execute what the
// configuration of whatever repository that happens to be names.
func newestTag(ctx context.Context, o Options, root string) (string, error) {
	if _, err := o.LookPath("git"); err != nil {
		return "", fmt.Errorf("this is a %q build of fugaro and git is not installed, so there is no version to fetch the source of", o.Version)
	}
	stage, err := stageRepo(ctx, o, root)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	out, err := o.Run(ctx, stage, gitArgs("ls-remote", "--tags", "--refs", "--sort=-v:refname", "origin", "refs/tags/v*")...)
	if err != nil {
		return "", fmt.Errorf("listing the release tags of %s: %w", o.SourceURL, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		_, ref, ok := strings.Cut(line, "\t")
		if tag := strings.TrimPrefix(ref, "refs/tags/"); ok && tag != ref && pluginwire.ValidTag(tag) {
			return tag, nil
		}
	}
	return "", fmt.Errorf("%s publishes no release tag", o.SourceURL)
}

// isRepository reports whether the tree at dir holds a repository of this
// package's own, rather than a name that leads to one somewhere else. git
// follows ".git" wherever it points, and a link, a "gitdir:" file, and a
// commondir file inside a real directory all make it read and write another
// repository's refs and object store — the HEAD of a working copy of the
// person's, say — while the call looks like a cache being read. Only a plain
// directory, of the kind this package's own "git init" makes, answers yes: the
// tarball transport is the only other writer of a tree, and entryPath refuses
// an archive that names .git at all. What that buys is a repository this
// package may read without writing into somebody's: it is not provenance. A
// copy of a clone's ".git" is a plain directory too and answers yes, and
// nothing here authenticates the bytes of a tree — the record is a note beside
// it, not a signature, against anything that can write both. Both readers of a
// tree already there ask it: the warm path, before it lets git say what commit
// the tree is at — a "gitdir:" file would have another repository answer, and a
// tree of another version be served — and ChangesSince, before it reads a
// history, which a "gitdir:" file would have answered with another
// repository's releases.
func isRepository(dir string) bool {
	at := filepath.Join(dir, ".git")
	fi, err := os.Lstat(at)
	if err != nil || !fi.IsDir() {
		return false
	}
	if _, err := os.Lstat(filepath.Join(at, "commondir")); err == nil {
		return false
	}
	return true
}

// stageRepo makes, in the cache directory, a repository of this call's own:
// git init, and a remote naming the source. Every call that fetches or checks
// out runs in one of these rather than in the tree the cache holds: git
// executes the configuration of the repository it runs in — a filter on a
// checkout, a remote's uploadpack, an sshCommand — and the tree sits under a
// directory anything running as this user can write. The caller removes what
// it returns; a fetch that replaces the tree renames it into place.
func stageRepo(ctx context.Context, o Options, root string) (string, error) {
	stage := filepath.Join(root, stageName)
	// Whatever is at the stage's name was not written by this call, and a
	// repository git init finds keeps its own configuration: it is removed
	// rather than initialised into.
	if err := os.RemoveAll(stage); err != nil {
		return "", err
	}
	if err := os.MkdirAll(stage, cacheDirMode); err != nil {
		return "", err
	}
	if _, err := o.Run(ctx, stage, gitArgs("init", "--quiet")...); err != nil {
		return "", err
	}
	url := o.SourceURL + ".git"
	if _, err := o.Run(ctx, stage, gitArgs("remote", "add", "origin", url)...); err != nil {
		return "", err
	}
	return stage, nil
}

// gitTree fetches ref into the tree and checks it out detached. The commit
// HEAD ends at is what the Record keeps, and for a build that carries a
// commit it must be that commit: the tree is only ever read, so a wrong one
// misleads a diagnosis rather than running anything, but a wrong answer is
// still the wrong answer (D1610).
//
// The fetch runs in a repository this call initialises itself, and the tree
// is replaced by it only once it has succeeded, whatever is at the tree's
// name now — a directory, a link or a file decides nothing, it is removed.
// A fetch that fails therefore leaves the tree as it was, and one that
// succeeds leaves a tree whose configuration is this package's own.
func gitTree(ctx context.Context, o Options, root string, w want) (string, error) {
	stage, err := stageRepo(ctx, o, root)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if _, err := o.Run(ctx, stage, gitArgs("fetch", "--quiet", "--depth", "1", "--force", "origin", w.ref)...); err != nil {
		return "", fmt.Errorf("fetching %s: %w", w.ref, err)
	}
	if _, err := o.Run(ctx, stage, gitArgs("checkout", "--quiet", "--detach", "--force", "FETCH_HEAD")...); err != nil {
		return "", err
	}
	out, err := o.Run(ctx, stage, gitArgs("rev-parse", "HEAD")...)
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(out))
	if w.Commit != "" && head != w.Commit {
		return "", fmt.Errorf("the source of %s is at commit %s, not the %s this fugaro was built from", w.ref, head, w.Commit)
	}
	dir := filepath.Join(root, treeName)
	if err := removeTree(dir); err != nil {
		return "", err
	}
	if err := os.Rename(stage, dir); err != nil {
		return "", err
	}
	if err := chmodTree(dir, false); err != nil {
		return "", err
	}
	return head, nil
}

// tarTree downloads the archive of ref and unpacks it over the tree. The
// archive carries no signature to check (docs/release.md's checksums cover
// the release archives, not a source tarball), so the trust is TLS to GitHub
// and the ref that was asked for; the unpack is hardened because it is the
// one place here that writes bytes a server chose (F21).
func tarTree(ctx context.Context, o Options, root string, w want) (string, error) {
	url := o.SourceURL + "/archive/" + w.ref + ".tar.gz"
	body, err := o.Fetch(ctx, url)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", url, err)
	}
	defer body.Close()
	stage := filepath.Join(root, stageName)
	if err := os.RemoveAll(stage); err != nil {
		return "", err
	}
	if err := os.MkdirAll(stage, cacheDirMode); err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if err := unpack(body, stage, o.MaxArchive); err != nil {
		return "", fmt.Errorf("unpacking %s: %w", url, err)
	}
	dir := filepath.Join(root, treeName)
	if err := removeTree(dir); err != nil {
		return "", err
	}
	if err := os.Rename(stage, dir); err != nil {
		return "", err
	}
	if err := chmodTree(dir, false); err != nil {
		return "", err
	}
	return w.Commit, nil
}

// unpack writes a GitHub source archive into dest, which must exist and be
// empty. Every entry goes through an os.Root, so no name can land outside
// dest, and the archive's single top-level directory is stripped. The global
// pax header a git archive opens with holds no file, so it is skipped before
// its name is judged. An entry that escapes — an absolute name, a ".."
// component, a link whose target leaves the tree — a second top-level
// directory, an entry that is not a directory, a regular file or such a link,
// and an archive over max bytes are all refused (F21). So is an entry whose
// directory is a link an earlier entry planted rather than a directory of the
// archive's own, a link that resolves out of the tree once every entry is in
// place, and any entry named .git: that is where git run in the tree reads a
// repository's own configuration, and the cache's own repository is the only
// one there.
//
// A link that stays inside the tree is created rather than refused: the
// repository itself ships links under .claude/skills, and refusing every one
// would break the very fallback this transport is.
func unpack(body io.Reader, dest string, max int64) error {
	gz, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("reading the archive: %w", err)
	}
	defer gz.Close()
	rt, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer rt.Close()
	tr := tar.NewReader(gz)
	real := map[string]bool{}
	var links []string
	top, total, entries := "", int64(0), 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return checkLinks(rt, links)
		}
		if err != nil {
			return fmt.Errorf("reading the archive: %w", err)
		}
		if entries++; entries > maxEntries {
			return fmt.Errorf("refusing the archive: more than %d entries", maxEntries)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			// A git archive opens with a global pax header — "pax_global_header"
			// — which writes nothing; Go's reader hands it to the caller all
			// the same, and its name is not the archive's top-level directory.
			continue
		}
		rel, err := entryPath(h.Name, &top)
		if err != nil {
			return err
		}
		if rel == "" {
			continue // the archive's own top-level directory
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := inside(rt, real, h.Name, rel); err != nil {
				return err
			}
			if err := rt.MkdirAll(rel, treeWritable); err != nil {
				return fmt.Errorf("writing %q: %w", h.Name, err)
			}
		case tar.TypeReg:
			if h.Size < 0 || h.Size > max-total {
				return fmt.Errorf("refusing %q: the archive unpacks to more than %d bytes", h.Name, max)
			}
			total += h.Size
			if err := inside(rt, real, h.Name, rel); err != nil {
				return err
			}
			if err := writeEntry(rt, rel, h, tr); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if escapes(rel, h.Linkname) {
				return fmt.Errorf("refusing %q: its link target %q leaves the source tree", h.Name, h.Linkname)
			}
			if err := inside(rt, real, h.Name, rel); err != nil {
				return err
			}
			if err := mkdirFor(rt, rel); err != nil {
				return fmt.Errorf("writing %q: %w", h.Name, err)
			}
			if err := rt.Symlink(filepath.FromSlash(h.Linkname), rel); err != nil {
				return fmt.Errorf("writing %q: %w", h.Name, err)
			}
			links = append(links, rel)
		default:
			return fmt.Errorf("refusing %q: a %s entry is not a directory, a file or a link", h.Name, entryType(h.Typeflag))
		}
	}
}

// entryPath maps one archive entry's name to its path inside the tree and
// strips the archive's single top-level directory, which GitHub names for
// the repository and the ref. A name that is absolute, that holds a
// backslash (a separator on Windows) or a ".." component, or that names a
// second top-level directory, is refused before anything is written. rel is
// "" for the top-level directory itself.
func entryPath(name string, top *string) (string, error) {
	if name == "" || path.IsAbs(name) || strings.ContainsRune(name, '\\') {
		return "", fmt.Errorf("refusing %q: it is not a path inside the archive", name)
	}
	parts := strings.Split(path.Clean(name), "/")
	for _, p := range parts {
		if p == ".." {
			return "", fmt.Errorf("refusing %q: it contains ..", name)
		}
		// git run in the tree reads the ".git" at its root, and a .git the
		// archive wrote is the one place a repository's own configuration can
		// run a command (a smudge filter on checkout), so the cache's own is
		// the only one a tree may hold; git refuses to track such a name
		// whatever its case, which is why this is not an exact comparison.
		if strings.EqualFold(p, ".git") {
			return "", fmt.Errorf("refusing %q: it names %s, and the cache's own repository lives under that name", name, p)
		}
	}
	if *top == "" {
		*top = parts[0]
	} else if parts[0] != *top {
		return "", fmt.Errorf("refusing %q: the archive holds more than one top-level directory", name)
	}
	rel := strings.Join(parts[1:], "/")
	if rel == "" || rel == "." {
		return "", nil
	}
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("refusing %q: it is not a local path", name)
	}
	return filepath.FromSlash(rel), nil
}

// writeEntry writes one regular file, exactly as many bytes as its header
// claims: a short entry is a corrupt archive, not a short file.
func writeEntry(rt *os.Root, rel string, h *tar.Header, body io.Reader) error {
	if err := mkdirFor(rt, rel); err != nil {
		return fmt.Errorf("writing %q: %w", h.Name, err)
	}
	f, err := rt.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, entryMode(h.Mode))
	if err != nil {
		return fmt.Errorf("writing %q: %w", h.Name, err)
	}
	n, err := io.CopyN(f, body, h.Size)
	if err == nil && n != h.Size {
		err = io.ErrUnexpectedEOF
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("writing %q: %w", h.Name, err)
	}
	return nil
}

// mkdirFor creates the directory an entry sits in, inside the root.
func mkdirFor(rt *os.Root, rel string) error {
	dir := filepath.Dir(rel)
	if dir == "." {
		return nil
	}
	return rt.MkdirAll(dir, treeWritable)
}

// inside refuses an entry that would be written through a link an earlier
// entry planted. escapes compares the entry's path in the tree with its link
// target, and both are resolved by the filesystem, not by that comparison: a
// directory of the path that is itself a link resolves wherever it points, so
// "a" -> "." followed by "a/link" -> "../outside" puts a link at the tree's
// root that leaves it, with every check above passing (internal/cache refuses
// the same archive for the same reason). A directory this unpack created is
// remembered, so the walk runs once per directory rather than once per entry.
func inside(rt *os.Root, real map[string]bool, name, rel string) error {
	dir := filepath.Dir(rel)
	if dir == "." || real[dir] {
		return nil
	}
	at := ""
	for _, c := range strings.Split(dir, string(filepath.Separator)) {
		at = filepath.Join(at, c)
		if real[at] {
			continue
		}
		fi, err := rt.Lstat(at)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// The rest of the path does not exist yet: MkdirAll builds it out
			// of directories of this unpack's own.
			real[dir] = true
			return nil
		case err != nil:
			return fmt.Errorf("checking %q: %w", name, err)
		case fi.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("refusing %q: %s on its path is a link, and a link resolves where it points", name, filepath.ToSlash(at))
		case fi.IsDir():
			real[at] = true
		default:
			return fmt.Errorf("refusing %q: %s on its path is not a directory", name, filepath.ToSlash(at))
		}
	}
	real[dir] = true
	return nil
}

// entryMode masks an entry's mode to 0755 when any execute bit is set and
// 0644 otherwise, so no setuid, setgid or sticky bit survives the unpack.
func entryMode(m int64) fs.FileMode {
	if fs.FileMode(m)&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// entryType names a tar entry type for a refusal message.
func entryType(t byte) string {
	switch t {
	case tar.TypeLink:
		return "hard link"
	case tar.TypeChar:
		return "character device"
	case tar.TypeBlock:
		return "block device"
	case tar.TypeFifo:
		return "fifo"
	default:
		return fmt.Sprintf("%q", t)
	}
}

// escapes reports whether a link at rel, a tree-relative path, pointing at
// target leaves the tree: an absolute or empty target, or one whose path
// from rel's own directory climbs above the tree. It is lexical, and the
// os.Root the entry is written through refuses the write anyway; checking
// first is what makes the refusal an error a person can read (the same first
// filter internal/cache uses for its own archives).
func escapes(rel, target string) bool {
	t := filepath.FromSlash(target)
	return t == "" || filepath.IsAbs(t) || !filepath.IsLocal(filepath.Join(filepath.Dir(rel), t))
}

// checkLinks refuses an archive whose links resolve out of the tree, once the
// whole archive is in place: escapes compares paths, and the kernel resolves
// them, so "a/../outside" is the tree's "outside" until "a" is a link an
// earlier entry planted -- and is the tree's parent afterwards, one level per
// link in a chain. Every link is in the tree by the time this runs, so the
// order the archive lists them in decides nothing (internal/cache resolves the
// links in its own archives the same way, and it is not shared because that
// package carries the cloud storage client into this one, which runs locally).
func checkLinks(rt *os.Root, links []string) error {
	for _, rel := range links {
		out, err := resolvesOutside(rt, rel)
		if err != nil {
			return fmt.Errorf("resolving %q: %w", filepath.ToSlash(rel), err)
		}
		if out {
			return fmt.Errorf("refusing the archive: %s resolves outside the source tree", filepath.ToSlash(rel))
		}
	}
	return nil
}

// resolvesOutside reports whether name, resolved inside rt the way the kernel
// would (following every link, the last one included), leaves rt. A component
// that does not exist is taken lexically, so a dangling "missing/x/../.." is
// judged as if missing and x were directories, and ".." above the root or an
// absolute link target counts as outside. An error -- too many links, an
// unreadable component -- is an error to fail closed on.
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
			cur = append(cur, c)
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

// ChangesSince writes the "changes since your version" file that Fugaro —
// never the agent — produces (D1602): the release tags newer than the
// installed version, newest first, and the commits each one brought. The
// agent reads it as one more evidence file, so it is written where the
// caller says, whatever it says: a tree fetched as a tarball carries no
// history, and the file says so rather than being absent, because the
// question was asked.
func ChangesSince(ctx context.Context, o Options, dir, dest string) error {
	o = o.withDefaults()
	if !isRepository(dir) {
		// No history of this source is here: a tree from the tarball
		// transport, or one whose ".git" names a repository somewhere else —
		// a log read there would report another repository's releases as this
		// source's, and a fetch would write into it.
		return os.WriteFile(dest, []byte(tarballNote(o.Version)), recordFileMod)
	}
	tag, err := pluginwire.Tag(o.Version)
	tagged := err == nil
	// The history is fetched and read in a repository this call initialises
	// itself, the way gitTree does, and the tree is not written to at all. dir
	// is the tree Ensure returned, so the cache directory is its parent.
	stage, err := stageRepo(ctx, o, filepath.Dir(dir))
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	// The history since the installed release is fetched by excluding that
	// release by name on the remote, and that exclusion fails the whole fetch
	// when the name is not there -- a version cut in main between two
	// releases, or one built from a private repository -- and when nothing is
	// left after it, which is a repository right after a release: the tag the
	// person installed is the newest one, and main is at it. Both would leave
	// the diagnosis with no changes file at all, so what the remote carries
	// decides the fetch and, for a version that is not one of its releases,
	// whether it is anchored to one (D1611).
	remote := remoteTags(ctx, o, stage)
	tagged = tagged && slices.Contains(remote, tag)
	args := []string{"fetch", "--quiet", "--force", "--tags"}
	if tagged && remote[0] != tag {
		// History from the installed release up, and not below it: what
		// changed since is exactly the range the log below walks.
		args = append(args, "--shallow-exclude="+tag)
	} else {
		args = append(args, "--depth", "1")
	}
	if _, err := o.Run(ctx, stage, gitArgs(append(args, "origin")...)...); err != nil {
		return fmt.Errorf("fetching the release tags: %w", err)
	}
	out, err := o.Run(ctx, stage, gitArgs("tag", "--list", "v*", "--sort=-v:refname")...)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Changes in fugaro after %s, newest first:\n", o.Version)
	newer := newerTags(string(out), tag)
	if !tagged {
		fmt.Fprintf(&b, "\nThis fugaro is a %q build, so the source could not be anchored to a release: the newest release is %s.\n", o.Version, firstOr(newer, "none"))
	}
	if len(newer) == 0 {
		fmt.Fprintf(&b, "\nNo release is newer than %s.\n", o.Version)
	}
	for _, t := range newer {
		span := t
		if tagged {
			span = tag + ".." + t
		}
		log, err := o.Run(ctx, stage, gitArgs("log", "--format=%h %s", span)...)
		if err != nil {
			return fmt.Errorf("reading the log of %s: %w", t, err)
		}
		fmt.Fprintf(&b, "\n%s:\n", t)
		for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
			if line != "" {
				fmt.Fprintf(&b, "  %s\n", line)
			}
		}
	}
	return os.WriteFile(dest, []byte(b.String()), recordFileMod)
}

// tarballNote is what the changes file says when the tree has no history to
// read.
func tarballNote(version string) string {
	return "The source of " + version + " was fetched as a tarball, which carries no history: what changed since it cannot be listed here.\n"
}

// remoteTags is the release tags the remote the tree fetches from carries,
// newest first, the way `git tag --list --sort=-v:refname` prints them. A
// remote that cannot be read gives nothing, and the fetch that follows reports
// why: this only decides what that fetch asks for.
func remoteTags(ctx context.Context, o Options, dir string) []string {
	out, err := o.Run(ctx, dir, gitArgs("ls-remote", "--tags", "--refs", "--sort=-v:refname", "origin", "refs/tags/v*")...)
	if err != nil {
		return nil
	}
	var tags []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if _, ref, ok := strings.Cut(line, "\t"); ok {
			tags = append(tags, strings.TrimPrefix(ref, "refs/tags/"))
		}
	}
	return tags
}

// newerTags is the release tags in a `git tag --list --sort=-v:refname`
// listing that are newer than tag, newest first. tag is "" for a build
// without a release version, whose listing is every tag.
func newerTags(listing, tag string) []string {
	var out []string
	for _, t := range strings.Split(strings.TrimSpace(listing), "\n") {
		if t == "" || !pluginwire.ValidTag(t) {
			continue
		}
		if tag != "" && t == tag {
			break
		}
		out = append(out, t)
	}
	return out
}

func firstOr(ss []string, fallback string) string {
	if len(ss) == 0 {
		return fallback
	}
	return ss[0]
}

// readRecord is the record the cache holds, when it holds one that names a
// transport and a version. Anything else is a miss, and a miss refetches.
func readRecord(root string) (Record, bool) {
	at := filepath.Join(root, recordName)
	if fi, err := os.Lstat(at); err != nil || !fi.Mode().IsRegular() {
		return Record{}, false
	}
	data, err := os.ReadFile(at)
	if err != nil {
		return Record{}, false
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, false
	}
	if rec.Version == "" || (rec.Kind != KindGit && rec.Kind != KindTarball) {
		return Record{}, false
	}
	return rec, true
}

// writeRecord replaces the record, never writing through a name already
// there: the cache path is attacker-influenced and its names are
// predictable, so a link planted at record.json decides nothing.
func writeRecord(root string, rec Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	at := filepath.Join(root, recordName)
	if err := os.Remove(at); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(at, os.O_WRONLY|os.O_CREATE|os.O_EXCL, recordFileMod)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(data, '\n'))
	return errors.Join(werr, f.Close())
}

// cacheRoot is `<cache>/fugaro/source`, created 0700, with both components
// checked the way the output plan checks the log directory (D1603): the path
// is attacker-influenced — an agent's session often points XDG_CACHE_HOME at
// a temporary directory — and the names are predictable, so a component that
// is a symlink or not this user's directory means no cache at all, and one
// that passes is tightened to 0700.
func cacheRoot(o Options) (string, error) {
	base, err := localcfg.UserDir(o.Getenv, o.GOOS, localcfg.DirCache)
	if err != nil {
		return "", fmt.Errorf("%w, so the source of this version cannot be cached", err)
	}
	if err := os.MkdirAll(base, cacheDirMode); err != nil {
		return "", err
	}
	dir := base
	for _, part := range []string{"fugaro", "source"} {
		dir = filepath.Join(dir, part)
		if err := checkCacheDir(dir); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// checkCacheDir creates one component of the cache path, or refuses what is
// already at it.
func checkCacheDir(dir string) error {
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.Mkdir(dir, cacheDirMode); err != nil {
			return err
		}
		// Mkdir applies the umask, so the mode is set once more rather than
		// assumed.
		return os.Chmod(dir, cacheDirMode)
	case err != nil:
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink, not a directory this user owns", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if !ownedByUser(fi) {
		return fmt.Errorf("%s is not this user's directory", dir)
	}
	if fi.Mode().Perm() != cacheDirMode {
		_ = os.Chmod(dir, cacheDirMode)
	}
	return nil
}

// chmodTree makes every directory and file under dir writable, or takes the
// write bit off every one of them again. The tree is left read-only because
// nothing — the agent included — has any business writing into the source it
// is reading (F5, D1602). No git call here writes into a served tree, a fetch
// running in a repository of the call's own (stageRepo), so the only widening
// is removeTree's, on the way to deleting one.
//
// A directory is 0755 or 0555, and a file keeps everything it had but the
// write bits, so a script's execute bit survives the round trip and the tree
// still matches what git checked out.
func chmodTree(dir string, writable bool) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case fi.IsDir():
			if writable {
				return os.Chmod(p, treeWritable)
			}
			return os.Chmod(p, treeDirMode)
		case fi.Mode().IsRegular():
			return os.Chmod(p, treeFileMode(fi.Mode().Perm(), writable))
		default:
			return nil
		}
	})
}

// treeFileMode is a file's mode with the write bit set or cleared.
func treeFileMode(perm fs.FileMode, writable bool) fs.FileMode {
	if writable {
		return perm | 0o600
	}
	return perm &^ 0o222
}

// removeTree deletes the tree, widening it first: removing an entry needs
// the write bit on the directory that holds it, and the tree is left
// read-only.
func removeTree(dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := chmodTree(dir, true); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// gitHardening is put before every git call here. The cache tree can be
// written by anything running as this user, and a hook or an fsmonitor
// command its own config names would then run with this command's
// privileges; an external protocol would run another program for a
// transport. It is internal/cli's list for the same reason, on a checkout
// that may be somebody else's (init_repo_target.go).
func gitArgs(args ...string) []string {
	return append([]string{
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "protocol.ext.allow=never",
	}, args...)
}

// gitEnv is the environment of every git call here: the process's, without the
// variables git takes a repository, or a configuration value, from, and with
// the two this package sets itself so that a caller's value cannot stand
// beside its own.
//
// It is the same rule as gitArgs, one step further out. gitArgs refuses the
// commands a repository the call meets may name; the environment is where the
// repository itself can be named. git reads GIT_DIR, GIT_WORK_TREE,
// GIT_INDEX_FILE and GIT_COMMON_DIR ahead of the directory a call runs in, and
// GIT_OBJECT_DIRECTORY, GIT_ALTERNATE_OBJECT_DIRECTORIES and GIT_NAMESPACE
// change what it finds there, so one inherited from the shell — a git hook or
// alias exports GIT_CONFIG_PARAMETERS to everything it starts, and a tool can
// leave a GIT_DIR behind — sends the "git init" and the fetch after it into a
// repository of the person's own, detaches its HEAD and overwrites its index.
// The GIT_CONFIG_COUNT family and GIT_CONFIG_PARAMETERS inject values the way
// "-c" does, so a wrapper's leftover url.*.insteadOf would have the fetch read
// the source from somewhere the call never named.
//
// GIT_CONFIG_GLOBAL, GIT_CONFIG_SYSTEM, GIT_CONFIG_NOSYSTEM and the deprecated
// GIT_CONFIG stay: they name the configuration files the person chose for this
// machine, which a fetch may need (a proxy, a mirror, a CA bundle), and a test
// that points them at nothing would otherwise fall back to the real home
// directory. The line is between a name the caller set deliberately and a
// value injected into this call; both are outside what this package can
// enforce anyway, since whoever sets this process's environment can name the
// git on its PATH.
func gitEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !gitEnvDropped(k) {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
}

// gitEnvDropped reports whether k is a variable this package refuses to
// inherit: the ones that name the repository, the index or the object store
// the call works on, the ones that inject a configuration value, and the two
// gitEnv sets itself.
func gitEnvDropped(k string) bool {
	switch k {
	case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
		"GIT_NAMESPACE", "GIT_CONFIG_PARAMETERS",
		"GIT_TERMINAL_PROMPT", "GIT_OPTIONAL_LOCKS":
		return true
	}
	return k == "GIT_CONFIG_COUNT" ||
		strings.HasPrefix(k, "GIT_CONFIG_KEY_") ||
		strings.HasPrefix(k, "GIT_CONFIG_VALUE_")
}

// execGit is the default Runner: git from PATH, with no terminal prompt and
// no optional lock, in dir.
func execGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = dir
	c.Env = gitEnv()
	c.WaitDelay = gitWaitDelay
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// httpClient bounds one download, body included.
var httpClient = &http.Client{Timeout: fetchTimeout}

// httpFetch is the default Fetcher: an HTTPS GET. The trust in what comes
// back is TLS to the host in the URL and the ref the URL names (D1610).
func httpFetch(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

// withDefaults fills every field the caller left out.
func (o Options) withDefaults() Options {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.SourceURL == "" {
		o.SourceURL = SourceURL
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.Run == nil {
		o.Run = execGit
	}
	if o.Fetch == nil {
		o.Fetch = httpFetch
	}
	if o.MaxArchive <= 0 {
		o.MaxArchive = maxArchive
	}
	return o
}
