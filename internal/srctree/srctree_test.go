package srctree

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// remote is a local bare repository standing in for github.com/dimipaun/fugaro,
// with one commit per release tag. url is the bare path without its ".git", so
// the package's own "<url>.git" and "<url>/archive/<ref>.tar.gz" both name it.
type remote struct {
	dir    string
	url    string
	commit map[string]string
}

func newRemote(t *testing.T, tags ...string) remote {
	t.Helper()
	testutil.IsolateGit(t)
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	testutil.Git(t, root, "init", "--quiet", "--bare", "-b", "main", bare)
	testutil.Git(t, bare, "config", "receive.autogc", "false")
	testutil.Git(t, bare, "config", "maintenance.auto", "false")
	// Fetching a commit by its sha is what D1610 asks for, and GitHub allows
	// it; a bare repository does not until it is told to.
	testutil.Git(t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	seed := filepath.Join(root, "seed")
	testutil.Git(t, root, "clone", "--quiet", bare, seed)
	r := remote{dir: bare, url: strings.TrimSuffix(bare, ".git"), commit: map[string]string{}}
	for _, tag := range tags {
		testutil.WriteFiles(t, seed, map[string]string{"VERSION": tag + "\n"})
		testutil.Git(t, seed, "add", "-A")
		testutil.Git(t, seed, "commit", "--quiet", "-m", "release "+tag)
		testutil.Git(t, seed, "tag", tag)
		r.commit[tag] = testutil.Git(t, seed, "rev-parse", "HEAD")
	}
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	testutil.Git(t, seed, "push", "--quiet", "--tags", "origin")
	return r
}

// options is a hermetic call: the cache is a temporary directory, the source
// is a local repository, and nothing else is the developer's.
func options(t *testing.T, r remote, version, commit string) Options {
	t.Helper()
	cache := t.TempDir()
	// The tree is left read-only, and removing an entry needs the write bit
	// on the directory that holds it; t.TempDir's own cleanup would fail
	// without this, which is the price of the property under test.
	t.Cleanup(func() {
		if _, err := os.Lstat(filepath.Join(cache, "fugaro")); err == nil {
			_ = chmodTree(filepath.Join(cache, "fugaro"), true)
		}
	})
	return Options{
		Version: version,
		Commit:  commit,
		Getenv: func(k string) string {
			if k == "XDG_CACHE_HOME" {
				return cache
			}
			return ""
		},
		GOOS:      "linux",
		SourceURL: r.url,
	}
}

// cacheRootOf is the cache directory an Options writes to.
func cacheRootOf(t *testing.T, o Options) string {
	t.Helper()
	return filepath.Join(o.Getenv("XDG_CACHE_HOME"), "fugaro", "source")
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// archive is a GitHub-shaped source archive: one top-level directory, then
// the files.
func archive(t *testing.T, top string, files map[string]string) []byte {
	t.Helper()
	hdrs := make([]*tar.Header, 0, len(files))
	bodies := map[string]string{}
	for name, body := range files {
		hdrs = append(hdrs, &tar.Header{Name: top + "/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		bodies[top+"/"+name] = body
	}
	return archiveOf(t, top, bodies, hdrs...)
}

// archiveOf builds a gzipped tar whose entries are exactly hdrs. A regular
// file's header must carry the size of its body in bodies; a header without
// one is written as a single byte.
func archiveOf(t *testing.T, top string, bodies map[string]string, hdrs ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if top != "" {
		if err := tw.WriteHeader(&tar.Header{Name: top + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range hdrs {
		body := bodies[h.Name]
		if h.Typeflag == tar.TypeReg {
			if h.Size == 0 {
				h.Size = 1
			}
			if body == "" {
				body = strings.Repeat("x", int(h.Size))
			}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// recordingGit is the real git with every call's argv kept, so a test can see
// both what ran and that nothing did.
func recordingGit(t *testing.T) (Runner, func() [][]string) {
	t.Helper()
	var mu sync.Mutex
	var calls [][]string
	run := func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		mu.Lock()
		calls = append(calls, append([]string{dir}, args...))
		mu.Unlock()
		c := exec.CommandContext(ctx, "git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
		var stderr bytes.Buffer
		c.Stderr = &stderr
		out, err := c.Output()
		if err != nil {
			return nil, fmt.Errorf("git %v: %w: %s", args, err, stderr.String())
		}
		return out, nil
	}
	kept := func() [][]string {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
	return run, kept
}

func TestEnsureReplacesATreeOfAnotherVersion(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	dir, rec, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != "v1.0.0" || rec.Commit != r.commit["v1.0.0"] || rec.Kind != KindGit || rec.MayNotMatch {
		t.Fatalf("record = %+v", rec)
	}
	if got := read(t, filepath.Join(dir, "VERSION")); got != "v1.0.0\n" {
		t.Fatalf("the tree holds %q", got)
	}

	o.Version, o.Commit = "v1.1.0", r.commit["v1.1.0"]
	dir, rec, err = Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != "v1.1.0" || rec.Commit != r.commit["v1.1.0"] {
		t.Fatalf("record = %+v", rec)
	}
	if got := read(t, filepath.Join(dir, "VERSION")); got != "v1.1.0\n" {
		t.Fatalf("the tree still holds %q: a record of another version was served", got)
	}
}

func TestAWarmCacheFetchesNothing(t *testing.T) {
	r := newRemote(t, "v1.0.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	run, calls := recordingGit(t)
	o.Run = run
	if _, _, err := Ensure(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if len(calls()) == 0 {
		t.Fatal("the first call ran no git")
	}
	o.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if !slices.Contains(args, "rev-parse") {
			t.Errorf("a warm cache ran git %v", args)
		}
		return []byte(r.commit["v1.0.0"] + "\n"), nil
	}
	dir, rec, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != "v1.0.0" || read(t, filepath.Join(dir, "VERSION")) != "v1.0.0\n" {
		t.Fatalf("record = %+v", rec)
	}
}

// TestAWarmCacheIsCheckedAgainstItsTree is the window between the tree and
// the record beside it: a run that dies after installing a tree and before
// writing the record leaves the record naming the version the tree is not,
// and a cache that answered from the record alone would serve that tree for
// the version asked for. The commit the tree's HEAD is at decides (D1610).
func TestAWarmCacheIsCheckedAgainstItsTree(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	if _, _, err := Ensure(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	root := cacheRootOf(t, o)
	note := fmt.Sprintf("{\"version\":\"v1.1.0\",\"commit\":%q,\"kind\":\"git\"}\n", r.commit["v1.1.0"])
	if err := os.WriteFile(filepath.Join(root, recordName), []byte(note), recordFileMod); err != nil {
		t.Fatal(err)
	}
	o.Version, o.Commit = "v1.1.0", r.commit["v1.1.0"]
	dir, _, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "VERSION")); got != "v1.1.0\n" {
		t.Fatalf("the tree holds %q: a record the tree does not answer to was served", got)
	}
}

// TestEnsureReplacesATreeOfAnotherVersionWithoutACommit is the plan's red
// step with the commit unable to cover for it: a build that knows only its
// version asks for a tag, so the recorded commit cannot decide the match and
// the recorded version alone has to.
func TestEnsureReplacesATreeOfAnotherVersionWithoutACommit(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.0.0", "")
	if _, rec, err := Ensure(context.Background(), o); err != nil {
		t.Fatal(err)
	} else if rec.Version != "v1.0.0" {
		t.Fatalf("record = %+v", rec)
	}
	o.Version = "v1.1.0"
	dir, rec, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != "v1.1.0" || read(t, filepath.Join(dir, "VERSION")) != "v1.1.0\n" {
		t.Fatalf("a record of another version was served: %+v, %q", rec, read(t, filepath.Join(dir, "VERSION")))
	}
}

// TestAFailedFetchLeavesTheTreeReadOnly: the fetch widens the tree so git can
// write into it, and every way out has to narrow it again — a run that could
// not fetch must not leave the agent a tree it can write to (D1602).
func TestAFailedFetchLeavesTheTreeReadOnly(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	dir, _, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	o.Version, o.Commit = "v1.1.0", r.commit["v1.1.0"]
	o.SourceURL = filepath.Join(t.TempDir(), "gone")
	if _, _, err := Ensure(context.Background(), o); err == nil {
		t.Fatal("a fetch from a source that is not there succeeded")
	}
	fi, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o200 != 0 {
		t.Fatalf("the tree was left writable: %v", fi.Mode())
	}
}

func TestEnsureReplacesATarballOfTheWrongVersion(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.1.0", r.commit["v1.1.0"])
	o.LookPath = func(string) (string, error) { return "", errors.New("no git here") }
	var urls []string
	o.Fetch = func(_ context.Context, url string) (io.ReadCloser, error) {
		urls = append(urls, url)
		body := archive(t, "fugaro-"+r.commit["v1.1.0"], map[string]string{"VERSION": "v1.1.0\n"})
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	// A cache of the older version, fetched the same way.
	root := cacheRootOf(t, o)
	if err := os.MkdirAll(root, cacheDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, treeName), cacheDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, treeName, "VERSION"), []byte("v1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeRecord(root, Record{Version: "v1.0.0", Kind: KindTarball}); err != nil {
		t.Fatal(err)
	}

	dir, rec, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != "v1.1.0" || rec.Commit != r.commit["v1.1.0"] || rec.Kind != KindTarball {
		t.Fatalf("record = %+v", rec)
	}
	if got := read(t, filepath.Join(dir, "VERSION")); got != "v1.1.0\n" {
		t.Fatalf("the tree still holds %q: a tarball of another version was served", got)
	}
	if len(urls) != 1 || !strings.HasSuffix(urls[0], "/archive/"+r.commit["v1.1.0"]+".tar.gz") {
		t.Fatalf("fetched %v", urls)
	}
}

func TestNoGitUsesTheTarball(t *testing.T) {
	r := newRemote(t, "v1.0.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	o.LookPath = func(string) (string, error) { return "", errors.New("no git here") }
	o.Fetch = func(context.Context, string) (io.ReadCloser, error) {
		body := archive(t, "fugaro-"+r.commit["v1.0.0"], map[string]string{"VERSION": "v1.0.0\n"})
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	dir, rec, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Kind != KindTarball {
		t.Fatalf("kind = %q", rec.Kind)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a tarball tree has a .git: %v", err)
	}
}

func TestDevBuildUsesTheNewestReleaseTag(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "dev", "unknown")
	dir, rec, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != "v1.1.0" || !rec.MayNotMatch || rec.Commit != r.commit["v1.1.0"] {
		t.Fatalf("record = %+v", rec)
	}
	if got := read(t, filepath.Join(dir, "VERSION")); got != "v1.1.0\n" {
		t.Fatalf("the tree holds %q", got)
	}
}

func TestDevBuildWithoutGitIsRefused(t *testing.T) {
	o := options(t, remote{url: "/nonexistent"}, "dev", "unknown")
	o.LookPath = func(string) (string, error) { return "", errors.New("no git here") }
	o.Fetch = func(context.Context, string) (io.ReadCloser, error) {
		t.Error("a build with no version fetched an archive")
		return nil, nil
	}
	_, _, err := Ensure(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "git is not installed") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureChecksHeadAgainstTheCommit(t *testing.T) {
	const want = "0123456789abcdef0123456789abcdef01234567"
	const other = "fedcba9876543210fedcba9876543210fedcba98"
	r := newRemote(t, "v1.0.0")
	o := options(t, r, "v1.0.0", want)
	o.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		for _, a := range args {
			if a == "rev-parse" {
				return []byte(other + "\n"), nil
			}
		}
		return nil, nil
	}
	_, _, err := Ensure(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(cacheRootOf(t, o), recordName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a refused tree was recorded: %v", err)
	}
}

func TestEnsureFailsClosedOnACommitTheRemoteLacks(t *testing.T) {
	r := newRemote(t, "v1.0.0")
	o := options(t, r, "v1.0.0", "0123456789abcdef0123456789abcdef01234567")
	if _, _, err := Ensure(context.Background(), o); err == nil {
		t.Fatal("a commit the remote does not have was fetched")
	}
}

func TestEveryGitCallIsHardened(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	run, calls := recordingGit(t)
	o.Run = run
	dir, _, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	o.Version, o.Commit = "v1.1.0", r.commit["v1.1.0"]
	if _, _, err := Ensure(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	// A development build asks the remote which release is newest (D1611), and
	// the note about what changed since a version is read with git too.
	o.Version, o.Commit = "dev", ""
	if _, _, err := Ensure(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if err := ChangesSince(context.Background(), o, dir, filepath.Join(t.TempDir(), "changes.md")); err != nil {
		t.Fatal(err)
	}
	all := calls()
	if len(all) == 0 {
		t.Fatal("no git call was recorded")
	}
	for _, call := range all {
		argv := strings.Join(call, " ")
		if !strings.Contains(argv, "core.hooksPath="+os.DevNull) {
			t.Errorf("a git call would run hooks: %s", argv)
		}
		if !strings.Contains(argv, "core.fsmonitor=false") {
			t.Errorf("a git call would run an fsmonitor command: %s", argv)
		}
		if !strings.Contains(argv, "protocol.ext.allow=never") {
			t.Errorf("a git call would run an external transport: %s", argv)
		}
	}
}

func TestAPlantedHookNeverRuns(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	dir, _, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := "#!/bin/sh\ntouch " + marker + "\n"
	if err := chmodTree(dir, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "post-checkout"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "post-merge"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := chmodTree(dir, false); err != nil {
		t.Fatal(err)
	}
	o.Version, o.Commit = "v1.1.0", r.commit["v1.1.0"]
	if _, _, err := Ensure(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a hook the cache held ran: %v", err)
	}
}

func TestTheTreeIsReadOnly(t *testing.T) {
	r := newRemote(t, "v1.0.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	dir, _, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
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
		if fi.Mode().Perm()&0o222 != 0 {
			t.Errorf("%s is writable (%v): the agent could write into the source it reads", p, fi.Mode())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCacheDirIsChecked(t *testing.T) {
	r := newRemote(t, "v1.0.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	o.Fetch = func(context.Context, string) (io.ReadCloser, error) {
		body := archive(t, "fugaro-"+r.commit["v1.0.0"], map[string]string{"VERSION": "v1.0.0\n"})
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	o.LookPath = func(string) (string, error) { return "", errors.New("no git here") }

	// A symlink at the cache name decides nothing: the call is refused.
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(o.Getenv("XDG_CACHE_HOME"), "fugaro")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Ensure(context.Background(), o); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v", err)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Fatalf("the cache was written through a link: %v, %v", entries, err)
	}

	// A wider cache directory is tightened to 0700.
	if err := os.Remove(filepath.Join(o.Getenv("XDG_CACHE_HOME"), "fugaro")); err != nil {
		t.Fatal(err)
	}
	root := cacheRootOf(t, o)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Ensure(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != cacheDirMode {
		t.Fatalf("cache mode = %v", fi.Mode())
	}
}

// TestAPlantedRecordIsNotTrusted: the record is the cache's own note about the
// tree beside it, so it is only read as the regular file this package writes.
// A link at its name pointing at a note that happens to name the wanted
// version would otherwise serve the tree the link's author chose, which is
// the one thing the version check is there to stop.
func TestAPlantedRecordIsNotTrusted(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.1.0", r.commit["v1.1.0"])
	o.LookPath = func(string) (string, error) { return "", errors.New("no git here") }
	o.Fetch = func(context.Context, string) (io.ReadCloser, error) {
		body := archive(t, "fugaro-x", map[string]string{"VERSION": "v1.1.0\n"})
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	root := cacheRootOf(t, o)
	if err := os.MkdirAll(filepath.Join(root, treeName), cacheDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, treeName, "VERSION"), []byte("v1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(t.TempDir(), "record.json")
	note := fmt.Sprintf("{\"version\":\"v1.1.0\",\"commit\":%q,\"kind\":\"tarball\"}\n", r.commit["v1.1.0"])
	if err := os.WriteFile(planted, []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(planted, filepath.Join(root, recordName)); err != nil {
		t.Fatal(err)
	}
	dir, _, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "VERSION")); got != "v1.1.0\n" {
		t.Fatalf("a record planted at the cache was trusted: the tree holds %q", got)
	}
}

// TestATreeSymlinkIsNotFollowed is both transports. git fetches and checks out
// into the tree it finds there, so a link at the tree's name would put the
// release source into the directory the link names — another checkout of this
// user's, say — and the tarball path must not write through it either. The
// directory the link names already holds a .git, which is what a link to a
// working copy looks like and what stops the "no repository here" path from
// removing the link on its own.
func TestATreeSymlinkIsNotFollowed(t *testing.T) {
	r := newRemote(t, "v1.0.0")
	for _, git := range []bool{true, false} {
		name := "tarball"
		if git {
			name = "git"
		}
		t.Run(name, func(t *testing.T) {
			o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
			if !git {
				o.LookPath = func(string) (string, error) { return "", errors.New("no git here") }
				o.Fetch = func(context.Context, string) (io.ReadCloser, error) {
					body := archive(t, "fugaro-x", map[string]string{"VERSION": "v1.0.0\n"})
					return io.NopCloser(bytes.NewReader(body)), nil
				}
			}
			root := cacheRootOf(t, o)
			if err := os.MkdirAll(root, cacheDirMode); err != nil {
				t.Fatal(err)
			}
			elsewhere := t.TempDir()
			if err := os.MkdirAll(filepath.Join(elsewhere, ".git"), cacheDirMode); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(elsewhere, "mine"), []byte("mine\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := names(t, elsewhere)
			if err := os.Symlink(elsewhere, filepath.Join(root, treeName)); err != nil {
				t.Fatal(err)
			}
			dir, _, err := Ensure(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			if after := names(t, elsewhere); !slices.Equal(before, after) {
				t.Fatalf("the tree was written through a link: %v became %v", before, after)
			}
			if got := read(t, filepath.Join(dir, "VERSION")); got != "v1.0.0\n" {
				t.Fatalf("the tree holds %q", got)
			}
		})
	}
}

// names is what a directory holds, for comparing a directory before and after
// a call that must not have touched it.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestUnpackRefusesAnUnsafeArchive(t *testing.T) {
	const top = "fugaro-abc"
	cases := []struct {
		name string
		max  int64
		hdrs []*tar.Header
	}{
		{"a .. entry", 1 << 20, []*tar.Header{{Name: top + "/../evil", Typeflag: tar.TypeReg}}},
		{"an absolute entry", 1 << 20, []*tar.Header{{Name: "/etc/evil", Typeflag: tar.TypeReg}}},
		{"a backslash entry", 1 << 20, []*tar.Header{{Name: top + `/..\evil`, Typeflag: tar.TypeReg}}},
		{"an escaping link", 1 << 20, []*tar.Header{{Name: top + "/link", Typeflag: tar.TypeSymlink, Linkname: "../../evil"}}},
		{"an absolute link", 1 << 20, []*tar.Header{{Name: top + "/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}}},
		{"a character device", 1 << 20, []*tar.Header{{Name: top + "/dev", Typeflag: tar.TypeChar}}},
		{"a block device", 1 << 20, []*tar.Header{{Name: top + "/dev", Typeflag: tar.TypeBlock}}},
		{"a fifo", 1 << 20, []*tar.Header{{Name: top + "/fifo", Typeflag: tar.TypeFifo}}},
		{"a hard link", 1 << 20, []*tar.Header{{Name: top + "/hard", Typeflag: tar.TypeLink, Linkname: top + "/VERSION"}}},
		{"a second top-level directory", 1 << 20, []*tar.Header{{Name: "other/file", Typeflag: tar.TypeReg}}},
		{"an oversize archive", 4, []*tar.Header{{Name: top + "/big", Typeflag: tar.TypeReg, Size: 8}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dest := t.TempDir()
			body := archiveOf(t, top, nil, tc.hdrs...)
			if err := unpack(bytes.NewReader(body), dest, tc.max); err == nil {
				t.Fatal("the archive was unpacked")
			}
			if entries, err := os.ReadDir(dest); err != nil || len(entries) != 0 {
				t.Fatalf("the refused archive wrote %v (%v)", entries, err)
			}
		})
	}
}

func TestUnpackKeepsTheSource(t *testing.T) {
	dest := t.TempDir()
	body := archiveOf(t, "fugaro-abc", map[string]string{"fugaro-abc/VERSION": "v1.0.0\n"}, []*tar.Header{
		{Name: "fugaro-abc/VERSION", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len("v1.0.0\n"))},
		{Name: "fugaro-abc/internal/x.go", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
		{Name: "fugaro-abc/run.sh", Typeflag: tar.TypeReg, Mode: 0o755, Size: 1},
		// The repository ships links like this one, so the transport that
		// exists for a machine without git must keep them.
		{Name: "fugaro-abc/.claude/skills/x", Typeflag: tar.TypeSymlink, Linkname: "../../.agents/skills/x"},
	}...)
	if err := unpack(bytes.NewReader(body), dest, 1<<20); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dest, "VERSION")); got != "v1.0.0\n" {
		t.Fatalf("VERSION = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "internal", "x.go")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dest, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("run.sh lost its execute bit: %v", fi.Mode())
	}
	if target, err := os.Readlink(filepath.Join(dest, ".claude", "skills", "x")); err != nil || target != "../../.agents/skills/x" {
		t.Fatalf("link = %q, %v", target, err)
	}
}

func TestEnsureKeepsTheTreeWhenAnArchiveIsRefused(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	o.LookPath = func(string) (string, error) { return "", errors.New("no git here") }
	o.Fetch = func(context.Context, string) (io.ReadCloser, error) {
		body := archive(t, "fugaro-x", map[string]string{"VERSION": "v1.0.0\n"})
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	dir, _, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	o.Version, o.Commit = "v1.1.0", r.commit["v1.1.0"]
	o.Fetch = func(context.Context, string) (io.ReadCloser, error) {
		body := archiveOf(t, "fugaro-x", nil, &tar.Header{Name: "fugaro-x/../evil", Typeflag: tar.TypeReg})
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	if _, _, err := Ensure(context.Background(), o); err == nil {
		t.Fatal("a hostile archive was unpacked")
	}
	if got := read(t, filepath.Join(dir, "VERSION")); got != "v1.0.0\n" {
		t.Fatalf("a refused archive replaced the tree: VERSION = %q", got)
	}
}

func TestChangesSinceWritesNewerReleases(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	dir, _, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "changes.txt")
	if err := ChangesSince(context.Background(), o, dir, dest); err != nil {
		t.Fatal(err)
	}
	got := read(t, dest)
	for _, want := range []string{"after v1.0.0", "v1.1.0:", "release v1.1.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("the changes file does not name %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "v1.0.0:") {
		t.Errorf("the changes file lists the installed version:\n%s", got)
	}
	// The tree is left as it was found.
	fi, err := os.Stat(filepath.Join(dir, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o222 != 0 {
		t.Fatalf(".git/config is writable: %v", fi.Mode())
	}
}

func TestChangesSinceSaysSoWithoutHistory(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(t.TempDir(), "changes.txt")
	if err := ChangesSince(context.Background(), Options{Version: "v1.0.0"}, dir, dest); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dest); !strings.Contains(got, "tarball") {
		t.Fatalf("the changes file = %q", got)
	}
}
