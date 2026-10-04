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
	if _, _, err := LoadProject(get, "aurora"); !errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), "fugaro init") {
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
	// The endpoints only fugaro init uses are checked the same way.
	for _, field := range []string{"storage", "iam", "artifact_registry", "resource_manager", "cloud_scheduler", "service_usage"} {
		c, err := Parse([]byte(sample + "endpoints: { " + field + ": \"http://127.0.0.1:9/\" }\n"))
		if err != nil {
			t.Errorf("%s: %v", field, err)
		} else if e := c.Endpoints; e.Storage+e.IAM+e.ArtifactRegistry+e.ResourceManager+e.CloudScheduler+e.ServiceUsage != "http://127.0.0.1:9/" {
			t.Errorf("%s: endpoints = %+v", field, e)
		}
		if _, err := Parse([]byte(sample + "endpoints: { " + field + ": \"http://example.com/\" }\n")); err == nil || !strings.Contains(err.Error(), "endpoints."+field) {
			t.Errorf("%s: plain http elsewhere: err = %v", field, err)
		}
	}
}

// The Firestore endpoints are checked like identity_toolkit.
func TestParseFirestoreEndpoints(t *testing.T) {
	for _, field := range []string{"firestore", "firebase_rules", "identity_toolkit"} {
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
// AI, so fugaro init enables the API.
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

// The other fields fugaro init records: each is checked for shape.
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
// bucket_url names it, as fugaro init reads it.
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

func TestParseOldBaseImageKey(t *testing.T) {
	ref := "us-east5-docker.pkg.dev/p/fugaro-base/fugaro-web-node:dev-1"
	_, err := Parse([]byte(sample + "base_image: " + ref + "\n"))
	if err == nil {
		t.Fatal("a leftover base_image was accepted")
	}
	for _, want := range []string{"`base_image: " + ref + "` is now `base_images: {web-node: " + ref + "}`", "one base image per base kind"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	// Through Load, the message names the file as well.
	path := filepath.Join(t.TempDir(), "p.yaml")
	if err := os.WriteFile(path, []byte(sample+"base_image: "+ref+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "base_images: {web-node: "+ref+"}") {
		t.Errorf("Load error = %v", err)
	}
}

func TestBaseImages(t *testing.T) {
	c, err := Parse([]byte(sample + "base_images: { web-node: reg/fugaro-web-node:1, java-services: reg/fugaro-java-services:1 }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseImage("web-node") != "reg/fugaro-web-node:1" || c.BaseImage("java-services") != "reg/fugaro-java-services:1" || c.BaseImage("go") != "" {
		t.Errorf("BaseImage = %v", c.BaseImages)
	}
	for name, yaml := range map[string]string{
		"unknown kind": "base_images: { rust: reg/x:1 }\n",
		"empty ref":    "base_images: { go: \"\" }\n",
		"spaces":       "base_images: { go: \"reg/x:1 reg/y:2\" }\n",
	} {
		if _, err := Parse([]byte(sample + yaml)); err == nil || !strings.Contains(err.Error(), "base_images") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestParseBaseImageFlag(t *testing.T) {
	for in, want := range map[string][2]string{
		"us-east5-docker.pkg.dev/p/fugaro-base/fugaro-web-node:dev-abc": {"web-node", "us-east5-docker.pkg.dev/p/fugaro-base/fugaro-web-node:dev-abc"},
		"ghcr.io/dimipaun/fugaro-java-services:1":                       {"java-services", "ghcr.io/dimipaun/fugaro-java-services:1"},
		"ghcr.io/dimipaun/fugaro-go@sha256:" + strings.Repeat("a", 64):  {"go", "ghcr.io/dimipaun/fugaro-go@sha256:" + strings.Repeat("a", 64)},
		"go=reg/my-own-image:3":                                         {"go", "reg/my-own-image:3"},
		"java-services=reg/fugaro-web-node:1":                           {"java-services", "reg/fugaro-web-node:1"}, // an explicit kind beats the name
	} {
		kind, ref, err := ParseBaseImageFlag(in)
		if err != nil || kind != want[0] || ref != want[1] {
			t.Errorf("%q = %q, %q, %v; want %v", in, kind, ref, err, want)
		}
	}
	for _, bad := range []string{"reg/some-image:1", "rust=reg/x:1", "go=", "reg/fugaro-rust:1"} {
		if _, _, err := ParseBaseImageFlag(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestBaseImageNameKind(t *testing.T) {
	for ref, want := range map[string]string{
		"ghcr.io/dimipaun/fugaro-go:1": "go", "reg/fugaro-base/fugaro-java-services:dev-a": "java-services",
		"reg/my-image:1": "", "reg/fugaro-rust:1": "",
	} {
		if got := BaseImageNameKind(ref); got != want {
			t.Errorf("%s: kind %q, want %q", ref, got, want)
		}
	}
}
