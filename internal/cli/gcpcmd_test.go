package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

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
	}
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		t.Fatal(err)
	}
	if js.Job != "fugaro-acme-app-web" || js.TaskTimeoutS != 20*60+120 || js.Env["FUGARO_BACKEND"] != "cloud-run" || js.Env["FUGARO_PROJECT"] != "proj-1234" || js.Env["FUGARO_REGION"] != "us-east5" {
		t.Fatalf("spec = %+v", js)
	}
	want := map[string]string{"FUGARO_BITBUCKET_TOKEN": "fugaro-acme-app-bitbucket-token", "CLAUDE_CODE_OAUTH_TOKEN": "fugaro-acme-app-claude-oauth-token", "NPM_TOKEN": "fugaro-acme-app-npm-token"}
	if len(js.Secrets) != len(want) {
		t.Fatalf("secrets = %v", js.Secrets)
	}
	for k, v := range want {
		if js.Secrets[k] != v {
			t.Fatalf("secrets = %v", js.Secrets)
		}
	}
	secrets, _, _ := execute(t, "gcp", "job-spec", "--workflow", "web", "--field", "secrets")
	if strings.TrimSpace(secrets) != "CLAUDE_CODE_OAUTH_TOKEN=fugaro-acme-app-claude-oauth-token:latest,FUGARO_BITBUCKET_TOKEN=fugaro-acme-app-bitbucket-token:latest,NPM_TOKEN=fugaro-acme-app-npm-token:latest" {
		t.Fatalf("--field secrets = %q", secrets)
	}
	cond, _, _ := execute(t, "gcp", "job-spec", "--workflow", "web", "--field", "bucket-condition")
	wantCond := `resource.name.startsWith("projects/_/buckets/proj-1234-fugaro-runs/objects/runs/acme-app/") || ` +
		`resource.name.startsWith("projects/_/buckets/proj-1234-fugaro-runs/objects/cache/acme-app/") || ` +
		`resource.name.startsWith("projects/_/buckets/proj-1234-fugaro-runs/objects/locks/acme-app/")`
	if strings.TrimSpace(cond) != wantCond {
		t.Fatalf("bucket-condition =\n%s\nwant\n%s", cond, wantCond)
	}
	help, _, _ := execute(t, "--help")
	if strings.Contains(help, "gcp") {
		t.Fatal("gcp is listed in fugaro --help")
	}
}

func TestGCPJobSpecFields(t *testing.T) {
	jobSpecCheckout(t, jobSpecYAML)
	for field, want := range map[string]string{
		"job":              "fugaro-acme-app-web",
		"sa-id":            "fugaro-acme-app-web",
		"sa":               "fugaro-acme-app-web@proj-1234.iam.gserviceaccount.com",
		"image":            "us-east5-docker.pkg.dev/proj-1234/fugaro/acme-app-web",
		"task-timeout":     "1320",
		"cpu":              "4", // the web-node defaults
		"memory":           "8Gi",
		"labels":           "fugaro=managed,fugaro_repo=acme-app,fugaro_workflow=web",
		"repo-label":       "acme-app",
		"git-secret":       "fugaro-acme-app-bitbucket-token",
		"env":              "FUGARO_BACKEND=cloud-run,FUGARO_BUCKET=gs://proj-1234-fugaro-runs,FUGARO_PROJECT=proj-1234,FUGARO_REGION=us-east5",
		"secret-ids":       "fugaro-acme-app-bitbucket-token\nfugaro-acme-app-claude-oauth-token\nfugaro-acme-app-npm-token",
		"secret-names":     "bitbucket-token=fugaro-acme-app-bitbucket-token\nclaude-oauth-token=fugaro-acme-app-claude-oauth-token\nnpm-token=fugaro-acme-app-npm-token",
		"build-secret-ids": "fugaro-acme-app-bitbucket-token\nfugaro-acme-app-npm-token",
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
	if out, _, err := execute(t, "gcp", "job-spec", "--repo", "Acme/App", "--field", "job"); err != nil || strings.TrimSpace(out) != "fugaro-acme-app-web" {
		t.Fatalf("--repo Acme/App: %q, %v", out, err)
	}
	if _, _, err := execute(t, "gcp", "job-spec", "--field", "nope"); ExitCode(err) != ExitUserError {
		t.Fatalf("unknown field: err = %v", err)
	}
}

func TestGCPJobSpecVertex(t *testing.T) {
	jobSpecCheckout(t, strings.Replace(jobSpecYAML, "auth: oauth", "auth: vertex", 1))
	out, _, err := execute(t, "gcp", "job-spec", "--field", "secrets")
	if err != nil || strings.TrimSpace(out) != "FUGARO_BITBUCKET_TOKEN=fugaro-acme-app-bitbucket-token:latest,NPM_TOKEN=fugaro-acme-app-npm-token:latest" {
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
	t.Run("no local config", func(t *testing.T) {
		jobSpecCheckout(t, jobSpecYAML)
		t.Setenv("FUGARO_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
		if _, _, err := execute(t, "gcp", "job-spec", "--json"); ExitCode(err) != ExitUserError {
			t.Fatalf("err = %v", err)
		}
	})
}
