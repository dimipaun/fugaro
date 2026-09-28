package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// appSlug is the storage slug of the fixture's repository, GitHub's acme/app.
var appSlug = mustSlug("github", "acme/app")

func mustSlug(provider, repo string) string {
	s, err := task.Slug(provider, repo)
	if err != nil {
		panic(err)
	}
	return s
}

// cloudFixture is a local config pointing at fakes and a file:// bucket.
type cloudFixture struct {
	dir     string
	bucket  string // file:// URL
	run     *gcpfake.Run
	logging *gcpfake.Logging
}

// newCloudFixture writes a local config whose endpoints point at the Run and
// Logging fakes, plus any extra "key: url" endpoint entries (secret_manager,
// cloud_build) a later test adds with its own fake.
func newCloudFixture(t *testing.T, extraEndpoints ...string) *cloudFixture {
	t.Helper()
	f := &cloudFixture{dir: t.TempDir(), run: gcpfake.NewRun(t), logging: gcpfake.NewLogging(t)}
	// Executions the fake starts live where the config's backend looks.
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	runs := filepath.Join(f.dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	f.bucket = "file://" + runs
	cfg := "version: 1\nproject: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\nbucket_url: " + f.bucket +
		"\nuser: someone@example.com\nmax_parallel: 2\n" +
		"endpoints: { run: " + f.run.URL + "/, logging: " + f.logging.URL + "/, " + extra(extraEndpoints) + "no_auth: true }\n" +
		"repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	path := filepath.Join(f.dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	f.run.AddJob(gcp.JobName(appSlug, "web"), "4", "8Gi")
	return f
}

// appendConfig adds top-level YAML (such as registry: …) to the local config.
func (f *cloudFixture) appendConfig(t *testing.T, yaml string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(yaml)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

func extra(entries []string) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e + ", ")
	}
	return b.String()
}

// memEnv builds a cloudEnv on a shared memblob bucket, for tests that call
// launchRun directly and concurrently (fileblob's IfNotExist is not atomic).
func memEnv(t *testing.T, f *cloudFixture) *cloudEnv {
	t.Helper()
	return envOn(t, f, blobx.Wrap(memblob.OpenBucket(nil)))
}

// gcsEnv builds a cloudEnv on the GCS fake, so conditional writes take
// blobx's generation-matching path, as in production.
func gcsEnv(t *testing.T, f *cloudFixture) *cloudEnv {
	t.Helper()
	return envOn(t, f, gcpfake.NewGCS(t).Bucket(t, "runs"))
}

func envOn(t *testing.T, f *cloudFixture, bucket *blobx.Bucket) *cloudEnv {
	t.Helper()
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	be, err := gcp.New(context.Background(), gcp.Options{Project: lc.Project, Region: lc.Region,
		Endpoints: gcp.Endpoints{Run: f.run.URL + "/", Logging: f.logging.URL + "/", NoAuth: true}})
	if err != nil {
		t.Fatal(err)
	}
	return &cloudEnv{lc: lc, bucket: bucket, be: be}
}

func TestGitprovSafe(t *testing.T) {
	for _, u := range []string{"https://user:tok@host/o/r", "ssh://user:tok@host/o/r"} {
		if got := gitprovSafe(u); strings.Contains(got, "tok") || !strings.Contains(got, "host/o/r") {
			t.Errorf("gitprovSafe(%q) = %q", u, got)
		}
	}
	if got := gitprovSafe("https://host/%zz"); got != "<unparseable URL>" {
		t.Errorf("unparseable: %q", got)
	}
}

func TestOriginRepo(t *testing.T) {
	testutil.IsolateGit(t)
	for origin, want := range map[string]string{
		"git@github.com:acme/app.git":          "acme/app",
		"https://user:tok@github.com/acme/app": "acme/app",
		"ssh://git@bitbucket.org:22/acme/app":  "acme/app",
	} {
		dir := t.TempDir()
		testutil.Git(t, dir, "init", "-q")
		testutil.Git(t, dir, "remote", "add", "origin", origin)
		t.Chdir(dir)
		if got, err := originRepo(context.Background()); err != nil || got != want {
			t.Errorf("originRepo(%s) = %q, %v", origin, got, err)
		}
	}
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "https://user:tok@host/deep/group/app")
	t.Chdir(dir)
	if _, err := originRepo(context.Background()); ExitCode(err) != ExitUserError || strings.Contains(err.Error(), "tok") {
		t.Errorf("nested origin: %v", err)
	}
	t.Chdir(t.TempDir())
	if _, err := originRepo(context.Background()); ExitCode(err) != ExitUserError {
		t.Errorf("no checkout: %v", err)
	}
}

func TestRemoteKeepsAnExistingCode(t *testing.T) {
	if ExitCode(remote(userErr("bad flag"))) != ExitUserError {
		t.Error("remote() overrode a user error")
	}
	if ExitCode(remote(os.ErrPermission)) != ExitRemoteError || remote(nil) != nil {
		t.Error("remote() did not mark a plain error as exit 2")
	}
}

// The cost estimate's prices are the region the jobs run in, after --region.
func TestCloudPricesFollowTheRegion(t *testing.T) {
	newCloudFixture(t) // region us-east5
	if gcp.ListPrices("us-east5") == gcp.ListPrices("europe-west2") {
		t.Fatal("the two regions price the same, so the test cannot tell them apart")
	}
	for flag, region := range map[string]string{"": "us-east5", "europe-west2": "europe-west2"} {
		env, err := openCloud(context.Background(), cloudOptions{region: flag})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := env.prices(), gcp.ListPrices(region); got != want {
			t.Errorf("--region %q: prices = %+v, want %s's %+v", flag, got, region, want)
		}
		env.Close()
	}
}

// "." and ".." are no slug: path.Join would take them out of runs/
// (security review S-M7).
func TestRunRefRefusesDotSlugs(t *testing.T) {
	newCloudFixture(t)
	for _, ref := range []string{"./20260927-100000-abcd", "../20260927-100000-abcd"} {
		for _, cmd := range []string{"logs", "diagnose", "cancel"} {
			_, _, err := execute(t, cmd, ref)
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "<repo-slug>/<run-id>") {
				t.Errorf("%s %s: %v", cmd, ref, err)
			}
		}
	}
}
