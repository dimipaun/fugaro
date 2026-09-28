package e2e

import (
	"bufio"
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The cloud end-to-end tests play Cloud Run with the gcpfake servers: the
// CLI (run, ls, logs, diagnose, cancel) talks to the Run and Logging fakes
// and a file:// runs bucket, and every jobs.run starts the same fugaro
// binary as `fugaro exec`, with the environment Cloud Run and the job would
// give it, its stderr fed to the Logging fake as Cloud Run ingests a
// container's structured output. Nothing else joins the two sides, so these
// tests prove that the CLI and the runner agree on every name and object.

const (
	cloudProject  = "proj-1234"
	cloudRegion   = "us-east5"
	cloudWorkflow = "app"
	// cloudProvider is acme/app's git provider in the local config, and so
	// part of its slug. The runner uses the fake provider (--provider fake);
	// it never derives the slug, which arrives whole in FUGARO_RUN.
	cloudProvider = "github"
)

// cloudSecret is the runner's ANTHROPIC_API_KEY, which the agent prints
// raw, as base64 and as wrapped base64. It is long enough that its base64
// wraps at both 64 and 76 columns. The CLI's own environment never holds
// it, so its redaction of reserved secrets can't hide a runner leak.
const cloudSecret = "e2e-planted-secret-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-9876543210"

// cloudSlug is acme/app's slug as the CLI derives it.
var cloudSlug = mustSlug(cloudProvider, "acme/app")

type cloudRig struct {
	t          *testing.T
	fugaro     string
	claude     string
	cfg        string
	bucket     string // file:// URL
	remote     string
	provider   string
	job        string
	cancelPoll time.Duration
	run        *gcpfake.Run
	logging    *gcpfake.Logging

	mu      sync.Mutex
	calls   []gcpfake.RunCall
	outputs map[string]string // every CLI and exec output by source, for the secret scan
	execs   sync.WaitGroup    // the executions OnRun started
}

// newCloudRig sets up the fakes, the bucket and the local config. The
// runner checks for the cancel marker every cancelPoll.
func newCloudRig(t *testing.T, claudeScript string, cancelPoll time.Duration) *cloudRig {
	t.Helper()
	testutil.IsolateGit(t)
	r := &cloudRig{t: t, fugaro: testutil.BuildFugaro(t), cancelPoll: cancelPoll, run: gcpfake.NewRun(t), logging: gcpfake.NewLogging(t)}
	// Start names executions in the job's own project and region, as
	// Cloud Run does for a retried or duplicated execution.
	r.run.Project, r.run.Region = cloudProject, cloudRegion
	// Registered after the fakes, so it runs before they close: no
	// execution outlives the test.
	t.Cleanup(r.wait)
	r.remote = testutil.NewRemote(t, testutil.FixtureFiles(t))
	r.claude = testutil.FakeClaude(t, claudeScript)
	dir := t.TempDir()
	bucket := filepath.Join(dir, "runs")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatal(err)
	}
	r.provider = filepath.Join(dir, "provider.json")
	r.bucket = "file://" + bucket
	r.cfg = filepath.Join(dir, "config.yaml")
	cfg := "version: 1\nproject: " + cloudProject + "\nregion: " + cloudRegion + "\nruns_bucket: unused-bucket\n" +
		"bucket_url: '" + r.bucket + "'\nuser: someone@example.com\n" +
		"endpoints:\n  run: '" + r.run.URL + "/'\n  logging: '" + r.logging.URL + "/'\n  no_auth: true\n" +
		"repos:\n  acme/app: { provider: " + cloudProvider + ", base_branch: main, workflows: [" + cloudWorkflow + "] }\n"
	if err := os.WriteFile(r.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.job = gcp.JobName(cloudSlug, cloudWorkflow)
	r.run.AddJob(r.job, "4", "8Gi")
	// SetOnRun, not an assignment: the requests come from the CLI's
	// process, so the race detector can't see an assignment happen first.
	// Everything above happens before it.
	r.run.SetOnRun(func(c gcpfake.RunCall) {
		full := r.fullName(c.Execution)
		r.run.SetState(full, backend.StateRunning)
		tmp := t.TempDir() // here, while the test is surely running
		// Under r.mu, so that wait sees the Add happen first.
		r.mu.Lock()
		r.calls = append(r.calls, c)
		r.execs.Add(1)
		r.mu.Unlock()
		go func() {
			defer r.execs.Done()
			r.execute(full, c.Env["FUGARO_RUN"], tmp)
		}()
	})
	return r
}

// wait waits for every execution OnRun has started. OnRun's Add is
// under r.mu, so taking it first orders that Add before this Wait.
func (r *cloudRig) wait() {
	r.mu.Lock()
	r.mu.Unlock() // empty on purpose: it only orders OnRun's Adds before the Wait
	r.execs.Wait()
}

// fullName is the canonical name of the job's execution short.
func (r *cloudRig) fullName(short string) string {
	return backend.ExecID{Project: cloudProject, Region: cloudRegion, Job: r.job, Name: short}.String()
}

// execute runs `fugaro exec` as execution full (already running in the
// fake) of run, as Cloud Run would, and returns its exit code. A hard
// cancel through the fake kills it, as Cloud Run stops the container.
func (r *cloudRig) execute(full, run, tmp string) int {
	id, _ := backend.ParseExecution(full)
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.fugaro, "exec", "--bucket", r.bucket, "--run", run,
		"--workdir", filepath.Join(tmp, "work"), "--remote", r.remote, "--state-dir", filepath.Join(tmp, "state"),
		"--provider", "fake", "--provider-state", r.provider, "--claude", r.claude, "--cancel-poll", r.cancelPoll.String())
	failsFile := filepath.Join(tmp, "fails")
	if err := os.WriteFile(failsFile, nil, 0o644); err != nil {
		r.t.Errorf("writing the fails file: %v", err)
		return -1
	}
	cmd.Env = append(withoutEnv(os.Environ(), "ANTHROPIC_API_KEY", "FUGARO_RUN", "FUGARO_BUCKET"),
		"CLOUD_RUN_EXECUTION="+id.Name, "CLOUD_RUN_JOB="+id.Job,
		"FUGARO_BACKEND=cloud-run", "FUGARO_PROJECT="+cloudProject, "FUGARO_REGION="+cloudRegion,
		"ANTHROPIC_API_KEY="+cloudSecret, "FIXTURE_FAILS_FILE="+failsFile, "HOME="+tmp)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGQUIT) } // a hang leaves a goroutine dump
	cmd.WaitDelay = 5 * time.Second
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.t.Errorf("exec stderr: %v", err)
		return -1
	}
	if err := cmd.Start(); err != nil {
		r.t.Errorf("starting exec: %v", err)
		return -1
	}
	exited := make(chan struct{})
	go func() { // Cloud Run stops the container of a cancelled execution
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-exited:
				return
			case <-t.C:
				if r.run.State(full) == backend.StateCancelled {
					_ = cmd.Process.Kill()
					return
				}
			}
		}
	}()
	var log bytes.Buffer
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := append(sc.Bytes(), '\n')
		log.Write(line)
		r.logging.AddJSONLines(full, line)
	}
	err = cmd.Wait()
	close(exited)
	code := cmd.ProcessState.ExitCode()
	r.t.Logf("fugaro exec %s (exit %d, err %v):\n%s", id.Name, code, err, log.String())
	r.record("fugaro exec "+id.Name, stdout.String()+"\n"+log.String())
	if !r.run.State(full).Terminal() {
		state := backend.StateSucceeded
		if code != 0 {
			state = backend.StateFailed
		}
		r.run.SetState(full, state)
	}
	return code
}

// withoutEnv drops the named variables from env.
func withoutEnv(env []string, names ...string) []string {
	var out []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, n := range names {
			drop = drop || name == n
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// record keeps a command's output for checkNoSecret.
func (r *cloudRig) record(what, out string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outputs == nil {
		r.outputs = map[string]string{}
	}
	what += " #" + strconv.Itoa(len(r.outputs))
	r.outputs[what] = out
}

// cli runs the fugaro CLI, outside any checkout, and returns its stdout.
// Its environment holds no ANTHROPIC_API_KEY.
func (r *cloudRig) cli(args ...string) (string, error) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.fugaro, args...)
	cmd.Env = append(withoutEnv(os.Environ(), "ANTHROPIC_API_KEY", "FUGARO_CONFIG"), "FUGARO_CONFIG="+r.cfg)
	cmd.Dir = r.t.TempDir() // not a checkout
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r.record("fugaro "+strings.Join(args, " "), stdout.String()+"\n"+stderr.String())
	if err != nil {
		r.t.Logf("fugaro %s: %v\nstderr:\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), err
}

// lsRun is id's row in ls --json of acme/app, and ls's totals.
func (r *cloudRig) lsRun(id string, extra ...string) (map[string]any, map[string]any) {
	r.t.Helper()
	out, err := r.cli(append([]string{"ls", "--json", "--repo", "acme/app", "--since", "0"}, extra...)...)
	if err != nil {
		r.t.Fatalf("ls: %v", err)
	}
	var got struct {
		Runs   []map[string]any `json:"runs"`
		Totals map[string]any   `json:"totals"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		r.t.Fatalf("ls --json: %v\n%s", err, out)
	}
	for _, row := range got.Runs {
		if row["run_id"] == id {
			return row, got.Totals
		}
	}
	return nil, got.Totals
}

// waitStatus waits until ls shows run id in one of the statuses want.
func (r *cloudRig) waitStatus(id string, want ...string) map[string]any {
	r.t.Helper()
	deadline := time.Now().Add(childTimeout)
	last := map[string]any(nil)
	for time.Now().Before(deadline) {
		row, _ := r.lsRun(id)
		if row != nil {
			last = row
			if s, _ := row["status"].(string); contains(want, s) {
				return row
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	r.t.Fatalf("run %s never reached %v; last row %v", id, want, last)
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// waitAgent waits until run id's agent has started, so a cancel lands
// mid-stage.
func (r *cloudRig) waitAgent(id string) {
	r.t.Helper()
	callsFile := filepath.Join(filepath.Dir(r.claude), "calls.jsonl")
	deadline := time.Now().Add(agentStartTimeout)
	for {
		if _, err := os.Stat(callsFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("the agent never started within %s", agentStartTimeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if row := r.waitStatus(id, "running"); row["stage"] != "implement" {
		r.t.Fatalf("row = %v", row)
	}
}

// store opens run id's objects in the bucket.
func (r *cloudRig) store(id string) (*runstore.Store, func()) {
	r.t.Helper()
	b, err := blob.OpenBucket(context.Background(), r.bucket)
	if err != nil {
		r.t.Fatal(err)
	}
	return runstore.Open(b, cloudSlug, id), func() { _ = b.Close() }
}

// launched is run id's launch.json and result.json.
func (r *cloudRig) launched(id string) (*runstore.Launch, *runstore.Record) {
	r.t.Helper()
	s, done := r.store(id)
	defer done()
	l, err := s.ReadLaunch(context.Background())
	if err != nil {
		r.t.Fatalf("launch.json: %v", err)
	}
	rec, err := s.ReadRecord(context.Background())
	if err != nil {
		r.t.Fatalf("result.json: %v", err)
	}
	return l, rec
}

// runCalls are the jobs.run calls so far.
func (r *cloudRig) runCalls() []gcpfake.RunCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]gcpfake.RunCall(nil), r.calls...)
}

// duplicate starts a second execution of run as Cloud Run might (a retried
// task, or a double launch that slipped through) and waits for it.
func (r *cloudRig) duplicate(run string) (full string, code int) {
	r.t.Helper()
	full = r.run.Start(r.job)
	r.run.SetState(full, backend.StateRunning)
	r.execs.Add(1)
	defer r.execs.Done()
	return full, r.execute(full, run, r.t.TempDir())
}

// checkNoSecret fails if any form of cloudSecret appears in the output of
// a CLI command or an execution (whose stderr is what the Logging fake
// ingested), a bucket object, the provider's state or a request the CLI
// sent the Logging fake.
// A wrapped base64 line shorter than 16 characters (the last) is not
// checked: the redactor may leave a few bytes of the secret visible there.
func (r *cloudRig) checkNoSecret() {
	r.t.Helper()
	forms := []string{cloudSecret, base64.StdEncoding.EncodeToString([]byte(cloudSecret)),
		base64.StdEncoding.EncodeToString([]byte(cloudSecret + "\n"))}
	for _, w := range []int{64, 76} {
		for _, line := range wrap(base64.StdEncoding.EncodeToString([]byte(cloudSecret)), w) {
			if len(line) >= 16 {
				forms = append(forms, line)
			}
		}
	}
	check := func(where, s string) {
		for _, f := range forms {
			if strings.Contains(s, f) {
				r.t.Errorf("%s holds the secret (as %q)", where, f)
			}
		}
	}
	r.mu.Lock()
	for what, out := range r.outputs {
		check("the output of "+what, out)
	}
	r.mu.Unlock()
	root := strings.TrimPrefix(r.bucket, "file://")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			r.t.Errorf("reading %s: %v", p, err)
			return nil
		}
		check("bucket object "+p, string(data))
		return nil
	})
	if data, err := os.ReadFile(r.provider); err == nil {
		check("provider state", string(data))
	}
	for _, req := range r.logging.Requests() {
		check("logging request", string(req.Body))
	}
}

// wrap splits s into lines of at most w characters.
func wrap(s string, w int) []string {
	var out []string
	for len(s) > w {
		out, s = append(out, s[:w]), s[w:]
	}
	return append(out, s)
}

// plantingImplement is implementOK's work, with the agent also printing
// its ANTHROPIC_API_KEY raw, as base64, and as base64 wrapped at 76 (GNU
// base64) and 64 (openssl) columns, and returning it in its result text.
func plantingImplement(t *testing.T) string {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString([]byte(cloudSecret))
	shell := `echo feature > feature.txt && git add -A && git commit -qm 'Add feature' && fugaro verify test; ` +
		`printf 'Add feature\n\nAdds feature.txt.\n' > "$FUGARO_STATE_DIR/pr.md"; ` +
		`echo "planted-raw: $ANTHROPIC_API_KEY"; ` +
		`echo 'planted-b64: ` + b64 + `'; ` +
		`printf 'planted-wrap76:\n` + strings.Join(wrap(b64, 76), `\n`) + `\n'; ` +
		`printf 'planted-wrap64:\n` + strings.Join(wrap(b64, 64), `\n`) + `\n'`
	call, err := json.Marshal(map[string]any{"shell": shell, "text": "done, key ${ANTHROPIC_API_KEY}", "cost": 1})
	if err != nil {
		t.Fatal(err)
	}
	return string(call)
}

// newRunID mints a run ID of now, so ls's default window holds it.
func newRunID(t *testing.T) string {
	t.Helper()
	id, err := task.NewRunID(time.Now(), crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCloudRunLsLogsDiagnose(t *testing.T) {
	r := newCloudRig(t, `{"calls":[`+plantingImplement(t)+`,`+reviewShip+`]}`, 100*time.Millisecond)
	id := newRunID(t)
	run := cloudSlug + "/" + id
	out, err := r.cli("run", "--repo", "acme/app", "--run-id", id, "--batch", "e2e", "--json", "Add a feature")
	var launch struct{ Run, Status, Execution string }
	if err != nil || json.Unmarshal([]byte(out), &launch) != nil || launch.Status != "launched" {
		t.Fatalf("run: %s, %v", out, err)
	}
	// One slug: the CLI's, the one jobs.run handed the runner, and where
	// the runner wrote.
	calls := r.runCalls()
	if launch.Run != run || len(calls) != 1 || calls[0].Env["FUGARO_RUN"] != run || calls[0].Job != r.job {
		t.Fatalf("launch %+v, jobs.run calls %+v; want run %s on job %s", launch, calls, run, r.job)
	}
	full := r.fullName(calls[0].Execution)
	if !backend.SameExecution(launch.Execution, full) {
		t.Fatalf("run reported execution %q, jobs.run started %q", launch.Execution, full)
	}

	row := r.waitStatus(id, "succeeded", "failed", "infra_error", "cancelled")
	if row["status"] != "succeeded" || row["pr_url"] == nil || row["pr_url"] == "" || row["stage"] != "writeback" ||
		row["run"] != run || row["batch"] != "e2e" || row["requested_by"] != "someone@example.com" {
		t.Fatalf("row = %v", row)
	}
	// The runner's recorded name and launch.json's are the same execution
	// (C-1), the one jobs.run started, and ls shows it.
	l, rec := r.launched(id)
	if !backend.SameExecution(rec.Execution, l.Execution) || !backend.SameExecution(l.Execution, full) ||
		!backend.SameExecution(row["execution"].(string), full) {
		t.Fatalf("record execution %q, launch %q, ls %v; jobs.run started %q", rec.Execution, l.Execution, row["execution"], full)
	}
	if rec.RunID != id || rec.Repo != "acme/app" || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record %+v", rec)
	}

	// A repeated launch reports the run instead of starting another.
	again, err := r.cli("run", "--repo", "acme/app", "--run-id", id, "--batch", "e2e", "--json", "Add a feature")
	if err != nil || !strings.Contains(again, `"already-launched"`) || len(r.runCalls()) != 1 || len(r.run.Executions()) != 1 {
		t.Fatalf("repeat run: %s, %v; executions %v", again, err, r.run.Executions())
	}

	// A second execution of the finished run finds its record and exits
	// without touching it.
	before, err := os.ReadFile(filepath.Join(strings.TrimPrefix(r.bucket, "file://"), "runs", cloudSlug, id, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	dup, code := r.duplicate(run)
	after, _ := os.ReadFile(filepath.Join(strings.TrimPrefix(r.bucket, "file://"), "runs", cloudSlug, id, "result.json"))
	if code == 0 || !bytes.Equal(before, after) {
		t.Fatalf("duplicate execution %s: exit %d, result.json changed: %v", dup, code, !bytes.Equal(before, after))
	}
	if row, _ := r.lsRun(id); row["status"] != "succeeded" || !backend.SameExecution(row["execution"].(string), full) {
		t.Fatalf("after the duplicate, row = %v", row)
	}

	// logs reads the execution launch.json names, runner lines and the
	// relay's agent lines alike, with the planted secret redacted.
	logs, err := r.cli("logs", id)
	if err != nil || !strings.Contains(logs, "stage started") || !strings.Contains(logs, "[implement/agent] result success") ||
		!strings.Contains(logs, "[review/agent] result success") {
		t.Fatalf("logs: %s, %v", logs, err)
	}
	for _, want := range []string{"planted-raw: [REDACTED]", "planted-b64: [REDACTED]", "planted-wrap76:", "planted-wrap64:"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %q:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "duplicate execution") {
		t.Errorf("logs of %s show the duplicate execution's lines:\n%s", id, logs)
	}
	if _, err := r.cli("logs", "--json", run); err != nil {
		t.Fatalf("logs --json: %v", err)
	}

	diag, err := r.cli("diagnose", "--json", id)
	var d struct {
		Row          map[string]any `json:"row"`
		AgentMessage string         `json:"agent_message"`
		LogTail      []string       `json:"log_tail"`
	}
	if err != nil || json.Unmarshal([]byte(diag), &d) != nil || d.Row["status"] != "succeeded" || d.Row["pr_url"] != row["pr_url"] ||
		!backend.SameExecution(d.Row["execution"].(string), full) {
		t.Fatalf("diagnose: %s, %v", diag, err)
	}
	if _, err := r.cli("diagnose", run); err != nil {
		t.Fatalf("diagnose: %v", err)
	}

	st, err := fake.Load(r.provider)
	if err != nil || len(st.PRs) != 1 || st.PRs[0].Draft || st.PRs[0].URL != row["pr_url"] {
		t.Fatalf("provider = %+v, %v", st, err)
	}

	// Cost reaches ls's totals: the agent's two calls (1 + 0.5, billed
	// under auth: api-key) and the compute estimate.
	batchRow, totals := r.lsRun(id, "--batch", "e2e")
	model, _ := totals["model_usd"].(float64)
	total, _ := totals["total_usd"].(float64)
	if batchRow == nil || totals["runs"] != float64(1) || model != 1.5 || total < model {
		t.Fatalf("ls --batch e2e: row %v, totals %v", batchRow, totals)
	}
	if _, totals := r.lsRun(id, "--batch", "other"); totals["runs"] != float64(0) {
		t.Fatalf("ls --batch other: totals %v", totals)
	}
	// Plain ls: the local config's repositories, the last 7 days.
	if out, err := r.cli("ls"); err != nil || !strings.Contains(out, id) || !strings.Contains(out, "succeeded") {
		t.Fatalf("ls: %s, %v", out, err)
	}

	// cancel of a finished run changes nothing.
	if out, err := r.cli("cancel", "--json", id); err != nil || !strings.Contains(out, `"already-finished"`) {
		t.Fatalf("cancel after the end: %s, %v", out, err)
	}
	r.checkNoSecret()
}

func TestCloudCancel(t *testing.T) {
	r := newCloudRig(t, `{"calls":[{"sleep_s":60,"text":"never","cost":0.1}]}`, 100*time.Millisecond)
	id := newRunID(t)
	run := cloudSlug + "/" + id
	if _, err := r.cli("run", "--repo", "acme/app", "--run-id", id, "Slow task"); err != nil {
		t.Fatal(err)
	}
	r.waitAgent(id)
	full := r.fullName(r.runCalls()[0].Execution)

	// A duplicate execution while the owner runs exits without writing,
	// and doesn't take the run from its owner.
	dup, code := r.duplicate(run)
	if _, rec := r.launched(id); code == 0 || !backend.SameExecution(rec.Execution, full) || rec.Status != runstore.StatusRunning {
		t.Fatalf("duplicate execution %s: exit %d, record %+v", dup, code, rec)
	}

	out, err := r.cli("cancel", "--json", "--grace", "60s", "--poll", "200ms", id)
	var c struct {
		Status       string
		Marker, Hard bool
		PR           string
	}
	if err != nil || json.Unmarshal([]byte(out), &c) != nil || c.Status != "finalized" || !c.Marker || c.Hard || c.PR == "" {
		t.Fatalf("cancel: %s, %v", out, err)
	}
	row := r.waitStatus(id, "cancelled", "succeeded", "failed", "infra_error")
	if row["status"] != "cancelled" || row["pr_url"] != c.PR || !backend.SameExecution(row["execution"].(string), full) {
		t.Fatalf("a cancelled run after bootstrap must still leave a draft PR: %v", row)
	}
	_, rec := r.launched(id)
	if rec.Reason != "cancelled during implement" || !backend.SameExecution(rec.Execution, full) {
		t.Fatalf("record %+v", rec)
	}
	st, err := fake.Load(r.provider)
	if err != nil || len(st.PRs) != 1 || !st.PRs[0].Draft {
		t.Fatalf("provider = %+v, %v", st, err)
	}
	// The runner finalized and exited: the execution was never stopped
	// through Cloud Run.
	r.wait()
	if s := r.run.State(full); s != backend.StateSucceeded {
		t.Fatalf("execution state %s, want succeeded (finalized, not hard-cancelled)", s)
	}
	if out, err := r.cli("diagnose", "--json", id); err != nil || !strings.Contains(out, `"cancelled"`) {
		t.Fatalf("diagnose: %s, %v", out, err)
	}
	r.checkNoSecret()
}

func TestCloudHardCancel(t *testing.T) {
	// The runner looks for the cancel marker only hourly, so the grace
	// period runs out and cancel stops the execution through Cloud Run.
	r := newCloudRig(t, `{"calls":[{"sleep_s":20,"text":"never","cost":0.1}]}`, time.Hour)
	id := newRunID(t)
	if _, err := r.cli("run", "--repo", "acme/app", "--run-id", id, "Slow task"); err != nil {
		t.Fatal(err)
	}
	r.waitAgent(id)
	full := r.fullName(r.runCalls()[0].Execution)
	out, err := r.cli("cancel", "--json", "--grace", "1s", "--grace-floor", "1s", "--poll", "200ms", id)
	var c struct {
		Status       string
		Marker, Hard bool
	}
	if err != nil || json.Unmarshal([]byte(out), &c) != nil || c.Status != "cancelled" || !c.Marker || !c.Hard {
		t.Fatalf("cancel: %s, %v", out, err)
	}
	r.wait() // the fake Cloud Run killed the container
	if s := r.run.State(full); s != backend.StateCancelled {
		t.Fatalf("execution state %s, want cancelled", s)
	}
	// The runner never finalized: result.json still says running, and the
	// views say so from the execution, with no PR.
	if _, rec := r.launched(id); rec.Status != runstore.StatusRunning {
		t.Fatalf("record %+v", rec)
	}
	row, _ := r.lsRun(id)
	if row["status"] != "cancelled" || row["reason"] != "execution cancelled before the run finalized" || row["pr_url"] != nil {
		t.Fatalf("row = %v", row)
	}
	if out, err := r.cli("cancel", "--json", id); err != nil || !strings.Contains(out, `"already-finished"`) {
		t.Fatalf("second cancel: %s, %v", out, err)
	}
	if out, err := r.cli("diagnose", "--json", id); err != nil || !strings.Contains(out, `"cancelled"`) {
		t.Fatalf("diagnose: %s, %v", out, err)
	}
	r.checkNoSecret()
}
