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

// workflowStep and imagesWorkflow parse exactly the fields GitHub executes
// (uses/if/env/run/continue-on-error, the job and workflow permissions, and
// the order steps run in). Every signing check below reads this structure:
// a guard that survives only in a comment, or a permission the parsed file
// does not carry, is not seen as a guard or a permission.
type workflowStep struct {
	ID              string            `yaml:"id"`
	Name            string            `yaml:"name"`
	Uses            string            `yaml:"uses"`
	If              string            `yaml:"if"`
	Run             string            `yaml:"run"`
	Env             map[string]string `yaml:"env"`
	ContinueOnError any               `yaml:"continue-on-error"`
}

type workflowJob struct {
	Permissions map[string]string `yaml:"permissions"`
	Steps       []workflowStep    `yaml:"steps"`
}

type imagesWorkflow struct {
	Permissions map[string]string      `yaml:"permissions"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

func parseImagesWorkflow(t *testing.T, data []byte) imagesWorkflow {
	t.Helper()
	var wf imagesWorkflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	return wf
}

// The four steps of publish-base's signing chain, by id, and the exact
// values this repository's file must carry. They are constants so the
// mutation test below can restate them without drifting from the checks.
const (
	signPushOnly   = "github.event_name == 'push'"
	cosignPin      = "sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6"
	signIdentity   = "https://github.com/dimipaun/fugaro/.github/workflows/images.yml@refs/tags/v$VERSION"
	signIssuer     = "https://token.actions.githubusercontent.com"
	indexDigestRef = "${{ steps.index.outputs.digest }}" // the digest the sign step must use
)

func stepName(s workflowStep) string {
	if s.Name != "" {
		return s.Name
	}
	if s.Uses != "" {
		return s.Uses
	}
	return s.ID
}

// signingProblems names every way the base image's signing chain can be
// weakened (PR #377's supply-chain review): a digest learned from a tag
// instead of from the create's own metadata, a signature that covers a tag
// or a digest the job did not build, a failure swallowed instead of failing
// the job, a gate widened past a tag push, a guard that is only a comment,
// a permission handed to the whole workflow, or the -pinned tag written
// before the signature exists.
func signingProblems(wf imagesWorkflow) []string {
	var out []string
	bad := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }

	// id-token is the keyless signing step's, on the one job that signs: a
	// workflow-level grant would hand it to every job, including the ones
	// that only build.
	if v := wf.Permissions["id-token"]; v != "" {
		bad("the workflow grants id-token: %q at the top level, so every job in it has one", v)
	}
	for name, job := range wf.Jobs {
		want := name == "publish-base"
		if (job.Permissions["id-token"] == "write") != want {
			bad("job %q id-token permission = %q, want write only for publish-base", name, job.Permissions["id-token"])
		}
	}
	job, ok := wf.Jobs["publish-base"]
	if !ok {
		return append(out, "no publish-base job")
	}
	at, steps := map[string]int{}, map[string]workflowStep{}
	for i, s := range job.Steps {
		if s.ID != "" {
			if _, dup := at[s.ID]; dup {
				bad("two publish-base steps share the id %q, so a later one silently wins", s.ID)
			}
			at[s.ID], steps[s.ID] = i, s
		}
		if strings.HasPrefix(s.Uses, "sigstore/cosign-installer@") && s.Uses != cosignPin {
			bad("cosign-installer is pinned as %q, want the full commit SHA %q", s.Uses, cosignPin)
		}
		// A failure anywhere in the chain must fail the job: `|| true` and
		// continue-on-error both turn a failed signature into a green run.
		if strings.Contains(s.Run, "|| true") {
			bad("step %q swallows a failure with `|| true`", stepName(s))
		}
		if s.ContinueOnError != nil && s.ContinueOnError != false {
			bad("step %q sets continue-on-error: %v, so its failure would not fail the job", stepName(s), s.ContinueOnError)
		}
		// No mode that skips the transparency log or the artifact binding.
		if strings.Contains(s.Run, "insecure") {
			bad("step %q runs cosign with an insecure option", stepName(s))
		}
	}
	if _, ok := steps["index"]; !ok {
		bad("publish-base has no index step (id \"index\")")
	}
	for _, id := range []string{"sign", "verify", "pinned"} {
		if _, ok := steps[id]; !ok {
			bad("publish-base has no %s step (id %q)", id, id)
		}
	}
	if len(out) > 0 {
		return out
	}

	// The index step: the digest it hands on comes from the create's own
	// --metadata-file, and the index it published is built from the child
	// manifests this job pushed.
	idx := steps["index"]
	if !strings.Contains(idx.Run, "--metadata-file") {
		bad("the index step does not take the digest from --metadata-file, so the signed digest comes from somewhere else")
	}
	if strings.Contains(idx.Run, "Manifest.Digest") {
		bad("the index step learns a digest by inspecting a tag: that is a movable second registry answer")
	}
	for _, want := range []string{"containerimage.descriptor", "$GITHUB_OUTPUT", `.manifests[]?.digest`, `"$published" != "$want"`} {
		if !strings.Contains(idx.Run, want) {
			bad("the index step does not check what it published (%q missing): the index's children must be the manifests this job pushed", want)
		}
	}

	// The sign step: by digest, that digest, gated to a tag push.
	sign := steps["sign"]
	if strings.TrimSpace(sign.If) != signPushOnly {
		bad("the sign step's if is %q, want exactly %q (a widened condition signs on an event this release's identity does not cover)", strings.TrimSpace(sign.If), signPushOnly)
	}
	if got := sign.Env["DIGEST"]; got != indexDigestRef {
		bad("the sign step's DIGEST is %q, want %q: it must sign the digest the index step reported, not a version or a tag", got, indexDigestRef)
	}
	if !strings.Contains(sign.Run, `cosign sign --yes "$repo@$DIGEST"`) {
		bad("the sign step does not run `cosign sign --yes \"$repo@$DIGEST\"`: signing a tag would sign whatever that tag names at that moment")
	}
	if strings.Contains(sign.Run, "$VERSION") || strings.Contains(sign.Run, "imagetools inspect") {
		bad("the sign step reads a tag or a version instead of the digest handed to it")
	}

	// The verify step: this workflow's own identity on this tag push.
	verify := steps["verify"]
	if strings.TrimSpace(verify.If) != signPushOnly {
		bad("the verify step's if is %q, want exactly %q", strings.TrimSpace(verify.If), signPushOnly)
	}
	if !strings.Contains(verify.Run, "cosign verify") || !strings.Contains(verify.Run, `"$repo@$DIGEST"`) {
		bad("the verify step does not run `cosign verify` against the signed digest")
	}
	for _, want := range []string{signIdentity, signIssuer} {
		if !strings.Contains(verify.Run, want) {
			bad("the verify step does not require %q: a signature from another identity would pass", want)
		}
	}

	// The -pinned tag: written last, at the signed digest, and proven.
	pinned := steps["pinned"]
	if strings.TrimSpace(pinned.If) != signPushOnly {
		bad("the -pinned step's if is %q, want exactly %q: the nightly must never create or move that tag", strings.TrimSpace(pinned.If), signPushOnly)
	}
	for _, want := range []string{`--tag "$repo:$VERSION-pinned"`, `"$repo@$DIGEST"`, `"$got" != "$DIGEST"`} {
		if !strings.Contains(pinned.Run, want) {
			bad("the -pinned step does not %q: it must point the tag at the signed digest and assert that it landed there", want)
		}
	}
	if at["index"] > at["sign"] {
		bad("the sign step runs before the index step that produces the digest it signs")
	}
	if at["sign"] > at["verify"] {
		bad("the verify step runs before the sign step")
	}
	if at["verify"] > at["pinned"] {
		bad("the -pinned tag is written before the signature has been verified")
	}
	if installer, ok := installerIndex(job); !ok {
		bad("publish-base has no cosign-installer step at all")
	} else if installer > at["sign"] {
		bad("cosign-installer runs after the sign step, so the sign step has no cosign")
	}
	return out
}

func installerIndex(job workflowJob) (int, bool) {
	for i, s := range job.Steps {
		if strings.HasPrefix(s.Uses, "sigstore/cosign-installer@") {
			return i, true
		}
	}
	return 0, false
}

// TestPublishBaseSignsByOCIDigestOnPushOnly pins the whole signing chain
// (design retire-image-mirror.md §2, plan Task 1, PR #377's review): the
// digest that gets signed is the one the index step's own --metadata-file
// reported, the signature is verified against this workflow's identity on a
// tag push, and the nightly-immune -pinned tag is written only after that —
// with id-token: write on publish-base alone.
func TestPublishBaseSignsByOCIDigestOnPushOnly(t *testing.T) {
	for _, p := range signingProblems(parseImagesWorkflow(t, readImagesWorkflow(t))) {
		t.Error(p)
	}
}

// TestSigningProblemsFindEveryMutation proves signingProblems itself, so a
// check that quietly stopped matching cannot read as a passing guarantee:
// each mutation below is one a review named (a tag instead of a digest, a
// swallowed failure, a widened or commented-out gate, a workflow-wide
// id-token, the wrong digest source, the wrong step order), and each must be
// reported. The base is the real workflow.
func TestSigningProblemsFindEveryMutation(t *testing.T) {
	base := parseImagesWorkflow(t, readImagesWorkflow(t))
	if got := signingProblems(base); len(got) != 0 {
		t.Fatalf("the real workflow already reports problems: %q", got)
	}
	mutations := []struct {
		what string
		do   func(imagesWorkflow) imagesWorkflow
	}{
		{"the sign step signs a tag instead of the digest", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.Run = "set -eu\ncosign sign --yes \"$repo:$VERSION\"" })
		}},
		{"the sign step swallows a failure with `|| true`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.Run += " || true" })
		}},
		{"the sign step sets continue-on-error: true", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.ContinueOnError = true })
		}},
		{"the sign step is moved before the push it signs", func(wf imagesWorkflow) imagesWorkflow {
			return moveStep(wf, "sign", 0)
		}},
		{"the -pinned tag is written unconditionally, with the guard only in a comment", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("pinned", func(s *workflowStep) {
				s.If = ""
				s.Run = "# only ever on a tag push\n" + s.Run
			})
		}},
		{"id-token: write is granted to the whole workflow", func(wf imagesWorkflow) imagesWorkflow {
			wf.Permissions = map[string]string{"contents": "read", "id-token": "write"}
			return wf
		}},
		{"the sign step's condition is widened to another event", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.If = signPushOnly + " || github.event_name == 'workflow_dispatch'" })
		}},
		{"the sign step takes a version instead of the index step's digest", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.Env["DIGEST"] = "${{ needs.version.outputs.version }}" })
		}},
		{"the index step learns the digest by inspecting the tag it just wrote", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `--metadata-file index.json`, `--format '{{.Manifest.Digest}}'`, 1)
			})
		}},
		{"the index step stops checking the children it published", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `.manifests[]?.digest`, `.config.digest`, 1)
			})
		}},
		{"the -pinned tag is written before the signature is verified", func(wf imagesWorkflow) imagesWorkflow {
			return moveStep(wf, "pinned", 0)
		}},
		{"the verify step is dropped", func(wf imagesWorkflow) imagesWorkflow {
			return withoutStep(wf, "verify")
		}},
		{"the sign step is dropped", func(wf imagesWorkflow) imagesWorkflow {
			return withoutStep(wf, "sign")
		}},
		{"the -pinned step is dropped", func(wf imagesWorkflow) imagesWorkflow {
			return withoutStep(wf, "pinned")
		}},
		{"cosign-installer is pinned to a tag", func(wf imagesWorkflow) imagesWorkflow {
			job := wf.Jobs["publish-base"]
			for i := range job.Steps {
				if strings.HasPrefix(job.Steps[i].Uses, "sigstore/cosign-installer@") {
					job.Steps[i].Uses = "sigstore/cosign-installer@v4.1.2"
				}
			}
			wf.Jobs["publish-base"] = job
			return wf
		}},
		{"the verification is told to skip the transparency log", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("verify", func(s *workflowStep) { s.Run += " --insecure-ignore-tlog" })
		}},
	}
	for _, m := range mutations {
		got := signingProblems(m.do(cloneWorkflow(base)))
		if len(got) == 0 {
			t.Errorf("%s: signingProblems reported nothing", m.what)
		}
	}
}

// slice copy of the parsed workflow, so a mutation cannot reach through the
// shared backing arrays into the next mutation's base.
func cloneWorkflow(wf imagesWorkflow) imagesWorkflow {
	out := imagesWorkflow{Permissions: cloneMap(wf.Permissions), Jobs: map[string]workflowJob{}}
	for name, job := range wf.Jobs {
		steps := make([]workflowStep, len(job.Steps))
		for i, s := range job.Steps {
			s.Env = cloneMap(s.Env)
			steps[i] = s
		}
		out.Jobs[name] = workflowJob{Permissions: cloneMap(job.Permissions), Steps: steps}
	}
	return out
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// withStep applies f to the first step whose id is id, in a clone.
func (wf imagesWorkflow) withStep(id string, f func(*workflowStep)) imagesWorkflow {
	out := cloneWorkflow(wf)
	job := out.Jobs["publish-base"]
	for i := range job.Steps {
		if job.Steps[i].ID == id {
			f(&job.Steps[i])
			break
		}
	}
	out.Jobs["publish-base"] = job
	return out
}

// withoutStep drops the step with id id, in a clone.
func withoutStep(wf imagesWorkflow, id string) imagesWorkflow {
	out := cloneWorkflow(wf)
	job := out.Jobs["publish-base"]
	var steps []workflowStep
	for _, s := range job.Steps {
		if s.ID != id {
			steps = append(steps, s)
		}
	}
	job.Steps = steps
	out.Jobs["publish-base"] = job
	return out
}

// moveStep moves the step with id id to index to, shifting the others.
func moveStep(wf imagesWorkflow, id string, to int) imagesWorkflow {
	out := cloneWorkflow(wf)
	job := out.Jobs["publish-base"]
	var moved *workflowStep
	var rest []workflowStep
	for i := range job.Steps {
		if job.Steps[i].ID == id {
			s := job.Steps[i]
			moved = &s
			continue
		}
		rest = append(rest, job.Steps[i])
	}
	if moved == nil {
		return out
	}
	steps := append([]workflowStep{}, rest[:to]...)
	steps = append(steps, *moved)
	steps = append(steps, rest[to:]...)
	job.Steps = steps
	out.Jobs["publish-base"] = job
	return out
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
