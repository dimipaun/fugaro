package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
)

const testSigner = "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"

// launchBudgetFixture is a cloud fixture whose project has the budget
// backend: an RTDB fake, an IAM Credentials fake that can sign as the signer.
type launchBudgetFixture struct {
	*cloudFixture
	db  *gcpfake.RTDB
	iam *gcpfake.IAMCredentials
}

func newLaunchBudget(t *testing.T, mode string) *launchBudgetFixture {
	t.Helper()
	iam := gcpfake.NewIAMCredentials(t)
	iam.AddSigner(testSigner)
	f := &launchBudgetFixture{cloudFixture: newCloudFixture(t, "iam_credentials: "+iam.URL+"/"), db: gcpfake.NewRTDB(t), iam: iam}
	f.appendConfig(t, "budget: { mode: "+mode+", per_run_usd: 5, rtdb_url: "+f.db.URL+", token_signer: "+testSigner+
		", firebase_api_key: AIzaSyFakeFakeFakeFakeFake12345 }\n")
	f.db.Set("fugaro/project", "aurora")
	f.db.Set("config/mode", "enforce")
	f.db.Set("config/caps/global", map[string]any{"dailyMicros": 100 * usd1, "perRunMicros": 10 * usd1})
	f.db.Set("config/caps/defaults", map[string]any{"repoDailyMicros": 50 * usd1, "repoPerRunMicros": 5 * usd1})
	f.db.Set("config/limits", map[string]any{"maxReserveMicros": 5 * usd1})
	// The job image's runner reads the sealed token: a launch that mints is
	// judged against it (the seal gate, sealSince).
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", releaseSeal)
	return f
}

// readToken is the run's token object as the bucket holds it — sealed, so not
// a token by itself (launchedToken opens it) — "" when there is none.
func readToken(t *testing.T, f *cloudFixture, run string) string {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	data, err := b.ReadAll(context.Background(), token.ObjectKey(appSlug, run))
	if err != nil {
		return ""
	}
	return string(data)
}

const budgetRun = "20261002-100000-abcd"

func runBudget(t *testing.T, extra ...string) (string, string, error) {
	t.Helper()
	return execute(t, append([]string{"run", "--repo", "acme/app", "--run-id", budgetRun, "Add a feature"}, extra...)...)
}

func claimsOf(t *testing.T, f *launchBudgetFixture, tok string) map[string]any {
	t.Helper()
	all, signer, err := f.iam.Verify(tok)
	if err != nil || signer != testSigner {
		t.Fatalf("token verify: signer %q, %v", signer, err)
	}
	c, _ := all["claims"].(map[string]any)
	if c == nil {
		t.Fatalf("no developer claims in %v", all)
	}
	return c
}

func TestRunMintsAndWritesObjectBeforeLaunch(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	var atLaunch string
	var env map[string]string
	f.run.OnRun = func(c gcpfake.RunCall) { env, atLaunch = c.Env, readToken(t, f.cloudFixture, budgetRun) }
	start := time.Now()
	if _, _, err := runBudget(t); err != nil {
		t.Fatal(err)
	}
	if atLaunch == "" {
		t.Fatal("the token object was not in the bucket when jobs.run was called")
	}
	c := claimsOf(t, f, unsealed(t, markerKey(t, env), []byte(atLaunch), budgetRun))
	if c["fs"] != appSlug || c["fr"] != budgetRun || c["fp"] != "aurora" || c["rb"] != "someone@example.com" {
		t.Fatalf("claims = %v", c)
	}
	fx, _ := c["fx"].(json.Number)
	ms, _ := fx.Int64()
	// launch + the job's timeout + 1h queueing + 5m: always beyond the hour.
	if got := time.UnixMilli(ms); got.Before(start.Add(time.Hour+5*time.Minute)) || got.After(time.Now().Add(30*time.Hour)) {
		t.Fatalf("fx = %v", got)
	}
	calls := f.iam.SignCalls()
	if len(calls) != 1 || calls[0].Signer != testSigner {
		t.Fatalf("sign calls = %+v", calls)
	}
}

func TestRunRequestedByInClaim(t *testing.T) {
	// A retry by someone else: rb is the launcher's own identity, not the
	// stored task's requester.
	f := newLaunchBudget(t, "enforce")
	env := memEnv(t, f.cloudFixture)
	env.lc.User = "retrier@example.com"
	spec := raceSpec(t, env)
	spec.RequestedBy = "original@example.com"
	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); err != nil {
		t.Fatal(err)
	}
	data, _, err := env.bucket.Read(context.Background(), token.ObjectKey(appSlug, spec.RunID))
	if err != nil {
		t.Fatal(err)
	}
	if c := claimsOf(t, f, unsealed(t, lastLaunchKey(t, f.cloudFixture), data, spec.RunID)); c["rb"] != "retrier@example.com" {
		t.Fatalf("rb = %v", c["rb"])
	}
}

// mintedClaimsFor is the claim set of the token a direct mint sealed for
// spec's run: what the launcher bound this run's identity to.
func mintedClaimsFor(t *testing.T, f *launchBudgetFixture, env *cloudEnv, spec *task.Spec) map[string]any {
	t.Helper()
	key, err := env.mintBudgetToken(context.Background(), appSlug, spec, nil)
	if err != nil || key == nil {
		t.Fatalf("mintBudgetToken: key %v, err %v", key, err)
	}
	data, _, err := env.bucket.Read(context.Background(), token.ObjectKey(appSlug, spec.RunID))
	if err != nil {
		t.Fatal(err)
	}
	return claimsOf(t, f, unsealed(t, key, data, spec.RunID))
}

// claimPRs reads the fpr claim of a claim set, failing the test if it is not
// there or not an array of numbers.
func claimPRs(t *testing.T, c map[string]any) []int {
	t.Helper()
	arr, ok := c["fpr"].([]any)
	if !ok {
		t.Fatalf("fpr = %v, want an array", c["fpr"])
	}
	out := make([]int, 0, len(arr))
	for _, v := range arr {
		n, ok := v.(json.Number)
		if !ok {
			t.Fatalf("fpr holds %T, want a number", v)
		}
		i, err := n.Int64()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, int(i))
	}
	return out
}

// TestLaunchMintsTheTaskBranchAndPRs: the launcher binds a run that pushes to
// its branch — the branch it creates on a first run, the one it continues on a
// follow-up — and to the pull request a follow-up continues (fb and fpr, G6).
// A first run names no pull request: it opens its own.
func TestLaunchMintsTheTaskBranchAndPRs(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	env := memEnv(t, f.cloudFixture)

	first := &task.Spec{Version: 1, RunID: "20991231-000000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	c := mintedClaimsFor(t, f, env, first)
	if c["fb"] != "fugaro/20991231-000000-abcd" {
		t.Errorf("fb = %v, want the run's own branch fugaro/%s", c["fb"], first.RunID)
	}
	if _, ok := c["fpr"]; ok {
		t.Errorf("a first run named pull requests: %v", c["fpr"])
	}

	follow := &task.Spec{Version: 1, RunID: "20991231-120000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web",
		Branch: "fugaro/20991231-000000-abcd", PR: 42, PreviousRun: "20991231-000000-abcd"}
	c = mintedClaimsFor(t, f, env, follow)
	if c["fb"] != follow.Branch {
		t.Errorf("fb = %v, want the branch it continues %q", c["fb"], follow.Branch)
	}
	if prs := claimPRs(t, c); len(prs) != 1 || prs[0] != 42 {
		t.Errorf("fpr = %v, want [42]", prs)
	}
}

// TestLaunchMintsNoBranchForReviewOrArtifact: a review run writes only
// comments on its pull requests and an artifact run pushes nothing, so neither
// carries a branch — the proxy refuses every push of theirs (G6). A review
// run still names the pull requests it reviews.
func TestLaunchMintsNoBranchForReviewOrArtifact(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	env := memEnv(t, f.cloudFixture)

	review := &task.Spec{Version: 1, RunID: "20991231-000000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web",
		ReviewPRs: []int{5, 6}}
	c := mintedClaimsFor(t, f, env, review)
	if _, ok := c["fb"]; ok {
		t.Errorf("a review run carries a branch: %v", c["fb"])
	}
	if prs := claimPRs(t, c); len(prs) != 2 || prs[0] != 5 || prs[1] != 6 {
		t.Errorf("fpr = %v, want the pull requests it reviews [5 6]", prs)
	}

	artifact := &task.Spec{Version: 1, RunID: "20991231-000000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web",
		Mode: recipe.ModeArtifact, Task: "x"}
	c = mintedClaimsFor(t, f, env, artifact)
	if _, ok := c["fb"]; ok {
		t.Errorf("an artifact run carries a branch: %v", c["fb"])
	}
	if _, ok := c["fpr"]; ok {
		t.Errorf("an artifact run names pull requests: %v", c["fpr"])
	}
}

// TestLaunchRefusesAStaleRunIDOnAFollowUp: a follow-up continues the branch a
// run created, so its run id must sort after that run's. --run-id is the
// operator's own text and prSpec holds it to its shape alone, so a run id that
// sorts BEFORE the branch's would mint an identity naming a branch created
// after the run itself — which the credentials service refuses at the run's
// first call, halting a launch that used to work. The pairing is refused here,
// before anything is signed and before anything is written to the bucket.
func TestLaunchRefusesAStaleRunIDOnAFollowUp(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	env := memEnv(t, f.cloudFixture)
	stale := &task.Spec{Version: 1, RunID: "20991231-000000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web",
		Branch: "fugaro/20991231-120000-abcd", PR: 42, PreviousRun: "20991231-120000-abcd"}
	key, err := env.mintBudgetToken(context.Background(), appSlug, stale, nil)
	if err == nil || key != nil {
		t.Fatalf("mintBudgetToken: key %v, err %v, want a refusal", key, err)
	}
	if n := len(f.iam.SignCalls()); n != 0 {
		t.Fatalf("signJwt was called %d times for a refused launch", n)
	}
	if raw := readToken(t, f.cloudFixture, stale.RunID); raw != "" {
		t.Fatal("a refused launch left a token object in the bucket")
	}

	// The same follow-up under a run id after that run's is admitted, and its
	// identity names the branch it continues.
	fresh := *stale
	fresh.RunID = "20991231-130000-abcd"
	if c := mintedClaimsFor(t, f, env, &fresh); c["fb"] != stale.Branch {
		t.Fatalf("fb = %v, want the branch it continues %q", c["fb"], stale.Branch)
	}
}

func TestRetryMintsFresh(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	env := memEnv(t, f.cloudFixture)
	spec := raceSpec(t, env)
	// A token an earlier attempt left and nobody took.
	old := "old.token.value"
	if _, err := env.bucket.Create(context.Background(), token.ObjectKey(appSlug, spec.RunID), []byte(old), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); err != nil {
		t.Fatal(err)
	}
	data, _, err := env.bucket.Read(context.Background(), token.ObjectKey(appSlug, spec.RunID))
	if err != nil || string(data) == old {
		t.Fatalf("the leftover token was kept: %q, %v", data, err)
	}
	claimsOf(t, f, unsealed(t, lastLaunchKey(t, f.cloudFixture), data, spec.RunID)) // a fresh, valid mint, sealed
	if n := len(f.iam.SignCalls()); n != 1 {
		t.Fatalf("sign calls = %d", n)
	}
}

// TestAMintThatCannotDeleteOrWriteStopsTheLaunch is mintBudgetToken's two
// failure paths after the mint itself succeeded — the launcher's last chances
// to say no, and the two the review found untested:
//
//   - the earlier attempt's object cannot be removed (the delete answers 503;
//     a delete that fails leaves the earlier key able to open an object a
//     second execution could be handed, D17). The launch is refused and
//     nothing of this attempt is written.
//   - the new object cannot be *confirmed* written: the create stored it and
//     then answered 503 — the response was lost. The launch is refused, and
//     the launcher removes what that create left (dropBudgetToken), because an
//     object nobody is going to be handed a key for is an object a later
//     reader could mistake for this run's identity.
//
// Both must return no key: the marker is the only delivery, and a refusal that
// had already handed the key on would be a key in front of an execution that
// was never launched.
//
// Mutation (run, restored): ignore token.DeleteObject's error in
// mintBudgetToken — "a delete that fails" launches.
// Mutation (run, restored): drop the e.dropBudgetToken call from the put
// failure arm — "a create whose response was lost" leaves the sealed object in
// the bucket.
func TestAMintThatCannotDeleteOrWriteStopsTheLaunch(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, want string
		fail       func(*gcpfake.GCS)
	}{
		{"a delete that fails", "deleting the token object failed", func(g *gcpfake.GCS) { g.FailObjectDeletes(1) }},
		{"a create whose response was lost", "writing the token object failed", func(g *gcpfake.GCS) { g.FailObjectStoresAfterWrite(1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchBudget(t, "enforce")
			gcs := gcpfake.NewGCS(t)
			env := envOn(t, f.cloudFixture, gcs.Bucket(t, "runs"))
			spec := raceSpec(t, env)
			name := token.ObjectKey(appSlug, spec.RunID)
			tc.fail(gcs)
			key, err := env.mintBudgetToken(ctx, appSlug, spec, nil)
			if err == nil || key != nil {
				t.Fatalf("key = %v, err = %v: the launch minted anyway", key, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to say %q", err, tc.want)
			}
			// The mint itself happened: the failure is the one after it, not
			// a decision not to mint.
			if n := len(f.iam.SignCalls()); n != 1 {
				t.Errorf("sign calls = %d, want the one mint the failure followed", n)
			}
			if _, _, rerr := env.bucket.Read(ctx, name); !errors.Is(rerr, blobx.ErrNotExist) {
				t.Errorf("the failed mint left %s in the bucket: %v", name, rerr)
			}
		})
	}
}

// A config that names a budget mode and no database is not refused for that
// alone (the review of this PR reconciled the launcher with internal/infra's
// readiness rule, "nothing is blocked until a policy exists"): the mode is the
// operator's claim about the budget, never a policy, and an installation whose
// config names no database has no policy this launcher can see however its mode
// is spelled. What refuses a launch is a policy the launcher *can* see and
// cannot honour — an allow-list that does not admit the repository
// (TestRunRefusesBeforeLaunch), a published node it cannot read
// (TestLaunchRefusesADeniedPolicyRead) — and at deploy time the readiness gate
// is what blocks a project-centric installation whose jobs carry no backend.
//
// So this launch goes ahead, with no token and with no database call at all:
// the same shape a never-configured or mode-off installation has always had.
//
// Both orders are pinned because they are different code: without
// --no-budget-check the budget pre-check runs first (and skips, having no
// database to check either), with it the policy gate is the only thing left.
// Each order gets its own fixture: the launch now succeeds, so a second one on
// the same run id would be refused as an already-launched run and would pin
// nothing.
func TestModeOnWithoutADatabaseLaunchesAsBefore(t *testing.T) {
	for _, mode := range []string{"observe", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name string
				args []string
			}{
				{"as launched", nil},
				{"--no-budget-check", []string{"--no-budget-check"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					f := newCloudFixture(t)
					f.appendConfig(t, "budget: { mode: "+mode+", per_run_usd: 5 }\n")
					var env map[string]string
					f.run.OnRun = func(c gcpfake.RunCall) { env = c.Env }
					args := append([]string{"run", "--repo", "acme/app", "--run-id", budgetRun}, tc.args...)
					_, errOut, err := execute(t, append(args, "Add a feature")...)
					if err != nil {
						t.Fatalf("err = %v (%s), want a launch: a mode that is on is not a policy, and with no database there is no policy to read", err, errOut)
					}
					if n := len(f.run.Executions()); n != 1 {
						t.Errorf("%d executions were created, want 1", n)
					}
					if !storedTask(t, f) {
						t.Error("the launch stored no task")
					}
					if tok := readToken(t, f, budgetRun); tok != "" {
						t.Error("a launch with no database to read a policy from minted a budget token")
					}
					// And the job carries no backend, which is the same
					// shape a never-configured installation deploys: no
					// address to read a policy from, so no mode to claim
					// either. A project that then publishes a policy is
					// what internal/infra's readiness gate stops the next
					// deploy for.
					for _, k := range []string{runner.RTDBURLEnv, runner.BudgetTokenEnv, "FUGARO_BUDGET_MODE"} {
						if v, ok := env[k]; ok {
							t.Errorf("the job's environment carries %s=%q with no database to read a policy from", k, v)
						}
					}
				})
			}
		})
	}
}

// The mint decision, at the line the original finding cites (budgetMintOn's
// early return): with no database there is no policy this CLI can see, whatever
// the mode says, so the answer is "no token needed" rather than a refusal. That
// reading is what makes the mode-off installation and the never-configured one
// the same path — and it is sound because the side that can see a policy, the
// launcher's own gate and internal/infra's readiness gate, is what refuses.
func TestMintIsNotNeededWithoutADatabaseWhateverTheMode(t *testing.T) {
	for _, cfg := range []string{
		"budget: { mode: observe, per_run_usd: 5 }\n",
		"budget: { mode: enforce, per_run_usd: 5 }\n",
		"",
	} {
		f := newCloudFixture(t)
		f.appendConfig(t, cfg)
		env := memEnv(t, f)
		if on, err := env.budgetMintOn(context.Background()); on || err != nil {
			t.Fatalf("budgetMintOn with %q = (%v, %v), want a skip: no database, so no policy this CLI could see", cfg, on, err)
		}
	}
}

// newOffModeBudget is the launch fixture with the budget mode off in the
// operator's config and the budget block spelled as given, so that what makes
// a launch mint (or refuse to) is the only thing that varies.
func newOffModeBudget(t *testing.T, budget string) *launchBudgetFixture {
	t.Helper()
	iam := gcpfake.NewIAMCredentials(t)
	iam.AddSigner(testSigner)
	f := &launchBudgetFixture{cloudFixture: newCloudFixture(t, "iam_credentials: "+iam.URL+"/"), db: gcpfake.NewRTDB(t), iam: iam}
	f.appendConfig(t, budget)
	f.db.Set("fugaro/project", "aurora")
	f.db.Set("config/mode", "enforce")
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", releaseSeal)
	return f
}

// The launcher's mint decision follows the published policy, not the
// operator's local mode (plan task 1.3 step 2): a job of a project that has
// published a policy must be able to read it, whatever mode its environment
// was deployed with, and the runner reads the policy's mode through that
// identity. Without a policy there is nothing to read and no token is minted
// — which is what makes the runner's "budget off, no token" path a legacy
// installation rather than a hole.
func TestLauncherMintsATokenWhenAPolicyExistsAndTheLocalModeIsOff(t *testing.T) {
	budget := func(f *launchBudgetFixture) string {
		return "budget: { mode: off, per_run_usd: 5, rtdb_url: " + f.db.URL + ", token_signer: " + testSigner +
			", firebase_api_key: AIzaSyFakeFakeFakeFakeFake12345 }\n"
	}
	// The allow-list admits the fixture's repository: a published policy with
	// an empty allow_repos is deny-all (plan task 1.5), so a policy that names
	// no repository would refuse the launch before the mint decision these
	// subtests are about.
	policy := `{"version":4,"budget":{"mode":"enforce","per_run_usd":5},"allow_repos":["github/acme/*"]}`

	t.Run("no policy, no token", func(t *testing.T) {
		f := newOffModeBudget(t, "")
		f.appendConfig(t, budget(f))
		var env map[string]string
		f.run.OnRun = func(c gcpfake.RunCall) { env = c.Env }
		if _, _, err := runBudget(t); err != nil {
			t.Fatal(err)
		}
		if tok := readToken(t, f.cloudFixture, budgetRun); tok != "" {
			t.Fatalf("a launch for a project with no policy minted a token: %q", tok)
		}
		if v, ok := env[runner.BudgetTokenEnv]; ok {
			t.Fatalf("a launch that minted nothing set %s = %q", runner.BudgetTokenEnv, v)
		}
		if n := len(f.iam.SignCalls()); n != 0 {
			t.Fatalf("sign calls = %d", n)
		}
	})
	t.Run("a policy, a token", func(t *testing.T) {
		f := newOffModeBudget(t, "")
		f.appendConfig(t, budget(f))
		f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
		f.db.Set(projectpolicy.PathShared(), json.RawMessage(policy))
		var env map[string]string
		f.run.OnRun = func(c gcpfake.RunCall) { env = c.Env }
		if _, _, err := runBudget(t); err != nil {
			t.Fatal(err)
		}
		raw := readToken(t, f.cloudFixture, budgetRun)
		if raw == "" {
			t.Fatal("a launch for a project with a published policy minted no token")
		}
		if c := claimsOf(t, f, unsealed(t, markerKey(t, env), []byte(raw), budgetRun)); c["fs"] != appSlug || c["fr"] != budgetRun || c["fp"] != "aurora" {
			t.Fatalf("claims = %v", c)
		}
		// M2: the launch that minted marks this execution with the token, and
		// the marker is the run request's, not the job's deployment (that
		// environment is update --repo's, and internal/infra pins that it never
		// carries the marker). It is what makes a missing token a refusal for
		// this run instead of "this project has published no policy", and its
		// value is the launch's seal key (D15), checked by markerKey above.
		// The launch is not the deploy: the job's own environment is the one
		// the mode was deployed with, and the launcher does not rewrite it to
		// the policy's mode. What the policy decides is the runner's mode,
		// which it reads with the identity minted here.
		if env[runner.BudgetModeEnv] == "enforce" {
			t.Fatalf("the launcher rewrote the job's %s to the policy's", runner.BudgetModeEnv)
		}
	})
	t.Run("a policy with no signer is refused", func(t *testing.T) {
		f := newOffModeBudget(t, "")
		f.appendConfig(t, "budget: { mode: off, per_run_usd: 5, rtdb_url: "+f.db.URL+", firebase_api_key: AIzaSyFakeFakeFakeFakeFake12345 }\n")
		f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
		f.db.Set(projectpolicy.PathShared(), json.RawMessage(policy))
		_, _, err := runBudget(t)
		if err == nil || !strings.Contains(err.Error(), "budget.token_signer") {
			t.Fatalf("err = %v, want the missing-signer refusal", err)
		}
		if launchedAny(f) {
			t.Fatal("the run was launched without a budget identity")
		}
	})
}

// The launch reads the policy once. Two things read it on this path: the gate,
// before anything of the launch exists, and the mint decision, because a
// published policy is what makes a token necessary whatever the local mode is.
// Two reads are two chances for an owner to change the node under one launch,
// on the launch that mints a run its identity, so both go through the launch's
// one memoized read (policyReadOnce) and the database sees a single GET of the
// node. (The runner reads it again in its own process; that read is the one
// that counts.)
//
// Mutation (run, restored): read it directly in launchPolicyGates again
// (projectPolicyRead instead of env.policy.read) — the count below becomes 2
// and this test fails.
func TestLaunchReadsThePolicyOnce(t *testing.T) {
	f := newOffModeBudget(t, "")
	f.appendConfig(t, "budget: { mode: off, per_run_usd: 5, rtdb_url: "+f.db.URL+", token_signer: "+testSigner+
		", firebase_api_key: AIzaSyFakeFakeFakeFakeFake12345 }\n")
	publishPolicy(t, f, `{"version":4,"budget":{"mode":"enforce","per_run_usd":5},"allow_repos":["*"]}`)
	if _, _, err := runBudget(t); err != nil {
		t.Fatal(err)
	}
	reads := 0
	for _, r := range f.db.Requests() {
		if r.Method == http.MethodGet && strings.Contains(r.Path, projectpolicy.PathShared()) {
			reads++
		}
	}
	if reads != 1 {
		t.Fatalf("%d reads of %s in one launch, want 1: the gate and the mint decision must not be admitted against two different policies", reads, projectpolicy.PathShared())
	}
}

// L2 of the PR #379 review, the first mutation no test caught: the CLI must
// not read a denied read of a published policy as "no policy here". The
// marker is deployed, so a policy may exist, and the database refuses the node
// to this operator — the rules were changed out from under it. Reading that as
// an absent node would launch the run with no budget identity at all, and the
// run would then go on with the project's policy unread and unenforced.
//
// A second mutation lives here, and this test carries the observable that
// catches it: swallowing the error in the launch gate (launchPolicyGates) so
// the launch goes on. Nothing else tells the two apart — the mint's own read
// then fails with the same message and the same exit code, no token is
// written and no execution is created either way. What differs is where the
// launch stopped: with the gate swallowing, createTask has already run, so the
// run's task.json is in the bucket. No stored task is this gate's promise
// ("a refused launch leaves nothing behind at all"), and it is asserted below.
func TestLaunchRefusesADeniedPolicyRead(t *testing.T) {
	f := newOffModeBudget(t, "")
	f.appendConfig(t, "budget: { mode: off, per_run_usd: 5, rtdb_url: "+f.db.URL+", token_signer: "+testSigner+
		", firebase_api_key: AIzaSyFakeFakeFakeFakeFake12345 }\n")
	f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	f.db.Set(projectpolicy.PathShared(), json.RawMessage(`{"version":4,"budget":{"mode":"enforce","per_run_usd":5}}`))
	f.db.DenyRead(projectpolicy.PathShared())
	_, _, err := runBudget(t)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a refusal (the policy node is published and cannot be read)", err)
	}
	// The remedy is the plain update, not --firebase-project: the rules stage
	// is what deploys the generation the marker is about (init_rules_stage.go).
	for _, want := range []string{projectpolicy.PathShared(), projectpolicy.PathRulesMarker(), "fugaro update", "rules stage"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if launchedAny(f) || readToken(t, f.cloudFixture, budgetRun) != "" {
		t.Fatal("a denied read of a published policy minted a token or launched the run")
	}
	if storedTask(t, f.cloudFixture) {
		t.Error("the refused launch stored a task: the gate read the policy after createTask had run")
	}
}

// Carried review item (b), the CLI half: the decoder reports the top-level
// keys nobody reads, the runner logs them and goes on (an image cannot be
// upgraded mid-run), and the CLI — which the operator can upgrade — refuses
// them, because a misspelt key is a policy the owner believes he set.
func TestLaunchRefusesAPolicyKeyThisFugaroDoesNotKnow(t *testing.T) {
	f := newOffModeBudget(t, "")
	f.appendConfig(t, "budget: { mode: off, per_run_usd: 5, rtdb_url: "+f.db.URL+", token_signer: "+testSigner+
		", firebase_api_key: AIzaSyFakeFakeFakeFakeFake12345 }\n")
	f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	f.db.Set(projectpolicy.PathShared(), json.RawMessage(`{"version":4,"allow_repo":["acme/*"]}`))
	_, _, err := runBudget(t)
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "allow_repo") {
		t.Fatalf("err = %v, want a refusal naming the unknown key", err)
	}
	// The refusal names the node itself, and policyReadErr must leave it alone:
	// re-prefixing it printed the same path twice ("config/policy/shared:
	// config/policy/shared lists ...").
	if n := strings.Count(err.Error(), projectpolicy.PathShared()); n != 1 {
		t.Errorf("the refusal names %s %d times, want once: %v", projectpolicy.PathShared(), n, err)
	}
	if launchedAny(f) {
		t.Fatal("the run was launched with a policy this Fugaro does not understand")
	}
}

// The branch of the CLI's policy read no test reached (the review of PR #379): a
// policy node the database handed over and this Fugaro could not read is the
// project's own published data, so it is the user's error (exit 1) — the class
// dbErr and checkDBProject use for a bad node in the same database. It used to
// fall through to remote (exit 2), which sends the operator to the dashboard
// for a policy he wrote himself.
func TestLaunchClassifiesAMalformedPolicyAsTheUsersOwnError(t *testing.T) {
	for name, node := range map[string]string{
		"a budget the decoder refuses": `{"version":4,"budget":{"mode":"nonsense"}}`,
		"a node with no version":       `{"budget":{"mode":"enforce"}}`,
		"not a JSON object":            `["budget"]`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newOffModeBudget(t, "")
			f.appendConfig(t, "budget: { mode: off, per_run_usd: 5, rtdb_url: "+f.db.URL+", token_signer: "+testSigner+
				", firebase_api_key: AIzaSyFakeFakeFakeFakeFake12345 }\n")
			f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
			f.db.Set(projectpolicy.PathShared(), json.RawMessage(node))
			_, _, err := runBudget(t)
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v (exit %d), want the user's own error (exit %d)", err, ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), projectpolicy.PathShared()) {
				t.Errorf("the refusal does not name the node to fix: %v", err)
			}
			if launchedAny(f) || readToken(t, f.cloudFixture, budgetRun) != "" {
				t.Fatal("a launch read a policy it cannot parse and minted the run an identity anyway")
			}
		})
	}
}

// The other half of the same split: an outage is remote (exit 2) — the launch
// cannot see whether the project has a policy at all, and nothing the operator
// writes in the policy would help. It fails closed, like dbErr.
func TestLaunchClassifiesAnUnreachablePolicyReadAsRemote(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // nothing answers on that port now
	f := newOffModeBudget(t, "")
	f.appendConfig(t, "budget: { mode: off, per_run_usd: 5, rtdb_url: http://"+addr+", token_signer: "+testSigner+
		", firebase_api_key: AIzaSyFakeFakeFakeFakeFake12345 }\n")
	_, _, err = runBudget(t)
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v (exit %d), want a remote failure (exit %d)", err, ExitCode(err), ExitRemoteError)
	}
	if launchedAny(f) {
		t.Fatal("the run was launched without seeing the project's policy")
	}
}

// policyExists reads the published node the way planRepo does — the memoized
// read, then exists — for a gate that only asks whether something is there.
func policyExists(t *testing.T, f *projectFixture, node any) (bool, error) {
	t.Helper()
	f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	f.db.Set(projectpolicy.PathShared(), node)
	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	var once policyReadOnce
	return once.exists(context.Background(), lc)
}

// A node written in an older version is published: the gate planRepo passes
// readies the repository's jobs to read a policy, and the policy stage of the
// same `fugaro update` migrates that node in place. Classifying it as "no
// policy, but an error" is what stopped the run at the repository stage, one
// stage before the migration, with the delete-and-publish remedy — a second
// command for a node this one was about to rewrite.
func TestPolicyExistsCountsAnOlderVersionNode(t *testing.T) {
	f := newProjectFixture(t, "")
	found, err := policyExists(t, f, v3Node(nil))
	if err != nil {
		t.Fatalf("an older published node was refused, so the gates before the policy stage stop the run: %v", err)
	}
	if !found {
		t.Error("an older published node was read as no policy at all, so Readiness's D11 gate would ready the jobs for a project with no policy to read")
	}
}

// The direction that stays a refusal: a node ahead of the binary is one no
// stage of this run can read or migrate, so the gate still stops it — with the
// remedy that fits it (upgrade Fugaro), not with a node this binary could
// write over.
func TestPolicyExistsRefusesANewerNode(t *testing.T) {
	f := newProjectFixture(t, "")
	found, err := policyExists(t, f, map[string]any{"version": projectpolicy.Version + 1})
	if err == nil {
		t.Fatal("a node written by a newer Fugaro was accepted as one this binary can go on with")
	}
	if found {
		t.Error("the refusal reports the node as an ordinary published policy")
	}
	if !strings.Contains(err.Error(), "upgrade Fugaro") {
		t.Errorf("the refusal does not carry the remedy for a node ahead of the binary: %v", err)
	}
}

// And a node no version words is not an older node: nothing migrates it, so
// the gate keeps refusing it, naming the node.
func TestPolicyExistsRefusesAMalformedNode(t *testing.T) {
	for name, node := range map[string]json.RawMessage{
		"not a JSON object":                json.RawMessage(`["budget"]`),
		"no version at all":                json.RawMessage(`{"budget":{"mode":"enforce"}}`),
		"a version that is not an integer": json.RawMessage(`{"version":"three"}`),
	} {
		t.Run(name, func(t *testing.T) {
			f := newProjectFixture(t, "")
			found, err := policyExists(t, f, node)
			if err == nil || found {
				t.Fatalf("found = %v, err = %v, want a refusal", found, err)
			}
			if !strings.Contains(err.Error(), projectpolicy.PathShared()) {
				t.Errorf("the refusal does not name the node to fix: %v", err)
			}
		})
	}
}

func TestMintedTokenNotInJobsRunRequest(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	var env map[string]string
	f.run.OnRun = func(c gcpfake.RunCall) { env = c.Env }
	out, errOut, err := runBudget(t)
	if err != nil {
		t.Fatal(err)
	}
	tok := launchedToken(t, f.cloudFixture, budgetRun)
	parts := strings.Split(tok, ".")
	for _, secret := range []string{tok, parts[1], parts[2]} {
		for _, r := range f.run.Requests() {
			if strings.Contains(string(r.Body), secret) || strings.Contains(r.Path+r.Query, secret) {
				t.Fatalf("the token reached the jobs.run request %s %s", r.Method, r.Path)
			}
		}
		for _, r := range f.logging.Requests() {
			if strings.Contains(string(r.Body), secret) {
				t.Fatalf("the token reached a logging request")
			}
		}
		for k, v := range env {
			if strings.Contains(v, secret) {
				t.Fatalf("the token is in the job's env %s", k)
			}
		}
		// The marker, and only the marker: the runner learns that this
		// execution was minted a token from this one variable, and never the
		// token itself (internal/runner's BudgetTokenEnv, M2). Its value is
		// the seal key, which opens the object and is not the token (D15).
		markerKey(t, env)
		if strings.Contains(out+errOut, secret) {
			t.Fatal("the token was printed")
		}
		for _, r := range f.db.Requests() {
			if strings.Contains(string(r.Body)+r.Path+r.Query, secret) {
				t.Fatal("the token reached the database")
			}
		}
	}
	// The launcher's own database calls carry no token either (the
	// pre-check acts as the person; here no_auth sends nothing).
	for _, c := range f.db.Credentials() {
		if strings.TrimSpace(c) != "" {
			t.Fatalf("credentials sent to the database fake: %q", c)
		}
	}
}

func launchedAny(f *launchBudgetFixture) bool {
	n := 0
	for _, r := range f.run.Requests() {
		if strings.HasSuffix(r.Path, ":run") {
			n++
		}
	}
	return n > 0
}

func TestPrecheckKilledRefuses(t *testing.T) {
	for _, node := range []string{"config/kill/global", budget.PathKillRepo(appSlug)} {
		f := newLaunchBudget(t, "enforce")
		f.db.Set(node, map[string]any{"on": true, "by": "boss@example.com", "reason": "incident \x1b[31m 42", "at": time.Now().UnixMilli()})
		_, errOut, err := runBudget(t)
		if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "kill switch") || !strings.Contains(err.Error(), "--no-budget-check") {
			t.Fatalf("%s: err = %v", node, err)
		}
		if strings.Contains(err.Error()+errOut, "\x1b") {
			t.Fatal("terminal controls from the kill reason reached the message")
		}
		if launchedAny(f) || len(f.iam.SignCalls()) != 0 || readToken(t, f.cloudFixture, budgetRun) != "" {
			t.Fatalf("a refused launch minted or launched (%s)", node)
		}
	}
}

func TestPrecheckKilledRefusesInObserve(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	f.db.Set("config/mode", "observe")
	f.db.Set("config/kill/global", map[string]any{"on": true})
	if _, _, err := runBudget(t); ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
}

func TestPrecheckNoCapRefuses(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	f.db.Set("config/caps/defaults", nil)
	_, _, err := runBudget(t)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "no_cap") {
		t.Fatalf("err = %v", err)
	}
	if launchedAny(f) || len(f.iam.SignCalls()) != 0 {
		t.Fatal("minted or launched")
	}
}

func TestPrecheckLowHeadroomRefuses(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	// $0.10 left of the repository's $50 day.
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": 49_900_000})
	_, errOut, err := runBudget(t)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "repo_daily_cap") {
		t.Fatalf("err = %v", err)
	}
	// That counter is past 80% of the same cap, and the refusal is what
	// speaks: nothing warns beside a launch that is not starting.
	if strings.Contains(errOut, "past 80% warns") {
		t.Fatalf("a refused launch carried the 80%% warning: %q", errOut)
	}
	// Exactly the minimum left passes.
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": 49_750_000})
	if _, _, err := runBudget(t); err != nil {
		t.Fatalf("$0.25 of headroom is enough: %v", err)
	}
}

// TestRunAttemptsBudgetPrecheckCountsAllAtOnce pins the ruling item 4 of the
// design generic-tool review (2026-10-13): a best-of-N batch leases N *
// budget.MinLaunchHeadroom once, before any attempt, instead of one
// MinLaunchHeadroom lease per attempt (which would let a batch with
// headroom for only its first attempt start, then halt attempts 2..N one
// at a time). $0.25 is TestPrecheckLowHeadroomRefuses's own boundary for a
// single run; two attempts need $0.50.
func TestRunAttemptsBudgetPrecheckCountsAllAtOnce(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	setMaxParallel(t, 5)
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": 49_750_000}) // $0.25 left
	_, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "task")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "repo_daily_cap") || !strings.Contains(err.Error(), "needs headroom for all 2") {
		t.Fatalf("err = %v", err)
	}
	if launchedAny(f) {
		t.Fatal("a batch refused by the budget must not launch any of it")
	}
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": 49_500_000}) // $0.50 left
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "task"); err != nil {
		t.Fatalf("$0.50 of headroom is enough for 2 attempts: %v", err)
	}
}

// TestRunAttemptsBudgetNotesPrintOncePerBatch pins the rest of item 4: the
// budget pre-check's notes (and, in TestRunAttemptsBudgetNoBudgetCheckWarnsOnce,
// the --no-budget-check warning) print once for the whole batch, not once
// per attempt — observe mode's advisory note is the simplest way to force
// Precheck to return a Note on a launch it still allows. The counter that
// leaves too little headroom for that note is past 80% of the same cap, so
// the launch carries the 80% warning too, and it is counted the same way.
func TestRunAttemptsBudgetNotesPrintOncePerBatch(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	setMaxParallel(t, 5)
	f.db.Set("config/mode", "observe")
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": 49_900_000})
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--attempts", "3", "task")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"budget: the project is in observe mode", "budget: the repository's daily cap"} {
		if n := strings.Count(errOut, want); n != 1 {
			t.Fatalf("%q printed %d times for a 3-attempt batch, want 1: %q", want, n, errOut)
		}
	}
	if n := strings.Count(errOut, "budget: "); n != 2 {
		t.Fatalf("the batch carried %d budget notes, want 2, one each: %q", n, errOut)
	}
	if !launchedAny(f) {
		t.Fatal("observe must still launch")
	}
}

// TestRunAttemptsBudgetNoBudgetCheckWarnsOnce is
// TestRunAttemptsBudgetNotesPrintOncePerBatch's --no-budget-check half.
func TestRunAttemptsBudgetNoBudgetCheckWarnsOnce(t *testing.T) {
	newLaunchBudget(t, "enforce")
	setMaxParallel(t, 5)
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--attempts", "3", "--no-budget-check", "task")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(errOut, "--no-budget-check"); n != 1 {
		t.Fatalf("the --no-budget-check warning printed %d times for a 3-attempt batch, want 1: %q", n, errOut)
	}
}

func TestPrecheckGlobalHeadroom(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	f.db.Set(budget.PathSpendGlobal(today()), map[string]any{"counted": 99_900_000})
	_, errOut, err := runBudget(t)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "global_daily_cap") {
		t.Fatalf("err = %v", err)
	}
	// Past 80% of the project's cap, but the refusal is what speaks.
	if strings.Contains(errOut, "past 80% warns") {
		t.Fatalf("a refused launch carried the 80%% warning: %q", errOut)
	}
}

func TestPrecheckObserveWarnsOnly(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	f.db.Set("config/mode", "observe")
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": 49_900_000})
	_, errOut, err := runBudget(t)
	if err != nil || !strings.Contains(errOut, "observe") || !launchedAny(f) {
		t.Fatalf("observe must launch with a note: %v / %q", err, errOut)
	}
}

func TestPrecheckForeignProjectRefuses(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	f.db.Set("fugaro/project", "other")
	if _, _, err := runBudget(t); ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "foreign_project") {
		t.Fatalf("err = %v", err)
	}
	f.db.Set("fugaro/project", nil)
	if _, _, err := runBudget(t); ExitCode(err) != ExitUserError {
		t.Fatalf("an unmarked database must refuse: %v", err)
	}
}

func TestPrecheckUnreadableExits2(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	f.db.Refuse(503, "UNAVAILABLE", "", "down")
	_, _, err := runBudget(t)
	if ExitCode(err) != ExitRemoteError || err == nil || !strings.Contains(err.Error(), "fail closed") {
		t.Fatalf("err = %v", err)
	}
	if launchedAny(f) || len(f.iam.SignCalls()) != 0 {
		t.Fatal("an unreadable database must not launch or mint")
	}
}

func TestNoBudgetCheckSkipsOnlyPrecheck(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	f.db.Set("config/kill/global", map[string]any{"on": true})
	_, errOut, err := runBudget(t, "--no-budget-check")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "--no-budget-check") {
		t.Fatalf("the skip is not announced: %q", errOut)
	}
	// The mint still happens, and the pre-check's own reads did not: the only
	// calls left are the policy read the launch makes for itself (its
	// admission and skew gates, plan task 1.5 step 4), never a cap, a kill
	// switch or a lease.
	if len(f.iam.SignCalls()) != 1 || readToken(t, f.cloudFixture, budgetRun) == "" || !launchedAny(f) {
		t.Fatal("the skip must still mint and launch")
	}
	for _, r := range f.db.Requests() {
		if r.Method != "GET" || !strings.Contains(r.Path, "config/policy/") && !strings.Contains(r.Path, "policyRules") {
			t.Fatalf("--no-budget-check skipped the pre-check, but the launch made a budget read: %s %s", r.Method, r.Path)
		}
	}
}

func TestBudgetOffSkipsEverything(t *testing.T) {
	for name, cfg := range map[string]string{
		"off, backend configured": "budget: { mode: off, rtdb_url: URL, token_signer: " + testSigner + " }\n",
		"no budget block":         "",
		// A mode that is not off with no database is the same skip: nothing is
		// blocked until a policy exists, and this config has none the launcher
		// could see (TestModeOnWithoutADatabaseLaunchesAsBefore is the launch
		// half of it, with the pre-checks in the way).
		"observe, no backend": "budget: { mode: observe, per_run_usd: 5 }\n",
	} {
		t.Run(name, func(t *testing.T) {
			iam := gcpfake.NewIAMCredentials(t)
			iam.AddSigner(testSigner)
			f := newCloudFixture(t, "iam_credentials: "+iam.URL+"/")
			db := gcpfake.NewRTDB(t)
			f.appendConfig(t, strings.ReplaceAll(cfg, "URL", db.URL))
			db.Set("config/kill/global", map[string]any{"on": true})
			var env map[string]string
			f.run.OnRun = func(c gcpfake.RunCall) { env = c.Env }
			_, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", budgetRun, "--no-budget-check", "Add a feature")
			if err != nil {
				t.Fatal(err)
			}
			// A budget-off launch touches no part of the budget: no caps, no
			// kill switch, no registry, no token. It does read whether the
			// project has published a policy, with the operator's own
			// credentials, because a published policy means the run needs an
			// identity whatever the mode is (task 1.3 step 2).
			for _, r := range db.Requests() {
				if r.Method != "GET" || !strings.Contains(r.Path, "config/policy/") && !strings.Contains(r.Path, "policyRules") {
					t.Fatalf("a budget-off launch made a database call it should not: %s %s", r.Method, r.Path)
				}
			}
			if env == nil || len(iam.SignCalls()) != 0 || readToken(t, f, budgetRun) != "" {
				t.Fatalf("a budget-off launch touched the backend (env %v)", env)
			}
			if strings.Contains(errOut, "budget") {
				t.Fatalf("a budget-off launch said something about the budget: %q", errOut)
			}
			for k := range env {
				if strings.Contains(k, "TOKEN") && k != "FUGARO_RUN" {
					t.Fatalf("unexpected env %s", k)
				}
			}
		})
	}
}

func TestBackendWithoutSignerIsRefused(t *testing.T) {
	f := newCloudFixture(t)
	db := gcpfake.NewRTDB(t)
	f.appendConfig(t, "budget: { mode: observe, rtdb_url: "+db.URL+" }\n")
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", budgetRun, "--no-budget-check", "Add a feature")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "token_signer") {
		t.Fatalf("err = %v", err)
	}
}

func TestMintFailureReleasesTheClaimAndLeavesNothing(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	f.iam.Refuse(403, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "denied")
	_, _, err := runBudget(t)
	if err == nil || !strings.Contains(err.Error(), "fugaroTokenMinter") {
		t.Fatalf("err = %v", err)
	}
	if launchedAny(f) || readToken(t, f.cloudFixture, budgetRun) != "" {
		t.Fatal("a failed mint launched or left a token")
	}
	f.iam.Refuse(0, "", "", "")
	if _, _, err := runBudget(t); err != nil {
		t.Fatalf("the claim was not released: %v", err)
	}
}

func TestRejectedLaunchDropsTheTokenObject(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	env := memEnv(t, f.cloudFixture)
	spec := raceSpec(t, env)
	spec.Workflow = "missing" // jobs.run is refused outright
	_, err := launchRun(context.Background(), env, appSlug, spec, time.Now())
	if !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("err = %v", err)
	}
	if _, _, rerr := env.bucket.Read(context.Background(), token.ObjectKey(appSlug, spec.RunID)); !errors.Is(rerr, blobx.ErrNotExist) {
		t.Fatalf("the token of a launch that never started was left: %v", rerr)
	}
}

func TestAmbiguousLaunchKeepsTheTokenObject(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	env := memEnv(t, f.cloudFixture)
	spec := raceSpec(t, env)
	f.run.FailRunWith = 503
	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); err == nil {
		t.Fatal("want an error")
	}
	// An execution may exist and need it.
	if _, _, rerr := env.bucket.Read(context.Background(), token.ObjectKey(appSlug, spec.RunID)); rerr != nil {
		t.Fatalf("token object dropped after an ambiguous launch: %v", rerr)
	}
}

func TestPrecheckSkippedForAnAlreadyLaunchedRun(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	if _, _, err := runBudget(t); err != nil {
		t.Fatal(err)
	}
	f.db.Set("config/kill/global", map[string]any{"on": true})
	out, _, err := runBudget(t, "--json")
	if err != nil || !strings.Contains(out, "already-launched") {
		t.Fatalf("a repeated run id must report its launch, not be refused: %q, %v", out, err)
	}
	if n := len(f.iam.SignCalls()); n != 1 {
		t.Fatalf("a repeated run id minted again: %d", n)
	}
}

// fxOf is the token's fx claim as a time.
func fxOf(t *testing.T, f *launchBudgetFixture) time.Time {
	t.Helper()
	c := claimsOf(t, f, launchedToken(t, f.cloudFixture, budgetRun))
	fx, _ := c["fx"].(json.Number)
	ms, err := fx.Int64()
	if err != nil {
		t.Fatalf("fx = %v", c["fx"])
	}
	return time.UnixMilli(ms)
}

// The token's identity window follows the TARGET job's task timeout, not the
// longest of every Fugaro job.
func TestRunFXFollowsTargetJobTimeout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target time.Duration
		other  time.Duration
		want   time.Duration // fx - launch, before queueing and slack
	}{
		{"short target, long other job", 30 * time.Minute, 30 * time.Hour, 30 * time.Minute},
		{"long target, short other job", 6 * time.Hour, 20 * time.Minute, 6 * time.Hour},
		{"no timeout on the job: Cloud Run's 10m default", 0, 30 * time.Hour, 10 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchBudget(t, "enforce")
			other := gcp.JobName("github--acme-other", "svc")
			f.run.AddJob(other, "1", "512Mi")
			f.run.SetJobTimeout(other, tc.other)
			if tc.target > 0 {
				f.run.SetJobTimeout(gcp.JobName(appSlug, "web"), tc.target)
			}
			start := time.Now()
			if _, _, err := runBudget(t); err != nil {
				t.Fatal(err)
			}
			got := fxOf(t, f).Sub(start)
			lo := tc.want + time.Hour + 5*time.Minute
			if got < lo || got > lo+2*time.Minute {
				t.Fatalf("fx - launch = %v, want about %v", got, lo)
			}
		})
	}
}

// TestRunFXFollowsNamedAccountJobTimeout is review finding #1's audit of
// every other place that derives a job from a workflow without the token:
// mintBudgetToken used to read the DEFAULT account's job timeout
// (e.be.TaskTimeout(ctx, slug, spec.Workflow)) even for a named run, which
// actually starts gcp.WorkflowVariant(spec.Workflow, spec.OAuthToken) — a
// distinct job that can have its own, different timeout. Fixed: the mint
// now reads the variant job's timeout.
func TestRunFXFollowsNamedAccountJobTimeout(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	f.appendConfig(t, "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/app]\n")
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", releaseSeal) // at once named tokens' and the seal's
	variant := gcp.WorkflowVariant("web", "acme-personal")
	f.run.AddJob(gcp.JobName(appSlug, variant), "4", "8Gi")
	f.run.SetJobTimeout(gcp.JobName(appSlug, variant), 30*time.Minute)
	f.run.SetJobTimeout(gcp.JobName(appSlug, "web"), 6*time.Hour) // the default account's job: must not be read

	start := time.Now()
	if _, _, err := runBudget(t, "--token", "acme-personal"); err != nil {
		t.Fatal(err)
	}
	got := fxOf(t, f).Sub(start)
	want := 30*time.Minute + time.Hour + 5*time.Minute
	if got < want || got > want+2*time.Minute {
		t.Fatalf("fx - launch = %v, want about %v (the named job's timeout, not the default job's 6h)", got, want)
	}
}

// TestLaunchWarnsAtEightyPercent: a launch whose repository or project daily
// counter has passed 80% of its cap prints one budget: warning line and
// launches anyway (R5); 79.9% prints none. The repository's cap here is the
// $50 default, the project's the $100 global one.
func TestLaunchWarnsAtEightyPercent(t *testing.T) {
	for _, tc := range []struct {
		counted int64
		want    bool
	}{
		{39_950_000, false}, // 79.9% of the repository's $50 day
		{40_000_000, true},  // exactly 80%
	} {
		f := newLaunchBudget(t, "enforce")
		f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": tc.counted})
		_, errOut, err := runBudget(t)
		if err != nil {
			t.Fatalf("%d counted: %v", tc.counted, err)
		}
		warned := strings.Contains(errOut, "budget: the repository's daily cap")
		if warned != tc.want {
			t.Fatalf("counted %d: warned = %v, want %v:\n%s", tc.counted, warned, tc.want, errOut)
		}
		if !launchedAny(f) {
			t.Fatalf("counted %d: the warning must not stop the launch", tc.counted)
		}
	}
	// The project's own cap warns beside the repository's.
	f := newLaunchBudget(t, "enforce")
	f.db.Set(budget.PathSpendGlobal(today()), map[string]any{"counted": 82 * usd1})
	_, errOut, err := runBudget(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "budget: the project's daily cap") || !strings.Contains(errOut, "$82.00 of $100.00") {
		t.Fatalf("the project's warning is missing:\n%s", errOut)
	}
}

// TestLaunchNotesAZeroCapInObserve: under enforce a zero daily cap refuses the
// launch, and the refusal is what speaks; under observe the launch happens,
// and there it carries the same 100% note `fugaro budget show` prints and
// watch's bar draws for a zero cap — the three surfaces agree that zero is a
// cap, not an unset one.
func TestLaunchNotesAZeroCapInObserve(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	f.db.Set("config/mode", "observe")
	f.db.Set(budget.PathCapsRepo(appSlug), map[string]any{"repo": "acme/app", "dailyMicros": 0})
	_, errOut, err := runBudget(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "budget: the repository's daily cap: $0.00 of $0.00 counted (100.0%)") {
		t.Fatalf("a zero cap drew no note:\n%s", errOut)
	}
	if !launchedAny(f) {
		t.Fatal("observe must still launch")
	}
}

// TestProxiedRepoPolicyInput: update --repo hands infra.Repo the node it read,
// an empty node when it read that the project published none, and nil when it
// read nothing — a failed read, or a config with no database address, where
// projectPolicyRead also answers not-found. Only the empty node judges the
// operator's project_secrets entry, so the no-address case must stay nil.
func TestProxiedRepoPolicyInput(t *testing.T) {
	withDB := &localcfg.Config{Budget: &localcfg.Budget{RTDBURL: "http://127.0.0.1:1"}}
	node := &projectpolicy.Policy{BitbucketToken: &projectpolicy.BitbucketToken{Proxy: true}}
	if got := repoPolicyInput(withDB, node, true, nil); got != node {
		t.Errorf("a node read = %v, want the node", got)
	}
	if got := repoPolicyInput(withDB, nil, false, nil); got == nil || got.BitbucketToken != nil {
		t.Errorf("no node published = %v, want an empty node", got)
	}
	if got := repoPolicyInput(withDB, nil, false, errors.New("unreachable")); got != nil {
		t.Errorf("a failed read = %v, want nil", got)
	}
	for name, lc := range map[string]*localcfg.Config{
		"no budget block":     {},
		"no database address": {Budget: &localcfg.Budget{}},
	} {
		if got := repoPolicyInput(lc, nil, false, nil); got != nil {
			t.Errorf("%s = %v, want nil: no node was read", name, got)
		}
	}
}
