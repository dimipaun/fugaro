package localcfg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	dir := filepath.Join(xdg, "fugaro", "projects")
	for _, f := range []string{"borealis.yaml", "aurora.yaml", "aurora.yaml.bak", "aurora.yaml.bak-20260930T000000Z", "notes.txt", "Bad_Name.yaml"} {
		writeFile(t, filepath.Join(dir, f), sample)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(xdg, "fugaro", "config.yaml"), sample)
	names, err := Projects(get)
	if err != nil || strings.Join(names, ",") != "aurora,borealis" {
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
