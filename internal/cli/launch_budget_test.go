package cli

import (
	"context"
	"encoding/json"
	"errors"
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
	return f
}

// readToken is the run's token object, "" when there is none.
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
	f.run.OnRun = func(c gcpfake.RunCall) { atLaunch = readToken(t, f.cloudFixture, budgetRun) }
	start := time.Now()
	if _, _, err := runBudget(t); err != nil {
		t.Fatal(err)
	}
	if atLaunch == "" {
		t.Fatal("the token object was not in the bucket when jobs.run was called")
	}
	c := claimsOf(t, f, atLaunch)
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
	if c := claimsOf(t, f, string(data)); c["rb"] != "retrier@example.com" {
		t.Fatalf("rb = %v", c["rb"])
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
	claimsOf(t, f, string(data)) // a fresh, valid mint
	if n := len(f.iam.SignCalls()); n != 1 {
		t.Fatalf("sign calls = %d", n)
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
	tok := readToken(t, f.cloudFixture, budgetRun)
	if tok == "" {
		t.Fatal("no token object")
	}
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
	_, _, err := runBudget(t)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "repo_daily_cap") {
		t.Fatalf("err = %v", err)
	}
	// Exactly the minimum left passes.
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": 49_750_000})
	if _, _, err := runBudget(t); err != nil {
		t.Fatalf("$0.25 of headroom is enough: %v", err)
	}
}

func TestPrecheckGlobalHeadroom(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	f.db.Set(budget.PathSpendGlobal(today()), map[string]any{"counted": 99_900_000})
	_, _, err := runBudget(t)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "global_daily_cap") {
		t.Fatalf("err = %v", err)
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
	// The mint still happens, and no database read did.
	if len(f.iam.SignCalls()) != 1 || readToken(t, f.cloudFixture, budgetRun) == "" || !launchedAny(f) {
		t.Fatal("the skip must still mint and launch")
	}
	if n := len(f.db.Requests()); n != 0 {
		t.Fatalf("%d database requests with the pre-check skipped", n)
	}
}

func TestBudgetOffSkipsEverything(t *testing.T) {
	for name, cfg := range map[string]string{
		"off, backend configured": "budget: { mode: off, rtdb_url: URL, token_signer: " + testSigner + " }\n",
		"no budget block":         "",
		"observe, no backend":     "budget: { mode: observe, per_run_usd: 5 }\n",
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
			if env == nil || len(iam.SignCalls()) != 0 || len(db.Requests()) != 0 || readToken(t, f, budgetRun) != "" {
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
	c := claimsOf(t, f, readToken(t, f.cloudFixture, budgetRun))
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
