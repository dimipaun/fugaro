package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// reviewCLI runs the review command through a real root, so the persistent
// setup, the flags and the cloud open are the tree's own. The command family's
// one registration line lives in internal/cli/root.go, which this task's brief
// leaves alone (see the PR body), so the test adds it here rather than relying
// on a root that would not resolve `fugaro review` yet.
func reviewCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd()
	root.AddCommand(newReviewCmd())
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(NormalizeArgs(append([]string{"review"}, args...)))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

// newReviewFixture is the installation a review is launched against: the
// project whose standard profile serves acme-personal and the default account,
// with both accounts' review-only variants deployed beside their coding jobs,
// the base image at the release the variant ships in (reviewJobSince, which
// every review launch is gated on), and the credentials service opted in.
//
// E7's second half — the service admitting a project-scoped caller — is not a
// fact of this tree at all, so the tests that have to reach the launch set it
// themselves (reviewJobAdmitted).
func newReviewFixture(t *testing.T) *projectJobFixture {
	t.Helper()
	f := newProjectJobServingFixture(t)
	setBaseImage(t, reviewJobSince)
	f.appendConfig(t, "credentials_service: { url: "+credentialsServiceFixtureURL+" }\n")
	for _, account := range []string{"acme-personal", ""} {
		job := credsvc.ReviewJobName("standard", "github", infra.ReviewVariantAccount(account))
		f.run.AddProjectJob(job, "standard", "github", infra.ReviewVariantAccount(account), "2", "4Gi")
		f.run.SetJobTimeout(job, projectJobTimeout+backend.TaskTimeoutSlack)
	}
	return f
}

// setBaseImage rewrites the fixture's base image tag, which is what a
// checkout: clone workflow's launches are judged on (checkCloneImageSince).
func setBaseImage(t *testing.T, tag string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := "fugaro-base:" + sealSince
	if !strings.Contains(string(data), old) {
		t.Fatalf("the fixture's local config no longer names the base image %q:\n%s", old, data)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, "fugaro-base:"+tag, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// admitReviewJob is E7's second half, set by the tests that have to reach a
// launch and restored after each: on this tree the deployed service carries no
// review job accounts, so a review launch is refused (reviewJobAdmitted).
func admitReviewJob(t *testing.T) {
	t.Helper()
	reviewJobAdmitted = true
	t.Cleanup(func() { reviewJobAdmitted = false })
}

// storedRunRef is the "<slug>/<run-id>" of the run the fixture's bucket holds:
// the one run a review launch writes.
func storedRunRef(t *testing.T, f *cloudFixture) string {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	slugs, err := runstore.ListSlugs(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range slugs {
		ids, err := runstore.ListRunIDs(ctx, b, slug, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) > 0 {
			return slug + "/" + ids[0]
		}
	}
	t.Fatal("the launch wrote no run to the bucket")
	return ""
}

// storedTask is the task.json the launcher wrote for one run.
func reviewTaskSpec(t *testing.T, f *cloudFixture, slug, id string) *task.Spec {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec, err := runstore.Open(b, slug, id).ReadTask(ctx)
	if err != nil {
		t.Fatalf("reading the stored task of %s/%s: %v", slug, id, err)
	}
	return spec
}

// TestReviewLaunchesOnTheNamedAccount is the command's core: `fugaro review
// --pr N --token NAME` writes one task that names the pull request and the
// account, and starts it on that account's REVIEW-ONLY job — the variant
// beside the shared project job, which is the whole point of the command (the
// reviewer left the coder's container, its mounts and its App key; CSR7), and
// never the shared coding job itself.
func TestReviewLaunchesOnTheNamedAccount(t *testing.T) {
	f := newReviewFixture(t)
	admitReviewJob(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	if _, _, err := reviewCLI(t, "--pr", "7", "--token", "acme-personal"); err != nil {
		t.Fatalf("a review of PR #7 on acme-personal: %v", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 {
		t.Fatalf("the launch asked Cloud Run for %d executions, want 1", len(calls))
	}
	if want := credsvc.ReviewJobName("standard", "github", "acme-personal"); calls[0].Job != want {
		t.Errorf("the review ran the job %q, want the review-only variant %q", calls[0].Job, want)
	}
	if coding := gcp.ProjectJobName("standard", "github", "acme-personal"); calls[0].Job == coding {
		t.Errorf("the review ran the shared coding job %q: a review is never the coder's own job", coding)
	}
	if got := calls[0].Env[runner.EnvReviewJob]; got != "1" {
		t.Errorf("%s = %q on the review's execution, want \"1\": the marker is what tells the run it is the review job", runner.EnvReviewJob, got)
	}
	slug, id, err := parseRunRef(storedRunRef(t, f.cloudFixture))
	if err != nil {
		t.Fatal(err)
	}
	spec := reviewTaskSpec(t, f.cloudFixture, slug, id)
	switch {
	case spec.Mode != recipe.ModeReview:
		t.Errorf("the stored task's mode is %q, want %q", spec.Mode, recipe.ModeReview)
	case len(spec.ReviewPRs) != 1 || spec.ReviewPRs[0] != 7:
		t.Errorf("the stored task reviews %v, want [7]", spec.ReviewPRs)
	case spec.OAuthToken != "acme-personal":
		t.Errorf("the stored task's account is %q, want acme-personal", spec.OAuthToken)
	}
}
