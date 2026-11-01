package cli

import (
	"context"
	"encoding/json"
	"fmt"
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
// database, as an owner's fugaro update --firebase-project (the marker) followed by a
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
	publishPolicy(t, f, `{"version":4,"allow_repos":["*"]}`)
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
	publishPolicy(t, f, `{"version":4,"allow_repos":["github/other/*"]}`)
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

// TestRunRefusesUnderAPolicyOfThePolicyGeneration: the allow-list gate applies
// to a policy published under a marker *below* this binary's generation but at
// or above the one that carries the policy nodes. Generation 1 is a real
// installation: it deployed the config/policy nodes, `project policy publish`
// wrote there and every run of it reads the policy — so a launcher that read
// the marker as legacy skipped the gate and launched a repository the policy
// does not admit. `launchPolicyGates` reads through projectpolicy.Read, which
// gates on PolicyRulesVersion for exactly this reason (the review finding on
// P2.1: the bump to 2 answered "no policy" for every installation on
// generation 1, keeping its job-environment ceiling and skipping this refusal).
//
// Mutation (verified, run and restored): gate Read on `gen < RulesVersion` —
// this test fails, the launch creating an execution under a policy that refuses
// its repository.
func TestRunRefusesUnderAPolicyOfThePolicyGeneration(t *testing.T) {
	if projectpolicy.PolicyRulesVersion >= projectpolicy.RulesVersion {
		t.Fatalf("PolicyRulesVersion = %d and RulesVersion = %d: this test is about a generation below this binary's, and the two have met", projectpolicy.PolicyRulesVersion, projectpolicy.RulesVersion)
	}
	f := newLaunchBudget(t, "enforce")
	markRules(t, f, projectpolicy.PolicyRulesVersion)
	// The policy node's own version is this binary's (projectpolicy.Version,
	// 2 on next-0.8.5): the generation under test is the rules marker's.
	f.db.Set(projectpolicy.PathShared(), json.RawMessage(fmt.Sprintf(`{"version":%d,"allow_repos":["github/other/*"]}`, projectpolicy.Version)))
	_, _, err := runBudget(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a user error: the policy of generation %d is read and its allow-list governs", err, ExitCode(err), projectpolicy.PolicyRulesVersion)
	}
	if !strings.Contains(err.Error(), "github/acme/app") || !strings.Contains(err.Error(), "allow_repos") {
		t.Errorf("the refusal does not read as the allow-list gate's: %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a refused launch", n)
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
	publishPolicy(t, f, `{"version":4,"allow_repos":["github/other/*"]}`)
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
// a message naming both versions and the fix. The budget is on here on purpose:
// with the mode not off nothing else in the launch path reads the policy node,
// so what refuses is this gate.
//
// The release it names is the one that introduced the version on the node, and
// this node's version (99) is in no row of projectpolicy.VersionSince, so the refusal
// names no release at all — deliberately, and this is the assertion that pins
// it: the sentence used to quote policySince, the release the policy *model*
// ships in (0.8.0), and an operator running an 0.8.1 binary was told to upgrade
// to >= 0.8.0, a release they already run. The version-2 case, where the table
// does name one, is TestPolicySkewRefusalNamesTheReleaseThatIntroducedTheVersion
// below.
//
// The remedy is the upgrading side's, and it is the only one this direction may
// print: `fugaro update --firebase-project` is the wrong move here, and it is the deploy's
// own plan that says so — a deployed rules marker above this binary's
// RulesVersion stops the plan, "the rules this database runs under came from a
// newer Fugaro, and 'fugaro update --firebase-project' would deploy this binary's older
// rules over them. Upgrade Fugaro first" (internal/infra's DB.Plan; doctor's
// policy-rules-skew reports the same state, and requirePolicyRules reads a
// marker at or above RulesVersion as one this Fugaro reads a policy under
// rather than refusing it). The assertion below is what keeps the directions
// apart.
func TestRunRefusesAPolicyNewerThanTheBinary(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	publishPolicy(t, f, `{"version":99,"allow_repos":["*"]}`)
	_, _, err := runBudget(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a user error", err, ExitCode(err))
	}
	for _, want := range []string{
		"the project policy is version 99", "this Fugaro understands version " + strconv.Itoa(projectpolicy.Version),
		"fugaro upgrade",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// No release, and above all not policySince: naming the model's release
	// beside a version it does not describe is the fault the table beside it
	// exists to fix.
	for _, bad := range []string{policySince, "policy model"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("the refusal says %q, and version 99 is not %s's: the release a too-new refusal names is the one that introduced the version it found (projectpolicy.VersionSince), never policySince, which names the release the model ships in — an operator told to upgrade to a release they already run: %v", bad, bad, err)
		}
	}
	for _, bad := range []string{"re-run init", "update --firebase-project"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("the refusal tells an older binary to %q: the deploy's plan refuses to put this release's rules over a newer Fugaro's, so it is the operator's upgrade that is missing: %v", bad, err)
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
// before this shape wrote — version 1, published before a runner enforced a
// profile's allow_repos, plan task 3.3's bump; a version below Version decodes
// fine and is not the legacy path, that is the marker's job) is refused by the same gate, and the remedy printed
// is the operator's delete-then-publish — the same remedy internal/cli's
// publishedPolicyOf refuses an older node with, because this release's publish
// will not overwrite one — and never `fugaro upgrade`, which is this
// binary's own remedy for the opposite direction and does nothing here. The two
// directions shared one sentence until this test: the binary looked newer than
// the node in both, so an operator whose node was simply behind was told to
// upgrade a Fugaro that was already ahead of it.
func TestRunRefusesAPolicyOlderThanTheBinary(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	publishPolicy(t, f, `{"version":1,"allow_repos":["*"]}`)
	_, _, err := runBudget(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v (exit %d), want a user error", err, ExitCode(err))
	}
	for _, want := range []string{
		"the project policy is version 1", fmt.Sprintf("this Fugaro understands version %d", projectpolicy.Version),
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
	for _, bad := range []string{"upgrade", "update --firebase-project"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("the refusal tells the operator to %q, but this binary is the newer side: what is behind is the node, and the remedy is the delete: %v", bad, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions were created for a refused launch", n)
	}
}

// The two directions as the sentences themselves, at the layer all three
// callers share (launch_budget.go's policyReadErr and run_allowlist.go's
// policySkewRefusal here, internal/runner's unreadablePolicy there, all of
// them projectpolicy.SkewSentence): each reader names its own remedy and not
// the other's, and both quote the versions. This is what a change to either
// constant has to keep true, and the reason the remedy lives in
// internal/projectpolicy at all — one wording, one helper, three readers.
func TestSkewSentencesSplitByDirection(t *testing.T) {
	older := &projectpolicy.VersionError{Found: 0, Understood: projectpolicy.Version}
	newer := &projectpolicy.VersionError{Found: projectpolicy.Version + 1, Understood: projectpolicy.Version}
	understood := fmt.Sprintf("version %d", projectpolicy.Version)
	for _, tc := range []struct {
		name    string
		ve      *projectpolicy.VersionError
		want    []string
		notWant []string
	}{
		{
			name: "the node is older", ve: older,
			want:    []string{"version 0", understood, "fugaro project policy publish --delete", "publish it again"},
			notWant: []string{"upgrade Fugaro", "fugaro upgrade", "update --firebase-project"},
		},
		{
			name: "the node is newer", ve: newer,
			want:    []string{fmt.Sprintf("version %d", projectpolicy.Version+1), understood, "upgrade Fugaro", "brew upgrade dimipaun/tap/fugaro"},
			notWant: []string{"publish --delete", "update --firebase-project", "(fugaro upgrade)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := projectpolicy.SkewSentence(tc.ve, projectpolicy.ReaderCLI)
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("SkewSentence(%+v, ReaderCLI) does not say %q: %s", tc.ve, want, msg)
				}
			}
			for _, bad := range tc.notWant {
				if strings.Contains(msg, bad) {
					t.Errorf("SkewSentence(%+v, ReaderCLI) says %q, which is the other direction's remedy: %s", tc.ve, bad, msg)
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

// The gate's answer to "upgrade to what", which is the whole point of the table
// beside policySince. The refusal is driven here with the VersionError a read
// produces, because this release's binary understands version 2 and so cannot
// meet a version-2 node through a launch — the direction belongs to a version-1
// binary (0.8.0 or 0.8.1), which is exactly where the release that introduced
// version 2 has to be named and where the operator it addresses is: an 0.8.1
// installation reading a policy 0.8.5 published.
//
// The three states, each pinned against the others:
//
//   - a version the table knows (2, the profile gate): the release that
//     introduced it, fugaro 0.8.5 — the release the operator has to reach —
//     and never policySince (0.8.0), which they already run;
//   - a version no row names (99): the upgrade alone. Borrowing a row's release
//     would name one that is already installed, which is the same fault one
//     release further out;
//   - the other direction (a version-1 node, read by this binary): unchanged,
//     and the one place the model's own release belongs — what wrote a node of
//     a version below projectpolicy.Version — with the delete-then-publish
//     remedy and not the upgrade.
func TestPolicySkewRefusalNamesTheReleaseThatIntroducedTheVersion(t *testing.T) {
	version1 := projectpolicy.Version - 1
	unknown := &projectpolicy.VersionError{Found: 99, Understood: projectpolicy.Version}
	for _, tc := range []struct {
		name    string
		ve      *projectpolicy.VersionError
		want    []string
		notWant []string
	}{
		{
			name: "a version the table knows",
			ve:   &projectpolicy.VersionError{Found: 2, Understood: 1},
			want: []string{
				"the project policy is version 2", "this Fugaro understands version 1",
				"policy version 2 arrived in fugaro 0.8.5", "upgrade Fugaro", "brew upgrade dimipaun/tap/fugaro",
			},
			notWant: []string{policySince, "policy model", "publish --delete", "(fugaro upgrade)"},
		},
		{
			name:    "a version no row names",
			ve:      unknown,
			want:    []string{"the project policy is version 99", "upgrade Fugaro", "brew upgrade dimipaun/tap/fugaro"},
			notWant: []string{policySince, "policy model", "arrived in fugaro", "(fugaro upgrade)"},
		},
		{
			// A node behind its reader where the table's row for the node's
			// version is not the model's release: the model's release is what
			// this direction quotes, never the table's.
			name:    "the other direction, a version-2 node behind a version-3 reader",
			ve:      &projectpolicy.VersionError{Found: 2, Understood: 3},
			want:    []string{"version 2", "policy model is fugaro " + policySince, "publish --delete"},
			notWant: []string{"upgrade Fugaro", "arrived in fugaro", projectpolicy.VersionSince[2]},
		},
		{
			name:    "the other direction, a node behind this binary",
			ve:      &projectpolicy.VersionError{Found: version1, Understood: projectpolicy.Version},
			want:    []string{fmt.Sprintf("version %d", version1), "policy model is fugaro " + policySince, "publish --delete"},
			notWant: []string{"upgrade Fugaro", "arrived in fugaro"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := policySkewRefusal(tc.ve).Error()
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("policySkewRefusal(%+v) does not say %q: %s", tc.ve, want, msg)
				}
			}
			for _, bad := range tc.notWant {
				if strings.Contains(msg, bad) {
					t.Errorf("policySkewRefusal(%+v) says %q: %s", tc.ve, bad, msg)
				}
			}
		})
	}
	// The table's own answer for the version the refusal above names, so the
	// two cannot drift: 2 is 0.8.5's, and it is not policySince.
	if rel, ok := projectpolicy.VersionSince[2]; !ok || rel != "0.8.5" {
		t.Errorf("projectpolicy.VersionSince[2] = %q (present %v), want 0.8.5: version 2 is the profile gate's, the release that ships in 0.8.5", rel, ok)
	}
}

// The round-6 correction: an installation whose rules predate the model — no
// marker at all, or a marker below the generation that carries the policy nodes
// — has no policy for the gate to compare, and when no node is published it has
// no allow-list either. The launch creates the execution and refuses nothing,
// exactly as it did before this phase. The marker is deliberately not part of
// the gate (plan task 1.5 step 4), and neither is the generation *between* the
// two constants: a marker at PolicyRulesVersion with no node is the same state
// as one at RulesVersion with no node — the gate compares what is published.
func TestRunLaunchesWithNoPolicyPublished(t *testing.T) {
	for _, tc := range []struct {
		name   string
		marker func(t *testing.T, f *launchBudgetFixture)
	}{
		{"no marker at all", func(*testing.T, *launchBudgetFixture) {}},
		{"a marker below the policy generation", func(t *testing.T, f *launchBudgetFixture) { markRules(t, f, projectpolicy.PolicyRulesVersion-1) }},
		{"the policy generation, no node", func(t *testing.T, f *launchBudgetFixture) { markRules(t, f, projectpolicy.PolicyRulesVersion) }},
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
