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
	"sort"
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

// archiveOf builds a gzipped tar whose entries are exactly hdrs, under the
// global pax header git archive opens a real source archive with: Go's tar
// reader hands that header to the caller, so an archive without one is not
// the shape this transport has to read. A regular file's header must carry
// the size of its body in bodies; a header without one is written as a
// single byte.
func archiveOf(t *testing.T, top string, bodies map[string]string, hdrs ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	global := &tar.Header{
		Name:       "pax_global_header",
		Typeflag:   tar.TypeXGlobalHeader,
		PAXRecords: map[string]string{"comment": strings.Repeat("0", 40)},
	}
	if err := tw.WriteHeader(global); err != nil {
		t.Fatal(err)
	}
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

// TestAFailedFetchLeavesTheTreeReadOnly: the fetch runs in a repository of its
// own and the tree is replaced only once it has succeeded, so a run that could
// not fetch leaves the tree it found — read-only, and still the tree of the
// version last served — rather than a half-written one (D1602).
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
	if got := read(t, filepath.Join(dir, "VERSION")); got != "v1.0.0\n" {
		t.Fatalf("a failed fetch left the tree holding %q, not the version last served", got)
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

// TestADevBuildAsksTheRemoteWhereTheCacheIs: a build with no version asks the
// remote for its newest release tag with git ls-remote, and git executes the
// configuration of the repository it runs in. Run in the working directory,
// which is whichever repository the person happened to run the command from,
// that configuration decides what runs — here an sshCommand on a URL an
// insteadOf rewrites to ssh. The question is asked in a repository of this
// call's own instead, the way every fetch here is.
func TestADevBuildAsksTheRemoteWhereTheCacheIs(t *testing.T) {
	r := newRemote(t, "v1.1.0")
	o := options(t, r, "dev", "unknown")
	marker := filepath.Join(t.TempDir(), "ran")
	cwd := t.TempDir()
	testutil.Git(t, cwd, "init", "--quiet")
	testutil.Git(t, cwd, "config", "core.sshCommand", "touch "+marker+" && false")
	testutil.Git(t, cwd, "config", "url.ssh://localhost/nonexistent.insteadOf", r.url)
	t.Chdir(cwd)

	_, rec, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != "v1.1.0" {
		t.Fatalf("record = %+v", rec)
	}
	if _, err := os.Lstat(marker); err == nil {
		t.Fatal("the working directory's repository ran a command")
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
	// A version that parses asks the remote for its tag before the fetch, so
	// that call is hardened too.
	o.Version = "v1.1.0"
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

// plantSmudgeFilter makes the git configuration at dir one that runs a command
// on the next checkout: a filter, and an attributes file applying it to every
// path. Nothing in dir need be a repository of its own; a .git/config and a
// .git/info/attributes are read by git wherever it runs, and this package
// leaves the cache directory writable by anything running as this user.
func plantSmudgeFilter(t *testing.T, dir, marker string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "info"), cacheDirMode); err != nil {
		t.Fatal(err)
	}
	cfg, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(cfg, "\n[filter \"x\"]\n\tsmudge = touch %s && cat\n", marker); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "attributes"), []byte("* filter=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAPlantedFilterNeverRuns: git runs commands named by the configuration of
// the repository it runs in, and a smudge filter is one of them (the comment
// on entryPath names this vector). Everything under the cache directory is
// writable by anything running as this user, so a filter planted in the tree's
// own config, and one planted in the directory a fetch stages in, must not run
// when the next version is fetched: the fetch happens in a repository this
// package initialises itself, and the tree is replaced rather than fetched
// into.
func TestAPlantedFilterNeverRuns(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	dir, _, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "filter-ran")
	if err := chmodTree(dir, true); err != nil {
		t.Fatal(err)
	}
	plantSmudgeFilter(t, dir, marker)
	if err := chmodTree(dir, false); err != nil {
		t.Fatal(err)
	}
	plantSmudgeFilter(t, filepath.Join(cacheRootOf(t, o), stageName), marker)

	o.Version, o.Commit = "v1.1.0", r.commit["v1.1.0"]
	if _, _, err := Ensure(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a filter the cache held ran: %v", err)
	}
	if got := read(t, filepath.Join(dir, "VERSION")); got != "v1.1.0\n" {
		t.Fatalf("the tree holds %q", got)
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

	// A file at the cache name is not a cache either.
	if err := os.Remove(filepath.Join(o.Getenv("XDG_CACHE_HOME"), "fugaro")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Getenv("XDG_CACHE_HOME"), "fugaro"), []byte("not a cache\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Ensure(context.Background(), o); err == nil || !strings.Contains(err.Error(), "fugaro is not a directory") {
		t.Fatalf("err = %v", err)
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

// gitPlants put at a tree's ".git" a name that points at another repository
// of this person's rather than at this cache's own: a link, a "gitdir:" file
// and a commondir file all do, and a check that only asks whether the name is
// there answers yes to every one of them.
var gitPlants = map[string]func(t *testing.T, tree, gitdir string){
	"a link": func(t *testing.T, tree, gitdir string) {
		if err := os.Symlink(gitdir, filepath.Join(tree, ".git")); err != nil {
			t.Fatal(err)
		}
	},
	"a gitdir file": func(t *testing.T, tree, gitdir string) {
		if err := os.WriteFile(filepath.Join(tree, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	},
	"a commondir file": func(t *testing.T, tree, gitdir string) {
		if err := os.MkdirAll(filepath.Join(tree, ".git"), cacheDirMode); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tree, ".git", "commondir"), []byte(gitdir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	},
}

// anotherRepository is a repository of this person's own, with a commit in it:
// what a ".git" planted at the tree's name points at, and what must be left
// exactly as it was.
func anotherRepository(t *testing.T) string {
	t.Helper()
	mine := t.TempDir()
	testutil.Git(t, mine, "init", "--quiet", "-b", "main")
	testutil.WriteFiles(t, mine, map[string]string{"notes": "mine\n"})
	testutil.Git(t, mine, "add", "-A")
	testutil.Git(t, mine, "commit", "--quiet", "-m", "mine")
	return mine
}

// cloneOf is a clone of the source: another repository of this person's own
// whose HEAD is the commit the newest release tag is at, so a check that asks
// it what its HEAD is cannot tell it from this cache's own tree.
func cloneOf(t *testing.T, r remote) string {
	t.Helper()
	mine := t.TempDir()
	testutil.Git(t, mine, "clone", "--quiet", r.dir, mine)
	return mine
}

// TestAWarmCacheDoesNotAskAnotherRepositoryForItsHead: the warm path asks the
// tree it found what commit it is at, and git run in the tree reads whatever
// its ".git" names — a repository of the person's own, whose HEAD is the same
// commit when it is a clone of the source. The record's version would then be
// checked against another repository's HEAD, and the tree of another version
// beside it served as the version asked for. Only a ".git" of this cache's own
// is read; anything else is replaced, which is what the fetch path does with
// it as well.
func TestAWarmCacheDoesNotAskAnotherRepositoryForItsHead(t *testing.T) {
	r := newRemote(t, "v1.0.0")
	for name, plant := range gitPlants {
		t.Run(name, func(t *testing.T) {
			o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
			mine := cloneOf(t, r)
			if got := testutil.Git(t, mine, "rev-parse", "HEAD"); got != r.commit["v1.0.0"] {
				t.Fatalf("the clone is at %s, not at the release commit", got)
			}
			before := treeNames(t, mine)
			root := cacheRootOf(t, o)
			tree := filepath.Join(root, treeName)
			if err := os.MkdirAll(tree, cacheDirMode); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(tree, "VERSION"), []byte("v0.9.0\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			plant(t, tree, filepath.Join(mine, ".git"))
			if err := writeRecord(root, Record{Version: "v1.0.0", Commit: r.commit["v1.0.0"], Kind: KindGit}); err != nil {
				t.Fatal(err)
			}
			dir, _, err := Ensure(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			if got := read(t, filepath.Join(dir, "VERSION")); got != "v1.0.0\n" {
				t.Fatalf("a tree the record did not name was served: it holds %q", got)
			}
			if after := treeNames(t, mine); !slices.Equal(before, after) {
				t.Fatalf("the repository the tree's .git named was written into: %v became %v", before, after)
			}
		})
	}
}

// TestATreeWhoseGitIsSomewhereElseIsRebuilt is the same attack one level
// deeper: git writes into whatever ".git" names, and a link, a "gitdir:" file
// and a commondir file name another repository of the user's just as well as
// the tree directory itself could be a link. The repository named is left at
// its own HEAD, and the cache gets a tree of its own instead.
func TestATreeWhoseGitIsSomewhereElseIsRebuilt(t *testing.T) {
	r := newRemote(t, "v1.0.0")
	for name, plant := range gitPlants {
		t.Run(name, func(t *testing.T) {
			o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
			mine := anotherRepository(t)
			head := read(t, filepath.Join(mine, ".git", "HEAD"))
			before := treeNames(t, mine)

			tree := filepath.Join(cacheRootOf(t, o), treeName)
			if err := os.MkdirAll(tree, cacheDirMode); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(tree, "VERSION"), []byte("v0.9.0\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			plant(t, tree, filepath.Join(mine, ".git"))

			dir, _, err := Ensure(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			if after := read(t, filepath.Join(mine, ".git", "HEAD")); after != head {
				t.Fatalf("the repository the tree's .git named was rewritten: %q became %q", head, after)
			}
			if after := treeNames(t, mine); !slices.Equal(before, after) {
				t.Fatalf("the repository the tree's .git named was written into: %v became %v", before, after)
			}
			if got := read(t, filepath.Join(mine, "notes")); got != "mine\n" {
				t.Fatalf("the repository the tree's .git named lost its work: %q", got)
			}
			if got := read(t, filepath.Join(dir, "VERSION")); got != "v1.0.0\n" {
				t.Fatalf("the tree holds %q", got)
			}
		})
	}
}

// treeNames is everything under a directory, relative and sorted, for
// comparing a repository before and after a call that must not have written
// into it. A whole listing rather than HEAD alone: an object or a ref landing
// in someone's repository is the harm, and where git puts it is its business.
func treeNames(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
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

// TestUnpackRefusesAnEntryThroughALink: the directory an entry is written in
// is resolved by the filesystem, not by the check that compares the entry's
// path with its link target, so a link an earlier entry planted turns a later
// entry's own directory into the link's target. Both archives below pass every
// other check -- "." and "../outside" are each local to the directory they name
// -- and together they write at the tree's top level a name the archive never
// put there: a link out of the tree, which is the link Read would follow, and a
// file where the tree's own root is. The entries that do it are what this
// package must refuse, because the tree it serves is read-only (F8, F9).
func TestUnpackRefusesAnEntryThroughALink(t *testing.T) {
	const top = "fugaro-abc"
	planted := &tar.Header{Name: top + "/a", Typeflag: tar.TypeSymlink, Linkname: "."}
	cases := []struct {
		name string
		hdrs []*tar.Header
		want string
	}{
		{"a link under a link", []*tar.Header{planted,
			{Name: top + "/a/link", Typeflag: tar.TypeSymlink, Linkname: "../outside"}}, "link"},
		{"a file under a link", []*tar.Header{planted,
			{Name: top + "/a/evil", Typeflag: tar.TypeReg}}, "evil"},
		{"a directory under a link", []*tar.Header{planted,
			{Name: top + "/a/dir", Typeflag: tar.TypeDir}}, "dir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dest := t.TempDir()
			body := archiveOf(t, top, nil, tc.hdrs...)
			err := unpack(bytes.NewReader(body), dest, 1<<20)
			if err == nil {
				t.Fatalf("the archive was unpacked into %v: %s is a name it never wrote at the tree's top level", treeNames(t, dest), tc.want)
			}
			if _, err := os.Lstat(filepath.Join(dest, tc.want)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("the refused archive left %s (%v)", filepath.Join(dest, tc.want), err)
			}
		})
	}
}

// TestUnpackRefusesALinkThatResolvesOutOfTheTree: a link target is resolved by
// the kernel, not by the comparison escapes makes, so "a/../outside" is not the
// tree's "outside" when "a" is a link an earlier entry planted: it is the
// parent of the tree, and a chain of such links climbs one level per link.
// Every link is in the tree once the archive is unpacked, so they are resolved
// then, the way internal/cache resolves the links in its own archives. The
// third archive is the other half of the check: a target that goes through a
// link and stays in the tree is kept, because links are legitimate entries the
// repository itself ships.
func TestUnpackRefusesALinkThatResolvesOutOfTheTree(t *testing.T) {
	const top = "fugaro-abc"
	link := func(name, target string) *tar.Header {
		return &tar.Header{Name: top + "/" + name, Typeflag: tar.TypeSymlink, Linkname: target}
	}
	file := func(name string) *tar.Header {
		return &tar.Header{Name: top + "/" + name, Typeflag: tar.TypeReg}
	}
	refused := []struct {
		name string
		hdrs []*tar.Header
	}{
		{"a target that climbs through a link", []*tar.Header{link("a", "."), link("y", "a/../outside")}},
		{"a chain of links that climbs", []*tar.Header{link("a", "."), link("y", "a/.."), link("z", "y/..")}},
		{"links that point at each other", []*tar.Header{link("a", "b"), link("b", "a")}},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			dest := t.TempDir()
			body := archiveOf(t, top, nil, tc.hdrs...)
			if err := unpack(bytes.NewReader(body), dest, 1<<20); err == nil {
				t.Fatalf("the archive was unpacked: %v holds a link that leaves the tree", treeNames(t, dest))
			}
		})
	}
	t.Run("a target that stays in the tree through a link", func(t *testing.T) {
		dest := t.TempDir()
		body := archiveOf(t, top, nil, link("a", "."), link("y", "a/VERSION"), file("VERSION"))
		if err := unpack(bytes.NewReader(body), dest, 1<<20); err != nil {
			t.Fatalf("a link that resolves inside the tree was refused: %v", err)
		}
		if target, err := os.Readlink(filepath.Join(dest, "y")); err != nil || target != "a/VERSION" {
			t.Fatalf("the link is not in the tree: %q, %v", target, err)
		}
	})
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
		{"a .git directory", 1 << 20, []*tar.Header{{Name: top + "/.git/config", Typeflag: tar.TypeReg}}},
		{"a .git file", 1 << 20, []*tar.Header{{Name: top + "/.git", Typeflag: tar.TypeReg}}},
		{"a .git directory in another case", 1 << 20, []*tar.Header{{Name: top + "/.GIT/config", Typeflag: tar.TypeReg}}},
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

// The table above cannot show which check refuses its ".." entry: the archive
// it writes names the top-level directory first, so a path that steps back over
// it is already a second top-level directory by the time it is judged. This is
// the check itself, judged with no top-level directory recorded yet.
func TestEntryPathRefusesAPathThatClimbs(t *testing.T) {
	for _, name := range []string{"../evil", "top/../../evil", "a/b/../../../evil"} {
		t.Run(name, func(t *testing.T) {
			top := ""
			if _, err := entryPath(name, &top); err == nil || !strings.Contains(err.Error(), "it contains ..") {
				t.Fatalf("entryPath(%q) = %v, want a refusal for ..", name, err)
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
		// The refusal of .git is by the name itself: the repository's own
		// .gitignore and .github must survive it.
		{Name: "fugaro-abc/.gitignore", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
		{Name: "fugaro-abc/.github/workflows/ci.yml", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
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
	for _, name := range []string{".gitignore", filepath.Join(".github", "workflows", "ci.yml")} {
		if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
			t.Fatalf("%s is not in the tree: %v", name, err)
		}
	}
	// The global pax header every archive here opens with writes no file: it
	// must not be taken for the archive's top-level directory, and it must
	// not land in the tree under its own name.
	if _, err := os.Stat(filepath.Join(dest, "pax_global_header")); err == nil {
		t.Fatal("the archive's global pax header was unpacked as a file")
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

// plantUploadpack makes the git configuration at dir one whose fetch runs a
// command of its own: a remote's uploadpack is the program git runs to speak
// to the source, and it is read from the same configuration a filter is.
func plantUploadpack(t *testing.T, dir, marker string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), cacheDirMode); err != nil {
		t.Fatal(err)
	}
	cfg, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(cfg, "\n[remote \"origin\"]\n\tuploadpack = touch %s && git-upload-pack\n", marker); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestChangesSinceRunsNoPlantedCommand: the fetch ChangesSince makes is the
// other place git runs a command the cache's configuration names (an
// uploadpack here, an sshCommand with an ssh URL, the same class), and the
// tree's configuration is written by whatever runs as this user. The history
// is fetched in a repository this call initialises itself, so neither the
// tree's configuration nor the one in the staging directory it uses is read.
func TestChangesSinceRunsNoPlantedCommand(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	dir, _, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "uploadpack-ran")
	if err := chmodTree(dir, true); err != nil {
		t.Fatal(err)
	}
	plantUploadpack(t, dir, marker)
	if err := chmodTree(dir, false); err != nil {
		t.Fatal(err)
	}
	plantUploadpack(t, filepath.Join(cacheRootOf(t, o), stageName), marker)

	dest := filepath.Join(t.TempDir(), "changes.txt")
	if err := ChangesSince(context.Background(), o, dir, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a command the cache held ran: %v", err)
	}
	if got := read(t, dest); !strings.Contains(got, "release v1.1.0") {
		t.Fatalf("the changes file was not written from the remote:\n%s", got)
	}
}

// TestChangesSinceLeavesAnotherRepositoryAlone: the tree's ".git" can name a
// repository of the person's rather than this cache's, and a changes file is
// read with git. A tree that is not this cache's repository gets the answer a
// tree without history gets, and the repository named is left alone: a fetch
// there would make it shallow and write its tags.
func TestChangesSinceLeavesAnotherRepositoryAlone(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	for name, plant := range gitPlants {
		t.Run(name, func(t *testing.T) {
			o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
			mine := anotherRepository(t)
			head := read(t, filepath.Join(mine, ".git", "HEAD"))
			before := treeNames(t, mine)

			tree := filepath.Join(cacheRootOf(t, o), treeName)
			if err := os.MkdirAll(tree, cacheDirMode); err != nil {
				t.Fatal(err)
			}
			plant(t, tree, filepath.Join(mine, ".git"))

			dest := filepath.Join(t.TempDir(), "changes.txt")
			if err := ChangesSince(context.Background(), o, tree, dest); err != nil {
				t.Fatal(err)
			}
			if got := read(t, dest); !strings.Contains(got, "carries no history") {
				t.Errorf("the changes file is not the one a tree without history gets:\n%s", got)
			}
			if after := read(t, filepath.Join(mine, ".git", "HEAD")); after != head {
				t.Fatalf("the repository the tree's .git named was rewritten: %q became %q", head, after)
			}
			if after := treeNames(t, mine); !slices.Equal(before, after) {
				t.Fatalf("the repository the tree's .git named was written into: %v became %v", before, after)
			}
		})
	}
}

// TestChangesSinceOnTheNewestRelease: the release the person installed is the
// newest one the remote has, and the remote's main is at it -- which is what
// this repository looks like right after a release. Excluding that tag in the
// fetch leaves nothing to select, and git fails the whole fetch over it ("no
// commits selected for shallow requests"), so the answer has to be the empty
// list rather than no file at all.
func TestChangesSinceOnTheNewestRelease(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.1.0", r.commit["v1.1.0"])
	dir, _, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "changes.txt")
	if err := ChangesSince(context.Background(), o, dir, dest); err != nil {
		t.Fatalf("the newest release: %v", err)
	}
	got := read(t, dest)
	for _, want := range []string{"after v1.1.0", "No release is newer"} {
		if !strings.Contains(got, want) {
			t.Errorf("the changes file does not name %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "could not be anchored") {
		t.Errorf("the newest release is anchored to its own tag:\n%s", got)
	}
}

// TestChangesSinceWithoutTheVersionsTag: a version that parses as a release
// but names a tag the remote does not carry -- a version cut in main between
// two releases, or one built from a private repository -- must still produce
// the file. --shallow-exclude names a ref on the remote, and the fetch fails
// with "ambiguous deepen-not" on one that is not there, which left the
// diagnosis with no changes file at all rather than with an unanchored one.
func TestChangesSinceWithoutTheVersionsTag(t *testing.T) {
	r := newRemote(t, "v1.0.0", "v1.1.0")
	o := options(t, r, "v1.0.0", r.commit["v1.0.0"])
	dir, _, err := Ensure(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	since := o
	since.Version = "1.2.3"
	dest := filepath.Join(t.TempDir(), "changes.txt")
	if err := ChangesSince(context.Background(), since, dir, dest); err != nil {
		t.Fatalf("the version's tag is not on the remote: %v", err)
	}
	got := read(t, dest)
	for _, want := range []string{"after 1.2.3", "could not be anchored", "v1.1.0:", "release v1.1.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("the changes file does not name %q:\n%s", want, got)
		}
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
