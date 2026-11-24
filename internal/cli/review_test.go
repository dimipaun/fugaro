package cli

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// reviewCLI runs the review command through a real root, so the persistent
// setup, the flags and the cloud open are the tree's own — including the
// registration, which is the core root's (root.go's AddCommand list) and not
// this file's.
func reviewCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd()
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
// the base image at the release whose runner can serve a review launch
// (reviewJobSince's gate), the credentials service opted in, and the
// installation's own tfvars naming that service at the release that admits the
// review job's own caller (reviewAdmissionSince, the later of the two releases
// a review launch's two halves are gated on).
//
// E7's two halves hold in it — the service is published, and the launcher's
// constant admits the class since 0.21.0 — so a launch from it reaches the
// review job unless a test says otherwise. It also has a state home of its own
// (isolateState), because the installation's tfvars live under it.
func newReviewFixture(t *testing.T) *projectJobFixture {
	t.Helper()
	return newReviewFixtureAt(t, reviewAdmissionSince)
}

// newReviewFixtureAt is the same installation with the one record
// checkReviewAdmission judges moved: service is the release the installation's
// own tfvars name for its credentials service image. The base image stays where
// the admitted fixture put it, because the two are pinned by different runs and
// only this one is the gate's subject — an installation between `brew upgrade`
// and `fugaro update` has the older service with everything else current.
func newReviewFixtureAt(t *testing.T, service string) *projectJobFixture {
	t.Helper()
	f := newProjectJobServingFixture(t)
	// nodeRelease and not reviewAdmissionSince: the fixture publishes a policy,
	// so its installation's base image has to be one whose runner reads the
	// node's version (policyImageGate) — reviewAdmissionSince is older than the
	// release that introduced it, and an installation cannot hold a node its
	// own base image cannot read. The admission E7's half is about still holds:
	// it is a floor, and the node's own release is above it.
	setBaseImage(t, nodeRelease)
	f.appendConfig(t, "credentials_service: { url: "+credentialsServiceFixtureURL+" }\n")
	isolateState(t)
	writeInstallationServiceImage(t, os.Getenv("XDG_STATE_HOME"), initProject, service)
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
	old := "fugaro-base:" + nodeRelease
	if !strings.Contains(string(data), old) {
		t.Fatalf("the fixture's local config no longer names the base image %q:\n%s", old, data)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, "fugaro-base:"+tag, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// unadmitReviewJob is the test seam on E7's second half, and it is set the one
// way round that has a use now that the constant is true: a test that wants the
// refusal a binary older than the flip answers sets it to false and restores
// the tree's own value after itself.
func unadmitReviewJob(t *testing.T) {
	t.Helper()
	old := reviewJobAdmitted
	reviewJobAdmitted = false
	t.Cleanup(func() { reviewJobAdmitted = old })
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
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
	seedRoot(t, f.cloudFixture, rootID, time.Now().Add(-time.Hour))
	active := wallRunIDAt(0, "000100", "bbbb")
	exec := seedSpec(t, f.cloudFixture, followUpSpec(active, rootID, rootID, 7), true)
	f.run.SetState(exec, backend.StateRunning)
	writeRecord(t, f.cloudFixture, active, &runstore.Record{Version: 1, RunID: active, Repo: "acme/app",
		Execution: exec, Status: runstore.StatusRunning, Stage: "implement", Branch: "fugaro/" + rootID,
		PR: &runstore.PRRef{Number: 7}, StartedAt: time.Now(),
		FollowUp: &runstore.FollowUp{PR: 7, PreviousRun: rootID}})

	// The refusal names the run the way every human surface does, by its
	// ending (spec.md §1), so the sentence is pinned whole around it.
	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"},
		"run "+task.ShortRun(active)+" on PR #7 is still running", "fugaro runs ls --pr 7 --repo acme/app")
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

// TestReviewStillRefusesWhenTheCredentialsServiceIsOff is E7's first half, and
// it is the refusal the flip leaves standing: the review-only job mounts
// nothing, so its read-only installation token and its account's model calls
// both come from the installation's credentials service, and an installation
// without one has nothing for the job to fetch anything from. The refusal names
// both commands that fix it, in order: the first is a fugaro update, which is
// the whole upgrade. E7's second half holds here (reviewJobAdmitted is true
// since 0.21.0, and the fixture's installation is at that release), which is
// what makes this the missing half alone.
func TestReviewStillRefusesWhenTheCredentialsServiceIsOff(t *testing.T) {
	f := newProjectJobServingFixture(t)
	setBaseImage(t, nodeRelease)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"},
		"credentials_service is not published in this project's config",
		"fugaro update --firebase-project",
		"fugaro creds set github-app-key")
}

// TestReviewRefusesAnInstallationWhoseServicePredatesTheClass is the gate the
// flip of reviewJobAdmitted made load-bearing, read from the command: the
// review-only variant is deployed and its job image can serve a launch
// (reviewJobSince admits it), while the installation's own tfvars name a
// credentials service image from a release older than the class — so the launch
// would start, bill and die on the service's 401. The fixture is the admitted
// one with its service record moved back to a release the job gate admits,
// which is exactly the installation between `brew upgrade` and `fugaro update`
// the gate exists for; the same fixture at reviewAdmissionSince launches, which
// TestReviewLaunchIsAdmittedWhenTheConstantIsFlipped pins.
func TestReviewRefusesAnInstallationWhoseServicePredatesTheClass(t *testing.T) {
	f := newReviewFixtureAt(t, reviewJobSince)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"},
		"only from fugaro "+reviewAdmissionSince+" on",
		"Run fugaro update",
		"refused rather than started")
}

// TestReviewLaunchIsAdmittedWhenTheConstantIsFlipped is the release's own
// state, and CSR11's red step read the other way: reviewJobAdmitted has been
// true since 0.21.0, so a launch whose two halves hold reaches the review-only
// variant with no test seam and no environment variable anywhere in its path —
// and the same installation under a binary that predates the flip refuses,
// which is the whole of what the constant decides.
func TestReviewLaunchIsAdmittedWhenTheConstantIsFlipped(t *testing.T) {
	f := newReviewFixture(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	if _, errOut, err := reviewCLI(t, "--pr", "7", "--token", "acme-personal"); err != nil {
		t.Fatalf("a review on the release that admits the review class: %v (stderr %q)", err, errOut)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 {
		t.Fatalf("the admitted launch asked Cloud Run for %d executions, want 1", len(calls))
	}
	if want := credsvc.ReviewJobName("standard", "github", "acme-personal"); calls[0].Job != want {
		t.Errorf("the admitted launch ran the job %q, want the review-only variant %q", calls[0].Job, want)
	}
	slug, id, err := parseRunRef(storedRunRef(t, f.cloudFixture))
	if err != nil {
		t.Fatal(err)
	}
	if spec := reviewTaskSpec(t, f.cloudFixture, slug, id); spec.Mode != recipe.ModeReview || spec.OAuthToken != "acme-personal" {
		t.Errorf("the admitted launch stored the task %+v, want a review on acme-personal", spec)
	}

	t.Run("the same installation under a binary that predates the flip", func(t *testing.T) {
		f := newReviewFixture(t)
		projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
		unadmitReviewJob(t)

		wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"}, "predates the release")
	})
}

// TestReviewStillRefusesWhenTheConstantIsOff is the one state the sentence in
// review.go is reachable in: a binary built before 0.21.0 carries the admission
// as false, so it refuses the launch whatever the installation in front of it
// admits — and the sentence names the upgrade as the remedy rather than a
// repair of the installation, because the fact is the binary's. The seam is the
// only way there, and the bytes are pinned because they are the whole of what
// such a binary answers.
func TestReviewStillRefusesWhenTheConstantIsOff(t *testing.T) {
	f := newReviewFixture(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
	unadmitReviewJob(t)

	const refusal = "this fugaro predates the release in which the credentials service admits the review job's own caller: the review's account belongs to a project-scoped job, and whether the service admits that class is not a question this launcher can ask it (E7's second half) — it is a fact the binary carries, and this one was built while the answer was no, so it refuses the launch whatever the installation in front of it admits. Upgrade Fugaro (`brew upgrade dimipaun/tap/fugaro`), then run `fugaro update` with the newer binary: that is the whole upgrade, and the command that deploys the service's review class beside the review-only job variant. Until then, review a pull request in the run's own job with fugaro run --pr N --recipe review-only"
	before := len(f.run.Executions())
	out, errOut, err := reviewCLI(t, "--pr", "7", "--token", "acme-personal")
	if err == nil {
		t.Fatalf("a review launched by a binary that predates the flip (out %q err %q)", out, errOut)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a refusal (exit 1): %v", ExitCode(err), err)
	}
	if err.Error() != refusal {
		t.Errorf("the refusal of a pre-flip binary is %q, want the pinned sentence byte for byte: %q", err.Error(), refusal)
	}
	if after := len(f.run.Executions()); after != before {
		t.Errorf("the refusal started %d executions", after-before)
	}
}

// rewriteConfigLine rewrites one line of the fixture's local config in place:
// the repos and profiles lines are the ones these tests move, and a second
// top-level (or second repos) key would be a duplicate YAML key rather than an
// override.
func rewriteConfigLine(t *testing.T, from, to string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), from) {
		t.Fatalf("the fixture's local config no longer holds %q:\n%s", from, data)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), from, to, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// appRepoEntry is newCloudFixture's own line for acme/app, the one a test that
// moves a field of it rewrites.
const appRepoEntry = "  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"

// githubAppIDRecordedOn returns a repos entry of the local config with the App
// ID the implicit review-only job's github provider needs to start recorded on
// it: that record is what internal/infra's implicitReviewVariant gates the job's
// deployment on (recordedGitHubAppID).
func githubAppIDRecordedOn(entry string) string {
	return strings.Replace(entry, "provider: github", `provider: github, github_app_id: "123456789"`, 1)
}

// withGitHubAppID records that ID on acme/app, the repository the fixture's
// installation is about.
func withGitHubAppID(t *testing.T) {
	t.Helper()
	rewriteConfigLine(t, appRepoEntry, githubAppIDRecordedOn(appRepoEntry))
}

// bakedOAuthRepoYAML is projectJobOAuthRepoYAML without its checkout: line —
// a baked workflow, which resolves no shared project job and which a review
// reaches the implicit default review-only job from.
func bakedOAuthRepoYAML(t *testing.T) string {
	t.Helper()
	baked := strings.Replace(projectJobOAuthRepoYAML, "    checkout: clone\n", "", 1)
	if baked == projectJobOAuthRepoYAML {
		t.Fatal("the fixture's oauth checkout no longer carries a checkout: clone line to take out")
	}
	return baked
}

// newImplicitReviewFixture is the installation a review of a repository with no
// shared project job reaches the implicit default review-only job on: the
// admitted review fixture (its credentials service, its base image at the
// release that can serve a review, the installation's own tfvars naming the
// service at the release that admits the review class, and the implicit job's
// name among the review jobs the fake holds — as the standard profile's own
// no-account variant, which is the same job under the same name on an
// installation that declares that profile) plus the one fact a github shared
// job needs to be deployable at all.
func newImplicitReviewFixture(t *testing.T) *projectJobFixture {
	t.Helper()
	f := newReviewFixture(t)
	withGitHubAppID(t)
	return f
}

// TestReviewResolvesTheImplicitJobForABakedWorkflow is C1's piece 3, the
// owner's requirement of 2026-11-13 (option C): a repository whose workflow is
// baked resolves no shared project job (launchProjectJobFor), and its review
// runs on the installation's implicit default review-only job rather than being
// refused for a variant no installation deploys beside a per-repository job.
//
// The account is the implicit job's own: the launch names no --token, so the
// review is on the default one, which is the account that job serves.
//
// Mutation (run, restore): resolve the review onto the coding job it would have
// had, or keep the pre-piece-3 refusal, and this launch stops.
func TestReviewResolvesTheImplicitJobForABakedWorkflow(t *testing.T) {
	f := newImplicitReviewFixture(t)
	projectJobCheckoutYAML(t, bakedOAuthRepoYAML(t))

	if _, errOut, err := reviewCLI(t, "--pr", "7"); err != nil {
		t.Fatalf("a review of a baked workflow: %v (stderr %q)", err, errOut)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 {
		t.Fatalf("the launch asked Cloud Run for %d executions, want 1", len(calls))
	}
	if want := infra.ImplicitReviewJobName("github"); calls[0].Job != want {
		t.Errorf("the launch ran the job %q, want the installation's implicit default review-only job %q", calls[0].Job, want)
	}
	slug, id, err := parseRunRef(storedRunRef(t, f.cloudFixture))
	if err != nil {
		t.Fatal(err)
	}
	if spec := reviewTaskSpec(t, f.cloudFixture, slug, id); spec.Mode != recipe.ModeReview || spec.OAuthToken != "" {
		t.Errorf("the launch stored the task %+v, want a review on the default account", spec)
	}

	// The other side of the same boundary: a repository that does resolve a
	// shared project job keeps its profile-derived variant, and the implicit
	// job is not a second way onto a shared review — a review on a named
	// account runs on the variant of that account's coding job.
	t.Run("a repository that resolves a shared job keeps its profile-derived variant", func(t *testing.T) {
		f := newImplicitReviewFixture(t)
		projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

		if _, errOut, err := reviewCLI(t, "--pr", "7", "--token", "acme-personal"); err != nil {
			t.Fatalf("a review of a clone workflow: %v (stderr %q)", err, errOut)
		}
		calls := f.run.RunRequests()
		if len(calls) != 1 {
			t.Fatalf("the launch asked Cloud Run for %d executions, want 1", len(calls))
		}
		if want := credsvc.ReviewJobName("standard", "github", "acme-personal"); calls[0].Job != want {
			t.Errorf("the launch ran the job %q, want the profile-derived variant %q", calls[0].Job, want)
		}
	})
}

// TestReviewResolvesTheImplicitJobForAProjectWithNoProfiles is the other entry
// into the same resolution, and the state the owner's requirement is about: a
// project that has published no profile at all — its local config declares none
// and the published policy defines none — so a repository's clone workflow
// selects a profile no document defines and resolves no shared job either
// (launchProjectJobFor). Its review runs on the implicit default review-only
// job, which is the one job such a project has.
func TestReviewResolvesTheImplicitJobForAProjectWithNoProfiles(t *testing.T) {
	f := newImplicitReviewFixture(t)
	rewriteConfigLine(t, projectJobServingProfile, "")
	publishPolicy(t, f.launchBudgetFixture, `{"version":9,"allow_repos":["github/acme/*"]}`)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

	if _, errOut, err := reviewCLI(t, "--pr", "7"); err != nil {
		t.Fatalf("a review of a project with no profiles: %v (stderr %q)", err, errOut)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 {
		t.Fatalf("the launch asked Cloud Run for %d executions, want 1", len(calls))
	}
	if want := infra.ImplicitReviewJobName("github"); calls[0].Job != want {
		t.Errorf("the launch ran the job %q, want the installation's implicit default review-only job %q", calls[0].Job, want)
	}
}

// reviewSlug is the slug a review of repo on provider is stored and launched
// under, as reviewSpec derives it before the resolution is asked (repoSlug):
// the pair names the provider back (runner.ProviderForSlug), which is how the
// resolution reads the kind a review is on.
func reviewSlug(t *testing.T, provider, repo string) string {
	t.Helper()
	slug, err := task.Slug(provider, repo)
	if err != nil {
		t.Fatalf("task.Slug(%s, %s): %v", provider, repo, err)
	}
	return slug
}

// TestReviewRefusesAnImplicitJobTheInstallationDoesNotDeploy is the deployment
// half of the same resolution, and the half the mutation check of piece 3 turns
// on: the implicit job is deployed only on an installation that publishes the
// credentials service and records exactly one GitHub App ID (internal/infra's
// implicitReviewVariant), so a review of a repository with no shared project job
// is refused where the installation deploys none rather than started on a job
// Cloud Run would answer 404 for.
//
// The credentials-service half is asked of the resolution itself, and not only
// of the command: checkReviewService refuses an installation without the service
// before the job is ever resolved, so the command's own refusal cannot show that
// the resolution refuses too. Both stand here — the direct call for the
// resolution, the command for the sentence an operator sees.
//
// Mutation (run, restore): resolve the implicit job by its name rather than out
// of the deployment's own set — implicitReviewJob answering with the name, the
// profile, the account and the clock internal/infra derives, whatever its own
// computation says — and the direct call below resolves a job this installation
// does not have. Nothing narrower is observable here: internal/infra derives no
// such job without the credentials service or an App ID either, so it answers
// with an error or leaves the name out of the set, and a resolution that read no
// gate of its own would refuse all the same.
func TestReviewRefusesAnImplicitJobTheInstallationDoesNotDeploy(t *testing.T) {
	t.Run("the credentials service is not published", func(t *testing.T) {
		f := newProjectJobServingFixture(t)
		projectJobCheckoutYAML(t, bakedOAuthRepoYAML(t))

		wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7"},
			"credentials_service is not published in this project's config",
			"fugaro update --firebase-project",
			"fugaro creds set github-app-key")

		// The same installation, with the command's own check out of the way:
		// the resolution deploys no implicit job here, so it resolves none.
		spec := &task.Spec{Repo: "acme/app"}
		job, err := memEnv(t, f.cloudFixture).reviewJobFor(context.Background(), nil, spec, reviewSlug(t, "github", spec.Repo))
		if err == nil {
			t.Fatalf("the resolution returned the job %+v for an installation with no credentials service, want a refusal: the review container mounts no credential, so nothing would serve it one", job)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("exit %d, want a refusal (exit 1): %v", ExitCode(err), err)
		}
	})

	t.Run("no GitHub App ID is recorded", func(t *testing.T) {
		f := newReviewFixture(t)
		projectJobCheckoutYAML(t, bakedOAuthRepoYAML(t))

		wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7"},
			"this installation deploys no implicit review-only job",
			"fugaro update --repo <owner/name> --github-app-id <id>")
	})
}

// TestReviewRefusesAnImplicitJobForAnotherAccount is the third of the
// resolution's facts, and the one the credentials service decides: the implicit
// job serves the default account alone, and a review's job is the job the
// service pairs it by (a review container mounts nothing, and the service serves
// the account of the caller whose job row names that account). A review on
// another account has no implicit job to run on, and is refused here rather than
// started on a job that would refuse it its credential.
func TestReviewRefusesAnImplicitJobForAnotherAccount(t *testing.T) {
	f := newImplicitReviewFixture(t)
	env := memEnv(t, f.cloudFixture)

	job, err := env.reviewJobFor(context.Background(), nil, &task.Spec{Repo: "acme/app", OAuthToken: "acme-personal"}, reviewSlug(t, "github", "acme/app"))
	if err == nil {
		t.Fatalf("the resolution returned the job %+v for a review on acme-personal, want a refusal: the implicit job's row names the default account", job)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a refusal (exit 1): %v", ExitCode(err), err)
	}
	for _, want := range []string{"acme-personal", config.DefaultClaudeAccount, reviewOnlyRecipe} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestReviewRefusesASlugThatNamesAnotherRepository is the first of the
// resolution's facts and the one that decides which provider's job is asked for
// at all: the provider is derived from the slug the launch computed and the
// repository the task names (runner.ProviderForSlug), never from the config's
// optional provider key or from the job's name, so a slug and a task that name
// two different repositories have no provider to derive and no job to run on.
// Without the check the derivation falls through to the provider of the task's
// own repository, and a review is placed in the review job of a repository the
// run is not about — a job whose container reads a different checkout and
// reports a verdict on a pull request the task never named.
//
// The installation is the admitted one in every other respect (the credentials
// service is published, the App ID is recorded, the implicit github job is
// deployed), so the incoherence is the only thing this refusal is about.
//
// Mutation (run, restored): drop the ProviderForSlug arm and take the provider
// from the task's repository alone, and this call resolves the installation's
// implicit github job for a slug that names another repository.
func TestReviewRefusesASlugThatNamesAnotherRepository(t *testing.T) {
	f := newImplicitReviewFixture(t)
	env := memEnv(t, f.cloudFixture)

	// A slug the launcher's own derivation produces, for a repository other than
	// the one the task names.
	const otherRepo = "acme/other"
	spec := &task.Spec{Repo: "acme/app"}
	slug := reviewSlug(t, "github", otherRepo)
	job, err := env.reviewJobFor(context.Background(), nil, spec, slug)
	if err == nil {
		t.Fatalf("the resolution returned the job %+v for the slug of %s and a task naming %s, want a refusal: the job a review runs on is the review job of the repository its own task names", job, otherRepo, spec.Repo)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a refusal (exit 1): %v", ExitCode(err), err)
	}
	for _, want := range []string{slug, spec.Repo, "the slug and the task must name one repository"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestReviewRefusesAnImplicitJobWhenNoPolicyIsPublished is the fourth of the
// resolution's facts, and the run's own: a run places itself in the job it is in
// by the policy the project published (internal/runner's matchJobName), and a
// project job is one that policy defines — so with no policy the implicit job's
// name is a job the run cannot place at all, and the container halts at
// admission (cannotPlaceJob's absent case) after the claim, the budget token and
// the execution were paid for. The launch is refused here instead, in the words
// of the run's own refusal and with the fix it names, so the review has a way to
// run today.
//
// The installation is the admitted one in every other respect — the credentials
// service is published, the App ID is recorded, and the review is on the account
// the implicit job serves — so the absent policy is the only thing this refusal
// is about. The node is taken away after the fixture published it: the state an
// owner reaches by deleting the policy, with the rules marker it was published
// under still in place.
//
// Mutation (run, restore): drop the read and resolve as before, and this launch
// starts a job whose container stops on it.
func TestReviewRefusesAnImplicitJobWhenNoPolicyIsPublished(t *testing.T) {
	f := newImplicitReviewFixture(t)
	f.db.Set(projectpolicy.PathShared(), nil)
	projectJobCheckoutYAML(t, bakedOAuthRepoYAML(t))

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7"},
		"has published no policy",
		"fugaro project policy publish",
		reviewOnlyRecipe)
}

// TestReviewRefusesAnImplicitJobTheProfileDoesNotAdmit is the fifth of the
// resolution's facts. The name the deployment holds is the one the policy's own
// standard profile derives — a github profile named DefaultProfileName, which is
// what every project that adopts profiles has — so it is one entry of the
// policy's candidate set under the profile's triple rather than the
// installation's implicit entry: internal/runner's projectJobCandidates adds the
// implicit candidate only for a name no profile derives, and the run in that job
// is admitted by the profile's list rather than by the project's allow_repos
// (admissionCheck's two arms, which runner.ProjectJobIsImplicitReviewJob tells
// apart by name). A repository the project enrolled but the profile's own
// narrower list leaves out would otherwise reach Cloud Run, take the claim and
// mint the token, and stop only in the runner, so the profile's half of the
// admission is asked at the launch too — the launcher's twin of the run's own
// check (profileAdmissionRefusal).
//
// The two lists are the state this is about: the project admits github/acme/*,
// and the profile's own list names one repository, which a profile's list can do
// and can only narrow (projectpolicy.AdmissionRefusal words it).
//
// Mutation (run, restore): drop the profile half, and this launch starts the job
// the run refuses the repository in.
func TestReviewRefusesAnImplicitJobTheProfileDoesNotAdmit(t *testing.T) {
	// The fixture's own policy with one field moved: the standard profile's list
	// names acme/other rather than acme/app, and both accounts cover it, so the
	// document is one publish could write (a profile's list is exact and every
	// account has to cover it) and the project's own list still admits acme/app.
	const narrowed = `{"version":9,"allow_repos":["github/acme/*"],` +
		`"profiles":{"standard":{"provider":"github","cpu":2,"memory":"4Gi","timeout_class":"6h",` +
		`"claude_accounts":["acme-personal","default"],"allow_repos":["github/acme/other"]}},` +
		`"accounts":{"acme-personal":{"allow_repos":["github/acme/app","github/acme/other"]},` +
		`"default":{"allow_repos":["github/acme/app","github/acme/other"]}}}`

	f := newImplicitReviewFixture(t)
	publishPolicy(t, f.launchBudgetFixture, narrowed)
	projectJobCheckoutYAML(t, bakedOAuthRepoYAML(t))

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7"},
		`the job profile "standard" does not admit github/acme/app`,
		"its allow_repos is github/acme/other",
		"The launch stops before any execution exists")
}

// TestReviewRefusesABitbucketReviewTheConfigComposesNoJobFor is the reading the
// resolution must take its provider from, and the state that reading decides: a
// repository whose local config entry names no provider is on the kind its
// checkout's fugaro.yaml gives it (repoSlug), so a review of it is a Bitbucket
// review — and this config composes no Bitbucket job, because the deployer
// composes one only for a provider a repository of the installation records
// (internal/infra's recordedBitbucketRepo). The launch is refused with the
// composition sentence, naming the Bitbucket job and the gate its config failed.
//
// A resolution that read the provider out of the config's entry instead — the
// key is optional and this entry leaves it out — would read github and launch
// the review onto the github job the installation does have, which is the job a
// Bitbucket repository's review must never run on (its container mounts no
// Bitbucket token, and the credentials service is not asked for one for a job
// the repository is not placed in).
//
// Mutation (run, restored): read the provider from the local config's entry
// rather than from the slug and the task's repository, and this launch reaches
// Cloud Run on the github job.
func TestReviewRefusesABitbucketReviewTheConfigComposesNoJobFor(t *testing.T) {
	f := newReviewFixture(t)
	rewriteConfigLine(t, appRepoEntry, "  acme/app: { base_branch: main, workflows: [web] }\n")
	// The App ID the github implicit job is deployed for is recorded on the
	// fixture's other repository, which stays on github (recordedGitHubAppID):
	// the review below is refused for the job its own provider's config composes
	// none of, not for want of a github job to be refused onto.
	rewriteConfigLine(t, otherRepoEntry, githubAppIDRecordedOn(otherRepoEntry))
	publishPolicy(t, f.launchBudgetFixture, bitbucketReviewPolicy())
	bitbucketReviewCheckout(t, bitbucketReviewClaim, strings.Replace(projectJobRepoYAML, "provider: github", "provider: bitbucket", 1))

	err := wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7"},
		"bitbucket", infra.ImplicitReviewJobName("bitbucket"),
		"a repository of that provider is recorded",
		"fugaro run --pr N --recipe "+reviewOnlyRecipe)
	if strings.Contains(err.Error(), infra.ImplicitReviewJobName("github")) {
		t.Errorf("the refusal names the github job: %q", err.Error())
	}
}

// otherRepoEntry is the fixture's line for its second repository (addRepoToConfig,
// newProjectJobFixture): the github entry the App ID the implicit review-only job
// is deployed for is recorded on where a test's own repository names no provider.
const otherRepoEntry = "  acme/other: { provider: github, base_branch: main, workflows: [web] }\n"

// TestReviewReadsNoAdmissionSwitch is CSR12's expiry date kept: the switch
// FUGARO_REVIEW_SKIP_ADMISSION_CHECK existed for the owner's own C3.9 live
// check and for nothing else, and CSR11's commit deletes it, so the variable
// changes nothing anywhere — with the seam at false and the variable set to
// "1", the launch is refused exactly as with it unset, no warning line is
// printed, and no execution starts. The source half is checked beside the
// behaviour: the mutation this test exists for is re-adding the read, and
// either spelling of it — honoured again, or read and ignored — has to fail.
func TestReviewReadsNoAdmissionSwitch(t *testing.T) {
	const name = "FUGARO_REVIEW_SKIP_ADMISSION_CHECK"
	t.Setenv(name, "1")
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), name) {
			t.Errorf("%s names %s: the switch CSR12 added for the live check is deleted in the same commit as the flip, and a variable that outlives the gate it was built for is a hole with no reason left to be open", e.Name(), name)
		}
	}

	f := newReviewFixture(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
	unadmitReviewJob(t)

	before := len(f.run.Executions())
	out, errOut, err := reviewCLI(t, "--pr", "7", "--token", "acme-personal")
	if err == nil {
		t.Fatalf("%s set and the launch was admitted (out %q err %q): no code path reads the variable", name, out, errOut)
	}
	if !strings.Contains(err.Error(), "predates the release") {
		t.Errorf("the refusal with %s set is %q, want the one the constant gives with the variable unset", name, err.Error())
	}
	for _, line := range strings.Split(errOut, "\n") {
		if strings.HasPrefix(line, "warning:") {
			t.Errorf("a launch with %s set printed a warning line: %q", name, line)
		}
	}
	if after := len(f.run.Executions()); after != before {
		t.Errorf("%s admitted %d executions", name, after-before)
	}
}

// TestTheRemovedAdmissionSwitchSurvivesOnlyInThePlan: the switch's name is kept
// in exactly one place on this tree — the plan that defines it and records its
// removal — and in no live document, no website page and no plugin skill. The
// step that was reached with it (docs/c39-review-identity/live-check.md, step
// 3) reads as the plain launch now, and the generated CLI reference is
// regenerated from the command tree, which never held the variable: a sentence
// left behind would offer an operator a door this release has taken out.
func TestTheRemovedAdmissionSwitchSurvivesOnlyInThePlan(t *testing.T) {
	const name = "FUGARO_REVIEW_SKIP_ADMISSION_CHECK"
	const want = "docs/cloud-senior-review/implementation-plan.md"
	found := map[string]bool{}
	for _, root := range []string{"../../docs", "../../website", "../../plugin"} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == "node_modules" {
					return fs.SkipDir
				}
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), name) {
				rel, err := filepath.Rel("../..", p)
				if err != nil {
					return err
				}
				found[filepath.ToSlash(rel)] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	if !found[want] {
		t.Errorf("%s no longer records %s: the plan is where the switch's definition and its removal are written, and a reader of the flip has to be able to find both", want, name)
	}
	delete(found, want)
	for path := range found {
		t.Errorf("%s names %s: the variable was removed by the flip, and a reference row, a settings surface, a live check or a skill sentence naming it would advertise a door that no longer exists", path, name)
	}
}

// clearAgentMarkers empties every marker of agentenv.Markers in this test's
// environment. Whether the machine running the tests is a coding agent's
// session is not what the tests that call it are about.
func clearAgentMarkers(t *testing.T) {
	t.Helper()
	for _, name := range agentenv.Markers {
		t.Setenv(name, "")
	}
}

// fixBriefVerify is the credentials service's verify route (C3.9f) as the
// launcher's own client meets it: one key made at run time, the frame the
// service builds for itself (v1\n<repository>\n<run>\n<digest>, spelled here
// because this stands in for the service's own code — the launcher's client
// never builds one), and every call it was sent, recorded so a test can read
// the digest and the account the launcher actually asked about.
type fixBriefVerify struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []credsvc.VerifyRequest
}

// fixBriefKey is the stand-in service's key, made once for the package: the
// verifier seam below is package-wide, so a sidecar one test wrote has to be
// verifiable in the next test's read of it.
var fixBriefKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return key
}()

// fixBriefSign is the signature the stand-in service makes over one artifact's
// bytes: HMAC-SHA-256 under its key, over the frame the digest is the last
// field of.
func fixBriefSign(repository, run string, data []byte) string {
	sum := sha256.Sum256(data)
	mac := hmac.New(sha256.New, fixBriefKey)
	mac.Write([]byte("v1\n" + repository + "\n" + run + "\n" + hex.EncodeToString(sum[:])))
	return hex.EncodeToString(mac.Sum(nil))
}

// fixBriefVerifyStart is the package's one stand-in verify service, started on
// first use: the seam is package-wide, so one server has to answer every test
// that reads an artifact another test's fixture signed.
var fixBriefVerifyStart = sync.OnceValue(func() *fixBriefVerify {
	v := &fixBriefVerify{}
	v.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req credsvc.VerifyRequest
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		v.mu.Lock()
		v.calls = append(v.calls, req)
		v.mu.Unlock()
		mac := hmac.New(sha256.New, fixBriefKey)
		mac.Write([]byte("v1\n" + req.Repository + "\n" + req.Run + "\n" + req.Digest))
		_ = json.NewEncoder(w).Encode(map[string]bool{
			"valid": hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(req.Signature)),
		})
	}))
	return v
})

func (v *fixBriefVerify) recorded() []credsvc.VerifyRequest {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]credsvc.VerifyRequest(nil), v.calls...)
}

// setFixBriefVerifierAt points the launcher's verifier at one URL. It is the
// seam's single assignment — one write and one t.Cleanup, so a test never has
// to overwrite a seam another helper set (the shape in which a cleanup order,
// or a test that started in between, would leave the wrong verifier in place).
func setFixBriefVerifierAt(t *testing.T, url string) {
	t.Helper()
	prev := reviewVerifierSeam
	reviewVerifierSeam = func(_ context.Context, account, run, repository string) (runner.SignatureVerifier, error) {
		return runner.ServiceVerifier{
			URL:     url,
			IDToken: func(context.Context) (string, error) { return "verifier-id-token", nil },
			Account: account, Run: run, Repository: repository,
		}, nil
	}
	t.Cleanup(func() { reviewVerifierSeam = prev })
}

// setFixBriefVerifier points the launcher's verifier at the stand-in service:
// the launcher's own client runs against it, so the call the wire carries is
// the real one and only the identity and the URL are a test's.
func setFixBriefVerifier(t *testing.T) *fixBriefVerify {
	t.Helper()
	svc := fixBriefVerifyStart()
	setFixBriefVerifierAt(t, svc.srv.URL)
	return svc
}

// fixBriefLaunch launches a fix round carrying reviewRef's findings on runID
// and answers the launch's error: the one call every test below makes.
func fixBriefLaunchOn(t *testing.T, f *cloudFixture, runID, reviewRef string) error {
	t.Helper()
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", runID, "--review-run", reviewRef)
	return err
}

// fixBriefLaunch is fixBriefLaunchOn on the fixture's own follow-up id.
func fixBriefLaunch(t *testing.T, f *cloudFixture, reviewRef string) error {
	t.Helper()
	return fixBriefLaunchOn(t, f, fuID, reviewRef)
}

// fixBriefHead is the head the stand-in forge reports for the fixture's pull
// request: the head every fixture artifact names (fixFirstArtifact's own), so
// the four checks the signature alone does not make hold unless a test breaks
// one of them on purpose.
const fixBriefHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// setFixBriefHead points the launcher's forge read at the fixture's head. The
// read is the pull request's CURRENT head, which no fixture bucket holds, so
// every test that reaches the launch answers it here rather than against a
// host.
func setFixBriefHead(t *testing.T) {
	t.Helper()
	setFixBriefHeadAt(t, fixBriefHead)
}

// setFixBriefHeadAt points the launcher's forge read at one head, for the test
// whose subject is the head itself. It is called after fixBriefSeed, which has
// already set the fixture's: the two cleanups stack in assignment order, so the
// seam is back where each of them found it either way.
func setFixBriefHeadAt(t *testing.T, sha string) {
	t.Helper()
	prev := reviewPRHeadSeam
	reviewPRHeadSeam = func(context.Context, *cloudEnv, string, int) (string, error) { return sha, nil }
	t.Cleanup(func() { reviewPRHeadSeam = prev })
}

// fixBriefSeed is the fixture every test below starts from: a root run on PR 7,
// so the pull request resolves, and a review run on it.
func fixBriefSeed(t *testing.T) (*cloudFixture, string) {
	t.Helper()
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	setFixBriefHead(t)
	return f, wallRunIDAt(0, "121500", "5eed")
}

// wantFixBriefRefused launches a fix round carrying reviewRef's findings and
// wants exit 1 whose error carries every want and names the review run, with no
// execution started: a refusal is a launch that did not happen.
func wantFixBriefRefused(t *testing.T, f *cloudFixture, reviewRef string, wants ...string) error {
	t.Helper()
	before := len(f.run.Executions())
	err := fixBriefLaunch(t, f, reviewRef)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("fix round on %s: err = %v, want a refusal (exit 1)", reviewRef, err)
	}
	if !strings.Contains(err.Error(), reviewRef) {
		t.Errorf("the refusal does not name the review run %s: %v", reviewRef, err)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal of %s does not say %q: %v", reviewRef, want, err)
		}
	}
	if after := len(f.run.Executions()); after != before {
		t.Fatalf("the refused launch of %s started %d executions", reviewRef, after-before)
	}
	return err
}

// TestFixBriefRefusesAVerdictWithNoSignature is CSR10's red step: an artifact
// with no sidecar beside it is not a verdict, and a fix round is not launched
// on one. Today the artifact is decoded and its findings rendered with no
// signature read at all, so a review the service never signed becomes a fix
// round's brief.
func TestFixBriefRefusesAVerdictWithNoSignature(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
	putReviewTask(t, f, reviewID, "acme-personal")
	setFixBriefVerifier(t)

	wantFixBriefRefused(t, f, reviewID, "no signature", runner.ReviewSignName)
}

// TestFixBriefRefusesASignatureTheServiceDoesNotOwn: a sidecar nothing signed
// is refused by the service's own answer, and the brief is never built. The
// bytes are an artifact this launch is for in every other way, so the refusal
// is the signature's and not a reader that refuses everything.
func TestFixBriefRefusesASignatureTheServiceDoesNotOwn(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	data := putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
	putReviewTask(t, f, reviewID, "acme-personal")
	svc := setFixBriefVerifier(t)
	putBuildObject(t, f, "runs/"+appSlug+"/"+reviewID+"/"+runstore.ArtifactPrefix+runner.ReviewSignName,
		[]byte(strings.Repeat("c", 64)+"\n"))

	before := len(svc.recorded())
	err := wantFixBriefRefused(t, f, reviewID, runner.ErrReviewSignature.Error())
	// The verifier's refusal is already the signature family's own sentence, so
	// the reader does not wrap it a second time: it is spelled once, with the
	// reason behind it and not in front of a copy of itself.
	if n := strings.Count(err.Error(), runner.ErrReviewSignature.Error()); n != 1 {
		t.Errorf("the refusal spells %q %d times, want once: %v", runner.ErrReviewSignature.Error(), n, err)
	}
	// The service answered about the artifact this launcher actually read: a
	// reader that asked it about some other bytes would be refused by a service
	// answering correctly — the same refusal for the wrong reason — so the
	// digest the call carried is checked against the stored bytes here.
	calls := svc.recorded()[before:]
	if len(calls) != 1 {
		t.Fatalf("the verify route was called %d times, want 1", len(calls))
	}
	sum := sha256.Sum256(data)
	if want := hex.EncodeToString(sum[:]); calls[0].Digest != want {
		t.Errorf("the service was asked about the digest %s, want the SHA-256 of the stored bytes %s", calls[0].Digest, want)
	}
}

// TestFixBriefRefusesWhenTheServiceIsUnreachable is the fail-closed direction
// that matters most: a verify call that cannot be made is a refusal and never
// "nothing to check", because a brief built from an artifact nobody verified is
// exactly the launch this task exists to stop.
func TestFixBriefRefusesWhenTheServiceIsUnreachable(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	// The fixture is written by hand rather than with putReviewArtifact: that
	// helper sets the verifier seam itself, and a test that replaced the seam
	// after it would be two writes with two cleanups over one package-wide
	// variable. Here the seam is written once, at the dead URL.
	data := putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
	putReviewTask(t, f, reviewID, "acme-personal")
	putReviewSignature(t, f, reviewID, appSlug, reviewID, data)

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	setFixBriefVerifierAt(t, deadURL)

	wantFixBriefRefused(t, f, reviewID, "could not be reached")
}

// TestFixBriefRefusesAnEarlierHead is the replay guard's own test: the
// signature is the service's over these bytes, the review run, the repository
// and the pull request all agree, and the only thing wrong is the head — the
// pull request has moved past the commit the review read. A validly signed
// verdict about an earlier head is a replay (spec §6, §8's window), so the
// brief is refused and no run starts.
//
// The head it is checked against has to be the FORGE's read, and that is what
// this test pins: built from the artifact's own head_sha instead, the check
// compares the claim against itself and every replay passes.
func TestFixBriefRefusesAnEarlierHead(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	data := putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
	putReviewTask(t, f, reviewID, "acme-personal")
	svc := setFixBriefVerifier(t)
	putReviewSignature(t, f, reviewID, appSlug, reviewID, data)
	// The forge answers with a commit the artifact does not name, of the same
	// length so nothing but the value tells the two apart.
	moved := strings.Repeat("b", len(fixBriefHead))
	if moved == fixBriefHead {
		t.Fatal("the moved head is the artifact's own, so this test cannot tell the two apart")
	}
	setFixBriefHeadAt(t, moved)

	before := len(svc.recorded())
	wantFixBriefRefused(t, f, reviewID, "head_sha")
	// The service verified the signature first, so the refusal is the head's
	// and not an earlier reader's: a launch that never reached the service
	// would be red for the wrong reason.
	if calls := svc.recorded()[before:]; len(calls) != 1 {
		t.Errorf("the verify route was called %d times before the head was refused, want 1", len(calls))
	}
}

// TestFixBriefVerifiesTheBytesItReads: the digest the service is asked about is
// the SHA-256 of the artifact's STORED bytes, byte for byte. The fixture stores
// the same review under an indented encoding, so an artifact this launcher
// decoded and re-encoded before hashing would ask about bytes nobody signed.
func TestFixBriefVerifiesTheBytesItReads(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	pretty, err := json.MarshalIndent(storedReviewArtifact(fixFirstArtifact(reviewID)), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	putBuildObject(t, f, "runs/"+appSlug+"/"+reviewID+"/"+runstore.ArtifactPrefix+runner.ReviewArtifactName, pretty)
	putReviewTask(t, f, reviewID, "acme-personal")
	svc := setFixBriefVerifier(t)
	putReviewSignature(t, f, reviewID, appSlug, reviewID, pretty)

	before := len(svc.recorded())
	if err := fixBriefLaunch(t, f, reviewID); err != nil {
		t.Fatalf("a fix round on a signed review: %v", err)
	}
	calls := svc.recorded()[before:]
	if len(calls) != 1 {
		t.Fatalf("the verify route was called %d times, want 1", len(calls))
	}
	sum := sha256.Sum256(pretty)
	if want := hex.EncodeToString(sum[:]); calls[0].Digest != want {
		t.Errorf("the digest the service was asked about is %s, want the SHA-256 of the stored bytes %s", calls[0].Digest, want)
	}
	compact, err := json.Marshal(storedReviewArtifact(fixFirstArtifact(reviewID)))
	if err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256(compact)
	if hex.EncodeToString(sum[:]) == calls[0].Digest {
		t.Fatal("the fixture's two encodings are the same bytes, so this test cannot tell a re-encode from the stored object")
	}
}

// TestFixBriefVerifiesADefaultAccountReview is the account's second direction:
// the field a task spells the unsuffixed default with is no field at all, so a
// review run whose task carries no agent.oauth_token has to be verified with
// the call naming config.DefaultClaudeAccount and never the empty string, which
// the service refuses as a bad CALL. A review task the launcher cannot read
// refuses the brief rather than guessing an account.
func TestFixBriefVerifiesADefaultAccountReview(t *testing.T) {
	t.Run("the unsuffixed default", func(t *testing.T) {
		f, reviewID := fixBriefSeed(t)
		data := putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
		putReviewTask(t, f, reviewID, "")
		svc := setFixBriefVerifier(t)
		putReviewSignature(t, f, reviewID, appSlug, reviewID, data)

		before := len(svc.recorded())
		if err := fixBriefLaunch(t, f, reviewID); err != nil {
			t.Fatalf("a fix round on a default-account review: %v", err)
		}
		calls := svc.recorded()[before:]
		if len(calls) != 1 {
			t.Fatalf("the verify route was called %d times, want 1", len(calls))
		}
		if got := calls[0].Account; got != config.DefaultClaudeAccount {
			t.Errorf("the verify call names the account %q, want %q (an empty account is refused as a bad call)", got, config.DefaultClaudeAccount)
		}
	})

	t.Run("a named account", func(t *testing.T) {
		f, reviewID := fixBriefSeed(t)
		data := putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
		putReviewTask(t, f, reviewID, "acme-personal")
		svc := setFixBriefVerifier(t)
		putReviewSignature(t, f, reviewID, appSlug, reviewID, data)

		before := len(svc.recorded())
		if err := fixBriefLaunch(t, f, reviewID); err != nil {
			t.Fatalf("a fix round on a named-account review: %v", err)
		}
		calls := svc.recorded()[before:]
		if len(calls) != 1 || calls[0].Account != "acme-personal" {
			t.Errorf("the named review's verify call names %v, want acme-personal", calls)
		}
	})

	// A review run whose task cannot be read: the account has one source, and a
	// launcher that guessed one would key the call on something nobody wrote.
	t.Run("a task the launcher cannot read", func(t *testing.T) {
		f, reviewID := fixBriefSeed(t)
		data := putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
		setFixBriefVerifier(t)
		putReviewSignature(t, f, reviewID, appSlug, reviewID, data)

		wantFixBriefRefused(t, f, reviewID, "task cannot be read")
	})
}

// TestFixBriefNamesNoProseFromTheArtifact: a refusal quotes the review run and
// the artifact's identity fields and NEVER a field of the review's own prose
// (spec §7). The fixture's prose is distinctive so a leak is unmistakable.
func TestFixBriefNamesNoProseFromTheArtifact(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	art := fixFirstArtifact(reviewID)
	art.Overview = "the overview nobody should read"
	art.Security = "the security note nobody should read"
	art.Issues[0].Summary = "the summary nobody should read"
	art.Issues[0].Fix = "the fix nobody should read"
	art.Issues[0].Evidence = "the evidence nobody should read"

	// The unsigned case: refused before any verdict is read at all.
	putReviewObject(t, f, reviewID, art)
	putReviewTask(t, f, reviewID, "acme-personal")
	setFixBriefVerifier(t)
	err := wantFixBriefRefused(t, f, reviewID, "no signature")
	for _, prose := range []string{art.Overview, art.Security, art.Issues[0].Summary, art.Issues[0].Fix, art.Issues[0].Evidence} {
		if strings.Contains(err.Error(), prose) {
			t.Errorf("the refusal quotes the review's prose %q: %v", prose, err)
		}
	}

	// And the verdict case: a verified artifact whose verdict asks for no fix
	// quotes the verdict's own word and none of the review's text.
	rejectID := wallRunIDAt(0, "121900", "9eed")
	art = fixFirstArtifact(rejectID)
	art.Verdict = "reject"
	art.Overview = "the overview nobody should read"
	art.Issues[0].Summary = "the summary nobody should read"
	putReviewArtifact(t, f, rejectID, art)
	err = wantFixBriefRefused(t, f, rejectID, "reject")
	for _, prose := range []string{art.Overview, art.Issues[0].Summary} {
		if strings.Contains(err.Error(), prose) {
			t.Errorf("the refusal quotes the review's prose %q: %v", prose, err)
		}
	}
}

// TestFixBriefRefusesAVerdictThatAsksForNoFix is Open item 2's answer of
// 2026-11-05: a verdict the launcher reads is a verdict the launcher acts on,
// and only `fix first` asks for a fix round. A `ship` and a `reject` refuse in
// one sentence that names the verdict word and points at the verb that prints
// the findings anyway.
func TestFixBriefRefusesAVerdictThatAsksForNoFix(t *testing.T) {
	for _, verdict := range []string{"ship", "reject"} {
		t.Run(verdict, func(t *testing.T) {
			f, reviewID := fixBriefSeed(t)
			art := fixFirstArtifact(reviewID)
			art.Verdict = verdict
			putReviewArtifact(t, f, reviewID, art)

			wantFixBriefRefused(t, f, reviewID, verdict, "fugaro review show "+appSlug+"/"+reviewID)
		})
	}
}

// TestFixBriefBuildsTheBriefForFixFirst is the second direction of the red
// step: `fix first` — the one verdict that asks for the work this launch does —
// reaches runner.FixFindings with the artifact's issues, so the refusal above
// is a verdict policy and not a reader that refuses everything.
func TestFixBriefBuildsTheBriefForFixFirst(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	// The fixture is written by hand, and the stand-in service's calls are read
	// back, because a launch that succeeds is not by itself evidence that a
	// signature was checked: this test has to hold both halves — the brief the
	// launch built AND the verify call that let it be built.
	data := putReviewObject(t, f, reviewID, fixFirstArtifact(reviewID))
	putReviewTask(t, f, reviewID, "acme-personal")
	svc := setFixBriefVerifier(t)
	putReviewSignature(t, f, reviewID, appSlug, reviewID, data)

	before := len(svc.recorded())
	if err := fixBriefLaunch(t, f, reviewID); err != nil {
		t.Fatalf("a fix round on a fix first review: %v", err)
	}
	calls := svc.recorded()[before:]
	if len(calls) != 1 {
		t.Fatalf("the verify route was called %d times, want 1: a brief built without one is the launch this task exists to stop", len(calls))
	}
	// The call the accepted launch made is the call every refusal above is
	// about: the review run's own account, the review run, the repository, and
	// the digest of the artifact's stored bytes.
	sum := sha256.Sum256(data)
	wantCall := credsvc.VerifyRequest{
		Account:    "acme-personal",
		Digest:     hex.EncodeToString(sum[:]),
		Signature:  fixBriefSign(appSlug, reviewID, data),
		Run:        reviewID,
		Repository: appSlug,
	}
	if calls[0] != wantCall {
		t.Errorf("the accepted launch's verify call is %+v, want %+v", calls[0], wantCall)
	}

	spec := readSpec(t, f, fuID)
	for _, want := range []string{reviewID, "the lock is released before the write"} {
		if !strings.Contains(spec.Task, want) {
			t.Errorf("the fix round's brief does not carry %q:\n%s", want, spec.Task)
		}
	}
}

// bitbucketReviewClaim is the repository the RJ8 launcher tests launch a review
// for, and the one the fixture's local config records beside its GitHub ones.
const bitbucketReviewClaim = "acme/bb"

// bitbucketReviewPolicy is the fixture's published policy with the Bitbucket
// repository admitted to the project: the profile and the accounts block stay
// their own (a project's allow_repos is the gate a per-repository launch — and
// the implicit review job's own admission — reads, and the profile is not the
// repository's to be launched on in any case).
//
// Its bitbucket_token block is what makes the launch servable at all: the
// implicit review-only job mounts no token, so the review is brokered or it is
// refused (implicitReviewJobFor's nodeBrokersBitbucket arm), and a policy that
// admits the repository without the proxy switch is the state
// bitbucketReviewPolicyUnbrokered spells for the refusal's own test.
func bitbucketReviewPolicy() string {
	widened := strings.Replace(projectJobAccountsPolicy,
		`"allow_repos":["github/acme/*"]`, `"allow_repos":["github/acme/*","bitbucket/acme/*"]`, 1)
	return strings.Replace(widened, `{"version":9,`,
		`{"version":9,"bitbucket_token":{"allow_repos":["bitbucket/acme/*"],"proxy":true},`, 1)
}

// bitbucketReviewPolicyUnbrokered is bitbucketReviewPolicy with the operator's
// proxy switch off: the node admits the repository, and the credentials service
// will not forward the token for it.
func bitbucketReviewPolicyUnbrokered() string {
	return strings.Replace(bitbucketReviewPolicy(), `"proxy":true`, `"proxy":false`, 1)
}

// allowAccountOnRepo adds repo to every claude_accounts and project_secrets
// entry of the fixture's local config: its lists name acme/app alone, and a
// launch may only name an account a repository's own list carries
// (claudeAccountAllowed). It fails the test rather than writing a document the
// fixture no longer holds.
func allowAccountOnRepo(t *testing.T, repo string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const app = `allow_data_to: ["acme/app"]`
	if n := strings.Count(string(data), app); n == 0 {
		t.Fatalf("the fixture's local config names no allow_data_to list to widen:\n%s", data)
	}
	widened := strings.ReplaceAll(string(data), app, `allow_data_to: ["acme/app","`+repo+`"]`)
	if err := os.WriteFile(path, []byte(widened), 0o600); err != nil {
		t.Fatal(err)
	}
}

// recordGitHubAppID writes the App ID of the fixture's GitHub repository into
// its local config. The fixture declares a github profile, and the deployer
// refuses to compose any shared project job for a config like that until an App
// ID is recorded (internal/infra's projectJobGitHubAppID): the composition a
// review launcher reads is one computation over the whole config, so a fixture
// of an installation whose configuration is deployable has to record one even
// when the launch under test is the Bitbucket repository's.
func recordGitHubAppID(t *testing.T) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const app = "  acme/app: { provider: github,"
	if !strings.Contains(string(data), app) {
		t.Fatalf("the fixture's local config no longer holds %q:\n%s", app, data)
	}
	recorded := strings.Replace(string(data), app, app+` github_app_id: "12345",`, 1)
	if err := os.WriteFile(path, []byte(recorded), 0o600); err != nil {
		t.Fatal(err)
	}
}

// bitbucketReviewCheckout is the checkout a review of a Bitbucket repository is
// launched from: the fixture's clone workflow document with the provider moved,
// on a remote on Bitbucket's own host, committed on main and published as
// origin/main — the ref the launch names, and the document the launcher's own
// resolution reads (projectJobCheckoutYAML, which spells GitHub's host).
func bitbucketReviewCheckout(t *testing.T, repo, yaml string) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@bitbucket.org:"+repo+".git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": yaml})
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "the repository's fugaro.yaml")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	t.Chdir(dir)
}

// TestReviewLaunchesOnTheImplicitJobOfABitbucketRepository is RJ8's launcher
// half, and the case the command had no answer for at all: no shared project
// job ever serves Bitbucket (design §4.5, D5 as amended), and a repository of a
// project that declares no profile has no coding job either, so before this
// task a review of one refused with a sentence about a variant that does not
// exist beside a job that does not exist. The installation deploys one implicit
// review-only job per provider (internal/infra's ProjectReviewJobs, finding F6),
// and the launch resolves the Bitbucket one out of the config's own composed
// review set — the deployer's computation, so a gate this launcher read
// differently cannot name a job no installation has.
//
// Mutation (run, restored): drop the implicitReviewJobFor arm of reviewJobFor
// (leave a launch with no shared job on its per-repository job). The review runs
// the Bitbucket repository's own job, which mounts the repository's token rather
// than serving a review through the installation's credentials service. Mutation
// (run, restored): compose the implicit job for every provider rather than the
// providers ImplicitReviewProviders names, and the fixture's config — which
// records a Bitbucket repository and no GitHub App ID — deploys a github one too;
// this test's own assertion on the job's name is where the two differ, and
// TestProviderKindsAgreeWithTheConfigsList holds the lists together.
func TestReviewLaunchesOnTheImplicitJobOfABitbucketRepository(t *testing.T) {
	f := newReviewFixture(t)
	const bbRepo = bitbucketReviewClaim
	addRepoToConfig(t, f.cloudFixture, bbRepo, "bitbucket")
	recordGitHubAppID(t)
	publishPolicy(t, f.launchBudgetFixture, bitbucketReviewPolicy())
	implicit := infra.ImplicitReviewJobName("bitbucket")
	f.run.AddProjectJob(implicit, infra.ImplicitReviewProfile, "bitbucket",
		config.ReviewVariantAccount("")+config.ReviewVariantSuffix, "2", "4Gi")
	f.run.SetJobTimeout(implicit, projectJobTimeout+backend.TaskTimeoutSlack)
	bitbucketReviewCheckout(t, bbRepo, strings.Replace(projectJobRepoYAML, "provider: github", "provider: bitbucket", 1))

	if _, errOut, err := reviewCLI(t, "--pr", "7"); err != nil {
		t.Fatalf("a review of the Bitbucket repository %s: %v (stderr %q)", bbRepo, err, errOut)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 {
		t.Fatalf("the launch asked Cloud Run for %d executions, want 1", len(calls))
	}
	if calls[0].Job != implicit {
		t.Errorf("the review ran the job %q, want the installation's implicit review-only job %q", calls[0].Job, implicit)
	}
	if slug, err := task.Slug("bitbucket", bbRepo); err != nil {
		t.Fatal(err)
	} else if perRepo := gcp.JobName(slug, "web"); calls[0].Job == perRepo {
		t.Errorf("the review ran the repository's own job %q: that container mounts the repository's token, and a review takes its credential from the installation's credentials service", perRepo)
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
	case spec.OAuthToken != "":
		t.Errorf("the stored task names the account %q, want none: the implicit job serves the default account alone, and the credentials service pairs its caller with the account the job was deployed for", spec.OAuthToken)
	}
}

// TestReviewRefusesWhenTheConfigComposesNoImplicitJob is the other half of the
// resolution: the launcher reads the job out of the config's own composed review
// set and never recomputes a name, so an installation whose config composes none
// for this provider — here one that records no Bitbucket repository, which is the
// gate the deployer's implicitReviewVariant applies — is refused with the
// sentence that says what the job is gated on, rather than launched onto a name
// Cloud Run would answer 404 for after the claim and the budget token.
//
// Mutation (run, restored): return the name from the launcher rather than
// looking it up in the composed set. This launch reaches Cloud Run.
func TestReviewRefusesWhenTheConfigComposesNoImplicitJob(t *testing.T) {
	f := newReviewFixture(t)
	const bbRepo = bitbucketReviewClaim
	recordGitHubAppID(t)
	publishPolicy(t, f.launchBudgetFixture, bitbucketReviewPolicy())
	implicit := infra.ImplicitReviewJobName("bitbucket")
	f.run.AddProjectJob(implicit, infra.ImplicitReviewProfile, "bitbucket",
		config.ReviewVariantAccount("")+config.ReviewVariantSuffix, "2", "4Gi")
	bitbucketReviewCheckout(t, bbRepo, strings.Replace(projectJobRepoYAML, "provider: github", "provider: bitbucket", 1))

	err := wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7"},
		implicit, "bitbucket", "credentials service", "repos", "fugaro run --pr N --recipe "+reviewOnlyRecipe)
	// The gate the config failed, so that the sentence a user reads says which
	// line of their own config to look at rather than which job to go and find.
	if !strings.Contains(err.Error(), "a repository of that provider is recorded") {
		t.Errorf("the refusal does not name the gate the config failed: %q", err.Error())
	}
}

// TestReviewRefusesATokenOnTheImplicitJob is the account axis of the resolution:
// the implicit job is deployed for the default account alone (its account
// segment is fixed where it is deployed, internal/infra's ImplicitReviewJobName),
// and the credentials service pairs the caller it verifies with the account the
// job was deployed for — so a review that names another account with --token is
// refused here rather than launched onto the default account's job with a task
// that says otherwise.
//
// Mutation (run, restored): drop the account refusal. This launch is admitted
// and the job's reviewer is served as the default account while the task names
// another one.
func TestReviewRefusesATokenOnTheImplicitJob(t *testing.T) {
	f := newReviewFixture(t)
	const bbRepo = bitbucketReviewClaim
	addRepoToConfig(t, f.cloudFixture, bbRepo, "bitbucket")
	recordGitHubAppID(t)
	publishPolicy(t, f.launchBudgetFixture, bitbucketReviewPolicy())
	allowAccountOnRepo(t, bbRepo)
	implicit := infra.ImplicitReviewJobName("bitbucket")
	f.run.AddProjectJob(implicit, infra.ImplicitReviewProfile, "bitbucket",
		config.ReviewVariantAccount("")+config.ReviewVariantSuffix, "2", "4Gi")
	f.run.SetJobTimeout(implicit, projectJobTimeout+backend.TaskTimeoutSlack)
	// The account has to be one the repository may use and one the fixture's
	// installation has not marked: acme-personal is both, and the review
	// document says agent.oauth: it is what a repository whose own fugaro.yaml
	// names a named account carries (projectJobOAuthRepoYAML's provider moved).
	bitbucketReviewCheckout(t, bbRepo, strings.Replace(projectJobOAuthRepoYAML, "provider: github", "provider: bitbucket", 1))

	wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7", "--token", "acme-personal"},
		"acme-personal", "serves one account", config.DefaultClaudeAccount,
		"fugaro run --pr N --recipe "+reviewOnlyRecipe)
}

// reviewImplicitPolicy is the published node of a project that declares no
// profile for its provider — the shape option C's implicit review-only job
// exists for (docs/review-job/spec.md): a review launched against it resolves no
// shared coding job, so its variant cannot be derived and the launch lands on
// the installation's implicit review-only job, which is the one place the
// account axis of that job is judged (implicitReviewJobFor). naming, when not
// "", is the project's default_claude_account (D873's rung 4).
//
// The accounts block is the project's own, one exact list per account, which is
// what bounds each token's repositories (D3) and what the credentials service
// pairs a review caller against when a job asks it for a credential.
func reviewImplicitPolicy(t *testing.T, naming string) string {
	t.Helper()
	head := `{"version":9,`
	if naming != "" {
		head += `"default_claude_account":"` + naming + `",`
	}
	return head + `"allow_repos":["github/acme/*"],` +
		`"accounts":{"acme-personal":{"allow_repos":["github/acme/app"]},"default":{"allow_repos":["github/acme/app"]}}}`
}

// repoYAMLWithAccount is the fixture's oauth checkout with the account its own
// fugaro.yaml commits (agent.oauth_token, rung 3 of the accounts chain): the
// shape a repository whose document picks its reviewer's account carries.
func repoYAMLWithAccount(t *testing.T, account string) string {
	t.Helper()
	const auth = "  auth: oauth\n"
	if !strings.Contains(projectJobOAuthRepoYAML, auth) {
		t.Fatalf("the fixture's oauth checkout no longer holds %q:\n%s", auth, projectJobOAuthRepoYAML)
	}
	return strings.Replace(projectJobOAuthRepoYAML, auth, auth+"  oauth_token: "+account+"\n", 1)
}

// TestReviewLaunchesOnTheImplicitJobOfAProjectWithANamedDefaultAccount is the
// case Check 750 hit on 2026-11-24: the project's default Claude account is a
// named one (default_claude_account, D873's rung 4), so a review launched with
// no --token resolves its account the way every other launch of that project
// does — and the implicit review-only job serves exactly that account, its
// segment being the deployment's own spelling of the project's default
// (internal/infra's ImplicitReviewJobName, whose axis writes the default as the
// empty string and the variant as config.DefaultClaudeAccount). The launcher
// compared the resolved name with the segment's literal spelling and refused
// the launch with "which serves one account: the default one", naming an
// account nobody had asked for and telling the person to drop a --token they
// never passed.
//
// The third case is the control: the same installation, with no
// default_claude_account in its local config and none in its published node, is
// unchanged — the launch is admitted onto the job and its task names no account
// at all.
//
// Mutation (run, restored): compare reviewAccount(spec) with
// config.DefaultClaudeAccount instead of the policy's default
// (implicitReviewServedAccount), and the first two cases refuse.
func TestReviewLaunchesOnTheImplicitJobOfAProjectWithANamedDefaultAccount(t *testing.T) {
	for _, tc := range []struct {
		name     string
		naming   string // the project's default_claude_account, "" for none
		args     []string
		account  string // the account the stored task must name
		codeOnly bool
	}{
		{name: "review", naming: "acme-personal", args: []string{"--pr", "7"}, account: "acme-personal"},
		{name: "code-only review", naming: "acme-personal", args: []string{"--pr", "7", "--code-only"}, account: "acme-personal", codeOnly: true},
		{name: "no named default", args: []string{"--pr", "7"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReviewFixture(t)
			if tc.naming != "" {
				f.appendConfig(t, "default_claude_account: "+tc.naming+"\n")
			}
			publishPolicy(t, f.launchBudgetFixture, reviewImplicitPolicy(t, tc.naming))
			// The config declares a github profile, and the deployer composes
			// no project job for one until exactly one App ID is recorded: this
			// launch reads the job back out of that composition
			// (implicitReviewJob), which fails without it.
			recordGitHubAppID(t)
			// The fixture's own base image is as old as the policy node it has
			// to read, and a review launched onto it has its spec dropped
			// (reviewSpecSkew, D2933): this test reads the block the --code-only
			// flag writes, so the installation is put on a release that carries
			// it.
			setBaseImage(t, reviewSpecSince)
			projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)

			if _, errOut, err := reviewCLI(t, tc.args...); err != nil {
				t.Fatalf("a review of PR #7 on a project whose default account is %q: %v (stderr %q)", tc.naming, err, errOut)
			}
			calls := f.run.RunRequests()
			if len(calls) != 1 {
				t.Fatalf("the launch asked Cloud Run for %d executions, want 1", len(calls))
			}
			if implicit := infra.ImplicitReviewJobName("github"); calls[0].Job != implicit {
				t.Errorf("the review ran the job %q, want the installation's implicit review-only job %q", calls[0].Job, implicit)
			}
			if named := credsvc.ReviewJobName("standard", "github", "acme-personal"); calls[0].Job == named {
				t.Errorf("the review ran the named account's own review variant %q: a launch that resolves no shared coding job runs the implicit one, whatever account the project's default is", named)
			}
			if perRepo := gcp.JobName(appSlug, "web"); calls[0].Job == perRepo {
				t.Errorf("the review ran the repository's own job %q: that container mounts the repository's token, and a review takes its credential from the installation's credentials service", perRepo)
			}
			if got := calls[0].Env[runner.EnvReviewJob]; got != "1" {
				t.Errorf("%s = %q on the review's execution, want \"1\": the marker is what tells the run it is the review job", runner.EnvReviewJob, got)
			}
			slug, id, err := parseRunRef(storedRunRef(t, f.cloudFixture))
			if err != nil {
				t.Fatal(err)
			}
			spec := reviewTaskSpec(t, f.cloudFixture, slug, id)
			if spec.Mode != recipe.ModeReview {
				t.Errorf("the stored task's mode is %q, want %q", spec.Mode, recipe.ModeReview)
			}
			if spec.OAuthToken != tc.account {
				t.Errorf("the stored task names the account %q, want %q: the account the launch resolves is the one the implicit job serves", spec.OAuthToken, tc.account)
			}
			switch {
			case tc.codeOnly && (spec.ReviewSpec == nil || spec.ReviewSpec.Source != task.ReviewSpecNone || spec.ReviewSpec.Reason != launchSpecCodeOnly):
				t.Errorf("a --code-only review's spec block is %+v, want a none source with the %s reason", spec.ReviewSpec, launchSpecCodeOnly)
			case !tc.codeOnly && spec.ReviewSpec != nil && spec.ReviewSpec.Reason == launchSpecCodeOnly:
				t.Errorf("a plain review's spec block says %q: --code-only was not passed", spec.ReviewSpec.Reason)
			}
		})
	}
}

// TestReviewRefusesAnAccountTheProjectDoesNotDefaultTo is the other half of the
// account axis, on the installation above: a review whose account is NOT the
// project's default is refused with the sentence that names the job's one
// account, whether it came from --token or from the repository's own
// agent.oauth_token (rung 3) — the second case being a launch the person passed
// no flag on, which the sentence must not answer by telling them to drop one.
//
// Mutation (run, restored): drop the account refusal, and both launches are
// admitted onto a job whose reviewer the credentials service pairs with the
// project's default account, while their tasks name another.
func TestReviewRefusesAnAccountTheProjectDoesNotDefaultTo(t *testing.T) {
	const other = "acme-work"
	for _, tc := range []struct {
		name      string
		args      []string
		committed bool // the repository's own fugaro.yaml names the account
	}{
		{name: "--token", args: []string{"--pr", "7", "--token", other}},
		{name: "the repository's own agent.oauth_token", args: []string{"--pr", "7"}, committed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReviewFixture(t)
			f.appendConfig(t, "default_claude_account: acme-personal\n")
			publishPolicy(t, f.launchBudgetFixture, reviewImplicitPolicy(t, "acme-personal"))
			recordGitHubAppID(t)
			yaml := projectJobOAuthRepoYAML
			if tc.committed {
				yaml = repoYAMLWithAccount(t, other)
			}
			projectJobCheckoutYAML(t, yaml)

			err := wantReviewRefused(t, f.cloudFixture, tc.args, other, "serves one account", "acme-personal",
				"fugaro run --pr N --recipe "+reviewOnlyRecipe)
			if strings.Contains(err.Error(), "without --token") {
				t.Errorf("the refusal tells the person to drop a --token, which this launch may never have passed: %q", err.Error())
			}
		})
	}
}

// TestReviewRefusesABitbucketRepositoryTheNodeDoesNotBroker is the credential
// axis of the same resolution, and the one state the implicit job cannot serve
// at all: the job mounts no Bitbucket token (it is the property that makes it a
// review job), so the token can only come from the credentials service, and the
// service forwards it for a repository only where the published node admits it
// with proxy: true. A repository the node admits without that switch is a
// review whose container would open the repository with no token and stop at
// its first git call (the runner's openProvider, on a variable that is not
// set), which is a failure the operator reads in the run's output rather than
// the sentence that names what to do instead.
//
// Mutation (run, restored): drop the nodeBrokersBitbucket arm of
// implicitReviewJobFor, and this launch reaches Cloud Run and starts a review
// that cannot read the pull request it was launched for.
func TestReviewRefusesABitbucketRepositoryTheNodeDoesNotBroker(t *testing.T) {
	f := newReviewFixture(t)
	const bbRepo = bitbucketReviewClaim
	addRepoToConfig(t, f.cloudFixture, bbRepo, "bitbucket")
	recordGitHubAppID(t)
	publishPolicy(t, f.launchBudgetFixture, bitbucketReviewPolicyUnbrokered())
	implicit := infra.ImplicitReviewJobName("bitbucket")
	f.run.AddProjectJob(implicit, infra.ImplicitReviewProfile, "bitbucket",
		config.ReviewVariantAccount("")+config.ReviewVariantSuffix, "2", "4Gi")
	f.run.SetJobTimeout(implicit, projectJobTimeout+backend.TaskTimeoutSlack)
	bitbucketReviewCheckout(t, bbRepo, strings.Replace(projectJobRepoYAML, "provider: github", "provider: bitbucket", 1))

	err := wantReviewRefused(t, f.cloudFixture, []string{"--pr", "7"},
		"mounts no Bitbucket token", "proxy: true",
		"fugaro run --pr N --recipe "+reviewOnlyRecipe)
	if !strings.Contains(err.Error(), bbRepo) {
		t.Errorf("the refusal does not name the repository whose access is not brokered: %q", err.Error())
	}
}

// writeSpecFile writes text to a file and returns its path: the FILE that
// `fugaro review --spec FILE` (and `fugaro run --recipe … --spec FILE`) reads.
func writeSpecFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReviewCodeOnlyAndSpecRefused is R11's first half (spec §5): the two flags
// ask a review for different things — no task at all, and this file's text as
// the task — so giving both is refused at launch, naming both, before anything
// is read or started.
func TestReviewCodeOnlyAndSpecRefused(t *testing.T) {
	f := newCloudFixture(t)
	spec := writeSpecFile(t, "rename the gadget")
	wantReviewRefused(t, f, []string{"--pr", "7", "--code-only", "--spec", spec},
		"--code-only and --spec", "Give one of the two")
}

// TestReviewSpecFlagOnNonReviewRecipeRefused is R11's second half: each flag
// asks for a review, so on a launch that is not one — an ordinary recipe, or a
// follow-up, which is a fix round — the flag is refused by name rather than
// accepted and silently ignored.
func TestReviewSpecFlagOnNonReviewRecipeRefused(t *testing.T) {
	f := newCloudFixture(t)
	spec := writeSpecFile(t, "rename the gadget")
	for _, tc := range []struct {
		name string
		args []string
		flag string
	}{
		{"--code-only on an ordinary recipe", []string{"run", "rename the gadget", "--repo", "acme/app", "--recipe", "default", "--code-only"}, "--code-only"},
		{"--spec on an ordinary recipe", []string{"run", "rename the gadget", "--repo", "acme/app", "--recipe", "default", "--spec", spec}, "--spec"},
		{"--code-only on a follow-up", []string{"run", "--repo", "acme/app", "--pr", "7", "--code-only"}, "--code-only"},
		{"--spec on a follow-up", []string{"run", "--repo", "acme/app", "--pr", "7", "--spec", spec}, "--spec"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantRefused(t, f, tc.flag, tc.args...)
		})
	}
}

// TestReviewSpecFileTooLarge is R4's other half for the one text a person hands
// in: a stored task is clipped, but a file over the cap is refused with its
// size, since a person can trim a file and the launcher can vouch for nothing
// it did not write.
func TestReviewSpecFileTooLarge(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+reviewSpecSince)
	big := writeSpecFile(t, strings.Repeat("a line of the brief\n", 2000)) // 40000 bytes, over the 16 KiB cap
	_, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "review-only", "--pr", "7",
		"--run-id", "20261119-100000-abcd", "--spec", big)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("--spec over the cap: err = %v, want exit 1", err)
	}
	for _, want := range []string{strconv.Itoa(40000), strconv.Itoa(task.MaxReviewSpecBytes)} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("--spec over the cap refused with %q, want it to name %s", err.Error(), want)
		}
	}
}

// TestReviewSpecFileRedacted is R13: a --spec FILE is text a launcher adds, so
// it is redacted with redact.Text before it is written — no credential a file
// happens to quote reaches the reviewer — and a file is no run, so the spec
// carries no run id.
func TestReviewSpecFileRedacted(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+reviewSpecSince)
	token := "ghp_" + strings.Repeat("a1B", 12)
	path := writeSpecFile(t, "the token is "+token+"\nrename the gadget")
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "review-only", "--pr", "7",
		"--run-id", "20261119-100000-abcd", "--spec", path); err != nil {
		t.Fatalf("a review against a --spec file: %v", err)
	}
	rs := readSpec(t, f, "20261119-100000-abcd").ReviewSpec
	if rs == nil || rs.Source != task.ReviewSpecFile {
		t.Fatalf("review spec = %+v, want the file's own text as the spec", rs)
	}
	if rs.Run != "" {
		t.Errorf("review spec names the run %q, and a file a person supplied is not a run", rs.Run)
	}
	if strings.Contains(rs.Text, token) {
		t.Error("the spec carries the token the file quotes")
	}
	if !strings.Contains(rs.Text, redactedMarker) {
		t.Errorf("the spec carries no %s where the token was: %q", redactedMarker, rs.Text)
	}
}

// TestReviewCodeOnlyLaunchesWithoutATask is spec §3 through the whole command:
// --code-only writes a spec of no spec whose reason is the flag, so the review
// job judges the code alone and records why.
func TestReviewCodeOnlyLaunchesWithoutATask(t *testing.T) {
	f := newReviewFixture(t)
	// The fixture's own base image is as old as the policy node it has to read,
	// and a review launched onto it has its spec dropped (reviewSpecSkew,
	// D2933): this test is about the field the flag writes, so the installation
	// is put on a release that carries it — the other test covers the drop.
	setBaseImage(t, reviewSpecSince)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
	if _, _, err := reviewCLI(t, "--pr", "7", "--token", "acme-personal", "--code-only"); err != nil {
		t.Fatalf("fugaro review --code-only: %v", err)
	}
	ref := storedRunRef(t, f.cloudFixture)
	slug, id, err := parseRunRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	rs := reviewTaskSpec(t, f.cloudFixture, slug, id).ReviewSpec
	if rs == nil || rs.Source != task.ReviewSpecNone || rs.Reason != launchSpecCodeOnly {
		t.Fatalf("review spec = %+v, want source none with the reason %q", rs, launchSpecCodeOnly)
	}
}

// TestReviewOldImageJSONCarriesReason is D2933 as amended: an older job image
// gets none of the fields and the launcher says why in one line — and the
// launch's --json document carries the same reason, so a caller that reads only
// the document is told why the review judged the code alone.
func TestReviewOldImageJSONCarriesReason(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, reviewSpecSince, -1))
	seedLaunchRoot(t, f, rootID, "Rename the gadget", 7, time.Now().Add(-72*time.Hour))
	out, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "review-only", "--pr", "7",
		"--run-id", "20261119-100000-abcd", "--json")
	if err != nil {
		t.Fatalf("launching a review on an older image: %v", err)
	}
	var res launchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("the launch's --json document: %v\n%s", err, out)
	}
	if res.ReviewSpecReason == "" {
		t.Fatalf("the document carries no reason for the dropped spec:\n%s", out)
	}
	for _, want := range []string{reviewSpecSince, "judges the code alone"} {
		if !strings.Contains(res.ReviewSpecReason, want) {
			t.Fatalf("the document's reason is %q, want it to name %s", res.ReviewSpecReason, want)
		}
	}
}

// TestReviewShowAgainstLine is R8's reader: `fugaro review show` prints one
// line saying what the review was checked against — the run whose task it was,
// the digest of a hand-supplied file, or none with the reason — beside the
// artifact it already printed, and never in --json, whose bytes are the job's
// own and are printed as they stand.
//
// The last two rows are the record this build does not know, which is what
// Against.Validate is for: an artifact is bytes a job wrote, and a line built
// from it without the check would print an unknown source as `none`, and would
// carry a sha256 that is not a digest — including the escape below — onto the
// terminal as it stands.
func TestReviewShowAgainstLine(t *testing.T) {
	f := newCloudFixture(t)
	digest := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name    string
		against string
		want    string // "" for a record no line is printed about
	}{
		{"a run's task", `{"source":"run","run":"` + rootID + `","sha256":"` + digest + `"}`, "against: run " + rootID},
		{"a hand-supplied file", `{"source":"file","sha256":"` + digest + `"}`, "against: file " + digest},
		{"no task at all", `{"source":"none","reason":"` + launchSpecCodeOnly + `"}`, "against: none (" + launchSpecCodeOnly + ")"},
		{"an unknown source", `{"source":"branch","sha256":"` + digest + `"}`, ""},
		{"a digest that is not one", `{"source":"file","sha256":"\u001b[2J"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := wallRunIDAt(0, "120000", "5eed")
			artifact := `{"version":1,"pr":7,"verdict":"comment","against":` + tc.against + `}`
			putBuildObject(t, f, "runs/"+appSlug+"/"+id+"/"+runstore.ArtifactPrefix+runner.ReviewArtifactName, []byte(artifact))
			putBuildObject(t, f, "runs/"+appSlug+"/"+id+"/"+runstore.ArtifactPrefix+runner.ReviewMarkdownName, []byte("# Review\n"))
			ref := appSlug + "/" + id

			out, _, err := reviewCLI(t, "show", ref)
			if err != nil {
				t.Fatalf("review show %s: %v", ref, err)
			}
			want := 0
			if tc.want != "" {
				want = 1
			}
			lines := 0
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, "against:") {
					lines++
					if line != tc.want {
						t.Errorf("review show printed %q, want %q", line, tc.want)
					}
				}
			}
			if lines != want {
				t.Errorf("review show printed %d against: lines, want %d:\n%s", lines, want, out)
			}

			asJSON, _, err := reviewCLI(t, "show", ref, "--json")
			if err != nil {
				t.Fatalf("review show --json %s: %v", ref, err)
			}
			if asJSON != artifact+"\n" {
				t.Errorf("review show --json printed %q, want the artifact's own bytes alone (%q)", asJSON, artifact+"\n")
			}
		})
	}
}

// TestReviewShowNoAgainstLineForAnOlderArtifact: an artifact written before the
// against field existed carries none, and a review show of one prints no line
// rather than inventing what it was checked against.
func TestReviewShowNoAgainstLineForAnOlderArtifact(t *testing.T) {
	f := newCloudFixture(t)
	id := wallRunIDAt(0, "120000", "5eed")
	putBuildObject(t, f, "runs/"+appSlug+"/"+id+"/"+runstore.ArtifactPrefix+runner.ReviewArtifactName, []byte(`{"version":1,"pr":7,"verdict":"comment"}`))
	putBuildObject(t, f, "runs/"+appSlug+"/"+id+"/"+runstore.ArtifactPrefix+runner.ReviewMarkdownName, []byte("# Review\n"))
	out, _, err := reviewCLI(t, "show", appSlug+"/"+id)
	if err != nil {
		t.Fatalf("review show: %v", err)
	}
	if strings.Contains(out, "against:") {
		t.Errorf("review show printed an against: line for an artifact that carries none:\n%s", out)
	}
}
