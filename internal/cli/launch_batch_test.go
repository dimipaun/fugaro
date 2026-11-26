package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// batchBlockForLaunch is the `batch:` block an installation that selected the
// backend carries (init_compute writes it on a provisioning apply), enough of
// it for the backend to open: every field CheckOptions requires, pointed at no
// cloud at all. The launches below refuse before any Batch call is made, so no
// endpoint of the block is ever asked anything -- what the block is here for is
// that the launch opens the backend the config selects and reaches the gates
// with one, which is where the refusal lives.
const batchBlockForLaunch = "backend: cloud-batch\n" +
	"batch:\n" +
	"  image: us-east5-docker.pkg.dev/proj-1234/fugaro/fugaro:1\n" +
	"  machine_type: e2-standard-4\n" +
	"  boot_image: projects/proj-1234/global/images/family/fugaro-base\n" +
	"  network: projects/proj-1234/global/networks/fugaro\n" +
	"  subnetwork: projects/proj-1234/regions/us-east5/subnetworks/fugaro\n" +
	"  service_account: fugaro-runs@proj-1234.iam.gserviceaccount.com\n"

// TestAnUnservedRepositoryIsRefusedOnBatchWithItsFix is BR11's launcher half
// (D3104): a Batch job mounts nothing, so a launch on the Batch backend whose
// repository the credentials service does not serve is refused before anything
// of it is created, with the fix that changes that. The run such a launch would
// start holds no model credential at all -- not the repository's own, which
// nothing mounts, and not the service's, which this installation cannot reach
// for it -- and it would hold none silently, failing at its first model call
// with a Task already created, a claim already taken and a token already
// minted.
//
// The rows are the switch's conditions taken one at a time, because the
// refusal is one sentence for all of them and a gate that judged any one of
// them wrongly would be a launch refused that the service serves, or admitted
// that it does not. What makes the refusal the gate's and not a launch's own is
// the Cloud Run row: the same installation, the same repository and the same
// unserved pair are a launch Cloud Run admits, because a Cloud Run run's job
// carries its own mounts where the service does not serve it (G17). The gate is
// called directly, the way the Batch half of the proxy gate is (G14) and for
// the same reason: what refuses is a fact about the backend and the switch, and
// a launch that reaches it needs a whole fixture (a build record, an image, a
// job) that has nothing to do with it.
//
// Mutation (drop the gate's second half, or its backend comparison): the first
// two rows launch, or every Cloud Run launch of an unserved repository is
// refused with it.
func TestAnUnservedRepositoryIsRefusedOnBatchWithItsFix(t *testing.T) {
	for _, tc := range []struct {
		name     string
		block    string
		switchOn bool
		be       backend.Codec
		refused  bool
	}{
		{"an installation that carries no credentials service, on Batch", "", true, backend.BatchCodec(), true},
		{"a repository whose record says its root was not applied with the switch on, on Batch", proxiedBlock, false, backend.BatchCodec(), true},
		{"a repository the service serves, on Batch", proxiedBlock, true, backend.BatchCodec(), false},
		{"the same unserved repository, on Cloud Run", "", true, backend.CloudRunCodec(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchBudget(t, "enforce")
			addProxiedRepo(t, f, releasePolicy, tc.block, tc.switchOn)
			publishPolicy(t, f, proxiedPolicy)
			_, spec := proxiedLaunchSpec()
			env := fileEnv(t, f.cloudFixture)
			env.be = backendNamed{codec: tc.be}
			err := batchLaunchRefusal(context.Background(), env, spec)
			switch {
			case tc.refused && err == nil:
				t.Fatal("a Batch launch of a repository the credentials service does not serve was admitted: it would start a run holding no model credential at all (D3104)")
			case tc.refused:
				if ExitCode(err) != ExitUserError {
					t.Errorf("the refusal is not a user error: %v", err)
				}
				for _, want := range []string{backend.CloudBatch, "credentials service", spec.Repo, selfCommand() + " update", "cloud-run"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not name %q: %v", want, err)
					}
				}
			case err != nil:
				t.Fatalf("a launch the service serves, or one on another backend, was refused: %v", err)
			}
		})
	}
}

// TestTheRefusalComesBeforeTheMint is the other half of BR11's launcher
// acceptance: the refusal is a gate, so it comes before the claim and before
// the budget token is minted -- a launch refused for holding no credential must
// not have spent one. It runs the command a person runs, on an installation
// whose config selects the Batch backend, and holds the two things the order is
// made of: nothing was minted (the IAM signer was never asked, and the bucket
// holds no token object) and nothing was launched (no execution, no stored
// task). A gate moved below the mint would fail here and nowhere else.
//
// Mutation (drop the call, or move it out of freshLaunchGates and into the
// launch after the mint): the launch goes on, and the assertions below are
// what fails -- the signer was asked and the bucket holds a token object.
func TestTheRefusalComesBeforeTheMint(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	f.appendConfig(t, batchBlockForLaunch)
	_, _, err := runBudget(t)
	if ExitCode(err) != ExitUserError || err == nil {
		t.Fatalf("a Batch launch of a repository the credentials service does not serve: err = %v, want a refusal", err)
	}
	for _, want := range []string{backend.CloudBatch, "credentials service", selfCommand() + " update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if n := len(f.iam.SignCalls()); n != 0 {
		t.Errorf("the refusal minted a budget token: %d signing call(s) reached the IAM fake", n)
	}
	if tok := readToken(t, f.cloudFixture, budgetRun); tok != "" {
		t.Error("a refused launch wrote a token object into the bucket")
	}
	if launchedAny(f) {
		t.Error("the refused launch started an execution")
	}
	if storedTask(t, f.cloudFixture) {
		t.Error("the refused launch stored a task")
	}
}

// batchOptInLine is the fact an operator's own selection writes into the
// published batch block (BR18, D3110): the launches below that are meant to
// proceed carry it, and the one meant to be refused does not. It is a line of
// its own rather than part of batchBlockForLaunch so that BR11's gate keeps
// this file's other fixture -- an installation that has not opted in, whose
// refusal is the opt-in's and not the credential's.
const batchOptInLine = "  opt_in: in\n"

// batchLaunchRig is one Batch launch's world: an installation whose config
// SELECTS the backend and whose endpoints point at the Cloud Batch fake, the
// credentials service this test stands up, and the ticket the service issues.
type batchLaunchRig struct {
	f   *launchBudgetFixture
	bf  *gcpfake.Batch
	svc *launchService
	// atRegister is how many jobs the fake had created when the registration
	// arrived, which is what makes the order of the two calls a fact of the
	// test rather than of the code it reads.
	atRegister int
}

// batchLaunchWorld builds it, with the opt-in fact as `optIn` ("" is an
// installation whose operator has not opted in, batchOptInLine the one that
// has) and the service answering `answers`. Everything else is the world every
// launch of this repository is launched into: the job image a ticket-taking
// runner is built from (releaseIdentity), the repository's own half of the
// service's switch, and this machine's identity mint stubbed -- no test may run
// gcloud.
func batchLaunchWorld(t *testing.T, optIn string, answers map[string]ticketAnswer) *batchLaunchRig {
	t.Helper()
	f := newLaunchBudget(t, "enforce")
	bf := gcpfake.NewBatch(t)
	bf.Project, bf.Region = "proj-1234", "us-east5"
	svc := newLaunchService(t, answers)
	r := &batchLaunchRig{f: f, bf: bf, svc: svc}
	// The fixture's endpoints line is rewritten rather than appended to:
	// localcfg's strict decode refuses a second `endpoints` key, and a Batch
	// backend opened without one calls the real API.
	f.rewriteConfig(t, "endpoints: { ", "endpoints: { batch: "+bf.URL+"/, ")
	f.appendConfig(t, batchBlockForLaunch+batchOptInLine+"credentials_service: { url: "+svc.serviceURL()+" }\n")
	markRepoCredentialsServiceReady(t, f.cloudFixture)
	// The job image this launch starts has to be one whose runner knows the
	// claim: a launch that holds a ticket holds no sealed object, and LM8's
	// gate refuses an older image (identitySince).
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", releaseIdentity)
	// And a checkout of the repository, because the artifact boundary is asked
	// before anything else and fails closed on Batch: a recipe this launcher
	// cannot read is a run nothing here can say is not an artifact one, and a
	// Batch run's account is not per-repository (checkArtifactLaunch).
	batchLaunchCheckout(t)

	prevIdentity, prevSleep, prevSelf, prevAsk := launchIdentity, ticketSleep, selfCommand, launchAskTicket
	launchAskTicket = func(ctx context.Context, i *launchIssuer, req identitywire.TicketRequest) (identitywire.TicketResponse, error) {
		return i.askRetry(ctx, req)
	}
	launchIdentity = func(context.Context, string) (string, error) { return ticketIdentity, nil }
	ticketSleep = func(context.Context, time.Duration) error { return nil }
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() {
		launchIdentity, ticketSleep, selfCommand, launchAskTicket = prevIdentity, prevSleep, prevSelf, prevAsk
	})
	return r
}

// launchRunOnBatch runs the command a person runs and answers the run's error,
// with the output the two streams carried.
func (r *batchLaunchRig) launchRunOnBatch(t *testing.T) (string, string, error) {
	t.Helper()
	return runBudget(t)
}

// createdJob is the one job this launch created, as the fake holds it.
func (r *batchLaunchRig) createdJob(t *testing.T) (string, map[string]any) {
	t.Helper()
	creates := r.bf.Creates()
	if len(creates) != 1 {
		t.Fatalf("the launch created %d jobs, want one", len(creates))
	}
	if creates[0].Name == "" {
		t.Fatalf("the fake refused the one create it recorded (jobId %q): %v", creates[0].JobID, creates[0].Job)
	}
	return creates[0].JobID, creates[0].Job
}

// jobVars is a created job's task environment as the job carries it, which is
// the only place a launch's variable goes on this backend (a Batch job has no
// per-execution override).
func jobVars(t *testing.T, job map[string]any) map[string]any {
	t.Helper()
	groups, ok := job["taskGroups"].([]any)
	if !ok || len(groups) == 0 {
		t.Fatalf("the created job carries no task group: %v", job)
	}
	g, ok := groups[0].(map[string]any)
	if !ok {
		t.Fatalf("the created job's task group is not an object: %v", groups[0])
	}
	spec, ok := g["taskSpec"].(map[string]any)
	if !ok {
		t.Fatalf("the created job's task group carries no taskSpec: %v", g)
	}
	env, ok := spec["environment"].(map[string]any)
	if !ok {
		t.Fatalf("the created job's taskSpec carries no environment: %v", spec)
	}
	vars, _ := env["variables"].(map[string]any)
	return vars
}

// jobTaskEnvironment is the whole task environment object, so a test can see
// the members a launch must NOT write as well as the ones it must.
func jobTaskEnvironment(t *testing.T, job map[string]any) map[string]any {
	t.Helper()
	groups := job["taskGroups"].([]any)
	spec := groups[0].(map[string]any)["taskSpec"].(map[string]any)
	return spec["environment"].(map[string]any)
}

// TestABatchLaunchCarriesTheTicketAndNothingElse is BR1's launcher acceptance
// on the Batch backend (R1, R8 and F21): the launch asks the issuer for the
// run's ticket with the full name of the job it is about to create, puts the
// ticket on that job as identitywire.TicketEnv and nothing else, and neither
// mints a budget token nor seals anything at all.
//
// The red step is the pair the assertion below is built from: the plain marker
// appearing among the job's variables (a launch that minted and sealed, which a
// Batch job cannot carry), or the ticket set with the ask naming no job (an
// identity issued for a job the run does not run).
//
// Mutation (mint on Batch, put the seal marker on the job, or ask for the
// ticket without the job's name): one of the assertions below fails -- the
// signer was asked, the marker is among the variables, or the ask's job is
// empty.
func TestABatchLaunchCarriesTheTicketAndNothingElse(t *testing.T) {
	expires := time.Now().Add(30 * time.Minute)
	r := batchLaunchWorld(t, batchOptInLine, ticketAnswers(expires))
	_, _, err := r.launchRunOnBatch(t)
	if err != nil {
		t.Fatalf("a Batch launch of an opted-in installation whose service issues a ticket: %v", err)
	}
	jobID, job := r.createdJob(t)

	// The ticket named the job before the job existed, and it named THIS job:
	// the backend's own derivation, against the fake's record of what it made.
	asked := r.svc.asked()
	if len(asked) == 0 || asked[0].Path != identitywire.TicketPath {
		t.Fatalf("the service was asked %v, want the ticket route", asked)
	}
	req := ticketAskOf(t, asked[0])
	want := "projects/proj-1234/locations/us-east5/jobs/" + jobID
	if string(req.Job) != want {
		t.Fatalf("the ask names the job %q, and the launch created %q: a Batch run's identity is issued for the job it runs (R1, D3108)", string(req.Job), want)
	}
	if req.Slug != appSlug || req.Run != budgetRun {
		t.Fatalf("the ask names %s/%s, want the run this launch is starting (%s/%s)", req.Slug, req.Run, appSlug, budgetRun)
	}

	// The job carries the ticket, and the ticket is the whole of the identity
	// it carries.
	vars := jobVars(t, job)
	if got := vars[identitywire.TicketEnv]; got != ticketValue {
		t.Fatalf("the job's variables carry %s=%v, want the ticket the service issued (R1)", identitywire.TicketEnv, got)
	}
	for _, name := range []string{runner.BudgetTokenEnv, "FUGARO_BUDGET_TOKEN", "FUGARO_BUDGET_SEAL_KEY"} {
		if v, ok := vars[name]; ok {
			t.Fatalf("the job's variables carry %s=%v: a Batch job never carries the budget token's marker, plain or sealed (F21, R8)", name, v)
		}
	}
	env := jobTaskEnvironment(t, job)
	for _, member := range []string{"secretVariables", "encryptedVariables"} {
		if v, ok := env[member]; ok && v != nil {
			t.Fatalf("the job's task environment carries %s=%v: a sealed entry on a Batch job is refused by the backend, so no launcher of this release writes one (F21, R8)", member, v)
		}
	}
	// And this launch minted nothing: the whole budget half of the launch --
	// the signer call, the token object, the seal key -- is absent.
	if calls := r.f.iam.SignCalls(); len(calls) != 0 {
		t.Fatalf("the launch minted %d budget token(s) with this machine's own signer, want none: a Batch run's identity is the ticket (R1, F28)", len(calls))
	}
	if raw := readToken(t, r.f.cloudFixture, budgetRun); raw != "" {
		t.Errorf("a Batch launch wrote a token object into the bucket (%d bytes), want none", len(raw))
	}
}

// batchLaunchCheckout makes the working directory a checkout of the
// repository these launches run for, holding the ordinary implement recipe:
// the one thing a Batch launch needs that a Cloud Run launch does not, because
// the artifact boundary reads the recipe this launcher can resolve and refuses
// a launch on Batch whose recipe it cannot read at all (checkArtifactLaunch).
//
// It is the fixture's own checkout rather than the test's own binary's: the
// launches below go through `fugaro run`, which resolves a repository recipe
// from the working tree, exactly as a person's launch does.
func batchLaunchCheckout(t *testing.T) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	testutil.WriteFiles(t, dir, map[string]string{
		"fugaro.yaml":                  "version: 1\nproject: aurora\ngit: { provider: github }\n",
		".fugaro/recipes/default.yaml": implementRecipe,
	})
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "-q", "-m", "the repository's own recipe")
	t.Chdir(dir)
}
