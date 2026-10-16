package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// publishPolicy puts a policy node and the rules marker on the fixture's
// database, as an owner's fugaro init --firebase (the marker) followed by a
// policy publish do. An empty policy publishes the node alone, which is the
// state a read must not confuse with "no policy at all".
func publishPolicy(t *testing.T, f *launchBudgetFixture, policy string) {
	t.Helper()
	f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	f.db.Set(projectpolicy.PathShared(), json.RawMessage(policy))
}

// markRules writes the rules marker alone: a project-centric installation
// whose owner has deployed the rules but published no policy yet.
func markRules(t *testing.T, f *launchBudgetFixture, generation int) {
	t.Helper()
	f.db.Set(projectpolicy.PathRulesMarker(), generation)
}

// storedTask is the task.json the launch stored under the fixture's slug and
// run, false when the launch stored none.
func storedTask(t *testing.T, f *cloudFixture) bool {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := runstore.Open(b, appSlug, budgetRun).ReadTask(context.Background()); err != nil {
		return false
	}
	return true
}

// The gate's own binding refusal (the third of its refusals, beside the skew
// and the allow-list). It is unreachable through `fugaro run` — the slug a
// launch stores its run under is task.Slug of the provider the local config or
// the checkout's fugaro.yaml names, both validated against config.Providers,
// which is the run's own list (TestProviderKindsAgreeWithTheConfigsList) — so
// it is called here directly, with the pair a launcher's own jobs.run call
// would have to build by hand. What it is worth is the operator hearing about
// a repository the run would refuse while he is still there, before an
// execution exists; what it guards is the day the two lists stop agreeing.
//
// Mutation (run, restored): delete the `if !ok` refusal. Nothing else in the
// package fails — no other test reaches the branch — which is what this test
// is for.
func TestLaunchPolicyGatesRefusesASlugOfAnotherRepository(t *testing.T) {
	f := newLaunchBudget(t, "off")
	// The allow-list admits every repository, so what refuses below is the
	// binding and not the policy.
	publishPolicy(t, f, `{"version":1,"allow_repos":["*"]}`)
	env := memEnv(t, f.cloudFixture)
	spec := &task.Spec{Version: 1, RunID: budgetRun, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}

	// The pair that agrees is admitted: the refusal below is about the slug.
	if err := launchPolicyGates(context.Background(), env, appSlug, spec); err != nil {
		t.Fatalf("the repository's own slug was refused: %v", err)
	}

	other, err := task.Slug("github", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	err = launchPolicyGates(context.Background(), env, other, spec)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want the binding's own refusal", err, ExitCode(err))
	}
	for _, want := range []string{
		other, "acme/app", "provider kind", "github", "bitbucket", "before any execution exists",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// Why the binding refusal above cannot fire on a launch this CLI builds, and
// why the test calls the gate by hand: repoSlug computes the slug with
// task.Slug of the provider the config or the checkout names, and
// config.Validate allows exactly the kinds the run derives its own from. The
// two lists are the same list — the run's jobProviderKinds, which
// runner.ProviderKinds exports for this message — and a kind added to one of
// them alone would make this gate, and only this gate, the thing that refuses
// a launch the run itself would refuse a moment later.
func TestProviderKindsAgreeWithTheConfigsList(t *testing.T) {
	if got, want := runner.ProviderKinds(), config.Providers; !slices.Equal(got, want) {
		t.Fatalf("the run's provider kinds are %v, the config's are %v: a repository whose provider one side knows and the other does not is a slug the two disagree about", got, want)
	}
}

// A published policy that does not admit the repository stops the launch, with
// the message the run itself prints (projectpolicy.NotAdmitted), before
// anything of the launch exists: no execution, no stored task and no budget
// token in the bucket. The check is the launcher's own read of the node, which
// is the only reason an operator sees it now rather than as a failed run.
func TestRunRefusesBeforeLaunch(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	publishPolicy(t, f, `{"version":1,"allow_repos":["github/other/*"]}`)
	_, _, err := runBudget(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a user error", err, ExitCode(err))
	}
	// github/acme/app is the fixture's repository (appSlug), canonicalized
	// against the kind the slug derives — not against the local config's own
	// provider, which only chose the slug.
	for _, want := range []string{
		"github/acme/app", "allow_repos", "github/other/*",
		"fugaro project repos add github/acme/app", "before any execution exists",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a refused launch", n)
	}
	if storedTask(t, f.cloudFixture) {
		t.Error("the refused launch stored a task")
	}
	if tok := readToken(t, f.cloudFixture, budgetRun); tok != "" {
		t.Error("the refused launch left a budget token in the bucket")
	}
}

// A best-of-N batch is refused by the same gate, and before its first attempt:
// runAttempts launches every attempt through launchAttempt, which in production
// is finishLaunch, so the gate is not a single-run path. The tests that swap
// that seam to fail an attempt replace the call the gate lives in, so none of
// them reaches it — a gate moved into a branch the batch's own pre-checks take
// (skipBudget, for one: the batch takes it) would leave every attempt of a
// repository the policy does not admit to launch.
//
// Mutation (verified, run and restored): move launchPolicyGates inside the
// `if !skipBudget` branch above it — this test fails, 2 executions created for a
// batch the policy refuses.
func TestRunAttemptsRefusesBeforeAnyLaunch(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	publishPolicy(t, f, `{"version":1,"allow_repos":["github/other/*"]}`)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "Add a feature")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a user error from the policy gate", err, ExitCode(err))
	}
	for _, want := range []string{
		"github/acme/app", "allow_repos", "github/other/*", "before any execution exists",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the batch's refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a refused batch", n)
	}
}

// The gate's one skew direction: a policy whose version is above what this
// binary understands refuses the launch, before the execution is created, with
// a message naming both versions, the release the policy model ships in
// (policySince) and the fix. The budget is on here on purpose: with the mode
// not off nothing else in the launch path reads the policy node, so what
// refuses is this gate.
//
// The remedy is the upgrading side's, and it is the only one this direction may
// print: `fugaro init --firebase` is the wrong move here, because this release's
// rules template would be redeployed over rules a newer Fugaro wrote (the same
// reasoning requirePolicyRules uses for the marker, project_cmd.go). The
// assertion below is what keeps the directions apart.
func TestRunRefusesAPolicyNewerThanTheBinary(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	publishPolicy(t, f, `{"version":99,"allow_repos":["*"]}`)
	_, _, err := runBudget(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a user error", err, ExitCode(err))
	}
	for _, want := range []string{
		"the project policy is version 99", "this Fugaro understands version 1",
		policySince, "fugaro upgrade",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	for _, bad := range []string{"re-run init", "init --firebase"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("the refusal tells an older binary to %q: this release's rules template would be redeployed over newer ones; it is the operator's upgrade that is missing: %v", bad, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a refused launch", n)
	}
	// policySince is a release, not the policy's integer version or the
	// marker's generation (plan task 1.5 step 4): a value that stopped being
	// one would still compile, so the shape is pinned here too.
	parts := strings.Split(policySince, ".")
	if len(parts) != 3 {
		t.Fatalf("policySince = %q, want a release", policySince)
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			t.Fatalf("policySince = %q, want a release", policySince)
		}
	}
}

// The other skew direction, which is the one the message used to get wrong: a
// node below what this Fugaro understands (a hand-written one, or one a release
// before this shape wrote; version 0 decodes fine and is not the legacy path,
// that is the marker's job) is refused by the same gate, and the remedy printed
// is the operator's delete-then-publish — the same remedy internal/cli's
// publishedPolicyOf refuses an older node with, because this release's publish
// will not overwrite one — and never `fugaro upgrade`, which is this
// binary's own remedy for the opposite direction and does nothing here. The two
// directions shared one sentence until this test: the binary looked newer than
// the node in both, so an operator whose node was simply behind was told to
// upgrade a Fugaro that was already ahead of it.
func TestRunRefusesAPolicyOlderThanTheBinary(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	publishPolicy(t, f, `{"version":0,"allow_repos":["*"]}`)
	_, _, err := runBudget(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a user error", err, ExitCode(err))
	}
	for _, want := range []string{
		"the project policy is version 0", "this Fugaro understands version 1",
		"fugaro project policy publish --delete", "publish it again", policySince,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// Two remedies that are wrong advice here: the other direction's (nothing
	// on this machine is behind the node) and init, which deploys the rules
	// template and marker but writes no policy node — an operator sent to
	// re-run it would meet this refusal again.
	for _, bad := range []string{"upgrade Fugaro", "fugaro upgrade", "init --firebase"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("the refusal tells the operator to %q, but this binary is the newer side: what is behind is the node, and the remedy is the delete: %v", bad, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a refused launch", n)
	}
}

// The two directions as the sentences themselves, at the layer both callers in
// this package share (launch_budget.go's policyReadErr and run_allowlist.go's
// policySkewRefusal word the same VersionError): each names its own remedy and
// not the other's, and both quote the versions. This is what a change to
// either constant has to keep true, and the reason the remedy lives in
// internal/projectpolicy at all — one wording, two readers.
func TestSkewSentencesSplitByDirection(t *testing.T) {
	older := &projectpolicy.VersionError{Found: 0, Understood: projectpolicy.Version}
	newer := &projectpolicy.VersionError{Found: projectpolicy.Version + 1, Understood: projectpolicy.Version}
	for _, tc := range []struct {
		name    string
		ve      *projectpolicy.VersionError
		want    []string
		notWant []string
	}{
		{
			name: "the node is older", ve: older,
			want:    []string{"version 0", "version 1", "fugaro project policy publish --delete", "publish it again"},
			notWant: []string{"upgrade Fugaro", "fugaro upgrade", "init --firebase"},
		},
		{
			name: "the node is newer", ve: newer,
			want:    []string{"version 2", "version 1", "upgrade Fugaro"},
			notWant: []string{"publish --delete", "init --firebase"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := cliSkewSentence(tc.ve)
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("cliSkewSentence(%+v) does not say %q: %s", tc.ve, want, msg)
				}
			}
			for _, bad := range tc.notWant {
				if strings.Contains(msg, bad) {
					t.Errorf("cliSkewSentence(%+v) says %q, which is the other direction's remedy: %s", tc.ve, bad, msg)
				}
			}
		})
	}

	// And the whole of the launch gate's refusal, which splices the sentence
	// into its own release-marked text: the remedy must survive the splice.
	gate := policySkewRefusal(older).Error()
	if !strings.Contains(gate, "publish --delete") || strings.Contains(gate, "upgrade Fugaro") {
		t.Errorf("policySkewRefusal(older) = %q, want the delete remark and not the upgrade one", gate)
	}
	if !strings.Contains(policySkewRefusal(newer).Error(), "upgrade Fugaro") {
		t.Errorf("policySkewRefusal(newer) = %q, want the upgrade remark", policySkewRefusal(newer))
	}

	// The other caller, which reads the node for the budget decision: same
	// sentence, one flat error.
	if e := policyReadErr(older); !strings.Contains(e.Error(), "publish --delete") || strings.Contains(e.Error(), "upgrade Fugaro") {
		t.Errorf("policyReadErr(older) = %v, want the delete remark", e)
	}
	if e := policyReadErr(newer); !strings.Contains(e.Error(), "upgrade Fugaro") {
		t.Errorf("policyReadErr(newer) = %v, want the upgrade remark", e)
	}
}

// The round-6 correction: an installation whose rules predate the model — no
// marker at all, or a marker below RulesVersion — has no policy for the gate
// to compare, and when no node is published it has no allow-list either. The
// launch creates the execution and refuses nothing, exactly as it did before
// this phase. The marker is deliberately not part of the gate (plan task 1.5
// step 4).
func TestRunLaunchesWithNoPolicyPublished(t *testing.T) {
	for _, tc := range []struct {
		name   string
		marker func(t *testing.T, f *launchBudgetFixture)
	}{
		{"no marker at all", func(*testing.T, *launchBudgetFixture) {}},
		{"a marker below RulesVersion", func(t *testing.T, f *launchBudgetFixture) { markRules(t, f, projectpolicy.RulesVersion-1) }},
		{"the rules generation, no node", func(t *testing.T, f *launchBudgetFixture) { markRules(t, f, projectpolicy.RulesVersion) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchBudget(t, "enforce")
			tc.marker(t, f)
			if _, _, err := runBudget(t); err != nil {
				t.Fatalf("err = %v: an installation that has published no policy must launch as it always has", err)
			}
			if n := len(f.run.Executions()); n != 1 {
				t.Fatalf("%d executions, want 1", n)
			}
		})
	}
}

// The gate is the launcher's, not a second refusal inside the run (R2-8(a),
// D10): the same policy that fugaro run refuses above is launched without
// complaint when the execution is created directly, as a launcher calling
// jobs.run itself does. launchRun is that call — it is what finishLaunch uses
// to submit the execution, after the gates — so driving it here is exactly
// the path a direct jobs.run takes.
func TestPolicySinceGateIsNotOnTheRunnerPath(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	publishPolicy(t, f, `{"version":99,"allow_repos":["*"]}`)

	// Through fugaro run: refused, and nothing was created.
	if _, _, err := runBudget(t); err == nil || !strings.Contains(err.Error(), "fugaro upgrade") {
		t.Fatalf("err = %v, want the gate's refusal", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions were created for a refused launch", n)
	}

	// Directly: the execution exists, and the run that starts from it is the
	// one that decides (its own policy read refuses a policy it cannot
	// understand, in the runner's own words — not this gate's).
	env := memEnv(t, f.cloudFixture)
	spec := raceSpec(t, env)
	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); err != nil {
		t.Fatalf("a launch that did not go through fugaro run was refused by the launcher's gate: %v", err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1: the gate must not sit below the launcher", n)
	}
}
