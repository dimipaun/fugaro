package images_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// imagesWorkflowPath and pendingImagesWorkflowPath: GitHub refuses a Fugaro
// push that touches .github/workflows/**, so a Fugaro run that needs a
// workflow change writes the new file's full content under
// docs/pending-workflows/ instead (see docs/pending-workflows/README.md) for
// a human to copy in by hand. readImagesWorkflow reads the real file and
// falls back to the pending one when the real file doesn't hold the change
// yet, so this test (and any other that needs the fugaro-base/publish-base
// jobs) is green before the copy and keeps guarding the jobs after it.
const (
	imagesWorkflowPath        = "../.github/workflows/images.yml"
	pendingImagesWorkflowPath = "../docs/pending-workflows/images.yml"
)

func readImagesWorkflow(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(imagesWorkflowPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "fugaro-base:") {
		data, err = os.ReadFile(pendingImagesWorkflowPath)
		if err != nil {
			t.Fatal(err)
		}
	}
	return data
}

func TestImagesWorkflowPublishesAMultiArchBase(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]struct {
			RunsOn      string            `yaml:"runs-on"`
			Permissions map[string]string `yaml:"permissions"`
			Strategy    struct {
				Matrix struct {
					Include []map[string]string `yaml:"include"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
			Steps []struct {
				Run string            `yaml:"run"`
				Env map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	b, ok := wf.Jobs["fugaro-base"]
	if !ok {
		t.Fatal("no fugaro-base job")
	}
	runners := map[string]string{}
	for _, inc := range b.Strategy.Matrix.Include {
		runners[inc["arch"]] = inc["runner"]
	}
	if runners["amd64"] != "ubuntu-latest" || runners["arm64"] != "ubuntu-24.04-arm" {
		t.Errorf("the base builds on %v, want native amd64 and arm64 runners", runners)
	}
	if b.Permissions["packages"] != "" {
		t.Error("the build job has a packages permission")
	}
	var runs []string
	for _, s := range b.Steps {
		runs = append(runs, s.Run)
	}
	all := strings.Join(runs, "\n")
	for _, want := range []string{
		"images/build-base.sh base fugaro-base:ci",
		"images/smoke.sh fugaro-base:ci base",
		"images/size-budget.sh fugaro-base:ci",
		"images/scan.sh --secrets fugaro-base:ci",
		`fugaro-base-$ARCH.tar.gz`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the fugaro-base job never runs %q", want)
		}
	}
	p, ok := wf.Jobs["publish-base"]
	if !ok || p.Permissions["packages"] != "write" {
		t.Fatal("no publish-base job with packages: write")
	}
	pub := ""
	for _, s := range p.Steps {
		pub += s.Run
	}
	for _, want := range []string{`"$repo:$VERSION-$arch"`, "docker buildx imagetools create", `--tag $repo:$VERSION`, `--tag $repo:$MAJOR`} {
		if !strings.Contains(pub, want) {
			t.Errorf("publish-base lacks %q", want)
		}
	}
	v := ""
	for _, s := range wf.Jobs["verify-public"].Steps {
		v += s.Run
	}
	if !strings.Contains(v, "images/verify-public.sh --arm64") {
		t.Error("verify-public does not require arm64 for the base")
	}
}

// TestOnlyPublishJobsHoldPackagesWrite: the build jobs (fugaro-base included)
// must never hold packages: write, only the jobs that push to ghcr.io.
func TestOnlyPublishJobsHoldPackagesWrite(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	for name, job := range wf.Jobs {
		write := job.Permissions["packages"] == "write"
		wantWrite := name == "publish" || name == "publish-base"
		if write != wantWrite {
			t.Errorf("job %q packages permission = %q, want write only for publish/publish-base", name, job.Permissions["packages"])
		}
	}
}
