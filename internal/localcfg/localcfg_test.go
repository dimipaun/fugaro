package localcfg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const sample = `version: 1
name: aurora
gcp_project: my-project
region: us-central1
runs_bucket: my-runs
registry: us-central1-docker.pkg.dev/my-project/fugaro
repos:
  acme/web: { provider: github, base_branch: main, workflows: [web] }
`

func TestParseDefaults(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxParallel != 20 || c.Build.MachineType != "E2_HIGHCPU_8" || c.BucketURL() != "gs://my-runs" || c.BuildRegion() != "us-central1" {
		t.Fatalf("defaults = %+v", c)
	}
	if r := c.Repos["acme/web"]; r.Provider != "github" || r.BaseBranch != "main" || len(r.Workflows) != 1 {
		t.Fatalf("repo = %+v", r)
	}
	if err := c.Override("my-project", "europe-west1"); err != nil {
		t.Fatal(err)
	}
	if c.GCPProject != "my-project" || c.Region != "europe-west1" || c.BuildRegion() != "europe-west1" {
		t.Fatalf("override = %+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, msg string }{
		"unknown field": {sample + "colour: blue\n", "colour"},
		"bad project":   {strings.Replace(sample, "my-project\n", "My_Project\n", 1), "gcp_project"},
		"no bucket":     {strings.Replace(sample, "runs_bucket: my-runs\n", "", 1), "runs_bucket"},
		"bad repo":      {sample + "  nope: { workflows: [web] }\n", "owner/name"},
		"bad workflow":  {sample + "  acme/api: { workflows: [Web] }\n", "workflow"},
		"bad parallel":  {sample + "max_parallel: -1\n", "max_parallel"},
		"bad provider":  {sample + "  acme/api: { provider: githbu, workflows: [web] }\n", "must be one of github, bitbucket"},
		"fake provider": {sample + "  acme/api: { provider: fake, workflows: [web] }\n", "must be one of github, bitbucket"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v, want ~%q", err, tc.msg)
			}
		})
	}
}

func TestProjectPathsAndLoad(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"HOME": dir}
	get := func(k string) string { return env[k] }
	d, err := ProjectsDir(get)
	if err != nil || d != filepath.Join(dir, ".config", "fugaro", "projects") {
		t.Fatalf("ProjectsDir = %q, %v", d, err)
	}
	env["XDG_CONFIG_HOME"] = filepath.Join(dir, "xdg")
	p, err := ProjectPath(get, "aurora")
	if err != nil || p != filepath.Join(dir, "xdg", "fugaro", "projects", "aurora.yaml") {
		t.Fatalf("ProjectPath = %q, %v", p, err)
	}
	if _, err := ProjectPath(get, "../x"); err == nil {
		t.Fatal("ProjectPath accepted a name that isn't a project name")
	}
	if _, err := ProjectsDir(func(string) string { return "" }); err == nil {
		t.Fatal("no HOME, no XDG_CONFIG_HOME: ProjectsDir succeeded")
	}
	// A Windows shell sets USERPROFILE and no HOME. Before UserDir that was
	// "neither XDG_CONFIG_HOME nor HOME is set", i.e. no config directory at
	// all for a Windows user (F5); the profile is the fallback that decides
	// the path, and the %APPDATA% rule above it is pinned by
	// TestConfigDirFollowsThePlatform, which runs the Windows rule here.
	profile := t.TempDir()
	onlyProfile := func(k string) string {
		if k == "USERPROFILE" {
			return profile
		}
		return ""
	}
	got, err := ProjectsDir(onlyProfile)
	if err != nil || got != filepath.Join(profile, ".config", "fugaro", "projects") {
		t.Fatalf("ProjectsDir with only USERPROFILE = %q, %v", got, err)
	}
	if _, _, err := LoadProject(get, "aurora"); !errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), "fugaro setup") {
		t.Fatalf("missing file: %v", err)
	}
	writeFile(t, p, sample)
	c, path, err := LoadProject(get, "aurora")
	if err != nil || c.GCPProject != "my-project" || c.Name != "aurora" || path != p {
		t.Fatalf("LoadProject = %+v, %q, %v", c, path, err)
	}
	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if again, err := Parse(data); err != nil || again.RunsBucket != "my-runs" || again.Name != "aurora" || again.GCPProject != "my-project" {
		t.Fatalf("round trip = %+v, %v", again, err)
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The local config of before project configs had project: for the GCP
// project; it is refused, saying what to do.
func TestParseOldProjectKey(t *testing.T) {
	old := strings.Replace(strings.Replace(sample, "gcp_project:", "project:", 1), "name: aurora\n", "", 1)
	_, err := Parse([]byte(old))
	for _, want := range []string{"`project:` is now `gcp_project:`", "projects/<name>.yaml", "name: <name>", "§13.1"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want one saying %q", err, want)
		}
	}
	// Even beside the new keys.
	if _, err := Parse([]byte(sample + "project: my-project\n")); err == nil || !strings.Contains(err.Error(), "gcp_project") {
		t.Fatalf("both keys: err = %v", err)
	}
}

func TestParseRequiresName(t *testing.T) {
	if _, err := Parse([]byte(strings.Replace(sample, "name: aurora\n", "", 1))); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("no name: err = %v", err)
	}
	for _, bad := range []string{"Aurora", "-a", "a_b", strings.Repeat("a", 41)} {
		if _, err := Parse([]byte(strings.Replace(sample, "name: aurora", "name: "+bad, 1))); err == nil || !strings.Contains(err.Error(), "name") {
			t.Errorf("name %q: err = %v", bad, err)
		}
	}
	if _, err := Parse([]byte(strings.Replace(sample, "gcp_project: my-project\n", "", 1))); err == nil || !strings.Contains(err.Error(), "gcp_project") {
		t.Fatalf("no gcp_project: err = %v", err)
	}
}

// projects/<name>.yaml holds project <name>: a file whose name: says
// another is refused, never loaded as either.
func TestLoadProjectNameMustMatchFile(t *testing.T) {
	xdg := t.TempDir()
	get := func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] }
	writeFile(t, filepath.Join(xdg, "fugaro", "projects", "borealis.yaml"), sample)
	_, _, err := LoadProject(get, "borealis")
	if err == nil || !strings.Contains(err.Error(), "aurora") || !strings.Contains(err.Error(), "borealis") {
		t.Fatalf("err = %v, want one naming both", err)
	}
}

func TestProjectsListsYAMLOnly(t *testing.T) {
	xdg := t.TempDir()
	get := func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] }
	if names, err := Projects(get); err != nil || len(names) != 0 {
		t.Fatalf("no directory: %v, %v", names, err)
	}
	// A projects path that can't be listed is an error, not "none".
	notDir := t.TempDir()
	writeFile(t, filepath.Join(notDir, "fugaro", "projects"), "")
	if names, err := Projects(func(k string) string { return map[string]string{"XDG_CONFIG_HOME": notDir}[k] }); err == nil {
		t.Fatalf("projects is a file: %v, nil", names)
	}
	dir := filepath.Join(xdg, "fugaro", "projects")
	for _, f := range []string{"borealis.yaml", "aurora.yaml", "aurora.yaml.bak", "aurora.yaml.bak-20260930T000000Z", "notes.txt", "Bad_Name.yaml"} {
		writeFile(t, filepath.Join(dir, f), sample)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(xdg, "fugaro", "config.yaml"), sample)
	// A symlink to a project config is one; to a directory or nowhere, not.
	elsewhere := filepath.Join(t.TempDir(), "cyan.yaml")
	writeFile(t, elsewhere, sample)
	for link, target := range map[string]string{"cyan.yaml": elsewhere, "dirlink.yaml": t.TempDir(), "dangling.yaml": filepath.Join(t.TempDir(), "gone")} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	names, err := Projects(get)
	if err != nil || strings.Join(names, ",") != "aurora,borealis,cyan" {
		t.Fatalf("Projects = %v, %v", names, err)
	}
}

// --gcp-project names the GCP project a project config already names: it
// can't point the config at another one.
func TestOverrideGCPProjectMustAgree(t *testing.T) {
	c, _ := Parse([]byte(sample))
	err := c.Override("other-project", "")
	if err == nil || !strings.Contains(err.Error(), "other-project") || !strings.Contains(err.Error(), "my-project") || c.GCPProject != "my-project" {
		t.Fatalf("another GCP project: err = %v, config %s", err, c.GCPProject)
	}
	if err := c.Override("my-project", "us-east5"); err != nil || c.Region != "us-east5" {
		t.Fatalf("the same GCP project: %v, region %s", err, c.Region)
	}
	if err := c.Override("", ""); err != nil {
		t.Fatalf("no flags: %v", err)
	}
}

func TestMe(t *testing.T) {
	testutil.IsolateGit(t)
	// IsolateGit points GIT_CONFIG_GLOBAL at os.DevNull, which git cannot
	// take a write lock on (it needs to create a sibling .lock file). Point
	// it at a writable temp file instead so this test's own
	// `git config --global` below can succeed; this keeps the developer's
	// real global config out of the test, same as IsolateGit intends.
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	c, _ := Parse([]byte(sample + "user: someone@example.com\n"))
	if me, err := c.Me(context.Background()); err != nil || me != "someone@example.com" {
		t.Fatalf("Me = %q, %v", me, err)
	}
	c, _ = Parse([]byte(sample))
	testutil.Git(t, "", "config", "--global", "user.email", "git@example.com")
	if me, err := c.Me(context.Background()); err != nil || me != "git@example.com" {
		t.Fatalf("Me from git = %q, %v", me, err)
	}
}

// With no_auth unset the CLI sends the operator's ADC bearer token to each
// endpoint, so only https, or plain http to this machine (fakes), is
// allowed.
func TestParseEndpoints(t *testing.T) {
	for _, ok := range []string{"https://run.example.com/", "http://127.0.0.1:8080/", "http://localhost:9/", "http://[::1]:9/"} {
		if _, err := Parse([]byte(sample + "endpoints: { run: \"" + ok + "\" }\n")); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://run.example.com/", "http://127.0.0.1.evil.example/", "ftp://127.0.0.1/", "https://user:pw@run.example.com/", "run.example.com", "https:///x"} {
		if _, err := Parse([]byte(sample + "endpoints: { logging: \"" + bad + "\" }\n")); err == nil || !strings.Contains(err.Error(), "endpoints.logging") {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
	// The endpoints only fugaro update uses, and endpoints.batch and
	// endpoints.compute -- the Batch API root the opener hands to
	// internal/backend/batch, whose clientOptions attaches that same token to
	// it (internal/cli/cloud.go's cloudOpeners), and the Compute Engine root
	// `fugaro doctor` reads a Batch installation's facts through, which
	// doctorAPIOpts attaches it to as well -- are checked the same way. One
	// field is set per iteration, so the sum is that field's own value.
	for _, field := range []string{"batch", "compute", "storage", "iam", "artifact_registry", "resource_manager", "cloud_scheduler", "service_usage"} {
		c, err := Parse([]byte(sample + "endpoints: { " + field + ": \"http://127.0.0.1:9/\" }\n"))
		if err != nil {
			t.Errorf("%s: %v", field, err)
		} else if e := c.Endpoints; e.Batch+e.Compute+e.Storage+e.IAM+e.ArtifactRegistry+e.ResourceManager+e.CloudScheduler+e.ServiceUsage != "http://127.0.0.1:9/" {
			t.Errorf("%s: endpoints = %+v", field, e)
		}
		if _, err := Parse([]byte(sample + "endpoints: { " + field + ": \"http://example.com/\" }\n")); err == nil || !strings.Contains(err.Error(), "endpoints."+field) {
			t.Errorf("%s: plain http elsewhere: err = %v", field, err)
		}
	}
}

// The Firestore endpoints are checked like identity_toolkit.
func TestParseFirestoreEndpoints(t *testing.T) {
	for _, field := range []string{"firestore", "firebase_rules", "identity_toolkit", "cloud_asset"} {
		if _, err := Parse([]byte(sample + "endpoints: { " + field + ": \"http://127.0.0.1:9\" }\n")); err != nil {
			t.Errorf("%s: %v", field, err)
		}
		for _, bad := range []string{"http://example.com/", "https://u:p@example.com/", "firestore.googleapis.com"} {
			if _, err := Parse([]byte(sample + "endpoints: { " + field + ": \"" + bad + "\" }\n")); err == nil || !strings.Contains(err.Error(), "endpoints."+field) {
				t.Errorf("%s %s: err = %v", field, bad, err)
			}
		}
	}
}

// --project and --region go into resource paths: they are validated like
// the config's own values.
func TestOverrideValidates(t *testing.T) {
	for _, tc := range [][2]string{{"x/../y", ""}, {"", "us-east5/../x"}, {"My_Project", ""}} {
		c, _ := Parse([]byte(sample))
		if err := c.Override(tc[0], tc[1]); err == nil {
			t.Errorf("Override(%q, %q) accepted", tc[0], tc[1])
		}
	}
}

// A price override for a region replaces the list price there, and says so;
// other regions keep the list price.
func TestPricesOverride(t *testing.T) {
	c, err := Parse([]byte(sample + "compute_prices:\n  us-east5: { vcpu_second_usd: 0.00001, gib_second_usd: 0.000001 }\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := c.PriceOverride("us-east5")
	if !ok || got.VCPUSecondUSD != 0.00001 || got.GiBSecondUSD != 0.000001 || got.Source != "local override" {
		t.Fatalf("us-east5 = %+v, %v", got, ok)
	}
	if got, ok := c.PriceOverride("us-central1"); ok {
		t.Fatalf("us-central1 = %+v, want no override (the list price)", got)
	}
}

func TestPricesValidation(t *testing.T) {
	ok := "0.00001"
	for name, tc := range map[string]struct{ vcpu, gib, region, path string }{
		"zero":     {"0", ok, "us-east5", "compute_prices.us-east5.vcpu_second_usd"},
		"negative": {ok, "-0.00001", "us-east5", "compute_prices.us-east5.gib_second_usd"},
		"nan":      {".nan", ok, "us-east5", "compute_prices.us-east5.vcpu_second_usd"},
		"infinite": {ok, ".inf", "us-east5", "compute_prices.us-east5.gib_second_usd"},
		"too high": {"1.0", ok, "us-east5", "compute_prices.us-east5.vcpu_second_usd"},
		"unset":    {ok, "", "us-east5", "compute_prices.us-east5.gib_second_usd"},
		"region":   {ok, ok, "Mars", "compute_prices: \"Mars\""},
	} {
		t.Run(name, func(t *testing.T) {
			entry := "vcpu_second_usd: " + tc.vcpu
			if tc.gib != "" {
				entry += ", gib_second_usd: " + tc.gib
			}
			_, err := Parse([]byte(sample + "compute_prices:\n  " + tc.region + ": { " + entry + " }\n"))
			if err == nil || !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("err = %v, want one naming %s", err, tc.path)
			}
		})
	}
}

// build.service_account is no longer how builds run: it still parses, so
// an older config keeps working, but it is reported.
func TestBuildServiceAccountDeprecated(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil || len(c.Warnings()) != 0 {
		t.Fatalf("plain config: warnings %v, err %v", c.Warnings(), err)
	}
	c, err = Parse([]byte(sample + "build: { service_account: fugaro-build@my-project.iam.gserviceaccount.com }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if w := c.Warnings(); len(w) != 1 || !strings.Contains(w[0], "build.service_account") || !strings.Contains(w[0], "deprecated") {
		t.Fatalf("warnings = %v", w)
	}
}

// vertex records that a repository's agent authenticates through Vertex
// AI, so fugaro update enables the API.
func TestRepoVertex(t *testing.T) {
	c, err := Parse([]byte(sample + "  acme/api: { provider: github, vertex: true, workflows: [web] }\n"))
	if err != nil || !c.Repos["acme/api"].Vertex {
		t.Fatalf("%+v, %v", c, err)
	}
	if !c.UsesVertex() {
		t.Error("a vertex repository: UsesVertex is false")
	}
	c, err = Parse([]byte(sample))
	if err != nil || c.UsesVertex() {
		t.Fatalf("no vertex repository: %v, %v", c.UsesVertex(), err)
	}
}

func TestGitHubAppIDValidation(t *testing.T) {
	for _, id := range []string{"1", "123456", "12345678901234567890"} {
		c, err := Parse([]byte(sample + "  acme/api: { provider: github, github_app_id: \"" + id + "\", workflows: [web] }\n"))
		if err != nil || c.Repos["acme/api"].GitHubAppID != id {
			t.Errorf("%s: %+v, %v", id, c, err)
		}
	}
	for _, id := range []string{"", "abc", "12 3", "-1", "123456789012345678901", "0x1f"} {
		_, err := Parse([]byte(sample + "  acme/api: { provider: github, github_app_id: \"" + id + "\", workflows: [web] }\n"))
		if id == "" {
			if err != nil {
				t.Errorf("empty (unset) refused: %v", err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "repos.acme/api: github_app_id") {
			t.Errorf("%q: err = %v", id, err)
		}
	}
}

func TestRegistryHostValidation(t *testing.T) {
	c, err := Parse([]byte(sample + "registry_host: us-central1-docker.pkg.dev/my-project\n"))
	if err != nil || c.RegistryHost != "us-central1-docker.pkg.dev/my-project" {
		t.Fatalf("good host: %+v, %v", c, err)
	}
	for name, host := range map[string]string{
		"other project": "us-central1-docker.pkg.dev/other-project",
		"with registry": "us-central1-docker.pkg.dev/my-project/fugaro",
		"not docker":    "us-central1-pkg.dev/my-project",
		"no region":     "docker.pkg.dev/my-project",
		"scheme":        "https://us-central1-docker.pkg.dev/my-project",
	} {
		if _, err := Parse([]byte(sample + "registry_host: " + host + "\n")); err == nil || !strings.Contains(err.Error(), "registry_host") {
			t.Errorf("%s (%s): err = %v", name, host, err)
		}
	}
}

// A config that sets no backend defaults to Cloud Run (design
// m11-setup-and-skills.md §7: no second backend yet, so only that name
// validates).
func TestBackendDefaultsToCloudRun(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil || c.Backend() != backend.CloudRun {
		t.Fatalf("Backend() = %q, %v, want %q", c.Backend(), err, backend.CloudRun)
	}
	c, err = Parse([]byte(sample + "backend: cloud-run\n"))
	if err != nil || c.Backend() != backend.CloudRun {
		t.Fatalf("explicit cloud-run: %q, %v", c.Backend(), err)
	}
	if _, err := Parse([]byte(sample + "backend: ecs-fargate\n")); err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("err = %v, want it to name backend", err)
	}
}

// Cloud Batch is a backend this build can speak but must not select yet: the
// seam and the fake exist (design compute-backend.md §8.1 Task 2), while the
// tasks that make a Batch run safe -- the log sink, the cost of a VM and the
// reaper that stops a leaked one -- come after the owner's Check 57a
// measurement (design §9). So `backend: cloud-batch` stays refused, and this
// is the single place that refuses it: every command that reads a project
// config, `fugaro update` included, goes through Parse.
func TestCloudBatchIsRefused(t *testing.T) {
	if backend.CloudBatch != "cloud-batch" {
		t.Fatalf("backend.CloudBatch = %q: the constant and the config value must not drift", backend.CloudBatch)
	}
	c, err := Parse([]byte(sample + "backend: " + backend.CloudBatch + "\n"))
	if err == nil || !strings.Contains(err.Error(), backend.CloudBatch) {
		t.Fatalf("err = %v, want it to name %s", err, backend.CloudBatch)
	}
	// It is refused as a name this build knows and has not enabled, not as
	// one it has never heard of: the two are different claims and only the
	// first is true (design §8.1 Task 2).
	if !strings.Contains(err.Error(), "not enabled yet") || strings.Contains(err.Error(), "not a known backend") {
		t.Fatalf("err = %q, want the refusal of a known backend that is not enabled", err)
	}
	// A name this build does not know at all is refused as such.
	if _, err := Parse([]byte(sample + "backend: ecs-fargate\n")); err == nil || !strings.Contains(err.Error(), "not a known backend") {
		t.Fatalf("err = %v, want an unknown backend", err)
	}
	// The refusal names the only backend this build runs, so the fix is in
	// the message; and nothing rewrites the name on the way out, so a caller
	// that ignored the error still sees what the operator asked for rather
	// than a silent fallback to Cloud Run.
	if c != nil && c.BackendName != backend.CloudBatch {
		t.Fatalf("a refused config rewrote its backend to %q", c.BackendName)
	}
}

// The batch block is what the Cloud Batch backend reads (plan
// 2026-10-19-compute-backend.md Task 8 step 2): the installation facts a run's
// VM and job are built and placed from, which internal/cli/cloud.go's Batch
// opener turns into internal/backend/batch.Options. Nothing selects that
// opener — `backend: cloud-batch` is refused above — so this block changes
// nothing about what runs; it is what the seam's second implementation would
// be opened from, and it is validated where it is written.
const batchSample = "batch:\n" +
	"  image: us-central1-docker.pkg.dev/my-project/fugaro/fugaro:1.2\n" +
	"  machine_type: e2-standard-4\n" +
	"  boot_image: projects/my-project/global/images/family/fugaro-base\n" +
	"  network: projects/my-project/global/networks/fugaro\n" +
	"  subnetwork: projects/my-project/regions/us-central1/subnetworks/fugaro\n" +
	"  service_account: fugaro-runs@my-project.iam.gserviceaccount.com\n" +
	"  secrets:\n" +
	"    ANTHROPIC_API_KEY: projects/my-project/secrets/anthropic-api-key\n" +
	"    GITHUB_TOKEN: projects/my-project/secrets/github-token/versions/7\n" +
	"  socket_gid: 412\n" +
	"  task_timeout: 1h30m\n"

// TestBatchSectionReadsEveryField: each field of the block reaches the config
// as written, because a field the opener cannot read is a run launched with a
// default rather than with the installation's fact.
func TestBatchSectionReadsEveryField(t *testing.T) {
	c, err := Parse([]byte(sample + batchSample))
	if err != nil {
		t.Fatal(err)
	}
	b := c.Batch
	if b == nil {
		t.Fatal("the batch block was not read")
	}
	for _, f := range []struct{ name, got, want string }{
		{"image", b.Image, "us-central1-docker.pkg.dev/my-project/fugaro/fugaro:1.2"},
		{"machine_type", b.MachineType, "e2-standard-4"},
		{"boot_image", b.BootImage, "projects/my-project/global/images/family/fugaro-base"},
		{"network", b.Network, "projects/my-project/global/networks/fugaro"},
		{"subnetwork", b.Subnetwork, "projects/my-project/regions/us-central1/subnetworks/fugaro"},
		{"service_account", b.ServiceAccount, "fugaro-runs@my-project.iam.gserviceaccount.com"},
	} {
		if f.got != f.want {
			t.Errorf("batch.%s = %q, want %q", f.name, f.got, f.want)
		}
	}
	if b.SocketGID != 412 {
		t.Errorf("batch.socket_gid = %d, want 412 (the gid Check 57a read off the boot image)", b.SocketGID)
	}
	if want := map[string]string{
		"ANTHROPIC_API_KEY": "projects/my-project/secrets/anthropic-api-key",
		"GITHUB_TOKEN":      "projects/my-project/secrets/github-token/versions/7",
	}; !reflect.DeepEqual(b.Secrets, want) {
		t.Errorf("batch.secrets = %v, want %v", b.Secrets, want)
	}
	if d := c.BatchTaskTimeout(); d != 90*time.Minute {
		t.Errorf("BatchTaskTimeout() = %v, want the written 1h30m", d)
	}
	// The default is the same one Cloud Run's jobs get, so turning the backend
	// on does not silently change how long a run may take.
	plain, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Batch != nil {
		t.Error("a config with no batch block has one")
	}
	if d := plain.BatchTaskTimeout(); d != backend.MaxRunTime {
		t.Errorf("BatchTaskTimeout() with no block = %v, want backend.MaxRunTime (%v)", d, backend.MaxRunTime)
	}
	// Leaving the key out is how a config takes the default; writing 0 is not
	// (TestBatchSectionValidation below).
	if c, err := Parse([]byte(sample + strings.Replace(batchSample, "  task_timeout: 1h30m\n", "", 1))); err != nil {
		t.Fatal(err)
	} else if d := c.BatchTaskTimeout(); d != backend.MaxRunTime {
		t.Errorf("BatchTaskTimeout() with no task_timeout = %v, want the default", d)
	}
	// The ceiling is the largest timeout a config may write, and not a
	// refusal in disguise: at exactly backend.MaxRunTime the reaper's two
	// sources agree, so the boundary itself is accepted (the case above the
	// boundary is TestBatchSectionValidation's).
	if c, err := Parse([]byte(sample + strings.Replace(batchSample, "task_timeout: 1h30m", "task_timeout: "+backend.MaxRunTime.String(), 1))); err != nil {
		t.Errorf("a task_timeout of exactly backend.MaxRunTime (%v) was refused: %v", backend.MaxRunTime, err)
	} else if d := c.BatchTaskTimeout(); d != backend.MaxRunTime {
		t.Errorf("BatchTaskTimeout() = %v, want the written %v", d, backend.MaxRunTime)
	}
}

// TestBatchSectionValidation: every field is refused in the spelling the
// backend could not use, named by the key it was written under, and accepted
// in the spelling it could. The backend re-checks the same values where it is
// opened (internal/backend/batch's checkNetwork, checkDocker,
// checkServiceAccount); this is the copy that fails at parse, where the
// operator can still see which line to fix.
func TestBatchSectionValidation(t *testing.T) {
	replace := func(old, new string) string {
		s := strings.Replace(batchSample, old, new, 1)
		if s == batchSample {
			t.Fatalf("the test's own sample does not contain %q", old)
		}
		return sample + s
	}
	for name, tc := range map[string]struct{ yaml, msg string }{
		"no image":         {sample + strings.Replace(batchSample, "  image: us-central1-docker.pkg.dev/my-project/fugaro/fugaro:1.2\n", "", 1), "batch.image"},
		"untagged image":   {replace("fugaro:1.2", "fugaro"), "batch.image"},
		"spaced image":     {replace("fugaro:1.2", "fugaro 1.2"), "batch.image"},
		"cloud build type": {replace("e2-standard-4", "E2_HIGHCPU_8"), "batch.machine_type"},
		"bare boot image":  {replace("projects/my-project/global/images/family/fugaro-base", "fugaro-base"), "batch.boot_image"},
		"generation boot image": {
			replace("projects/my-project/global/images/family/fugaro-base", "projects/my-project/global/images/fugaro-base-v3"),
			"batch.boot_image",
		},
		"bare network": {replace("projects/my-project/global/networks/fugaro", "default"), "batch.network"},
		"other project network": {
			replace("projects/my-project/global/networks/fugaro", "projects/other/global/networks/fugaro"),
			"batch.network",
		},
		"bare subnetwork": {replace("projects/my-project/regions/us-central1/subnetworks/fugaro", "fugaro-subnet"), "batch.subnetwork"},
		"account id not an email": {
			replace("fugaro-runs@my-project.iam.gserviceaccount.com", "fugaro-runs"),
			"batch.service_account",
		},
		"negative gid": {replace("socket_gid: 412", "socket_gid: -1"), "batch.socket_gid"},
		// The secrets cases name the entry by position, never by its key: a
		// key that failed the environment-variable-name rule may itself be a
		// secret's value, so neither message repeats what it refused (the
		// assertions below hold both halves of that).
		"secret key with a dash": {
			replace("    ANTHROPIC_API_KEY:", "    ANTHROPIC-API-KEY:"),
			"batch.secrets: entry 1 of 2",
		},
		"secret value instead of a name": {
			replace("projects/my-project/secrets/anthropic-api-key", "sk-ant-not-a-secret-name"),
			"batch.secrets: entry 1 of 2",
		},
		"secret in another project's name shape": {
			replace("projects/my-project/secrets/anthropic-api-key", "my-project/anthropic-api-key"),
			"batch.secrets: entry 1 of 2",
		},
		"zero timeout":     {replace("task_timeout: 1h30m", "task_timeout: 0s"), "batch.task_timeout"},
		"negative timeout": {replace("task_timeout: 1h30m", "task_timeout: -5m"), "batch.task_timeout"},
		// A timeout past a run's own ceiling is refused, and this is the
		// reaper's requirement rather than a cap for its own sake: a run can
		// never be given more than backend.MaxRunTime (--total-timeout is
		// capped there), so a job sized longer is headroom no run can use --
		// while the reaper that runs from its own Cloud Run job's environment
		// has no config to read the number from and derives its bound from the
		// ceiling (internal/cli/reaper.go). The two would disagree about when
		// a live run has ended, and the disagreement deletes a running job.
		"timeout past a run's ceiling": {replace("task_timeout: 1h30m", "task_timeout: 30h"), "batch.task_timeout"},
		// An unparsable duration is refused by the decoder, which the two
		// spellings of a duration in this file share (config.Duration): the
		// message names the value and the syntax, not the key.
		"bad timeout": {replace("task_timeout: 1h30m", "task_timeout: soon"), "invalid duration \"soon\""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v, want it to name %s", err, tc.msg)
			}
		})
	}
	// Neither refusal may quote what it refused, on either side of the map: a
	// value written where a name belongs is the case this section exists to
	// catch, and a message that printed it would put the value in a log or a
	// terminal. The key slot needs this as much as the value slot -- an
	// environment variable name and an access key id can be spelled
	// identically, so a refused key may be the value (the same rule
	// internal/backend/batch/batch.go's checkSecrets follows).
	for _, tc := range []struct{ name, old, new, value string }{
		{"value in the name slot", "projects/my-project/secrets/anthropic-api-key", "sk-ant-not-a-secret-name", "sk-ant-not-a-secret-name"},
		{"value in the key slot", "    ANTHROPIC_API_KEY:", "    sk-ant-api03-EXAMPLE:", "sk-ant-api03-EXAMPLE"},
	} {
		err := func() error { _, err := Parse([]byte(replace(tc.old, tc.new))); return err }()
		if err == nil {
			t.Fatalf("%s: a secret's value was accepted", tc.name)
		}
		if strings.Contains(err.Error(), tc.value) {
			t.Errorf("%s: the refusal quotes the secret value: %v", tc.name, err)
		}
	}
	// A key that is spelled like a legal environment variable name is still
	// checked, and still accepted: nothing here can tell an access key id from
	// a variable's name when the two are spelled the same, which is the
	// limitation internal/backend/batch/batch.go's checkSecrets documents.
	// What the refusal above must never do is repeat it.
	if _, err := Parse([]byte(replace("    ANTHROPIC_API_KEY:", "    AKIAIOSFODNN7EXAMPLE:"))); err != nil {
		t.Errorf("a key spelled like an environment variable name is refused, and it may be one: %v", err)
	}
	// The refusal of a bad key does not name the other entry either: the
	// sample's GITHUB_TOKEN is a name here, so a message that mentioned it
	// would be quoting a key nobody refused.
	if _, err := Parse([]byte(replace("    ANTHROPIC_API_KEY:", "    sk-ant-api03-EXAMPLE:"))); err != nil &&
		strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("the key-slot refusal names another entry: %v", err)
	}
	// A missing gid is not a broken gid: 0 is the recorded state "this
	// installation has no socket", which the backend refuses a Docker run
	// against (internal/backend/batch's checkDocker).
	if _, err := Parse([]byte(replace("socket_gid: 412", "socket_gid: 0"))); err != nil {
		t.Errorf("socket_gid: 0 is refused: %v", err)
	}
}

// TestBatchSectionDoesNotEnableTheBackend: the block is what the opener would
// read, not what selects it. `backend: cloud-batch` stays refused with the
// block present, so an installation that writes the whole section still runs
// on Cloud Run and TestCloudBatchIsRefused above stays the single place that
// decides it.
func TestBatchSectionDoesNotEnableTheBackend(t *testing.T) {
	_, err := Parse([]byte(sample + batchSample + "backend: " + backend.CloudBatch + "\n"))
	if err == nil || !strings.Contains(err.Error(), "not enabled yet") {
		t.Fatalf("err = %v, want the block to leave the backend refused as not enabled yet", err)
	}
	c, err := Parse([]byte(sample + batchSample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend() != backend.CloudRun {
		t.Fatalf("a config with a batch block selects %q, want %s", c.Backend(), backend.CloudRun)
	}
	// And the block must not be a way to smuggle a second selection path in:
	// there is one key that names a backend, and it is `backend`. A `backend`
	// written *under* batch: is an unknown field of the block.
	if _, err := Parse([]byte(sample + batchSample + "  backend: cloud-batch\n")); err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("err = %v, want batch.backend refused as an unknown field of the block", err)
	}
}

// The other fields fugaro update records: each is checked for shape.
func TestM5FieldsValidation(t *testing.T) {
	const good = "terraform:\n  state_bucket: fugaro-tfstate-my-project\n  alert_email: ops@example.com\n" +
		"  launchers: [\"user:a@example.com\", \"group:devs@example.com\"]\n  operators: [\"serviceAccount:ci@my-project.iam.gserviceaccount.com\", \"domain:example.com\"]\n" +
		"log_view: projects/my-project/locations/global/buckets/fugaro/views/runs\nscheduler_region: us-east4\n"
	c, err := Parse([]byte(sample + good))
	if err != nil {
		t.Fatal(err)
	}
	if c.Terraform.StateBucket != "fugaro-tfstate-my-project" || c.Terraform.AlertEmail != "ops@example.com" ||
		len(c.Terraform.Launchers) != 2 || len(c.Terraform.Operators) != 2 ||
		c.LogView != "projects/my-project/locations/global/buckets/fugaro/views/runs" || c.SchedulerRegion != "us-east4" {
		t.Fatalf("parsed = %+v", c)
	}
	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if again, err := Parse(data); err != nil || again.Terraform.StateBucket != c.Terraform.StateBucket || again.LogView != c.LogView {
		t.Fatalf("round trip = %+v, %v", again, err)
	}
	for name, tc := range map[string]struct{ yaml, msg string }{
		"state bucket":     {"terraform: { state_bucket: Bad_Bucket }\n", "terraform.state_bucket"},
		"alert email":      {"terraform: { alert_email: nobody }\n", "terraform.alert_email"},
		"launcher":         {"terraform: { launchers: [\"a@example.com\"] }\n", "terraform.launchers"},
		"operator":         {"terraform: { operators: [\"owner:a@example.com\"] }\n", "terraform.operators"},
		"log view":         {"log_view: projects/my-project/buckets/fugaro/views/runs\n", "log_view"},
		"log view project": {"log_view: projects/My_Project/locations/global/buckets/fugaro/views/runs\n", "log_view"},
		"scheduler region": {"scheduler_region: us_east4\n", "scheduler_region"},
		"unknown field":    {"terraform: { bucket: x }\n", "bucket"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(sample + tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v, want ~%q", err, tc.msg)
			}
		})
	}
}

// An M4 config, without any of the new fields, marshals without them.
func TestM4ConfigMarshalsWithoutM5Fields(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"terraform", "compute_prices", "log_view", "scheduler_region", "registry_host", "github_app_id", "vertex"} {
		if strings.Contains(string(data), field) {
			t.Errorf("marshalled M4 config has %s:\n%s", field, data)
		}
	}
}

// Image builds record in the runs bucket, the only bucket a build account
// may write, whatever bucket_url says; with no runs_bucket, a gs://
// bucket_url names it, as fugaro update reads it.
func TestRecordBucketURL(t *testing.T) {
	for _, c := range []struct{ runs, bucketURL, want string }{
		{"fugaro-runs-x", "", "gs://fugaro-runs-x"},
		{"fugaro-runs-x", "gs://other-bucket", "gs://fugaro-runs-x"},
		{"fugaro-runs-x", "file:///tmp/runs", "gs://fugaro-runs-x"},
		{"", "gs://fugaro-runs-y", "gs://fugaro-runs-y"},
		{"", "file:///tmp/runs", ""},
	} {
		lc := &Config{RunsBucket: c.runs, Bucket: c.bucketURL}
		if got := lc.RecordBucketURL(); got != c.want {
			t.Errorf("runs_bucket %q, bucket_url %q: %q, want %q", c.runs, c.bucketURL, got, c.want)
		}
	}
}

func TestBudgetDefaultsOff(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.BudgetMode() != "off" || c.Budget != nil {
		t.Fatalf("no block: mode %q, budget %+v", c.BudgetMode(), c.Budget)
	}
	if o, err := c.Overrides(); err != nil || len(o) != 0 {
		t.Fatalf("overrides = %v, %v", o, err)
	}
	c, err = Parse([]byte(sample + "budget: { per_run_usd: 5 }\n"))
	if err != nil || c.BudgetMode() != "off" {
		t.Fatalf("empty mode: %q, %v", c.BudgetMode(), err)
	}
	for _, mode := range []string{"off", "observe", "enforce"} {
		c, err := Parse([]byte(sample + "budget: { mode: " + mode + ", per_run_usd: 5 }\n"))
		if err != nil || c.BudgetMode() != mode || c.Budget.PerRunUSD != 5 {
			t.Fatalf("%s: %q, %v", mode, c.BudgetMode(), err)
		}
	}
	// observe accounts only without a cap.
	if _, err := Parse([]byte(sample + "budget: { mode: observe }\n")); err != nil {
		t.Fatalf("observe without a cap: %v", err)
	}
}

// TestUpdateCheckEnabled pins all three states: absent (on), explicit
// true (on), explicit false (off). Only "false" being tested before left
// a flipped condition (`*c.UpdateCheck` without the `||`, or an inverted
// `!*c.UpdateCheck`) free to survive for the "explicit true" case.
//
// Mutation: changing UpdateCheckEnabled's body to `return c.UpdateCheck ==
// nil` (dropping `|| *c.UpdateCheck`) makes "explicit true" wrongly report
// disabled.
func TestUpdateCheckEnabled(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if !c.UpdateCheckEnabled() {
		t.Error("no update_check key: UpdateCheckEnabled() = false, want true")
	}
	c, err = Parse([]byte(sample + "update_check: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.UpdateCheckEnabled() {
		t.Error("update_check: true: UpdateCheckEnabled() = false, want true")
	}
	c, err = Parse([]byte(sample + "update_check: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.UpdateCheckEnabled() {
		t.Error("update_check: false: UpdateCheckEnabled() = true, want false")
	}
}

func TestBudgetValidation(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, msg string }{
		"unknown mode":          {"budget: { mode: strict, per_run_usd: 5 }\n", "budget.mode"},
		"enforce without cap":   {"budget: { mode: enforce }\n", "per_run_usd"},
		"enforce zero cap":      {"budget: { mode: enforce, per_run_usd: 0 }\n", "per_run_usd"},
		"negative cap":          {"budget: { mode: observe, per_run_usd: -1 }\n", "per_run_usd"},
		"NaN cap":               {"budget: { mode: enforce, per_run_usd: .nan }\n", "per_run_usd"},
		"infinite cap":          {"budget: { mode: enforce, per_run_usd: .inf }\n", "per_run_usd"},
		"cap rounds to nothing": {"budget: { mode: enforce, per_run_usd: 0.0000001 }\n", "rounds to nothing"},
		"cap over 100000":       {"budget: { mode: enforce, per_run_usd: 100000.01 }\n", "per_run_usd"},
		"unknown field":         {"budget: { mode: off, per_run: 5 }\n", "per_run"},
		"price without rates":   {"model_prices: { claude-sonnet-5-5: { cache_read: 0.1 } }\n", "input_per_m"},
		"negative price":        {"model_prices: { claude-sonnet-5-5: { input_per_m: -1, output_per_m: 10 } }\n", "claude-sonnet-5-5"},
		"alias key":             {"model_prices: { sonnet: { input_per_m: 1, output_per_m: 10 } }\n", "alias"},
		"unknown price field":   {"model_prices: { claude-sonnet-5-5: { input_per_m: 1, output_per_m: 10, tokens: 3 } }\n", "tokens"},
		"two keys one model":    {"model_prices: { claude-haiku-4-5: { input_per_m: 1, output_per_m: 5 }, 'claude-haiku-4-5@20251001': { input_per_m: 1, output_per_m: 5 } }\n", "both price"},
		"half a tier":           {"model_prices: { claude-x-1: { input_per_m: 1, output_per_m: 5, long_context: { above_input_tokens: 200000, input_per_m: 2 } } }\n", "long_context"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(sample + tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.msg)
			}
		})
	}
	if _, err := Parse([]byte(sample + "budget: { mode: enforce, per_run_usd: 100000 }\n")); err != nil {
		t.Fatalf("the largest cap: %v", err)
	}
}

func TestModelPricesDefaults(t *testing.T) {
	c, err := Parse([]byte(sample + `model_prices:
  claude-sonnet-5-5: { input_per_m: 3, output_per_m: 15 }
  claude-mine-1:
    input_per_m: 1
    output_per_m: 5
    cache_write_5m: 1.5
    cache_read: 0
    long_context: { above_input_tokens: 200000, input_per_m: 2, output_per_m: 8 }
`))
	if err != nil {
		t.Fatal(err)
	}
	o, err := c.Overrides()
	if err != nil || len(o) != 2 {
		t.Fatalf("overrides = %v, %v", o, err)
	}
	r := o["claude-sonnet-5-5"]
	if r.InputPerM != 3 || r.OutputPerM != 15 || r.CacheWrite5m != 1.25 || r.CacheWrite1h != 2 || r.CacheRead != 0.1 || r.WebSearchPer1k != 10 || r.LongContext != nil {
		t.Fatalf("defaults = %+v", r)
	}
	r = o["claude-mine-1"]
	if r.CacheWrite5m != 1.5 || r.CacheWrite1h != 2 || r.CacheRead != 0 || r.WebSearchPer1k != 10 {
		t.Fatalf("explicit values = %+v", r)
	}
	if lc := r.LongContext; lc == nil || lc.AboveInputTokens != 200000 || lc.InputPerM != 2 || lc.OutputPerM != 8 {
		t.Fatalf("long_context = %+v", r.LongContext)
	}
	// What the config says survives a write and a read.
	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatalf("round trip: %v\n%s", err, data)
	}
	if o2, err := back.Overrides(); err != nil || len(o2) != 2 || o2["claude-mine-1"].CacheRead != 0 {
		t.Fatalf("round trip overrides = %v, %v", o2, err)
	}
}

func TestBudgetTokensAndModelsValidation(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, msg string }{
		"negative tokens":   {"budget: { max_run_tokens: -1 }\n", "max_run_tokens"},
		"empty list":        {"budget: { allowed_models: [] }\n", "allowed_models"},
		"alias":             {"budget: { allowed_models: [sonnet] }\n", "alias"},
		"latest alias":      {"budget: { allowed_models: [claude-sonnet-latest] }\n", "alias"},
		"empty entry":       {"budget: { allowed_models: [claude-sonnet-5-5, ''] }\n", "allowed_models[1]"},
		"space in entry":    {"budget: { allowed_models: ['claude-sonnet-5-5 '] }\n", "whitespace"},
		"duplicate entry":   {"budget: { allowed_models: [claude-sonnet-5-5, claude-sonnet-5-5] }\n", "listed twice"},
		"comma in entry":    {"budget: { allowed_models: ['claude-a,claude-b'] }\n", "allowed_models[0]"},
		"bogus mode":        {"budget: { mode: ENFORCE }\n", "budget.mode"},
		"tokens not number": {"budget: { max_run_tokens: lots }\n", "lots"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(sample + tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.msg)
			}
		})
	}
	c, err := Parse([]byte(sample + "budget: { max_run_tokens: 500000, allowed_models: [claude-sonnet-5-5, claude-haiku-4-5@20251001] }\n"))
	if err != nil || c.Budget.MaxRunTokens != 500000 || len(c.Budget.AllowedModels) != 2 {
		t.Fatalf("valid: %+v, %v", c.Budget, err)
	}
	// The two keys hold with the dollar budget off.
	if c.BudgetMode() != BudgetOff {
		t.Errorf("mode = %q", c.BudgetMode())
	}
}

func TestBudgetRTDBURLValidated(t *testing.T) {
	for url, ok := range map[string]bool{
		"https://aurora-fp-default-rtdb.firebaseio.com": true,
		"http://aurora.firebaseio.com":                  false,
		"https://evil.example.com":                      false,
		"https://user@x.firebaseio.com":                 false,
		"http://127.0.0.1:9":                            false, // loopback only with no_auth
	} {
		c := &Config{Budget: &Budget{RTDBURL: url}}
		var problems []string
		c.validateBudget(func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) })
		if got := len(problems) == 0; got != ok {
			t.Errorf("%s: ok=%v, problems %v", url, got, problems)
		}
	}
	c := &Config{Budget: &Budget{RTDBURL: "http://127.0.0.1:9"}, Endpoints: Endpoints{NoAuth: true}}
	c.validateBudget(func(f string, a ...any) { t.Errorf("loopback with no_auth refused: "+f, a...) })
}

const firebaseBudget = `budget:
  mode: observe
  firebase_project: aurora-fp
  rtdb_url: https://aurora-fp-default-rtdb.firebaseio.com
  firebase_api_key: AIzaSyA0123456789abcdefghijklmnopqrstu
  token_signer: fugaro-token-signer@aurora-fp.iam.gserviceaccount.com
  unreachable_grace: 3m
`

func TestLocalConfigBudgetFirebaseKeys(t *testing.T) {
	c, err := Parse([]byte(sample + firebaseBudget + "terraform: { budget_admins: [\"user:boss@example.com\"] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	b := c.Budget
	if b.FirebaseProject != "aurora-fp" || b.RTDBURL != "https://aurora-fp-default-rtdb.firebaseio.com" ||
		b.FirebaseAPIKey != "AIzaSyA0123456789abcdefghijklmnopqrstu" || b.TokenSigner != "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com" ||
		b.Grace != 3*time.Minute || !slices.Equal(c.Terraform.BudgetAdmins, []string{"user:boss@example.com"}) {
		t.Fatalf("budget = %+v, admins %v", b, c.Terraform.BudgetAdmins)
	}
	// It writes back and reads again unchanged.
	out, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(out)
	if err != nil || !reflect.DeepEqual(again.Budget, c.Budget) || !slices.Equal(again.Terraform.BudgetAdmins, c.Terraform.BudgetAdmins) {
		t.Fatalf("round trip: %v\n%s\n%+v", err, out, again.Budget)
	}
	for name, body := range map[string]string{
		"unknown key":     "budget: { mode: observe, firebase_projct: x }\n",
		"project":         "budget: { firebase_project: NOT-A-PROJECT }\n",
		"api key":         "budget: { firebase_api_key: \"has space\" }\n",
		"signer":          "budget: { token_signer: someone@example.com }\n",
		"grace too small": "budget: { unreachable_grace: 1s }\n",
		"grace too large": "budget: { unreachable_grace: 24h }\n",
		"grace above 3m":  "budget: { unreachable_grace: 10m }\n",
		"heartbeat":       "budget: { heartbeat: 15s }\n",
		"admin domain":    "terraform: { budget_admins: [\"domain:example.com\"] }\n",
		"admin wildcard":  "terraform: { budget_admins: [\"user:*@example.com\"] }\n",
		"admin bare":      "terraform: { budget_admins: [boss@example.com] }\n",
	} {
		if _, err := Parse([]byte(sample + body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestBurnAlertConfigStrict(t *testing.T) {
	c, err := Parse([]byte(sample + "watch: { burn_alert_usd_per_hour: 12.5 }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a := c.BurnAlert(); a == nil || *a != 12_500_000 {
		t.Fatalf("alert = %v", a)
	}
	if c, err = Parse([]byte(sample + "watch: { burn_alert_usd_per_hour: 0 }\n")); err != nil || c.BurnAlert() == nil || *c.BurnAlert() != 0 {
		t.Fatalf("0 is a valid alert: %v %v", c.BurnAlert(), err)
	}
	if c, err = Parse([]byte(sample)); err != nil || c.BurnAlert() != nil {
		t.Fatalf("unset must stay nil: %v %v", c.BurnAlert(), err)
	}
	for name, y := range map[string]string{
		"negative": "watch: { burn_alert_usd_per_hour: -1 }\n",
		"nan":      "watch: { burn_alert_usd_per_hour: .nan }\n",
		"inf":      "watch: { burn_alert_usd_per_hour: .inf }\n",
		"huge":     "watch: { burn_alert_usd_per_hour: 1e9 }\n",
		"string":   "watch: { burn_alert_usd_per_hour: fast }\n",
		"unknown":  "watch: { burn_alert: 1 }\n",
	} {
		if _, err := Parse([]byte(sample + y)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestProviderDecodeStrict(t *testing.T) {
	base := "name: belong\ngcp_project: edge-devel-dimi\nregion: us-east5\nruns_bucket: belong-runs\nrepos: {}\n"
	good := base + `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    route_fee_pct: 5.5
    models: ["deepseek/*"]
    allow_data_to: [edgeappinc/fugarosandbox, dimipaun/fugaro]
`
	c, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if p := c.Providers["openrouter"]; p.Secret != "openrouter-api-key" || p.RouteFeePct != 5.5 || !p.AllowsData("dimipaun/fugaro") {
		t.Errorf("decoded provider = %+v", p)
	}
	if _, err := Parse([]byte(strings.Replace(good, "auth: bearer", "auth: bearer\n    api_key: sk-secret", 1))); err == nil {
		t.Error("an unknown provider field (api_key) was accepted")
	}
	if _, err := Parse([]byte(strings.Replace(good, "https://openrouter.ai/api", "http://openrouter.ai/api", 1))); err == nil || !strings.Contains(err.Error(), "providers.openrouter.base_url") {
		t.Errorf("plain http accepted or unnamed: %v", err)
	}
}

// TestProviderAllowDataToWrittenEmpty: through the operator's own door
// (`fugaro validate`, `fugaro update`): a provider whose allow_data_to was
// written with nothing under it -- an emptied block, a bare key, a `~`, or
// an explicit [] -- is refused at config-load time, because a null key
// decodes to the same nil slice as an absent one, which means every
// repository. Absent and ["*"] stay accepted (design §3a).
func TestProviderAllowDataToWrittenEmpty(t *testing.T) {
	base := "name: belong\ngcp_project: edge-devel-dimi\nregion: us-east5\nruns_bucket: belong-runs\nrepos: {}\n"
	head := base + `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
`
	for name, tc := range map[string]struct {
		tail    string
		anyRepo bool
		refused bool
	}{
		"absent":              {"", true, false},
		"wildcard":            {"    allow_data_to: [\"*\"]\n", true, false},
		"listed":              {"    allow_data_to: [acme/app]\n", false, false},
		"explicit empty list": {"    allow_data_to: []\n", false, true},
		"null":                {"    allow_data_to:\n", false, true},
		"tilde":               {"    allow_data_to: ~\n", false, true},
		"only comments":       {"    allow_data_to:\n      # - acme/app\n", false, true},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := Parse([]byte(head + tc.tail))
			if tc.refused {
				if err == nil {
					t.Fatalf("a written-but-empty allow_data_to was accepted (anyRepo %v)", c.Providers["openrouter"].AllowsData("acme/anything"))
				}
				for _, want := range []string{"providers.openrouter.allow_data_to", "ambiguous", "[\"*\"]"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("err = %v, want it to mention %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := c.Providers["openrouter"].AllowsData("acme/anything"); got != tc.anyRepo {
				t.Errorf("AllowsData(acme/anything) = %v, want %v", got, tc.anyRepo)
			}
		})
	}
}

func TestClaudeAccountsDecodeStrict(t *testing.T) {
	base := "name: belong\ngcp_project: edge-devel-dimi\nregion: us-east5\nruns_bucket: belong-runs\nrepos: {}\n"
	good := base + `claude_accounts:
  acme-personal:
    allow_data_to: [edgeappinc/fugarosandbox, dimipaun/fugaro]
`
	c, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if a := c.ClaudeAccounts["acme-personal"]; !a.AllowsData("dimipaun/fugaro") || a.AllowsData("someone/else") {
		t.Errorf("decoded claude account = %+v", a)
	}
	if _, err := Parse([]byte(strings.Replace(good, "allow_data_to:", "secret: claude-oauth-token-acme-personal\n    allow_data_to:", 1))); err == nil {
		t.Error("an unknown claude_accounts field (secret) was accepted")
	}
	// Validate's wiring: a reserved or malformed name is refused, not
	// silently dropped (config.ValidateClaudeAccounts's own rules are
	// tested in internal/config; this only proves the call is wired in).
	bad := base + "claude_accounts:\n  default: {}\n"
	if _, err := Parse([]byte(bad)); err == nil || !strings.Contains(err.Error(), "claude_accounts.default") {
		t.Errorf("a reserved claude account name was accepted: %v", err)
	}
}

// TestClaudeAccountsDuplicateKeyRefused: two claude_accounts entries with
// the same name must not let the second silently win (a bucket writer or a
// hand-edited file repeating a key to mean something different from what a
// reviewer sees once). yaml.v3 already refuses a repeated mapping key by
// default; this pins that behavior for claude_accounts specifically, the
// same way ParseShared's own sharedTopLevel checks a repeated key.
func TestClaudeAccountsDuplicateKeyRefused(t *testing.T) {
	base := "name: belong\ngcp_project: edge-devel-dimi\nregion: us-east5\nruns_bucket: belong-runs\nrepos: {}\n"
	dup := base + "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/one]\n  acme-personal:\n    allow_data_to: [acme/two]\n"
	if _, err := Parse([]byte(dup)); err == nil {
		t.Fatal("a repeated claude_accounts key was accepted")
	} else if !strings.Contains(err.Error(), "acme-personal") {
		t.Errorf("the refusal does not name the repeated key: %v", err)
	}
}

func TestParseOldBaseImageKey(t *testing.T) {
	ref := "us-east5-docker.pkg.dev/p/fugaro-base/fugaro-web-node:dev-1"
	_, err := Parse([]byte(sample + "base_image: " + ref + "\n"))
	if err == nil {
		t.Fatal("a leftover base_image was accepted")
	}
	for _, want := range []string{"`base_image: " + ref + "` is now `base_images: {base: " + ref + "}`", "one base image per base kind"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	// Through Load, the message names the file as well.
	path := filepath.Join(t.TempDir(), "p.yaml")
	if err := os.WriteFile(path, []byte(sample+"base_image: "+ref+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "base_images: {base: "+ref+"}") {
		t.Errorf("Load error = %v", err)
	}
}

func TestBaseImages(t *testing.T) {
	c, err := Parse([]byte(sample + "base_images: { base: reg/fugaro-base:1 }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseImage("base") != "reg/fugaro-base:1" || c.BaseImage("history") != "" {
		t.Errorf("BaseImage = %v", c.BaseImages)
	}
	for name, yaml := range map[string]string{
		"unknown kind": "base_images: { rust: reg/x:1 }\n",
		"empty ref":    "base_images: { base: \"\" }\n",
		"spaces":       "base_images: { base: \"reg/x:1 reg/y:2\" }\n",
	} {
		if _, err := Parse([]byte(sample + yaml)); err == nil || !strings.Contains(err.Error(), "base_images") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// TestBaseImagesLegacyKindNamesTheMigrationDoc: a base_images key that is
// not a base kind (go, java-services, web-node included: each was removed
// in 0.7.0, not just any unrecognized string) names config.MigrationDoc,
// the same as the analogous base: refusals in config.Validate (validate.go,
// layer.go).
func TestBaseImagesLegacyKindNamesTheMigrationDoc(t *testing.T) {
	if _, err := Parse([]byte(sample + "base_images: { web-node: reg/x:1 }\n")); err == nil || !strings.Contains(err.Error(), config.MigrationDoc) {
		t.Errorf("err = %v, want it to name %s", err, config.MigrationDoc)
	}
}

// TestLocalConfigRefusesLegacyBaseImageKeys: the refusal of a base_images
// key of a removed kind names the exact key (base_images.<kind>), the fix
// (fugaro image refresh) and config.MigrationDoc.
func TestLocalConfigRefusesLegacyBaseImageKeys(t *testing.T) {
	for _, kind := range []string{"go", "web-node", "java-services"} {
		_, err := Parse([]byte(sample + "base_images:\n  " + kind + ": us-east5-docker.pkg.dev/p/fugaro-base/fugaro-" + kind + ":0.6.0\n"))
		if err == nil || !strings.Contains(err.Error(), "base_images."+kind) || !strings.Contains(err.Error(), "fugaro image refresh") ||
			!strings.Contains(err.Error(), config.MigrationDoc) {
			t.Errorf("%s: err = %v", kind, err)
		}
	}
}

// TestLocalConfigRefusesUnknownBaseImageKey: a base_images key that was
// never a base at all (not one of config.LegacyBaseKinds) is refused as
// "unknown base kind", not with the 0.7.0 removal message, which would
// falsely claim the cut removed it.
func TestLocalConfigRefusesUnknownBaseImageKey(t *testing.T) {
	_, err := Parse([]byte(sample + "base_images:\n  rust: us-east5-docker.pkg.dev/p/fugaro-base/fugaro-rust:0.6.0\n"))
	if err == nil || !strings.Contains(err.Error(), "base_images.rust") || !strings.Contains(err.Error(), `unknown base kind "rust"`) ||
		strings.Contains(err.Error(), "removed in 0.7.0") || strings.Contains(err.Error(), config.MigrationDoc) {
		t.Errorf("err = %v", err)
	}
}

func TestParseBaseImageFlag(t *testing.T) {
	for in, want := range map[string][2]string{
		"us-east5-docker.pkg.dev/p/fugaro-base/fugaro-base:dev-abc":      {"base", "us-east5-docker.pkg.dev/p/fugaro-base/fugaro-base:dev-abc"},
		"ghcr.io/dimipaun/fugaro-base@sha256:" + strings.Repeat("a", 64): {"base", "ghcr.io/dimipaun/fugaro-base@sha256:" + strings.Repeat("a", 64)},
		"base=reg/my-own-image:3":                                        {"base", "reg/my-own-image:3"}, // an explicit kind beats the name
	} {
		kind, ref, err := ParseBaseImageFlag(in)
		if err != nil || kind != want[0] || ref != want[1] {
			t.Errorf("%q = %q, %q, %v; want %v", in, kind, ref, err, want)
		}
	}
	for _, bad := range []string{"reg/some-image:1", "rust=reg/x:1", "base=", "reg/fugaro-rust:1", "go=reg/x:1"} {
		if _, _, err := ParseBaseImageFlag(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestBaseImageNameKind(t *testing.T) {
	for ref, want := range map[string]string{
		"ghcr.io/dimipaun/fugaro-base:1": "base", "reg/fugaro-base/fugaro-base:dev-a": "base",
		"reg/my-image:1": "", "reg/fugaro-rust:1": "", "ghcr.io/dimipaun/fugaro-go:1": "",
	} {
		if got := BaseImageNameKind(ref); got != want {
			t.Errorf("%s: kind %q, want %q", ref, got, want)
		}
	}
}

// A config with the project-creation endpoint and adopt mode's installation
// switches loads, and one of an older init (neither) still does.
func TestConfigWithEndpointAndAdoptKeysLoads(t *testing.T) {
	base := "version: 1\nname: belong\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\n"
	lc, err := Parse([]byte(base + "endpoints: { firebase_management: http://127.0.0.1:1/ }\nterraform: { registry_cleanup: on, no_log_isolation: true, budget_backend: true }\n"))
	if err != nil || lc.Endpoints.FirebaseManagement == "" || lc.Terraform.RegistryCleanup != "on" || !lc.Terraform.NoLogIsolation || !lc.Terraform.BudgetBackend {
		t.Fatalf("%+v, %v", lc, err)
	}
	if _, err := Parse([]byte(base)); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]byte(base + "terraform: { registry_cleanup: sometimes }\n")); err == nil {
		t.Error("a bad cleanup mode was accepted")
	}
}

// TestRepoBaseBranchValidated: a repo's base_branch reaches git, Cloud
// Build and `fugaro run`'s ref, so even a local config refuses a hostile
// one, by the same rule as fugaro.yaml's git.base_branch.
func TestRepoBaseBranchValidated(t *testing.T) {
	for _, ok := range []string{"main", "release/1.2", "dev_x"} {
		if _, err := Parse([]byte(strings.Replace(sample, "base_branch: main", "base_branch: "+ok, 1))); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{`"--upload-pack=x"`, `"main\n"`, `"a..b"`, `"$(id)"`, `"x y"`, `"\e[31m"`, `".hidden"`, `"main.lock"`, `"/x"`} {
		_, err := Parse([]byte(strings.Replace(sample, "base_branch: main", "base_branch: "+bad, 1)))
		if err == nil || !strings.Contains(err.Error(), "base_branch") {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
	// No base_branch at all is fine (the checkout's is used).
	if _, err := Parse([]byte(strings.Replace(sample, "base_branch: main, ", "", 1))); err != nil {
		t.Errorf("no base_branch: %v", err)
	}
}

// TestBuildBlockValidated: build.region goes into Cloud Build resource
// paths and build.machine_type into the build request.
func TestBuildBlockValidated(t *testing.T) {
	for _, ok := range []string{"build: { region: us-east5 }", "build: { machine_type: E2_HIGHCPU_32 }", "build: { machine_type: e2-medium }"} {
		if _, err := Parse([]byte(sample + ok + "\n")); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []struct{ yaml, want string }{
		{`build: { region: "us-east5/../../../projects/evil-proj/locations/us-east5" }`, "build.region"},
		{`build: { region: "x?alt=json#" }`, "build.region"},
		{`build: { region: "US-EAST5" }`, "build.region"},
		{`build: { machine_type: "E2 HIGHCPU" }`, "build.machine_type"},
		{`build: { machine_type: "x/../y" }`, "build.machine_type"},
		{`build: { machine_type: "` + strings.Repeat("A", 41) + `" }`, "build.machine_type"},
	} {
		if _, err := Parse([]byte(sample + bad.yaml + "\n")); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%s: err = %v", bad.yaml, err)
		}
	}
}

// TestRoutingInTheLocalConfig: the operator's config is the first definition
// site of the routing switch, so a switch it cannot act on is refused at load
// — a glob (D1a: an explicit list, never a pattern), a via it does not know,
// and a route no provider entry claims (D1b: openrouter resolves to a provider
// or it does not resolve at all). A valid switch loads, and an absent one is
// direct.
func TestRoutingInTheLocalConfig(t *testing.T) {
	if c, err := Parse([]byte(sample)); err != nil || c.Routing != nil {
		t.Fatalf("an absent switch: c.Routing = %+v, err = %v; want nil and no error", c.Routing, err)
	}
	ok := sample + "routing:\n  claude:\n    via: direct\n"
	c, err := Parse([]byte(ok))
	if err != nil {
		t.Fatalf("via: direct: %v", err)
	}
	if c.Routing == nil || c.Routing.Claude.ViaValue() != config.RouteDirect || len(c.Routing.Claude.Models) != 0 {
		t.Fatalf("via: direct: routing = %+v", c.Routing)
	}
	// A block with no Claude entry loads and means nothing here — it names no
	// route, so this loader reads it as it reads an absent block — but it is not
	// a statement that the switch is off: the surface that would act on it is
	// `project policy publish`, which refuses to turn an active node route off
	// unless the operator says so (the review of PR #414, and
	// TestProjectPolicyPublishRefusesARoutingBlockThatSaysNothing is where that
	// half lives: this package holds no policy node).
	for _, tc := range []struct{ name, block string }{
		{"an empty block", "routing: {}\n"},
		{"a claude with no value", "routing:\n  claude:\n"},
	} {
		c, err := Parse([]byte(sample + tc.block))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if c.Routing == nil || c.Routing.Claude != nil {
			t.Errorf("%s: routing = %+v, want a block that names no Claude switch", tc.name, c.Routing)
		}
	}
	for _, tc := range []struct{ name, block, msg string }{
		{"a glob", "routing:\n  claude:\n    via: openrouter\n    models: [claude-*]\n", "explicit list"},
		{"an unknown via", "routing:\n  claude:\n    via: somewhere-else\n    models: [claude-sonnet-5-5]\n", "routing.claude.via"},
		{"an empty list", "routing:\n  claude:\n    via: openrouter\n", "routing.claude.models"},
		{"an unclaimed route", "routing:\n  claude:\n    via: openrouter\n    models: [claude-sonnet-5-5]\n", "no provider entry claims it"},
	} {
		if _, err := Parse([]byte(sample + tc.block)); err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: err = %v, want a refusal naming %q", tc.name, err, tc.msg)
		}
	}

	// The positive case, and the reason the refusals above mean anything: a
	// config that *enables* the switch loads, from the file, and the one
	// resolver takes the model to the route that claims it. Without this case a
	// rule that refuses everything would pass, and "openrouter can never be
	// turned on" would be true of this package by accident (the security review
	// of this slice asked for exactly this test).
	claim := "providers:\n" +
		"  openrouter-claude:\n" +
		"    kind: anthropic-compat\n" +
		"    base_url: https://openrouter.ai/api/v1\n" +
		"    auth: bearer\n" +
		"    secret: openrouter-claude-key\n" +
		"    models: [\"anthropic/claude-sonnet-5.5\"]\n" +
		"    allow_data_to: [\"*\"]\n"
	routed, err := Parse([]byte(sample + claim + "routing:\n  claude:\n    via: openrouter\n    models: [claude-sonnet-5-5]\n"))
	if err != nil {
		t.Fatalf("a config that enables the switch was refused: %v", err)
	}
	route, sent, resolved := config.RoutingClaudeFor(routed.Routing.Claude, "claude-sonnet-5-5", routed.Providers)
	if !resolved || route != "openrouter-claude" || sent != "anthropic/claude-sonnet-5.5" {
		t.Errorf("RoutingClaudeFor = (%q, %q, %v), want the route entry's name and its own ID", route, sent, resolved)
	}
	// The same config with the model left out of the list is direct: the list
	// is the opt-in, not the route.
	if route, sent, resolved := config.RoutingClaudeFor(routed.Routing.Claude, "claude-opus-5-5", routed.Providers); !resolved || route != config.RouteDirect || sent != "claude-opus-5-5" {
		t.Errorf("an unlisted model = (%q, %q, %v), want the direct route", route, sent, resolved)
	}
	// And the claim without allow_data_to written is refused at load (D1b):
	// the same file, minus the line that makes the route open to a repository.
	bare := strings.Replace(claim, "    allow_data_to: [\"*\"]\n", "", 1)
	if _, err := Parse([]byte(sample + bare + "routing:\n  claude:\n    via: openrouter\n    models: [claude-sonnet-5-5]\n")); err == nil || !strings.Contains(err.Error(), "allow_data_to") {
		t.Errorf("a claim with no allow_data_to: err = %v, want a refusal naming allow_data_to", err)
	}
}

// TestProjectBaseBranchValidated: the installation config's top-level
// base_branch is the project's default base branch, a sibling of repos:, so
// it reaches git, Cloud Build and a run's ref exactly as a repo's does and
// is held to the same rule. The refusal names the layer the value came
// from, because the same word is a per-repo key one level down.
func TestProjectBaseBranchValidated(t *testing.T) {
	c, err := Parse([]byte(sample + "base_branch: trunk\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseBranch != "trunk" {
		t.Errorf("BaseBranch = %q, want trunk", c.BaseBranch)
	}
	for _, ok := range []string{"main", "release/1.2", "dev_x"} {
		if _, err := Parse([]byte(sample + "base_branch: " + ok + "\n")); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{`"--upload-pack=x"`, `"main\n"`, `"a..b"`, `"$(id)"`, `"x y"`, `"/x"`} {
		_, err := Parse([]byte(sample + "base_branch: " + bad + "\n"))
		if err == nil || !strings.Contains(err.Error(), "the project's default base branch") {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
	// No project base_branch at all is fine: the checkout's or main is used.
	if c, err := Parse([]byte(sample)); err != nil || c.BaseBranch != "" {
		t.Errorf("no base_branch: %v, %q", err, c.BaseBranch)
	}
}

// TestBitbucketAPIRootsParse: the owner's Bitbucket API-root allow-list is a
// local config key (D474), and this file's decoder is strict — a key it does
// not know refuses the whole document — so the list is unreadable until the
// field exists. It is carried as it is written, and it is dropped from the
// published shared config: the rule a root is held to is the node's
// (projectpolicy.ValidBitbucketAPIRoots), applied where the node is built
// (the publish calls projectpolicy.Decode through checkPolicyBytes), so there
// is no second copy of it here to drift from that one.
func TestBitbucketAPIRootsParse(t *testing.T) {
	c, err := Parse([]byte(sample + "bitbucket_api_roots: [https://api.bitbucket.example/2.0]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://api.bitbucket.example/2.0"}; !slices.Equal(c.BitbucketAPIRoots, want) {
		t.Errorf("BitbucketAPIRoots = %v, want %v", c.BitbucketAPIRoots, want)
	}
	if none, err := Parse([]byte(sample)); err != nil {
		t.Fatal(err)
	} else if none.BitbucketAPIRoots != nil {
		t.Errorf("a config that lists no root carries %v", none.BitbucketAPIRoots)
	}
	if s := c.Shared(); s.BitbucketAPIRoots != nil {
		t.Errorf("Shared() carries bitbucket_api_roots = %v, and it is published through the policy node instead", s.BitbucketAPIRoots)
	}
	if c.BitbucketAPIRoots == nil {
		t.Error("Shared() cleared the receiver's own list")
	}
}

// TestSecretRotationRecordRefused: the operator's rotation record — the period
// doctor measures against and the dates it reports — is refused at load rather
// than at doctor. A period or a date this file cannot use would turn into a
// check that silently says nothing: doctor reads a malformed date as a name it
// has no fact about, so the secret the operator recorded would drop out of the
// sweep without a word. Whether the name exists is doctor's to find; the shape
// is this document's, beside the other shape rules, and the refusal names the
// entry it read.
func TestSecretRotationRecordRefused(t *testing.T) {
	c, err := Parse([]byte(sample + "secret_rotation_days: 30\nsecret_expires:\n  bitbucket-token: \"2026-11-05\"\n"))
	if err != nil {
		t.Fatalf("the operator's own record was refused: %v", err)
	}
	if c.SecretRotationDays != 30 || c.SecretExpires["bitbucket-token"] != "2026-11-05" || len(c.SecretExpires) != 1 {
		t.Errorf("the record read as %d day(s) and %v", c.SecretRotationDays, c.SecretExpires)
	}
	// Zero is how the record says "no period", and it is what an absent key says
	// too: only a period doctor could measure against a negative age is refused.
	for _, ok := range []string{"secret_rotation_days: 0\n", ""} {
		c, err := Parse([]byte(sample + ok))
		if err != nil || c.SecretRotationDays != 0 {
			t.Errorf("%q: (%d, %v), want no period and no error", ok, c.SecretRotationDays, err)
		}
	}
	for _, tc := range []struct{ name, body, want string }{
		{"a negative period", "secret_rotation_days: -1\n", "secret_rotation_days must be zero or more days"},
		{"a period no age is outside", "secret_rotation_days: -30\n", "secret_rotation_days must be zero or more days"},
		{"a date in the operator's own spelling", "secret_expires:\n  bitbucket-token: \"05/11/2026\"\n", `secret_expires.bitbucket-token "05/11/2026" is not a date (YYYY-MM-DD)`},
		{"a date with a one-digit day", "secret_expires:\n  bitbucket-token: \"2026-11-5\"\n", "is not a date"},
		{"a date that does not exist", "secret_expires:\n  bitbucket-token: \"2026-02-30\"\n", "is not a date"},
		{"a date that is a number", "secret_expires:\n  bitbucket-token: 20261105\n", "is not a date"},
		{"a name with nothing in it", "secret_expires:\n  \"\": \"2026-11-05\"\n", "secret_expires has an entry with no secret name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Parse([]byte(sample + tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse = (%+v, %v), want an error naming %q", c, err, tc.want)
			}
		})
	}
}

// TestReviewFloorDecodeStrict is D722's owner half: the floor is a closed
// vocabulary twice over — the modes rigor names and the three levels — and
// both are refused at load, never dropped. A misspelt mode or level read as
// "no floor for that mode" would be a run that reviews less than the owner
// asked for, and nothing downstream can tell the misspelling from a mode the
// owner meant to leave alone.
func TestReviewFloorDecodeStrict(t *testing.T) {
	c, err := Parse([]byte(sample + "review_floor:\n  pro: [l2]\n  critical: [l2, l3]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]string{"pro": {"l2"}, "critical": {"l2", "l3"}}; !reflect.DeepEqual(c.ReviewFloor, want) {
		t.Fatalf("ReviewFloor = %v, want %v", c.ReviewFloor, want)
	}
	floor, err := c.ReviewFloorPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if want := map[rigor.Mode][]policy.Level{rigor.Pro: {policy.LevelL2}, rigor.Critical: {policy.LevelL2, policy.LevelL3}}; !reflect.DeepEqual(floor, want) {
		t.Errorf("ReviewFloorPolicy = %v, want %v", floor, want)
	}
	if none, err := Parse([]byte(sample)); err != nil {
		t.Fatal(err)
	} else if f, err := none.ReviewFloorPolicy(); err != nil || f != nil {
		t.Errorf("a config that sets no floor gives (%v, %v), want (nil, nil)", f, err)
	}

	// The key written with no mode under it is the other way to say "none",
	// and it is the spelling `fugaro project policy publish` reads as "take
	// the node's floor away on purpose" — an absent key keeps the node's floor
	// (internal/cli's buildProjectPolicy). The two differ in the field and not
	// in the floor: a written empty object is a non-nil empty map, an absent
	// key a nil one, and both parse to no floor.
	if empty, err := Parse([]byte(sample + "review_floor: {}\n")); err != nil {
		t.Fatal(err)
	} else if empty.ReviewFloor == nil {
		t.Error("a written empty object read as an absent key, so a publish cannot tell 'drop the floor' from silence about it")
	} else if f, err := empty.ReviewFloorPolicy(); err != nil || f != nil {
		t.Errorf("review_floor: {} gives (%v, %v), want (nil, nil)", f, err)
	}

	// Every name the vocabulary does not hold, and every shape of a floor that
	// says nothing, is a load error.
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"an unknown mode", "review_floor:\n  prod: [l2]\n", "is not a rigor mode"},
		{"a mode in another case", "review_floor:\n  Pro: [l2]\n", "is not a rigor mode"},
		{"an unknown level", "review_floor:\n  pro: [l4]\n", "is not a review level"},
		{"a level in another case", "review_floor:\n  pro: [L2]\n", "is not a review level"},
		{"a level that is a number", "review_floor:\n  pro: [2]\n", "is not a review level"},
		{"a repeated level", "review_floor:\n  pro: [l2, l2]\n", "is listed twice"},
		{"a mode with no level", "review_floor:\n  pro: []\n", "a floor with no level in it says nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Parse([]byte(sample + tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse = (%v, %v), want an error naming %q", c, err, tc.want)
			}
			// And the refusal is the loader's, not the node's: the same value
			// never reaches the publisher, which would otherwise write a floor
			// the runner then reads as a smaller one.
			if c != nil {
				if f, err := c.ReviewFloorPolicy(); err == nil {
					t.Errorf("ReviewFloorPolicy = (%v, nil), want the same refusal", f)
				}
			}
		})
	}
}

// TestReviewFloorNotInShared: the floor is the owner's, and it reaches a run
// through the policy node — which only the owner writes and the runner fails
// closed on — never through the shared config a launcher can write. Shared()
// drops it the way it drops update_check and base_branch, and MergeShared
// never takes it back from a published object: a floor in the runs bucket's
// shared file would be the launcher the floor exists to bind, setting it
// (docs/design/bucket-iam.md §10 L1, L2).
func TestReviewFloorNotInShared(t *testing.T) {
	c, err := Parse([]byte(sample + "review_floor:\n  pro: [l2]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := c.Shared(); s.ReviewFloor != nil {
		t.Errorf("Shared() carries review_floor = %v, and it is published through the policy node instead", s.ReviewFloor)
	}
	if c.ReviewFloor == nil {
		t.Error("Shared() cleared the receiver's own floor")
	}
	published, err := Parse([]byte(sample + "review_floor:\n  critical: [l3]\n"))
	if err != nil {
		t.Fatal(err)
	}
	// The published object is the other machine's: its floor is dropped by
	// Shared before the merge sees it, so it cannot come back either.
	if merged := MergeShared(published, c); merged.ReviewFloor != nil {
		t.Errorf("MergeShared carried review_floor = %v from the published object", merged.ReviewFloor)
	}
	if b, err := c.Shared().Marshal(); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(b), "review_floor") {
		t.Errorf("the published file names review_floor:\n%s", b)
	}
}

// TestLayerFileIsTheProjectNameUnderProjects: the one local layer file is the
// project's own name, with -layer.yaml, beside its config (D830) — the path is
// a convention and nothing records it.
func TestLayerFileIsTheProjectNameUnderProjects(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "xdg")}
	get := func(k string) string { return env[k] }
	p, err := LayerFile(get, "aurora")
	if err != nil || p != filepath.Join(dir, "xdg", "fugaro", "projects", "aurora-layer.yaml") {
		t.Fatalf("LayerFile = %q, %v", p, err)
	}
	// A name that is not a single path element would put the file, which
	// `fugaro update` writes as the owner's own layer, anywhere on the
	// machine — the same refusal ProjectPath makes.
	for _, name := range []string{"../x", "a/b", "", "Aurora", "-x", "x-", strings.Repeat("a", 41)} {
		if _, err := LayerFile(get, name); err == nil {
			t.Errorf("LayerFile accepted %q", name)
		}
	}
}
