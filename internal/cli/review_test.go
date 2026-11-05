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

// wantReviewRefused runs the review command with args and wants exit 1 whose
// error carries every want, and no execution started by it: a refusal is a
// launch that did not happen, not one whose job failed.
func wantReviewRefused(t *testing.T, f *cloudFixture, args []string, wants ...string) error {
	t.Helper()
	before := len(f.run.Executions())
	out, errOut, err := reviewCLI(t, args...)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("review %v: out %q err %q, err = %v; want a refusal (exit 1)", args, out, errOut, err)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("review %v refused with %q, want it to say %q", args, err.Error(), want)
		}
	}
	if after := len(f.run.Executions()); after != before {
		t.Fatalf("review %v refused and still started %d executions", args, after-before)
	}
	return err
}

// TestReviewRefusesAnAccountTheRepoMayNotUse is the F4 allow-list rule the
// other launches already carry (claudeAccountMembership), reused here: --token
// names an account the repository's claude_accounts does not list, so the
// review is refused before anything is written. A review is launched by hand
// more often than most runs, which is exactly why the rule is not relaxed for
// it: --token here is the operator naming the credential a job will run on.
func TestReviewRefusesAnAccountTheRepoMayNotUse(t *testing.T) {
	f := newReviewFixture(t)
	admitReviewJob(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	err := wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-stranger"}, "is not one acme/app may use")
	if !strings.Contains(err.Error(), "acme-stranger") {
		t.Errorf("the refusal does not name the account asked for: %q", err.Error())
	}
}

// TestReviewRefusesAnExhaustedAccount is D882: an account the credentials
// service has marked holds no review at all — the launch refuses with the
// reset time the upstream reported, rather than starting a job whose reviewer
// is refused a minute later and whose run is then retried by hand.
func TestReviewRefusesAnExhaustedAccount(t *testing.T) {
	f := newReviewFixture(t)
	admitReviewJob(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
	until := time.Now().UTC().Truncate(time.Second).Add(3 * time.Hour)
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "exhausted", "until": until.Format(time.RFC3339Nano)})

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"},
		"the Claude account acme-personal is exhausted until "+until.Format("2006-01-02 15:04Z"),
		"no review is started on it")
}

// TestReviewRefusesWhileTheCodingRunIsGoing is spec §5's moving-head refusal:
// the review records the head it read, so a pull request whose own coding run
// is still going is refused rather than reviewed against a commit that can move
// under it.
func TestReviewRefusesWhileTheCodingRunIsGoing(t *testing.T) {
	f := newReviewFixture(t)
	admitReviewJob(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
	seedRoot(t, f.cloudFixture, rootID, time.Now().Add(-time.Hour))
	active := wallRunIDAt(0, "000100", "bbbb")
	exec := seedSpec(t, f.cloudFixture, followUpSpec(active, rootID, rootID, 7), true)
	f.run.SetState(exec, backend.StateRunning)
	writeRecord(t, f.cloudFixture, active, &runstore.Record{Version: 1, RunID: active, Repo: "acme/app",
		Execution: exec, Status: runstore.StatusRunning, Stage: "implement", Branch: "fugaro/" + rootID,
		PR: &runstore.PRRef{Number: 7}, StartedAt: time.Now(),
		FollowUp: &runstore.FollowUp{PR: 7, PreviousRun: rootID}})

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"},
		active, "is still running", "fugaro runs ls --pr 7 --repo acme/app")
}

// TestReviewShowPrintsTheArtifactAndItsMarkdown is the second verb: what the
// review job wrote, read back as it is — the artifact's own bytes, and beside
// them the same review as markdown. --json prints the artifact alone, because
// its reader checks the sidecar signature over exactly those bytes.
func TestReviewShowPrintsTheArtifactAndItsMarkdown(t *testing.T) {
	f := newCloudFixture(t)
	id := wallRunIDAt(0, "120000", "5eed")
	artifact := `{"version":1,"pr":7,"verdict":"comment"}`
	markdown := "# Review of PR #7\n\nNothing blocking.\n"
	putBuildObject(t, f, "runs/"+appSlug+"/"+id+"/"+runstore.ArtifactPrefix+runner.ReviewArtifactName, []byte(artifact))
	putBuildObject(t, f, "runs/"+appSlug+"/"+id+"/"+runstore.ArtifactPrefix+runner.ReviewMarkdownName, []byte(markdown))
	ref := appSlug + "/" + id

	out, errOut, err := reviewCLI(t, "show", ref)
	if err != nil {
		t.Fatalf("review show %s: %v (%s)", ref, err, errOut)
	}
	if !strings.Contains(out, artifact) || !strings.Contains(out, markdown) {
		t.Errorf("review show printed %q, want the artifact and its markdown", out)
	}

	asJSON, _, err := reviewCLI(t, "show", ref, "--json")
	if err != nil {
		t.Fatalf("review show --json %s: %v", ref, err)
	}
	if asJSON != artifact+"\n" {
		t.Errorf("review show --json printed %q, want the artifact's own bytes alone (%q)", asJSON, artifact+"\n")
	}

	// A run that has no artifact — a review still going, or a run that is not
	// a review at all — says so instead of printing an empty review.
	empty := wallRunIDAt(0, "120100", "6eed")
	seedSpec(t, f, firstRunSpec(empty), false)
	wantReviewRefused(t, f, []string{"show", appSlug + "/" + empty}, "has no review artifact")
}

// TestReviewRefusesWhereTheCredentialsServiceIsOff is E7's first half: the
// review-only job mounts nothing, so its read-only installation token and its
// account's model calls both come from the installation's credentials service,
// and an installation without one has nothing for the job to fetch anything
// from. The refusal names both commands that fix it, in order: the first is a
// fugaro update, which is the whole upgrade.
func TestReviewRefusesWhereTheCredentialsServiceIsOff(t *testing.T) {
	f := newProjectJobServingFixture(t)
	setBaseImage(t, reviewJobSince)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"},
		"credentials_service is not published in this project's config",
		"fugaro update --provision-credentials",
		"fugaro creds set github-app-key")
}

// TestReviewRefusesAJobTheCredentialsServiceWillNotAdmit is E7's second half,
// the one no operator command can supply: the deployed service admits a
// repository's own job, and admitting a review caller of a project-scoped job
// is C3.9, which is not built. The refusal has to say so — a sentence that
// named a fugaro update would send the operator looking for one that does not
// exist — and point at the verb that works today.
func TestReviewRefusesAJobTheCredentialsServiceWillNotAdmit(t *testing.T) {
	f := newReviewFixture(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"},
		"does not admit the review job's own caller", "No fugaro update supplies this half",
		"fugaro run --pr N --recipe review-only")
}
