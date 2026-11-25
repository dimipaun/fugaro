package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/routing"
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
	f.noBudget(t)
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
	for _, s := range []string{"user:", "endpoints:", "bucket_url", "providers:", "routing:"} {
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
	fakeTerminal(t)
	dir := sharedRuns(t)
	if _, _, err := executeStdin(t, "", "setup", "--config-only"); err != nil {
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
	out, _, err := executeStdin(t, "", "setup", "--publish-config")
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

// A launcher's --publish-config is refused with the admin text and exit
// 1, since 0.7.0 only admins may write fugaro/ (bucket-iam.md H8).
func TestInitPublishConfigAsLauncherIsRefused(t *testing.T) {
	r := newInitRig(t)
	old := sharedBucketOpener
	sharedBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return r.gcs.Bucket(t, initRunsBucket), nil
	}
	t.Cleanup(func() { sharedBucketOpener = old })
	r.gcs.DenyWrites(initRunsBucket, "fugaro/")
	_, _, err := executeStdin(t, "", "setup", "--publish-config")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "admin role") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// A plain converge (not --publish-config) only warns when the
// shared config publish is forbidden: the installation itself still
// succeeds, exit 0 (initRun.publishSharedConfig, init.go).
func TestInitWarnsWhenSharedConfigPublishIsForbidden(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	fakeTerminal(t)
	old := sharedBucketOpener
	sharedBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return r.gcs.Bucket(t, initRunsBucket), nil
	}
	t.Cleanup(func() { sharedBucketOpener = old })
	r.gcs.DenyWrites(initRunsBucket, "fugaro/")
	out, _, err := executeStdin(t, "", "setup")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if !strings.Contains(out, "could not publish the shared config") || !strings.Contains(out, "admin role") {
		t.Fatalf("no warning naming the admin role:\n%s", out)
	}
}

func TestInitPublishConfigNeedsALocalConfig(t *testing.T) {
	r := newInitRig(t)
	sharedRuns(t)
	if err := os.Remove(r.cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", "")
	_, _, err := executeStdin(t, "", "setup", "--publish-config", "--name", "aurora")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

func TestInitPublishConfigExcludesTheOtherModes(t *testing.T) {
	newInitRig(t)
	for _, other := range []string{"--config-only", "--plan-only", "--print-vars", "--forget"} {
		// --forget is the update door's flag (W1.2): setup refuses it before
		// the run's own mode checks, so the row that pairs it with a mode
		// runs the door that takes it.
		door := "setup"
		if other == "--forget" {
			door = "update"
		}
		_, _, err := executeStdin(t, "", door, "--publish-config", other)
		if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "--publish-config") || !strings.Contains(err.Error(), "exclude one another") {
			t.Errorf("%s: exit %d, err %v", other, ExitCode(err), err)
		}
	}
}

func TestInitPublishFailureWarnsAndSucceeds(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	fakeTerminal(t)
	dir := sharedRuns(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if f, err := os.Create(filepath.Join(dir, "probe")); err == nil {
		f.Close()
		t.Skip("the directory is writable despite its mode (running as root)")
	}
	out, _, err := executeStdin(t, "", "setup", "--config-only")
	if err != nil {
		t.Fatalf("a publish failure must not fail init: %v", err)
	}
	if !strings.Contains(out, "Warning: could not publish the shared config: ") || !strings.Contains(out, "teammates will need fugaro setup until it is published") {
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

// publishedOptIn is the opt-in fact of the object the runs bucket holds: what
// every machine without a local config of its own reads, and therefore the value
// a publish has to have left there (R15).
func publishedOptIn(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(sharedObjectPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse(data)
	if err != nil {
		t.Fatalf("%v:\n%s", err, data)
	}
	if lc.Batch == nil {
		t.Fatalf("the published object carries no batch block:\n%s", data)
	}
	return lc.Batch.OptIn
}

// localOptIn is the opt-in fact of this machine's own config FILE: the copy of
// the fact every publish rewrites from the published one (R15).
func localOptIn(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse(data)
	if err != nil {
		t.Fatalf("%v:\n%s", err, data)
	}
	if lc.Batch == nil {
		return ""
	}
	return lc.Batch.OptIn
}

// setLocalBatch rewrites a machine's own config so its batch block is b, a nil b
// leaving the file with none. The file's copy of the fact is what a publish
// rewrites, so a test has to be able to move it the way an operator's own
// command, an older release or a person's editor would have left it.
func setLocalBatch(t *testing.T, path string, b *localcfg.Batch) {
	t.Helper()
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse(old)
	if err != nil {
		t.Fatalf("%v:\n%s", err, old)
	}
	lc.Batch = b
	data, err := lc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestPublishingRefreshesTheLocalCopyOfTheOptIn: the opt-in fact a publish
// leaves in the runs bucket is the installation's, and every publish rewrites
// this machine's own copy of it from the published one (R15, BR19). The
// published object is the local config of every teammate who has none — the
// reference `fugaro project show` reads and the copy their launches read the
// fact off — so a publishing machine's own block is never the authority on it:
// a stale local `in` neither publishes over a withdrawal nor survives one.
// The two writers R15 names are the only publishes that CHANGE the value, through
// publishSharedOptIn: an operator's selection passes `in` (R2, BR18) and the
// opt-out passes `withdrawn` (R16, BR20), the value passed explicitly because
// no other publish may write one.
//
// The other half of the rule is the machine's own file, which follows the value
// the installation now holds: that is how the window O19 describes closes on a
// machine whose file still carries the value from before — a withdrawal it has
// not followed, or an opt-in since withdrawn. The publish writes through the
// caller's own config write (init.go's refreshLocalOptIn, which the door below
// drives), so a machine with no batch block of its own is left alone: a block
// there is an operator's selection (BR18), and a publish inventing one would be
// the installation's fact reaching a file nobody wrote it in.
func TestPublishingRefreshesTheLocalCopyOfTheOptIn(t *testing.T) {
	r := newInitRig(t)
	dir := sharedRuns(t)
	// The rig's own file reaches the shared object through a bucket_url of the
	// test's own, so the publishes this test makes from the seam and the ones the
	// door makes are the same object.
	appendConfig(t, "bucket_url: file://"+dir+"\n")
	ctx := context.Background()
	block := func(optIn string) *localcfg.Batch {
		return &localcfg.Batch{Image: "us-east5-docker.pkg.dev/proj-1234/fugaro/acme-web:1", OptIn: optIn}
	}
	// local is the machine's own config as its file states it, which is what the
	// two seam publishes below publish; the door re-reads the file for itself.
	local := func(t *testing.T) *localcfg.Config {
		t.Helper()
		lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
		if err != nil {
			t.Fatal(err)
		}
		return lc
	}
	publish := func(t *testing.T) {
		t.Helper()
		if _, _, err := executeStdin(t, "", "setup", "--publish-config"); err != nil {
			t.Fatal(err)
		}
	}
	// The machine's own block states the fact and the installation's copy is not
	// there yet: the block published is this machine's own whole, and the file it
	// came from is already the value the bucket holds.
	setLocalBatch(t, r.cfg, block(localcfg.BatchOptInOn))
	publish(t)
	if got := publishedOptIn(t, dir); got != localcfg.BatchOptInOn {
		t.Fatalf("the publish left the installation with %q, want %q", got, localcfg.BatchOptInOn)
	}
	if got := localOptIn(t, r.cfg); got != localcfg.BatchOptInOn {
		t.Fatalf("the publish rewrote a file that already agreed: %q", got)
	}
	// The operator withdraws the fact while this machine's own file still says
	// `in`: the writer's value is what the installation gets, and a publish from
	// the seam has no path to a file, which is the stale copy O19 is about.
	if written, _, err := publishSharedOptIn(ctx, local(t), localcfg.BatchOptInWithdrawn, func(string) {}); err != nil || !written {
		t.Fatalf("written=%v err=%v", written, err)
	}
	if got := publishedOptIn(t, dir); got != localcfg.BatchOptInWithdrawn {
		t.Fatalf("the opt-out writer published %q", got)
	}
	if got := localOptIn(t, r.cfg); got != localcfg.BatchOptInOn {
		t.Fatalf("a publish without a file wrote one: %q", got)
	}
	// Every publish after that keeps what the published copy holds — the stale
	// local `in` never publishes over the withdrawal — and the machine's own file
	// follows it, which is the window O19 names closing.
	publish(t)
	if got := publishedOptIn(t, dir); got != localcfg.BatchOptInWithdrawn {
		t.Errorf("a stale local `in` published over the withdrawal: %q", got)
	}
	if got := localOptIn(t, r.cfg); got != localcfg.BatchOptInWithdrawn {
		t.Errorf("this machine's own copy of the fact did not follow the published one: %q", got)
	}
	// And the other way round (R2): the writer opts the installation in again
	// while the file still says `withdrawn`, and the publish that follows carries
	// the file to the published value.
	if written, _, err := publishSharedOptIn(ctx, local(t), localcfg.BatchOptInOn, func(string) {}); err != nil || !written {
		t.Fatalf("written=%v err=%v", written, err)
	}
	if got := localOptIn(t, r.cfg); got != localcfg.BatchOptInWithdrawn {
		t.Fatalf("the opt-in writer changed this machine's file: %q", got)
	}
	publish(t)
	if got := publishedOptIn(t, dir); got != localcfg.BatchOptInOn {
		t.Errorf("a stale local withdrawal published over the opt-in: %q", got)
	}
	if got := localOptIn(t, r.cfg); got != localcfg.BatchOptInOn {
		t.Errorf("this machine's own copy did not follow the opt-in: %q", got)
	}
	// Where the published copy is refused, the one case R15 leaves open (O18),
	// the replacement is this machine's view: the fact is the one its own block
	// carries, and the warning beside it is the one that already says so.
	writePublished(t, dir, strings.Replace(publishedWithOtherRepo, "gcp_project: proj-1234", "gcp_project: other-proj", 1))
	setLocalBatch(t, r.cfg, block(localcfg.BatchOptInWithdrawn))
	var warned []string
	if written, _, err := publishSharedWarn(ctx, local(t), func(m string) { warned = append(warned, m) }); err != nil || !written {
		t.Fatalf("written=%v err=%v", written, err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "the published shared config was refused (") || !strings.Contains(warned[0], "replacing it") {
		t.Errorf("warnings = %q", warned)
	}
	if got := publishedOptIn(t, dir); got != localcfg.BatchOptInWithdrawn {
		t.Errorf("the replacement published %q, not this machine's own value", got)
	}
	// A machine whose file carries no block at all states no fact, and a publish
	// writes none into it: the file is left exactly as it is.
	setLocalBatch(t, r.cfg, nil)
	before, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	publish(t)
	after, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a publish wrote an opt-in into a file with no batch block:\n%s", after)
	}
	if got := publishedOptIn(t, dir); got != localcfg.BatchOptInWithdrawn {
		t.Errorf("the publish from a machine with no block changed the published fact: %q", got)
	}
	// An absent fact is a state of its own and not a withdrawal (C1, D3111), and
	// the published copy's absence is kept like any other value: a machine whose
	// file states an opt-in the installation does not carry has it rewritten to
	// the absence, rather than keeping a value the installation never wrote.
	writePublished(t, dir, strings.Replace(publishedWithOtherRepo, "gcp_project: proj-1234", "gcp_project: other-proj", 1))
	setLocalBatch(t, r.cfg, block(""))
	publish(t)
	if got := publishedOptIn(t, dir); got != "" {
		t.Fatalf("the replacement published %q, want no fact at all", got)
	}
	setLocalBatch(t, r.cfg, block(localcfg.BatchOptInOn))
	publish(t)
	if got := publishedOptIn(t, dir); got != "" {
		t.Errorf("a publish invented the opt-in its own block states: %q", got)
	}
	if got := localOptIn(t, r.cfg); got != "" {
		t.Errorf("this machine's own copy kept a fact the published one does not carry: %q", got)
	}
}

func TestPublishSharedKeepsTheReposOfOtherMachines(t *testing.T) {
	f := newCloudFixture(t)
	f.noBudget(t)
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
	f.noBudget(t)
	dir := filepath.Join(f.dir, "runs")
	writePublished(t, dir, strings.Replace(publishedWithOtherRepo, "gcp_project: proj-1234", "gcp_project: other-proj", 1))
	lc := publishable(t)
	lc.Repos = nil
	var warned []string
	if _, _, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) }); err != nil {
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
	f.noBudget(t)
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
	_, _, err := executeStdin(t, "", "setup", "--repo", "--publish-config")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "--publish-config") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// TestPublishSharedNeverPublishesProviders: providers are local-only; the
// local ones are not published, and a published object carrying some (an
// older publisher's) is refused and replaced, not merged.
func TestPublishSharedNeverPublishesProviders(t *testing.T) {
	f := newCloudFixture(t)
	f.noBudget(t)
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
	if _, _, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) }); err != nil {
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

// TestPublishSharedNeverPublishesRouting: the routing switch is the same
// decision as providers, from the switch's side — which Claude IDs may leave
// for a third party — and it resolves through providers, which are local-only.
// The local routing block is not published, and a published object carrying one
// (an older publisher's, or a bucket writer's) is refused and replaced, never
// merged: anyone who can write the runs bucket must not be able to route
// another operator's Claude calls by adding one.
func TestPublishSharedNeverPublishesRouting(t *testing.T) {
	f := newCloudFixture(t)
	f.noBudget(t)
	dir := filepath.Join(f.dir, "runs")
	block := "routing:\n  claude:\n    via: direct\n    models: [claude-sonnet-5-5]\n"
	writePublished(t, dir, publishedWithOtherRepo+block)
	if _, err := localcfg.Parse([]byte(publishedWithOtherRepo + block)); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	f.appendConfig(t, block)
	lc := publishable(t)
	if lc.Routing == nil || lc.Routing.Claude == nil {
		t.Fatalf("the local fixture has no routing block: %+v", lc.Routing)
	}
	var warned []string
	if _, _, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) }); err != nil {
		t.Fatal(err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "refused") || !strings.Contains(warned[0], "routing") {
		t.Errorf("warnings = %q", warned)
	}
	got, err := os.ReadFile(sharedObjectPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "routing") {
		t.Errorf("routing was published:\n%s", got)
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
			f.noBudget(t)
			dir := filepath.Join(f.dir, "runs")
			if _, err := localcfg.Parse([]byte(content)); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			writePublished(t, dir, content)
			lc := publishable(t)
			var warned []string
			if _, _, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) }); err != nil {
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
			lc.BaseImages = map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:1"}
		}, "base_images.base"},
		"separate Firebase project": {func(lc *localcfg.Config) {
			lc.Budget = &localcfg.Budget{Mode: "observe", RTDBURL: "https://my-fp1-default-rtdb.firebaseio.com", FirebaseProject: "my-fp1", TokenSigner: "fugaro-token-signer@my-fp1.iam.gserviceaccount.com"}
		}, "separate Firebase project"},
		"registry host of another region": {func(lc *localcfg.Config) { lc.RegistryHost = "us-west1-docker.pkg.dev/proj-1234" }, "registry_host"},
		// The batch block is published, so the self-check is what keeps a
		// machine whose own block points elsewhere from writing it for
		// everyone: the publisher runs ParseShared on the exact bytes, and
		// this is that refusal arriving as a warning instead of a write.
		"batch image of another project": {func(lc *localcfg.Config) {
			lc.Batch = &localcfg.Batch{Image: "us-east5-docker.pkg.dev/evil-proj/fugaro/fugaro:1"}
		}, "batch.image"},
		"non-default runs bucket": {func(lc *localcfg.Config) { lc.RunsBucket = "my-own-runs" }, "default runs bucket name"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newCloudFixture(t)
			f.noBudget(t)
			dir := filepath.Join(f.dir, "runs")
			writePublished(t, dir, publishedWithOtherRepo)
			lc := publishable(t)
			tc.mutate(lc)
			var warned []string
			if _, _, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) }); err != nil {
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
	out, _, err := executeStdin(t, "", "setup", "--publish-config")
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

// oversizePublished is a valid published object just under the cap (a writer
// bloated model_prices) whose every merge with this machine's own config goes
// over it: the state both oversize tests below are written from.
func oversizePublished(t *testing.T) []byte {
	t.Helper()
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
	return data
}

// TestPublishSharedOversizeMergeFallsBackToLocal: a valid published object
// just under the cap (a writer bloated model_prices) would push every merge
// over it; the publisher then replaces it with this machine's view instead
// of failing for good.
func TestPublishSharedOversizeMergeFallsBackToLocal(t *testing.T) {
	f := newCloudFixture(t)
	f.noBudget(t)
	dir := filepath.Join(f.dir, "runs")
	writePublished(t, dir, string(oversizePublished(t)))
	lc := publishable(t)
	var warned []string
	written, _, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) })
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

// TestPublishSharedOversizeReplacementKeepsTheWritersValue: the oversize branch
// is a replacement like the refused one (O18) — what it writes is this
// machine's own block and not a merge — and the opt-in fact in it follows the
// merge's own rule, which is the one place the two differ: the value a writer
// passed, and this machine's own block's for an ordinary publish. An ordinary
// publish takes the fact from the published copy, so a copy being replaced
// leaves it nothing to take it from and its own block's value is what goes out;
// a writer's value is what the two writers R15 names pass and no other publish
// may write.
func TestPublishSharedOversizeReplacementKeepsTheWritersValue(t *testing.T) {
	f := newCloudFixture(t)
	f.noBudget(t)
	dir := filepath.Join(f.dir, "runs")
	oversize := oversizePublished(t)
	ctx := context.Background()
	// This machine's own block states the other value, so a fact that reads
	// `in` in the object can only be the writer's.
	setLocalBatch(t, os.Getenv("FUGARO_CONFIG"), &localcfg.Batch{
		Image: "us-east5-docker.pkg.dev/proj-1234/fugaro/acme-web:1",
		OptIn: localcfg.BatchOptInWithdrawn,
	})
	for _, tc := range []struct {
		name string
		pub  func(*localcfg.Config, func(string)) (bool, string, error)
		want string
	}{
		{"the writer's value", func(lc *localcfg.Config, warn func(string)) (bool, string, error) {
			return publishSharedOptIn(ctx, lc, localcfg.BatchOptInOn, warn)
		}, localcfg.BatchOptInOn},
		{"this machine's own value", func(lc *localcfg.Config, warn func(string)) (bool, string, error) {
			return publishSharedWarn(ctx, lc, warn)
		}, localcfg.BatchOptInWithdrawn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writePublished(t, dir, string(oversize))
			var warned []string
			written, fact, err := tc.pub(publishable(t), func(m string) { warned = append(warned, m) })
			if err != nil || !written {
				t.Fatalf("written=%v err=%v", written, err)
			}
			if len(warned) != 1 || !strings.Contains(warned[0], "too large to merge") || !strings.Contains(warned[0], "replacing it with this machine's view") {
				t.Errorf("warnings = %q", warned)
			}
			if fact != tc.want {
				t.Errorf("publishedOptIn = %q, want %q: the fact is what the write really left in the bucket", fact, tc.want)
			}
			if got := publishedOptIn(t, dir); got != tc.want {
				t.Errorf("the replacement left the installation with %q, want %q", got, tc.want)
			}
			got, err := os.ReadFile(sharedObjectPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) > localcfg.SharedMaxBytes || strings.Contains(string(got), "claude-sonnet-0-0") || !strings.Contains(string(got), "acme/app") {
				t.Errorf("published %d bytes:\n%.400s", len(got), got)
			}
		})
	}
}

// TestFakeEndpointsNeverOpenARealBucket: with a gs:// bucket and fake
// endpoints in the local config (no_auth or a storage endpoint) nothing
// opens the bucket: the publish is skipped without error, and so is the
// build-record read. A file:// bucket is not skipped.
func TestFakeEndpointsNeverOpenARealBucket(t *testing.T) {
	newCloudFixture(t)
	old, oldRec, oldOpen := skipGSOnFakeEndpoints, openRecordBucket, sharedBucketOpener
	skipGSOnFakeEndpoints = true
	t.Cleanup(func() { skipGSOnFakeEndpoints, openRecordBucket, sharedBucketOpener = old, oldRec, oldOpen })
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
		written, _, err := publishSharedWarn(context.Background(), lc, func(string) {})
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
			_, _, err := executeStdin(t, "", "setup", "--publish-config")
			if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), refusal) {
				t.Fatalf("--publish-config: exit %d, err %v; want %q", ExitCode(err), err, refusal)
			}
			if hint := strings.Contains(err.Error(), "you can unset "+initflow.IDEMarker); hint != (marker == initflow.IDEMarker) {
				t.Errorf("IDE hint present = %v for %s: %v", hint, marker, err)
			}
			// The note is printed by a run of one of the two doors, and it
			// names the door the reader is at: --publish-config is neither
			// door's alone (it is in neither foreign list), so both doors
			// reach this line, and a hardcoded one would send half of them to
			// the command they are not running (task W2.1).
			for _, door := range []string{"setup", "update"} {
				var buf strings.Builder
				(&initRun{w: &buf, o: &initOptions{door: door}}).publishSharedConfig(t.Context(), publishable(t))
				want := "note: the shared config was not published: " + refusal + "; run fugaro " + door + " --publish-config in your own terminal\n"
				if buf.String() != want {
					t.Errorf("note (%s):\n%q\nwant:\n%q", door, buf.String(), want)
				}
			}
			if _, err := os.Stat(sharedObjectPath(dir)); !os.IsNotExist(err) {
				t.Errorf("published from an agent's session: %v", err)
			}
		})
	}
}

// TestSharedConfigRefusesAliases: an alias is a name the account-choice keys
// may hold in place of an account's own name, so it is local-only for their
// reason and one indirection in (docs/account-aliases, D2090) — a published
// alias would let whoever can write the runs bucket's shared file name an
// account that file never admitted. The publish writes the resolved account
// into the policy node instead, and localcfg.Config.Shared nils the block, so
// the reader refuses the key by name before the file is parsed and again on the
// decoded value, the way review_floor and the catalog are refused.
func TestSharedConfigRefusesAliases(t *testing.T) {
	if !slices.Contains(sharedForbidden, "claude_account_aliases") {
		t.Fatal("sharedForbidden does not name claude_account_aliases, so a published alias would be read")
	}
	block := "claude_account_aliases:\n    reviewer: team-a\n"
	_, err := ParseShared([]byte(validShared()+block), sharedAnchor)
	if err == nil {
		t.Fatal("a published shared config carrying claude_account_aliases: was accepted")
	}
	for _, want := range []string{"claude_account_aliases", "never published", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// The decoded half, which no document reaches (the key is refused before
	// Parse): a config built another way must not publish a block the converge
	// never writes. The alias's own target is defined here, so what the parse
	// complains about, if anything, is the block and not the fixture.
	c, err := localcfg.Parse([]byte(validShared() + "claude_accounts:\n    team-a:\n        allow_data_to:\n            - belong/edgeweb\n" + block))
	if err != nil {
		t.Fatalf("the fixture does not read back: %v", err)
	}
	if !sharedNeverPublished(c) {
		t.Error("a decoded config carrying claude_account_aliases: is called publishable, so a shared file could carry an alias")
	}
	// That fixture is publishable by three of the predicate's arms at once
	// (registry, claude_accounts and the alias block), so it cannot say which
	// one answered. This pair does: the alias block alone is refused, and a
	// config that carries nothing is the control that keeps the arm from being
	// replaced by a predicate that refuses everything.
	if !sharedNeverPublished(&localcfg.Config{ClaudeAccountAliases: map[string]string{"reviewer": "team-a"}}) {
		t.Error("only the alias block is set, and the config is called publishable")
	}
	if sharedNeverPublished(&localcfg.Config{}) {
		t.Error("a config carrying no section at all is called unpublishable, so the check is not the sections it names")
	}
}

// TestSharedConfigRefusesImageCatalog: the image catalog is the owner's policy
// and is published through the policy node — which the runner reads and fails
// closed on — precisely because the shared config is the file any launcher with
// objectAdmin on the runs bucket can write (D1140). A catalog there would let a
// launcher choose the image every teammate's runs execute in, and the
// Dockerfile a build fetches with the project's own credentials, so the reader
// refuses the key by name before the file is parsed, the way review_floor and
// verifier_accounts are refused.
func TestSharedConfigRefusesImageCatalog(t *testing.T) {
	if !slices.Contains(sharedForbidden, "image_catalog") {
		t.Fatal("sharedForbidden does not name image_catalog, so a published catalog would be read")
	}
	data := validShared() + "image_catalog:\n    entries:\n        - name: universal\n          source: { kind: image, image: ghcr.io/acme/agent:1.2 }\n"
	_, err := ParseShared([]byte(data), sharedAnchor)
	if err == nil {
		t.Fatal("a published shared config carrying image_catalog: was accepted")
	}
	for _, want := range []string{"image_catalog", "never published", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestSharedConfigRefusesIssues: the issues block holds the Issues section's
// two nudge thresholds (T-IS15, docs/troubleshoot-issue/ F14), and they are the
// printing machine's own — the section is printed on whichever terminal runs
// the digest, and the committed report carries no part of it — so the block is
// local-only and localcfg.Config.Shared nils it, the way update_check is, which
// is refused for the same shape of choice: a machine's own decision about the
// nag. A writer of the runs bucket publishing one would be choosing when every
// teammate's machine is told its issues have waited too long, and on a machine
// with no local config for the project the fetched shared config is the one the
// commands read, so the reader refuses the key by name before the file is
// parsed, the way review_floor and the catalog are refused.
func TestSharedConfigRefusesIssues(t *testing.T) {
	if !slices.Contains(sharedForbidden, "issues") {
		t.Fatal("sharedForbidden does not name issues, so a published block would be read")
	}
	data := validShared() + "issues:\n    nudge_after_days: 1\n"
	_, err := ParseShared([]byte(data), sharedAnchor)
	if err == nil {
		t.Fatal("a published shared config carrying issues: was accepted")
	}
	for _, want := range []string{"issues", "never published", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestSharedConfigRefusesOrchestrator: the orchestrator block is the owner's
// policy too (D1239, D1234) and travels the floor's way into the policy node —
// the daemon reads the caps there — rather than being read from the shared
// config, which any launcher with objectAdmin on the runs bucket can write. A
// block there would let a launcher raise the guardrails that exist to bound it:
// the running cap and the per-minute, hourly and daily spawn rates are what a
// looping cycle is held to, and one who could write them could run the loop it
// was written to stop. The reader refuses the key by name before the file is
// parsed, the way review_floor and the catalog are refused.
func TestSharedConfigRefusesOrchestrator(t *testing.T) {
	if !slices.Contains(sharedForbidden, "orchestrator") {
		t.Fatal("sharedForbidden does not name orchestrator, so a published block would be read")
	}
	data := validShared() + "orchestrator:\n    spawn_per_minute: 60\n"
	_, err := ParseShared([]byte(data), sharedAnchor)
	if err == nil {
		t.Fatal("a published shared config carrying orchestrator: was accepted")
	}
	for _, want := range []string{"orchestrator", "never published", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestSharedConfigRefusesCloneURLs: the clone URL is where a repository's runs
// clone from, and it is the local config's block (SD5, D842) published through
// the policy node the runner reads — the file the runner computes the URL from
// and fails closed on — rather than read from the shared config, which any
// launcher with objectAdmin on the runs bucket can write. An entry there would
// let a launcher point every teammate's runs at a URL of its choosing, which is
// the whole of what the node's own rule (projectpolicy.ValidCloneURLs) exists to
// refuse. The reader refuses the key by name before the file is parsed, the way
// review_floor and the catalog are refused.
func TestSharedConfigRefusesCloneURLs(t *testing.T) {
	if !slices.Contains(sharedForbidden, "clone_urls") {
		t.Fatal("sharedForbidden does not name clone_urls, so a published block would be read")
	}
	data := validShared() + "clone_urls:\n    github/acme/app: https://github.com/acme/app\n"
	_, err := ParseShared([]byte(data), sharedAnchor)
	if err == nil {
		t.Fatal("a published shared config carrying clone_urls: was accepted")
	}
	for _, want := range []string{"clone_urls", "never published", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestSharedConfigRefusesModelRouting: the routing table names which credential
// serves which model, and by which admissions, so it is local-only the way the
// pools and crafts are (RT2 of docs/model-credentials-routing/, D3182, F12): a
// launcher who could write it into the runs bucket's shared file would be
// aiming every teammate's calls at a credential and admissions the owner never
// granted. The table travels to the policy node instead, which the runner reads
// and fails closed on, and localcfg.Config.Shared nils it — so the reader
// refuses the key by name before the file is parsed and again on the decoded
// value, the way review_floor and the catalog are refused.
func TestSharedConfigRefusesModelRouting(t *testing.T) {
	if !slices.Contains(sharedForbidden, "model_routing") {
		t.Fatal("sharedForbidden does not name model_routing, so a published table would be read")
	}
	block := "model_routing:\n  - match: claude-*\n    chain:\n      - kind: vertex\n"
	_, err := ParseShared([]byte(validShared()+block), sharedAnchor)
	if err == nil {
		t.Fatal("a published shared config carrying model_routing: was accepted")
	}
	for _, want := range []string{"model_routing", "never published", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// The decoded half, which no document reaches (the key is refused before
	// Parse): a config built another way must not publish a table the converge
	// never writes. The control beside it keeps the arm from being replaced by
	// a predicate that refuses everything.
	if !sharedNeverPublished(&localcfg.Config{ModelRouting: routing.Table{{Match: "claude-*", Chain: []routing.Source{{Kind: routing.KindVertex}}}}}) {
		t.Error("only the routing table is set, and the config is called publishable")
	}
	if sharedNeverPublished(&localcfg.Config{}) {
		t.Error("a config carrying no section at all is called unpublishable, so the check is not the sections it names")
	}
}

// TestConfigPublishRefusesTheBinariesKey: the binaries block is the owner's
// grant of the paths a run may add a binary at and of the cap the run is held
// to, and it travels the way the review floor and the clone URLs do (AR1, D986,
// F11). The shared config is the file any launcher holding objectAdmin on the
// runs bucket can write, so a `binaries:` key in it would be the launcher the
// rule binds stating the rule — admitting its own payload to every teammate's
// runs, from the one file it can write — and both doors are closed here as they
// are for the floor: the reader refuses the key (sharedForbidden), and the
// publisher never writes it, because localcfg's Shared nils the block (so the
// local file's block publishes none of itself) and the block reaches a run off
// the policy node instead, which the owner writes and the runner fails closed
// on.
//
// The existing published object is the launcher's half of the fixture rather
// than an older publisher's, and it is the case the refusal exists for: an
// object carrying the key is refused and replaced, never merged, so the next
// publish is the one that takes the launcher's own grant out of the file every
// teammate reads.
func TestConfigPublishRefusesTheBinariesKey(t *testing.T) {
	f := newCloudFixture(t)
	f.noBudget(t)
	dir := filepath.Join(f.dir, "runs")
	block := "binaries:\n  allow: [\"brand/*.png\"]\n  max_bytes: 1048576\n"
	writePublished(t, dir, publishedWithOtherRepo+block)
	if _, err := localcfg.Parse([]byte(publishedWithOtherRepo + block)); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	f.appendConfig(t, block)
	lc := publishable(t)
	if lc.Binaries == nil {
		t.Fatalf("the local fixture states no binaries block: %+v", lc.Binaries)
	}
	var warned []string
	if _, _, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) }); err != nil {
		t.Fatal(err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "refused") || !strings.Contains(warned[0], "binaries") {
		t.Errorf("warnings = %q", warned)
	}
	got, err := os.ReadFile(sharedObjectPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "binaries") || strings.Contains(string(got), "brand/*.png") {
		t.Errorf("the shared config carries the binaries block, which the launcher who wrote it must not be able to state:\n%s", got)
	}
	if r := publishedRepos(t, dir); len(r) != 1 || r["acme/app"].Provider != "github" {
		t.Errorf("published repos = %v; want only the local acme/app", r)
	}
}

// TestCascadeCarrierIsNeverSharedConfig: the local config grows three carriers
// in this release (N1, D2607) — the cascade under `credential_cascade` (F23:
// the node's `credentials` member is the cascade, while here `credentials` is
// already the list of typed ones), the ladder's `model_grades`, and the
// explicit-unset record `node_removals` — and none of the three is publishable.
// They cross to a run through the policy node, which the owner writes and the
// runner fails closed on. The cascade's fallback decides whether a run may
// spend the project's money at all (D152), a grade names the model a rung runs
// and the credential or pool its calls go out on, and the removals are the
// destructive one: a launcher who could write them into the file it can write
// would be deleting pools, credentials and crafts of the project's node. Both
// doors are closed here as they are for the binaries block — the reader refuses
// each key (sharedForbidden) and the publisher never writes it, because
// localcfg's Shared nils all three.
//
// The published object in the fixture is the launcher's half and the case the
// refusal exists for: an object carrying these keys is refused and replaced,
// never merged, so the launcher's own cascade, grades and removals never
// survive in the file every teammate reads.
func TestCascadeCarrierIsNeverSharedConfig(t *testing.T) {
	for _, k := range []string{"credential_cascade", "model_grades", "node_removals"} {
		if !slices.Contains(sharedForbidden, k) {
			t.Errorf("sharedForbidden does not name %s, so a published %s: would be decoded and read", k, k)
		}
		_, err := ParseShared([]byte(validShared()+k+": {}\n"), sharedAnchor)
		if err == nil {
			t.Fatalf("a published shared config carrying %s: was accepted", k)
		}
		for _, want := range []string{k, "never published", "fugaro update"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal of %s does not name %q: %v", k, want, err)
			}
		}
	}
	f := newCloudFixture(t)
	f.noBudget(t)
	dir := filepath.Join(f.dir, "runs")
	block := "credential_cascade: {fallback: auto, wait_threshold: 90s}\n" +
		"model_grades:\n  strong: {model: anthropic/claude-sonnet-5.5, access: acme-main}\n" +
		"node_removals:\n  pools: [acme-pool]\n"
	writePublished(t, dir, publishedWithOtherRepo+block)
	if _, err := localcfg.Parse([]byte(publishedWithOtherRepo + block)); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	f.appendConfig(t, block)
	lc := publishable(t)
	if lc.CredentialCascade == nil || lc.ModelGrades == nil || lc.NodeRemovals == nil {
		t.Fatalf("the local fixture states none of the three: %+v", lc)
	}
	var warned []string
	if _, _, err := publishSharedWarn(context.Background(), lc, func(m string) { warned = append(warned, m) }); err != nil {
		t.Fatal(err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "refused") || !strings.Contains(warned[0], "credential_cascade") {
		t.Errorf("warnings = %q", warned)
	}
	got, err := os.ReadFile(sharedObjectPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	// The values are checked beside the keys: a publish that moved a block into
	// a section of its own would be the same leak under another name.
	for _, s := range []string{"credential_cascade", "model_grades", "node_removals", "acme-pool", "anthropic/claude-sonnet-5.5", "90s"} {
		if strings.Contains(string(got), s) {
			t.Errorf("the shared config carries %q, which the launcher who wrote it must not be able to state:\n%s", s, got)
		}
	}
	if r := publishedRepos(t, dir); len(r) != 1 || r["acme/app"].Provider != "github" {
		t.Errorf("published repos = %v; want only the local acme/app", r)
	}
}

// TestSharedConfigRefusesARepositoryAccountList: a repos entry's own
// claude_accounts is the repository-level half of the trust anchor T1 keeps in
// the operator-only local config — it is what admits a repository whose runs
// are not on a profile job to an account (docs/project-claude-accounts.md §3) —
// so it is refused in the shared file exactly as the top-level block is, and
// localcfg.Config.Shared clears it for the same reason. A launcher with
// objectAdmin on the runs bucket who could publish one would be adding a
// repository to an account's admission, and the machine whose next publish
// derives the admission would be deriving what that launcher wrote. The key is
// nested, so it is refused on the decoded value rather than by key before the
// parse; the refusal names the entry, the key and the block to edit.
func TestSharedConfigRefusesARepositoryAccountList(t *testing.T) {
	data := strings.Replace(validShared(),
		"        workflows:\n            - web\n",
		"        workflows:\n            - web\n        claude_accounts:\n            - team-a\n", 1)
	if data == validShared() {
		t.Fatal("the fixture did not gain the entry: the check below would pass on a file carrying no claude_accounts")
	}
	_, err := ParseShared([]byte(data), sharedAnchor)
	if err == nil {
		t.Fatal("a published shared config carrying repos.<r>.claude_accounts: was accepted")
	}
	for _, want := range []string{"repos.belong/edgeweb.claude_accounts", "never published", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// The publish half: the converge's own copy of the repos map carries
	// neither local key, so a config that has one is never the file that
	// reaches the bucket. The control beside it is the same config without the
	// key, so the check is the entry's own keys and not the entry.
	c, err := localcfg.Parse([]byte(data))
	if err != nil {
		t.Fatalf("the fixture does not read back: %v", err)
	}
	if got := c.Shared().Repos["belong/edgeweb"]; len(got.ClaudeAccounts) > 0 || got.BaseBranch != "" {
		t.Errorf("the published copy of belong/edgeweb carries %v/%q, so a repository's own account list would travel to the runs bucket", got.ClaudeAccounts, got.BaseBranch)
	}
	if got := validSharedPublished(t); len(got.Repos["belong/edgeweb"].ClaudeAccounts) > 0 {
		t.Error("the control entry carries an account list, so the check above proves nothing")
	}
}

// validSharedPublished is the shared copy of the valid fixture, for the tests
// that need the config the converge publishes beside the config it reads.
func validSharedPublished(t *testing.T) *localcfg.Config {
	t.Helper()
	c, err := localcfg.Parse([]byte(validShared()))
	if err != nil {
		t.Fatalf("the fixture does not read back: %v", err)
	}
	return c.Shared()
}

// TestSharedConfigRefusesEveryForbiddenSection: the list is the fence, and a
// name dropped from it is a section the commands would read from a file any
// launcher with objectAdmin on the runs bucket can write. Each name is put in
// front of the reader here, so the check is the list's own and cannot be kept
// by the handful of tests that pin one key each -- the ones this release added
// (`image_map`, `bake_repos`) included.
func TestSharedConfigRefusesEveryForbiddenSection(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range sharedForbidden {
		if seen[k] {
			t.Errorf("sharedForbidden names %s twice", k)
		}
		seen[k] = true
		t.Run(k, func(t *testing.T) {
			// The value is a flow mapping rather than a section's own shape:
			// the refusal is on the key, before the file is parsed, so
			// nothing about the value can keep the check from firing.
			_, err := ParseShared([]byte(validShared()+k+": {}\n"), sharedAnchor)
			if err == nil {
				t.Fatalf("a published shared config carrying %s: was accepted: the commands would read a section the converge never writes", k)
			}
			for _, want := range []string{k, "never published", "fugaro update"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
	// The keys this release added are in it: the map decides which image each
	// repository's jobs execute (D1906) and the switch decides whether a
	// repository is baked at all (D1901), so a launcher who could write either
	// into the shared file would be choosing what code every teammate's runs
	// run -- the catalog's own reason, one step out -- and the triage switch
	// decides which of a review round's findings hold a pull request back
	// (D1854), which is the floor's reason one level down. `rigor_pending` is
	// the wizard's rigor answer waiting for the Layer stage to write it into the
	// project layer (task W of docs/rigor-command/implementation-plan.md, D900),
	// and it is refused for the floor's reason read from the other end: the mode
	// is how much review every run of the project gets, so a launcher who could
	// write the key would be choosing that for every teammate.
	//
	// `backend` joined the list with the enable (plan
	// 2026-10-19-compute-backend.md Task 10 step 4), for a reason of the same
	// shape: the checks that judge a `batch:` block are `fugaro doctor`'s and
	// they read the machine's own config, so a launcher who could write the
	// choice into the shared file would move every teammate with no local
	// config onto VMs with a block that same launcher chose and no check ever
	// saw.
	for _, k := range []string{"image_map", "bake_repos", "triage", "rigor_pending", "backend"} {
		if !slices.Contains(sharedForbidden, k) {
			t.Errorf("sharedForbidden does not name %s, so a published %s would be read", k, k)
		}
	}
}

// TestSharedNeverPublishedCatchesTheDecodedSections: the second net, on the
// values the commands use rather than on the file's keys. ParseShared refuses
// every one of these keys before it parses, so no file reaches this check
// carrying one -- which is exactly why it needs a test of its own: the branch
// is unreachable from a document, and a field dropped from it would leave every
// test green while a config built another way (a merge, a decoder that mapped a
// key onto a field) published a section the converge never writes.
func TestSharedNeverPublishedCatchesTheDecodedSections(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"image_map", "image_map:\n    acme/app: tools\n"},
		{"bake_repos", "bake_repos: false\n"},
		{"triage", "triage: false\n"},
		{"review_against_task", "review_against_task: false\n"},
		{"rigor_pending", "rigor_pending: critical\n"},
		{"backend", "backend: cloud-batch\n"},
		{"clone_urls", "clone_urls:\n    github/acme/app: https://github.com/acme/app\n"},
		{"bitbucket_tokens", "bitbucket_tokens:\n    - {name: ci, type: repo, coverage: [belong/edgeweb]}\n"},
		{"credential_cascade", "credential_cascade: {fallback: auto}\n"},
		{"model_grades", "model_grades:\n    strong: {model: anthropic/claude-sonnet-5.5, access: acme-main}\n"},
		{"node_removals", "node_removals:\n    pools: [acme-pool]\n"},
		{"reports_commit", "reports_commit: {repo: acme/reports}\n"},
		{"workspace_shared", "workspace_shared:\n    anthropic-api-key: {kind: model, projects: [aurora]}\n"},
		{"locations", "locations:\n    firestore: us-east5\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := localcfg.Parse([]byte(validShared() + tc.yaml))
			if err != nil {
				t.Fatalf("the fixture does not read back: %v", err)
			}
			if !sharedNeverPublished(c) {
				t.Errorf("a decoded config carrying %s: is called publishable, so a shared file could carry the section", tc.name)
			}
			// And the file it came from is refused by name, which is the net a
			// document meets first.
			_, err = ParseShared([]byte(validShared()+tc.yaml), sharedAnchor)
			if err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Errorf("ParseShared on %s: = %v, want the key named", tc.name, err)
			}
		})
	}
	// The control: the file every other test in this file parses carries none
	// of the forbidden sections, so the predicate is not simply always true.
	c, err := localcfg.Parse([]byte(validShared()))
	if err != nil {
		t.Fatal(err)
	}
	if sharedNeverPublished(c) {
		t.Error("a shared config carrying none of the forbidden sections is called unpublishable")
	}
}

// TestSharedNeverPublishesReviewAgainstTask: the review-against-task switch is
// the triage switch's own case one key over (R7 of docs/review-against-task/,
// D2935) and the reason is the sharper one of the two — the switch is what
// turns off the check every review stage makes of a run's own work, so a
// launcher who could write it into the runs bucket's shared file would be
// having every teammate's pull requests reviewed against nothing, from a file
// it can write. The key is refused by name before the file is parsed, the way
// the triage switch beside it is, and again on the decoded value, so a config
// that arrived some other way cannot publish it either.
//
// The local config keeps its own copy: the switch is the owner's answer for the
// whole project, published through the policy node, so dropping it from the
// shared file must not touch the answer on the machine that wrote it.
func TestSharedNeverPublishesReviewAgainstTask(t *testing.T) {
	if !slices.Contains(sharedForbidden, "review_against_task") {
		t.Fatal("sharedForbidden does not name review_against_task, so a published switch would be read")
	}
	_, err := ParseShared([]byte(validShared()+"review_against_task: false\n"), sharedAnchor)
	if err == nil {
		t.Fatal("a published shared config carrying review_against_task: was accepted")
	}
	for _, want := range []string{"review_against_task", "never published", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	off := false
	c, err := localcfg.Parse([]byte(validShared() + "review_against_task: false\n"))
	if err != nil {
		t.Fatalf("the fixture does not read back: %v", err)
	}
	if !sharedNeverPublished(c) {
		t.Error("a decoded config carrying review_against_task: is called publishable, so a shared file could carry the switch")
	}
	// The switch alone, so the arm that answered is the one this test is about
	// and not another key the fixture happens to carry, and the control that
	// keeps the predicate from being one that refuses everything.
	if !sharedNeverPublished(&localcfg.Config{ReviewAgainstTask: &off}) {
		t.Error("only the review-against-task switch is set, and the config is called publishable")
	}
	if sharedNeverPublished(&localcfg.Config{}) {
		t.Error("a config carrying no section at all is called unpublishable, so the check is not the sections it names")
	}
	// The layer the shared file is written from, and the merge a launcher's
	// copy is read through: both drop the switch and neither touches the
	// config they were given.
	for name, got := range map[string]*localcfg.Config{"shared": c.Shared(), "merged": localcfg.MergeShared(nil, c)} {
		if got.ReviewAgainstTask != nil {
			t.Errorf("%s: review_against_task = %v, want it dropped: a launcher may not write the project's check", name, got.ReviewAgainstTask)
		}
		out, err := got.Marshal()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(out), "review_against_task") {
			t.Errorf("%s: the marshalled config carries the switch:\n%s", name, out)
		}
	}
	if c.ReviewAgainstTask == nil || *c.ReviewAgainstTask {
		t.Error("Shared/MergeShared cleared the switch on the config they were given")
	}
}
