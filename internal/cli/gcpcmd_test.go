package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The job-spec fixture is Bitbucket's acme/app; every name below is
// computed from its slug through the naming contract, never spelled out.
var (
	bbSlug   = mustSlug("bitbucket", "acme/app")
	bbJob    = gcp.JobName(bbSlug, "web")
	bbSAID   = gcp.ServiceAccountID(bbSlug, "web")
	bbGit    = gcp.SecretID(bbSlug, "bitbucket-token")
	bbOAuth  = gcp.SecretID(bbSlug, "claude-oauth-token")
	bbNPM    = gcp.SecretID(bbSlug, "npm-token")
	bbLabel  = bbSlug // a slug is [a-z0-9-] only, so gcp.RepoLabel keeps it whole
	registry = "us-east5-docker.pkg.dev/proj-1234/fugaro"
)

// bucketCondition is the IAM condition job-spec must print for slug.
func bucketCondition(bucket, slug string) string {
	var conds []string
	for _, p := range []string{"runs", "cache", "locks"} {
		conds = append(conds, `resource.name.startsWith("projects/_/buckets/`+bucket+`/objects/`+p+`/`+slug+`/")`)
	}
	return strings.Join(conds, " || ")
}

const jobSpecYAML = `version: 1
git: { provider: bitbucket, base_branch: main }
agent: { auth: oauth }
workflows:
  web:
    base: web-node
    commands: { build: npm run build, test: npm test }
    secrets:
      - { name: npm-token, env: NPM_TOKEN }
    timeouts: { total: 20m }
`

func jobSpecCheckout(t *testing.T, cfg string) {
	t.Helper()
	files := npmFiles()
	files["fugaro.yaml"] = cfg
	dir := checkoutWith(t, files)
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://bitbucket.org/acme/app.git")
	lc := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(lc, []byte("version: 1\nproject: proj-1234\nregion: us-east5\nruns_bucket: proj-1234-fugaro-runs\nregistry: us-east5-docker.pkg.dev/proj-1234/fugaro\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", lc)
}

func TestGCPJobSpec(t *testing.T) {
	jobSpecCheckout(t, jobSpecYAML)
	out, _, err := execute(t, "gcp", "job-spec", "--workflow", "web", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var js struct {
		Job          string            `json:"job"`
		TaskTimeoutS int               `json:"task_timeout_s"`
		Env          map[string]string `json:"env"`
		Secrets      map[string]string `json:"secrets"`
		SADisplay    string            `json:"service_account_display_name"`
	}
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		t.Fatal(err)
	}
	if js.Job != bbJob || js.TaskTimeoutS != 20*60+int(backend.TaskTimeoutSlack/time.Second) || js.Env["FUGARO_BACKEND"] != backend.CloudRun || js.Env["FUGARO_PROJECT"] != "proj-1234" || js.Env["FUGARO_REGION"] != "us-east5" {
		t.Fatalf("spec = %+v", js)
	}
	// The display name is the account's ownership mark (IAM allows 100 characters).
	if js.SADisplay != "Fugaro M4 job "+bbSlug+" web" || len(js.SADisplay) > 100 {
		t.Fatalf("service_account_display_name = %q", js.SADisplay)
	}
	// Every mounted secret's variable, for the runner to redact from the start.
	if got := js.Env["FUGARO_SECRET_ENVS"]; got != "CLAUDE_CODE_OAUTH_TOKEN,FUGARO_BITBUCKET_TOKEN,NPM_TOKEN" {
		t.Fatalf("FUGARO_SECRET_ENVS = %q", got)
	}
	want := map[string]string{"FUGARO_BITBUCKET_TOKEN": bbGit, "CLAUDE_CODE_OAUTH_TOKEN": bbOAuth, "NPM_TOKEN": bbNPM}
	if len(js.Secrets) != len(want) {
		t.Fatalf("secrets = %v", js.Secrets)
	}
	for k, v := range want {
		if js.Secrets[k] != v {
			t.Fatalf("secrets = %v", js.Secrets)
		}
	}
	secrets, _, _ := execute(t, "gcp", "job-spec", "--workflow", "web", "--field", "secrets")
	if strings.TrimSpace(secrets) != "CLAUDE_CODE_OAUTH_TOKEN="+bbOAuth+":latest,FUGARO_BITBUCKET_TOKEN="+bbGit+":latest,NPM_TOKEN="+bbNPM+":latest" {
		t.Fatalf("--field secrets = %q", secrets)
	}
	cond, _, _ := execute(t, "gcp", "job-spec", "--workflow", "web", "--field", "bucket-condition")
	wantCond := bucketCondition("proj-1234-fugaro-runs", bbSlug)
	if strings.TrimSpace(cond) != wantCond {
		t.Fatalf("bucket-condition =\n%s\nwant\n%s", cond, wantCond)
	}
	help, _, _ := execute(t, "--help")
	if strings.Contains(help, "gcp") {
		t.Fatal("gcp is listed in fugaro --help")
	}
}

// startsWithRE is one clause of the bucket condition job-spec prints.
var startsWithRE = regexp.MustCompile(`^resource\.name\.startsWith\("([^"\\]+)"\)$`)

// conditionMatches evaluates a job-spec bucket condition, an OR of
// resource.name.startsWith clauses and nothing else, against an object's
// IAM resource name.
func conditionMatches(t *testing.T, cond, name string) bool {
	t.Helper()
	for _, clause := range strings.Split(cond, " || ") {
		m := startsWithRE.FindStringSubmatch(clause)
		if m == nil {
			t.Fatalf("bucket condition clause %q is not resource.name.startsWith(\"...\")", clause)
		}
		if strings.HasPrefix(name, m[1]) {
			return true
		}
	}
	return false
}

// A slug can be a string prefix of another repository's slug; the job's
// bucket condition must still never reach that repository's objects.
func TestGCPJobSpecConditionIsNotAPrefixMatch(t *testing.T) {
	jobSpecCheckout(t, jobSpecYAML)
	// acme/app's slug is acme-app-<hash>, so the repository
	// acme/app-<hash> gets acme-app-<hash>-<hash2>: a real slug with
	// bbSlug as a string prefix.
	other := mustSlug("bitbucket", "acme/"+strings.TrimPrefix(bbSlug, "acme-"))
	if !strings.HasPrefix(other, bbSlug) || other == bbSlug {
		t.Fatalf("slug %q does not extend %q; the pair no longer tests anything", other, bbSlug)
	}
	out, _, err := execute(t, "gcp", "job-spec", "--workflow", "web", "--field", "bucket-condition")
	if err != nil {
		t.Fatal(err)
	}
	cond := strings.TrimSpace(out)
	object := func(area, slug, rest string) string {
		return "projects/_/buckets/proj-1234-fugaro-runs/objects/" + area + "/" + slug + "/" + rest
	}
	for area, rest := range map[string]string{"runs": "20260927-100000-abcd/result.json", "cache": "web/k.tar.zst", "locks": "0123456789abcdef"} {
		if !conditionMatches(t, cond, object(area, bbSlug, rest)) {
			t.Errorf("the condition does not grant the job's own %s", object(area, bbSlug, rest))
		}
		if conditionMatches(t, cond, object(area, other, rest)) {
			t.Errorf("the condition for %s grants %s", bbSlug, object(area, other, rest))
		}
	}
	if conditionMatches(t, cond, "projects/_/buckets/proj-1234-fugaro-runs/objects/runs/"+bbSlug) {
		t.Error("the condition grants the bare prefix without its trailing slash")
	}
}

func TestGCPJobSpecFields(t *testing.T) {
	jobSpecCheckout(t, jobSpecYAML)
	for field, want := range map[string]string{
		"slug":            bbSlug,
		"job":             bbJob,
		"sa-id":           bbSAID,
		"sa":              bbSAID + "@proj-1234.iam.gserviceaccount.com",
		"image":           gcp.ImageName(registry, bbSlug, "web"),
		"task-timeout":    strconv.Itoa(20*60 + int(backend.TaskTimeoutSlack/time.Second)),
		"sa-display-name": "Fugaro M4 job " + bbSlug + " web",
		"cpu":             "4", // the web-node defaults
		"memory":          "8Gi",
		"labels":          "fugaro=managed,fugaro_repo=" + bbLabel + ",fugaro_workflow=web",
		"repo-label":      bbLabel,
		"git-secret":      bbGit,
		// FUGARO_SECRET_ENVS holds commas, so gcloud's ^;^ delimiter form.
		"env": "^;^FUGARO_BACKEND=cloud-run;FUGARO_BUCKET=gs://proj-1234-fugaro-runs;FUGARO_PROJECT=proj-1234;FUGARO_REGION=us-east5;" +
			"FUGARO_SECRET_ENVS=CLAUDE_CODE_OAUTH_TOKEN,FUGARO_BITBUCKET_TOKEN,NPM_TOKEN",
		"secret-ids":       strings.Join(sorted(bbGit, bbOAuth, bbNPM), "\n"),
		"secret-names":     "bitbucket-token=" + bbGit + "\nclaude-oauth-token=" + bbOAuth + "\nnpm-token=" + bbNPM,
		"build-secret-ids": strings.Join(sorted(bbGit, bbNPM), "\n"),
	} {
		out, _, err := execute(t, "gcp", "job-spec", "--workflow", "web", "--field", field)
		if err != nil || strings.TrimSpace(out) != want {
			t.Errorf("--field %s = %q, %v; want %q", field, out, err, want)
		}
	}
	// --repo must agree with the checkout's origin: the names come from it,
	// the secrets and resources from the checkout's fugaro.yaml.
	if _, _, err := execute(t, "gcp", "job-spec", "--repo", "acme/other", "--field", "job"); ExitCode(err) != ExitUserError {
		t.Fatalf("mismatched --repo: err = %v", err)
	}
	if out, _, err := execute(t, "gcp", "job-spec", "--repo", "Acme/App", "--field", "job"); err != nil || strings.TrimSpace(out) != bbJob {
		t.Fatalf("--repo Acme/App: %q, %v", out, err)
	}
	if _, _, err := execute(t, "gcp", "job-spec", "--field", "nope"); ExitCode(err) != ExitUserError {
		t.Fatalf("unknown field: err = %v", err)
	}
}

func TestGCPJobSpecVertex(t *testing.T) {
	jobSpecCheckout(t, strings.Replace(jobSpecYAML, "auth: oauth", "auth: vertex", 1))
	out, _, err := execute(t, "gcp", "job-spec", "--field", "secrets")
	if err != nil || strings.TrimSpace(out) != "FUGARO_BITBUCKET_TOKEN="+bbGit+":latest,NPM_TOKEN="+bbNPM+":latest" {
		t.Fatalf("secrets = %q, %v", out, err)
	}
	out, _, err = execute(t, "gcp", "job-spec", "--field", "env")
	if err != nil || !strings.Contains(out, "ANTHROPIC_VERTEX_PROJECT_ID=proj-1234") || !strings.Contains(out, "CLOUD_ML_REGION=us-east5") {
		t.Fatalf("env = %q, %v", out, err)
	}
}

func TestGCPJobSpecRefuses(t *testing.T) {
	for name, cfg := range map[string]string{
		"github":                           strings.Replace(jobSpecYAML, "bitbucket", "github", 1),
		"cpu 3":                            strings.Replace(jobSpecYAML, "    timeouts:", "    resources: { cpu: 3, memory: 4Gi }\n    timeouts:", 1),
		"env collides with the platform's": strings.Replace(jobSpecYAML, "env: NPM_TOKEN", "env: FUGARO_BUCKET", 1),
	} {
		t.Run(name, func(t *testing.T) {
			jobSpecCheckout(t, cfg)
			if _, _, err := execute(t, "gcp", "job-spec", "--workflow", "web", "--json"); ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("no origin", func(t *testing.T) {
		jobSpecCheckout(t, jobSpecYAML)
		testutil.Git(t, ".", "remote", "remove", "origin")
		_, _, err := execute(t, "gcp", "job-spec", "--repo", "acme/app", "--json")
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "origin") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("local config names another provider", func(t *testing.T) {
		jobSpecCheckout(t, jobSpecYAML)
		for provider, ok := range map[string]bool{"github": false, "bitbucket": true} {
			lc := filepath.Join(t.TempDir(), "config.yaml")
			body := "version: 1\nproject: proj-1234\nregion: us-east5\nruns_bucket: proj-1234-fugaro-runs\n" +
				"registry: us-east5-docker.pkg.dev/proj-1234/fugaro\nrepos:\n  Acme/App: { provider: " + provider + ", workflows: [web] }\n"
			if err := os.WriteFile(lc, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FUGARO_CONFIG", lc)
			out, _, err := execute(t, "gcp", "job-spec", "--field", "slug")
			switch {
			case ok && (err != nil || strings.TrimSpace(out) != bbSlug):
				t.Errorf("provider %s: %q, %v", provider, out, err)
			case !ok && (ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "make them agree")):
				t.Errorf("provider %s: err = %v", provider, err)
			}
		}
	})
	t.Run("no local config", func(t *testing.T) {
		jobSpecCheckout(t, jobSpecYAML)
		t.Setenv("FUGARO_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
		if _, _, err := execute(t, "gcp", "job-spec", "--json"); ExitCode(err) != ExitUserError {
			t.Fatalf("err = %v", err)
		}
	})
}

func sorted(s ...string) []string { return slices.Sorted(slices.Values(s)) }

// gcloudDict must keep every pair whole: a value with a comma switches to
// gcloud's ^DELIM^ form with a delimiter no pair contains.
func TestGCloudDict(t *testing.T) {
	for _, c := range []struct {
		pairs []string
		want  string
	}{
		{[]string{"A=1", "B=2"}, "A=1,B=2"},
		{[]string{"A=1,2", "B=3"}, "^;^A=1,2;B=3"},
		{[]string{"A=1,2", "B=x;y"}, "^|^A=1,2|B=x;y"},
	} {
		if got := gcloudDict(c.pairs); got != c.want {
			t.Errorf("gcloudDict(%q) = %q, want %q", c.pairs, got, c.want)
		}
	}
}

// job-spec stays the M4 bootstrap's interface, so gcp-m4.sh still works for
// a rollback: its output for the sandbox fixture is the one the M4 binary
// printed (testdata/capture-m4-jobspec.sh), the legacy image path included.
func TestJobSpecCommandUnchanged(t *testing.T) {
	testutil.IsolateGit(t)
	golden, err := filepath.Abs("../infra/testdata")
	if err != nil {
		t.Fatal(err)
	}
	golden += "/"
	dir := filepath.Join(t.TempDir(), "sandbox")
	if err := os.CopyFS(dir, os.DirFS("../../deploy/bootstrap/sandbox")); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "init", "-q", "-b", "master")
	testutil.Git(t, dir, "remote", "add", "origin", "https://bitbucket.org/acme/sandbox.git")
	lc := filepath.Join(t.TempDir(), "config.yaml")
	cfg := "version: 1\nproject: proj-1234\nregion: us-east5\nruns_bucket: fugaro-runs-proj-1234\n" +
		"registry: us-east5-docker.pkg.dev/proj-1234/fugaro\nbuild: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }\n" +
		"user: test@example.com\nrepos:\n  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }\n"
	if err := os.WriteFile(lc, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", lc)
	t.Chdir(dir)
	args := []string{"gcp", "job-spec", "--repo", "acme/sandbox", "--workflow", "web"}
	out, _, err := execute(t, append(args, "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(golden + "m4-jobspec-sandbox.json")
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Errorf("job-spec --json =\n%s\nwant\n%s", out, want)
	}
	rows, err := os.ReadFile(golden + "m4-jobspec-sandbox.fields.tsv")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(rows)) {
		field, value, _ := strings.Cut(strings.TrimSuffix(line, "\n"), "\t")
		out, _, err := execute(t, append(args, "--field", field)...)
		if got := strings.ReplaceAll(out, "\n", ";"); err != nil || got != value {
			t.Errorf("--field %s = %q, %v; want %q", field, got, err, value)
		}
	}
}
