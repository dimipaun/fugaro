package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	batchpkg "github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
)

// batchAnswers is what a Batch launch's world is answered with: the ticket
// route minting, and the registration route BR17's second call reaches --
// without which every world here would meet a 404 on the registration and fail
// for a reason it is not about. An override replaces one route's answer.
func batchAnswers(expires time.Time, override ...any) map[string]ticketAnswer {
	ans := ticketAnswers(expires)
	ans[identitywire.JobPath] = ticketAnswer{Status: http.StatusOK, Body: `{}`}
	if len(override) == 2 {
		ans[override[0].(string)] = override[1].(ticketAnswer)
	}
	return ans
}

// jobObject reads one created Batch job's document by the path provider
// blocks nest by (locations_end_to_end_test.go's at), and fails the test where
// the path is not there: the variables a launch's entries land in are nested
// inside the task group's own spec.
func jobObject(t *testing.T, v any, path ...any) map[string]any {
	t.Helper()
	m, ok := at(v, path...).(map[string]any)
	if !ok {
		t.Fatalf("the created job has no %v: %v", path, v)
	}
	return m
}

// batchAskAccount is the account a Batch launch's ask names (runAccount): the
// string is carried to the service and judged there, and these tests never sign
// as it.
const batchAskAccount = "launch-account@proj-1234.iam.gserviceaccount.com"

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

// batchBlockOptedIn is the same block with the opt-in fact on it (BR1, R3,
// D3110): the launch gate reads the fact from the block this machine holds, and
// a launch on this backend proceeds only where it is `in`.
const batchBlockOptedIn = batchBlockForLaunch + "  opt_in: in\n"

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
		{"an installation that carries no credentials service, on Batch", batchBlockOptedIn, true, backend.BatchCodec(), true},
		{"a repository whose record says its root was not applied with the switch on, on Batch", batchBlockOptedIn + proxiedBlock, false, backend.BatchCodec(), true},
		{"a repository the service serves, on Batch", batchBlockOptedIn + proxiedBlock, true, backend.BatchCodec(), false},
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

// TestABatchLaunchWithoutTheOptInIsRefusedWithItsFix is BR1's gate at the
// launcher (R3, R17, O15): a launch on this backend whose block carries no
// opt-in fact, or carries the written withdrawal, is refused before anything is
// created -- and the fix names the write that publishes the fact and the
// `fugaro update` that applies the installation to it.
//
// The three states are one row each, because they are one refusal with one fix
// and the sentence has to name the state it found: an installation that never
// opted in is not one an operator opted out, and a person told "not opted in"
// by an installation that recorded the withdrawal would not know the fact had
// ever been written.
//
// The rows are judged on a repository the credentials service really serves
// (the block and the repository's own record are the admitted pair), so what
// refuses is the fact alone: drop the gate and the first two rows are launches
// this installation would make.
func TestABatchLaunchWithoutTheOptInIsRefusedWithItsFix(t *testing.T) {
	withdrawn := &localcfg.Batch{OptIn: localcfg.BatchOptInWithdrawn}
	for _, tc := range []struct {
		name    string
		fact    *localcfg.Batch
		be      backend.Codec
		refused bool
	}{
		{"an installation whose block carries no opt-in fact", nil, backend.BatchCodec(), true},
		{"an installation whose block records the withdrawal", withdrawn, backend.BatchCodec(), true},
		{"an installation that opted in", &localcfg.Batch{OptIn: localcfg.BatchOptInOn}, backend.BatchCodec(), false},
		{"the same installation with no fact, on Cloud Run", nil, backend.CloudRunCodec(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchBudget(t, "enforce")
			addProxiedRepo(t, f, releasePolicy, proxiedBlock, true)
			publishPolicy(t, f, proxiedPolicy)
			_, spec := proxiedLaunchSpec()
			env := fileEnv(t, f.cloudFixture)
			env.be = backendNamed{codec: tc.be}
			env.lc.Batch = tc.fact
			err := batchLaunchRefusal(context.Background(), env, spec)
			switch {
			case tc.refused && err == nil:
				t.Fatal("a Batch launch of an installation that has not opted in was admitted: the run it starts holds no identity, since the ticket is what the service issues and the fact is what puts the service there (R3, R17)")
			case tc.refused:
				if ExitCode(err) != ExitUserError {
					t.Errorf("the refusal is not a user error: %v", err)
				}
				for _, want := range []string{"batch.opt_in", spec.Repo, backend.CloudBatch, selfCommand() + " update", "backends use cloud-batch", "nothing was created"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not name %q: %v", want, err)
					}
				}
				state := "absent"
				if tc.fact != nil {
					state = "withdrawn"
				}
				if !strings.Contains(err.Error(), state) {
					t.Errorf("the refusal does not say the fact is %s: %v", state, err)
				}
			case err != nil:
				t.Fatalf("a launch the installation opted in for, or one on another backend, was refused: %v", err)
			}
		})
	}
}

// batchLaunchWorld is one launch on an installation that selected the Batch
// backend: the fixture, the Cloud Batch fake its job is created on, the
// credentials service that issues the ticket, and the environment the launch
// runs in. It is ticketWorld's shape for the other backend.
type batchLaunchWorld struct {
	f    *launchBudgetFixture
	bf   *gcpfake.Batch
	svc  *launchService
	env  *cloudEnv
	spec *task.Spec
}

// newBatchLaunchWorld builds it: the installation's config selects the
// backend, carries the opt-in fact and names the service, and the backend the
// launch runs on is the real one, opened from that config against the fakes --
// so what a launch does here is what a launch does.
func newBatchLaunchWorld(t *testing.T, answers map[string]ticketAnswer) *batchLaunchWorld {
	t.Helper()
	bf := gcpfake.NewBatch(t)
	bf.Project, bf.Region = "proj-1234", "us-east5"
	f := &launchBudgetFixture{cloudFixture: newCloudFixture(t, "batch: "+bf.URL+"/")}
	f.db, f.iam = f.cloudFixture.budget.DB, f.cloudFixture.iam
	f.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: "+f.db.URL+", token_signer: "+testSigner+
		", firebase_api_key: "+settingsFakeAPIKey+" }\n")
	f.db.Set("fugaro/project", "aurora")
	f.db.Set(budget.PathMark, auroraMark())
	f.db.Set("config/mode", "enforce")
	seedLaunchCaps(f.db)
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", releaseIdentity)
	// The service that issues the ticket, and the two facts that make this
	// repository one it serves: the installation's block addresses it, and the
	// repository's own record says its root was applied with the switch on.
	svc := newLaunchService(t, answers)
	f.appendConfig(t, batchBlockOptedIn+"credentials_service:\n  url: "+svc.serviceURL()+"\n")
	markRepoCredentialsServiceReady(t, f.cloudFixture)

	env := fileEnv(t, f.cloudFixture)
	be, err := backend.Open(context.Background(), backend.CloudBatch, cloudOpeners(env.lc, gcp.Options{}))
	if err != nil {
		t.Fatalf("opening the Batch backend the world's config selects: %v", err)
	}
	env.be = be
	prevIdentity, prevSelf := launchIdentity, selfCommand
	prevAsk := launchAskTicket
	launchAskTicket = func(ctx context.Context, i *launchIssuer, req identitywire.TicketRequest) (identitywire.TicketResponse, error) {
		return i.askRetry(ctx, req)
	}
	launchIdentity = func(context.Context, string) (string, error) { return ticketIdentity, nil }
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() {
		launchIdentity, selfCommand, launchAskTicket = prevIdentity, prevSelf, prevAsk
	})
	return &batchLaunchWorld{f: f, bf: bf, svc: svc, env: env, spec: raceSpec(t, env)}
}

// launch runs the world's launch, answering what the Batch fake was asked.
func (w *batchLaunchWorld) launch(t *testing.T) ([]gcpfake.BatchCall, error) {
	t.Helper()
	_, err := launchRun(context.Background(), w.env, appSlug, w.spec, time.Now())
	return w.bf.Creates(), err
}

// variantOf is the workflow a launch creates its job with: run.go's own
// derivation (jobWorkflow), which is the string the backend is handed as
// backend.LaunchSpec.Workflow and the one both backends name a job by.
func variantOf(t *testing.T, spec *task.Spec) string {
	t.Helper()
	v, ok := jobWorkflow(spec.Workflow, spec.OAuthToken)
	if !ok {
		t.Fatalf("jobWorkflow(%q, %q) answered no variant", spec.Workflow, spec.OAuthToken)
	}
	return v
}

// jobName is the full resource name of the job the world's launch created, read
// off the Batch fake's own record of the create: the platform's name for the job
// that really exists, and not the launcher's derivation of it, so an ask or a
// registration naming some other job cannot agree with this by construction.
func (w *batchLaunchWorld) jobName(t *testing.T, creates []gcpfake.BatchCall) identitywire.JobName {
	t.Helper()
	if len(creates) != 1 {
		t.Fatalf("jobs.create calls = %d, want 1", len(creates))
	}
	return identitywire.JobName("projects/" + w.bf.Project + "/locations/" + w.bf.Region + "/jobs/" + creates[0].JobID)
}

// batchNamedAccount is the Claude account the named-account launches below run
// as: a name the account axis accepts, and the reason a workflow's job is the
// variant <workflow>_<account> rather than the plain workflow.
const batchNamedAccount = "work"

// TestTheTicketNamesTheJob is R1 and R18 at the launcher: a Batch launch asks
// for its run's ticket naming the FULL resource name of the job it is about to
// create, and a Cloud Run launch -- every launch of that backend -- names none.
//
// The launch driven here runs on a NAMED Claude account, which is the one axis
// where the workflow in the task and the workflow the job is created with come
// apart (gcp.WorkflowVariant): the job that really exists is the variant's, and
// an ask derived from the plain workflow names a job no create ever makes -- the
// service issues a ticket for nothing, the id read-back finds no job, and the
// launch cancels the job it just created. The expectation is the fake's own
// record of the create and never the launcher's derivation, so the two cannot
// agree by construction.
//
// A launch that asked without a job at all would be issued a record naming
// none, and the VM that claims the run is held to a job of that name (BR17,
// BR2) -- so the run would stop at its claim with a job already created and a
// launcher long gone.
func TestTheTicketNamesTheJob(t *testing.T) {
	expires := time.Now().Add(30 * time.Minute)
	batch := newBatchLaunchWorld(t, batchAnswers(expires))
	batch.spec.OAuthToken = batchNamedAccount
	creates, err := batch.launch(t)
	if err != nil {
		t.Fatalf("a Batch launch of a repository the service serves was refused: %v", err)
	}
	asked := batch.svc.asked()
	if len(asked) == 0 {
		t.Fatal("the service received no ask: a Batch run's identity is its ticket (R1)")
	}
	req := ticketAskOf(t, asked[0])
	want := batch.jobName(t, creates)
	if string(want) == "" {
		t.Fatal("the Batch fake holds no name for the job the launch created")
	}
	if req.Job != want {
		t.Errorf("the ask names the job %q, want %q: the service writes it on the run's record, and the claiming VM is held to it", req.Job, want)
	}
	// The job that exists is the variant's, and the plain workflow's is not it:
	// this is what makes the assertion above able to fail.
	variant := variantOf(t, batch.spec)
	if got := creates[0].JobID; got != batchpkg.JobID(appSlug, variant, batch.spec.RunID) {
		t.Errorf("the launch created the job %q, want the backend's JobID over the variant %q", got, variant)
	}
	if plain := batchpkg.JobID(appSlug, batch.spec.Workflow, batch.spec.RunID); plain == creates[0].JobID {
		t.Errorf("the launch created the job %q, which is JobID over the PLAIN workflow %q: the run is on the named account %q", creates[0].JobID, batch.spec.Workflow, batchNamedAccount)
	}

	// R18's other half: a Cloud Run launch names no job at all.
	run := servedTicketWorld(t, releaseIdentity, ticketAnswers(expires))
	if _, err := run.env.launchTicketFor(context.Background(), appSlug, run.spec, batchAskAccount, variantOf(t, run.spec), nil, false, time.Now()); err != nil {
		t.Fatalf("a Cloud Run launch of the same repository was refused: %v", err)
	}
	runAsked := run.svc.asked()
	if len(runAsked) != 1 {
		t.Fatalf("the Cloud Run service received %d ask(s), want 1", len(runAsked))
	}
	if got := ticketAskOf(t, runAsked[0]).Job; got != "" {
		t.Errorf("a Cloud Run launch names the job %q: that backend's launches name none (R18)", got)
	}
}

// TestTheLaunchNamesTheServicesReason is O15 at the launcher: where the ticket
// cannot be obtained, the refusal prints the reason the SERVICE gave -- C1's own
// code -- and never the service's free text, which is another writer's.
//
// The reasons are the four the route answers with, and the fix each carries is
// the operator's: a service whose revision holds no issuer, or one of a release
// behind, is an installation to update (and on this backend it is a refusal,
// not R7's bridge: a Batch job has no carrier for the sealed identity a bridged
// launch would hand over). A caller the roster does not admit, or a repository
// it may not launch from, is a fact about the roster `fugaro update` renders.
func TestTheLaunchNamesTheServicesReason(t *testing.T) {
	// freeText is what the service says beside the code: it must not travel.
	const freeText = "a message this CLI must never repeat"
	for _, tc := range []struct {
		name string
		ans  ticketAnswer
		code string
	}{
		{"a revision whose identity half is not configured", ticketNotServed, identitywire.RefusalNotServed},
		{"a caller the roster does not admit", ticketAnswer{Status: http.StatusForbidden, Body: `{"error":"` + identitywire.RefusalCallerNotAdmitted + `","message":"` + freeText + `"}`}, identitywire.RefusalCallerNotAdmitted},
		{"a repository this caller may not launch", ticketAnswer{Status: http.StatusForbidden, Body: `{"error":"` + identitywire.RefusalNoLaunchScope + `","message":"` + freeText + `"}`}, identitywire.RefusalNoLaunchScope},
		{"an identity the run's record cannot take", ticketAnswer{Status: http.StatusForbidden, Body: `{"error":"` + identitywire.RefusalIdentityRefused + `","message":"` + freeText + `"}`}, identitywire.RefusalIdentityRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newBatchLaunchWorld(t, batchAnswers(time.Now().Add(30*time.Minute), identitywire.TicketPath, tc.ans))
			_, err := w.launch(t)
			if err == nil {
				t.Fatal("a Batch launch whose ticket the service refused was admitted: the run it would start holds no identity (R18)")
			}
			if !strings.Contains(err.Error(), tc.code) {
				t.Errorf("the refusal does not print the reason the service gave (%s): %v", tc.code, err)
			}
			if strings.Contains(err.Error(), freeText) {
				t.Errorf("the refusal repeats the service's own text, which this CLI never does: %v", err)
			}
			if !strings.Contains(err.Error(), selfCommand()) {
				t.Errorf("the refusal names no fix: %v", err)
			}
			if n := len(w.bf.Creates()); n != 0 {
				t.Errorf("a refused launch created %d job(s): the refusal is before the create", n)
			}
		})
	}
}

// TestAFailedTicketCreatesNothing is BR1's ordering at its hardest (R17, O15):
// a Batch launch whose ticket cannot be obtained is refused before ANYTHING is
// created -- no job on the backend, no execution, no stored task, and no budget
// token minted, since this machine's own signer is never asked. The run such a
// launch would start would hold no identity, and the launcher is the last place
// that can be said while a person is watching.
func TestAFailedTicketCreatesNothing(t *testing.T) {
	w := newBatchLaunchWorld(t, batchAnswers(time.Now().Add(30*time.Minute), identitywire.TicketPath, ticketRefused))
	if _, err := w.launch(t); err == nil {
		t.Fatal("a Batch launch whose ticket the service refused was admitted")
	}
	if n := len(w.bf.IDs()); n != 0 {
		t.Errorf("the refused launch left %d job(s) on the backend: %v", n, w.bf.IDs())
	}
	if launchedAny(w.f) {
		t.Error("the refused launch started an execution")
	}
	if storedTask(t, w.f.cloudFixture) {
		t.Error("the refused launch stored a task")
	}
	if tok := readToken(t, w.f.cloudFixture, w.spec.RunID); tok != "" {
		t.Error("the refused launch minted a budget token")
	}
	if n := len(w.f.iam.SignCalls()); n != 0 {
		t.Errorf("the refused launch reached the signer %d time(s): nothing of this run was created, so nothing was minted", n)
	}
}

// TestTheTicketIsRedactedAndNotRecorded is R7 and D3115 at the launcher: the
// ticket a Batch launch holds reaches the job and nothing else -- it is on the
// job's own variables under the name the run claims it by, it is in no run
// record, and this launch writes no sealed object for it either (a launch that
// holds a ticket signs nothing locally and writes no token object).
//
// The record is read back as bytes because that is what a reader outside this
// binary consumes: a field a launcher added would travel there, and the ticket
// is a one-time secret of one execution.
func TestTheTicketIsRedactedAndNotRecorded(t *testing.T) {
	w := newBatchLaunchWorld(t, batchAnswers(time.Now().Add(30*time.Minute)))
	creates, err := w.launch(t)
	if err != nil {
		t.Fatalf("a Batch launch of a repository the service serves was refused: %v", err)
	}
	if len(creates) != 1 {
		t.Fatalf("jobs.create calls = %d, want 1", len(creates))
	}
	vars := jobObject(t, creates[0].Job, "taskGroups", 0, "taskSpec", "environment", "variables")
	if got := vars[identitywire.TicketEnv]; got != ticketValue {
		t.Errorf("the job's %s = %v, want the ticket the service issued: it is what the run claims its identity with", identitywire.TicketEnv, got)
	}
	// The job carries no sealed entry and never the marker: those are the
	// budget token's, and this backend refuses both (BR1, F21, R8).
	env := jobObject(t, creates[0].Job, "taskGroups", 0, "taskSpec", "environment")
	for _, field := range []string{"secretVariables", "encryptedVariables"} {
		if got, ok := env[field]; ok {
			t.Errorf("the job carries %v under %s: a Batch job's only secret carrier is its own definition, which BR1's launch puts the ticket in as a plain variable", got, field)
		}
	}
	if _, ok := vars[gitops.BudgetTokenEnv]; ok {
		t.Errorf("the job's variables carry %s: the marker's value is the seal key and it is never a job variable (R5)", gitops.BudgetTokenEnv)
	}
	if tok := ticketObject(t, w.env, w.spec.RunID); tok != "" {
		t.Error("a launch that holds a ticket wrote a sealed token object into the bucket (R7)")
	}
	rec, ok := ticketLaunchRecord(t, w.env, w.spec.RunID)
	if !ok {
		t.Fatal("the launch recorded nothing: every run has a launch record")
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal the launch record: %v", err)
	}
	for _, secret := range []string{ticketValue, ticketIdentity} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the run's launch record carries %q: the ticket is a one-time secret of one execution and it is in no record (D3115): %s", secret, raw)
		}
	}
}

// TestTheJobIsRegisteredAfterTheCreateWithItsId is BR1's second call at the
// launcher (R1, BR17): after the create returned, the job is registered with
// the service by its own full name and the id the platform assigned it -- the
// write that lets the VM claiming this run be held to this execution.
//
// The registration is read off the service's own calls, because that is what
// the contract is: one POST to the registration route, carrying the run, the
// job and the id. The id compared is the one the Batch fake assigned the job it
// really created, so a launcher that registered a name, a zero value or a
// second derivation fails here.
func TestTheJobIsRegisteredAfterTheCreateWithItsId(t *testing.T) {
	w := newBatchLaunchWorld(t, batchAnswers(time.Now().Add(30*time.Minute)))
	creates, err := w.launch(t)
	if err != nil {
		t.Fatalf("a Batch launch of a repository the service serves was refused: %v", err)
	}
	if len(creates) != 1 {
		t.Fatalf("jobs.create calls = %d, want 1", len(creates))
	}
	id, _ := w.bf.JobJSON("proj-1234", "us-east5", creates[0].JobID)["uid"].(string)
	if id == "" {
		t.Fatal("the fake holds no id for the job the launch created: the registration below has nothing to be checked against")
	}
	var seen []identitywire.JobRequest
	for _, call := range w.svc.asked() {
		if call.Path != identitywire.JobPath {
			continue
		}
		var req identitywire.JobRequest
		if err := json.Unmarshal([]byte(call.Body), &req); err != nil {
			t.Fatalf("the registration the service received is not this contract's: %v (%q)", err, call.Body)
		}
		seen = append(seen, req)
	}
	if len(seen) != 1 {
		t.Fatalf("the service received %d registration(s), want 1: a launch that created a job owes the service its id, and a second write is refused by the record's own fence", len(seen))
	}
	req := seen[0]
	if req.Slug != appSlug || req.Run != w.spec.RunID {
		t.Errorf("the registration names %s/%s, want %s/%s", req.Slug, req.Run, appSlug, w.spec.RunID)
	}
	if req.Job != w.jobName(t, creates) {
		t.Errorf("the registration names the job %q, want the one this launch created (%q): the two are one job (BR17)", req.Job, w.jobName(t, creates))
	}
	if req.JobID != id {
		t.Errorf("the registration carries the id %q, want the one Batch assigned this job (%q): the name is the launcher's and the id is the platform's (D3108)", req.JobID, id)
	}
}

// seedBatchJob puts a job of the given id into the Batch fake the way a create
// that really landed leaves it -- a call to the fake itself, because the create
// the launch below makes is the ambiguous one and creates nothing through the
// client -- and answers the id the platform assigned it. The uid is then
// replaced with a sentinel, so a registration that carries it can only have read
// THIS job back.
func seedBatchJob(t *testing.T, bf *gcpfake.Batch, id, uid string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, bf.URL+"/v1/projects/"+bf.Project+"/locations/"+bf.Region+"/jobs?jobId="+id, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("seeding the Batch job %s: %v", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seeding the Batch job %s: status %d", id, resp.StatusCode)
	}
	bf.SetUID(id, uid)
}

// ambiguousJobUID is the id the seeded job above is given: a value no create
// could answer, so a registration carrying it proves the read-back reached that
// job.
const ambiguousJobUID = "uid-of-the-job-the-ambiguous-create-made"

// TestTheJobIsRegisteredAfterAnAmbiguousCreate is the second half of the call
// above, on the branch where the create's outcome is unknown (R1, BR17).
//
// A Batch create that answered a timeout, a 5xx or a dropped connection may have
// made its job all the same. A job nothing registers is a VM that runs, is
// billed and stops at its claim -- and no later --retry can put that right:
// BR17's route writes the id once, on the record the ask wrote, and only the
// launcher holding the claim may write it. So the id is read back here too, and
// registered; the launch still fails with the ambiguity it always did, because
// what a person must not do is relaunch before the claim goes stale.
//
// The job is seeded before the launch and the create is made to fail, so this is
// the case the finding describes: the platform holds the job, the client was
// told nothing. The uid compared is the sentinel SeedBatchJob set, so a
// registration of anything but that job fails here.
//
// The second half is the create that never happened -- the ordinary answer to
// this branch -- which must not register, must not cancel a job that is not
// there and must not warn: the launch's own message is the one it always was.
func TestTheJobIsRegisteredAfterAnAmbiguousCreate(t *testing.T) {
	expires := time.Now().Add(30 * time.Minute)
	w := newBatchLaunchWorld(t, batchAnswers(expires))
	id := batchpkg.JobID(appSlug, variantOf(t, w.spec), w.spec.RunID)
	seedBatchJob(t, w.bf, id, ambiguousJobUID)
	w.bf.FailCreateWith = http.StatusServiceUnavailable
	var warned strings.Builder
	prevWarn := launchWarn
	launchWarn = &warned
	t.Cleanup(func() { launchWarn = prevWarn })

	_, err := w.launch(t)
	if err == nil {
		t.Fatal("a launch whose create answered 503 was not refused: the outcome is unknown and the operator must not relaunch before the claim goes stale")
	}
	if !strings.Contains(err.Error(), "the outcome is unknown") {
		t.Errorf("the launch of the ambiguous create failed with %q, want the ambiguity it always reported", err)
	}
	if strings.Contains(err.Error(), "could not be registered") {
		t.Errorf("the launch reported a registration failure (%q): the seeded job is there to be read back and registered", err)
	}
	var seen []identitywire.JobRequest
	for _, call := range w.svc.asked() {
		if call.Path != identitywire.JobPath {
			continue
		}
		var req identitywire.JobRequest
		if err := json.Unmarshal([]byte(call.Body), &req); err != nil {
			t.Fatalf("the registration the service received is not this contract's: %v (%q)", err, call.Body)
		}
		seen = append(seen, req)
	}
	if len(seen) != 1 {
		t.Fatalf("the service received %d registration(s), want 1: a job the ambiguous create may have made is read back and registered here, or nothing can ever claim its identity", len(seen))
	}
	if got := seen[0]; got.Slug != appSlug || got.Run != w.spec.RunID || got.JobID != ambiguousJobUID || !strings.HasSuffix(string(got.Job), "/jobs/"+id) {
		t.Errorf("the registration carries %+v, want %s/%s of the job %s with the id %q: the job the create made is the one read back",
			got, appSlug, w.spec.RunID, id, ambiguousJobUID)
	}
	if got := w.bf.Cancels(); len(got) != 0 {
		t.Errorf("the launch cancelled %v: the job was registered, so there is nothing to stop", got)
	}
	// Only the job's own warnings are read here: the world's launch warns about
	// a slip besides, which is the fixture's and not this branch's.
	if got := warned.String(); strings.Contains(got, "Batch job") || strings.Contains(got, id) {
		t.Errorf("the launch warned %q about a job it registered: a warning is for a job left behind, and this one is not", got)
	}

	// The ordinary case of the same branch: the create made nothing, so the
	// read-back finds no job, and that is not this launch's failure.
	none := newBatchLaunchWorld(t, batchAnswers(expires))
	none.bf.FailCreateWith = http.StatusServiceUnavailable
	noneID := batchpkg.JobID(appSlug, variantOf(t, none.spec), none.spec.RunID)
	var noneWarned strings.Builder
	launchWarn = &noneWarned
	_, err = none.launch(t)
	if err == nil {
		t.Fatal("a launch whose create answered 503 with no job behind it was not refused")
	}
	if !strings.Contains(err.Error(), "the outcome is unknown") {
		t.Errorf("the launch of the ambiguous create failed with %q, want the ambiguity it always reported", err)
	}
	if strings.Contains(err.Error(), "could not be registered") {
		t.Errorf("the launch reported a registration failure (%q): the create made nothing, which is the ordinary answer to this branch", err)
	}
	for _, call := range none.svc.asked() {
		if call.Path == identitywire.JobPath {
			t.Errorf("the service received a registration (%q) for a create that made no job", call.Body)
		}
	}
	if got := none.bf.Cancels(); len(got) != 0 {
		t.Errorf("the launch cancelled %v: no job of that name exists to cancel", got)
	}
	if got := noneWarned.String(); strings.Contains(got, "Batch job") || strings.Contains(got, noneID) {
		t.Errorf("the launch warned %q about a job that does not exist: the create made nothing, and a warning here would name a job to delete", got)
	}
}

// TestABatchLaunchCarriesTheTicketAndNothingElse is BR1's acceptance at the
// launcher, seen whole: a launch on an installation whose block carries the
// opt-in fact asks for the run's ticket naming the job it is about to create,
// puts it on that job under identitywire.TicketEnv, and the job carries nothing
// else of this launch's secrets.
//
// The red step is the marker among the job's variables and the ticket set
// without the job's name: neither can pass here, because the job's variables
// are read back from the fake's own record of the create.
func TestABatchLaunchCarriesTheTicketAndNothingElse(t *testing.T) {
	w := newBatchLaunchWorld(t, batchAnswers(time.Now().Add(30*time.Minute)))
	creates, err := w.launch(t)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if len(creates) != 1 {
		t.Fatalf("jobs.create calls = %d, want 1", len(creates))
	}
	asked := w.svc.asked()
	if len(asked) == 0 {
		t.Fatal("the launch asked the service for nothing: a Batch run's identity is its ticket (R1)")
	}
	if got := ticketAskOf(t, asked[0]).Job; got != w.jobName(t, creates) {
		t.Fatalf("the ask names the job %q, want %q: the ticket is issued for the job this launch is about to create", got, w.jobName(t, creates))
	}
	vars := jobObject(t, creates[0].Job, "taskGroups", 0, "taskSpec", "environment", "variables")
	if got := vars[identitywire.TicketEnv]; got != ticketValue {
		t.Fatalf("the job's %s = %v, want the ticket the service issued", identitywire.TicketEnv, got)
	}
	delete(vars, identitywire.TicketEnv)
	for _, reserved := range []string{gitops.BudgetTokenEnv, runner.BudgetTokenEnv, "FUGARO_SLIP"} {
		if _, ok := vars[reserved]; ok {
			t.Errorf("the job's variables carry %s: the only thing a Batch launch adds to them is the ticket", reserved)
		}
	}
}
