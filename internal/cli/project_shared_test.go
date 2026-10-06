package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// auroraShared is validShared for project aurora in GCP project proj-1234.
func auroraShared() string {
	return strings.NewReplacer("belong", "aurora", "fugaro-aurora", "proj-1234", "fugaro-belong", "proj-1234").Replace(validShared())
}

// sharedEnd2End is a cloud fixture with no local config at all: the project's
// runs bucket (the fixture's, behind sharedBucketOpener) holds the marker and
// a published config. The shared config can't carry endpoints, so the
// fetched one is pointed at the fixture's fakes through the sharedFetch seam
// after it was fetched and validated for real.
func sharedEnd2End(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	if err := os.Remove(os.Getenv("FUGARO_CONFIG")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", "")
	runs := strings.TrimPrefix(f.bucket, "file://")
	if err := os.WriteFile(filepath.Join(runs, filepath.FromSlash(infra.SharedConfigObject)), []byte(auroraShared()), 0o644); err != nil {
		t.Fatal(err)
	}
	oldOpen, oldFetch := sharedBucketOpener, sharedFetch
	sharedBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) { return blobx.Open(ctx, f.bucket) }
	sharedFetch = func(ctx context.Context, getenv func(string) string, now time.Time, name, gcp string) (*localcfg.Config, string, error) {
		c, note, err := fetchSharedConfig(ctx, getenv, now, name, gcp)
		if err == nil {
			c.Bucket = f.bucket
			c.Endpoints = localcfg.Endpoints{Run: f.run.URL + "/", Logging: f.logging.URL + "/", NoAuth: true}
		}
		return c, note, err
	}
	t.Cleanup(func() { sharedBucketOpener, sharedFetch = oldOpen, oldFetch })
	return f
}

func TestLsWithNoLocalConfigUsesTheSharedOne(t *testing.T) {
	f := sharedEnd2End(t)
	t.Chdir(gitCheckout(t, filepath.Join(f.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))

	out, errOut, err := execute(t, "ls")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "0 runs") {
		t.Errorf("out = %q", out)
	}
	if !strings.Contains(errOut, "project: aurora (GCP proj-1234)") || !strings.Contains(errOut, "no local config for aurora: using the shared config published to gs://fugaro-runs-proj-1234") {
		t.Errorf("the header must say where the config came from:\n%s", errOut)
	}
	if projects, _ := localcfg.Projects(os.Getenv); len(projects) != 0 {
		t.Errorf("a local config was written: %v", projects)
	}
	if !cacheExistsFor(t, "aurora") {
		t.Errorf("the fetched config was not cached")
	}
}

func cacheExistsFor(t *testing.T, name string) bool {
	t.Helper()
	_, ok := localcfg.LoadSharedCache(os.Getenv, name)
	return ok
}

func TestLsWithNoLocalConfigAndNoGCPProjectAsksForTheLine(t *testing.T) {
	f := sharedEnd2End(t)
	t.Chdir(gitCheckout(t, filepath.Join(f.dir, "app"), "version: 1\nproject: aurora\n"))

	_, _, err := execute(t, "ls")
	if ExitCode(err) != ExitUserError || err == nil {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	for _, want := range []string{"add `gcp_project: <id>` next to `project:` in fugaro.yaml", "run fugaro init in the checkout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	// No value is ever suggested for the line.
	if strings.Contains(err.Error(), "proj-1234") {
		t.Errorf("the refusal hints at a GCP project: %v", err)
	}
}

func TestSharedSelectionFlagContradictingTheCheckoutIsRefused(t *testing.T) {
	f := sharedEnd2End(t)
	t.Chdir(gitCheckout(t, filepath.Join(f.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))

	_, _, err := execute(t, "ls", "--gcp-project", "other-proj")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "contradicts this checkout's gcp_project proj-1234") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

func TestAnnounceRefusesAGCPProjectOverrideOfTheSharedConfig(t *testing.T) {
	lc, err := ParseShared([]byte(auroraShared()), SharedAnchor{Name: "aurora", GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234"})
	if err != nil {
		t.Fatal(err)
	}
	var errOut strings.Builder
	o := cloudOptions{gcpProject: "other-proj", stderr: func() io.Writer { return &errOut }}
	err = announce(o, localcfg.Selection{Name: "aurora", From: "shared config"}, lc)
	if err == nil || !strings.Contains(err.Error(), "can't be pointed at another GCP project") {
		t.Fatalf("err = %v", err)
	}
	if lc.GCPProject != "proj-1234" {
		t.Fatalf("the override was applied: %s", lc.GCPProject)
	}
}

// Review focus: a command that would write the local config, running against a
// shared selection, refuses and creates nothing. The only writers are
// fugaro init's; loadRepoConfig (init --repo) is the one that selects an
// existing config and writes it back.
func TestWritersRefuseAgainstASharedSelection(t *testing.T) {
	f := sharedEnd2End(t)
	dir := gitCheckout(t, filepath.Join(f.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n")
	t.Chdir(dir)

	o := &initOptions{}
	o.cloud.sharedOK = true // not reachable from a flag: selectProject alone sets it
	_, path, _, err := loadRepoConfig(context.Background(), o, dir)
	if err == nil || !strings.Contains(err.Error(), "the shared one published by an operator, so it can't be changed here; run fugaro init to create your own local config") {
		t.Fatalf("path %q, err %v", path, err)
	}
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d", ExitCode(err))
	}
	if projects, _ := localcfg.Projects(os.Getenv); len(projects) != 0 {
		t.Fatalf("a local config appeared: %v", projects)
	}

	// The writer itself never writes to an empty path.
	r := &initRun{o: o, w: &strings.Builder{}}
	lc, _ := ParseShared([]byte(auroraShared()), SharedAnchor{Name: "aurora", GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234"})
	if err := r.writeLocalConfig(lc, "", nil, true); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("writeLocalConfig to an empty path: %v", err)
	}
}

func TestRefuseSharedWrite(t *testing.T) {
	if err := refuseSharedWrite(localcfg.Selection{Name: "aurora", From: "shared config"}); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("shared: %v", err)
	}
	for _, from := range []string{"checkout", "--config", "only project config"} {
		if err := refuseSharedWrite(localcfg.Selection{Path: "/x", Name: "aurora", From: from}); err != nil {
			t.Errorf("%s: %v", from, err)
		}
	}
}

// fugaro init keeps working on a machine whose only config is the shared
// one: it selects creating (no fetch, no shared hook) and gets its own path.
func TestInitSelectionNeverUsesTheSharedConfig(t *testing.T) {
	f := sharedEnd2End(t)
	t.Chdir(gitCheckout(t, filepath.Join(f.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))
	calls := 0
	old := sharedFetch
	sharedFetch = func(ctx context.Context, g func(string) string, n time.Time, name, gcp string) (*localcfg.Config, string, error) {
		calls++
		return old(ctx, g, n, name, gcp)
	}
	t.Cleanup(func() { sharedFetch = old })

	co, err := checkoutProject(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	sel, lc, err := selectNamed(cloudOptions{sharedOK: true}, co, true, "")
	if err != nil || lc != nil || sel.From == "shared config" || sel.Path == "" || calls != 0 {
		t.Fatalf("%+v %v %v calls=%d", sel, lc, err, calls)
	}
	o := &initOptions{}
	_, path, oldData, err := loadInitConfig(context.Background(), o)
	// No config to create from: init says what it still needs, never a path
	// into the shared config.
	if err == nil || path != "" || oldData != nil || !strings.Contains(err.Error(), "there is no project config for aurora yet: pass --gcp-project and --region to create one") {
		t.Fatalf("loadInitConfig: path %q, err %v", path, err)
	}
	if calls != 0 {
		t.Fatalf("init fetched the shared config")
	}
}

// A local config naming the same project always wins over the published one,
// silently, and the hook is not called by selection.
func TestLocalConfigWinsOverTheSharedOne(t *testing.T) {
	f := sharedEnd2End(t)
	writeProject(t, "aurora", "proj-1234")
	t.Chdir(gitCheckout(t, filepath.Join(f.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))
	calls := 0
	old := sharedFetch
	sharedFetch = func(ctx context.Context, g func(string) string, n time.Time, name, gcp string) (*localcfg.Config, string, error) {
		calls++
		return old(ctx, g, n, name, gcp)
	}
	t.Cleanup(func() { sharedFetch = old })

	var errOut strings.Builder
	sel, lc, err := selectProject(context.Background(), cloudOptions{stderr: func() io.Writer { return &errOut }})
	if err != nil || lc == nil || sel.From != "checkout" || sel.Path == "" || calls != 0 {
		t.Fatalf("%+v %v %v calls=%d", sel, lc, err, calls)
	}
	if strings.Contains(errOut.String(), "shared") {
		t.Fatalf("a local config won noisily: %s", errOut.String())
	}
}

// --config and a local config always win: the checkout's gcp_project never
// contradicts the flag then (Override compares the flag with the selected
// config only).
func TestConfigFileIgnoresTheCheckoutsGCPProject(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(gitCheckout(t, filepath.Join(f.dir, "app"), "version: 1\nproject: aurora\ngcp_project: other-proj\n"))
	cfg := os.Getenv("FUGARO_CONFIG")
	_, errOut, err := execute(t, "ls", "--config", cfg, "--gcp-project", "proj-1234")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, errOut)
	}
	if strings.Contains(errOut, "shared config") {
		t.Errorf("the shared config was used:\n%s", errOut)
	}
	// Override still compares the flag with the selected config.
	_, _, err = execute(t, "ls", "--config", cfg, "--gcp-project", "other-proj")
	if err == nil || !strings.Contains(err.Error(), "can't be pointed at another GCP project") {
		t.Fatalf("err = %v", err)
	}
}

// With no local config, --gcp-project alone (no line in the checkout) is
// the GCP project to look the shared config up in.
func TestSharedConfigFromTheFlagAlone(t *testing.T) {
	f := sharedEnd2End(t)
	t.Chdir(gitCheckout(t, filepath.Join(f.dir, "app"), "version: 1\nproject: aurora\n"))
	_, errOut, err := execute(t, "ls", "--gcp-project", "proj-1234")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "using the shared config") {
		t.Errorf("errOut = %s", errOut)
	}
}

// Outside a checkout --project names the project; the bucket's marker still
// has to name it: a marker of another project is refused.
func TestSharedSelectionPassesTheRequestedNameToTheMarkerCheck(t *testing.T) {
	f := sharedEnd2End(t)
	runs := strings.TrimPrefix(f.bucket, "file://")
	if err := os.WriteFile(filepath.Join(runs, filepath.FromSlash(infra.ProjectMarkerObject)), []byte(`{"version":1,"name":"borealis","gcp_project":"proj-1234"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	_, _, err := execute(t, "ls", "--project", "aurora", "--gcp-project", "proj-1234")
	if err == nil || !strings.Contains(err.Error(), "is project borealis, not aurora") {
		t.Fatalf("err = %v", err)
	}
	if cacheExistsFor(t, "aurora") {
		t.Errorf("a refused marker left a cache")
	}
}

// The offline commands never reach the bucket, with no local config and a
// checkout that names project and gcp_project: they behave as before.
func TestOfflineCommandsNeverFetchTheSharedConfig(t *testing.T) {
	dir := t.TempDir()
	isolateProjects(t, dir)
	oldOpen, oldFetch := sharedBucketOpener, sharedFetch
	sharedBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		t.Fatal("an offline command opened the runs bucket")
		return nil, nil
	}
	sharedFetch = func(context.Context, func(string) string, time.Time, string, string) (*localcfg.Config, string, error) {
		t.Fatal("an offline command fetched the shared config")
		return nil, "", nil
	}
	t.Cleanup(func() { sharedBucketOpener, sharedFetch = oldOpen, oldFetch })
	t.Chdir(gitCheckout(t, filepath.Join(dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))

	if _, errOut, err := execute(t, "validate"); err != nil && ExitCode(err) != ExitUserError {
		t.Fatalf("validate: %v\n%s", err, errOut)
	}
	if out, _, err := execute(t, "config", "example"); err != nil || !strings.Contains(out, "project:") {
		t.Fatalf("config example: %v\n%s", err, out)
	}
	// budget prices selects through pricesConfig: nothing selectable is a note.
	out, errOut, err := execute(t, "budget", "prices")
	if err != nil || !strings.Contains(errOut, "no project config selected, so the built-in prices only") || !strings.Contains(errOut, "there is no project config for aurora") || out == "" {
		t.Fatalf("budget prices: %v\nstdout %q\nstderr %s", err, out, errOut)
	}
	if strings.Contains(errOut, "gcp_project: <id>") {
		t.Errorf("the offline refusal changed: %s", errOut)
	}
}
