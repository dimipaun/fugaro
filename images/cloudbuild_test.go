package images_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/images"
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
