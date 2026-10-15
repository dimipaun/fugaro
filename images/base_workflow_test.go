package images_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// TestImagesWorkflowBuildsTheBaseAndHistory: after the 0.7.0 cut the images
// workflow has one base-build leg (fugaro-base, matrix amd64/arm64) and the
// history leg, no per-legacy-kind "base" job.
func TestImagesWorkflowBuildsTheBaseAndHistory(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"history", "fugaro-base", "publish", "publish-base"} {
		if _, ok := wf.Jobs[want]; !ok {
			t.Errorf("no %s job", want)
		}
	}
	if _, ok := wf.Jobs["base"]; ok {
		t.Error("the legacy per-kind \"base\" build job still exists")
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
	// publish-base must have SUCCEEDED when the kinds include base; a skipped
	// publish-base is fine only when there is no base kind (a backport tree).
	for _, want := range []string{"needs['publish-base'].result == 'success'", "!contains(needs.version.outputs.kinds, ',base,')"} {
		if !strings.Contains(v.If, want) {
			t.Errorf("verify-public's if does not gate on publish-base correctly (want %q): %q", want, v.If)
		}
	}
	for _, bad := range []string{"!= 'failure'", "!= 'cancelled'"} {
		if strings.Contains(v.If, "needs['publish-base'].result "+bad) {
			t.Errorf("verify-public's if lets a failed or cancelled publish-base through (%q): %q", bad, v.If)
		}
	}
}

// TestPublishBaseSignsByOCIDigestOnPushOnly pins the signing shape (design
// retire-image-mirror.md §2): publish-base gains id-token: write, a
// cosign-installer step at the exact pin release.yml already uses, a
// -pinned tag added only on a real tag push, and a cosign sign step gated
// the same way. It also pins the least-privilege rule the signing step
// needs and nothing else does: id-token: write on publish-base, and on no
// other job in this workflow (package writes are
// TestOnlyPublishJobsHoldPackagesWrite's).
func TestPublishBaseSignsByOCIDigestOnPushOnly(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Uses string `yaml:"uses"`
				If   string `yaml:"if"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	const cosignPin = "sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6"
	for name, job := range wf.Jobs {
		wantWrite := name == "publish-base"
		if got := job.Permissions["id-token"] == "write"; got != wantWrite {
			t.Errorf("job %q id-token permission = %q, want write only for publish-base (the keyless signing step)", name, job.Permissions["id-token"])
		}
	}
	job, ok := wf.Jobs["publish-base"]
	if !ok {
		t.Fatal("no publish-base job")
	}
	if job.Permissions["id-token"] != "write" {
		t.Errorf("permissions[id-token] = %q, want write", job.Permissions["id-token"])
	}
	var installed, taggedPinned, signed bool
	var pinnedGuarded, signGuarded bool
	for _, s := range job.Steps {
		if strings.HasPrefix(s.Uses, cosignPin) {
			installed = true
		}
		// The tag-creation step's own run script literally invokes
		// "docker buildx imagetools create $tags" (the real command, with
		// its real argument list); the sign step's run script only ever
		// *mentions* imagetools create in a comment ("docker buildx
		// imagetools create does not print...") and in a different
		// subcommand, "imagetools inspect" — both of which also contain
		// the bare substring "imagetools create" and "$VERSION-pinned".
		// Matching on "imagetools create" alone (an earlier draft of this
		// test's mistake) lets the sign step's own iteration overwrite
		// pinnedGuarded with a false negative, since the sign step's
		// guard is YAML-level (If), not this in-script check. Matching on
		// the exact invocation instead is unambiguous between the two
		// steps.
		if strings.Contains(s.Run, "docker buildx imagetools create $tags") {
			taggedPinned = strings.Contains(s.Run, "$VERSION-pinned")
			pinnedGuarded = strings.Contains(s.Run, `"$GITHUB_EVENT_NAME" = push`)
		}
		if strings.Contains(s.Run, "cosign sign ") {
			signed = true
			signGuarded = strings.Contains(s.If, "github.event_name == 'push'")
		}
	}
	if !installed || !taggedPinned || !signed {
		t.Errorf("cosign-installer=%v, -pinned tag=%v, cosign sign=%v", installed, taggedPinned, signed)
	}
	if !pinnedGuarded {
		t.Error("the -pinned tag addition must be guarded by $GITHUB_EVENT_NAME = push in its own shell script (a different gating mechanism from the sign step's YAML-level if:, since both live inside the same long-running script)")
	}
	if !signGuarded {
		t.Error("the cosign sign step's if: must require github.event_name == 'push'")
	}
}

var (
	usesRE = regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*([^@\s]+)@(\S+)`)
	shaRE  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type actionPin struct{ action, ref string }

// actionPins lists EVERY `uses: owner/repo@ref` in a workflow, one entry per
// occurrence: an action used several times must be checked each time, or a
// floating ref on one line hides behind a pinned later line.
func actionPins(data []byte) []actionPin {
	var out []actionPin
	for _, g := range usesRE.FindAllSubmatch(data, -1) {
		out = append(out, actionPin{string(g[1]), string(g[2])})
	}
	return out
}

// pinProblems names every occurrence in mine that is not pinned by a full
// commit SHA, and every occurrence of an action that another workflow also
// uses but pins to a different SHA. other is that workflow's pins and name
// is its file name.
func pinProblems(mine []actionPin, name string, other []actionPin) []string {
	var out []string
	for _, p := range mine {
		if !shaRE.MatchString(p.ref) && !strings.HasPrefix(p.action, "./") {
			out = append(out, fmt.Sprintf("%s is pinned by %q, not a full commit SHA", p.action, p.ref))
		}
	}
	for _, o := range other {
		for _, p := range mine {
			if p.action == o.action && p.ref != o.ref {
				out = append(out, fmt.Sprintf("the images workflow pins %s to %s, %s pins it to %s", p.action, p.ref, name, o.ref))
				break
			}
		}
	}
	return out
}

// TestPinProblemsFindsDriftAndFloatingRefs pins pinProblems itself: a floating
// tag and a pin that differs from another workflow's are both reported, and a
// matching pin is not.
func TestPinProblemsFindsDriftAndFloatingRefs(t *testing.T) {
	const a, b = "cf430e030ddbb5b0abf93d22962f4752f3646cd9", "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
	mine := []actionPin{{"actions/upload-artifact", a}, {"actions/checkout", a}, {"actions/checkout", "v4"}}
	got := pinProblems(mine, "release.yml", []actionPin{{"actions/upload-artifact", b}, {"actions/setup-go", a}})
	if len(got) != 2 {
		t.Fatalf("want a floating-ref (on the SECOND checkout occurrence) and a drift problem, got %q", got)
	}
	if p := pinProblems([]actionPin{{"actions/upload-artifact", a}}, "release.yml", []actionPin{{"actions/upload-artifact", a}}); len(p) != 0 {
		t.Fatalf("matching pins reported: %q", p)
	}
}

// TestActionPinsAreFullSHAs: every `uses:` in the images workflow (the pending
// file until it is copied) is pinned by a full commit SHA. Once the pending
// file is gone and the workflow is the real one, it must also agree with the
// pin of every action another workflow in .github/workflows uses (while it is
// pending, next's other workflows can lag main's Dependabot bumps, and
// Dependabot does not watch docs/pending-workflows/, so only the SHA form is
// enforced then; the person copying it checks the README's drift note).
func TestActionPinsAreFullSHAs(t *testing.T) {
	mine := actionPins(readImagesWorkflow(t))
	if len(mine) == 0 {
		t.Fatal("no `uses:` found in the images workflow")
	}
	for _, p := range pinProblems(mine, "", nil) {
		t.Error(p)
	}
	if _, err := os.Stat(pendingImagesWorkflowPath); err == nil {
		return // pending: the cross-workflow comparison starts after the copy
	}
	others, err := filepath.Glob("../.github/workflows/*.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range others {
		if filepath.Base(f) == "images.yml" {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pinProblems(mine, filepath.Base(f), actionPins(data)) {
			t.Error(p)
		}
	}
}
