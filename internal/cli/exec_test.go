package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob/fileblob"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
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

// setCloudRunEnv sets the environment a Cloud Run execution of the
// acme-app job sees.
func setCloudRunEnv(t *testing.T, execution string) {
	t.Setenv("CLOUD_RUN_EXECUTION", execution)
	t.Setenv("CLOUD_RUN_JOB", "fugaro-acme-app-app")
	t.Setenv("FUGARO_PROJECT", "proj-1234")
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
