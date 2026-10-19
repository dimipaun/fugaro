package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The account gate (projectJobAccountFor) must ask its question about the document the run
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

// projectJobProfiledRepoYAML is projectJobAnchoredRepoYAML whose workflow takes
// one of the layer's workflow templates (`profile:`, the layer's own selection;
// job_profile is the published policy's job). A file that names a template
// resolves only over a layer that defines it, which is what makes the carried
// layer the one that decides whether this checkout resolves at all.
const projectJobProfiledRepoYAML = `version: 1
project: aurora
gcp_project: proj-1234
git:
  provider: github
  base_branch: main
workflows:
  web:
    profile: p
    commands:
      build: sh build.sh
      test: sh test.sh
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
`

// projectJobProfiledOAuthLayer is projectJobOAuthLayer plus the template
// projectJobProfiledRepoYAML's workflow selects: the copy that applies to the
// checkout is a complete one, so the launcher's own resolution of it succeeds
// and the launch reaches the resolution this test is about.
const projectJobProfiledOAuthLayer = projectJobOAuthLayer + "profiles:\n  p: { checkout: clone }\n"

// TestSharedJobRefusalUsesTheLayerTheRunCarries is the stale-layer case the
// owner's ruling of 2026-10-17 is about: a task stored while the project layer
// still said `agent.auth: oauth`, repeated after the layer stopped saying it.
// The run reads its stored layer (createTask adopts it, and the runner applies
// it over the ref's fugaro.yaml), so it is an OAuth launch onto a shared job —
// while the launcher's own, freshly resolved layer says api-key.
//
// Mutation (run, restore): pass co.Agent.Auth to projectJobAccountFor again
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
	// refused by the account gate (the fixture's profile serves no Claude
	// account) — and it stores task.json first, because the job is
	// decided after createTask (run.go's finishLaunch), which is what makes the
	// stored layer the one a repeat carries. No claim is taken by a refused
	// launch, so the repeat below is not blocked by one.
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "lists no Claude account") {
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
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "lists no Claude account") || !strings.Contains(err.Error(), "standard") {
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
// The credential here is the fugaro.yaml at the ref the run checks out, which
// the launcher reads for its own resolution: the read fails, so it cannot tell
// what auth the run resolves to.
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

	read := readFugaroYAMLAtRef
	t.Cleanup(func() { readFugaroYAMLAtRef = read })
	readFugaroYAMLAtRef = func(context.Context, string, string) ([]byte, error) {
		return nil, errors.New("permission denied")
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

// TestSharedJobRefusalIgnoresALayerThatDoesNotApply is the other side of the
// same rule, and the reason the resolution is the runner's rather than a copy
// of it: a carried layer whose `gcp_project` is not the file's own does not
// apply to that file (runner.ResolveConfig computes Applied; config.Resolve's
// anchor check is what it feeds), so the run resolves the fugaro.yaml alone and
// its agent.auth comes from there. The launcher must judge that resolution —
// the layer is valid, it is simply another GCP project's — and must not refuse
// it as an unreadable one. A checkout whose `gcp_project:` changed after the
// task was stored is exactly this state, and it is a run that starts perfectly
// well.
//
// Mutation (run, restore): anchor the layer on the file's own project and
// gcp_project and resolve it with config.Resolve (what this function did
// before it called runner.ResolveConfig) — the layer no longer matches the
// anchor, so this launch is refused with "the project layer its task carries is
// invalid" and the run below never starts.
func TestSharedJobRefusalIgnoresALayerThatDoesNotApply(t *testing.T) {
	f := newProjectJobFixture(t)
	isolateCache(t)
	publishedLayer(t, f.cloudFixture, projectJobOAuthLayer)
	projectJobCheckoutYAML(t, projectJobAnchoredRepoYAML)

	const run = "20261018-150000-dddd"
	// The first attempt stores task.json and stops at the OAuth refusal, which
	// is the stored task the rewrite below needs.
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "lists no Claude account") {
		t.Fatalf("the first attempt: %v", err)
	}
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
	// A layer of the same project and another GCP project: it parses, it is
	// complete, and it does not apply to this checkout's file.
	const other = "version: 1\nproject: aurora\ngcp_project: another-project\ndefaults:\n  agent: { auth: oauth }\n"
	spec.ProjectLayer = &task.ProjectLayer{SHA256: config.LayerSum([]byte(other)), YAML: other}
	if err := s.WriteTask(ctx, spec); err != nil {
		t.Fatal(err)
	}

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); err != nil {
		t.Fatalf("a repeat whose carried layer belongs to another GCP project: %v, want the launch to proceed (the layer does not apply, so the run resolves the file alone)", err)
	}
	if n := len(f.run.RunRequests()); n != 1 {
		t.Errorf("the launch made %d jobs.run calls, want 1: the resolution it was judged on is the file's own", n)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Errorf("%d executions, want 1", n)
	}
}

// TestSharedJobRefusalWhenTheCarriedLayerIsInvalid is the stored layer that no
// longer parses: the bytes in task.json are the run's own, whatever wrote them
// (an older binary's spelling of a key today's decoder rejects, an operator's
// hand), and the launcher cannot resolve a credential from them. It refuses
// instead of resolving against today's layer, which is not what the run
// carries — the run would read the stored one.
//
// Mutation (run, restore): drop the LayerErr refusal (projectJobAgentAuth) and
// resolve past the invalid layer — the launch then resolves the checkout alone,
// whose own agent block is unset, so the auth reads "" and projectJobAccountFor
// sees no OAuth run: the run launches on the shared project job carrying the
// OAuth layer the stored task dictates, which is the
// launch the account gate exists to refuse. This test (and the one above) then fail on
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
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "lists no Claude account") {
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
	// The words are the runner's own (runner.ResolveConfig's LayerErr), because
	// that is the resolution the launch is judged on: the run fails bootstrap on
	// the same document, so a launcher that refused in different words would be
	// describing a second rule. It names the layer by the sum the task carries,
	// which is what makes the fix (republish, or launch the run again) findable.
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cannot tell which Claude credential") ||
		!strings.Contains(err.Error(), "the task's project layer (sha256 ") || !strings.Contains(err.Error(), "is invalid") {
		t.Fatalf("a repeat whose stored layer no longer parses: %v, want the carried layer named by its sum", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none", n)
	}
}

// TestSharedJobRefusalWhenTheCheckoutCannotBeFound is the arm of
// projectJobAgentAuth that has no other guard behind it: when the checkout the
// resolution is made against cannot be found, the credential is unknown, and an
// unknown credential must refuse the launch rather than be read as "no oauth
// here" (the empty string projectJobAccountFor is then handed).
//
// It is the state a launch reaches when its checkout goes away between the two
// reads of it — launchProjectJobFor only gets past checkoutConfig with a
// checkout in hand, and this is that checkout having moved or gone before
// projectJobAgentAuth asks for its root again — so the launcher cannot be driven
// into it from the fixture: the two reads run inside one command. The test calls
// the resolution directly, in a directory that is no checkout at all (git
// answers "not a git repository" for --show-toplevel there).
//
// Mutation (run, restore): return "", nil for the empty root instead of
// refusing — the launcher reads the empty string as the run's agent.auth, and
// projectJobAccountFor finds no "oauth" in it, so an OAuth run would be launched
// on a shared job that holds no token for it.
func TestSharedJobRefusalWhenTheCheckoutCannotBeFound(t *testing.T) {
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir()) // not a repository: there is no checkout to find

	e := &cloudEnv{lc: &localcfg.Config{Name: "aurora"}}
	_, err := e.projectJobAgentAuth(context.Background(), &task.Spec{Repo: "acme/app"})
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cannot tell which Claude credential") ||
		!strings.Contains(err.Error(), "checkout could not be found") || !strings.Contains(err.Error(), "acme/app") {
		t.Fatalf("a launch whose checkout cannot be found: %v, want a refusal naming the repository and the missing checkout", err)
	}
}

// TestSharedJobRefusalWhenTheCarriedLayerDoesNotResolve is the other refusal
// with no test behind it: a carried layer the runner's resolution parses (no
// LayerErr, so it is not the invalid-layer case above) but whose merge with the
// checkout's fugaro.yaml does not validate. runner.ResolveConfig reports that as
// Problems and returns no config, so there is no agent.auth to judge — and the
// arm that reads out.Cfg.Agent.Auth off it would be reading a nil config.
//
// This is the ordinary shape of a project layer that is legal as a layer and
// wrong as a file: a layer's own keys are held to the layer's rules
// (config.ParseProjectLayer) one at a time, so what it can break is a relation
// only the resolution knows — here the profile the checkout's workflow selects,
// which the layer republished under the run no longer defines (L13/L16: the
// launcher's own read of the layer and the stored task's can legitimately
// differ, and it is the stored one the run resolves over). The layer parses,
// it applies, and config.Resolve still has nothing to build a config from:
// resolveWorkflows reports the missing profile and returns no config at all.
//
// The checkout's workflow names that profile, so its own fugaro.yaml needs the
// launcher's layer to resolve — which the bucket's copy defines, with the OAuth
// auth the first attempt below is refused for.
//
// Mutation (run, restore): make the arm `if false` — the resolution returns a
// nil *config.Config with its problems, and out.Cfg.Agent.Auth panics on the
// nil dereference: the launch neither refuses nor starts on a resolved
// credential.
func TestSharedJobRefusalWhenTheCarriedLayerDoesNotResolve(t *testing.T) {
	f := newProjectJobFixture(t)
	isolateCache(t)
	publishedLayer(t, f.cloudFixture, projectJobProfiledOAuthLayer)
	projectJobCheckoutYAML(t, projectJobProfiledRepoYAML)

	const run = "20261018-160000-eeee"
	// The first attempt stores task.json and stops at the OAuth refusal: what
	// the repeat below carries is that stored layer, replaced.
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "lists no Claude account") {
		t.Fatalf("the first attempt: %v", err)
	}
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
		t.Fatal("the first attempt stored no project layer; this test needs one to replace")
	}
	// A layer of this project and this GCP project — so it applies to the
	// checkout's file (runner.ResolveConfig's Applied) — that parses on its own
	// and no longer defines the profile the checkout's workflow names.
	bad := "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  agent: { auth: api-key }\nprofiles:\n  other: { checkout: clone }\n"
	spec.ProjectLayer = &task.ProjectLayer{SHA256: config.LayerSum([]byte(bad)), YAML: bad}
	if err := s.WriteTask(ctx, spec); err != nil {
		t.Fatal(err)
	}

	claimed := false
	launchHooks.beforeClaim = func() { claimed = true }
	t.Cleanup(func() { launchHooks.beforeClaim = nil })

	_, _, err = execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cannot tell which Claude credential") ||
		!strings.Contains(err.Error(), "do not resolve") || !strings.Contains(err.Error(), `names profile "p"`) {
		t.Fatalf("a repeat whose carried layer and checkout do not resolve together: %v, want a refusal naming the problem", err)
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
