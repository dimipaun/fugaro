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
// mustContain is a string the real file must hold once the pending file is
// gone, so a copy-in that dropped the change fails here instead of being read
// as the workflow the pending file promised; a caller whose own assertions
// are that check passes "".
func readWorkflow(pendingPath, realPath, mustContain string) ([]byte, error) {
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
	if mustContain != "" && !strings.Contains(string(data), mustContain) {
		return nil, fmt.Errorf("%s has no %s and %s does not exist: was the pending change copied in and the pending file removed without it?", realPath, mustContain, pendingPath)
	}
	return data, nil
}

func readImagesWorkflow(t *testing.T) []byte {
	t.Helper()
	data, err := readWorkflow(pendingImagesWorkflowPath, imagesWorkflowPath, "fugaro-base:")
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
	data, err := readWorkflow(pending, real, "fugaro-base:")
	if err != nil || !strings.Contains(string(data), "fugaro-base") {
		t.Fatalf("pending present: got %q, err %v, want the pending file's content", data, err)
	}

	if err := os.Remove(pending); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("jobs:\n  fugaro-base: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, err := readWorkflow(pending, real, "fugaro-base:"); err != nil || !strings.Contains(string(data), "fugaro-base") {
		t.Fatalf("pending gone, real file holds the job: got %q, err %v, want the real file's content", data, err)
	}

	if err := os.WriteFile(real, []byte("jobs:\n  other: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkflow(pending, real, "fugaro-base:"); err == nil {
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
// workflow has one base-build leg (fugaro-base, matrix amd64/arm64), the
// history leg and the credentials service's leg, and no per-legacy-kind
// "base" job.
func TestImagesWorkflowBuildsTheBaseAndHistory(t *testing.T) {
	data := readImagesWorkflow(t)
	var wf struct {
		Jobs map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"history", "credentials", "fugaro-base", "publish", "publish-base", "publish-credentials"} {
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
		wantWrite := name == "publish" || name == "publish-base" || name == "publish-credentials"
		if write != wantWrite {
			t.Errorf("job %q packages permission = %q, want write only for publish/publish-base/publish-credentials", name, job.Permissions["packages"])
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
	// publish-credentials carries the same gate for the credentials image, so
	// a skipped publish-credentials cannot leave that image unverified either.
	for _, want := range []string{"needs['publish-base'].result == 'success'", "!contains(needs.version.outputs.kinds, ',base,')", "needs['publish-credentials'].result == 'success'", "!contains(needs.version.outputs.kinds, ',credentials,')"} {
		if !strings.Contains(v.If, want) {
			t.Errorf("verify-public's if does not gate on its publish jobs correctly (want %q): %q", want, v.If)
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
	Permissions     map[string]string `yaml:"permissions"`
	If              string            `yaml:"if"`
	ContinueOnError any               `yaml:"continue-on-error"`
	Steps           []workflowStep    `yaml:"steps"`
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
	// The identity above alone does not say the run was a TAG PUSH: a
	// workflow_dispatch run started against a tag ref gets the same
	// `...images.yml@refs/tags/vX.Y.Z` certificate subject. These two Fulcio
	// extensions say it (1.3.6.1.4.1.57264.1.2 = event_name, .1.5 =
	// repository), and cosign's verify reads them through these flags.
	signTriggerFlag = `--certificate-github-workflow-trigger "push"`
	signRepoFlag    = `--certificate-github-workflow-repository "dimipaun/fugaro"`
	// publish-base's own gate: pinned so that widening it to always() — which
	// would sign and publish on a pull request or a nightly — fails here.
	publishBaseIf = "needs.version.outputs.publish == 'true' && contains(needs.version.outputs.kinds, ',base,')"
	// publish-credentials' gate, pinned for the same reason: the credentials
	// service's image is signed and its -pinned tag written only on a real
	// release, for a tree that actually holds that image.
	publishCredentialsIf = "needs.version.outputs.publish == 'true' && contains(needs.version.outputs.kinds, ',credentials,')"
	// credentialsRepo is the repository the whole publish-credentials chain
	// works on. Every step of it names this, so a chain pointed at another
	// repository cannot sign and pin that one while leaving this image with
	// nothing `fugaro update` may resolve.
	credentialsRepo = "ghcr.io/dimipaun/fugaro-credentials"
)

// swallowedFailure is how a shell turns a failed command into a green step:
// `|| true`, `|| :`, `|| /bin/true`, `|| exit 0`. All of them are the same
// mistake, so the check catches the class, not one spelling.
var swallowedFailure = regexp.MustCompile(`\|\|\s*(true|:|/bin/true|exit 0)(\s|$)`)

// cosignFallback finds a `||` in a script that runs cosign. The list above
// is one spelling of the class, not the class: `|| echo skipped`, or any
// other fallback, makes a rejected signature a green step just as `|| true`
// does, so in a script that runs cosign ANY `||` is refused — whatever it
// falls back to, and wherever in the script it is written (a check that
// only looked for a `||` on the same text line as the word "cosign" would
// pass an edit that moved the fallback onto its own line, or that appended
// one to a multi-line cosign invocation). Comments are stripped before this
// runs (see script), so a `||` that survives only in a comment is not seen.
func cosignFallback(s string) (string, bool) {
	if !strings.Contains(s, "cosign") {
		return "", false
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "||") {
			return strings.TrimSpace(line), true
		}
	}
	return "", false
}

// setPlusE reports a line turning the shell's own fail-fast off: `set +e`,
// `set +ex`. It needs no `||` at all — with it every later command's
// failure, cosign's included, stops failing the step — so a signing step
// carrying one is refused for the same reason a fallback is.
func setPlusE(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "set" && strings.HasPrefix(f[1], "+") && strings.Contains(f[1], "e") {
			return true
		}
	}
	return false
}

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

	// id-token is the keyless signing step's, on the jobs that sign: a
	// workflow-level grant would hand it to every job, including the ones
	// that only build. publish-credentials signs the credentials service's
	// image and is checked by credentialsSigningProblems below; both signing
	// jobs, and only they, may hold it.
	if v := wf.Permissions["id-token"]; v != "" {
		bad("the workflow grants id-token: %q at the top level, so every job in it has one", v)
	}
	for name, job := range wf.Jobs {
		want := name == "publish-base" || name == "publish-credentials"
		if (job.Permissions["id-token"] == "write") != want {
			bad("job %q id-token permission = %q, want write only for the jobs that sign (publish-base, publish-credentials)", name, job.Permissions["id-token"])
		}
	}
	job, ok := wf.Jobs["publish-base"]
	if !ok {
		return append(out, "no publish-base job")
	}
	// The job's own gate, pinned exactly: `always()` (or anything else that
	// drops the publish/kinds conditions) would sign and publish the pinned
	// tag on a pull request, a nightly, or a backport tree with no base.
	if got := strings.TrimSpace(job.If); got != publishBaseIf {
		bad("the publish-base job's if is %q, want exactly %q: signing must stay behind the publish/kinds gate", got, publishBaseIf)
	}
	if job.ContinueOnError != nil && job.ContinueOnError != false {
		bad("publish-base sets continue-on-error: %v at JOB level, so a failed signature would leave the workflow green", job.ContinueOnError)
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
		// A failure anywhere in the chain must fail the job: a shell `|| true`
		// (or `|| :`, `|| /bin/true`, `|| exit 0`), any other fallback on a
		// line that runs cosign, `set +e`, and continue-on-error all turn a
		// failed signature into a green run.
		if swallowedFailure.MatchString(script(s.Run)) {
			bad("step %q swallows a failure with `|| true` (or the same thing spelled `|| :`, `|| /bin/true`, `|| exit 0`)", stepName(s))
		}
		if line, ok := cosignFallback(script(s.Run)); ok {
			bad("step %q runs cosign and carries a `||` (%q): a cosign failure must fail the step, whatever the fallback says", stepName(s), line)
		}
		if setPlusE(script(s.Run)) {
			bad("step %q turns the shell's fail-fast off with `set +e`, so a failed cosign would no longer fail the step", stepName(s))
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

	// Anything before the -pinned step that writes or names the pinned tag
	// is the earlier draft's mistake: the tag would then exist (or move)
	// before the signature does, and the whole point of it is that it never
	// names an unsigned digest. Comments do not count — only what runs.
	for i, s := range job.Steps {
		if i >= at["pinned"] {
			break
		}
		if mentions(s, "$VERSION-pinned") {
			bad("step %q names $VERSION-pinned before the pinned step: the tag must be written only after the signature is verified, and never by an earlier step", stepName(s))
		}
	}

	// The index step: the digest it hands on comes from the create's own
	// --metadata-file, and the index it published is built from the child
	// manifests this job pushed. Both are asserted as the exact commands,
	// with comments stripped: --metadata-file and containerimage.descriptor
	// also appear in the step's own comment block, so a substring test over
	// the whole run text would pass with neither command left.
	idx := script(steps["index"].Run)
	if !strings.Contains(idx, "docker buildx imagetools create $tags $digests --metadata-file index.json") {
		bad("the index step does not run `docker buildx imagetools create ... --metadata-file index.json`, so the digest the signing steps use comes from somewhere else")
	}
	if !strings.Contains(idx, `jq -r '."containerimage.descriptor".digest' index.json`) {
		bad(`the index step does not read the published digest out of index.json's containerimage.descriptor: that key, from the create's own --metadata-file, is the only digest this job may sign`)
	}
	if strings.Contains(idx, "Manifest.Digest") {
		bad("the index step learns a digest by inspecting a tag: that is a movable second registry answer")
	}
	for _, want := range []string{"$GITHUB_OUTPUT", `.manifests[]?.digest`, `"$published" != "$want"`} {
		if !strings.Contains(idx, want) {
			bad("the index step does not check what it published (%q missing): the index's children must be the manifests this job pushed, and the digest must be handed on through $GITHUB_OUTPUT", want)
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
	if !strings.Contains(script(sign.Run), `cosign sign --yes "$repo@$DIGEST"`) {
		bad("the sign step does not run `cosign sign --yes \"$repo@$DIGEST\"`: signing a tag would sign whatever that tag names at that moment")
	}
	if mentions(sign, "$VERSION") || mentions(sign, "imagetools inspect") {
		bad("the sign step reads a tag or a version instead of the digest handed to it")
	}

	// The verify step: this workflow's own identity on this tag push.
	verify := steps["verify"]
	if strings.TrimSpace(verify.If) != signPushOnly {
		bad("the verify step's if is %q, want exactly %q", strings.TrimSpace(verify.If), signPushOnly)
	}
	verifyRun := script(verify.Run)
	if !strings.Contains(verifyRun, "cosign verify") || !strings.Contains(verifyRun, `"$repo@$DIGEST"`) {
		bad("the verify step does not run `cosign verify` against the signed digest")
	}
	for _, want := range []string{signIdentity, signIssuer, signTriggerFlag, signRepoFlag} {
		if !strings.Contains(verifyRun, want) {
			bad("the verify step does not require %q: a signature from another identity, a workflow_dispatch run against the same tag ref, or another repository would pass", want)
		}
	}

	// The -pinned tag: written last, at the signed digest, and proven.
	pinned := steps["pinned"]
	if strings.TrimSpace(pinned.If) != signPushOnly {
		bad("the -pinned step's if is %q, want exactly %q: the nightly must never create or move that tag", strings.TrimSpace(pinned.If), signPushOnly)
	}
	pinnedRun := script(pinned.Run)
	if !strings.Contains(pinnedRun, `docker buildx imagetools create --tag "$repo:$VERSION-pinned" "$repo@$DIGEST"`) {
		bad("the -pinned step does not create the tag at the digest this job signed: the tag must be a carbon copy of the signed index, never a rebuild's bytes")
	}
	if !strings.Contains(pinnedRun, `"$got" != "$DIGEST"`) {
		bad("the -pinned step does not check that the tag resolves to the signed digest after creating it")
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

// credentialsSigningProblems checks publish-credentials' chain: the same
// chain publish-base has, for a single-architecture image instead of a
// multi-platform index. It is a separate function rather than a parameter of
// signingProblems because the two jobs differ in exactly two steps — how the
// digest to sign is obtained, and how the -pinned tag is written — and a
// shared function with branches for those is where one of the checks goes
// missing without anyone noticing.
//
// Why this image is signed at all is a property of its consumer, not of this
// file: `fugaro update`'s credentials stage resolves
// `fugaro-credentials:<version>-pinned`, verifies the cosign signature and
// writes the digest it names into the service's Terraform variable
// (internal/cli/init_credentials.go; internal/infra/tf refuses a tag at plan
// time). The service runs as the account that reads every model credential in
// the bundle, while every operator of the project holds
// roles/artifactregistry.writer on the registry the image comes from, so the
// tag has to be nightly-immune and the digest it names has to be signed by
// this workflow and nobody else.
func credentialsSigningProblems(wf imagesWorkflow) []string {
	var out []string
	bad := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }

	job, ok := wf.Jobs["publish-credentials"]
	if !ok {
		return append(out, "no publish-credentials job")
	}
	// The job's own gate, pinned exactly: always() (or anything else that
	// drops the publish/kinds conditions) would sign and publish the pinned
	// tag on a pull request, a nightly, or a tree with no credentials image.
	if got := strings.TrimSpace(job.If); got != publishCredentialsIf {
		bad("the publish-credentials job's if is %q, want exactly %q: signing must stay behind the publish/kinds gate", got, publishCredentialsIf)
	}
	if job.ContinueOnError != nil && job.ContinueOnError != false {
		bad("publish-credentials sets continue-on-error: %v at JOB level, so a failed signature would leave the workflow green", job.ContinueOnError)
	}
	at, steps := map[string]int{}, map[string]workflowStep{}
	for i, s := range job.Steps {
		if s.ID != "" {
			if _, dup := at[s.ID]; dup {
				bad("two publish-credentials steps share the id %q, so a later one silently wins", s.ID)
			}
			at[s.ID], steps[s.ID] = i, s
		}
		if strings.HasPrefix(s.Uses, "sigstore/cosign-installer@") && s.Uses != cosignPin {
			bad("cosign-installer is pinned as %q, want the full commit SHA %q", s.Uses, cosignPin)
		}
		// The same class of mistakes signingProblems refuses in publish-base:
		// a failure anywhere in this chain must fail the job, or a rejected
		// signature reads as a green release.
		if swallowedFailure.MatchString(script(s.Run)) {
			bad("publish-credentials step %q swallows a failure with `|| true` (or the same thing spelled `|| :`, `|| /bin/true`, `|| exit 0`)", stepName(s))
		}
		if line, ok := cosignFallback(script(s.Run)); ok {
			bad("publish-credentials step %q runs cosign and carries a `||` (%q): a cosign failure must fail the step, whatever the fallback says", stepName(s), line)
		}
		if setPlusE(script(s.Run)) {
			bad("publish-credentials step %q turns the shell's fail-fast off with `set +e`, so a failed cosign would no longer fail the step", stepName(s))
		}
		if s.ContinueOnError != nil && s.ContinueOnError != false {
			bad("publish-credentials step %q sets continue-on-error: %v, so its failure would not fail the job", stepName(s), s.ContinueOnError)
		}
		if strings.Contains(s.Run, "insecure") {
			bad("publish-credentials step %q runs cosign with an insecure option", stepName(s))
		}
	}
	for _, id := range []string{"index", "sign", "verify", "pinned"} {
		if _, ok := steps[id]; !ok {
			bad("publish-credentials has no %s step (id %q)", id, id)
		}
	}
	if len(out) > 0 {
		return out
	}

	// Every step of the chain works on the credentials service's own
	// repository. A chain pointed anywhere else would sign and pin that
	// repository and leave this image with nothing `fugaro update` may resolve.
	for _, id := range []string{"index", "sign", "verify", "pinned"} {
		if !strings.Contains(script(steps[id].Run), "repo="+credentialsRepo) {
			bad("the %s step does not work on %s: the chain must sign and pin this image's own repository", id, credentialsRepo)
		}
	}

	// Anything before the -pinned step that writes or names the pinned tag is
	// the mistake publish-base's comment warns about: the tag would exist (or
	// move) before the signature does. Comments do not count — only what runs.
	for i, s := range job.Steps {
		if i >= at["pinned"] {
			break
		}
		if mentions(s, "$VERSION-pinned") {
			bad("publish-credentials step %q names $VERSION-pinned before the pinned step: the tag must be written only after the signature is verified, and never by an earlier step", stepName(s))
		}
	}

	// The index step: the digest it hands on is the one the push itself
	// reported — this runner's own record of what the registry accepted for
	// this repository — and never one learned by asking a tag what it
	// resolves to. The shape check is what keeps an empty or truncated answer
	// from being handed on as a digest and signed.
	idx := script(steps["index"].Run)
	for _, want := range []string{
		`docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "fugaro-credentials:ci"`,
		`grep -qE '^sha256:[0-9a-f]{64}$'`,
		`echo "digest=$digest" >> "$GITHUB_OUTPUT"`,
	} {
		if !strings.Contains(idx, want) {
			bad("the index step does not take the digest from the push it just made (%q missing): the digest this job signs must come from the registry's answer to its own push, not from a tag", want)
		}
	}
	if strings.Contains(idx, "Manifest.Digest") {
		bad("the index step learns a digest by inspecting a tag: that is a movable second registry answer")
	}

	// The sign step: by digest, that digest, gated to a tag push.
	sign := steps["sign"]
	if strings.TrimSpace(sign.If) != signPushOnly {
		bad("the sign step's if is %q, want exactly %q (a widened condition signs on an event this release's identity does not cover)", strings.TrimSpace(sign.If), signPushOnly)
	}
	if got := sign.Env["DIGEST"]; got != indexDigestRef {
		bad("the sign step's DIGEST is %q, want %q: it must sign the digest the index step reported, not a version or a tag", got, indexDigestRef)
	}
	if !strings.Contains(script(sign.Run), `cosign sign --yes "$repo@$DIGEST"`) {
		bad("the sign step does not run `cosign sign --yes \"$repo@$DIGEST\"`: signing a tag would sign whatever that tag names at that moment")
	}
	if mentions(sign, "$VERSION") || mentions(sign, "imagetools inspect") {
		bad("the sign step reads a tag or a version instead of the digest handed to it")
	}

	// The verify step: this workflow's own identity on this tag push. The
	// identity alone is not enough to say "tag push" — a workflow_dispatch
	// run started against a tag ref gets the same certificate subject — so
	// the trigger and repository extensions are required too.
	verify := steps["verify"]
	if strings.TrimSpace(verify.If) != signPushOnly {
		bad("the verify step's if is %q, want exactly %q", strings.TrimSpace(verify.If), signPushOnly)
	}
	verifyRun := script(verify.Run)
	if !strings.Contains(verifyRun, "cosign verify") || !strings.Contains(verifyRun, `"$repo@$DIGEST"`) {
		bad("the verify step does not run `cosign verify` against the signed digest")
	}
	for _, want := range []string{signIdentity, signIssuer, signTriggerFlag, signRepoFlag} {
		if !strings.Contains(verifyRun, want) {
			bad("the verify step does not require %q: a signature from another identity, a workflow_dispatch run against the same tag ref, or another repository would pass", want)
		}
	}

	// The -pinned tag: written last, at the signed digest, and proven. The
	// tag is written by pushing the manifest this job already signed (see the
	// step's own comment for why not `imagetools create`), so the digest
	// under the tag is the digest of the bytes the signature covers.
	pinned := steps["pinned"]
	if strings.TrimSpace(pinned.If) != signPushOnly {
		bad("the -pinned step's if is %q, want exactly %q: the nightly must never create or move that tag", strings.TrimSpace(pinned.If), signPushOnly)
	}
	pinnedRun := script(pinned.Run)
	if !strings.Contains(pinnedRun, `docker tag "fugaro-credentials:ci" "$repo:$VERSION-pinned"`) ||
		!strings.Contains(pinnedRun, `docker push "$repo:$VERSION-pinned"`) {
		bad("the -pinned step does not push the image this job already signed under $VERSION-pinned: the tag must name the very bytes the signature covers, never a rebuild's")
	}
	if !strings.Contains(pinnedRun, `imagetools inspect "$repo:$VERSION-pinned"`) || !strings.Contains(pinnedRun, `"$got" != "$DIGEST"`) {
		bad("the -pinned step does not read the tag back from the registry and check it against the signed digest, so nothing proves what a resolve will find")
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
		bad("publish-credentials has no cosign-installer step at all")
	} else if installer > at["sign"] {
		bad("cosign-installer runs after the sign step, so the sign step has no cosign")
	}
	return out
}

// script is a run block with its comment lines removed: the shell that
// actually executes. A guard that survives only in a comment is not a guard,
// and neither is a command that survives only in one — the checks below all
// read this, never the raw run text, which carries the step's own explanatory
// comments (including the words "--metadata-file" and
// "containerimage.descriptor") alongside the commands.
func script(run string) string {
	var kept []string
	for _, line := range strings.Split(run, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// mentions reports whether anything the step *executes* (or gates on) names
// frag: its script, its if:, or any env value. Comments are excluded, as in
// script.
func mentions(s workflowStep, frag string) bool {
	if strings.Contains(script(s.Run), frag) || strings.Contains(s.If, frag) {
		return true
	}
	for _, v := range s.Env {
		if strings.Contains(v, frag) {
			return true
		}
	}
	return false
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
// check that quietly stopped matching cannot read as a passing guarantee, and
// every check site must be live: each mutation below is one weakening a
// review named or found missing, and must be reported with the specific
// message that names it — a mutation that merely produces *some* problem
// (say, from an unrelated check) would let its own check rot unnoticed. The
// base is the real workflow.
func TestSigningProblemsFindEveryMutation(t *testing.T) {
	base := parseImagesWorkflow(t, readImagesWorkflow(t))
	if got := signingProblems(base); len(got) != 0 {
		t.Fatalf("the real workflow already reports problems: %q", got)
	}
	mutations := []struct {
		what string
		do   func(imagesWorkflow) imagesWorkflow
		want string // a phrase only the check this mutation targets produces
	}{
		{"id-token: write is granted to the whole workflow", func(wf imagesWorkflow) imagesWorkflow {
			wf.Permissions = map[string]string{"contents": "read", "id-token": "write"}
			return wf
		}, "the workflow grants id-token"},
		{"id-token: write is granted to the build-only job as well", func(wf imagesWorkflow) imagesWorkflow {
			job := wf.Jobs["fugaro-base"]
			job.Permissions = map[string]string{"contents": "read", "id-token": "write", "packages": "write"}
			wf.Jobs["fugaro-base"] = job
			return wf
		}, "id-token permission ="},
		{"two steps share a step id, so a later one silently wins", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("pinned", func(s *workflowStep) { s.ID = "index" })
		}, "share the id"},
		{"cosign-installer is pinned to a tag", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withUses("sigstore/cosign-installer@", func(s *workflowStep) { s.Uses = "sigstore/cosign-installer@v4.1.2" })
		}, "cosign-installer is pinned as"},
		{"the sign step swallows a failure with `|| true`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.Run += " || true" })
		}, "swallows a failure"},
		{"the sign step swallows a failure with `|| :` instead", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.Run += ` || :` })
		}, "swallows a failure"},
		{"the verification step swallows a failure with `|| exit 0`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("verify", func(s *workflowStep) { s.Run += " || exit 0" })
		}, "swallows a failure"},
		// The list above is one spelling of the class, not the class: a
		// fallback nobody wrote down (`|| echo skipped`) turns a rejected
		// signature into a green step exactly as `|| true` does. Any `||` on
		// a line that runs cosign is refused, whatever it falls back to.
		{"the sign step falls back with `|| echo skipped`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.Run += " || echo skipped" })
		}, "runs cosign and carries a `||`"},
		{"the verify step falls back with `|| echo skipped`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("verify", func(s *workflowStep) { s.Run += " || echo skipped" })
		}, "runs cosign and carries a `||`"},
		// `set +e` needs no `||` at all: it turns the shell's fail-fast off,
		// and every later command's failure — cosign's included — stops
		// failing the step.
		{"the sign step turns fail-fast off with `set +e`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.Run = "set +e\n" + s.Run })
		}, "fail-fast"},
		{"the verify step turns fail-fast off with `set +e`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("verify", func(s *workflowStep) { s.Run = "set +e\n" + s.Run })
		}, "fail-fast"},
		{"the -pinned step turns fail-fast off with `set +ex`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("pinned", func(s *workflowStep) { s.Run = "set +ex\n" + s.Run })
		}, "fail-fast"},
		{"publish-base is marked continue-on-error at job level", func(wf imagesWorkflow) imagesWorkflow {
			job := wf.Jobs["publish-base"]
			job.ContinueOnError = true
			wf.Jobs["publish-base"] = job
			return wf
		}, "at JOB level"},
		{"the publish-base job's gate is widened to always()", func(wf imagesWorkflow) imagesWorkflow {
			job := wf.Jobs["publish-base"]
			job.If = "always()"
			wf.Jobs["publish-base"] = job
			return wf
		}, "the publish-base job's if is"},
		{"the sign step sets continue-on-error: true", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.ContinueOnError = true })
		}, "sets continue-on-error"},
		{"the verification is told to skip the transparency log", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("verify", func(s *workflowStep) { s.Run += " --insecure-ignore-tlog" })
		}, "runs cosign with an insecure option"},
		{"there is no publish-base job at all", func(wf imagesWorkflow) imagesWorkflow {
			delete(wf.Jobs, "publish-base")
			return wf
		}, "no publish-base job"},
		{"the index step is dropped", func(wf imagesWorkflow) imagesWorkflow {
			return withoutStep(wf, "index")
		}, "has no index step"},
		{"the sign step is dropped", func(wf imagesWorkflow) imagesWorkflow {
			return withoutStep(wf, "sign")
		}, "has no sign step"},
		{"the verify step is dropped", func(wf imagesWorkflow) imagesWorkflow {
			return withoutStep(wf, "verify")
		}, "has no verify step"},
		{"the -pinned step is dropped", func(wf imagesWorkflow) imagesWorkflow {
			return withoutStep(wf, "pinned")
		}, "has no pinned step"},
		{"the index step no longer takes the digest from --metadata-file, and the flag survives only in a comment", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run,
					"docker buildx imagetools create $tags $digests --metadata-file index.json",
					"docker buildx imagetools create $tags $digests\n# the create above takes --metadata-file index.json", 1)
			})
		}, "imagetools create ... --metadata-file"},
		{"the index step takes the digest from somewhere other than index.json, whose key survives only in a comment", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run,
					`index=$(jq -r '."containerimage.descriptor".digest' index.json)`,
					"# containerimage.descriptor\ndigest_url=https://example.invalid/d\nindex=$(curl -sf \"$digest_url\" | jq -r .digest)", 1)
			})
		}, "containerimage.descriptor: that key"},
		{"the index step learns the digest by inspecting the tag it just wrote", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run,
					`index=$(jq -r '."containerimage.descriptor".digest' index.json)`,
					"index=$(docker buildx imagetools inspect \"$repo:$VERSION\" --format '{{.Manifest.Digest}}')", 1)
			})
		}, "learns a digest by inspecting a tag"},
		{"the index step stops checking the children it published", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `.manifests[]?.digest`, `.config.digest`, 1)
			})
		}, "does not check what it published"},
		{"the index step stops comparing the published children with the ones it pushed", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `"$published" != "$want"`, `"$published" = "$published"`, 1)
			})
		}, "does not check what it published"},
		{"the index step stops handing the digest on to the signing steps", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `echo "digest=$index" >> "$GITHUB_OUTPUT"`, `echo "digest=$index"`, 1)
			})
		}, "does not check what it published"},
		{"the sign step is moved before the push it signs", func(wf imagesWorkflow) imagesWorkflow {
			return moveStep(wf, "sign", 0)
		}, "runs before the index step"},
		{"the sign step is moved after the verification", func(wf imagesWorkflow) imagesWorkflow {
			return moveStep(wf, "sign", len(wf.Jobs["publish-base"].Steps)-1)
		}, "the verify step runs before the sign step"},
		{"the -pinned tag is written before the signature is verified", func(wf imagesWorkflow) imagesWorkflow {
			return moveStep(wf, "pinned", 0)
		}, "written before the signature has been verified"},
		{"cosign-installer is dropped, so the sign step has no cosign", func(wf imagesWorkflow) imagesWorkflow {
			return withoutUses(wf, "sigstore/cosign-installer@")
		}, "has no cosign-installer step at all"},
		{"cosign-installer is moved after the sign step", func(wf imagesWorkflow) imagesWorkflow {
			out := cloneWorkflow(wf)
			job := out.Jobs["publish-base"]
			var moved, rest []workflowStep
			for _, s := range job.Steps {
				if strings.HasPrefix(s.Uses, "sigstore/cosign-installer@") {
					moved = append(moved, s)
					continue
				}
				rest = append(rest, s)
			}
			if len(moved) == 0 {
				return out
			}
			job.Steps = append(rest, moved...)
			out.Jobs["publish-base"] = job
			return out
		}, "cosign-installer runs after the sign step"},
		{"the sign step's condition is widened to another event", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.If = signPushOnly + " || github.event_name == 'workflow_dispatch'" })
		}, "the sign step's if is"},
		{"the sign step's condition is dropped, with the guard left in a comment", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) {
				s.If = ""
				s.Run = "# only ever on a tag push\n" + s.Run
			})
		}, "the sign step's if is"},
		{"the sign step takes a version instead of the index step's digest", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.Env["DIGEST"] = "${{ needs.version.outputs.version }}" })
		}, "the sign step's DIGEST is"},
		{"the sign step signs a tag instead of the digest", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) { s.Run = "set -eu\ncosign sign --yes \"$repo:$VERSION\"" })
		}, "signing a tag would sign whatever that tag names"},
		{"the sign step reads a tag or a version besides the digest it signs", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("sign", func(s *workflowStep) {
				s.Run += "\ndocker buildx imagetools inspect \"$repo:$VERSION\""
			})
		}, "reads a tag or a version instead of the digest"},
		{"the verify step's condition is widened to another event", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("verify", func(s *workflowStep) { s.If = signPushOnly + " || github.event_name == 'schedule'" })
		}, "the verify step's if is"},
		{"the verify step no longer runs cosign verify against the digest", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("verify", func(s *workflowStep) { s.Run = "set -eu\necho verified\n" })
		}, "against the signed digest"},
		{"the verify step no longer requires this workflow's identity", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("verify", func(s *workflowStep) {
				s.Run = strings.ReplaceAll(s.Run, signIdentity, "https://github.com/anyone/anything/.github/workflows/whatever.yml@refs/heads/main")
			})
		}, `does not require "https://github.com`},
		{"the verify step no longer requires GitHub's OIDC issuer", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("verify", func(s *workflowStep) {
				s.Run = strings.ReplaceAll(s.Run, signIssuer, "https://example.invalid/issuer")
			})
		}, `does not require "https://token.actions`},
		// The identity says the workflow and the tag ref, not that the run was
		// a tag PUSH: a workflow_dispatch run on that same ref passes the
		// identity check alone, so the trigger extension is what closes it.
		{"the verify step no longer requires the triggering event to be a push", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("verify", func(s *workflowStep) {
				s.Run = strings.ReplaceAll(s.Run, signTriggerFlag, "")
			})
		}, "--certificate-github-workflow-trigger"},
		{"the verify step accepts a workflow_dispatch run against the tag ref", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("verify", func(s *workflowStep) {
				s.Run = strings.ReplaceAll(s.Run, signTriggerFlag, `--certificate-github-workflow-trigger "workflow_dispatch"`)
			})
		}, "--certificate-github-workflow-trigger"},
		{"the verify step no longer requires this repository", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("verify", func(s *workflowStep) {
				s.Run = strings.ReplaceAll(s.Run, signRepoFlag, "")
			})
		}, "--certificate-github-workflow-repository"},
		{"the -pinned tag is written unconditionally, with the guard only in a comment", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("pinned", func(s *workflowStep) {
				s.If = ""
				s.Run = "# only ever on a tag push\n" + s.Run
			})
		}, "the -pinned step's if is"},
		{"the -pinned tag is created at a rebuild instead of the signed digest", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("pinned", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `docker buildx imagetools create --tag "$repo:$VERSION-pinned" "$repo@$DIGEST"`,
					`docker buildx imagetools create --tag "$repo:$VERSION-pinned" "$repo:$VERSION"`, 1)
			})
		}, "the tag must be a carbon copy of the signed index"},
		{"the -pinned step stops checking where the tag landed", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("pinned", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `"$got" != "$DIGEST"`, `"$got" = "$got"`, 1)
			})
		}, "does not check that the tag resolves to the signed digest"},
		{"the -pinned tag is added back to the index step's create call", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStep("index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run,
					"docker buildx imagetools create $tags $digests --metadata-file index.json",
					"docker buildx imagetools create $tags --tag $repo:$VERSION-pinned $digests --metadata-file index.json", 1)
			})
		}, "names $VERSION-pinned before the pinned step"},
	}
	for _, m := range mutations {
		got := signingProblems(m.do(cloneWorkflow(base)))
		if len(got) == 0 {
			t.Errorf("%s: signingProblems reported nothing", m.what)
			continue
		}
		var found bool
		for _, p := range got {
			if strings.Contains(p, m.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no problem names it (want a message containing %q), got %q", m.what, m.want, got)
		}
	}
}

// TestCredentialsPublishSignsByDigestOnPushOnly pins publish-credentials'
// chain: the credentials service's image is pushed, the digest that push
// reported is signed with this workflow's own keyless identity on a tag push,
// the signature is verified, and only then is the nightly-immune
// <version>-pinned tag written at that digest — the tag `fugaro update`'s
// credentials stage resolves and verifies before it writes the digest into
// the service's Terraform variable.
func TestCredentialsPublishSignsByDigestOnPushOnly(t *testing.T) {
	for _, p := range credentialsSigningProblems(parseImagesWorkflow(t, readImagesWorkflow(t))) {
		t.Error(p)
	}
}

// TestCredentialsSigningProblemsFindEveryMutation proves the checks above
// themselves, exactly as TestSigningProblemsFindEveryMutation does for
// publish-base: every check site must be live, so each mutation below is one
// weakening a review named or found missing and must be reported with the
// specific message that names it — a mutation that merely produced *some*
// problem (say, from an unrelated check) would let its own check rot
// unnoticed.
func TestCredentialsSigningProblemsFindEveryMutation(t *testing.T) {
	base := parseImagesWorkflow(t, readImagesWorkflow(t))
	if got := credentialsSigningProblems(base); len(got) != 0 {
		t.Fatalf("the real workflow already reports problems: %q", got)
	}
	const job = "publish-credentials"
	mutations := []struct {
		what string
		do   func(imagesWorkflow) imagesWorkflow
		want string // a phrase only the check this mutation targets produces
	}{
		{"there is no publish-credentials job at all", func(wf imagesWorkflow) imagesWorkflow {
			delete(wf.Jobs, job)
			return wf
		}, "no publish-credentials job"},
		{"the job's gate is widened to always()", func(wf imagesWorkflow) imagesWorkflow {
			j := wf.Jobs[job]
			j.If = "always()"
			wf.Jobs[job] = j
			return wf
		}, "the publish-credentials job's if is"},
		{"the job's gate drops the kinds condition, so a tree without the image signs", func(wf imagesWorkflow) imagesWorkflow {
			j := wf.Jobs[job]
			j.If = "needs.version.outputs.publish == 'true'"
			wf.Jobs[job] = j
			return wf
		}, "the publish-credentials job's if is"},
		{"the job is marked continue-on-error", func(wf imagesWorkflow) imagesWorkflow {
			j := wf.Jobs[job]
			j.ContinueOnError = true
			wf.Jobs[job] = j
			return wf
		}, "at JOB level"},
		{"two steps share a step id, so a later one silently wins", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "pinned", func(s *workflowStep) { s.ID = "index" })
		}, "share the id"},
		{"cosign-installer is pinned to a tag", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withUsesIn(job, "sigstore/cosign-installer@", func(s *workflowStep) { s.Uses = "sigstore/cosign-installer@v4.1.2" })
		}, "cosign-installer is pinned as"},
		{"the sign step swallows a failure with `|| true`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "sign", func(s *workflowStep) { s.Run += " || true" })
		}, "swallows a failure"},
		{"the verify step swallows a failure with `|| exit 0`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "verify", func(s *workflowStep) { s.Run += " || exit 0" })
		}, "swallows a failure"},
		{"the sign step falls back with `|| echo skipped`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "sign", func(s *workflowStep) { s.Run += " || echo skipped" })
		}, "runs cosign and carries a `||`"},
		{"the verify step turns fail-fast off with `set +e`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "verify", func(s *workflowStep) { s.Run = "set +e\n" + s.Run })
		}, "fail-fast"},
		{"the -pinned step turns fail-fast off with `set +ex`", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "pinned", func(s *workflowStep) { s.Run = "set +ex\n" + s.Run })
		}, "fail-fast"},
		{"the sign step sets continue-on-error: true", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "sign", func(s *workflowStep) { s.ContinueOnError = true })
		}, "sets continue-on-error"},
		{"the verification is told to skip the transparency log", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "verify", func(s *workflowStep) { s.Run += " --insecure-ignore-tlog" })
		}, "runs cosign with an insecure option"},
		{"the index step is dropped", func(wf imagesWorkflow) imagesWorkflow {
			return withoutStepIn(wf, job, "index")
		}, "has no index step"},
		{"the sign step is dropped", func(wf imagesWorkflow) imagesWorkflow {
			return withoutStepIn(wf, job, "sign")
		}, "has no sign step"},
		{"the verify step is dropped", func(wf imagesWorkflow) imagesWorkflow {
			return withoutStepIn(wf, job, "verify")
		}, "has no verify step"},
		{"the -pinned step is dropped", func(wf imagesWorkflow) imagesWorkflow {
			return withoutStepIn(wf, job, "pinned")
		}, "has no pinned step"},
		// The chain must sign and pin THIS image's repository: a chain that
		// ran against another one would put this release's identity on that
		// repository and leave the credentials image with nothing `fugaro
		// update` may resolve.
		{"the sign step works on another repository", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "sign", func(s *workflowStep) {
				s.Run = strings.ReplaceAll(s.Run, credentialsRepo, "ghcr.io/dimipaun/fugaro-base")
			})
		}, "does not work on ghcr.io/dimipaun/fugaro-credentials"},
		{"the -pinned step works on another repository", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "pinned", func(s *workflowStep) {
				s.Run = strings.ReplaceAll(s.Run, credentialsRepo, "ghcr.io/dimipaun/fugaro-base")
			})
		}, "does not work on ghcr.io/dimipaun/fugaro-credentials"},
		// The digest that gets signed must come from this job's own push. A
		// tag is a movable second registry answer, and this release's signing
		// identity must never cover a digest learned from one.
		{"the index step learns the digest by inspecting the tag it just pushed", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run,
					`docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "fugaro-credentials:ci"`,
					`docker buildx imagetools inspect "$repo:$VERSION" --format '{{.Manifest.Digest}}'`, 1)
			})
		}, "learns a digest by inspecting a tag"},
		{"the index step stops checking the digest it read has a digest's shape", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `grep -qE '^sha256:[0-9a-f]{64}$'`, `grep -q .`, 1)
			})
		}, "does not take the digest from the push it just made"},
		{"the index step stops handing the digest on to the signing steps", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `echo "digest=$digest" >> "$GITHUB_OUTPUT"`, `echo "digest=$digest"`, 1)
			})
		}, "does not take the digest from the push it just made"},
		{"the sign step's condition is widened to another event", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "sign", func(s *workflowStep) { s.If = signPushOnly + " || github.event_name == 'schedule'" })
		}, "the sign step's if is"},
		{"the sign step takes a version instead of the index step's digest", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "sign", func(s *workflowStep) { s.Env["DIGEST"] = "${{ needs.version.outputs.version }}" })
		}, "the sign step's DIGEST is"},
		{"the sign step signs a tag instead of the digest", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "sign", func(s *workflowStep) { s.Run = "set -eu\ncosign sign --yes \"$repo:$VERSION\"" })
		}, "signing a tag would sign whatever that tag names"},
		{"the sign step reads a tag or a version besides the digest it signs", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "sign", func(s *workflowStep) {
				s.Run += "\ndocker buildx imagetools inspect \"$repo:$VERSION\""
			})
		}, "reads a tag or a version instead of the digest"},
		{"the verify step's condition is widened to another event", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "verify", func(s *workflowStep) { s.If = signPushOnly + " || github.event_name == 'workflow_dispatch'" })
		}, "the verify step's if is"},
		{"the verify step no longer runs cosign verify against the digest", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "verify", func(s *workflowStep) { s.Run = "set -eu\necho verified\n" })
		}, "against the signed digest"},
		{"the verify step no longer requires this workflow's identity", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "verify", func(s *workflowStep) {
				s.Run = strings.ReplaceAll(s.Run, signIdentity, "https://github.com/anyone/anything/.github/workflows/whatever.yml@refs/heads/main")
			})
		}, `does not require "https://github.com`},
		{"the verify step no longer requires GitHub's OIDC issuer", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "verify", func(s *workflowStep) {
				s.Run = strings.ReplaceAll(s.Run, signIssuer, "https://example.invalid/issuer")
			})
		}, `does not require "https://token.actions`},
		{"the verify step no longer requires the triggering event to be a push", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "verify", func(s *workflowStep) {
				s.Run = strings.ReplaceAll(s.Run, signTriggerFlag, "")
			})
		}, "--certificate-github-workflow-trigger"},
		{"the verify step accepts a workflow_dispatch run against the tag ref", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "verify", func(s *workflowStep) {
				s.Run = strings.ReplaceAll(s.Run, signTriggerFlag, `--certificate-github-workflow-trigger "workflow_dispatch"`)
			})
		}, "--certificate-github-workflow-trigger"},
		{"the verify step no longer requires this repository", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "verify", func(s *workflowStep) {
				s.Run = strings.ReplaceAll(s.Run, signRepoFlag, "")
			})
		}, "--certificate-github-workflow-repository"},
		{"the -pinned tag is written unconditionally, with the guard only in a comment", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "pinned", func(s *workflowStep) {
				s.If = ""
				s.Run = "# only ever on a tag push\n" + s.Run
			})
		}, "the -pinned step's if is"},
		{"the -pinned step no longer pushes the signed manifest under the tag", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "pinned", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `docker tag "fugaro-credentials:ci" "$repo:$VERSION-pinned"`, `docker tag "fugaro-credentials:ci" "$repo:$VERSION"`, 1)
			})
		}, "does not push the image this job already signed"},
		{"the -pinned step stops checking where the tag landed", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "pinned", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, `"$got" != "$DIGEST"`, `"$got" = "$got"`, 1)
			})
		}, "does not read the tag back"},
		{"the -pinned tag is written by the index step as well", func(wf imagesWorkflow) imagesWorkflow {
			return wf.withStepIn(job, "index", func(s *workflowStep) {
				s.Run = strings.Replace(s.Run, "tags=$VERSION", "tags=\"$VERSION $VERSION-pinned\"", 1)
			})
		}, "names $VERSION-pinned before the pinned step"},
		{"the sign step is moved before the push it signs", func(wf imagesWorkflow) imagesWorkflow {
			return moveStepIn(wf, job, "sign", 0)
		}, "runs before the index step"},
		{"the sign step is moved after the verification", func(wf imagesWorkflow) imagesWorkflow {
			return moveStepIn(wf, job, "sign", len(wf.Jobs[job].Steps)-1)
		}, "the verify step runs before the sign step"},
		{"the -pinned tag is written before the signature is verified", func(wf imagesWorkflow) imagesWorkflow {
			return moveStepIn(wf, job, "pinned", 0)
		}, "written before the signature has been verified"},
		{"cosign-installer is dropped, so the sign step has no cosign", func(wf imagesWorkflow) imagesWorkflow {
			return withoutUsesIn(wf, job, "sigstore/cosign-installer@")
		}, "has no cosign-installer step at all"},
		{"cosign-installer is moved after the sign step", func(wf imagesWorkflow) imagesWorkflow {
			out := cloneWorkflow(wf)
			j := out.Jobs[job]
			var moved, rest []workflowStep
			for _, s := range j.Steps {
				if strings.HasPrefix(s.Uses, "sigstore/cosign-installer@") {
					moved = append(moved, s)
					continue
				}
				rest = append(rest, s)
			}
			if len(moved) == 0 {
				return out
			}
			j.Steps = append(rest, moved...)
			out.Jobs[job] = j
			return out
		}, "cosign-installer runs after the sign step"},
	}
	for _, m := range mutations {
		got := credentialsSigningProblems(m.do(cloneWorkflow(base)))
		if len(got) == 0 {
			t.Errorf("%s: credentialsSigningProblems reported nothing", m.what)
			continue
		}
		var found bool
		for _, p := range got {
			if strings.Contains(p, m.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no problem names it (want a message containing %q), got %q", m.what, m.want, got)
		}
	}
}

// TestCloneWorkflowIsFaithful is the mutation test's own guard: a clone that
// silently dropped a field (a new job-level `if:` or `continue-on-error`, a
// step's env) would make every mutation of that field look identical to the
// real workflow — the mutation would pass, or worse, the *real* file would
// report a problem it does not have. Comparing the two marshalled forms
// means any field added to these structs is copied or this fails.
func TestCloneWorkflowIsFaithful(t *testing.T) {
	base := parseImagesWorkflow(t, readImagesWorkflow(t))
	want, err := yaml.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := yaml.Marshal(cloneWorkflow(base))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("cloneWorkflow did not copy the workflow faithfully:\nclone:\n%s\nparsed:\n%s", got, want)
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
		out.Jobs[name] = workflowJob{
			Permissions:     cloneMap(job.Permissions),
			If:              job.If,
			ContinueOnError: job.ContinueOnError,
			Steps:           steps,
		}
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
	return wf.withStepIn("publish-base", id, f)
}

// withStepIn applies f to the first step of job jobName whose id is id, in a
// clone.
func (wf imagesWorkflow) withStepIn(jobName, id string, f func(*workflowStep)) imagesWorkflow {
	out := cloneWorkflow(wf)
	job := out.Jobs[jobName]
	for i := range job.Steps {
		if job.Steps[i].ID == id {
			f(&job.Steps[i])
			break
		}
	}
	out.Jobs[jobName] = job
	return out
}

// withUses applies f to every step in publish-base whose `uses` starts with
// prefix, in a clone. Use it for the steps that have no id (the installer).
func (wf imagesWorkflow) withUses(prefix string, f func(*workflowStep)) imagesWorkflow {
	return wf.withUsesIn("publish-base", prefix, f)
}

// withUsesIn is withUses for any job.
func (wf imagesWorkflow) withUsesIn(jobName, prefix string, f func(*workflowStep)) imagesWorkflow {
	out := cloneWorkflow(wf)
	job := out.Jobs[jobName]
	for i := range job.Steps {
		if strings.HasPrefix(job.Steps[i].Uses, prefix) {
			f(&job.Steps[i])
		}
	}
	out.Jobs[jobName] = job
	return out
}

// withoutUses drops every step in publish-base whose `uses` starts with
// prefix, in a clone.
func withoutUses(wf imagesWorkflow, prefix string) imagesWorkflow {
	return withoutUsesIn(wf, "publish-base", prefix)
}

// withoutUsesIn is withoutUses for any job.
func withoutUsesIn(wf imagesWorkflow, jobName, prefix string) imagesWorkflow {
	out := cloneWorkflow(wf)
	job := out.Jobs[jobName]
	var steps []workflowStep
	for _, s := range job.Steps {
		if !strings.HasPrefix(s.Uses, prefix) {
			steps = append(steps, s)
		}
	}
	job.Steps = steps
	out.Jobs[jobName] = job
	return out
}

// withoutStep drops the step with id id, in a clone.
func withoutStep(wf imagesWorkflow, id string) imagesWorkflow {
	return withoutStepIn(wf, "publish-base", id)
}

// withoutStepIn is withoutStep for any job.
func withoutStepIn(wf imagesWorkflow, jobName, id string) imagesWorkflow {
	out := cloneWorkflow(wf)
	job := out.Jobs[jobName]
	var steps []workflowStep
	for _, s := range job.Steps {
		if s.ID != id {
			steps = append(steps, s)
		}
	}
	job.Steps = steps
	out.Jobs[jobName] = job
	return out
}

// moveStep moves the step with id id to index to, shifting the others.
func moveStep(wf imagesWorkflow, id string, to int) imagesWorkflow {
	return moveStepIn(wf, "publish-base", id, to)
}

// moveStepIn is moveStep for any job.
func moveStepIn(wf imagesWorkflow, jobName, id string, to int) imagesWorkflow {
	out := cloneWorkflow(wf)
	job := out.Jobs[jobName]
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
	out.Jobs[jobName] = job
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
