package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

func sharedObjectPath(dir string) string {
	return filepath.Join(dir, filepath.FromSlash(infra.SharedConfigObject))
}

func TestPublishSharedWritesAndOverwrites(t *testing.T) {
	f := newCloudFixture(t)
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if err := publishShared(context.Background(), lc); err != nil {
		t.Fatal(err)
	}
	path := sharedObjectPath(filepath.Join(f.dir, "runs"))
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := lc.Shared().Marshal()
	if string(got) != string(want) {
		t.Errorf("published:\n%s\nwant:\n%s", got, want)
	}
	if _, err := localcfg.Parse(got); err != nil {
		t.Errorf("the published file does not parse: %v", err)
	}
	for _, s := range []string{"user:", "endpoints:", "bucket_url", "providers:"} {
		if strings.Contains(string(got), s) {
			t.Errorf("published file contains %q", s)
		}
	}
	lc.MaxParallel = 9
	if err := publishShared(context.Background(), lc); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if !strings.Contains(string(got), "max_parallel: 9") {
		t.Errorf("the second publish did not overwrite:\n%s", got)
	}
}

func TestPublishSharedTooLarge(t *testing.T) {
	newCloudFixture(t)
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	lc.LogView = strings.Repeat("x", localcfg.SharedMaxBytes)
	if err := publishShared(context.Background(), lc); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("err = %v, want the size limit", err)
	}
}

// sharedRuns points the publisher at a file:// bucket of the test's own and
// returns its directory.
func sharedRuns(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := sharedBucketOpener
	sharedBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return blobx.Open(ctx, "file://"+dir)
	}
	t.Cleanup(func() { sharedBucketOpener = old })
	return dir
}

func TestInitConfigOnlyPublishesTheSharedConfig(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	dir := sharedRuns(t)
	if _, _, err := executeStdin(t, "", "init", "--config-only", "--yes"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sharedObjectPath(dir))
	if err != nil {
		t.Fatalf("not published: %v", err)
	}
	if lc, err := localcfg.Parse(data); err != nil || lc.Name != initProjectName || lc.Terraform.StateBucket != "" {
		t.Errorf("published %+v, %v:\n%s", lc, err, data)
	}
}

func TestInitPublishConfigAlone(t *testing.T) {
	r := newInitRig(t)
	dir := sharedRuns(t)
	out, _, err := executeStdin(t, "", "init", "--publish-config")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sharedObjectPath(dir)); err != nil {
		t.Fatalf("not published: %v\n%s", err, out)
	}
	if len(r.calls(t)) != 0 {
		t.Errorf("--publish-config ran terraform: %q", r.calls(t))
	}
}

func TestInitPublishConfigNeedsALocalConfig(t *testing.T) {
	r := newInitRig(t)
	sharedRuns(t)
	if err := os.Remove(r.cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", "")
	_, _, err := executeStdin(t, "", "init", "--publish-config", "--name", "aurora")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

func TestInitPublishConfigExcludesTheOtherModes(t *testing.T) {
	newInitRig(t)
	for _, other := range []string{"--config-only", "--plan-only", "--print-vars", "--forget"} {
		_, _, err := executeStdin(t, "", "init", "--publish-config", other)
		if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "--publish-config") || !strings.Contains(err.Error(), "exclude one another") {
			t.Errorf("%s: exit %d, err %v", other, ExitCode(err), err)
		}
	}
}

func TestInitPublishFailureWarnsAndSucceeds(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	dir := sharedRuns(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if f, err := os.Create(filepath.Join(dir, "probe")); err == nil {
		f.Close()
		t.Skip("the directory is writable despite its mode (running as root)")
	}
	out, _, err := executeStdin(t, "", "init", "--config-only", "--yes")
	if err != nil {
		t.Fatalf("a publish failure must not fail init: %v", err)
	}
	if !strings.Contains(out, "warning: could not publish the shared config: ") || !strings.Contains(out, "teammates will need fugaro init until it is published") {
		t.Errorf("no warning:\n%s", out)
	}
}

const publishedWithOtherRepo = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: unused-bucket
build:
  machine_type: E2_HIGHCPU_8
max_parallel: 2
repos:
  other/svc: { provider: github, base_branch: main, workflows: [web] }
`

func writePublished(t *testing.T, dir, content string) {
	t.Helper()
	p := sharedObjectPath(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func publishedRepos(t *testing.T, dir string) map[string]localcfg.Repo {
	t.Helper()
	data, err := os.ReadFile(sharedObjectPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse(data)
	if err != nil {
		t.Fatalf("%v:\n%s", err, data)
	}
	return lc.Repos
}

func TestPublishSharedKeepsTheReposOfOtherMachines(t *testing.T) {
	f := newCloudFixture(t)
	dir := filepath.Join(f.dir, "runs")
	writePublished(t, dir, publishedWithOtherRepo)
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	lc.Repos = nil // an adopter's config
	if err := publishShared(context.Background(), lc); err != nil {
		t.Fatal(err)
	}
	if r := publishedRepos(t, dir); len(r) != 1 || r["other/svc"].BaseBranch != "main" {
		t.Errorf("published repos = %v", r)
	}
	// A local repo is added, and wins a clash.
	lc.Repos = map[string]localcfg.Repo{
		"other/svc": {Provider: "github", BaseBranch: "dev", Workflows: []string{"web"}},
		"acme/app":  {Provider: "github", BaseBranch: "main", Workflows: []string{"web"}},
	}
	if err := publishShared(context.Background(), lc); err != nil {
		t.Fatal(err)
	}
	if r := publishedRepos(t, dir); len(r) != 2 || r["other/svc"].BaseBranch != "dev" {
		t.Errorf("published repos = %v", r)
	}
}

func TestPublishSharedIgnoresAForeignObjectAndWarns(t *testing.T) {
	f := newCloudFixture(t)
	dir := filepath.Join(f.dir, "runs")
	writePublished(t, dir, strings.Replace(publishedWithOtherRepo, "proj-1234", "other-proj", 1))
	lc, _ := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	lc.Repos = nil
	var warned []string
	if err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) }); err != nil {
		t.Fatal(err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "another installation") {
		t.Errorf("warnings = %q", warned)
	}
	if r := publishedRepos(t, dir); len(r) != 0 {
		t.Errorf("a foreign object was merged: %v", r)
	}
}

func TestPublishSharedTreatsABadExistingObjectAsAbsent(t *testing.T) {
	f := newCloudFixture(t)
	dir := filepath.Join(f.dir, "runs")
	lc, _ := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	for name, content := range map[string]string{
		"unparseable": "this: [is not\n",
		"oversized":   publishedWithOtherRepo + "# " + strings.Repeat("x", localcfg.SharedMaxBytes) + "\n",
	} {
		writePublished(t, dir, content)
		if err := publishShared(context.Background(), lc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r := publishedRepos(t, dir); len(r) != 1 || r["acme/app"].BaseBranch != "main" {
			t.Errorf("%s: published repos = %v", name, r)
		}
	}
}

func TestRepoOnboardingPublishesAndKeepsOtherReposPublished(t *testing.T) {
	e, _ := stageEngine(t, "", nil)
	dir := sharedRuns(t)
	writePublished(t, dir, publishedWithOtherRepo)
	path := filepath.Join(t.TempDir(), "aurora.yaml")
	e.lc = &localcfg.Config{Version: 1, Name: "aurora", GCPProject: initProject, Region: "us-east5", RunsBucket: initRunsBucket}
	cfg, problems := config.Parse([]byte(checkoutYAML("github", "oauth", "aurora", "")))
	if cfg == nil {
		t.Fatal(problems)
	}
	e.r.cmd.SetErr(io.Discard)
	e.r.res.Applied = true // the apply confirmed the config write
	if err := e.r.writeRepoConfig(t.Context(), e.lc, infra.RepoSpec{Name: "acme/app", Provider: "github", BaseBranch: "main", GitHubAppID: "12345"}, cfg, path, nil); err != nil {
		t.Fatal(err)
	}
	if r := publishedRepos(t, dir); len(r) != 2 || r["acme/app"].BaseBranch != "main" || r["other/svc"].BaseBranch != "main" {
		t.Errorf("published repos = %v", r)
	}
}

func TestInitPublishConfigIsInstallationOnlyForRepo(t *testing.T) {
	newInitRig(t)
	_, _, err := executeStdin(t, "", "init", "--repo", "--publish-config")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "--publish-config") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// TestPublishSharedNeverPublishesProviders: providers are local-only; the
// local ones are not published and published ones (an older publisher's)
// are not kept.
func TestPublishSharedNeverPublishesProviders(t *testing.T) {
	f := newCloudFixture(t)
	dir := filepath.Join(f.dir, "runs")
	block := "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n"
	writePublished(t, dir, publishedWithOtherRepo+block)
	if _, err := localcfg.Parse([]byte(publishedWithOtherRepo + block)); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	f.appendConfig(t, strings.ReplaceAll(strings.ReplaceAll(block, "openrouter", "local"), "deepseek", "qwen"))
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil || len(lc.Providers) != 1 {
		t.Fatalf("%v %v", lc, err)
	}
	if err := publishShared(context.Background(), lc); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(sharedObjectPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "providers") || strings.Contains(string(got), "openrouter") {
		t.Errorf("providers were published:\n%s", got)
	}
	if r := publishedRepos(t, dir); r["other/svc"].BaseBranch != "main" {
		t.Errorf("the published repos were not kept (was the fixture read as absent?): %v", r)
	}
}
