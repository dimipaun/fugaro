package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/driver"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/report"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// testCacheRoot is TestMain's own cache directory. It is never removed (the
// operating system cleans its temp directory), unlike t.TempDir's. A test
// whose command leaves a background cache write running past its return can
// point XDG_CACHE_HOME here, so the late write cannot race t.TempDir's
// RemoveAll cleanup. "" when TestMain could not create it.
var testCacheRoot string

// testConfigRoot is TestMain's own config directory: the
// $XDG_CONFIG_HOME every test that does not isolate one of its own gets, so
// a test can never write the developer's real ~/.config/fugaro (see the
// comment in TestMain). Also never removed. "" when TestMain could not
// create it, which TestATestNeverWritesTheDeveloperConfigHome fails on.
var testConfigRoot string

// testStateRoot is TestMain's own state directory: the $XDG_STATE_HOME every
// test that does not isolate one of its own gets, so the logs the step
// reporter keeps (report.OpenLog) can never be filed in the developer's real
// ~/.local/state/fugaro/logs — and pruned there, 50 files per command name.
// Also never removed. "" when TestMain could not create it, which
// TestATestNeverWritesTheDeveloperStateHome fails on.
var testStateRoot string

// TestMain closes the ways a test could reach real Google through a
// gs:// bucket_url or an unfaked client: no application default
// credentials (a file that doesn't exist), no metadata server, and every
// https request sent to a proxy nothing listens on (Go sends loopback
// traffic, which is all the fakes use, around any proxy). A test that
// means to talk to GCS points its config's endpoints at a fake.
func TestMain(m *testing.M) {
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/fugaro-tests-have-no-credentials.json")
	os.Setenv("GCE_METADATA_HOST", "127.0.0.1:1")
	os.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	os.Setenv("https_proxy", "http://127.0.0.1:1")
	os.Unsetenv("STORAGE_EMULATOR_HOST")
	// warnUpdateAvailable/startUpdateCheck run on every command (PreRun and
	// PostRun), so every test in this package that executes one touches the
	// update-check cache; without this, a test that never calls
	// isolateProjects itself would read and write the developer's own
	// ~/.cache/fugaro/update-check.json. The HTTPS_PROXY above already
	// stops the (rare, info-severity) doctor lookup that reaches it from
	// going anywhere real.
	if dir, err := os.MkdirTemp("", "fugaro-test-cache-"); err == nil {
		testCacheRoot = dir
		os.Setenv("XDG_CACHE_HOME", dir)
	}
	// The other half of the developer's own fugaro directory: every command
	// that resolves a checkout's project remembers it, in
	// $XDG_CONFIG_HOME/fugaro/checkouts.json (recordCheckout, project.go) —
	// so without this, every test that runs a command in a checkout without
	// isolating a config home of its own writes into the developer's real
	// ~/.config/fugaro, the file the console's first run reads, and one whose
	// own entries the write path prunes (20 repositories per project, 5
	// paths each) — a suite run evicts them. The state it leaves is not even
	// harmless noise: /tmp/Test… paths read as installations of a checkout
	// that no longer exists.
	//
	// A test that means to look at a config home makes its own with
	// isolateProjects, which is the override the config tests already use;
	// this is the backstop that makes reaching the real one impossible for
	// every other test. Neither directory is removed: the operating system
	// cleans its temp directory, and TestATestNeverWritesTheDeveloperConfigHome
	// needs the config one to outlive the test that wrote it.
	if dir, err := os.MkdirTemp("", "fugaro-test-config-"); err == nil {
		testConfigRoot = dir
		os.Setenv("XDG_CONFIG_HOME", dir)
	}
	// And the third: the state directory, where every command that runs
	// steps or writes files files its log (D1351, D1352). A log is opened
	// and pruned by the command that writes it — 50 files per command name —
	// so without this a suite run would read and delete the developer's own
	// ~/.local/state/fugaro/logs, the logs an operator reads back after a
	// failure. A test that reads its command's log back isolates a state
	// home of its own (isolateState, or a rig that does it per test for a
	// whole family — newInitRig); this is the backstop for every other test.
	// Never removed, like the two above.
	if dir, err := os.MkdirTemp("", "fugaro-test-state-"); err == nil {
		testStateRoot = dir
		os.Setenv("XDG_STATE_HOME", dir)
	}
	// init refuses to apply anything in a coding agent's session, and the
	// tests are often run from one: a test that means to be in one sets a
	// marker itself.
	for _, k := range agentMarkers {
		os.Unsetenv(k)
	}
	// Commands read the project from the fugaro.yaml of the checkout they run
	// in, found through git from the working directory. The tests run inside
	// this repository, whose own fugaro.yaml names the project fugaro, so git
	// is told not to look in the repository root: a test that wants a
	// checkout makes its own (git doesn't enter a ceiling directory itself).
	if wd, err := os.Getwd(); err == nil {
		os.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(filepath.Dir(wd)))
	}
	// The shared config is published to the runs bucket after init writes
	// the local one: tests that don't look at it publish to memory, never
	// to a real bucket.
	sharedBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) {
		if strings.HasPrefix(url, "file://") {
			return blobx.Open(ctx, url)
		}
		return blobx.Open(ctx, "mem://")
	}
	// Production skips a gs:// bucket (publish, build-record read) when the
	// config's endpoints are fakes; the tests reach buckets through seams
	// while their configs carry fake endpoints, so they turn the skip off and
	// the tests of the skip itself turn it on.
	skipGSOnFakeEndpoints = false
	// Neither shipped gate needs a seam any more: managedDeployerShipped is
	// true as of 0.10.0 and batchProvisionShipped as of 0.9.5, so the managed
	// paths and the Batch provisioning run as they ship, with nothing opened
	// for the tests.
	// findLayer's bucket has no seam override redirecting gs:// elsewhere
	// the way sharedBucketOpener does above: a rig whose local config
	// computes a gs:// runs bucket URL (most do; only bucket_url: file://
	// fixtures don't) would otherwise have it opened for real the first
	// time anything resolves a project layer. A test that means to
	// exercise a layer already uses a file:// bucket_url or overrides this
	// var itself; anything else panics loudly instead of quietly reaching
	// storage.googleapis.com.
	layerBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) {
		if strings.HasPrefix(url, "gs://") {
			panic("fugaro: test tried to open a real gs:// project layer bucket: " + url + "; use a bucket_url: file:// fixture or override layerBucketOpener")
		}
		return blobx.Open(ctx, url)
	}
	// The fake gh of the watch tests links to one wrapper, run once here
	// before any test can fork (testutil.FakeCommand).
	cleanupFakes, err := testutil.PrepareFakeCommands()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	cleanupFakes()
	os.Exit(code)
}

// stubBucket is the base of a test blob driver: the embedded driver.Bucket is
// nil, so only the methods a test's driver implements are ever reached, and the
// four methods driver.Bucket demands whatever the test reaches -- artifacts'
// appearDriver and the budget compute tests' countListDriver -- are answered
// here once instead of in each of them.
type stubBucket struct{ driver.Bucket }

func (stubBucket) ErrorCode(error) gcerrors.ErrorCode { return gcerrors.Unknown }
func (stubBucket) As(any) bool                        { return false }
func (stubBucket) ErrorAs(error, any) bool            { return false }
func (stubBucket) Close() error                       { return nil }

// A test that reaches findLayer's bucket with a gs:// URL and no seam
// override of its own must crash loudly, not quietly cross the network
// (TestNoRealGCSFromTests would stop the credentials and the proxy, but a
// slow failure there is still the gax retry loop the production risk fix
// in layer_resolve.go's layerBucketTimeout exists for).
//
// Mutation (run, restore): change the `strings.HasPrefix(url, "gs://")`
// condition in TestMain to `false`, and this test fails because nothing
// panics.
func TestLayerBucketOpenerRefusesARealGSBucket(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("layerBucketOpener did not panic on a gs:// URL")
		}
		if !strings.Contains(fmt.Sprint(r), "gs://") {
			t.Fatalf("panic = %v", r)
		}
	}()
	_, _ = layerBucketOpener(context.Background(), "gs://fugaro-tests-should-never-open-this")
}

// TestATestNeverWritesTheDeveloperConfigHome: the commands a test runs
// remember the checkout they ran in, under $XDG_CONFIG_HOME/fugaro
// (recordCheckout). TestMain points XDG_CONFIG_HOME at a directory of its own
// before any test runs, so a test that isolates no config home of its own —
// and several do not — still cannot reach the developer's real
// ~/.config/fugaro/checkouts.json, where an operator's own entries live and
// where the recorder's pruning (20 repositories per project, 5 paths each)
// would evict them.
//
// This is the recording path itself, run exactly as such a test runs it: no
// isolation call anywhere, a checkout of acme/app with an origin, and
// checkoutProject over it. The state file it writes must be the one under
// TestMain's config home.
//
// The one thing the test does set up is the enrolment the recorder now
// requires (recordCheckout): TestMain's config home gets a project config for
// this test's own project name, written there and removed again at the end of
// the test, so the recorder has something to record under. It is a name of
// its own rather than one a fixture uses, so a test that reads TestMain's
// directory while this one runs — none does, they all isolate — would not
// find a project it did not write.
//
// Mutation (run, restore): delete the XDG_CONFIG_HOME block from TestMain and
// this fails twice over — testConfigRoot is empty and XDG_CONFIG_HOME is the
// developer's own (usually unset, so the recording would land in
// ~/.config/fugaro/checkouts.json).
func TestATestNeverWritesTheDeveloperConfigHome(t *testing.T) {
	if os.Getenv("XDG_CONFIG_HOME") != testConfigRoot || testConfigRoot == "" {
		t.Fatalf("XDG_CONFIG_HOME = %q, TestMain's own = %q: a test that records a checkout would write the developer's own config home", os.Getenv("XDG_CONFIG_HOME"), testConfigRoot)
	}
	if home := os.Getenv("HOME"); home != "" && strings.HasPrefix(testConfigRoot, home+string(filepath.Separator)) {
		t.Fatalf("TestMain's config home %q is inside the developer's home %q", testConfigRoot, home)
	}
	const project = "devhome"
	projDir := filepath.Join(testConfigRoot, "fugaro", "projects")
	if err := os.MkdirAll(projDir, 0o700); err != nil {
		t.Fatal(err)
	}
	projPath := filepath.Join(projDir, project+".yaml")
	body := "version: 1\nname: " + project + "\ngcp_project: fugaro-tests-devhome\n" +
		"region: us-east5\nruns_bucket: fugaro-runs-fugaro-tests-devhome\nrepos:\n  acme/app: {}\n"
	if err := os.WriteFile(projPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(projPath) })
	// A checkout with an origin, and the recorder over it — no isolate call.
	dir := gitCheckout(t, filepath.Join(t.TempDir(), "app"), checkoutsProjectYAML(project))
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	co, err := checkoutProject(context.Background(), dir)
	if err != nil || co == nil {
		t.Fatalf("checkout = %+v, %v", co, err)
	}
	path, err := localcfg.CheckoutsPath(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(testConfigRoot, "fugaro", localcfg.CheckoutsDir, localcfg.CheckoutsFile); path != want {
		t.Fatalf("the state file = %s, want %s", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the recording wrote nothing to its own config home: %v", err)
	}
}

// TestAStepCommandsLogLandsInTestMainsStateHome opens a log the way a
// step-printing command does and reads back the path the sink reports: the
// state directory's half of the rule above, through the code that resolves it
// rather than the arithmetic of the rule, so a change to either one moves this
// test's failure with it. A log under the developer's own state home is
// pruned on every open — fourteen days, fifty files per command name — so
// this is the difference between a suite run and an operator's logs.
//
// TestATestNeverWritesTheDeveloperStateHome (report_run_test.go, CL7c) is the
// other half: that the variable itself is TestMain's and not under $HOME.
func TestAStepCommandsLogLandsInTestMainsStateHome(t *testing.T) {
	sink, err := report.OpenLog("devhome", os.Getenv, runtime.GOOS, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	if dir, want := filepath.Dir(sink.Path()), filepath.Join(testStateRoot, "fugaro", "logs"); dir != want {
		t.Fatalf("a run's log is filed under %s, want %s", dir, want)
	}
}

// TestATestNeverWritesTheDeveloperStateHome is the state home's half, and it
// drives the write rather than reading the variable: a run of a migrated
// command files the log its account points a failure at (D1351), and one that
// landed in the developer's own directory would be pruned by the next run
// (D1352). The command is not isolated the way its own tests isolate it —
// no t.Setenv of its own — so what it does is what every other test in this
// package does, and the directory it must not have written is read the way
// OpenLog would resolve it with $XDG_STATE_HOME taken away: the developer's
// own.
//
// Mutation (run, restore): delete the XDG_STATE_HOME block from TestMain and
// this fails twice over — testStateRoot is empty, and the log lands in the
// developer's own ~/.local/state/fugaro/logs.
func TestATestNeverWritesTheDeveloperStateHome(t *testing.T) {
	if os.Getenv("XDG_STATE_HOME") != testStateRoot || testStateRoot == "" {
		t.Fatalf("XDG_STATE_HOME = %q, TestMain's own = %q: a test that runs a mutating command would file its log in the developer's own state directory", os.Getenv("XDG_STATE_HOME"), testStateRoot)
	}
	if home := os.Getenv("HOME"); home != "" && strings.HasPrefix(testStateRoot, home+string(filepath.Separator)) {
		t.Fatalf("TestMain's state home %q is inside the developer's home %q", testStateRoot, home)
	}
	secretsFixture(t)
	logs := filepath.Join(testStateRoot, "fugaro", "logs")
	devLogs := developerStateLogDir(t)
	before, devBefore := readDirNames(t, logs), readDirNames(t, devLogs)
	if _, _, err := executeStdin(t, tokenValue+"\n", "secrets", "set", "claude-oauth-token", "--repo", "acme/app"); err != nil {
		t.Fatal(err)
	}
	// The harm this is about, and the half that cannot be read as a count of
	// names: nothing this run left is in the developer's own directory. It is
	// asserted first because it is the assertion no other test can take away —
	// a log is named `<command>-<14 digits UTC>-<pid>` (report.logName) and the
	// create is O_EXCL, so a second run of one command in this process and
	// second keeps no log at all.
	if fresh := newNames(readDirNames(t, devLogs), devBefore); len(fresh) != 0 {
		t.Fatalf("a test filed %v in the developer's own state directory %s", fresh, devLogs)
	}
	// And this run's log is the new one here. It cannot be when another test's
	// run of this command in this same process and second already holds the
	// name: that is a test to give a state home of its own (isolateState,
	// newInitRig), not a failure of this one.
	fresh := newNames(readDirNames(t, logs), before)
	switch {
	case len(fresh) == 1 && strings.HasPrefix(fresh[0], "secrets-set-"):
	case len(fresh) == 0:
		t.Skipf("another run of `secrets set` in this process and second holds this run's log name, so this run kept no log to find in %s", logs)
	default:
		t.Fatalf("the run left %v in %s, want one secrets-set log of its own", fresh, logs)
	}
}

// developerStateLogDir is the directory a run would file its log in with
// $XDG_STATE_HOME taken away: the developer's own, resolved through the one
// rule the CLI has (localcfg.UserDir), so this test names no path of its own.
func developerStateLogDir(t *testing.T) string {
	t.Helper()
	base, err := localcfg.UserDir(func(name string) string {
		if name == "XDG_STATE_HOME" {
			return ""
		}
		return os.Getenv(name)
	}, runtime.GOOS, localcfg.DirState)
	if err != nil {
		t.Skipf("the developer's own state directory cannot be named: %v", err)
	}
	return filepath.Join(base, "fugaro", "logs")
}

// newNames is the names of after that were not in before.
func newNames(after, before []string) []string {
	had := make(map[string]bool, len(before))
	for _, n := range before {
		had[n] = true
	}
	var fresh []string
	for _, n := range after {
		if !had[n] {
			fresh = append(fresh, n)
		}
	}
	return fresh
}

// readDirNames is a directory's entry names, and nothing when it is not there
// yet (this test's first read is of a directory no test has written to).
func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestNoRealGCSFromTests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	b, err := blobx.Open(ctx, "gs://fugaro-tests-never-this-bucket")
	if err != nil {
		return // refused at once: also fine
	}
	defer b.Close()
	// Whatever stops it (no credentials, the dead proxy), it must stop:
	// the bucket doesn't exist, and nothing real may answer.
	if _, err := b.Exists(ctx, "x"); err == nil {
		t.Fatal("a gs:// read of a bucket nobody made succeeded: a test can reach real Google")
	}
}
