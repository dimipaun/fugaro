package gitops

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

const workflowsRejection = "git push --quiet origin HEAD:refs/heads/fugaro/x: exit status 1: " +
	"To https://github.com/o/r.git\n" +
	" ! [remote rejected] HEAD -> fugaro/x (refusing to allow a GitHub App to create or update workflow `.github/workflows/images.yml` without `workflows` permission)\n" +
	"error: failed to push some refs to 'https://github.com/o/r.git'"

func TestClassifyPush(t *testing.T) {
	cases := []struct {
		name  string
		msg   string
		kind  RejectKind
		files []string
	}{
		{"workflows", workflowsRejection, RejectWorkflows, []string{".github/workflows/images.yml"}},
		{"protected", "remote: error: GH006: Protected branch update failed for refs/heads/fugaro/x.\n ! [remote rejected] HEAD -> fugaro/x (protected branch hook declined)", RejectProtectedBranch, nil},
		{"large file", "remote: error: GH001: Large files detected. You may want to try Git Large File Storage\nremote: error: File big.bin is 150.00 MB; this exceeds GitHub's file size limit of 100.00 MB\n ! [remote rejected] HEAD -> fugaro/x (pre-receive hook declined)", RejectLargeFile, nil},
		{"secret scanning", "remote: error: GH013: Repository rule violations found for refs/heads/fugaro/x.\nremote: - GITHUB PUSH PROTECTION\nremote:   Push cannot contain secrets\n ! [remote rejected] HEAD -> fugaro/x (push declined due to repository rule violations)", RejectSecretScanning, nil},
		{"secret scanning, GH013 wording", "remote: error: GH013: Repository rule violations found for refs/heads/fugaro/x.\nremote: - Secret detected in commit abc123", RejectSecretScanning, nil},
		{"push protection wording", "remote: error: Push protection blocked this push: secrets were found", RejectSecretScanning, nil},
		{"generic repository rules", "remote: error: GH013: Repository rule violations found for refs/heads/fugaro/x.\nremote: - Commits must have verified signatures.\n ! [remote rejected] HEAD -> fugaro/x (push declined due to repository rule violations)", RejectRepoRules, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyPush(errors.New(c.msg))
			if got == nil || got.Kind != c.kind || !reflect.DeepEqual(got.Files, c.files) {
				t.Fatalf("ClassifyPush = %+v, want kind %s files %v", got, c.kind, c.files)
			}
		})
	}
	for _, msg := range []string{
		"git push --force-with-lease=refs/heads/fugaro/x: origin HEAD:refs/heads/fugaro/x: exit status 128: fatal: unable to access 'https://github.com/org/secrets-api/': Could not resolve host: github.com",
		"git push https://github.com/org/secrets-api.git: exit status 1: ! [rejected] HEAD -> fugaro/x (fetch first)",
		"git push: exit status 128: fatal: unable to access: Could not resolve host",
		"! [rejected] HEAD -> fugaro/x (stale info)",
		"",
	} {
		if got := ClassifyPush(errors.New(msg)); got != nil {
			t.Errorf("ClassifyPush(%q) = %+v, want nil", msg, got)
		}
	}
	if ClassifyPush(nil) != nil {
		t.Error("ClassifyPush(nil) != nil")
	}
}

func TestClassifyPushThroughWrapping(t *testing.T) {
	err := fmt.Errorf("pushing: %w", errors.New(workflowsRejection))
	if ClassifyPush(err) == nil {
		t.Fatal("a wrapped rejection was not recognised")
	}
}

// rejectingRemote installs a pre-receive hook that prints msg and refuses.
func rejectingRemote(t *testing.T, remote, msg string) {
	t.Helper()
	hook := filepath.Join(remote, "hooks", "pre-receive")
	script := "#!/bin/sh\ncat >/dev/null\necho '" + msg + "' >&2\nexit 1\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPushReportsAWorkflowRejection(t *testing.T) {
	repo, remote := setup(t)
	rejectingRemote(t, remote, "refusing to allow a GitHub App to create or update workflow `.github/workflows/ci.yml` without `workflows` permission")
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{".github/workflows/ci.yml": "on: push\n"})
	if _, err := repo.CommitAll(ctx, "ci"); err != nil {
		t.Fatal(err)
	}
	err := repo.Push(ctx, "fugaro/x")
	var rej *PushRejected
	if !errors.As(err, &rej) || rej.Kind != RejectWorkflows {
		t.Fatalf("Push = %v, want a workflows *PushRejected", err)
	}
	if !strings.Contains(err.Error(), "exit status") {
		t.Errorf("the original git error is lost: %v", err)
	}
}

func TestPushLeavesOtherFailuresAlone(t *testing.T) {
	repo, remote := setup(t)
	rejectingRemote(t, remote, "no thanks")
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if _, err := repo.CommitAll(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	err := repo.Push(ctx, "fugaro/x")
	var rej *PushRejected
	if err == nil || errors.As(err, &rej) {
		t.Fatalf("Push = %v, want a plain error", err)
	}
}

func TestWorkflowFiles(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.WorkflowFiles(ctx, "origin/main"); err != nil || len(got) != 0 {
		t.Fatalf("clean branch: %v, %v", got, err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{
		".github/workflows/b.yml":   "b\n",
		".github/workflows/a.yaml":  "a\n",
		".github/ISSUE_TEMPLATE.md": "not a workflow\n",
		"docs/.github/workflows/x":  "nested, not the root\n",
		"src/main.go":               "package main\n",
	})
	if _, err := repo.CommitAll(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	got, err := repo.WorkflowFiles(ctx, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".github/workflows/a.yaml", ".github/workflows/b.yml"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("WorkflowFiles = %v, want %v", got, want)
	}
}

// A change is a difference in content, mode or existence against the base:
// editing, deleting and adding are all this run's, and so is a file moved
// into the directory (git reports no rename here, only the add).
func TestWorkflowFilesSeesEveryKindOfChange(t *testing.T) {
	cases := []struct {
		name  string
		apply func(t *testing.T, repo *Repo)
		want  []string
	}{
		{"edited", func(t *testing.T, repo *Repo) {
			testutil.WriteFiles(t, repo.Dir, map[string]string{".github/workflows/ci.yml": "on: pull_request\n"})
			if _, err := repo.CommitAll(ctx, "edit"); err != nil {
				t.Fatal(err)
			}
		}, []string{".github/workflows/ci.yml"}},
		{"deleted", func(t *testing.T, repo *Repo) {
			if err := os.Remove(filepath.Join(repo.Dir, ".github", "workflows", "ci.yml")); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.CommitAll(ctx, "delete"); err != nil {
				t.Fatal(err)
			}
		}, []string{".github/workflows/ci.yml"}},
		{"added", func(t *testing.T, repo *Repo) {
			testutil.WriteFiles(t, repo.Dir, map[string]string{".github/workflows/new.yml": "on: push\n"})
			if _, err := repo.CommitAll(ctx, "add"); err != nil {
				t.Fatal(err)
			}
		}, []string{".github/workflows/new.yml"}},
		{"renamed in", func(t *testing.T, repo *Repo) {
			testutil.Git(t, repo.Dir, "mv", "pipeline.yml", ".github/workflows/pipeline.yml")
			if _, err := repo.CommitAll(ctx, "move it in"); err != nil {
				t.Fatal(err)
			}
		}, []string{".github/workflows/pipeline.yml"}},
		{"renamed within", func(t *testing.T, repo *Repo) {
			testutil.Git(t, repo.Dir, "mv", ".github/workflows/ci.yml", ".github/workflows/ci2.yml")
			if _, err := repo.CommitAll(ctx, "rename it"); err != nil {
				t.Fatal(err)
			}
		}, []string{".github/workflows/ci.yml", ".github/workflows/ci2.yml"}},
		{"moved out", func(t *testing.T, repo *Repo) {
			testutil.Git(t, repo.Dir, "mv", ".github/workflows/ci.yml", "ci.yml")
			if _, err := repo.CommitAll(ctx, "move it out"); err != nil {
				t.Fatal(err)
			}
		}, []string{".github/workflows/ci.yml"}},
		{"a file swapped for a symlink", func(t *testing.T, repo *Repo) {
			if err := os.Remove(filepath.Join(repo.Dir, ".github", "workflows", "ci.yml")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../evil.yml", filepath.Join(repo.Dir, ".github", "workflows", "ci.yml")); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.CommitAll(ctx, "swap"); err != nil {
				t.Fatal(err)
			}
		}, []string{".github/workflows/ci.yml"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, _ := setup(t)
			if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
				t.Fatal(err)
			}
			testutil.WriteFiles(t, repo.Dir, map[string]string{"pipeline.yml": "steps:\n", ".github/workflows/ci.yml": "on: push\n"})
			if _, err := repo.CommitAll(ctx, "a person's CI"); err != nil {
				t.Fatal(err)
			}
			base, err := repo.HeadSHA(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := repo.WorkflowFiles(ctx, base); err != nil || len(got) != 0 {
				t.Fatalf("before the change: %v, %v", got, err)
			}
			c.apply(t, repo)
			got, err := repo.WorkflowFiles(ctx, base)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("WorkflowFiles = %v, want %v", got, c.want)
			}
		})
	}
}

// The directory itself, swapped for a symlink or for a submodule (a
// gitlink), is a change too: it takes .github/workflows/ and everything
// under it out of the tree the push would land. A pathspec with a trailing
// slash hides a symlink entirely, and a name-prefix filter drops the path.
func TestWorkflowFilesSeesTheDirectoryItself(t *testing.T) {
	cases := []struct {
		name    string
		replace func(t *testing.T, repo *Repo)
	}{
		{"symlink", func(t *testing.T, repo *Repo) {
			if err := os.MkdirAll(filepath.Join(repo.Dir, ".github"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../evil", filepath.Join(repo.Dir, ".github", "workflows")); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.CommitAll(ctx, "symlink it"); err != nil {
				t.Fatal(err)
			}
		}},
		{"gitlink", func(t *testing.T, repo *Repo) {
			// A submodule standing where the directory was, staged by
			// hand: git add would demand a .gitmodules entry for it.
			testutil.Git(t, repo.Dir, "update-index", "--add", "--cacheinfo",
				"160000,1111111111111111111111111111111111111111,.github/workflows")
			testutil.Git(t, repo.Dir, "commit", "--quiet", "-m", "a gitlink")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, _ := setup(t)
			if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
				t.Fatal(err)
			}
			base, err := repo.HeadSHA(ctx)
			if err != nil {
				t.Fatal(err)
			}
			c.replace(t, repo)
			got, err := repo.WorkflowFiles(ctx, base)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(got, workflowRoot) {
				t.Fatalf("WorkflowFiles = %v, want it to name %s", got, workflowRoot)
			}
		})
	}
}

// A run that merges or rebases a newer base branch into its own branch
// carries that base's commits. Their workflow changes are the base's, not
// the run's: against the commit the run was cut from alone they are
// reported, and the push is refused for work the run never did.
func TestWorkflowFilesExceptLeavesOutTheBasesOwnChanges(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	start, err := repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The run's own change, before it rebases.
	testutil.WriteFiles(t, repo.Dir, map[string]string{".github/workflows/ci.yml": "on: pull_request\n"})
	if _, err := repo.CommitAll(ctx, "the run's CI"); err != nil {
		t.Fatal(err)
	}
	// A person lands a workflow file on main after the run was cut: a
	// dependabot bump, say, merged while the run was working.
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	testutil.WriteFiles(t, seed, map[string]string{".github/workflows/person.yml": "on: push\n"})
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "a person's CI")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	// The agent rebases onto it, as a run told to keep up with its base does.
	testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin", "main")
	testutil.Git(t, repo.Dir, "rebase", "--quiet", "origin/main")
	head, err := repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Against the run's own base alone the person's file is blamed on the
	// run, which is the shape of the merge-only range.
	mine, err := repo.WorkflowFiles(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".github/workflows/ci.yml", ".github/workflows/person.yml"}; !reflect.DeepEqual(mine, want) {
		t.Fatalf("WorkflowFiles = %v, want %v", mine, want)
	}
	// Less what the base branch changed itself, only the run's is left.
	got, err := repo.WorkflowFilesExcept(ctx, start, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".github/workflows/ci.yml"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("WorkflowFilesExcept = %v, want %v", got, want)
	}
	// The same over the exact commit a checkpoint would push.
	in, err := repo.WorkflowFilesInExcept(ctx, start, "origin/main", head)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, got) {
		t.Fatalf("WorkflowFilesInExcept = %v, want %v", in, got)
	}
	// A base branch that cannot be read falls back to the run's own base
	// alone — what the guard did before it knew about the base branch's own
	// commits — so the list is longer rather than empty.
	files, err := repo.WorkflowFilesExcept(ctx, start, "origin/nope")
	if err != nil {
		t.Fatalf("WorkflowFilesExcept with an unknown base ref: %v", err)
	}
	if !reflect.DeepEqual(files, mine) {
		t.Fatalf("WorkflowFilesExcept = %v, want the run's own %v", files, mine)
	}
}

// CommitsAhead counts the run's own commits: the base branch's own, merged
// or rebased in after the run was cut, are not the run's work, so a run
// that adds nothing still counts none.
func TestCommitsAheadCountsOnlyTheRunsOwnCommits(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	start, err := repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := repo.CommitsAhead(ctx, start, "origin/main"); err != nil || n != 0 {
		t.Fatalf("CommitsAhead before any commit = %d, %v; want 0", n, err)
	}
	if err := repo.CommitEmpty(ctx, "the run's own"); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.CommitsAhead(ctx, start, "origin/main"); err != nil || n != 1 {
		t.Fatalf("CommitsAhead = %d, %v; want 1", n, err)
	}
	// The base branch moves on and the agent rebases onto it: its commit is
	// not the run's, so the count is unchanged.
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	testutil.WriteFiles(t, seed, map[string]string{"person.txt": "a person's\n"})
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "a person's work")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin", "main")
	testutil.Git(t, repo.Dir, "rebase", "--quiet", "origin/main")
	if n, err := repo.CommitsAhead(ctx, start, "origin/main"); err != nil || n != 1 {
		t.Fatalf("CommitsAhead after a rebase = %d, %v; want the run's 1", n, err)
	}
	// Without the base branch to subtract there is no way to tell the two
	// apart, which is why the run itself is measured at bootstrap.
	if n, err := repo.CommitsAhead(ctx, start, ""); err != nil || n != 2 {
		t.Fatalf("CommitsAhead without a base = %d, %v; want 2", n, err)
	}
	// A base commit the checkout does not have (origin names a tip the
	// agent never fetched) subtracts nothing rather than failing finalize.
	if n, err := repo.CommitsAhead(ctx, start, "1111111111111111111111111111111111111111"); err != nil || n != 2 {
		t.Fatalf("CommitsAhead with a base the checkout lacks = %d, %v; want 2", n, err)
	}
}

func TestBundleHoldsOnlyTheRunsCommits(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if _, err := repo.CommitAll(ctx, "add a"); err != nil {
		t.Fatal(err)
	}
	head, _ := repo.HeadSHA(ctx)
	data, err := repo.Bundle(ctx, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "work.bundle")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// The bundle verifies against the base alone and fetches into a clone.
	testutil.Git(t, repo.Dir, "bundle", "verify", file)
	if got := testutil.Git(t, repo.Dir, "bundle", "list-heads", file); !strings.Contains(got, head) {
		t.Fatalf("list-heads = %q, want %s", got, head)
	}
	// Only the run's one commit travels: the seed commit is a prerequisite.
	clone := t.TempDir()
	testutil.Git(t, clone, "init", "--quiet")
	if _, err := repo.git(ctx, "-C", clone, "fetch", "--quiet", file, "HEAD"); err == nil {
		t.Fatal("a bundle with a prerequisite fetched into an empty repository")
	}
}

func TestBundleSizeCap(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": strings.Repeat("x", 4096)})
	if _, err := repo.CommitAll(ctx, "add a"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.BundleMax(ctx, "origin/main", 100); !errors.Is(err, ErrBundleTooLarge) {
		t.Fatalf("BundleMax = %v, want ErrBundleTooLarge", err)
	}
}

// commitRange makes a branch with the given commits, each a map of files
// (an empty value deletes) with its message, and returns the repo.
type commitSpec struct {
	msg   string
	files map[string]string
	del   []string
}

func branchWith(t *testing.T, commits ...commitSpec) *Repo {
	t.Helper()
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	for _, c := range commits {
		testutil.WriteFiles(t, repo.Dir, c.files)
		for _, d := range c.del {
			if err := os.Remove(filepath.Join(repo.Dir, d)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := repo.CommitAll(ctx, c.msg); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func hasSecret(text string) bool { return strings.Contains(text, "S3CRET-VALUE") }

func TestScanWorkSeesAValueCommittedThenRemoved(t *testing.T) {
	repo := branchWith(t,
		commitSpec{msg: "add", files: map[string]string{"k.txt": "token=S3CRET-VALUE\n"}},
		commitSpec{msg: "remove it", del: []string{"k.txt"}},
	)
	// The net diff is clean, but the bundle holds the first commit.
	if net := testutil.Git(t, repo.Dir, "diff", "origin/main...HEAD"); strings.Contains(net, "S3CRET") {
		t.Fatalf("the test's net diff is not clean: %s", net)
	}
	res, err := repo.ScanWork(ctx, "origin/main", hasSecret)
	if err != nil || !res.Hit {
		t.Fatalf("ScanWork = %+v, %v; want a hit", res, err)
	}
}

func TestScanWorkReadsCommitMessages(t *testing.T) {
	repo := branchWith(t, commitSpec{msg: "uses S3CRET-VALUE", files: map[string]string{"a.txt": "a\n"}})
	if res, err := repo.ScanWork(ctx, "origin/main", hasSecret); err != nil || !res.Hit {
		t.Fatalf("ScanWork = %+v, %v; want a hit", res, err)
	}
}

func TestScanWorkCleanRangeAndSinceBoundary(t *testing.T) {
	repo := branchWith(t,
		commitSpec{msg: "old", files: map[string]string{"k.txt": "S3CRET-VALUE\n"}},
		commitSpec{msg: "new", files: map[string]string{"b.txt": "b\n"}},
	)
	start := testutil.Git(t, repo.Dir, "rev-parse", "HEAD~1")
	res, err := repo.ScanWork(ctx, start, hasSecret)
	if err != nil || res.Hit || res.Unscannable {
		t.Fatalf("a range after the secret: %+v, %v", res, err)
	}
}

func TestScanWorkBinaryIsUnscannable(t *testing.T) {
	repo := branchWith(t, commitSpec{msg: "bin", files: map[string]string{"x.bin": "a\x00b\x00S3CRET"}})
	res, err := repo.ScanWork(ctx, "origin/main", hasSecret)
	if err != nil || !res.Unscannable {
		t.Fatalf("ScanWork = %+v, %v; want unscannable", res, err)
	}
}

func TestScanWorkAttributesCannotHideText(t *testing.T) {
	repo := branchWith(t, commitSpec{msg: "hidden", files: map[string]string{
		".gitattributes": "*.txt -diff\n", "k.txt": "S3CRET-VALUE\n"}})
	res, err := repo.ScanWork(ctx, "origin/main", hasSecret)
	if err != nil || !(res.Hit || res.Unscannable) {
		t.Fatalf("a -diff file passed the scan: %+v, %v", res, err)
	}
}

// The value sits across the boundary of two chunks: found only because
// the chunks overlap.
func TestScanWorkMatchesAcrossChunks(t *testing.T) {
	repo := branchWith(t, commitSpec{msg: "m", files: map[string]string{"k.txt": "token=S3CRET-VALUE\n"}})
	full := testutil.Git(t, repo.Dir, "log", "-m", "-p", "--format=%an <%ae> %cn <%ce>%n%B", "--no-show-signature", "^origin/main", "HEAD")
	at := strings.Index(full, "S3CRET-VALUE")
	if at < 0 {
		t.Fatal("the value is not in git's output")
	}
	defer func(c, o int) { scanChunk, scanOverlap = c, o }(scanChunk, scanOverlap)
	scanChunk, scanOverlap = at+4, 32 // the first chunk ends 4 bytes into the value
	if res, err := repo.ScanWork(ctx, "origin/main", hasSecret); err != nil || !res.Hit {
		t.Fatalf("ScanWork = %+v, %v; want a hit across the chunk boundary", res, err)
	}
	scanOverlap = 0
	if res, _ := repo.ScanWork(ctx, "origin/main", hasSecret); res.Hit {
		t.Fatal("without an overlap the split value was found: the test does not prove the overlap")
	}
}

func TestScanWorkReadsAuthorHeaders(t *testing.T) {
	repo := branchWith(t, commitSpec{msg: "m", files: map[string]string{"a.txt": "a\n"}})
	t.Setenv("GIT_AUTHOR_NAME", "S3CRET-VALUE")
	testutil.Git(t, repo.Dir, "commit", "--quiet", "--amend", "--reset-author", "--no-edit")
	if res, err := repo.ScanWork(ctx, "origin/main", hasSecret); err != nil || !res.Hit {
		t.Fatalf("ScanWork = %+v, %v; want a hit in the author", res, err)
	}
}

func TestScanWorkTooLargeIsNotBinary(t *testing.T) {
	repo := branchWith(t, commitSpec{msg: "m", files: map[string]string{"a.txt": strings.Repeat("a", 4096)}})
	defer func(c int) { scanChunk = c }(scanChunk)
	scanChunk = 512
	// Reads of 512 bytes against a cap of 1 byte: stops after the first.
	res, err := repo.scanWork(ctx, "origin/main", "HEAD", func(string) bool { return false }, 1)
	if err != nil || !res.TooLarge || res.Unscannable {
		t.Fatalf("scanWork = %+v, %v", res, err)
	}
}

// diff.external and a textconv in the checkout's config, which the agent
// controls, never run for anything the runner does.
func TestHostileDiffConfigNeverRuns(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	repo := branchWith(t, commitSpec{msg: "w", files: map[string]string{
		".gitattributes": "*.txt diff=evil\n", "a.txt": "a\n", ".github/workflows/ci.yml": "x\n"}})
	hook := "touch " + marker
	testutil.Git(t, repo.Dir, "config", "diff.external", hook)
	testutil.Git(t, repo.Dir, "config", "diff.evil.textconv", hook)
	testutil.Git(t, repo.Dir, "config", "diff.evil.command", hook)
	if _, err := repo.WorkflowFiles(ctx, "origin/main"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ScanWork(ctx, "origin/main", hasSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Bundle(ctx, "origin/main"); err != nil {
		t.Fatal(err)
	}
	if exists(marker) {
		t.Fatal("a configured diff program ran")
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestScanRangeStopsAtUntil(t *testing.T) {
	repo := branchWith(t,
		commitSpec{msg: "clean", files: map[string]string{"a.txt": "a\n"}},
		commitSpec{msg: "leak", files: map[string]string{"k.txt": "S3CRET-VALUE\n"}},
	)
	clean := testutil.Git(t, repo.Dir, "rev-parse", "HEAD~1")
	if res, err := repo.ScanRange(ctx, "origin/main", clean, hasSecret); err != nil || res.Hit {
		t.Fatalf("up to the clean commit: %+v, %v; want no hit", res, err)
	}
	head := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	if res, err := repo.ScanRange(ctx, "origin/main", head, hasSecret); err != nil || !res.Hit {
		t.Fatalf("up to HEAD: %+v, %v; want a hit", res, err)
	}
}

func TestWorkflowFilesInStopsAtUntil(t *testing.T) {
	repo := branchWith(t,
		commitSpec{msg: "code", files: map[string]string{"a.txt": "a\n"}},
		commitSpec{msg: "ci", files: map[string]string{".github/workflows/ci.yml": "on: push\n"}},
	)
	before := testutil.Git(t, repo.Dir, "rev-parse", "HEAD~1")
	if got, err := repo.WorkflowFilesIn(ctx, "origin/main", before); err != nil || len(got) != 0 {
		t.Fatalf("before the CI commit: %v, %v", got, err)
	}
	head := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	if got, err := repo.WorkflowFilesIn(ctx, "origin/main", head); err != nil || len(got) != 1 || got[0] != ".github/workflows/ci.yml" {
		t.Fatalf("at HEAD: %v, %v", got, err)
	}
}

func TestScanRangeScansEveryCommit(t *testing.T) {
	repo := branchWith(t,
		commitSpec{msg: "leak", files: map[string]string{"k.txt": "S3CRET-VALUE\n"}},
		commitSpec{msg: "clean", files: map[string]string{"a.txt": "a\n"}},
	)
	tip := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	if res, err := repo.ScanWork(ctx, "HEAD^", hasSecret); err != nil || res.Hit {
		t.Fatalf("the tip commit alone: %+v, %v; want it clean", res, err)
	}
	if res, err := repo.ScanRange(ctx, "origin/main", tip, hasSecret); err != nil || !res.Hit {
		t.Fatalf("a secret in an earlier commit of the range: %+v, %v; want a hit", res, err)
	}
}

func TestWorkflowFilesInSeesEveryCommit(t *testing.T) {
	repo := branchWith(t,
		commitSpec{msg: "ci", files: map[string]string{".github/workflows/ci.yml": "on: push\n"}},
		commitSpec{msg: "code", files: map[string]string{"a.txt": "a\n"}},
	)
	tip := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	if got, err := repo.WorkflowFilesIn(ctx, "origin/main", tip); err != nil || len(got) != 1 {
		t.Fatalf("a workflow file in an earlier commit: %v, %v", got, err)
	}
}

func TestRangeBoundsMustBeSafe(t *testing.T) {
	repo := branchWith(t, commitSpec{msg: "leak", files: map[string]string{"k.txt": "S3CRET-VALUE\n"}})
	tip := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	out := filepath.Join(t.TempDir(), "out")
	badUntil := []string{"HEAD", "fugaro/x", tip[:12], "+" + tip, "-" + tip[1:], "--output=" + out, "", strings.ToUpper(tip), tip + "\n", tip + " "}
	badSince := []string{"--output=" + out, "-x", "", "origin/main..HEAD", "a b", "HEAD~1", "main^", "x:y", "/abs", "a\nb"}
	for _, u := range badUntil {
		if _, err := repo.ScanRange(ctx, "origin/main", u, hasSecret); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("ScanRange until %q: err = %v, want a refusal", u, err)
		}
		if _, err := repo.WorkflowFilesIn(ctx, "origin/main", u); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("WorkflowFilesIn until %q: err = %v, want a refusal", u, err)
		}
	}
	for _, s := range badSince {
		if _, err := repo.ScanRange(ctx, s, tip, hasSecret); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("ScanRange since %q: err = %v, want a refusal", s, err)
		}
		if _, err := repo.WorkflowFilesIn(ctx, s, tip); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("WorkflowFilesIn since %q: err = %v, want a refusal", s, err)
		}
	}
	if exists(out) {
		t.Fatal("git wrote a file named by a range bound")
	}
	// A full SHA and a plain ref both work as since.
	base := testutil.Git(t, repo.Dir, "rev-parse", "origin/main")
	for _, s := range []string{"origin/main", base} {
		if res, err := repo.ScanRange(ctx, s, tip, hasSecret); err != nil || !res.Hit {
			t.Errorf("ScanRange since %q: %+v, %v", s, res, err)
		}
	}
}

func TestWorkflowFilesSinceAFollowUpStart(t *testing.T) {
	repo := branchWith(t,
		commitSpec{msg: "person", files: map[string]string{".github/workflows/p.yml": "p\n"}},
		commitSpec{msg: "run", files: map[string]string{"a.txt": "a\n"}},
	)
	start := testutil.Git(t, repo.Dir, "rev-parse", "HEAD~1")
	if got, err := repo.WorkflowFiles(ctx, start); err != nil || len(got) != 0 {
		t.Fatalf("since the start: %v, %v", got, err)
	}
	if got, _ := repo.WorkflowFiles(ctx, "origin/main"); len(got) != 1 {
		t.Fatalf("since the base: %v", got)
	}
}

// Every commit the push adds is read, whatever the agent does to hide one,
// and whatever its own git configuration says: GitHub refuses a push for a
// workflow change in any commit it adds, not only in the tip's tree. The
// checkout's config and refs are the agent's, so each case is a way it
// could make git leave a commit or a path out.
func TestWorkflowFilesSeesWhatTheAgentHides(t *testing.T) {
	const ci = ".github/workflows/ci.yml"
	sh := func(t *testing.T, repo *Repo, script string) {
		t.Helper()
		cmd := exec.Command("sh", "-c", script)
		cmd.Dir, cmd.Env = repo.Dir, repo.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", script, err, out)
		}
	}
	cases := []struct {
		name, script, want string
	}{
		{"mode only", "git update-index --chmod=-x " + ci + " && git commit -qm 'chmod'", ci},
		{"changed, then reverted", "echo evil >> " + ci + " && git commit -qam w && git revert --no-edit HEAD >/dev/null", ci},
		{"behind a merge that keeps one side", "git checkout -qb side && echo evil >> " + ci + " && git commit -qam w && " +
			"git checkout -q fugaro/x && git merge -q -s ours side -m m", ci},
		{"only in a merge's own tree", "git checkout -qb side && git commit -q --allow-empty -m s && git checkout -q fugaro/x && " +
			"git commit -q --allow-empty -m mine && git merge -q --no-ff --no-commit side && echo x > .github/workflows/evil.yml && " +
			"git add -A && git commit -qm m", ".github/workflows/evil.yml"},
		{"in a root commit, with log.showRoot off", "git config log.showRoot false && echo x > .github/workflows/evil.yml && git add -A && " +
			"git reset -q --hard $(git commit-tree $(git write-tree) -m root)", ".github/workflows/evil.yml"},
		{"under a replace ref", "echo evil >> " + ci + " && git commit -qam w && " +
			"git replace HEAD $(git commit-tree HEAD~1^{tree} -p HEAD~1 -m fake)", ci},
		{"a gitlink, with diff.ignoreSubmodules all", "git config diff.ignoreSubmodules all && git rm -rq --cached .github/workflows && " +
			"git update-index --add --cacheinfo 160000,1111111111111111111111111111111111111111,.github/workflows && git commit -qm g", workflowRoot},
		{"a case variant of the directory", "git update-index --add --cacheinfo 100644,$(git hash-object -w " + ci + "),.GitHub/Workflows/x.yml && " +
			"git commit -qm c", ".GitHub/Workflows/x.yml"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, _ := setup(t)
			if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
				t.Fatal(err)
			}
			testutil.WriteFiles(t, repo.Dir, map[string]string{ci: "on: push\n"})
			if _, err := repo.CommitAll(ctx, "a person's CI"); err != nil {
				t.Fatal(err)
			}
			base, err := repo.HeadSHA(ctx)
			if err != nil {
				t.Fatal(err)
			}
			sh(t, repo, c.script)
			head, err := repo.HeadSHA(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for name, list := range map[string]func() ([]string, error){
				"WorkflowFiles":         func() ([]string, error) { return repo.WorkflowFiles(ctx, base) },
				"WorkflowFilesIn":       func() ([]string, error) { return repo.WorkflowFilesIn(ctx, base, head) },
				"WorkflowFilesExcept":   func() ([]string, error) { return repo.WorkflowFilesExcept(ctx, base, base) },
				"WorkflowFilesInExcept": func() ([]string, error) { return repo.WorkflowFilesInExcept(ctx, base, base, head) },
			} {
				got, err := list()
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if !slices.Contains(got, c.want) {
					t.Errorf("%s = %v, want it to name %s", name, got, c.want)
				}
			}
		})
	}
}
