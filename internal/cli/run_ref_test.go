package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// refCheckout makes the working directory a checkout of acme/app on branch,
// one commit deep, with origin's remote-tracking refs for each of onOrigin.
func refCheckout(t *testing.T, branch string, onOrigin ...string) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	testutil.WriteFiles(t, dir, map[string]string{"README.md": "app\n"})
	testutil.Git(t, dir, "add", "README.md")
	testutil.Git(t, dir, "commit", "-q", "-m", "init")
	for _, b := range onOrigin {
		testutil.Git(t, dir, "update-ref", "refs/remotes/origin/"+b, "HEAD")
	}
	if branch != "main" {
		testutil.Git(t, dir, "checkout", "-q", "-b", branch)
	}
	t.Chdir(dir)
}

func launchedRef(t *testing.T, f *cloudFixture, runID string) string {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec, err := runstore.Open(b, appSlug, runID).ReadTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return spec.Ref
}

// A launch from a checkout on a pushed non-default branch, without --ref,
// is cut from that branch, says so on stderr, and names the base it uses
// on the launch confirmation: the run's review then measures the run's own
// work, not the branch's whole divergence from main.
func TestRunRefDefaultsToCheckoutBranch(t *testing.T) {
	f := newCloudFixture(t)
	refCheckout(t, "integration-x", "main", "integration-x")
	out, errOut, err := execute(t, "run", "--run-id", "20260927-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if got := launchedRef(t, f, "20260927-100000-abcd"); got != "integration-x" {
		t.Fatalf("ref = %q, want integration-x", got)
	}
	if want := "using --ref integration-x: this checkout is on integration-x; pass --ref main to run from the default branch"; !strings.Contains(errOut, want) {
		t.Fatalf("stderr:\n%s\nwant %q", errOut, want)
	}
	if want := "\n  branch fugaro/20260927-100000-abcd cut from integration-x\n"; !strings.Contains(out, want) {
		t.Fatalf("output:\n%s\nwant %q", out, want)
	}
}

func TestRunRefExplicitWins(t *testing.T) {
	f := newCloudFixture(t)
	refCheckout(t, "integration-x", "main", "integration-x")
	_, errOut, err := execute(t, "run", "--run-id", "20260927-100000-abcd", "--ref", "main", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if got := launchedRef(t, f, "20260927-100000-abcd"); got != "main" {
		t.Fatalf("ref = %q, want main", got)
	}
	if strings.Contains(errOut, "using --ref") {
		t.Fatalf("stderr names a defaulted ref with --ref given:\n%s", errOut)
	}
}

func TestRunRefDefaultBranchCheckoutUnchanged(t *testing.T) {
	f := newCloudFixture(t)
	refCheckout(t, "main", "main")
	_, errOut, err := execute(t, "run", "--run-id", "20260927-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if got := launchedRef(t, f, "20260927-100000-abcd"); got != "main" {
		t.Fatalf("ref = %q, want main", got)
	}
	if strings.Contains(errOut, "using --ref") {
		t.Fatalf("stderr:\n%s", errOut)
	}
}

// A branch origin does not have (no remote-tracking ref in the checkout)
// is never the default: the job could not fetch it.
func TestRunRefUnpushedBranchNotUsed(t *testing.T) {
	f := newCloudFixture(t)
	refCheckout(t, "local-only", "main")
	_, errOut, err := execute(t, "run", "--run-id", "20260927-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if got := launchedRef(t, f, "20260927-100000-abcd"); got != "main" {
		t.Fatalf("ref = %q, want main", got)
	}
	if strings.Contains(errOut, "using --ref") {
		t.Fatalf("stderr:\n%s", errOut)
	}
}

// A checkout of another repository says nothing about the launched one's
// base: --repo acme/app from a checkout of acme/other keeps main.
func TestRunRefOtherRepositoryCheckoutNotUsed(t *testing.T) {
	f := newCloudFixture(t)
	refCheckout(t, "integration-x", "main", "integration-x")
	testutil.Git(t, ".", "remote", "set-url", "origin", "git@github.com:acme/other.git")
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	if got := launchedRef(t, f, "20260927-100000-abcd"); got != "main" {
		t.Fatalf("ref = %q, want main", got)
	}
}

func TestWarnTaskCutsBranch(t *testing.T) {
	for _, tc := range []struct {
		text, ref string
		warn      bool
	}{
		{"Run `git checkout -b work origin/rigor-modes` and fix the test.", "main", true},
		{"git switch -c work origin/rigor-modes", "main", true},
		{"git checkout -b work origin/rigor-modes", "rigor-modes", false},
		{"Compare with origin/rigor-modes before you start.", "main", false},
		{"git diff origin/main...HEAD", "main", false},
	} {
		var b bytes.Buffer
		warnTaskCutsBranch(&b, tc.text, tc.ref)
		if got := b.Len() > 0; got != tc.warn {
			t.Errorf("%q at %s: warned = %v (%q), want %v", tc.text, tc.ref, got, b.String(), tc.warn)
		}
		if tc.warn && !strings.Contains(b.String(), "--ref rigor-modes") {
			t.Errorf("%q: warning %q does not name --ref rigor-modes", tc.text, b.String())
		}
	}
}
