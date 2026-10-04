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
	if got, err := repo.WorkflowFiles(ctx, "main"); err != nil || len(got) != 0 {
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
	got, err := repo.WorkflowFiles(ctx, "main")
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
	data, err := repo.Bundle(ctx, "main")
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
	if _, err := repo.BundleMax(ctx, "main", 100); !errors.Is(err, ErrBundleTooLarge) {
		t.Fatalf("BundleMax = %v, want ErrBundleTooLarge", err)
	}
}
