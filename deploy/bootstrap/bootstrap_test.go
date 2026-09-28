package bootstrap_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// fakeBin puts gcloud and docker stand-ins on PATH that fail the test if
// the dry run ever calls them.
func fakeBin(t *testing.T) string {
	dir := t.TempDir()
	for _, name := range []string{"gcloud", "docker"} {
		script := "#!/bin/sh\necho \"$0 was called in a dry run: $*\" >&2\nexit 99\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// recordingBin puts gcloud and docker stand-ins on PATH that succeed and
// append their argv, one call per line, to the returned log.
func recordingBin(t *testing.T) (dir, log string) {
	dir = t.TempDir()
	log = filepath.Join(dir, "calls.log")
	for _, name := range []string{"gcloud", "docker"} {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> " + log + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir, log
}

// sandboxCheckout is a git checkout of ./sandbox with a Bitbucket https origin.
func sandboxCheckout(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sandbox")
	entries, err := os.ReadDir("sandbox")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join("sandbox", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(data)
	}
	testutil.WriteFiles(t, dir, files)
	testutil.Git(t, dir, "init", "--quiet", "-b", "master")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "sandbox")
	testutil.Git(t, dir, "remote", "add", "origin", "https://bitbucket.org/acme/sandbox.git")
	return dir
}

// writeLocalConfig writes a minimal local config for project proj-1234.
func writeLocalConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "version: 1\nproject: proj-1234\nregion: us-east5\nruns_bucket: proj-1234-fugaro-runs\n" +
		"registry: us-east5-docker.pkg.dev/proj-1234/fugaro\nbuild: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// scriptEnv is the environment of a full run against the sandbox checkout.
func scriptEnv(t *testing.T, bin string) []string {
	fugaro := testutil.BuildFugaro(t)
	return append(os.Environ(),
		"PATH="+bin+":"+filepath.Dir(fugaro)+":"+os.Getenv("PATH"),
		"PROJECT=proj-1234", "REGION=us-east5", "BUCKET=proj-1234-fugaro-runs", "REPO=acme/sandbox",
		"WORKFLOW=web", "CHECKOUT="+sandboxCheckout(t), "FUGARO="+fugaro, "FUGARO_CONFIG="+writeLocalConfig(t),
		"HEAVY=/bin/echo", "FUGARO_SRC="+testutil.ModuleRoot())
}

func TestDryRunNamesMatchTheContract(t *testing.T) {
	testutil.IsolateGit(t)
	env := scriptEnv(t, fakeBin(t))
	for _, step := range []string{"apis", "bucket", "registry", "build-sa", "job-sa", "secrets", "secrets-access", "base", "image", "job", "teardown", "teardown-all"} {
		args := []string{"gcp-m4.sh", step}
		if step == "teardown-all" {
			args = append(args, "--all")
		}
		cmd := exec.Command("bash", args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", step, err, out)
		}
		s := string(out)
		if step != "secrets" && !strings.Contains(s, "⚠ CONFIRM") {
			t.Errorf("%s creates or deletes resources without a ⚠ CONFIRM banner", step)
		}
		if step != "secrets" && !strings.Contains(s, "proj-1234") {
			t.Errorf("%s: the banner does not name the project:\n%s", step, s)
		}
		if step == "job" {
			for _, want := range []string{gcp.JobName("acme-sandbox", "web"), "--max-retries 0", "--task-timeout 1320s", "FUGARO_BACKEND=cloud-run",
				"CLAUDE_CODE_OAUTH_TOKEN=" + gcp.SecretID("acme-sandbox", "claude-oauth-token") + ":latest"} {
				if !strings.Contains(s, want) {
					t.Errorf("job step lacks %q:\n%s", want, s)
				}
			}
		}
		if step == "job-sa" && !strings.Contains(s, gcp.ServiceAccountID("acme-sandbox", "web")) {
			t.Errorf("job-sa step lacks the service account ID:\n%s", s)
		}
		if step == "secrets" {
			for _, want := range []string{"claude-oauth-token", "bitbucket-token", "sandbox-probe"} {
				if !strings.Contains(s, want) {
					t.Errorf("secrets step lacks %q:\n%s", want, s)
				}
			}
		}
		if step == "image" && !strings.Contains(s, "+ ") {
			t.Errorf("image step prints no command:\n%s", s)
		}
	}
}

// TestApplyPassesProjectToEveryGcloudCall runs every GCP step for real
// against a recording gcloud, and checks each call names the project.
func TestApplyPassesProjectToEveryGcloudCall(t *testing.T) {
	testutil.IsolateGit(t)
	bin, log := recordingBin(t)
	env := scriptEnv(t, bin)
	for _, step := range []string{"apis", "bucket", "registry", "build-sa", "job-sa", "secrets-access", "base", "job", "teardown", "teardown-all"} {
		args := []string{"gcp-m4.sh", "--apply", "--yes", step}
		if step == "teardown-all" {
			args = append(args, "--all")
		}
		cmd := exec.Command("bash", args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", step, err, out)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(calls) < 20 {
		t.Fatalf("only %d calls recorded:\n%s", len(calls), data)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "gcloud ") && !strings.Contains(c, "--project proj-1234") {
			t.Errorf("gcloud call without --project proj-1234: %s", c)
		}
		if strings.HasPrefix(c, "docker ") {
			t.Errorf("docker called directly rather than through HEAVY: %s", c)
		}
	}
	for _, want := range []string{
		"gcloud run jobs deploy fugaro-acme-sandbox-web ",
		"gcloud storage buckets add-iam-policy-binding gs://proj-1234-fugaro-runs --member serviceAccount:fugaro-acme-sandbox-web@proj-1234.iam.gserviceaccount.com --role roles/storage.objectUser --condition expression=resource.name.startsWith(\"projects/_/buckets/proj-1234-fugaro-runs/objects/runs/acme-sandbox/\")",
		"gcloud secrets add-iam-policy-binding fugaro-acme-sandbox-sandbox-probe --project proj-1234 --member serviceAccount:fugaro-build@proj-1234.iam.gserviceaccount.com",
		"gcloud secrets delete fugaro-acme-sandbox-claude-oauth-token ",
		"gcloud storage rm --recursive gs://proj-1234-fugaro-runs",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("no call contains %q:\n%s", want, data)
		}
	}
}

func TestApplyNeedsConfirmation(t *testing.T) {
	testutil.IsolateGit(t)
	bin, log := recordingBin(t)
	cmd := exec.Command("bash", "gcp-m4.sh", "--apply", "apis")
	cmd.Env = scriptEnv(t, bin)
	cmd.Stdin = strings.NewReader("proj-1234\n") // not a terminal: only --yes confirms
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "--yes") {
		t.Fatalf("--apply without --yes: %v\n%s", err, out)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("gcloud was called without a confirmation")
	}
}

func TestTeardownAllRefusesWhileJobsExist(t *testing.T) {
	testutil.IsolateGit(t)
	bin, log := recordingBin(t)
	// This gcloud reports one job for `run jobs list`.
	script := "#!/bin/sh\necho \"gcloud $*\" >> " + log + "\ncase \"$*\" in 'run jobs list'*) echo fugaro-acme-sandbox-web;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "gcloud"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "gcp-m4.sh", "--apply", "--yes", "teardown-all", "--all")
	cmd.Env = scriptEnv(t, bin)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "still exist") {
		t.Fatalf("teardown-all with a job left: %v\n%s", err, out)
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "delete") || strings.Contains(string(data), "storage rm") {
		t.Fatalf("teardown-all deleted something while a job exists:\n%s", data)
	}
}

func TestConfigStep(t *testing.T) {
	testutil.IsolateGit(t)
	gitcfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitcfg, []byte("[user]\n\temail = dev@example.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fugaro", "config.yaml")
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+gitcfg, "FUGARO_CONFIG="+path,
		"PROJECT=proj-1234", "REGION=us-east5", "BUCKET=proj-1234-fugaro-runs", "REPOS=acme/sandbox:master:web acme/app:main:web")
	run := func(extra []string, args ...string) (string, error) {
		cmd := exec.Command("bash", append([]string{"gcp-m4.sh"}, args...)...)
		cmd.Env = append(env, extra...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run(nil, "config"); err != nil || !strings.Contains(out, "project: proj-1234") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the dry run wrote the config")
	}
	if out, err := run(nil, "--apply", "config"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"user: dev@example.invalid", "service_account: fugaro-build@proj-1234.iam.gserviceaccount.com",
		"acme/sandbox: { base_branch: master, workflows: [web] }", "acme/app: { base_branch: main, workflows: [web] }"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("config lacks %q:\n%s", want, data)
		}
	}
	if out, err := run([]string{"BASE_IMAGE=r/b:dev"}, "--apply", "config"); err == nil || !strings.Contains(out, "FORCE=1") || !strings.Contains(out, "+base_image: r/b:dev") {
		t.Fatalf("overwrite without FORCE: %v\n%s", err, out)
	}
	if out, err := run([]string{"BASE_IMAGE=r/b:dev", "FORCE=1"}, "--apply", "config"); err != nil {
		t.Fatalf("FORCE=1: %v\n%s", err, out)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "base_image: r/b:dev") {
		t.Fatalf("FORCE=1 did not replace the config:\n%s", data)
	}
}

func TestScriptGuards(t *testing.T) {
	testutil.IsolateGit(t)
	cfg := writeLocalConfig(t) // project: proj-1234
	base := append(os.Environ(), "PATH="+fakeBin(t)+":"+os.Getenv("PATH"), "FUGARO_CONFIG="+cfg, "REGION=us-east5", "BUCKET=b")
	cmd := exec.Command("bash", "gcp-m4.sh", "apis")
	cmd.Env = append(base, "PROJECT=someone-elses-project")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "refusing") {
		t.Fatalf("project mismatch: %v\n%s", err, out)
	}
	cmd = exec.Command("bash", "gcp-m4.sh", "teardown-all")
	cmd.Env = append(base, "PROJECT=proj-1234")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "--all") {
		t.Fatalf("teardown-all without --all: %v\n%s", err, out)
	}
	cmd = exec.Command("bash", "gcp-m4.sh", "teardown")
	cmd.Env = append(base, "PROJECT=proj-1234", "REPO=acme/sandbox", "WORKFLOW=web", "CHECKOUT="+sandboxCheckout(t), "FUGARO="+testutil.BuildFugaro(t))
	out, err := cmd.CombinedOutput()
	if err != nil || strings.Contains(string(out), "storage rm") || strings.Contains(string(out), "repositories delete") {
		t.Fatalf("per-repo teardown touches shared resources: %v\n%s", err, out)
	}
	// A job-spec failure must stop the step, never run gcloud with empty names.
	cmd = exec.Command("bash", "gcp-m4.sh", "job")
	cmd.Env = append(base, "PROJECT=proj-1234", "REPO=acme/other", "WORKFLOW=web", "CHECKOUT="+sandboxCheckout(t), "FUGARO="+testutil.BuildFugaro(t))
	if out, err := cmd.CombinedOutput(); err == nil || strings.Contains(string(out), "+ gcloud") {
		t.Fatalf("job step with a failing job-spec: %v\n%s", err, out)
	}
}

func TestScriptRefusesUnknownStep(t *testing.T) {
	cmd := exec.Command("bash", "gcp-m4.sh", "everything")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "usage") {
		t.Fatalf("unknown step: %v\n%s", err, out)
	}
}

func TestScriptShellcheck(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck not installed")
	}
	if out, err := exec.Command("shellcheck", "gcp-m4.sh").CombinedOutput(); err != nil {
		t.Fatalf("shellcheck:\n%s", out)
	}
}
