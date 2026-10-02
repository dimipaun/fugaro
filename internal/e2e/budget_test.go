package e2e

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The budget backend end to end: the real fugaro binary as `fugaro exec`,
// the gateway against a fake model API, and the project's Firebase side
// (database, token exchange) played by the gcpfake servers. The launcher's
// part is played by the test: it leaves the run's token in the bucket.

const (
	e2eSigner = "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"
	e2eAPIKey = "AIzaSyFakeWebApiKeyForTests000000000"
	e2eSonnet = "claude-sonnet-5-5"
	e2eHaiku  = "claude-haiku-4-5"
)

type budgetRig struct {
	t      *testing.T
	db     *gcpfake.RTDB
	itk    *gcpfake.IdentityToolkit
	iam    *gcpfake.IAMCredentials
	upstr  *anthropicfake.Fake
	bucket string // directory
	claude string
	rec    runstore.Record
	exit   int
	out    string
}

func e2eBudgetConfig(t *testing.T, auth string) string {
	t.Helper()
	cfg := testutil.FixtureFiles(t)["fugaro.yaml"]
	if auth == "oauth" {
		return strings.Replace(cfg, "auth: api-key", "auth: oauth", 1)
	}
	out := strings.Replace(cfg, "  review_rounds: 2\n", `  review_rounds: 2
  model: `+e2eSonnet+`
  models: { coder: `+e2eSonnet+`, reviewer: `+e2eSonnet+`, background: `+e2eHaiku+` }
  max_output_tokens: { coder: 4096, reviewer: 4096 }
`, 1)
	if out == cfg {
		t.Fatal("fixture has no review_rounds line")
	}
	return out
}

// runBudget runs the scenario. onStart, when set, runs once the fake agent
// has started (the test's chance to act mid-run).
func runBudget(t *testing.T, auth, mode, script string, onStart func(r *budgetRig), replies ...anthropicfake.Reply) *budgetRig {
	t.Helper()
	testutil.IsolateGit(t)
	r := &budgetRig{t: t}
	files := testutil.FixtureFiles(t)
	files["fugaro.yaml"] = e2eBudgetConfig(t, auth)
	remote := testutil.NewRemote(t, files)
	fugaro := testutil.BuildFugaro(t)
	r.claude = testutil.FakeClaude(t, script)
	tmp := t.TempDir()
	r.bucket = filepath.Join(tmp, "bucket")
	if err := os.MkdirAll(r.bucket, 0o755); err != nil {
		t.Fatal(err)
	}
	r.db = gcpfake.NewRTDB(t)
	r.iam = gcpfake.NewIAMCredentials(t)
	r.iam.AddSigner(e2eSigner)
	r.itk = gcpfake.NewIdentityToolkit(t, r.iam, e2eAPIKey, "aurora-fp")
	var up *httptest.Server
	r.upstr, up = anthropicfake.New(t, replies...)
	m := func(v budget.Micros) *budget.Micros { return &v }
	r.db.Set("fugaro/project", "aurora")
	r.db.Set(budget.PathMode, mode)
	r.db.Set(budget.PathLimits, budget.Limits{MaxReserveMicros: m(5_000_000)})
	r.db.Set(budget.PathCapsGlobal, budget.GlobalCaps{DailyMicros: m(150_000_000), PerRunMicros: m(20_000_000)})
	r.db.Set(budget.PathCapsRepo(acmeSlug), budget.RepoCaps{DailyMicros: m(60_000_000), PerRunMicros: m(20_000_000)})

	// The launcher: mint the run's token and leave it in the bucket.
	signer, err := token.NewIAMSigner(e2eSigner, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.launcher"}), token.WithIAMEndpoint(r.iam.URL))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tok, err := token.MintAt(context.Background(), signer, token.Claims{Slug: acmeSlug, Run: runID, FX: token.ExpiryFX(now, time.Hour), FP: "aurora", RB: "dev@example.invalid"}, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := blobx.Open(context.Background(), "file://"+r.bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := token.PutObject(context.Background(), b, acmeSlug, runID, tok); err != nil {
		t.Fatal(err)
	}
	b.Close()

	taskFile := filepath.Join(tmp, "task.json")
	if err := os.WriteFile(taskFile, []byte(`{"version":1,"run_id":"`+runID+`","repo":"acme/app","ref":"main","task":"Add a feature"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(tmp, "claude-code")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	providerState := filepath.Join(tmp, "provider.json")
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	args := []string{"exec", "--bucket", "file://" + r.bucket, "--task-file", taskFile,
		"--workdir", filepath.Join(tmp, "work"), "--remote", remote, "--state-dir", filepath.Join(tmp, "state"),
		"--provider", "fake", "--provider-state", providerState, "--claude", r.claude, "--cancel-poll", "100ms",
		"--budget-identity-url", r.itk.URL}
	if auth != "oauth" {
		args = append(args, "--gateway-upstream", up.URL, "--managed-settings", filepath.Join(managed, "managed-settings.json"))
	}
	cmd := exec.CommandContext(ctx, fugaro, args...)
	env := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + tmp, "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"FUGARO_BUDGET_MODE=" + mode, "FUGARO_RTDB_URL=" + r.db.URL, "FUGARO_FIREBASE_API_KEY=" + e2eAPIKey, "FUGARO_BUDGET_GRACE=5s",
		"FIXTURE_FAILS_FILE=" + filepath.Join(tmp, "fails"),
	}
	if auth == "oauth" {
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN=oauth-e2e-token-abcdef")
	} else {
		env = append(env, "ANTHROPIC_API_KEY=sk-ant-e2e-0123456789abcdefghijklmnop")
	}
	cmd.Env = env
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGQUIT) }
	cmd.WaitDelay = 5 * time.Second
	started := make(chan struct{})
	if onStart != nil {
		callsFile := filepath.Join(filepath.Dir(r.claude), "calls.jsonl")
		go func() {
			defer close(started)
			deadline := time.Now().Add(agentStartTimeout)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(callsFile); err == nil {
					onStart(r)
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Errorf("the agent never started")
		}()
	} else {
		close(started)
	}
	out, runErr := cmd.CombinedOutput()
	<-started
	r.out = string(out)
	t.Logf("fugaro exec output (err=%v):\n%s", runErr, out)
	if cmd.ProcessState == nil {
		t.Fatalf("fugaro exec never started: %v", runErr)
	}
	r.exit = cmd.ProcessState.ExitCode()
	data, err := os.ReadFile(filepath.Join(r.bucket, "runs", acmeSlug, runID, "result.json"))
	if err != nil {
		t.Fatalf("no result.json: %v\n%s", err, out)
	}
	if err := json.Unmarshal(data, &r.rec); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Load(providerState); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *budgetRig) num(path string) int64 {
	r.t.Helper()
	switch v := r.db.Value(path).(type) {
	case nil:
		return 0
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	r.t.Fatalf("%s = %#v", path, r.db.Value(path))
	return 0
}

func TestCloudBudgetRunWithFakes(t *testing.T) {
	// One model call of $0.04 through the gateway, then a clean run.
	impl := `{"shell":"echo feature > feature.txt && git add -A && git commit -qm 'Add feature' && fugaro verify test; printf 'Add feature\\n\\nAdds feature.txt.\\n' > \"$FUGARO_STATE_DIR/pr.md\"","api":[{"model":"` + e2eSonnet + `","max_tokens":4000}],"cost":1}`
	r := runBudget(t, "api-key", "enforce", `{"calls":[`+impl+`,`+reviewShip+`]}`, nil,
		anthropicfake.MessageOK(e2eSonnet, pricing.Usage{Output: 4000}))
	if r.exit != 0 || r.rec.Status != runstore.StatusSucceeded || r.rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("exit %d, record %+v", r.exit, r.rec)
	}
	day := budget.Day(time.Now())
	if r.rec.Budget == nil || r.rec.Budget.Mode != "enforce" || r.rec.Budget.GrantedMicros == 0 {
		t.Fatalf("budget record = %+v", r.rec.Budget)
	}
	if got := r.num(budget.PathRun(acmeSlug, runID) + "/spent"); got != 40_000 {
		t.Fatalf("spent = %d, want the call's $0.04", got)
	}
	if got := r.num(budget.PathSpendGlobal(day) + "/counted"); got != 40_000 {
		t.Fatalf("global counted = %d: the unused lease must be released", got)
	}
	if out, _ := r.db.Value(budget.PathOutcome(day, acmeSlug, runID)).(map[string]any); out == nil || out["status"] != "succeeded" {
		t.Fatalf("outcome = %v", out)
	}
	if r.db.Value(budget.PathAgent(acmeSlug, runID)) != nil {
		t.Fatal("the registry entry survived the run")
	}
	if _, err := os.Stat(filepath.Join(r.bucket, "runs", acmeSlug, runID, "budget-token")); !os.IsNotExist(err) {
		t.Fatalf("the token object is still in the bucket (%v)", err)
	}
	// Nothing of the run's Firebase identity is in the bucket.
	err := filepath.WalkDir(r.bucket, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, _ := os.ReadFile(p)
		if strings.Contains(string(data), e2eAPIKey) || strings.Contains(string(data), "eyJ") {
			t.Errorf("%s holds a Firebase credential", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.out, e2eAPIKey) {
		t.Fatal("the API key is in the run's output")
	}
}

func TestCloudBudgetKillMidRun(t *testing.T) {
	impl := `{"shell":"echo feature > feature.txt && git add -A && git commit -qm 'Add feature'","sleep_s":60}`
	start := time.Now()
	r := runBudget(t, "api-key", "observe", `{"calls":[`+impl+`]}`, func(r *budgetRig) {
		time.Sleep(300 * time.Millisecond) // the kill streams are up by now
		r.db.Set(budget.PathKillGlobal, budget.Kill{On: true, By: "admin@example.invalid", Reason: "e2e", At: time.Now().UnixMilli()})
	})
	if r.exit != 0 || r.rec.Status != runstore.StatusHalted || r.rec.Outcome != runstore.OutcomeDraft ||
		r.rec.Halt == nil || r.rec.Halt.Reason != runstore.HaltKillSwitch || r.rec.Halt.Scope != "global" {
		t.Fatalf("exit %d, record %+v, halt %+v", r.exit, r.rec, r.rec.Halt)
	}
	if d := time.Since(start); d > 40*time.Second {
		t.Fatalf("the kill took %s to stop the run", d)
	}
}

func TestCloudBudgetOAuthNotional(t *testing.T) {
	impl := `{"shell":"echo feature > feature.txt && git add -A && git commit -qm 'Add feature' && fugaro verify test; printf 'Add feature\\n\\nAdds feature.txt.\\n' > \"$FUGARO_STATE_DIR/pr.md\"","cost":1,` +
		`"model_usage":{"` + e2eSonnet + `":{"inputTokens":1000,"outputTokens":500}}}`
	r := runBudget(t, "oauth", "enforce", `{"calls":[`+impl+`,`+reviewShip+`]}`, nil)
	if r.exit != 0 || r.rec.Status != runstore.StatusSucceeded {
		t.Fatalf("exit %d, record %+v", r.exit, r.rec)
	}
	day := budget.Day(time.Now())
	if got := r.num(budget.PathRun(acmeSlug, runID) + "/notional"); got != 1_500_000 {
		t.Fatalf("notional = %d", got)
	}
	if got := r.num(budget.PathSpendGlobal(day) + "/counted"); got != 0 {
		t.Fatalf("an oauth run counted %d against the caps", got)
	}
	if got := r.num(budget.PathByModel(day, acmeSlug, e2eSonnet) + "/in"); got != 1000 {
		t.Fatalf("byModel in = %d", got)
	}
}
