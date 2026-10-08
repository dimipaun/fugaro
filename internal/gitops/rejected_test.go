package gitops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
