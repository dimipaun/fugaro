package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob/fileblob"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// cloneAppYAML is the fixture repository's workflow as a checkout:
// clone one: no per-repository image (design generic-tool.md §2, G3/G4), so
// the run clones its repository itself.
const cloneAppYAML = `version: 1
project: aurora
git:
  provider: github
  base_branch: main
agent:
  auth: api-key
  review_rounds: 2
workflows:
  app:
    checkout: clone
    commands:
      build: sh build.sh
      test: sh test.sh
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
`

// cloneJobEnv is the environment a checkout: clone workflow's job carries,
// computed by infra from the repository's own local config — the production
// path (infra.Workflow), never a remote this test picks itself.
func cloneJobEnv(t *testing.T) map[string]string {
	t.Helper()
	lc := &localcfg.Config{
		Version: 1, Name: "aurora", GCPProject: "proj-1234", Region: "us-east5",
		RunsBucket: "fugaro-runs-proj-1234",
		BaseImages: map[string]string{config.BaseKind: "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc"},
		Repos:      map[string]localcfg.Repo{"acme/app": {Provider: "github", BaseBranch: "main", Workflows: []string{"app"}, GitHubAppID: "123456"}},
	}
	cfg, problems := config.Parse([]byte(cloneAppYAML))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	ws, err := infra.Workflow(infra.Inputs{LC: lc, Repo: "acme/app", Cfg: cfg}, "app")
	if err != nil {
		t.Fatal(err)
	}
	return ws.Env
}

// TestExecCloneJobClonesTheRepositoryItWasDeployedFor (review C1): a Cloud
// Run checkout: clone job runs the base image, which holds no checkout, and
// its job template sets no command, no args and (before this fix) no
// remote: the repository has to reach the runner through the job's own
// environment. infra sets FUGARO_REMOTE on a clone workflow's job, and
// fugaro exec's --remote defaults to it, so the runner clones the
// repository — not "".
//
// This runs the real path end to end: the environment is infra.Workflow's
// own, the exec command is the job's entrypoint, and no --remote flag is
// passed. Only the transport is local: a git insteadOf rule rewrites the
// repository's https URL onto the fixture's bare remote, which is exactly
// what the runner records as origin.
//
// Mutation (run, restore): drop the `RemoteEnv: c.repoURL` line from
// workflow() in internal/infra/spec.go (or the os.Getenv default of
// --remote) — the clone never happens and the run fails with "is empty and
// this job has no repository to clone" instead.
func TestExecCloneJobClonesTheRepositoryItWasDeployedFor(t *testing.T) {
	const runID = "20260926-221530-abcd"
	testutil.IsolateGit(t)
	setCloudRunEnv(t, "aaaaa")
	files := testutil.FixtureFiles(t)
	// The run must get past the clone and its configuration read to a
	// failure this test can name: the fixture's own project, edited to
	// another one, is that failure (TestExecPassesProject uses it the same
	// way), and it can only be reached from a checkout that exists.
	files["fugaro.yaml"] = strings.Replace(files["fugaro.yaml"], "project: aurora", "project: borealis", 1)
	remote := testutil.NewRemote(t, files)

	env := cloneJobEnv(t)
	want, ok := env[infra.RemoteEnv]
	if !ok || want != "https://github.com/acme/app.git" {
		t.Fatalf("%s = %q, want the repository's clone URL", infra.RemoteEnv, want)
	}
	// git itself must reach the fixture's bare remote while the job's
	// environment still names the repository's real URL.
	gitcfg := filepath.Join(t.TempDir(), "gitconfig")
	rule := "[url \"file://" + remote + "\"]\n\tinsteadOf = " + want + "\n"
	if err := os.WriteFile(gitcfg, []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
	for k, v := range env {
		t.Setenv(k, v)
	}

	dir := t.TempDir()
	b, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	store := runstore.Open(b, appSlug, runID)
	if err := store.WriteTask(ctx, &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Workflow: "app", Ref: "main", Task: "x"}); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "work")
	_, _, err = execute(t, "exec", "--bucket", "file://"+dir, "--run", appSlug+"/"+runID,
		"--provider", "fake", "--provider-state", filepath.Join(t.TempDir(), "provider.json"),
		"--workdir", work, "--state-dir", filepath.Join(t.TempDir(), "state"))
	if err == nil || !strings.Contains(err.Error(), "project mismatch") {
		t.Fatalf("err = %v, want the run to fail on the fixture's project, after its clone", err)
	}
	if got := testutil.Git(t, work, "config", "remote.origin.url"); got != want {
		t.Errorf("origin = %q, want the job's own %s (%q)", got, infra.RemoteEnv, want)
	}
	if got := testutil.Git(t, work, "config", "remote.origin.partialclonefilter"); got != "blob:none" {
		t.Errorf("the checkout's filter = %q, want blob:none: the clone must be the blobless one", got)
	}
}
