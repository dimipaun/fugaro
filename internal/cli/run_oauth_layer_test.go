package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The refusal of task 3.4a must ask the question about the document the run
// carries, not about the launcher's own resolution of the checkout
// (projectJobAgentAuth, internal/cli/project_job.go).
//
// What a run resolves its agent.auth from is the fugaro.yaml at the ref it
// checks out, over the project layer its task.json carries (config.Resolve, the
// runner's own resolution). The launcher holds a different pair of moments:
// checkoutConfig resolves the checkout leniently (decision L16), and — on a
// repeated --run-id — createTask adopts the *stored* task's project layer over
// whatever embedProjectLayer has just resolved (run.go: repeatTaskBytes leaves
// the layer out of "is this the same task", and createTask's own comment says a
// layer republished between the first launch and the repeat makes the two
// differ by design, because the stored task is what the job reads).
//
// The tests below pin both halves: the auth is re-resolved over the layer the
// run carries, and a credential that cannot be resolved at all refuses the
// launch rather than guessing.

// projectJobOAuthLayer is a project layer whose defaults put every workflow
// that resolves over it on `auth: oauth`, for the fixture's anchored checkout
// (acme/app, the checkout: clone workflow web the local config names).
const projectJobOAuthLayer = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  agent: { auth: oauth }
`

// projectJobAnchoredRepoYAML is projectJobRepoYAML with the two lines every
// project layer needs before it applies to a file at all (config.LayerAnchor:
// a real project: and gcp_project: pair). Its own agent block is deliberately
// absent: what this run's auth is comes from the layer's defaults, the case a
// launcher reading its checkout's own resolution alone gets wrong.
const projectJobAnchoredRepoYAML = `version: 1
project: aurora
gcp_project: proj-1234
git:
  provider: github
  base_branch: main
workflows:
  web:
    checkout: clone
    commands:
      build: sh build.sh
      test: sh test.sh
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
`

// TestSharedJobRefusalUsesTheLayerTheRunCarries is the stale-layer case the
// owner's ruling of 2026-10-17 is about: a task stored while the project layer
// still said `agent.auth: oauth`, repeated after the layer stopped saying it.
// The run reads its stored layer (createTask adopts it, and the runner applies
// it over the ref's fugaro.yaml), so it is an OAuth launch onto a shared job —
// while the launcher's own, freshly resolved layer says api-key.
//
// Mutation (run, restore): pass co.Agent.Auth to checkProjectJobOAuth again
// (what launchProjectJobFor read before this change) — the second attempt
// launches on the shared project job, and the run halts inside Cloud Run with
// the credential the job has no token for.
func TestSharedJobRefusalUsesTheLayerTheRunCarries(t *testing.T) {
	f := newProjectJobFixture(t)
	isolateCache(t)
	publishedLayer(t, f.cloudFixture, projectJobOAuthLayer)
	projectJobCheckoutYAML(t, projectJobAnchoredRepoYAML)

	const run = "20261018-120000-aaaa"
	// The first attempt: the layer puts this run on `auth: oauth`, so it is
	// refused (task 3.4a) — and it stores task.json first, because the job is
	// decided after createTask (run.go's finishLaunch), which is what makes the
	// stored layer the one a repeat carries. No claim is taken by a refused
	// launch, so the repeat below is not blocked by one.
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "phase 5") {
		t.Fatalf("the first attempt, whose layer says oauth: %v", err)
	}
	if pl := readSpec(t, f.cloudFixture, run).ProjectLayer; pl == nil || !strings.Contains(pl.YAML, "auth: oauth") {
		t.Fatalf("the stored task's project layer = %+v, want the layer the first attempt resolved", pl)
	}

	// The layer is republished between the two attempts without the OAuth
	// auth: the launcher's own resolution of the same checkout now says
	// api-key, while the run carries the stored layer.
	writeBucketFile(t, f.cloudFixture, config.LayerKey, strings.Replace(projectJobOAuthLayer, "  agent: { auth: oauth }\n", "  agent: { auth: api-key }\n", 1))

	claimed := false
	launchHooks.beforeClaim = func() { claimed = true }
	t.Cleanup(func() { launchHooks.beforeClaim = nil })

	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "phase 5") || !strings.Contains(err.Error(), "standard") {
		t.Fatalf("the repeat, whose task carries the oauth layer the first attempt stored: %v, want the same refusal the first attempt got", err)
	}
	if claimed {
		t.Error("the refusal came after the launch claim: the claim, the budget token and the Cloud Run call all happen past it")
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none", n)
	}
	if calls := f.run.RunRequests(); len(calls) != 0 {
		t.Errorf("the refused launch made %d jobs.run calls (%+v), want none", len(calls), calls)
	}
}

// TestSharedJobRefusalWhenTheCredentialCannotBeResolved pins the other half of
// the rule: a launch onto a shared project job whose own credential cannot be
// resolved is refused rather than started. An unknown credential may be an
// OAuth one, and the job it would land on holds no account's token to serve it
// with; a run that cannot be judged must not be launched on a guess.
//
// The credential here is the checkout's fugaro.yaml, read again for the
// launch's own resolution (embedProjectLayer's read is the command's first):
// the second read fails, so the launcher cannot tell what auth the run
// resolves to.
//
// Mutation (run, restore): return "" from projectJobAgentAuth's read failure
// instead of refusing — the launch takes the claim, mints a budget token and
// starts an execution on the shared job, judged on a credential nobody
// resolved.
func TestSharedJobRefusalWhenTheCredentialCannotBeResolved(t *testing.T) {
	f := newProjectJobFixture(t)
	isolateCache(t)
	publishedLayer(t, f.cloudFixture, strings.Replace(projectJobOAuthLayer, "auth: oauth", "auth: api-key", 1))
	projectJobCheckoutYAML(t, projectJobAnchoredRepoYAML)

	read := readCheckoutFugaroYAML
	t.Cleanup(func() { readCheckoutFugaroYAML = read })
	calls := 0
	readCheckoutFugaroYAML = func(path string) ([]byte, error) {
		calls++
		if calls > 1 {
			return nil, errors.New("permission denied")
		}
		return read(path)
	}

	claimed := false
	launchHooks.beforeClaim = func() { claimed = true }
	t.Cleanup(func() { launchHooks.beforeClaim = nil })

	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261018-130000-bbbb", "Add a feature")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cannot tell which Claude credential") || !strings.Contains(err.Error(), "acme/app") {
		t.Fatalf("a launch whose own credential cannot be resolved: %v, want a refusal naming the repository", err)
	}
	if claimed {
		t.Error("the refusal came after the launch claim")
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none", n)
	}
}

// TestSharedJobRefusalWhenTheCarriedLayerIsInvalid is the stored layer that no
// longer parses: the bytes in task.json are the run's own, whatever wrote them
// (an older binary's spelling of a key today's decoder rejects, an operator's
// hand), and the launcher cannot resolve a credential from them. It refuses
// instead of resolving against today's layer, which is not what the run
// carries — the run would read the stored one.
//
// Mutation (run, restore): fall through with a nil layer instead of refusing on
// the parse's problems (projectJobAgentAuth) — the launch then resolves the
// checkout alone, whose own agent block is unset, so the auth reads "" and
// checkProjectJobOAuth finds no oauth in it: the run launches on the shared
// project job carrying the OAuth layer the stored task dictates, which is the
// launch task 3.4a exists to refuse. This test (and the one above) then fail on
// the first attempt already, since the stored layer is never consulted.
func TestSharedJobRefusalWhenTheCarriedLayerIsInvalid(t *testing.T) {
	f := newProjectJobFixture(t)
	isolateCache(t)
	publishedLayer(t, f.cloudFixture, projectJobOAuthLayer)
	projectJobCheckoutYAML(t, projectJobAnchoredRepoYAML)

	const run = "20261018-140000-cccc"
	// A first attempt stores task.json and stops at the OAuth refusal (the same
	// oauth layer as the test above): what it leaves behind is a stored task
	// with no launch, which is the state this test needs.
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "phase 5") {
		t.Fatalf("the first attempt: %v", err)
	}
	// The stored layer is then replaced by bytes the decoder refuses — what an
	// older binary's spelling, or a hand-edited task.json, would leave. Its
	// own task is untouched: a repeat is judged on everything the caller chose,
	// never on the layer (repeatTaskBytes).
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s := runstore.Open(b, appSlug, run)
	spec, err := s.ReadTask(ctx)
	if err != nil {
		t.Fatalf("task.json of the first attempt: %v", err)
	}
	if spec.ProjectLayer == nil {
		t.Fatal("the first attempt stored no project layer; this test needs one to corrupt")
	}
	bad := "version: 99\nproject: aurora\ngcp_project: proj-1234\n"
	spec.ProjectLayer = &task.ProjectLayer{SHA256: config.LayerSum([]byte(bad)), YAML: bad}
	if err := s.WriteTask(ctx, spec); err != nil {
		t.Fatal(err)
	}

	_, _, err = execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cannot tell which Claude credential") || !strings.Contains(err.Error(), "the project layer its task carries is invalid") {
		t.Fatalf("a repeat whose stored layer no longer parses: %v, want the carried layer named", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none", n)
	}
}
