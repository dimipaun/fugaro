package imagecheck

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// filterRemote is a bare repository seeded with files, which serves
// blobless clones and their on-demand blob fetches over file://, as a
// provider's server does. It returns the URL and a working clone to push
// further commits from.
func filterRemote(t *testing.T, files map[string]string) (url, work string) {
	t.Helper()
	testutil.IsolateGit(t)
	bare := testutil.NewRemote(t, files)
	testutil.Git(t, bare, "config", "uploadpack.allowFilter", "true")
	testutil.Git(t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	work = filepath.Join(t.TempDir(), "work")
	testutil.Git(t, filepath.Dir(work), "clone", "--quiet", bare, work)
	return "file://" + bare, work
}

func push(t *testing.T, work string, files map[string]string, msg string) string {
	t.Helper()
	testutil.WriteFiles(t, work, files)
	testutil.Git(t, work, "add", "-A")
	testutil.Git(t, work, "commit", "--quiet", "-m", msg)
	testutil.Git(t, work, "push", "--quiet", "--force", "origin", "HEAD:refs/heads/main")
	return testutil.Git(t, work, "rev-parse", "HEAD")
}

func clone(t *testing.T, url string) *GitTree {
	t.Helper()
	tree, err := Clone(context.Background(), CloneOptions{URL: url, Branch: "main", Dir: filepath.Join(t.TempDir(), "clone"), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

var nodeFiles = map[string]string{
	"fugaro.yaml":       webYAML,
	"package.json":      `{"name":"web"}`,
	"package-lock.json": `{"lockfileVersion":3}`,
	"src/index.ts":      "export {}\n",
}

// recordOf is the record a build of tree's head would write.
func recordOf(t *testing.T, tree Tree, head string) *Record {
	t.Helper()
	cfg := parse(t, webYAML)
	keys, err := KeyFiles(cfg, "web", tree)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := ImageConfigHash(cfg, "web", tree)
	if err != nil {
		t.Fatal(err)
	}
	return &Record{Version: RecordVersion, Workflow: "web", SourceCommit: head, KeyFiles: keys, ImageConfigHash: hash,
		BaseRef: "base:1", BaseDigest: digestA, ImageDigest: digestLatest, BuiltAt: time.Now().Add(-time.Hour)}
}

// inputsFor are the check's inputs against tree for rec.
func inputsFor(t *testing.T, tree *GitTree, rec *Record) Inputs {
	t.Helper()
	cfg := parse(t, webYAML)
	in := Inputs{
		Config: cfg, Workflow: "web", Record: rec, Head: tree.Head(), Tree: tree, Changed: tree.Changed,
		BaseRef: "base:1", BaseDigest: digestA, LatestDigest: digestLatest, Now: time.Now(), Rebuild: cfg.Workflows["web"].Rebuild.Defaults(),
	}
	if rec != nil {
		ok, err := tree.IsAncestor(rec.SourceCommit)
		if err != nil {
			t.Fatal(err)
		}
		in.BuiltCommitReachable = ok
	}
	return in
}

func TestCloneMatchesOpen(t *testing.T) {
	url, work := filterRemote(t, nodeFiles)
	tree := clone(t, url)
	if tree.Head() != testutil.Git(t, work, "rev-parse", "HEAD") {
		t.Fatalf("head = %s", tree.Head())
	}
	dir, err := Open(context.Background(), work, "HEAD", os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	for path := range nodeFiles {
		a, err1 := tree.BlobID(path)
		b, err2 := dir.BlobID(path)
		if err1 != nil || err2 != nil || a != b {
			t.Errorf("%s: tree %s %v, dir %s %v", path, a, err1, b, err2)
		}
	}
	if _, err := tree.BlobID("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
	data, err := tree.ReadFile("package.json")
	if err != nil || string(data) != nodeFiles["package.json"] {
		t.Fatalf("ReadFile = %q, %v", data, err)
	}
	got, err := tree.Glob("**/*.ts")
	if err != nil || !slices.Equal(got, []string{"src/index.ts"}) {
		t.Fatalf("Glob = %v, %v", got, err)
	}
	// The clone is blobless (a partial clone of its origin), and has no
	// working tree.
	if got := testutil.Git(t, tree.Dir(), "config", "remote.origin.partialclonefilter"); got != "blob:none" {
		t.Errorf("partialclonefilter = %q", got)
	}
	entries, err := os.ReadDir(tree.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != ".git" {
			t.Errorf("the clone checked out %s", e.Name())
		}
	}
}

// TestOpenReadsGitNotTheWorkingTree: the build's tree (Open over its
// checkout) gives the same key-file IDs and contents as the check's clone,
// even when an eol attribute makes the checkout's bytes differ from the
// blob's, and an untracked file is not in it.
func TestOpenReadsGitNotTheWorkingTree(t *testing.T) {
	files := map[string]string{}
	for k, v := range nodeFiles {
		files[k] = v
	}
	files[".gitattributes"] = "package-lock.json text eol=crlf\n"
	files["package-lock.json"] = "{\n  \"lockfileVersion\": 3\n}\n"
	url, _ := filterRemote(t, files)
	work := filepath.Join(t.TempDir(), "checkout")
	testutil.Git(t, filepath.Dir(work), "clone", "--quiet", strings.TrimPrefix(url, "file://"), work)
	if data, _ := os.ReadFile(filepath.Join(work, "package-lock.json")); !strings.Contains(string(data), "\r\n") {
		t.Fatalf("the checkout did not apply the attribute: %q", data)
	}
	testutil.WriteFiles(t, work, map[string]string{"untracked.lock": "x\n"})
	built, err := Open(context.Background(), work, "HEAD", os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	checked := clone(t, url)
	cfg := parse(t, webYAML)
	a, err1 := KeyFiles(cfg, "web", built)
	b, err2 := KeyFiles(cfg, "web", checked)
	if err1 != nil || err2 != nil || !maps.Equal(a, b) || a["package-lock.json"] == "" {
		t.Errorf("key files: build %v %v, check %v %v", a, err1, b, err2)
	}
	h1, err1 := ImageConfigHash(cfg, "web", built)
	h2, err2 := ImageConfigHash(cfg, "web", checked)
	if err1 != nil || err2 != nil || h1 != h2 {
		t.Errorf("image config hash: build %s %v, check %s %v", h1, err1, h2, err2)
	}
	if got, _ := built.Glob("*.lock"); len(got) != 0 {
		t.Errorf("the build's tree has untracked files: %v", got)
	}
	if _, err := Open(context.Background(), work, "--output=x", os.Environ()); err == nil {
		t.Error("Open took an option as a revision")
	}
}

func TestCheckRebuildsWhenRecordMissing(t *testing.T) {
	url, _ := filterRemote(t, nodeFiles)
	tree := clone(t, url)
	d := Decide(context.Background(), inputsFor(t, tree, nil))
	if d.Decision != Rebuild || !slices.Equal(d.Reasons, []string{ReasonNoRecord}) || d.Inputs.SourceCommit != tree.Head() {
		t.Fatalf("Decide = %+v", d)
	}
}

// TestCheckRebuildsWhenBuiltCommitNotOnBranch: the recorded commit was
// force-pushed away, so the image holds code the branch no longer has.
func TestCheckRebuildsWhenBuiltCommitNotOnBranch(t *testing.T) {
	url, work := filterRemote(t, nodeFiles)
	built := push(t, work, map[string]string{"src/index.ts": "export const a = 1\n"}, "built")
	// Rewrite history: drop the built commit and push another one.
	testutil.Git(t, work, "reset", "--quiet", "--hard", "HEAD~1")
	push(t, work, map[string]string{"src/other.ts": "export {}\n"}, "rewritten")
	tree := clone(t, url)
	rec := recordOf(t, tree, built)
	d := Decide(context.Background(), inputsFor(t, tree, rec))
	if d.Decision != Rebuild || !slices.Contains(d.Reasons, ReasonBuiltCommitGone) {
		t.Fatalf("Decide = %+v", d)
	}
}

func TestLockfilesIgnoresUnrelatedCommits(t *testing.T) {
	url, work := filterRemote(t, nodeFiles)
	built := testutil.Git(t, work, "rev-parse", "HEAD")
	push(t, work, map[string]string{"src/index.ts": "export const b = 2\n", "README.md": "hi\n"}, "unrelated")
	tree := clone(t, url)
	rec := recordOf(t, Dir{Root: work}, built)
	d := Decide(context.Background(), inputsFor(t, tree, rec))
	if d.Decision != Skip || len(d.Reasons) != 0 || tree.Head() == built {
		t.Fatalf("unrelated commit: %+v", d)
	}

	push(t, work, map[string]string{"package-lock.json": `{"lockfileVersion":3,"packages":{}}`}, "lockfile")
	tree = clone(t, url)
	d = Decide(context.Background(), inputsFor(t, tree, rec))
	if d.Decision != Rebuild || !slices.Equal(d.Reasons, []string{ReasonLockfiles}) {
		t.Fatalf("lockfile commit: %+v", d)
	}
}

func TestPathsTrigger(t *testing.T) {
	url, work := filterRemote(t, nodeFiles)
	built := testutil.Git(t, work, "rev-parse", "HEAD")
	push(t, work, map[string]string{"packages/a/src/x.ts": "export {}\n"}, "package change")
	tree := clone(t, url)
	changed, files, err := tree.Changed(built, tree.Head(), []string{"packages/**"})
	if err != nil || !changed || !slices.Equal(files, []string{"packages/a/src/x.ts"}) {
		t.Fatalf("Changed = %v %v %v", changed, files, err)
	}
	if changed, _, err := tree.Changed(built, tree.Head(), []string{"docs/**"}); err != nil || changed {
		t.Fatalf("docs/** = %v %v", changed, err)
	}
	in := inputsFor(t, tree, recordOf(t, Dir{Root: work}, built))
	in.Rebuild.Paths = []string{"packages/**"}
	if d := Decide(context.Background(), in); d.Decision != Rebuild || !slices.Equal(d.Reasons, []string{ReasonPaths}) {
		t.Fatalf("Decide = %+v", d)
	}
}

// TestCheckNeverReadsFullCheckout: the check reads fugaro.yaml and the few
// files package manager detection needs, never the whole tree. A git
// wrapper on PATH counts the blob reads.
func TestCheckNeverReadsFullCheckout(t *testing.T) {
	files := map[string]string{}
	for k, v := range nodeFiles {
		files[k] = v
	}
	for i := range 50 {
		files["src/f"+strconv.Itoa(i)+".ts"] = "export const x = " + strconv.Itoa(i) + "\n"
	}
	url, _ := filterRemote(t, files)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	count := filepath.Join(t.TempDir(), "count")
	wrapper := "#!/bin/sh\nfor a in \"$@\"; do if [ \"$a\" = cat-file ]; then echo x >> " + count + "; break; fi; done\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	tree := clone(t, url)
	data, err := tree.ReadFile("fugaro.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg := parse(t, string(data))
	keys, err := KeyFiles(cfg, "web", tree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ImageConfigHash(cfg, "web", tree); err != nil {
		t.Fatal(err)
	}
	// A second workflow's evaluation reads nothing again.
	if _, err := KeyFiles(cfg, "web", tree); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(count)
	n := strings.Count(string(raw), "x")
	if n == 0 || n > len(keys)+2 {
		t.Fatalf("%d cat-file calls for %d key files", n, len(keys))
	}
}
