package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

func sharedObjectPath(dir string) string {
	return filepath.Join(dir, filepath.FromSlash(infra.SharedConfigObject))
}

// publishable is the cloud fixture's config made publishable: the
// convention runs bucket name and the installation's registry host (the
// fixture reaches its bucket through bucket_url, which is not published).
func publishable(t *testing.T) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	lc.RunsBucket = "fugaro-runs-proj-1234"
	lc.RegistryHost = "us-east5-docker.pkg.dev/proj-1234"
	return lc
}

func TestPublishSharedWritesAndOverwrites(t *testing.T) {
	f := newCloudFixture(t)
	lc := publishable(t)
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
	lc := publishable(t)
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

// A launcher's --publish-config is refused with the operator text and exit
// 1, since 0.7.0 only operators may write fugaro/ (bucket-iam.md H8).
func TestInitPublishConfigAsLauncherIsRefused(t *testing.T) {
	r := newInitRig(t)
	old := sharedBucketOpener
	sharedBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return r.gcs.Bucket(t, initRunsBucket), nil
	}
	t.Cleanup(func() { sharedBucketOpener = old })
	r.gcs.DenyWrites(initRunsBucket, "fugaro/")
	_, _, err := executeStdin(t, "", "init", "--publish-config")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "operator role") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// A plain `fugaro init` run (not --publish-config) only warns when the
// shared config publish is forbidden: the installation itself still
// succeeds, exit 0 (initRun.publishSharedConfig, init.go).
func TestInitWarnsWhenSharedConfigPublishIsForbidden(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	old := sharedBucketOpener
	sharedBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return r.gcs.Bucket(t, initRunsBucket), nil
	}
	t.Cleanup(func() { sharedBucketOpener = old })
	r.gcs.DenyWrites(initRunsBucket, "fugaro/")
	out, _, err := executeStdin(t, "", "init", "--yes")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if !strings.Contains(out, "could not publish the shared config") || !strings.Contains(out, "operator role") {
		t.Fatalf("no warning naming the operator role:\n%s", out)
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
runs_bucket: fugaro-runs-proj-1234
registry_host: us-east5-docker.pkg.dev/proj-1234
build:
  machine_type: E2_HIGHCPU_8
max_parallel: 2
repos:
  other/svc: { provider: github, workflows: [svc] }
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
	lc := publishable(t)
	lc.Repos = nil // an adopter's config
	if err := publishShared(context.Background(), lc); err != nil {
		t.Fatal(err)
	}
	if r := publishedRepos(t, dir); len(r) != 1 || r["other/svc"].Workflows[0] != "svc" {
		t.Errorf("published repos = %v", r)
	}
	// A local repo is added, and wins a clash.
	lc.Repos = map[string]localcfg.Repo{
		"other/svc": {Provider: "github", BaseBranch: "dev", Workflows: []string{"dev"}},
		"acme/app":  {Provider: "github", BaseBranch: "main", Workflows: []string{"web"}},
	}
	if err := publishShared(context.Background(), lc); err != nil {
		t.Fatal(err)
	}
	if r := publishedRepos(t, dir); len(r) != 2 || r["other/svc"].Workflows[0] != "dev" || r["other/svc"].BaseBranch != "" {
		t.Errorf("published repos = %v", r)
	}
}

func TestPublishSharedIgnoresAForeignObjectAndWarns(t *testing.T) {
	f := newCloudFixture(t)
	dir := filepath.Join(f.dir, "runs")
	writePublished(t, dir, strings.Replace(publishedWithOtherRepo, "gcp_project: proj-1234", "gcp_project: other-proj", 1))
	lc := publishable(t)
	lc.Repos = nil
	var warned []string
	if _, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) }); err != nil {
		t.Fatal(err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "the published shared config was refused (") || !strings.Contains(warned[0], "gcp_project") || !strings.Contains(warned[0], "replacing it") {
		t.Errorf("warnings = %q", warned)
	}
	if r := publishedRepos(t, dir); len(r) != 0 {
		t.Errorf("a foreign object was merged: %v", r)
	}
}

func TestPublishSharedTreatsABadExistingObjectAsAbsent(t *testing.T) {
	f := newCloudFixture(t)
	dir := filepath.Join(f.dir, "runs")
	lc := publishable(t)
	for name, content := range map[string]string{
		"unparseable": "this: [is not\n",
		"oversized":   publishedWithOtherRepo + "# " + strings.Repeat("x", localcfg.SharedMaxBytes) + "\n",
	} {
		writePublished(t, dir, content)
		if err := publishShared(context.Background(), lc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r := publishedRepos(t, dir); len(r) != 1 || r["acme/app"].Workflows[0] != "web" {
			t.Errorf("%s: published repos = %v", name, r)
		}
	}
}

func TestRepoOnboardingPublishesAndKeepsOtherReposPublished(t *testing.T) {
	e, _ := stageEngine(t, "", nil)
	dir := sharedRuns(t)
	writePublished(t, dir, publishedWithOtherRepo)
	path := filepath.Join(t.TempDir(), "aurora.yaml")
	e.lc = &localcfg.Config{Version: 1, Name: "aurora", GCPProject: initProject, Region: "us-east5", RunsBucket: initRunsBucket, RegistryHost: "us-east5-docker.pkg.dev/" + initProject}
	cfg, problems := config.Parse([]byte(checkoutYAML("github", "oauth", "aurora", "")))
	if cfg == nil {
		t.Fatal(problems)
	}
	e.r.cmd.SetErr(io.Discard)
	e.r.res.Applied = true // the apply confirmed the config write
	if err := e.r.writeRepoConfig(t.Context(), e.lc, infra.RepoSpec{Name: "acme/app", Provider: "github", BaseBranch: "main", GitHubAppID: "12345"}, cfg, path, nil); err != nil {
		t.Fatal(err)
	}
	if r := publishedRepos(t, dir); len(r) != 2 || r["acme/app"].Provider != "github" || r["other/svc"].Workflows[0] != "svc" {
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
// local ones are not published, and a published object carrying some (an
// older publisher's) is refused and replaced, not merged.
func TestPublishSharedNeverPublishesProviders(t *testing.T) {
	f := newCloudFixture(t)
	dir := filepath.Join(f.dir, "runs")
	block := "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n"
	writePublished(t, dir, publishedWithOtherRepo+block)
	if _, err := localcfg.Parse([]byte(publishedWithOtherRepo + block)); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	f.appendConfig(t, strings.ReplaceAll(strings.ReplaceAll(block, "openrouter", "local"), "deepseek", "qwen"))
	lc := publishable(t)
	if len(lc.Providers) != 1 {
		t.Fatalf("%v", lc.Providers)
	}
	var warned []string
	if _, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) }); err != nil {
		t.Fatal(err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "refused") || !strings.Contains(warned[0], "providers") {
		t.Errorf("warnings = %q", warned)
	}
	got, err := os.ReadFile(sharedObjectPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "providers") || strings.Contains(string(got), "openrouter") {
		t.Errorf("providers were published:\n%s", got)
	}
	if r := publishedRepos(t, dir); len(r) != 1 || r["acme/app"].Provider != "github" {
		t.Errorf("published repos = %v; want only the local acme/app", r)
	}
}

// TestPublishSharedRefusesALaunderedObject: a published object that Parse
// accepts but ParseShared refuses (a tampered signer, a base_branch) is
// never merged from: it is replaced, with a warning naming why.
func TestPublishSharedRefusesALaunderedObject(t *testing.T) {
	budget := "budget:\n  mode: observe\n  rtdb_url: https://evil-proj-default-rtdb.firebaseio.com\n  firebase_project: evil-proj\n  token_signer: fugaro-token-signer@evil-proj.iam.gserviceaccount.com\n"
	for name, content := range map[string]string{
		"foreign budget": publishedWithOtherRepo + budget,
		"base_branch":    strings.Replace(publishedWithOtherRepo, "workflows: [svc]", "base_branch: attacker, workflows: [svc]", 1),
	} {
		t.Run(name, func(t *testing.T) {
			f := newCloudFixture(t)
			dir := filepath.Join(f.dir, "runs")
			if _, err := localcfg.Parse([]byte(content)); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			writePublished(t, dir, content)
			lc := publishable(t)
			var warned []string
			if _, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) }); err != nil {
				t.Fatal(err)
			}
			if len(warned) != 1 || !strings.Contains(warned[0], "the published shared config was refused (") || !strings.Contains(warned[0], "replacing it") {
				t.Errorf("warnings = %q", warned)
			}
			got, _ := os.ReadFile(sharedObjectPath(dir))
			if strings.Contains(string(got), "evil-proj") || strings.Contains(string(got), "other/svc") || strings.Contains(string(got), "attacker") {
				t.Errorf("the refused object was merged:\n%s", got)
			}
		})
	}
}

// TestPublishSharedSelfCheck: what would be written is checked with
// ParseShared first; a file teammates would refuse is not published, and
// what is there stays as it was.
func TestPublishSharedSelfCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*localcfg.Config)
		want   string
	}{
		"base image outside the registry": {func(lc *localcfg.Config) {
			lc.BaseImages = map[string]string{"web-node": "ghcr.io/dimipaun/fugaro-web-node:1"}
		}, "base_images.web-node"},
		"separate Firebase project": {func(lc *localcfg.Config) {
			lc.Budget = &localcfg.Budget{Mode: "observe", RTDBURL: "https://my-fp1-default-rtdb.firebaseio.com", FirebaseProject: "my-fp1", TokenSigner: "fugaro-token-signer@my-fp1.iam.gserviceaccount.com"}
		}, "separate Firebase project"},
		"registry host of another region": {func(lc *localcfg.Config) { lc.RegistryHost = "us-west1-docker.pkg.dev/proj-1234" }, "registry_host"},
		"non-default runs bucket":         {func(lc *localcfg.Config) { lc.RunsBucket = "my-own-runs" }, "default runs bucket name"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newCloudFixture(t)
			dir := filepath.Join(f.dir, "runs")
			writePublished(t, dir, publishedWithOtherRepo)
			lc := publishable(t)
			tc.mutate(lc)
			var warned []string
			if _, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) }); err != nil {
				t.Fatal(err)
			}
			if len(warned) != 1 || !strings.Contains(warned[0], "not publishing the shared config: ") || !strings.Contains(warned[0], tc.want) {
				t.Errorf("warnings = %q, want one naming %q", warned, tc.want)
			}
			if got, _ := os.ReadFile(sharedObjectPath(dir)); string(got) != publishedWithOtherRepo {
				t.Errorf("the object was rewritten:\n%s", got)
			}
		})
	}
}

// TestInitPublishConfigSaysSoWhenItDoesNotPublish: a refused self-check
// warns and is never reported as published.
func TestInitPublishConfigSaysSoWhenItDoesNotPublish(t *testing.T) {
	r := newInitRig(t)
	dir := sharedRuns(t)
	data, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.cfg, []byte(strings.Replace(string(data), "runs_bucket: fugaro-runs-proj-1234", "runs_bucket: my-own-runs", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "init", "--publish-config")
	// An explicit publish request that publishes nothing is a failure.
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if strings.Contains(out, "published the shared config") || !strings.Contains(out, "not publishing the shared config") || !strings.Contains(out, "default runs bucket name") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(sharedObjectPath(dir)); !os.IsNotExist(err) {
		t.Errorf("published anyway: %v", err)
	}
}

// TestPublishSharedOversizeMergeFallsBackToLocal: a valid published object
// just under the cap (a writer bloated model_prices) would push every merge
// over it; the publisher then replaces it with this machine's view instead
// of failing for good.
func TestPublishSharedOversizeMergeFallsBackToLocal(t *testing.T) {
	f := newCloudFixture(t)
	dir := filepath.Join(f.dir, "runs")
	pub, err := localcfg.Parse([]byte(publishedWithOtherRepo))
	if err != nil {
		t.Fatal(err)
	}
	one, two := 1.0, 2.0
	pub.ModelPrices = map[string]localcfg.ModelPrice{}
	size := func() []byte {
		d, err := pub.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	for i := 0; len(size()) <= localcfg.SharedMaxBytes-150; i++ {
		pub.ModelPrices["claude-sonnet-"+strconv.Itoa(i)+"-0"] = localcfg.ModelPrice{InputPerM: &one, OutputPerM: &two}
	}
	// One more entry, its key lengthened until the file is within 40 bytes
	// of the cap: every merge with this machine's repos goes over.
	var data []byte
	for l := 1; l < 100; l++ {
		key := "claude-sonnet-" + strings.Repeat("a", l)
		pub.ModelPrices[key] = localcfg.ModelPrice{InputPerM: &one, OutputPerM: &two}
		if data = size(); len(data) > localcfg.SharedMaxBytes-40 {
			break
		}
		delete(pub.ModelPrices, key)
	}
	if len(data) <= localcfg.SharedMaxBytes-40 || len(data) > localcfg.SharedMaxBytes {
		t.Fatalf("fixture is %d bytes", len(data))
	}
	writePublished(t, dir, string(data))
	lc := publishable(t)
	var warned []string
	written, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) })
	if err != nil || !written {
		t.Fatalf("written=%v err=%v", written, err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "too large to merge") || !strings.Contains(warned[0], "replacing it with this machine's view") {
		t.Errorf("warnings = %q", warned)
	}
	got, _ := os.ReadFile(sharedObjectPath(dir))
	if len(got) > localcfg.SharedMaxBytes || strings.Contains(string(got), "claude-sonnet-0-0") || !strings.Contains(string(got), "acme/app") {
		t.Errorf("published %d bytes:\n%.400s", len(got), got)
	}
}

// TestFakeEndpointsNeverOpenARealBucket: with a gs:// bucket and fake
// endpoints in the local config (no_auth or a storage endpoint) nothing
// opens the bucket: the publish is skipped without error, and so is the
// build-record read. A file:// bucket is not skipped.
func TestFakeEndpointsNeverOpenARealBucket(t *testing.T) {
	newCloudFixture(t)
	old, oldRec := skipGSOnFakeEndpoints, openRecordBucket
	skipGSOnFakeEndpoints = true
	t.Cleanup(func() { skipGSOnFakeEndpoints, openRecordBucket = old, oldRec })
	opened := 0
	sharedBucketOpener = func(context.Context, string) (*blobx.Bucket, error) { opened++; return nil, errors.New("opened") }
	openRecordBucket = func(context.Context, string) (*blobx.Bucket, error) { opened++; return nil, errors.New("opened") }
	for name, edit := range map[string]func(*localcfg.Config){
		"no_auth": func(lc *localcfg.Config) { lc.Endpoints.NoAuth = true },
		"storage": func(lc *localcfg.Config) { lc.Endpoints.Storage = "http://127.0.0.1:1/" },
	} {
		lc := publishable(t)
		lc.Bucket = ""
		edit(lc)
		written, err := publishSharedWarn(context.Background(), lc, func(string) {})
		if written || err != nil || opened != 0 {
			t.Errorf("%s: written %v, err %v, opens %d; want a silent skip", name, written, err, opened)
		}
		if !fakeEndpointsOnGS(lc, lc.BucketURL()) {
			t.Errorf("%s: gs:// with fake endpoints not recognised", name)
		}
		if fakeEndpointsOnGS(lc, "file:///x") || fakeEndpointsOnGS(lc, "mem://") {
			t.Errorf("%s: a file:// or mem:// bucket was skipped", name)
		}
	}
}

// A coding agent's session publishes nothing: --publish-config refuses, and
// the publish at the end of the other init paths is skipped with a note. Both
// use the standard refusal text, which names the marker and, for the IDE
// extension's, says how to unset it.
func TestInitPublishFromACodingAgentSession(t *testing.T) {
	for _, marker := range []string{"CLAUDECODE", initflow.IDEMarker} {
		t.Run(marker, func(t *testing.T) {
			r := newInitRig(t)
			r.stateBucket()
			dir := sharedRuns(t)
			t.Setenv(marker, "1")
			refusal := initflow.AgentRefusal(marker)
			_, _, err := executeStdin(t, "", "init", "--publish-config")
			if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), refusal) {
				t.Fatalf("--publish-config: exit %d, err %v; want %q", ExitCode(err), err, refusal)
			}
			if hint := strings.Contains(err.Error(), "you can unset "+initflow.IDEMarker); hint != (marker == initflow.IDEMarker) {
				t.Errorf("IDE hint present = %v for %s: %v", hint, marker, err)
			}
			var buf strings.Builder
			(&initRun{w: &buf}).publishSharedConfig(t.Context(), publishable(t))
			want := "note: the shared config was not published: " + refusal + "; run fugaro init --publish-config in your own terminal\n"
			if buf.String() != want {
				t.Errorf("note:\n%q\nwant:\n%q", buf.String(), want)
			}
			if _, err := os.Stat(sharedObjectPath(dir)); !os.IsNotExist(err) {
				t.Errorf("published from an agent's session: %v", err)
			}
		})
	}
}
