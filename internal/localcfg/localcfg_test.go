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
project: my-project
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
	if err := c.Override("other-project", "europe-west1"); err != nil {
		t.Fatal(err)
	}
	if c.Project != "other-project" || c.Region != "europe-west1" || c.BuildRegion() != "europe-west1" {
		t.Fatalf("override = %+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, msg string }{
		"unknown field": {sample + "colour: blue\n", "colour"},
		"bad project":   {strings.Replace(sample, "my-project\n", "My_Project\n", 1), "project"},
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

func TestPathAndLoad(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"HOME": dir}
	get := func(k string) string { return env[k] }
	p, err := Path(get)
	if err != nil || p != filepath.Join(dir, ".config", "fugaro", "config.yaml") {
		t.Fatalf("Path = %q, %v", p, err)
	}
	env["XDG_CONFIG_HOME"] = filepath.Join(dir, "xdg")
	if p, _ = Path(get); p != filepath.Join(dir, "xdg", "fugaro", "config.yaml") {
		t.Fatalf("XDG Path = %q", p)
	}
	env["FUGARO_CONFIG"] = filepath.Join(dir, "explicit.yaml")
	if p, _ = Path(get); p != env["FUGARO_CONFIG"] {
		t.Fatalf("FUGARO_CONFIG Path = %q", p)
	}
	if _, err := Load(p); !errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), "run fugaro init") {
		t.Fatalf("missing file: %v", err)
	}
	if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || c.Project != "my-project" {
		t.Fatalf("Load = %+v, %v", c, err)
	}
	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if again, err := Parse(data); err != nil || again.RunsBucket != "my-runs" {
		t.Fatalf("round trip = %+v, %v", again, err)
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
