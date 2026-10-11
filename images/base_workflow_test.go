package images_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// imagesWorkflowPath and pendingImagesWorkflowPath: GitHub refuses a Fugaro
// push that touches .github/workflows/**, so a Fugaro run that needs a
// workflow change writes the new file's full content under
// docs/pending-workflows/ instead (see docs/pending-workflows/README.md) for
// a human to copy in by hand (deleting the pending file as part of the
// copy). readWorkflow reads the pending file while it still exists (the
// change awaiting copy); once it is gone, it reads the real file and that
// file must hold the change, so a regression that deletes the jobs from the
// real file (after the copy) fails the test instead of silently reading a
// stale pending copy that was never cleaned up.
const (
	imagesWorkflowPath        = "../.github/workflows/images.yml"
	pendingImagesWorkflowPath = "../docs/pending-workflows/images.yml"
)

// readWorkflow implements the fallback described above for any
// (pendingPath, realPath) pair, so TestReadWorkflowFallsBackOnlyWhilePending
// can exercise both modes without touching the real repository files.
func readWorkflow(pendingPath, realPath string) ([]byte, error) {
	switch _, err := os.Stat(pendingPath); {
	case err == nil:
		return os.ReadFile(pendingPath)
	case !os.IsNotExist(err):
		return nil, err
	}
	data, err := os.ReadFile(realPath)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(string(data), "fugaro-base:") {
		return nil, fmt.Errorf("%s has no fugaro-base job and %s does not exist: were the base jobs deleted after the pending file was copied in and removed?", realPath, pendingPath)
	}
	return data, nil
}

func readImagesWorkflow(t *testing.T) []byte {
	t.Helper()
	data, err := readWorkflow(pendingImagesWorkflowPath, imagesWorkflowPath)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestReadWorkflowFallsBackOnlyWhilePending proves both modes of readWorkflow:
// the pending file wins outright while it exists (even over a real file that
// already disagrees with it), and once it is gone the real file is read and
// must contain the base jobs on its own.
func TestReadWorkflowFallsBackOnlyWhilePending(t *testing.T) {
	dir := t.TempDir()
	pending := filepath.Join(dir, "pending.yml")
	real := filepath.Join(dir, "real.yml")

	if err := os.WriteFile(pending, []byte("jobs:\n  fugaro-base: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("jobs:\n  other: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := readWorkflow(pending, real)
	if err != nil || !strings.Contains(string(data), "fugaro-base") {
		t.Fatalf("pending present: got %q, err %v, want the pending file's content", data, err)
	}

	if err := os.Remove(pending); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("jobs:\n  fugaro-base: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, err := readWorkflow(pending, real); err != nil || !strings.Contains(string(data), "fugaro-base") {
		t.Fatalf("pending gone, real file holds the job: got %q, err %v, want the real file's content", data, err)
	}

	if err := os.WriteFile(real, []byte("jobs:\n  other: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkflow(pending, real); err == nil {
		t.Fatal("pending gone, real file lacks fugaro-base: passed; a deletion of the base jobs after the copy would go unnoticed")
	}
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

// TestVerifyPublicRunsEvenWhenPublishBaseIsSkipped: publish-base is skipped
// (not failed) on a tag tree with no images/base (a backport). GitHub's
// default job gate skips a job whose needs include a skipped job, so without
// always() and an explicit check, verify-public would silently skip too and
// the legacy images would publish with no anonymous-pull check at all.
func TestVerifyPublicRunsEvenWhenPublishBaseIsSkipped(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]struct {
			If string `yaml:"if"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	v, ok := wf.Jobs["verify-public"]
	if !ok {
		t.Fatal("no verify-public job")
	}
	if !strings.Contains(v.If, "always()") {
		t.Errorf("verify-public's if does not override the default needs-gate with always(): %q", v.If)
	}
	if !strings.Contains(v.If, "needs.publish.result == 'success'") {
		t.Errorf("verify-public's if does not require publish to have succeeded: %q", v.If)
	}
	for _, want := range []string{"needs['publish-base'].result != 'failure'", "needs['publish-base'].result != 'cancelled'"} {
		if !strings.Contains(v.If, want) {
			t.Errorf("verify-public's if does not guard publish-base's result (want %q): %q", want, v.If)
		}
	}
}
