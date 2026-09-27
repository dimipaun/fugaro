package images_test

import (
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
		ID        string   `yaml:"id"`
		Name      string   `yaml:"name"`
		Args      []string `yaml:"args"`
		SecretEnv []string `yaml:"secretEnv"`
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
	if !slices.Equal(cb.Steps[0].SecretEnv, []string{"GIT_CREDENTIALS"}) || len(cb.Steps[1].SecretEnv)+len(cb.Steps[2].SecretEnv) != 0 {
		t.Error("only the source step may see GIT_CREDENTIALS as a variable")
	}
	if len(cb.AvailableSecrets.SecretManager) != 1 || cb.AvailableSecrets.SecretManager[0].Env != "GIT_CREDENTIALS" {
		t.Errorf("availableSecrets = %+v", cb.AvailableSecrets)
	}
	if !slices.Equal(cb.Images, []string{"${_IMAGE}:latest"}) {
		t.Errorf("images = %v", cb.Images)
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

// TestCIWorkflowPermissionsScoped keeps packages:write off any job that can
// run on pull_request: the workflow-level default must be read-only, and
// only a job whose `if:` gates on the resolved publish flag (never true for
// pull_request; see the version job's "Pick the version" step) may declare
// packages:write.
func TestCIWorkflowPermissionsScoped(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/images.yml")
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
		t.Fatal(err)
	}
	if wf.Permissions["contents"] != "read" || wf.Permissions["packages"] != "" {
		t.Errorf("workflow-level permissions = %+v, want only contents: read", wf.Permissions)
	}
	for name, job := range wf.Jobs {
		if job.Permissions["packages"] != "write" {
			continue
		}
		if !strings.Contains(job.If, "publish") {
			t.Errorf("job %q grants packages:write but its `if:` (%q) does not gate on the resolved publish flag, so it could run on pull_request", name, job.If)
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
// workflows to a full commit SHA, with the tag it resolves to as a comment,
// so a moved or compromised tag can't change what CI runs.
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
			if !pinnedRE.MatchString(m[1]) || !regexp.MustCompile(`^\s+# v\d`).MatchString(m[2]) {
				t.Errorf("%s: %q is not pinned as owner/repo@<40-hex sha> # vX", wf, strings.TrimSpace(m[0]))
			}
		}
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
