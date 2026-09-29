package imagecheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// GitTree is a Tree over one commit of a blobless, no-checkout clone: it
// holds every commit and tree of the branch, and fetches a blob from the
// remote only when ReadFile asks for it. The working tree is never
// checked out.
type GitTree struct {
	ctx  context.Context
	dir  string
	env  []string
	head string
	// files are the head tree's regular files: path → blob ID.
	files map[string]string
	paths []string // sorted

	mu    sync.Mutex
	reads map[string][]byte
}

// CloneOptions say what to clone and how.
type CloneOptions struct {
	URL, Branch string
	// Dir is the directory to clone into; it must not exist yet.
	Dir string
	// Env is git's whole environment, credential variables included
	// (gitops.CredentialVars), so no token reaches an argv.
	Env []string
}

// gitWaitDelay bounds how long a git command's output is drained after
// git exits.
const gitWaitDelay = 5 * time.Second

// Clone makes a blobless, no-checkout, single-branch clone of o.Branch and
// reads its head's tree. ctx bounds every later git call of the tree.
func Clone(ctx context.Context, o CloneOptions) (*GitTree, error) {
	if o.URL == "" || o.Branch == "" || o.Dir == "" {
		return nil, errors.New("a clone needs a URL, a branch and a directory")
	}
	g := &GitTree{ctx: ctx, env: o.Env, reads: map[string][]byte{}}
	if _, err := g.git("", "clone", "--quiet", "--filter=blob:none", "--no-checkout", "--single-branch", "--branch", o.Branch, "--", o.URL, o.Dir); err != nil {
		return nil, fmt.Errorf("cloning the %s branch: %w", o.Branch, err)
	}
	g.dir = o.Dir
	head, err := g.git(g.dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, fmt.Errorf("reading the %s branch's head: %w", o.Branch, err)
	}
	g.head = strings.TrimSpace(string(head))
	// ls-tree needs trees only, which the blobless clone has.
	out, err := g.git(g.dir, "ls-tree", "-r", "-z", "--full-tree", g.head)
	if err != nil {
		return nil, fmt.Errorf("listing the head's tree: %w", err)
	}
	g.files = map[string]string{}
	for _, entry := range bytes.Split(out, []byte{0}) {
		meta, path, ok := strings.Cut(string(entry), "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta) // <mode> <type> <id>
		// Only regular files, as Dir counts them: no symlinks (120000) and
		// no submodules (commits).
		if len(f) != 3 || f[1] != "blob" || (f[0] != "100644" && f[0] != "100755") {
			continue
		}
		g.files[path] = f[2]
		g.paths = append(g.paths, path)
	}
	slices.Sort(g.paths)
	return g, nil
}

// git runs git in dir (none when "") with the tree's environment.
func (g *GitTree) git(dir string, args ...string) ([]byte, error) {
	return g.gitEnv(dir, nil, args...)
}

func (g *GitTree) gitEnv(dir string, extra []string, args ...string) ([]byte, error) {
	c := exec.CommandContext(g.ctx, "git", args...)
	c.Dir = dir
	c.Env = append(slices.Clone(g.env), extra...)
	c.WaitDelay = gitWaitDelay
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		return nil, &GitError{Args: args[0], Err: err, Stderr: msg}
	}
	return stdout.Bytes(), nil
}

// GitError is a failed git command.
type GitError struct {
	Args   string // the subcommand
	Err    error
	Stderr string
}

func (e *GitError) Error() string {
	if e.Stderr == "" {
		return "git " + e.Args + ": " + e.Err.Error()
	}
	return "git " + e.Args + ": " + e.Err.Error() + ": " + e.Stderr
}

func (e *GitError) Unwrap() error { return e.Err }

// Head is the branch's head commit.
func (g *GitTree) Head() string { return g.head }

// Dir is the clone's directory.
func (g *GitTree) Dir() string { return g.dir }

// BlobID is the blob ID of the regular file at path in the head tree.
func (g *GitTree) BlobID(path string) (string, error) {
	id, ok := g.files[path]
	if !ok {
		return "", fmt.Errorf("%s: %w", path, fs.ErrNotExist)
	}
	return id, nil
}

// Glob returns the head tree's regular files matching pattern.
func (g *GitTree) Glob(pattern string) ([]string, error) {
	var out []string
	for _, p := range g.paths {
		ok, err := doublestar.Match(pattern, p)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// ReadFile reads a regular file of the head tree: one git cat-file, which
// fetches that blob on demand. A file is read at most once.
func (g *GitTree) ReadFile(path string) ([]byte, error) {
	if _, err := g.BlobID(path); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if data, ok := g.reads[path]; ok {
		return slices.Clone(data), nil
	}
	data, err := g.git(g.dir, "cat-file", "blob", g.head+":"+path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	g.reads[path] = data
	return slices.Clone(data), nil
}

// Changed reports whether a file matching one of globs differs between
// the commits from and to, and which files do. It compares trees only:
// rename detection, which would fetch blobs, is off.
func (g *GitTree) Changed(from, to string, globs []string) (bool, []string, error) {
	out, err := g.git(g.dir, "diff", "--name-only", "--no-renames", "-z", from, to, "--")
	if err != nil {
		return false, nil, err
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	matched := MatchPaths(globs, files)
	return len(matched) > 0, matched, nil
}

// IsAncestor reports whether commit is an ancestor of the head (or the
// head itself). A commit the clone doesn't have is not: it is gone from
// the branch, after a force-push. The clone doesn't fetch it.
func (g *GitTree) IsAncestor(commit string) (bool, error) {
	if commit == "" || strings.HasPrefix(commit, "-") {
		return false, nil
	}
	noFetch := []string{"GIT_NO_LAZY_FETCH=1"}
	if _, err := g.gitEnv(g.dir, noFetch, "cat-file", "-e", commit+"^{commit}"); err != nil {
		return false, nil
	}
	_, err := g.gitEnv(g.dir, noFetch, "merge-base", "--is-ancestor", commit, g.head)
	var ee *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return false, nil
	}
	return false, err
}
