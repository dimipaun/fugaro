package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob/fileblob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestExecNeedsBucket(t *testing.T) {
	t.Setenv("FUGARO_BUCKET", "")
	_, _, err := execute(t, "exec", "--bucket", "")
	if err == nil || !strings.Contains(err.Error(), "--bucket") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecRejectsUnknownProvider(t *testing.T) {
	t.Setenv("FUGARO_RUN", "")
	t.Setenv("FUGARO_GIT_PROVIDER", "gitlab") // the flag's default comes from here
	_, _, err := execute(t, "exec", "--bucket", "file://"+t.TempDir(), "--run", "acme-app/20260926-221530-abcd")
	if err == nil || !strings.Contains(err.Error(), `github, bitbucket or fake, not "gitlab"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecProviderStateNeedsFake(t *testing.T) {
	t.Setenv("FUGARO_RUN", "")
	t.Setenv("FUGARO_GIT_PROVIDER", "")
	_, _, err := execute(t, "exec", "--bucket", "file://"+t.TempDir(), "--run", "acme-app/20260926-221530-abcd",
		"--provider", "bitbucket", "--provider-state", "state.json")
	if err == nil || !strings.Contains(err.Error(), "--provider-state only applies to --provider fake") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecRejectsTaskFileWithRun(t *testing.T) {
	// --run defaults from FUGARO_RUN; clear it so only the explicit --run
	// below (not a leaked ambient value) exercises the mutual-exclusion check.
	t.Setenv("FUGARO_RUN", "")
	_, _, err := execute(t, "exec", "--bucket", "file://"+t.TempDir(),
		"--task-file", "-", "--run", "acme-app/20260926-221530-abcd")
	if err == nil || !strings.Contains(err.Error(), "--task-file") || !strings.Contains(err.Error(), "--run") {
		t.Fatalf("err = %v", err)
	}
}

// The provider kind is part of the slug, so a --task-file run without it is
// refused before task.json is written anywhere.
func TestExecTaskFileNeedsProvider(t *testing.T) {
	t.Setenv("FUGARO_RUN", "")
	t.Setenv("FUGARO_GIT_PROVIDER", "")
	dir := t.TempDir()
	spec := `{"version": 1, "run_id": "20260926-221530-abcd", "repo": "acme/app", "ref": "main", "task": "x"}`
	_, _, err := executeStdin(t, spec, "exec", "--bucket", "file://"+dir, "--task-file", "-")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "--task-file needs --provider") {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	if entries, rerr := os.ReadDir(dir); rerr != nil || len(entries) > 0 {
		t.Fatalf("the refused exec wrote to the bucket: %v, %v", entries, rerr)
	}
}

// setCloudRunEnv sets the environment a Cloud Run execution of the
// acme-app job sees.
func setCloudRunEnv(t *testing.T, execution string) {
	t.Setenv("CLOUD_RUN_EXECUTION", execution)
	t.Setenv("CLOUD_RUN_JOB", "fugaro-acme-app-app")
	t.Setenv("FUGARO_GCP_PROJECT", "proj-1234")
	t.Setenv("FUGARO_PROJECT", "aurora")
	t.Setenv("FUGARO_REGION", "us-east5")
	t.Setenv("FUGARO_GIT_PROVIDER", "")
	t.Setenv("FUGARO_RUN", "")
}

func TestExecIncompleteCloudRunEnvIsUserError(t *testing.T) {
	setCloudRunEnv(t, "fugaro-acme-app-app-aaaaa")
	t.Setenv("FUGARO_REGION", "")
	dir := filepath.Join(t.TempDir(), "bucket")
	_, _, err := execute(t, "exec", "--bucket", "file://"+dir, "--run", "acme-app/20260926-221530-abcd")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "FUGARO_REGION") {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	if _, serr := os.Stat(dir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("exec touched the bucket before failing: %v", serr)
	}
}

func TestExecDuplicateExecutionExitsRemoteError(t *testing.T) {
	const runID = "20260926-221530-abcd"
	setCloudRunEnv(t, "fugaro-acme-app-app-bbbbb")
	dir := t.TempDir()
	b, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	store := runstore.Open(b, "acme-app", runID)
	if err := store.WriteTask(ctx, &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "x"}); err != nil {
		t.Fatal(err)
	}
	first := &runstore.Record{Version: 1, RunID: runID, Repo: "acme/app", Status: runstore.StatusRunning, Stage: "implement",
		Execution: "projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-app/executions/fugaro-acme-app-app-aaaaa"}
	if err := store.WriteRecord(ctx, first); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "exec", "--bucket", "file://"+dir, "--run", "acme-app/"+runID,
		"--workdir", filepath.Join(t.TempDir(), "work"), "--state-dir", filepath.Join(t.TempDir(), "state"))
	if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "another execution already owns this run") {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	if out != "" {
		t.Fatalf("a duplicate printed a record: %s", out)
	}
	if stored, err := store.ReadRecord(ctx); err != nil || stored.Stage != "implement" || stored.Execution != first.Execution {
		t.Fatalf("the duplicate touched result.json: %+v, %v", stored, err)
	}
}

func TestExecRejectsTaskFileOnCloudRun(t *testing.T) {
	setCloudRunEnv(t, "fugaro-acme-app-app-aaaaa")
	dir := filepath.Join(t.TempDir(), "bucket")
	_, _, err := execute(t, "exec", "--bucket", "file://"+dir, "--task-file", "-")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--task-file is for local runs") {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	if _, serr := os.Stat(dir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("exec touched the bucket before failing: %v", serr)
	}
}

// TestExecRefusesHTTP2Debug: with GODEBUG's
// http2debug, Go would print the runner's bearer tokens to its logs.
func TestExecRefusesHTTP2Debug(t *testing.T) {
	t.Setenv("FUGARO_RUN", "")
	t.Setenv("FUGARO_GIT_PROVIDER", "")
	t.Setenv("GODEBUG", "http2debug=2")
	_, _, err := execute(t, "exec", "--bucket", "file://"+t.TempDir(), "--run", "acme-app/20260926-221530-abcd")
	if err == nil || !strings.Contains(err.Error(), "http2debug") {
		t.Fatalf("err = %v", err)
	}
}

// On Cloud Run the runner prices compute with FUGARO_COMPUTE_PRICES when it
// is set, so its PR report agrees with ls; a malformed value is a warning
// and the list price. Local runs estimate no compute.
func TestExecUsesPriceEnv(t *testing.T) {
	list := gcp.ListPrices("us-east5")
	for name, tc := range map[string]struct {
		env  map[string]string
		want *backend.Prices
		warn bool
	}{
		"override": {map[string]string{"FUGARO_BACKEND": backend.CloudRun, "FUGARO_REGION": "us-east5", "FUGARO_COMPUTE_PRICES": "0.00001,0.000001"},
			&backend.Prices{VCPUSecondUSD: 0.00001, GiBSecondUSD: 0.000001, Source: "local override"}, false},
		"unset":     {map[string]string{"FUGARO_BACKEND": backend.CloudRun, "FUGARO_REGION": "us-east5"}, &list, false},
		"malformed": {map[string]string{"FUGARO_BACKEND": backend.CloudRun, "FUGARO_REGION": "us-east5", "FUGARO_COMPUTE_PRICES": "cheap"}, &list, true},
		"local":     {map[string]string{"FUGARO_COMPUTE_PRICES": "0.00001,0.000001"}, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			var warned []string
			got := execPrices(func(k string) string { return tc.env[k] }, func(m string) { warned = append(warned, m) })
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("prices = %+v, want %+v", got, tc.want)
			}
			if tc.warn != (len(warned) == 1) || tc.warn && !strings.Contains(warned[0], "FUGARO_COMPUTE_PRICES") {
				t.Fatalf("warnings = %q", warned)
			}
		})
	}
}

// TestExecFakeProviderKnowsRepoAndRemote: `exec --provider fake` opens the
// fake for the task's repository, reading branch heads from --remote, so a
// follow-up's checks see the same PR a real host would show.
func TestExecFakeProviderKnowsRepoAndRemote(t *testing.T) {
	state := filepath.Join(t.TempDir(), "provider.json")
	kind, open, err := providerOptions(execOptions{provider: "fake", providerState: state, remote: "/srv/remote.git"}, nil, nil)
	if err != nil || kind != "" {
		t.Fatalf("providerOptions = %q, %v", kind, err)
	}
	p, secrets, err := open(context.Background(), "github", "acme/app")
	if err != nil || len(secrets) != 0 {
		t.Fatalf("open = %v, %v", secrets, err)
	}
	f, ok := p.(*fake.Provider)
	if !ok || f.Repo != "acme/app" || f.Remote != "/srv/remote.git" || f.Path != state || f.SelfID != "fugaro-bot" {
		t.Fatalf("provider = %#v", p)
	}
}

// A job set up before M9a has FUGARO_PROJECT = the GCP ID and no
// FUGARO_GCP_PROJECT. exec writes the run's record and exits 2 before the
// runner starts, so the old value is never read as a project name.
func TestExecOldJobEnvSaysInitRepo(t *testing.T) {
	const runID = "20260926-221530-abcd"
	setCloudRunEnv(t, "fugaro-acme-app-app-aaaaa")
	t.Setenv("FUGARO_GCP_PROJECT", "")
	t.Setenv("FUGARO_PROJECT", "proj-1234")
	dir := t.TempDir()
	b, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	store := runstore.Open(b, "acme-app", runID)
	if err := store.WriteTask(ctx, &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Workflow: "app", Ref: "main", Task: "x"}); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "work")
	out, _, err := execute(t, "exec", "--bucket", "file://"+dir, "--run", "acme-app/"+runID,
		"--workdir", work, "--state-dir", filepath.Join(t.TempDir(), "state"))
	if ExitCode(err) != ExitRemoteError || err == nil {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	for _, want := range []string{"FUGARO_GCP_PROJECT", "before M9a", "fugaro init --repo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "mismatch") {
		t.Errorf("the old FUGARO_PROJECT was compared with a name: %v", err)
	}
	rec, err := store.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone || rec.Stage != "bootstrap" ||
		!strings.Contains(rec.Reason, "FUGARO_GCP_PROJECT") || !strings.Contains(rec.Reason, "fugaro init --repo") ||
		strings.Contains(rec.Reason, "mismatch") || rec.Execution != "" || rec.Repo != "acme/app" || rec.Workflow != "app" {
		t.Errorf("record = %+v", rec)
	}
	if rec.FinishedAt == nil {
		t.Error("the record is final and has no finished_at")
	}
	if out != "" {
		t.Errorf("printed a run record: %s", out)
	}
	if _, serr := os.Stat(work); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("the runner started (a workdir exists): %v", serr)
	}
}

func TestExecOldJobEnvKeepsExistingRecord(t *testing.T) {
	const runID = "20260926-221530-abcd"
	setCloudRunEnv(t, "fugaro-acme-app-app-bbbbb")
	t.Setenv("FUGARO_GCP_PROJECT", "")
	dir := t.TempDir()
	b, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	store := runstore.Open(b, "acme-app", runID)
	first := &runstore.Record{Version: 1, RunID: runID, Repo: "acme/app", Status: runstore.StatusRunning, Stage: "implement",
		Execution: "projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-app/executions/fugaro-acme-app-app-aaaaa"}
	if err := store.WriteRecord(ctx, first); err != nil {
		t.Fatal(err)
	}
	_, _, err = execute(t, "exec", "--bucket", "file://"+dir, "--run", "acme-app/"+runID)
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	if stored, err := store.ReadRecord(ctx); err != nil || stored.Stage != "implement" || stored.Status != runstore.StatusRunning || stored.Execution != first.Execution {
		t.Fatalf("the old-env exec replaced result.json: %+v, %v", stored, err)
	}
}

// A job with FUGARO_GCP_PROJECT but no project name fails the same way.
func TestExecJobEnvWithoutProjectName(t *testing.T) {
	const runID = "20260926-221530-abcd"
	setCloudRunEnv(t, "fugaro-acme-app-app-aaaaa")
	t.Setenv("FUGARO_PROJECT", "")
	dir := t.TempDir()
	_, _, err := execute(t, "exec", "--bucket", "file://"+dir, "--run", "acme-app/"+runID)
	if ExitCode(err) != ExitRemoteError || err == nil || !strings.Contains(err.Error(), "FUGARO_PROJECT") {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
}

// FUGARO_PROJECT reaches the runner: a repository that names another
// project is refused before the run does anything else.
func TestExecPassesProject(t *testing.T) {
	const runID = "20260926-221530-abcd"
	testutil.IsolateGit(t)
	setCloudRunEnv(t, "fugaro-acme-app-app-aaaaa")
	files := testutil.FixtureFiles(t)
	files["fugaro.yaml"] = strings.Replace(files["fugaro.yaml"], "project: aurora", "project: borealis", 1)
	remote := testutil.NewRemote(t, files)
	dir := t.TempDir()
	b, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	store := runstore.Open(b, "acme-app", runID)
	if err := store.WriteTask(ctx, &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "x"}); err != nil {
		t.Fatal(err)
	}
	_, _, err = execute(t, "exec", "--bucket", "file://"+dir, "--run", "acme-app/"+runID, "--remote", remote,
		"--workdir", filepath.Join(t.TempDir(), "work"), "--state-dir", filepath.Join(t.TempDir(), "state"))
	if ExitCode(err) != ExitRemoteError || err == nil ||
		!strings.Contains(err.Error(), "project mismatch: fugaro.yaml on main names project borealis; this job belongs to project aurora") {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	rec, err := store.ReadRecord(ctx)
	if err != nil || rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("record = %+v, %v", rec, err)
	}
}

// localExec runs `exec` for a local run of the fixture repository (fake
// provider, no Cloud Run variables) with extra environment and flags, and
// returns the stdout, the error and the store of the run.
func localExec(t *testing.T, env map[string]string, flags ...string) (string, error, *runstore.Store) {
	t.Helper()
	const runID = "20260926-221530-abcd"
	testutil.IsolateGit(t)
	for _, k := range []string{"FUGARO_RUN", "FUGARO_GIT_PROVIDER", "FUGARO_PROJECT", "FUGARO_BUDGET_MODE", "FUGARO_MAX_RUN_USD", "FUGARO_MODEL_PRICES", "FUGARO_MANAGED_SETTINGS", "CLOUD_RUN_EXECUTION"} {
		t.Setenv(k, "")
	}
	// The backend's variables must be absent, not empty: an empty one is a
	// malformed setting (TestExecPassesBackendErr).
	for _, k := range []string{"FUGARO_RTDB_URL", "FUGARO_FIREBASE_API_KEY", "FUGARO_BUDGET_GRACE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	remote := testutil.NewRemote(t, testutil.FixtureFiles(t))
	dir := t.TempDir()
	b, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	store := runstore.Open(b, "acme-app", runID)
	if err := store.WriteTask(context.Background(), &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "x"}); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"exec", "--bucket", "file://" + dir, "--run", "acme-app/" + runID, "--remote", remote,
		"--provider", "fake", "--provider-state", filepath.Join(t.TempDir(), "provider.json"),
		"--workdir", filepath.Join(t.TempDir(), "work"), "--state-dir", filepath.Join(t.TempDir(), "state")}, flags...)
	out, _, err := execute(t, args...)
	return out, err, store
}

// The budget in the job's environment reaches the runner: enforce with a cap
// and the fixture's unpinned models fails at the pin check, which only a
// run that is under a budget gets to.
func TestExecSpendFromEnv(t *testing.T) {
	_, err, store := localExec(t, map[string]string{"FUGARO_BUDGET_MODE": "enforce", "FUGARO_MAX_RUN_USD": "5"})
	if ExitCode(err) != ExitRemoteError || err == nil || !strings.Contains(err.Error(), "agent.models.coder: is required with a budget") {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	rec, rerr := store.ReadRecord(context.Background())
	if rerr != nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("record = %+v, %v", rec, rerr)
	}
}

// A malformed budget is the runner's infra_error after it claimed the
// record; exec writes no record of its own for it.
func TestExecPassesSpendErr(t *testing.T) {
	out, err, store := localExec(t, map[string]string{"FUGARO_BUDGET_MODE": "enforce", "FUGARO_MAX_RUN_USD": "lots"})
	if ExitCode(err) != ExitRemoteError || err == nil || !strings.Contains(err.Error(), "FUGARO_MAX_RUN_USD") {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	rec, rerr := store.ReadRecord(context.Background())
	if rerr != nil || rec.Status != runstore.StatusInfraError || rec.Stage != "bootstrap" ||
		!strings.Contains(rec.Reason, "the budget in the job's environment") || !strings.Contains(rec.Reason, `"lots"`) {
		t.Fatalf("record = %+v, %v", rec, rerr)
	}
	if !strings.Contains(out, `"infra_error"`) {
		t.Fatalf("the runner's record was not printed: %s", out)
	}
}

// A malformed backend (a database URL that is no Firebase database) is the
// same: the runner's infra_error, never a silent run without the backend.
func TestExecPassesBackendErr(t *testing.T) {
	for _, env := range []map[string]string{
		{"FUGARO_RTDB_URL": "https://evil.example.invalid"},
		{"FUGARO_BUDGET_GRACE": "an hour"},
	} {
		env["FUGARO_BUDGET_MODE"] = "observe"
		_, err, store := localExec(t, env)
		if ExitCode(err) != ExitRemoteError || err == nil || !strings.Contains(err.Error(), "the budget backend in the job's environment") {
			t.Fatalf("%v: err = %v (exit %d)", env, err, ExitCode(err))
		}
		rec, rerr := store.ReadRecord(context.Background())
		if rerr != nil || rec.Status != runstore.StatusInfraError {
			t.Fatalf("record = %+v, %v", rec, rerr)
		}
	}
}

// A bootstrap halt is an outcome, not a failure: exec exits 0 and prints the
// halted record. The trigger is real: an api-key run that enforces with no cap.
func TestExecBootstrapHaltExitsZero(t *testing.T) {
	out, err, store := localExec(t, map[string]string{"FUGARO_BUDGET_MODE": "enforce"})
	if err != nil || ExitCode(err) != 0 {
		t.Fatalf("err = %v (exit %d), want exit 0", err, ExitCode(err))
	}
	if !strings.Contains(out, `"status": "halted"`) || !strings.Contains(out, `"reason": "no_cap"`) {
		t.Fatalf("stdout:\n%s", out)
	}
	rec, rerr := store.ReadRecord(context.Background())
	if rerr != nil || rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeNone || rec.Halt == nil || rec.Halt.Reason != runstore.HaltNoCap {
		t.Fatalf("record = %+v, %v", rec, rerr)
	}
}

func TestExecGatewayUpstreamLoopbackOnly(t *testing.T) {
	for _, bad := range []string{
		"https://api.anthropic.com", "http://example.com:80", "http://127.0.0.1", "http://127.0.0.1:8080/v1",
		"http://localhost:8080", "http://user@127.0.0.1:8080", "http://127.0.0.1:0", "http://127.0.0.1:99999", "ftp://127.0.0.1:21", "http://127.0.0.1:8080?x=1",
	} {
		_, err, _ := localExec(t, nil, "--gateway-upstream", bad)
		if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "--gateway-upstream") {
			t.Errorf("%s: err = %v (exit %d)", bad, err, ExitCode(err))
		}
	}
	if err := checkTestHooks(execOptions{gatewayUpstream: "http://127.0.0.1:8080"}, runner.Spend{}, nil, ""); err != nil {
		t.Errorf("a loopback upstream was refused: %v", err)
	}
}

// The managed settings path moves only with a loopback upstream, or when no
// gateway runs: a job with the real upstream writes the real path.
func TestExecManagedSettingsFlag(t *testing.T) {
	on := runner.Spend{Mode: "observe"}
	off := runner.Spend{Mode: "off"}
	lo := "http://127.0.0.1:8080"
	cases := []struct {
		name     string
		o        execOptions
		spend    runner.Spend
		spendErr error
		fileMode string
		refused  bool
	}{
		{"committed enforce under an off ceiling", execOptions{managedSettings: "/tmp/m.json"}, off, nil, "enforce", true},
		{"committed observe, no ceiling", execOptions{managedSettings: "/tmp/m.json"}, runner.Spend{}, nil, "observe", true},
		{"committed off", execOptions{managedSettings: "/tmp/m.json"}, off, nil, "off", false},
		{"budget on, real upstream", execOptions{managedSettings: "/tmp/m.json"}, on, nil, "", true},
		{"budget on, real upstream, even a real URL", execOptions{managedSettings: "/tmp/m.json", gatewayUpstream: "https://api.anthropic.com"}, on, nil, "", true},
		{"malformed budget counts as on", execOptions{managedSettings: "/tmp/m.json"}, runner.Spend{}, errors.New("bad"), "", true},
		{"budget on, loopback upstream", execOptions{managedSettings: "/tmp/m.json", gatewayUpstream: lo}, on, nil, "", false},
		{"budget off", execOptions{managedSettings: "/tmp/m.json"}, off, nil, "", false},
		{"unset", execOptions{}, on, nil, "", false},
	}
	for _, c := range cases {
		err := checkTestHooks(c.o, c.spend, c.spendErr, c.fileMode)
		if c.refused != (err != nil) || (err != nil && ExitCode(err) != ExitUserError) {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
	// Through the command, with the variable instead of the flag.
	_, err, _ := localExec(t, map[string]string{"FUGARO_BUDGET_MODE": "observe", "FUGARO_MANAGED_SETTINGS": filepath.Join(t.TempDir(), "m.json")})
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "--managed-settings") {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
}

// The budget backend's Firebase Auth URL moves only to a loopback address.
func TestExecIdentityURLFlag(t *testing.T) {
	for _, bad := range []string{"https://identitytoolkit.googleapis.com", "http://example.invalid:80", "http://127.0.0.1:8080/x", "http://localhost:80"} {
		if err := checkTestHooks(execOptions{identityURL: bad}, runner.Spend{}, nil, ""); err == nil || ExitCode(err) != ExitUserError {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
	if err := checkTestHooks(execOptions{identityURL: "http://127.0.0.1:9099"}, runner.Spend{}, nil, ""); err != nil {
		t.Errorf("a loopback URL was refused: %v", err)
	}
}
