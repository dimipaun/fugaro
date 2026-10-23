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
	"github.com/dimipaun/fugaro/internal/config"
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

// fixtureWorkflow is the fixture repository's only workflow (its
// fugaro.yaml), and the workflow a task the runner places in a job must name.
const fixtureWorkflow = "app"

// cloudRunJob is the name of the job the Cloud Run fixtures run in: the
// per-repository job of the fixture repository and the fixture's workflow,
// computed the way a run computes its own (gcp.JobName of task.Slug and
// gcp.WorkflowVariant, plan task 3.3 step 1). It is computed rather than
// spelled: a name that is neither a project job the policy computes nor this
// run's own is a halt, so a hand-written one would stop a fixture before
// whatever the test is about.
func cloudRunJob(t *testing.T) string {
	t.Helper()
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	return gcp.JobName(slug, gcp.WorkflowVariant(fixtureWorkflow, ""))
}

// setCloudRunEnv sets the environment a Cloud Run execution of the acme-app
// job sees. suffix names this execution of it ("aaaaa", "bbbbb"), as Cloud Run
// names an execution after its job.
func setCloudRunEnv(t *testing.T, suffix string) {
	t.Helper()
	job := cloudRunJob(t)
	t.Setenv("CLOUD_RUN_EXECUTION", job+"-"+suffix)
	t.Setenv("CLOUD_RUN_JOB", job)
	t.Setenv("FUGARO_GCP_PROJECT", "proj-1234")
	t.Setenv("FUGARO_PROJECT", "aurora")
	t.Setenv("FUGARO_REGION", "us-east5")
	t.Setenv("FUGARO_GIT_PROVIDER", "")
	t.Setenv("FUGARO_RUN", "")
}

func TestExecIncompleteCloudRunEnvIsUserError(t *testing.T) {
	setCloudRunEnv(t, "aaaaa")
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
	setCloudRunEnv(t, "bbbbb")
	dir := t.TempDir()
	b, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	store := runstore.Open(b, "acme-app", runID)
	if err := store.WriteTask(ctx, &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Workflow: fixtureWorkflow, Ref: "main", Task: "x"}); err != nil {
		t.Fatal(err)
	}
	first := &runstore.Record{Version: 1, RunID: runID, Repo: "acme/app", Status: runstore.StatusRunning, Stage: "implement",
		Execution: "projects/proj-1234/locations/us-east5/jobs/" + cloudRunJob(t) + "/executions/" + cloudRunJob(t) + "-aaaaa"}
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
	setCloudRunEnv(t, "aaaaa")
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

// execPlatform is the one resolved answer every check in runExec reads
// (design compute-backend.md §2.5, C12): the invocation names local, the
// platform's own signal names the backend, and everything else is in the
// cloud.
func TestExecPlatformResolution(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	run := map[string]string{"CLOUD_RUN_EXECUTION": cloudRunJob(t) + "-aaaaa", "CLOUD_RUN_JOB": cloudRunJob(t), "FUGARO_GCP_PROJECT": "proj-1234", "FUGARO_REGION": "us-east5"}
	for name, tc := range map[string]struct {
		o       execOptions
		env     map[string]string
		local   bool
		backend string
	}{
		"a local task file": {execOptions{taskFile: "/tmp/task.json"}, nil, true, backend.Local},
		"a local run is not a cloud run whatever the shell holds": {execOptions{taskFile: "/tmp/task.json"}, map[string]string{"FUGARO_BACKEND": backend.CloudRun, "FUGARO_RUN": "acme-app/20260926-221530-abcd"}, true, backend.Local},
		"a task file on a Cloud Run execution stays a cloud run":  {execOptions{taskFile: "/tmp/task.json"}, run, false, backend.CloudRun},
		// The local case is a platform that claims nothing, not the flag
		// alone: a Batch task's own markers are a platform, so the same
		// --task-file is a cloud run here (design §2.5, C12).
		"a task file on a Batch task stays a Batch task": {execOptions{taskFile: "/tmp/task.json"}, map[string]string{"BATCH_TASK_INDEX": "0", "BATCH_TASK_COUNT": "1"}, false, backend.CloudBatch},
		"a Cloud Run execution":                          {execOptions{run: "acme-app/20260926-221530-abcd"}, run, false, backend.CloudRun},
		// No marker at all, or only Fugaro's own variables: in the cloud,
		// and no backend claims it -- the fail-closed default.
		"no marker": {execOptions{run: "acme-app/20260926-221530-abcd"}, nil, false, ""},
		"a Fugaro variable is not a signal": {execOptions{run: "acme-app/20260926-221530-abcd"},
			map[string]string{"FUGARO_BACKEND": backend.CloudRun, "FUGARO_GCP_PROJECT": "proj-1234", "FUGARO_REGION": "us-east5", "FUGARO_RUN": "acme-app/20260926-221530-abcd"}, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			p := execPlatform(tc.o, get(tc.env))
			if p.Local() != tc.local || p.Backend() != tc.backend || p.InCloud() == tc.local {
				t.Fatalf("platform = local %v, backend %q, in cloud %v; want local %v, backend %q", p.Local(), p.Backend(), p.InCloud(), tc.local, tc.backend)
			}
		})
	}
}

// A process no platform claims is in the cloud, not local: `exec --run`
// with no marker is refused for the missing job environment rather than
// running as a local run with the cloud checks switched off (design
// compute-backend.md §2.5, C12).
func TestExecUnclaimedProcessIsInTheCloud(t *testing.T) {
	t.Setenv("FUGARO_RUN", "")
	t.Setenv("FUGARO_GIT_PROVIDER", "")
	t.Setenv("FUGARO_PROJECT", "")
	t.Setenv("FUGARO_GCP_PROJECT", "")
	t.Setenv("CLOUD_RUN_EXECUTION", "")
	dir := t.TempDir()
	_, _, err := execute(t, "exec", "--bucket", "file://"+dir, "--run", "acme-app/20260926-221530-abcd",
		"--workdir", filepath.Join(t.TempDir(), "work"), "--state-dir", filepath.Join(t.TempDir(), "state"))
	if ExitCode(err) != ExitRemoteError || err == nil || !strings.Contains(err.Error(), "FUGARO_GCP_PROJECT") {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	// The refusal is the cloud one, recorded as the run's infra_error the
	// way a Cloud Run job without the variables is.
	b, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	rec, rerr := runstore.Open(b, "acme-app", "20260926-221530-abcd").ReadRecord(context.Background())
	if rerr != nil || rec.Status != runstore.StatusInfraError || rec.Stage != "bootstrap" || !strings.Contains(rec.Reason, "FUGARO_GCP_PROJECT") {
		t.Fatalf("record = %+v, %v", rec, rerr)
	}
}

// cloudExec runs `exec --run` on a process no platform claims -- no marker
// at all -- whose job carries the names every job has carried since M9a, so
// the run gets past the CLI's checks and the test sees what the runner does
// with the platform the CLI resolved (design compute-backend.md §2.5). The
// run's task is in the bucket, as a launcher would have left it.
func cloudExec(t *testing.T, files map[string]string, edit func(string) string, env map[string]string, flags ...string) (string, string, error, *runstore.Store) {
	t.Helper()
	const runID = "20260926-221530-abcd"
	testutil.IsolateGit(t)
	for _, k := range []string{"FUGARO_RUN", "FUGARO_GIT_PROVIDER", "FUGARO_BUDGET_MODE", "FUGARO_MAX_RUN_USD", "FUGARO_MODEL_PRICES", "FUGARO_MANAGED_SETTINGS", "CLOUD_RUN_EXECUTION", "FUGARO_TEST_ALLOW_ROUTING_SETTINGS"} {
		t.Setenv(k, "")
	}
	// The backend's variables must be absent, not empty (an empty one is a
	// malformed setting).
	for _, k := range []string{"FUGARO_RTDB_URL", "FUGARO_FIREBASE_API_KEY", "FUGARO_BUDGET_GRACE", "FUGARO_MODEL_PROVIDERS"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("FUGARO_GCP_PROJECT", "proj-1234")
	t.Setenv("FUGARO_PROJECT", "aurora")
	for k, v := range env {
		t.Setenv(k, v)
	}
	fixture := testutil.FixtureFiles(t)
	if edit != nil {
		fixture["fugaro.yaml"] = edit(fixture["fugaro.yaml"])
	}
	for k, v := range files {
		fixture[k] = v
	}
	remote := testutil.NewRemote(t, fixture)
	dir := t.TempDir()
	b, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	// The storage slug is the launcher's: task.Slug of the provider kind and
	// the repository, never the readable name. "acme-app" is a slug no kind
	// produces, and an unclaimed process is in the cloud — so the run reaches
	// the admission check, which refuses a slug that is not its repository's
	// (the review finding on this change). A fixture that wants the checks
	// *after* the admission needs the slug a launcher would have written.
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	store := runstore.Open(b, slug, runID)
	if err := store.WriteTask(context.Background(), &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Workflow: "app", Ref: "main", Task: "x"}); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"exec", "--bucket", "file://" + dir, "--run", slug + "/" + runID, "--remote", remote,
		"--provider", "fake", "--provider-state", filepath.Join(t.TempDir(), "provider.json"),
		"--workdir", filepath.Join(t.TempDir(), "work"), "--state-dir", filepath.Join(t.TempDir(), "state")}, flags...)
	out, errOut, err := execute(t, args...)
	return out, errOut, err, store
}

// A budget database on loopback is a local run's, and an unclaimed process
// is not local: the runner refuses it at bootstrap. This is the gate the
// CLI passes `platform.Local()` to, and nothing else in the exec tests
// reaches it (design compute-backend.md §2.5).
func TestExecUnclaimedRefusesLoopbackBudget(t *testing.T) {
	_, _, err, store := cloudExec(t, nil, nil, map[string]string{
		"FUGARO_BUDGET_MODE": "observe",
		"FUGARO_RTDB_URL":    "http://127.0.0.1:9000",
	})
	// Not "FUGARO_RTDB_URL is not set": the refusal is the loopback one, so
	// the message is the backend error the CLI passed to the runner.
	if ExitCode(err) != ExitRemoteError || err == nil || !strings.Contains(err.Error(), "the budget backend in the job's environment") {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	rec, rerr := store.ReadRecord(context.Background())
	if rerr != nil || rec.Status != runstore.StatusInfraError || rec.Stage != "bootstrap" ||
		!strings.Contains(rec.Reason, "the budget backend in the job's environment") || !strings.Contains(rec.Reason, "FUGARO_RTDB_URL") {
		t.Fatalf("record = %+v, %v", rec, rerr)
	}
}

// The live test's routing knob is a local run's, and an unclaimed process is
// not local: it is in the cloud (platform.InCloud), and the CLI hands the
// runner Local = false for it. Since plan task 3.3's fix round (review C2) the
// runner reads that as a process with no job name to place and refuses it at
// the admission, before any git credential, before the checkout — so before
// the routing settings this test used to reach. The refusal is itself the
// proof that the CLI resolved the platform as the cloud: a local one skips the
// admission. The knob's own "ignored on Cloud Run" refusal is pinned in the
// runner (TestTestKnobIgnoredInCloud, Local = false).
func TestExecUnclaimedRefusesRoutingSettings(t *testing.T) {
	_, _, err, store := cloudExec(t,
		map[string]string{".claude/settings.json": `{"env": {"ANTHROPIC_BASE_URL": "https://elsewhere.invalid"}}`},
		nil,
		map[string]string{"FUGARO_TEST_ALLOW_ROUTING_SETTINGS": "1"})
	if ExitCode(err) != ExitRemoteError || err == nil || !strings.Contains(err.Error(), "not a local invocation") {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
	rec, rerr := store.ReadRecord(context.Background())
	if rerr != nil || rec.Status != runstore.StatusInfraError || rec.Stage != "bootstrap" || !strings.Contains(rec.Reason, "no Cloud Run execution") {
		t.Fatalf("record = %+v, %v", rec, rerr)
	}
}

// On Cloud Run the runner prices compute with FUGARO_COMPUTE_PRICES when it
// is set, so its PR report agrees with ls; a malformed value is a warning
// and the list price.
//
// execPrices asks the platform, not FUGARO_BACKEND (design
// compute-backend.md §5.2, site 3): the variable is the launcher's record
// of the backend it chose, so a local run whose shell exports it acquires
// no prices, and no other value can take a cloud run's away.
func TestExecPricesAsksThePlatform(t *testing.T) {
	list := gcp.ListPrices("us-east5")
	cloud := backend.DetectPlatform(func(k string) string {
		return map[string]string{"CLOUD_RUN_EXECUTION": "x"}[k]
	})
	for name, tc := range map[string]struct {
		platform backend.Platform
		env      map[string]string
		want     *backend.Prices
		warn     bool
	}{
		"override": {cloud, map[string]string{"FUGARO_REGION": "us-east5", "FUGARO_COMPUTE_PRICES": "0.00001,0.000001"},
			&backend.Prices{VCPUSecondUSD: 0.00001, GiBSecondUSD: 0.000001, Source: "local override"}, false},
		"unset":     {cloud, map[string]string{"FUGARO_REGION": "us-east5"}, &list, false},
		"malformed": {cloud, map[string]string{"FUGARO_REGION": "us-east5", "FUGARO_COMPUTE_PRICES": "cheap"}, &list, true},
		// A local invocation has no compute to estimate, whatever the
		// owner's shell holds; a platform no backend claims has no price
		// table either.
		"local":     {backend.LocalPlatform(), map[string]string{"FUGARO_BACKEND": backend.CloudRun, "FUGARO_REGION": "us-east5", "FUGARO_COMPUTE_PRICES": "0.00001,0.000001"}, nil, false},
		"unclaimed": {backend.DetectPlatform(func(string) string { return "" }), map[string]string{"FUGARO_BACKEND": backend.CloudRun, "FUGARO_REGION": "us-east5"}, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			var warned []string
			got := execPrices(tc.platform, func(k string) string { return tc.env[k] }, func(m string) { warned = append(warned, m) })
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
	setCloudRunEnv(t, "aaaaa")
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
	for _, want := range []string{"FUGARO_GCP_PROJECT", "before M9a", "fugaro update --repo"} {
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
		!strings.Contains(rec.Reason, "FUGARO_GCP_PROJECT") || !strings.Contains(rec.Reason, "fugaro update --repo") ||
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
	setCloudRunEnv(t, "bbbbb")
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
		Execution: "projects/proj-1234/locations/us-east5/jobs/" + cloudRunJob(t) + "/executions/" + cloudRunJob(t) + "-aaaaa"}
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
	setCloudRunEnv(t, "aaaaa")
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
	setCloudRunEnv(t, "aaaaa")
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
	store := runstore.Open(b, appSlug, runID)
	if err := store.WriteTask(ctx, &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Workflow: fixtureWorkflow, Ref: "main", Task: "x"}); err != nil {
		t.Fatal(err)
	}
	_, _, err = execute(t, "exec", "--bucket", "file://"+dir, "--run", appSlug+"/"+runID, "--remote", remote,
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
	return localExecCfg(t, nil, env, flags...)
}

// localExecCfg is localExec with the fixture's fugaro.yaml replaced by
// edit(its text), when edit is not nil.
func localExecCfg(t *testing.T, edit func(string) string, env map[string]string, flags ...string) (string, error, *runstore.Store) {
	t.Helper()
	const runID = "20260926-221530-abcd"
	testutil.IsolateGit(t)
	for _, k := range []string{"FUGARO_RUN", "FUGARO_GIT_PROVIDER", "FUGARO_PROJECT", "FUGARO_BUDGET_MODE", "FUGARO_MAX_RUN_USD", "FUGARO_MODEL_PRICES", "FUGARO_MANAGED_SETTINGS", "CLOUD_RUN_EXECUTION"} {
		t.Setenv(k, "")
	}
	// The backend's variables must be absent, not empty: an empty one is a
	// malformed setting (TestExecPassesBackendErr).
	for _, k := range []string{"FUGARO_RTDB_URL", "FUGARO_FIREBASE_API_KEY", "FUGARO_BUDGET_GRACE", "FUGARO_MODEL_PROVIDERS"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	files := testutil.FixtureFiles(t)
	if edit != nil {
		files["fugaro.yaml"] = edit(files["fugaro.yaml"])
	}
	remote := testutil.NewRemote(t, files)
	dir := t.TempDir()
	b, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	// The provider kind is part of the slug, and `--task-file` derives it
	// from --provider, as the CLI does for the task it writes.
	slug, err := task.Slug("fake", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	store := runstore.Open(b, slug, runID)
	// `--task-file` is the CLI's one local invocation (design
	// compute-backend.md §2.5): a local run is named by the invocation, so
	// this is what makes the checks below treat the process as local.
	taskFile := filepath.Join(t.TempDir(), "task.json")
	spec := `{"version": 1, "run_id": "` + runID + `", "repo": "acme/app", "ref": "main", "task": "x"}`
	if err := os.WriteFile(taskFile, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"exec", "--bucket", "file://" + dir, "--task-file", taskFile, "--remote", remote,
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

// The owner's model providers in the job's environment reach the runner: a
// repository the provider lists, with a provider model pinned, gets as far as
// the provider's key (not mounted here); without the variable the same file
// is refused as a model no provider serves. A malformed value is the run's
// infra_error, never a silent none.
func TestExecPassesModelProviders(t *testing.T) {
	pinned := func(cfg string) string {
		out := strings.Replace(cfg, "  review_rounds: 2\n", `  review_rounds: 2
  model: claude-sonnet-5-5
  models: { coder: deepseek/deepseek-v4-flash, reviewer: claude-sonnet-5-5, background: claude-sonnet-5-5 }
  max_output_tokens: { coder: 4096, reviewer: 4096 }
`, 1)
		if out == cfg {
			t.Fatal("fixture has no review_rounds line")
		}
		return out
	}
	budget := map[string]string{"FUGARO_BUDGET_MODE": "enforce", "FUGARO_MAX_RUN_USD": "5"}
	with := func(k, v string) map[string]string {
		m := map[string]string{k: v}
		for bk, bv := range budget {
			m[bk] = bv
		}
		return m
	}
	flags := []string{"--managed-settings", filepath.Join(t.TempDir(), "managed-settings.json"), "--gateway-upstream", "http://127.0.0.1:1"}
	pv, err := runner.ProvidersEnv(map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer", Secret: "openrouter-api-key",
		Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"},
	}}, "acme/app")
	if err != nil || pv == "" {
		t.Fatalf("ProvidersEnv = %q, %v", pv, err)
	}
	_, err, store := localExecCfg(t, pinned, with(runner.ModelProvidersEnv, pv), flags...)
	if err == nil || !strings.Contains(err.Error(), "not mounted") {
		t.Fatalf("with the provider: err = %v, want the missing key", err)
	}
	if rec, rerr := store.ReadRecord(context.Background()); rerr != nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("record = %+v, %v", rec, rerr)
	}
	if _, err, _ = localExecCfg(t, pinned, budget, flags...); err == nil || !strings.Contains(err.Error(), "no provider serves it") {
		t.Fatalf("without the provider: err = %v", err)
	}
	_, err, store = localExecCfg(t, pinned, with(runner.ModelProvidersEnv, "{not json"), flags...)
	if err == nil || !strings.Contains(err.Error(), runner.ModelProvidersEnv) {
		t.Fatalf("malformed: err = %v", err)
	}
	rec, rerr := store.ReadRecord(context.Background())
	if rerr != nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, runner.ModelProvidersEnv) {
		t.Fatalf("malformed: record = %+v, %v", rec, rerr)
	}
}
