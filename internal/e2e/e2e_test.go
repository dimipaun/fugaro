// Package e2e runs the fugaro binary end to end against a local git remote,
// a file:// bucket, the fake provider and the fake claude.
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// childTimeout bounds a fugaro exec child: long enough for any real scenario
// (the slowest, TestStageTimeout, finishes in well under a minute even under
// heavy load), short enough that a future hang fails the test instead of
// stalling `go test` for its own -timeout. On expiry, cmd.Cancel sends
// SIGQUIT rather than killing outright, so a genuine hang leaves a goroutine
// dump in the captured output (which runScenario logs via t.Logf).
const childTimeout = 90 * time.Second

// agentStartTimeout bounds how long cancelOnAgentStart waits for the fake
// claude to record its first call before giving up and writing the cancel
// marker anyway.
const agentStartTimeout = 60 * time.Second

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
	config             string // replaces the fixture fugaro.yaml when non-empty
	script             string // fake claude script
	fails              string // tests that fail
	cancelOnAgentStart bool   // write the cancel marker once the agent has actually started
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

	runCtx, cancelRun := context.WithTimeout(context.Background(), childTimeout)
	defer cancelRun()
	cmd := exec.CommandContext(runCtx, fugaro, "exec",
		"--bucket", "file://"+bucket, "--task-file", taskFile,
		"--workdir", filepath.Join(tmp, "work"), "--remote", remote, "--state-dir", filepath.Join(tmp, "state"),
		"--provider", "fake", "--provider-state", providerState, "--claude", claude, "--cancel-poll", "100ms")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + tmp,
		"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"ANTHROPIC_API_KEY=test-key-1234", "FIXTURE_FAILS_FILE=" + failsFile, "UNDECLARED_SECRET=hunter2",
	}
	// On timeout, SIGQUIT rather than kill outright: a Go binary dumps its
	// goroutines to stderr (captured below) before exiting, so a real hang
	// is diagnosable instead of just "the test timed out".
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGQUIT) }
	cmd.WaitDelay = 5 * time.Second

	var cancelDone chan struct{}
	if sc.cancelOnAgentStart {
		cancelDone = make(chan struct{})
		callsFile := filepath.Join(filepath.Dir(claude), "calls.jsonl")
		go func() {
			defer close(cancelDone)
			deadline := time.Now().Add(agentStartTimeout)
			started := false
			for time.Now().Before(deadline) {
				if _, err := os.Stat(callsFile); err == nil {
					started = true
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !started {
				t.Errorf("the agent never started within %s", agentStartTimeout)
			}
			// The run's directory may not exist yet on a slow machine; a lost
			// cancel would let the scenario run on into review.
			marker := filepath.Join(bucket, "runs", "acme-app", runID, "cancel")
			if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
				t.Errorf("creating the cancel marker's directory: %v", err)
			}
			if err := os.WriteFile(marker, []byte("now"), 0o644); err != nil {
				t.Errorf("writing the cancel marker: %v", err)
			}
		}()
	}

	start := time.Now()
	out, runErr := cmd.CombinedOutput()
	if cancelDone != nil {
		<-cancelDone // never write the marker after we return
	}
	elapsed := time.Since(start)
	t.Logf("fugaro exec output (err=%v):\n%s", runErr, out)
	if cmd.ProcessState == nil {
		t.Fatalf("fugaro exec never started: %v", runErr)
	}
	res := result{bucket: bucket, exitCode: cmd.ProcessState.ExitCode(), elapsed: elapsed, calls: testutil.FakeClaudeCalls(t, claude)}
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
	if r.rec.Outcome != runstore.OutcomeDraft || r.rec.Reason != "tests failing on the final commit" ||
		len(r.provider.PRs) != 1 || !r.provider.PRs[0].Draft {
		t.Fatalf("record %+v, provider %+v", r.rec, r.provider)
	}
}

func TestFlakyRerunIsReady(t *testing.T) {
	flaky := `{"shell":"echo feature > feature.txt && git add -A && git commit -qm 'Add feature'; fugaro verify test; : > \"$FIXTURE_FAILS_FILE\"; fugaro verify test --rerun-failed","cost":1}`
	r := runScenario(t, scenario{script: `{"calls":[` + flaky + `,` + reviewShip + `]}`, fails: "beta"})
	if r.rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record %+v", r.rec)
	}
	if len(r.rec.Verify) == 0 {
		t.Fatalf("record has no verify entries: %+v", r.rec)
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
	r := runScenario(t, scenario{script: `{"calls":[{"sleep_s":30}]}`, cancelOnAgentStart: true})
	// A cancel during implement still finalizes: it pushes and opens a
	// draft PR, so runner.Run returns a nil error and exec exits 0.
	if r.exitCode != 0 || r.rec.Status != runstore.StatusCancelled || r.rec.Reason != "cancelled during implement" ||
		len(r.provider.PRs) != 1 || !r.provider.PRs[0].Draft {
		t.Fatalf("exit %d, record %+v, provider %+v", r.exitCode, r.rec, r.provider)
	}
}

func TestInvalidConfigExitsTwo(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "provider: github", "provider: gitlab", 1)
	r := runScenario(t, scenario{config: cfg, script: `{"calls":[]}`})
	if r.exitCode != 2 || r.rec.Status != runstore.StatusInfraError || len(r.provider.PRs) != 0 {
		t.Fatalf("exit %d, record %+v", r.exitCode, r.rec)
	}
}
