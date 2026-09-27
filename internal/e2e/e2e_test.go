// Package e2e runs the fugaro binary end to end against a local git remote,
// a file:// bucket, the fake provider and the fake claude.
package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const runID = "20260926-221530-abcd"

const implementOK = `{"shell":"echo feature > feature.txt && git add -A && git commit -qm 'Add feature' && fugaro verify test; printf 'Add feature\\n\\nAdds feature.txt.\\n' > \"$FUGARO_STATE_DIR/pr.md\"","text":"done, key ${ANTHROPIC_API_KEY}","cost":1}`
const reviewShip = `{"structured":{"verdict":"ship","findings":[]},"text":"LGTM","cost":0.5}`

type result struct {
	rec      runstore.Record
	provider fake.State
	calls    []testutil.FakeCall
	bucket   string
	exitCode int
	elapsed  time.Duration
}

type scenario struct {
	config   string // replaces the fixture fugaro.yaml when non-empty
	script   string // fake claude script
	fails    string // tests that fail
	cancelAt time.Duration
}

func runScenario(t *testing.T, sc scenario) result {
	t.Helper()
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	if sc.config != "" {
		files["fugaro.yaml"] = sc.config
	}
	remote := testutil.NewRemote(t, files)
	fugaro := testutil.BuildFugaro(t)
	claude := testutil.FakeClaude(t, sc.script)
	tmp := t.TempDir()
	bucket := filepath.Join(tmp, "bucket")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatal(err)
	}
	failsFile := filepath.Join(tmp, "fails")
	if err := os.WriteFile(failsFile, []byte(sc.fails), 0o644); err != nil {
		t.Fatal(err)
	}
	taskFile := filepath.Join(tmp, "task.json")
	if err := os.WriteFile(taskFile, []byte(`{"version":1,"run_id":"`+runID+`","repo":"acme/app","ref":"main","task":"Add a feature"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	providerState := filepath.Join(tmp, "provider.json")
	cmd := exec.Command(fugaro, "exec",
		"--bucket", "file://"+bucket, "--task-file", taskFile,
		"--workdir", filepath.Join(tmp, "work"), "--remote", remote, "--state-dir", filepath.Join(tmp, "state"),
		"--provider", "fake", "--provider-state", providerState, "--claude", claude, "--cancel-poll", "100ms")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + tmp,
		"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"ANTHROPIC_API_KEY=test-key-1234", "FIXTURE_FAILS_FILE=" + failsFile, "UNDECLARED_SECRET=hunter2",
	}
	if sc.cancelAt > 0 {
		go func() {
			time.Sleep(sc.cancelAt)
			marker := filepath.Join(bucket, "runs", "acme-app", runID, "cancel")
			_ = os.WriteFile(marker, []byte("now"), 0o644)
		}()
	}
	start := time.Now()
	out, _ := cmd.CombinedOutput()
	res := result{bucket: bucket, exitCode: cmd.ProcessState.ExitCode(), elapsed: time.Since(start), calls: testutil.FakeClaudeCalls(t, claude)}
	data, err := os.ReadFile(filepath.Join(bucket, "runs", "acme-app", runID, "result.json"))
	if err != nil {
		t.Fatalf("no result.json: %v\n%s", err, out)
	}
	if err := json.Unmarshal(data, &res.rec); err != nil {
		t.Fatal(err)
	}
	if res.provider, err = fake.Load(providerState); err != nil {
		t.Fatal(err)
	}
	t.Logf("fugaro exec output:\n%s", out)
	return res
}

func TestReadyRun(t *testing.T) {
	r := runScenario(t, scenario{script: `{"calls":[` + implementOK + `,` + reviewShip + `]}`})
	if r.exitCode != 0 || r.rec.Status != runstore.StatusSucceeded || r.rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("exit %d, record %+v", r.exitCode, r.rec)
	}
	if len(r.provider.PRs) != 1 || r.provider.PRs[0].Draft || r.provider.PRs[0].Spec.Title != "Add feature" {
		t.Fatalf("provider = %+v", r.provider)
	}
	if len(r.calls) != 2 || r.calls[0].Prompt != "Add a feature" || !slices.Contains(r.calls[1].Args, "--json-schema") {
		t.Fatalf("calls = %+v", r.calls)
	}
	env := r.calls[0].Env
	if !slices.Contains(env, "ANTHROPIC_API_KEY=test-key-1234") || !slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "FIXTURE_FAILS_FILE=") }) {
		t.Fatalf("agent env lacks declared variables: %v", env)
	}
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "UNDECLARED_SECRET=") }) {
		t.Fatal("an undeclared variable reached the agent")
	}
	transcript, err := os.ReadFile(filepath.Join(r.bucket, "runs", "acme-app", runID, "transcripts", "implement-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(transcript), "test-key-1234") || !strings.Contains(string(transcript), "[REDACTED]") {
		t.Fatalf("transcript not redacted: %s", transcript)
	}
	if _, err := os.Stat(filepath.Join(r.bucket, "runs", "acme-app", runID, "report.md")); err != nil {
		t.Fatal("report.md missing")
	}
}

func TestFailingTestsDraft(t *testing.T) {
	r := runScenario(t, scenario{script: `{"calls":[` + implementOK + `,` + reviewShip + `]}`, fails: "beta"})
	if r.rec.Outcome != runstore.OutcomeDraft || r.rec.Reason != "tests failing on the final commit" || !r.provider.PRs[0].Draft {
		t.Fatalf("record %+v", r.rec)
	}
}

func TestFlakyRerunIsReady(t *testing.T) {
	flaky := `{"shell":"echo feature > feature.txt && git add -A && git commit -qm 'Add feature'; fugaro verify test; : > \"$FIXTURE_FAILS_FILE\"; fugaro verify test --rerun-failed","cost":1}`
	r := runScenario(t, scenario{script: `{"calls":[` + flaky + `,` + reviewShip + `]}`, fails: "beta"})
	if r.rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record %+v", r.rec)
	}
	last := r.rec.Verify[len(r.rec.Verify)-1]
	if !last.Rerun || !slices.Equal(last.Flaky, []string{"pkg.Suite.beta"}) {
		t.Fatalf("last verify = %+v", last)
	}
}

func TestFixRound(t *testing.T) {
	changes := `{"structured":{"verdict":"changes","findings":[{"summary":"add a test"}]}}`
	fix := `{"shell":"echo more >> feature.txt && git commit -qam 'Address review' && fugaro verify test"}`
	r := runScenario(t, scenario{script: `{"calls":[` + implementOK + `,` + changes + `,` + fix + `,` + reviewShip + `]}`})
	if r.rec.Outcome != runstore.OutcomeReady || len(r.rec.Reviews) != 2 || len(r.calls) != 4 {
		t.Fatalf("record %+v, %d calls", r.rec, len(r.calls))
	}
	implSession := r.calls[0].Args[slices.Index(r.calls[0].Args, "--session-id")+1]
	fixArgs := r.calls[2].Args
	if i := slices.Index(fixArgs, "--resume"); i < 0 || fixArgs[i+1] != implSession {
		t.Fatalf("fix args %q do not resume %s", fixArgs, implSession)
	}
}

func TestStageTimeout(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }",
		"timeouts: { total: 1m, stage: 2s, verify: 30s, finalize_reserve: 10s }", 1)
	r := runScenario(t, scenario{config: cfg, script: `{"calls":[{"sleep_s":30}]}`})
	if r.rec.Status != runstore.StatusFailed || !strings.Contains(r.rec.Reason, "stage implement timed out") || len(r.provider.PRs) != 1 {
		t.Fatalf("record %+v", r.rec)
	}
	if r.elapsed > 20*time.Second {
		t.Fatalf("run took %s; the stage timeout did not stop the agent", r.elapsed)
	}
}

func TestCancel(t *testing.T) {
	r := runScenario(t, scenario{script: `{"calls":[{"sleep_s":30}]}`, cancelAt: time.Second})
	if r.rec.Status != runstore.StatusCancelled || r.rec.Reason != "cancelled during implement" || len(r.provider.PRs) != 1 || !r.provider.PRs[0].Draft {
		t.Fatalf("record %+v", r.rec)
	}
}

func TestInvalidConfigExitsTwo(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "provider: github", "provider: gitlab", 1)
	r := runScenario(t, scenario{config: cfg, script: `{"calls":[]}`})
	if r.exitCode != 2 || r.rec.Status != runstore.StatusInfraError || len(r.provider.PRs) != 0 {
		t.Fatalf("exit %d, record %+v", r.exitCode, r.rec)
	}
}
