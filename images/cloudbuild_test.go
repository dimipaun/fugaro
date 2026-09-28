package images_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/testutil"
)

type cloudBuild struct {
	Substitutions    map[string]string `yaml:"substitutions"`
	AvailableSecrets struct {
		SecretManager []struct {
			VersionName string `yaml:"versionName"`
			Env         string `yaml:"env"`
		} `yaml:"secretManager"`
	} `yaml:"availableSecrets"`
	Steps []struct {
		ID         string   `yaml:"id"`
		Name       string   `yaml:"name"`
		Entrypoint string   `yaml:"entrypoint"`
		Env        []string `yaml:"env"`
		Args       []string `yaml:"args"`
		SecretEnv  []string `yaml:"secretEnv"`
	} `yaml:"steps"`
	Images []string `yaml:"images"`
}

func TestCloudBuildConfig(t *testing.T) {
	data, err := os.ReadFile("derived/cloudbuild.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cb cloudBuild
	if err := yaml.Unmarshal(data, &cb); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range cb.Steps {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"source", "render", "build"}) {
		t.Fatalf("steps = %v", ids)
	}
	for _, m := range regexp.MustCompile(`\$\{(_[A-Z_]+)\}`).FindAllStringSubmatch(string(data), -1) {
		if _, ok := cb.Substitutions[m[1]]; !ok {
			t.Errorf("%s is used but not declared in substitutions", m[1])
		}
	}
	script := func(i int) string { return strings.Join(cb.Steps[i].Args, " ") }
	if !strings.Contains(script(1), "fugaro image render") || cb.Steps[1].Name != "${_FUGARO_BASE}" {
		t.Error("the render step must use the base image's own fugaro image render")
	}
	if !strings.Contains(script(2), "--secret id=git-credentials,") || !strings.Contains(images.DerivedTemplate, "id=git-credentials") {
		t.Error("the build step and the template disagree on the git credential secret")
	}
	if !strings.Contains(script(2), "RepoDigests") {
		t.Error("the build step does not pin the base image by digest")
	}
	for _, k := range []string{"_REPO_URL", "_BASE_BRANCH", "_WORKFLOW", "_FUGARO_BASE", "_IMAGE", "_GIT_SECRET", "_GIT_USER", "_SECRET_ENVS"} {
		if _, ok := cb.Substitutions[k]; !ok {
			t.Errorf("substitution %s is not declared", k)
		}
	}
	build := strings.Join(cb.Steps[2].Args, " ")
	if strings.Contains(build, "docker pull") || !strings.Contains(build, "docker image inspect") {
		t.Error("the build step must reuse the render step's base image, not pull it again")
	}
	if !strings.Contains(build, "SECRET_ENVS") || !strings.Contains(build, "--secret") {
		t.Error("the build step does not pass workflow secrets")
	}
	if !slices.Equal(cb.Steps[0].SecretEnv, []string{"GIT_TOKEN"}) {
		t.Errorf("source secretEnv = %v", cb.Steps[0].SecretEnv)
	}
	if len(cb.Steps[1].SecretEnv)+len(cb.Steps[2].SecretEnv) != 0 {
		t.Error("only the source step may see GIT_TOKEN as a variable; workflow secrets are added per request")
	}
	if src := strings.Join(cb.Steps[0].Args, " "); !strings.Contains(src, "https://*)") {
		t.Error("the source step must refuse a non-https REPO_URL")
	}
	if len(cb.AvailableSecrets.SecretManager) != 1 || cb.AvailableSecrets.SecretManager[0].Env != "GIT_TOKEN" {
		t.Errorf("availableSecrets = %+v", cb.AvailableSecrets)
	}
	if !bytes.Equal(images.CloudBuild, data) {
		t.Error("images.CloudBuild is not derived/cloudbuild.yaml")
	}
	if !slices.Equal(cb.Images, []string{"${_IMAGE}:latest"}) {
		t.Errorf("images = %v", cb.Images)
	}
}

// TestCloudBuildNoSubstitutionsInScripts keeps Cloud Build substitutions
// out of shell script text. Substitution is textual, so a crafted value (a
// branch name such as x$(curl…|sh) is a valid git ref) would run as shell
// next to GIT_CREDENTIALS. Each value must reach a shell step through env:
// instead, and every $$VAR a script reads must be one it was given.
func TestCloudBuildNoSubstitutionsInScripts(t *testing.T) {
	data, err := os.ReadFile("derived/cloudbuild.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cb cloudBuild
	if err := yaml.Unmarshal(data, &cb); err != nil {
		t.Fatal(err)
	}
	varRE := regexp.MustCompile(`\$\$\{?([A-Z][A-Z0-9_]*)`)
	for _, s := range cb.Steps {
		if s.Entrypoint != "bash" && s.Entrypoint != "sh" {
			continue
		}
		script := strings.Join(s.Args, "\n")
		if strings.Contains(script, "${_") {
			t.Errorf("step %s interpolates a substitution into its script; pass it through env: and read it as $$VAR", s.ID)
		}
		given := map[string]bool{}
		for _, e := range append(append([]string{}, s.Env...), s.SecretEnv...) {
			name, _, _ := strings.Cut(e, "=")
			given[name] = true
		}
		for _, m := range varRE.FindAllStringSubmatch(script, -1) {
			if !given[m[1]] {
				t.Errorf("step %s reads $$%s, which is not in its env: or secretEnv:", s.ID, m[1])
			}
		}
	}
}

// TestCIWorkflowUsesScripts keeps CI and local runs identical: the workflow
// may only build, smoke-test and scan through the scripts developers run.
func TestCIWorkflowUsesScripts(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/images.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"images/build-base.sh", "images/smoke.sh", "images/scan.sh", "go test -tags docker"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("images.yml does not run %s", want)
		}
	}
	if strings.Contains(string(data), "docker build ") {
		t.Error("images.yml calls docker build directly; use images/build-base.sh")
	}
}

// TestCIWorkflowPermissionsScoped holds every workflow file to a read-only
// GITHUB_TOKEN by default: the workflow-level permissions must be exactly
// contents: read, and only a job whose `if:` gates on the resolved publish
// flag (never true for pull_request; see images.yml's "Pick the version"
// step) may declare packages:write.
func TestCIWorkflowPermissionsScoped(t *testing.T) {
	files, err := filepath.Glob("../.github/workflows/*.yml")
	if err != nil || len(files) < 2 {
		t.Fatalf("workflow files = %v (%v)", files, err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var wf struct {
			Permissions map[string]string `yaml:"permissions"`
			Jobs        map[string]struct {
				If          string            `yaml:"if"`
				Permissions map[string]string `yaml:"permissions"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &wf); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
			t.Errorf("%s: workflow-level permissions = %+v, want only contents: read", f, wf.Permissions)
		}
		for name, job := range wf.Jobs {
			if job.Permissions["packages"] != "write" {
				continue
			}
			if !strings.Contains(job.If, "publish") {
				t.Errorf("%s: job %q grants packages:write but its `if:` (%q) does not gate on the resolved publish flag, so it could run on pull_request", f, name, job.If)
			}
		}
	}
}

// TestCIWorkflowNoUnsafeInterpolation keeps refname-derived and actor values
// out of `run:` script text, where a crafted ref name or username could
// inject shell. They must instead be passed through `env:` and referenced as
// shell variables.
func TestCIWorkflowNoUnsafeInterpolation(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/images.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	for jobName, job := range wf.Jobs {
		for _, step := range job.Steps {
			for _, bad := range []string{"${{ needs.", "${{ github.actor"} {
				if strings.Contains(step.Run, bad) {
					t.Errorf("job %q step %q run: interpolates %q directly; pass it via env: and reference it as a shell variable", jobName, step.Name, bad)
				}
			}
		}
	}
}

// TestWorkflowActionsPinnedBySHA pins every third-party action in both
// workflows to a full commit SHA, with the exact release tag it sits on as
// a comment, so a moved or compromised tag can't change what CI runs, and a
// Dependabot config keeps the pins current.
func TestWorkflowActionsPinnedBySHA(t *testing.T) {
	usesRE := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*(\S+)(.*)$`)
	pinnedRE := regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)
	for _, wf := range []string{"../.github/workflows/images.yml", "../.github/workflows/ci.yml"} {
		data, err := os.ReadFile(wf)
		if err != nil {
			t.Fatal(err)
		}
		matches := usesRE.FindAllStringSubmatch(string(data), -1)
		if len(matches) == 0 {
			t.Errorf("%s has no uses: lines", wf)
		}
		for _, m := range matches {
			if !pinnedRE.MatchString(m[1]) || !regexp.MustCompile(`^\s+# v\d+\.\d+\.\d+\s*$`).MatchString(m[2]) {
				t.Errorf("%s: %q is not pinned as owner/repo@<40-hex sha> # vX.Y.Z", wf, strings.TrimSpace(m[0]))
			}
		}
	}
	data, err := os.ReadFile("../.github/dependabot.yml")
	if err != nil {
		t.Fatal(err)
	}
	var db struct {
		Updates []struct {
			Ecosystem string `yaml:"package-ecosystem"`
			Schedule  struct {
				Interval string `yaml:"interval"`
			} `yaml:"schedule"`
		} `yaml:"updates"`
	}
	if err := yaml.Unmarshal(data, &db); err != nil {
		t.Fatal(err)
	}
	weekly := false
	for _, u := range db.Updates {
		weekly = weekly || (u.Ecosystem == "github-actions" && u.Schedule.Interval == "weekly")
	}
	if !weekly {
		t.Errorf("dependabot.yml has no weekly github-actions update: %+v", db.Updates)
	}
}

// versionStep extracts the version job's "Pick the version" script from
// images.yml, with the one GitHub expression it holds replaced, so it can
// run under plain sh.
func versionStep(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../.github/workflows/images.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	for _, s := range wf.Jobs["version"].Steps {
		if s.Name == "Pick the version" {
			return strings.ReplaceAll(s.Run, "${{ github.event.number }}", "7")
		}
	}
	t.Fatal("images.yml has no version job step named \"Pick the version\"")
	return ""
}

// TestCIWorkflowMovesMajorOnlyForTheNewestRelease: a backport tag publishes
// :X.Y.Z but must not move :X backwards past a newer release in the same
// major.
func TestCIWorkflowMovesMajorOnlyForTheNewestRelease(t *testing.T) {
	testutil.IsolateGit(t)
	script := versionStep(t)
	repo := t.TempDir()
	testutil.Git(t, repo, "init", "--quiet", "-b", "main", repo)
	testutil.Git(t, repo, "commit", "--quiet", "--allow-empty", "-m", "seed")
	for _, tag := range []string{"v1.1.4", "v1.1.5", "v1.2.0", "v1.10.0-rc1", "v2.0.0", "v10.0.0"} {
		testutil.Git(t, repo, "tag", tag)
	}
	for _, tc := range []struct {
		event, ref, version string
		moveMajor           bool
	}{
		{"push", "v1.1.5", "1.1.5", false},
		{"push", "v1.2.0", "1.2.0", true},
		{"push", "v2.0.0", "2.0.0", true},
		{"push", "v10.0.0", "10.0.0", true},
		{"schedule", "main", "10.0.0", true},
	} {
		t.Run(tc.event+" "+tc.ref, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "output")
			cmd := exec.Command("sh", "-c", script)
			cmd.Dir = repo
			cmd.Env = append(os.Environ(), "GITHUB_EVENT_NAME="+tc.event, "GITHUB_REF_NAME="+tc.ref, "GITHUB_OUTPUT="+out)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("Pick the version: %v\n%s", err, b)
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			got := string(data)
			for _, want := range []string{"version=" + tc.version + "\n", "publish=true\n", fmt.Sprintf("move_major=%t\n", tc.moveMajor)} {
				if !strings.Contains(got, want) {
					t.Errorf("outputs lack %q:\n%s", want, got)
				}
			}
		})
	}
	data, err := os.ReadFile("../.github/workflows/images.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "MOVE_MAJOR: ${{ needs.version.outputs.move_major }}") {
		t.Error("the publish step does not take move_major from the version job")
	}
}

// TestCIWorkflowJobsHaveTimeouts bounds every job in every workflow, so a
// hung build or test can't hold a runner for GitHub's 6-hour default.
func TestCIWorkflowJobsHaveTimeouts(t *testing.T) {
	files, err := filepath.Glob("../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("workflow files = %v (%v)", files, err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var wf struct {
			Jobs map[string]struct {
				TimeoutMinutes int `yaml:"timeout-minutes"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &wf); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for name, job := range wf.Jobs {
			if job.TimeoutMinutes <= 0 || job.TimeoutMinutes > 120 {
				t.Errorf("%s: job %q timeout-minutes = %d, want 1..120", f, name, job.TimeoutMinutes)
			}
		}
	}
}

// TestCIWorkflowCheckoutsDropCredentials: no job needs the GITHUB_TOKEN in
// .git/config after checkout, so every actions/checkout sets
// persist-credentials: false.
func TestCIWorkflowCheckoutsDropCredentials(t *testing.T) {
	files, err := filepath.Glob("../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("workflow files = %v (%v)", files, err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var wf struct {
			Jobs map[string]struct {
				Steps []struct {
					Uses string         `yaml:"uses"`
					With map[string]any `yaml:"with"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &wf); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for name, job := range wf.Jobs {
			for _, s := range job.Steps {
				if strings.HasPrefix(s.Uses, "actions/checkout@") && s.With["persist-credentials"] != false {
					t.Errorf("%s: job %q checks out without persist-credentials: false", f, name)
				}
			}
		}
	}
}
