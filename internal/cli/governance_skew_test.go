package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// governanceSkewLayer is a project layer that writes a top-level governance:
// block, parsed the way repoEngine's resolution parses one.
func governanceSkewLayer(t *testing.T) *config.ProjectLayer {
	t.Helper()
	l, ps := config.ParseProjectLayer([]byte("version: 1\nproject: acme\ngcp_project: acme-fugaro\ngovernance: { review_ratio: 20 }\n"),
		config.LayerAnchor{Project: "acme", GCPProject: "acme-fugaro"})
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
	return l
}

// layerWithGovernance is testProjectLayer with extra taken as a top-level
// governance: block: unlike defaults.rigor the layer file gives the block the
// root, because it is a policy the project sets for all its repositories
// rather than a default a branch may replace.
func layerWithGovernance(extra string) string {
	return strings.Replace(testProjectLayer, "gcp_project: proj-1234\n", "gcp_project: proj-1234\n"+extra, 1)
}

// TestUpdateRepoRefusesGovernanceOnOlderBaseImage is governanceSince's gate,
// the same shape as rigorSince's
// (TestUpdateRepoRefusesRigorOnOlderBaseImage): the governance: block is
// decoded strictly by every release, so a runner older than the release that
// reads it dies at bootstrap on the key, and fugaro update --repo refuses to
// deploy a job that would. The boundary is the constant's own: one release
// below is refused, the constant and one above pass, a base that is not a
// release only warns, and no base at all passes.
//
// The repository's half is the key being written (RepoGovernanceSet), not the
// block being set: `governance:` with nothing under it is a line an older
// runner's KnownFields decoder refuses, so it is refused here too, while a file
// that writes no key at all asks nothing of the image.
func TestUpdateRepoRefusesGovernanceOnOlderBaseImage(t *testing.T) {
	withBlock := &config.Config{Governance: &config.Governance{ReviewRatio: 3}, RepoGovernanceSet: true}
	nullBlock := &config.Config{RepoGovernanceSet: true}
	below, above := shiftRelease(t, governanceSince, -1), shiftRelease(t, governanceSince, 1)
	lc := func(ref string) *localcfg.Config {
		if ref == "" {
			return &localcfg.Config{}
		}
		return &localcfg.Config{BaseImages: map[string]string{"base": ref}}
	}
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	r := &initRun{o: &initOptions{}, cmd: cmd, w: &out}

	older := "ghcr.io/dimipaun/fugaro-base:" + below
	for _, cfg := range []*config.Config{withBlock, nullBlock} {
		err := r.gateGovernance(r.o, "acme/app", lc(older), cfg)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("%+v: err = %v, want refused", cfg, err)
		}
		for _, want := range []string{
			"fugaro " + governanceSince + " or later",
			older + ", release " + below,
			"fugaro update --base base",
			"fugaro.yaml's governance: block of acme/app",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%+v: %v lacks %q", cfg, err, want)
			}
		}
		if errOut.Len() != 0 {
			t.Errorf("stderr = %q, want none (refused, not warned)", errOut.String())
		}
	}

	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + governanceSince, "ghcr.io/dimipaun/fugaro-base:" + above, ""} {
		var warn bytes.Buffer
		if err := refuseGovernanceSkew("acme/app", true, false, ref, &warn); err != nil || warn.Len() != 0 {
			t.Errorf("%q: err = %v, warn = %q, want neither", ref, err, warn.String())
		}
	}
	var warn bytes.Buffer
	if err := refuseGovernanceSkew("acme/app", true, false, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", &warn); err != nil {
		t.Errorf("dev base: err = %v, want a warning only", err)
	}
	if !strings.Contains(warn.String(), "not a release base image") || !strings.Contains(warn.String(), "is not checked") {
		t.Errorf("dev base: warn = %q", warn.String())
	}

	// A configuration that writes no governance anywhere asks nothing of the
	// image.
	for _, cfg := range []*config.Config{nil, {}} {
		if err := r.gateGovernance(r.o, "acme/app", lc(older), cfg); err != nil {
			t.Errorf("%+v: err = %v, want none", cfg, err)
		}
	}

	// --forget and --plan-only deploy nothing, so neither is refused;
	// --print-vars is judged like a deploy and writes nothing to stdout.
	for _, o := range []*initOptions{{planOnly: true}, {forget: true}} {
		if err := (&initRun{o: o, cmd: cmd, w: &out}).gateGovernance(o, "acme/app", lc(older), withBlock); err != nil {
			t.Errorf("%+v: err = %v, want none", o, err)
		}
	}
	out.Reset()
	pv := &initOptions{printVars: true}
	if err := (&initRun{o: pv, cmd: cmd, w: io.Discard}).gateGovernance(pv, "acme/app", lc(older), withBlock); err == nil {
		t.Fatal("--print-vars: want refused")
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars wrote to stdout: %q", out.String())
	}
}

// TestGovernanceSkewNamesTheLayerThatWroteIt: the subject names what wrote the
// key, because the fix differs. A repository's governance: block is the
// repository's to remove; the project layer's is read by every repository of
// the project, and its refusal says so, since every repository whose job image
// is older breaks the same way. The layer's key is the top-level one: a refusal
// that sent its operator to defaults.governance would name a line their file
// cannot carry.
func TestGovernanceSkewNamesTheLayerThatWroteIt(t *testing.T) {
	r := &initRun{o: &initOptions{}, cmd: NewRootCmd(), w: io.Discard}
	older := &localcfg.Config{BaseImages: map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, governanceSince, -1)}}
	layer := governanceSkewLayer(t)
	for _, c := range []struct {
		name    string
		cfg     *config.Config
		subject string
		absent  string
	}{
		{"repo", &config.Config{RepoGovernanceSet: true}, "fugaro.yaml's governance: block of acme/app", "project layer's governance"},
		{"layer", &config.Config{Layer: layer}, "the project layer's governance: block (read for acme/app)", "fugaro.yaml's governance"},
		{"both", &config.Config{Layer: layer, RepoGovernanceSet: true}, "fugaro.yaml's governance: block and the project layer's governance: block of acme/app", ""},
	} {
		err := r.gateGovernance(r.o, "acme/app", older, c.cfg)
		if err == nil || !strings.Contains(err.Error(), c.subject) {
			t.Errorf("%s: the refusal does not name %q: %v", c.name, c.subject, err)
			continue
		}
		if c.absent != "" && strings.Contains(err.Error(), c.absent) {
			t.Errorf("%s: the refusal names %q, which did not write the key: %v", c.name, c.absent, err)
		}
		if strings.Contains(err.Error(), "defaults.governance") {
			t.Errorf("%s: the refusal names defaults.governance, which is no key of the layer file: %v", c.name, err)
		}
	}
	err := r.gateGovernance(r.o, "acme/app", older, &config.Config{Layer: layer})
	if err == nil || !strings.Contains(err.Error(), "every repository of the project") {
		t.Errorf("the layer's refusal does not say it reaches every repository: %v", err)
	}
}

// TestRepoEngineRefusesGovernanceOnAnOlderBaseImage is the gate at its call
// site, before the plan or the apply, for both places the key can be written:
// the repository's own fugaro.yaml, and the project layer the checkout resolves
// over (the file:// bucket findLayer reads, as the cascade's layer test does).
func TestRepoEngineRefusesGovernanceOnAnOlderBaseImage(t *testing.T) {
	below := shiftRelease(t, governanceSince, -1)
	base := "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:" + below + "}\n"
	const wf = "workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	const head = "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: bitbucket }\n"
	for _, c := range []struct {
		name, yaml, layer, subject string
	}{
		{"repo", head + "governance: { review_ratio: 3 }\n" + wf, "", "fugaro.yaml's governance: block of"},
		{"layer", head + wf, "version: 1\nproject: aurora\ngcp_project: proj-1234\ngovernance: { review_ratio: 20 }\n", "the project layer's governance: block"},
		// governance.tiers is the same key for the skew gate: an older runner's
		// KnownFields decoder refuses a tiers-only block exactly like any other
		// governance: line, so the gate has to judge the block and not one of its
		// keys (T8.1).
		{"repo tiers only", head + "governance: { tiers: { trivial: { max_subagents: 3 } } }\n" + wf, "", "fugaro.yaml's governance: block of"},
		{"layer tiers only", head + wf, "version: 1\nproject: aurora\ngcp_project: proj-1234\ngovernance: { tiers: { trivial: { max_subagents: 3 } } }\n", "the project layer's governance: block"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newInitRig(t)
			isolateCache(t)
			extra := base
			if c.layer == "" {
				noProjectLayerBucket(t)
			} else {
				runs := t.TempDir()
				if err := os.MkdirAll(filepath.Join(runs, "fugaro"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(runs, config.LayerKey), []byte(c.layer), 0o644); err != nil {
					t.Fatal(err)
				}
				extra += "bucket_url: file://" + runs + "\n"
			}
			r.appendConfig(t, extra)
			r.save(t)
			t.Chdir(repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", c.yaml))
			e := rigEngine(t, r, &initOptions{})
			err := e.r.repoEngine(t.Context(), ".", "", true)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("exit = %d, err = %v: want the governance key refused on this installation's base image", ExitCode(err), err)
			}
			for _, want := range []string{governanceSince, below, "fugaro update --base base", c.subject} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v lacks %q", err, want)
				}
			}
			if applies := r.ran(t, "apply"); len(applies) != 0 {
				t.Errorf("the refusal applied anyway: %v", applies)
			}
		})
	}
}

// TestGovernanceSinceIsTheReleaseTheChecklistNames holds governanceSince to
// the reader that states its number: docs/release.md's Before-you-tag bullet.
func TestGovernanceSinceIsTheReleaseTheChecklistNames(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `governanceSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no governanceSince bullet")
	}
	if !strings.Contains(bullet, "currently `\""+governanceSince+"\"`") {
		t.Errorf("docs/release.md's governanceSince bullet does not carry the constant's release (%q)", governanceSince)
	}
}

// TestGovernanceSinceIsTheReleaseGovernanceShipsIn pins the constant to the
// release the governance block ships in. It is written out because the reader
// above moves with it and stays green at any number.
func TestGovernanceSinceIsTheReleaseGovernanceShipsIn(t *testing.T) {
	if governanceSince != "0.13.0" {
		t.Errorf("governanceSince = %q: the governance block ships in CLI 0.13.0", governanceSince)
	}
}

// TestRunRefusesGovernanceOnAnOlderBaseImage is governanceSince's launch gate:
// the judge is the workflow's build record, as it is for every other
// per-workflow image gate (checkImageSince), and the boundary is the constant's
// own: one release below is refused, the constant and one above pass.
func TestRunRefusesGovernanceOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, governanceSince, -1), shiftRelease(t, governanceSince, 1)
	for _, c := range []struct {
		rel     string
		refused bool
	}{
		{below, true},
		{governanceSince, false},
		{above, false},
	} {
		t.Run(c.rel, func(t *testing.T) {
			f := newCloudFixture(t)
			configSkewCheckout(t, f, configSkewHead+"governance: { review_ratio: 3 }\n"+configSkewWorkflow)
			writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+c.rel)
			_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
			if !c.refused {
				if err != nil {
					t.Fatalf("release %s: err = %v, want launched", c.rel, err)
				}
				return
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want the launch refused", err)
			}
			for _, want := range []string{
				"fugaro.yaml's governance: block of acme/other",
				"fugaro " + governanceSince + " or later",
				"ghcr.io/dimipaun/fugaro-base:" + c.rel + ", release " + c.rel,
				"fugaro image refresh --repo acme/other --workflow svc",
				"; or remove the governance: block from fugaro.yaml",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v lacks %q", err, want)
				}
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Errorf("%d executions, want none", n)
			}
		})
	}
}

// TestRunRefusesANullGovernanceBlockOnAnOlderBaseImage: `governance:` with
// nothing under it writes the key and decodes to no block at all, so the gate
// has to judge the key and not the value — an older runner's KnownFields reader
// refuses the whole file either way, and a gate that read Governance alone
// would launch a run that dies at bootstrap on a line the operator did write.
func TestRunRefusesANullGovernanceBlockOnAnOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	configSkewCheckout(t, f, configSkewHead+"governance:\n"+configSkewWorkflow)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, governanceSince, -1))
	wantRefused(t, f, "fugaro.yaml's governance: block of acme/other needs a job image whose runner knows the governance: block (fugaro "+governanceSince+" or later)",
		"run", "--run-id", "20261008-100000-abcd", "A task")
}

// TestRunRefusesTheLayersGovernanceOnAnOlderBaseImage: the other layer the key
// can come from is the project layer, and the subject says so — that block is
// the operator's to remove, and it breaks every repository of the project whose
// job image is older, while a repository's own file says nothing about it. The
// key named is the top-level one, not defaults.governance.
func TestRunRefusesTheLayersGovernanceOnAnOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithGovernance("governance: { review_ratio: 20 }\n"))
	dir := layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	// Committed at the ref the launch resolves (the local config's base_branch
	// for acme/other, main): the gate reads the ref's own file.
	commitRef(t, dir, "main")
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, governanceSince, -1))
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{
		"the project layer's governance: block (read for acme/other)",
		"knows the project layer's governance: block",
		"fugaro image refresh --repo acme/other --workflow " + config.ImplicitWorkflow,
		"remove the governance: block from the project layer",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "fugaro.yaml's governance") {
		t.Errorf("the refusal blames the repository's file, which writes no governance: %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunRefusesBothGovernanceKeysOnAnOlderBaseImage is the launch arm where
// the repository's file and the project layer it resolves over both write the
// key: the refusal has to name both, because removing either one alone leaves
// the other behind and the run dies on it just the same. governanceSubject and
// governanceKnows' fromRepo && fromLayer branch is otherwise reached only
// through gateGovernance.
func TestRunRefusesBothGovernanceKeysOnAnOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithGovernance("governance: { review_ratio: 20 }\n"))
	dir := layerCheckout(t, f, minimalAnchored+"governance: { review_ratio: 3 }\n")
	registerOtherLocally(t, f)
	commitRef(t, dir, "main")
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, governanceSince, -1))
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{
		"fugaro.yaml's governance: block and the project layer's governance: block of acme/other",
		"knows the governance: block and the project layer's governance: block",
		"fugaro image refresh --repo acme/other --workflow " + config.ImplicitWorkflow,
		"; or remove the governance: block from fugaro.yaml",
		"remove the governance: block from the project layer",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunRefusesGovernanceOnACloneWorkflowsOlderBaseImage: a checkout: clone
// workflow has no build record to judge (it runs the installation's base image
// as it is), so the same gate is judged against base_images.base itself and
// names the fix that image has: fugaro update --base base, not an image refresh
// of a workflow that is never built.
func TestRunRefusesGovernanceOnACloneWorkflowsOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, governanceSince, -1)
	f.appendConfig(t, "base_images: {base: "+older+"}\n")
	configSkewCheckout(t, f, configSkewHead+"governance: { review_ratio: 3 }\n"+
		"workflows:\n  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n")
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{"fugaro.yaml's governance: block of acme/other", "Run fugaro update --base base", older} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestGovernanceOnACoreImageWarns: the skew gate judges the job image's
// release, and this is the other half — a job image new enough to decode
// governance: whose runner is a core build reads the block and enforces
// nothing in it, so the launch says so. A warning and never a refusal: a core
// runner ignores the block (D522), so nothing about the launch breaks, and the
// statement is about what the image does with the block, not about a licence
// (D523). The judge is the job image's edition (R3), which the build record
// carries next to the base_ref the skew gate already reads.
func TestGovernanceOnACoreImageWarns(t *testing.T) {
	f := newCloudFixture(t)
	configSkewCheckout(t, f, configSkewHead+"governance: { review_ratio: 3 }\n"+configSkewWorkflow)
	writeEditionBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", releaseTier, EditionCore)
	_, errOut, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatalf("a core job image must be warned about, never refused: %v", err)
	}
	for _, want := range []string{
		"the job image of acme/other workflow svc is a core build of fugaro",
		"reads the governance: block and ignores it",
		"fugaro image refresh --repo acme/other --workflow svc",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the run launched", n)
	}
}

// TestTaggedImageFromAnUntaggedLauncherDoesNotWarn: the edition a launch
// judges is the JOB IMAGE's, never the launcher's (R3): governance is enforced
// by the fugaro inside the job, so a core CLI built from source that launches
// onto a tagged image has governance enforced and nothing to say about it. The
// launcher in this test is the test binary, which is core.
func TestTaggedImageFromAnUntaggedLauncherDoesNotWarn(t *testing.T) {
	if Edition() != EditionCore {
		t.Fatalf("this test's launcher is %q, so it cannot be the untagged one", Edition())
	}
	f := newCloudFixture(t)
	configSkewCheckout(t, f, configSkewHead+"governance: { review_ratio: 3 }\n"+configSkewWorkflow)
	writeEditionBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", releaseTier, EditionEE)
	_, errOut, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatalf("a launch onto a tagged image must not be refused: %v", err)
	}
	if strings.Contains(errOut, "core build of fugaro") {
		t.Errorf("stderr %q warns about an image whose runner enforces the block", errOut)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the run launched", n)
	}
}

// TestGovernanceOnAnOldImageStillRefuses: the edition half is a warning and
// never a substitute for the refusal. An image older than governanceSince dies
// at bootstrap on the key, so it is refused whatever its record says about its
// edition — and no warning is printed beside a refusal, since nothing was
// launched and there is no run whose behaviour to describe.
func TestGovernanceOnAnOldImageStillRefuses(t *testing.T) {
	f := newCloudFixture(t)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, governanceSince, -1)
	configSkewCheckout(t, f, configSkewHead+"governance: { review_ratio: 3 }\n"+configSkewWorkflow)
	writeEditionBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", older, EditionCore)
	_, errOut, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro.yaml's governance: block of acme/other") {
		t.Fatalf("err = %v, want the launch refused on the image that predates the key", err)
	}
	if strings.Contains(errOut, "core build of fugaro") {
		t.Errorf("a refused launch warns about the image's edition: %q", errOut)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestTierOnACoreImageWarns: a task's tier is decoded and ignored by a core
// runner exactly as the governance: block is (D522), so a --tier launch onto a
// core image says so too — judged at the same call site, which is why run.go
// needs no change (the tier's own release gate is untouched: this image is a
// current release, so nothing refuses). A launch that asks for no tier on the
// same image says nothing: the warning is about what the run asks for. Neither
// does an image that gate refuses, which is the last two cases here.
func TestTierOnACoreImageWarns(t *testing.T) {
	f := newCloudFixture(t)
	configSkewCheckout(t, f, configSkewHead+configSkewWorkflow)
	writeEditionBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", releaseTier, EditionCore)
	_, errOut, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "--tier", "small", "A task")
	if err != nil {
		t.Fatalf("a core job image must be warned about, never refused: %v", err)
	}
	for _, want := range []string{
		"the job image of acme/other workflow svc is a core build of fugaro",
		"reads the run's tier and ignores it",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
	}

	f2 := newCloudFixture(t)
	configSkewCheckout(t, f2, configSkewHead+configSkewWorkflow)
	writeEditionBuildRecord(t, f2, mustSlug("github", "acme/other"), "svc", releaseTier, EditionCore)
	_, quiet, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet, "core build of fugaro") {
		t.Errorf("a launch that asks for no tier and writes no governance: block warns: %q", quiet)
	}

	// An image the gates refuse is not warned about: a warning describes what a
	// run does with what it was given, and a refused launch has no run. The
	// tier's own release gate is the one that fires here — this launch writes
	// no governance: block, so the skew half above never runs — and a record
	// that cannot say which base it was built from is refused by that gate too.
	f3 := newCloudFixture(t)
	configSkewCheckout(t, f3, configSkewHead+configSkewWorkflow)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, governanceSince, -1)
	writeEditionBuildRecord(t, f3, mustSlug("github", "acme/other"), "svc", older, EditionCore)
	_, oldErr, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "--tier", "small", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "the run's own tier request (--tier) needs a job image") {
		t.Fatalf("err = %v, want the tier's release gate to refuse an image older than the field", err)
	}
	if strings.Contains(oldErr, "core build of fugaro") {
		t.Errorf("a refused launch warns about the image's edition: %q", oldErr)
	}
	writeEditionBuildRecord(t, f3, mustSlug("github", "acme/other"), "svc", "", EditionCore)
	_, noBaseErr, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "--tier", "small", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "does not say which base image it was built from") {
		t.Fatalf("err = %v, want a record that names no base refused", err)
	}
	if strings.Contains(noBaseErr, "core build of fugaro") {
		t.Errorf("a record that names no base warns about the image's edition: %q", noBaseErr)
	}
	if n := len(f3.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestCloneWorkflowOnACoreRecordDoesNotWarn: a checkout: clone workflow is
// never built, so it has no build record of its own, and a record left under
// its key by an earlier life as a base-kind workflow describes an image the
// job does not run — the clone job runs the installation's base image itself.
// Its edition is therefore unknown, and an unknown image warns about nothing
// (design ee-boundary-moves §7); the tag lands in that base image's own build
// (§8), not in this gate. validate reads no record for such a workflow either,
// so the two commands agree about what they cannot see. The skew half of the
// same gate still judges the base image by its ref, so a clone workflow that
// writes governance: is not left unjudged.
func TestCloneWorkflowOnACoreRecordDoesNotWarn(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	f.appendConfig(t, "base_images: {base: "+releaseTier+"}\n")
	configSkewCheckout(t, f, configSkewHead+"governance: { review_ratio: 3 }\n"+
		"workflows:\n  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n")
	writeEditionBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", releaseTier, EditionCore)
	_, errOut, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatalf("a clone workflow writing governance: is judged by its base image, and that image is current: %v", err)
	}
	if strings.Contains(errOut, "core build of fugaro") {
		t.Errorf("stderr %q warns about a record the clone job's image does not answer to", errOut)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the run launched", n)
	}

	_, valErr, err := execute(t, "validate")
	if err != nil {
		t.Fatalf("validate judges the same file: %v", err)
	}
	if strings.Contains(valErr, "core build of fugaro") {
		t.Errorf("validate warned about a record no clone workflow's image answers to: %q", valErr)
	}
	if !strings.Contains(valErr, "needs fugaro "+governanceSince) {
		t.Errorf("validate stderr %q lacks the skew half", valErr)
	}
}

// TestCurrentReleaseImageWithNoEditionDoesNotWarn: the edition gate warns about
// one record only — the one that says core. A record written without an edition
// (one from before the field existed, or a hand-written one) is unknown, and an
// unknown image is warned about nothing (design ee-boundary-moves §7, which is
// what every --image-source mirror looks like too). The image is a current
// release, so the skew half says nothing either: a run of this file onto it
// carries no edition sentence on stderr, and validate's only stderr is the skew
// statement about the file, which prints whether or not an image can be judged
// — never an edition warning and never an unresolved note.
func TestCurrentReleaseImageWithNoEditionDoesNotWarn(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	configSkewCheckout(t, f, configSkewHead+"governance: { review_ratio: 3 }\n"+configSkewWorkflow)
	writeEditionBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", releaseTier, "")

	_, errOut, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatalf("a launch onto a current-release image must not be refused: %v", err)
	}
	// The run prints the project and recipe it resolved; what it must not print
	// is a word about the image's edition.
	for _, unwanted := range []string{"core build of fugaro", "edition"} {
		if strings.Contains(errOut, unwanted) {
			t.Errorf("a run onto a record with no edition wrote %q to stderr, which says %q", errOut, unwanted)
		}
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the run launched", n)
	}

	_, valErr, err := execute(t, "validate")
	if err != nil {
		t.Fatalf("validate judged the same file: %v", err)
	}
	for _, unwanted := range []string{"core build of fugaro", "edition unresolved"} {
		if strings.Contains(valErr, unwanted) {
			t.Errorf("validate's stderr %q says %q about an image with no edition", valErr, unwanted)
		}
	}
	if !strings.Contains(valErr, "needs fugaro "+governanceSince) {
		t.Errorf("validate's stderr %q lacks the skew half, which is a statement about the file", valErr)
	}
}

// TestValidateWarnsGovernanceOnACoreImage: validate holds no cloud env, so the
// edition half reads the records online, through the road every other command
// takes to an image from a checkout: the project config's client, the origin's
// slug, one read of the workflow's record (design §7). The file is valid
// whichever image it is built for, so this is a warning, and the skew half of
// the same sentence still prints beside it.
//
// --offline makes no such read — the flag promises the command reads neither
// the runs bucket nor the published policy — so the edition is unknown and
// validate says so instead of passing a file whose block needs enforcing as
// though it had looked; the skew half prints there too.
func TestValidateWarnsGovernanceOnACoreImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	configSkewCheckout(t, f, configSkewHead+"governance: { review_ratio: 3 }\n"+configSkewWorkflow)
	writeEditionBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", releaseTier, EditionCore)
	_, errOut, err := execute(t, "validate")
	if err != nil {
		t.Fatalf("a file that writes governance: is valid: %v", err)
	}
	for _, want := range []string{
		"core build of fugaro",
		"reads the governance: block and ignores it",
		"needs fugaro " + governanceSince,
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
	}

	_, offErr, err := execute(t, "validate", "--offline")
	if err != nil {
		t.Fatalf("--offline judges the same file: %v", err)
	}
	if strings.Contains(offErr, "core build of fugaro") {
		t.Errorf("--offline read a build record: %q", offErr)
	}
	for _, want := range []string{"unresolved", "--offline", "needs fugaro " + governanceSince} {
		if !strings.Contains(offErr, want) {
			t.Errorf("--offline stderr %q lacks %q", offErr, want)
		}
	}
	// The offline note names its own fix, the way every online note names its
	// (TestValidateNamesTheFixInAnEditionUnresolvedNote): the flag that would
	// let the read happen is the whole of what the operator has to do.
	if fix := "run validate without --offline to have the records read"; !strings.Contains(offErr, fix) {
		t.Errorf("--offline stderr %q does not name the fix %q", offErr, fix)
	}
}

// editionFixSelectConfig, editionFixCheckout, editionFixOriginSlug and
// editionFixBucket are the fixes the four online edition-unresolved notes name
// (editionUnresolved in validate.go), each in the words
// plugin/skills/setup/reference/validation.md promises them in.
const (
	editionFixSelectConfig = "name the installation with --config or --project, or run fugaro setup to create this project's config"
	editionFixCheckout     = "run validate in the repository's own checkout"
	editionFixOriginSlug   = "run validate in the checkout whose origin names the repository"
	editionFixBucket       = "check that this machine's credentials can read the installation's runs bucket"
)

// TestValidateNamesTheFixInAnEditionUnresolvedNote pins the promise
// plugin/skills/setup/reference/validation.md makes for the online notes: a
// note that says the job images' edition is unresolved names the fix that
// would let the read happen, the way the --offline note names its flag. The
// read fails at four points — no project config selected for the file's
// project, a directory that is no checkout, an origin whose slug cannot be
// resolved, a runs bucket that cannot be opened — and each has its own
// sentence for the operator to follow, so a note that stopped at "unresolved"
// would leave a file whose governance: block needs enforcing for a reason
// nobody could act on.
//
// It is the note's own wording that is pinned, not just the state: the note is
// the only thing this command can offer in each of these cases.
func TestValidateNamesTheFixInAnEditionUnresolvedNote(t *testing.T) {
	for _, tc := range []struct {
		what string
		run  func(t *testing.T) validateOutput
		fix  string
	}{
		{
			what: "no project config is selected for the file's project",
			run: func(t *testing.T) validateOutput {
				// validateGovernance isolates the project configs and judges a
				// file outside its own project's config: nothing is selected,
				// and the read cannot start.
				doc, _ := validateGovernance(t, "governance: { review_ratio: 3 }\n")
				return doc
			},
			fix: editionFixSelectConfig,
		},
		{
			what: "the directory is no checkout",
			run: func(t *testing.T) validateOutput {
				editionProjectConfig(t, "", "")
				t.Chdir(t.TempDir())
				return validateEditionDoc(t, editionRepoFile(t, "aurora"))
			},
			fix: editionFixCheckout,
		},
		{
			what: "the origin's slug cannot be resolved",
			run: func(t *testing.T) validateOutput {
				// The local config and the checkout disagree about the
				// provider, which is the resolution failing with something to
				// say rather than no checkout at all.
				editionProjectConfig(t, "  acme/other: { provider: bitbucket, base_branch: main }\n", "")
				return validateEditionDoc(t, editionCheckout(t, "acme/other", "github"))
			},
			fix: editionFixOriginSlug,
		},
		{
			what: "the runs bucket cannot be opened",
			run: func(t *testing.T) validateOutput {
				editionProjectConfig(t, "  acme/other: { provider: github, base_branch: main }\n", "ftp://bucket")
				return validateEditionDoc(t, editionCheckout(t, "acme/other", "github"))
			},
			fix: editionFixBucket,
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			doc := tc.run(t)
			note := editionNote(doc)
			if note == "" {
				t.Fatalf("no edition-unresolved note, and the read cannot have happened: %+v", doc.Notes)
			}
			if !strings.HasSuffix(note, "; "+tc.fix) {
				t.Errorf("the note %q does not end in the fix %q", note, tc.fix)
			}
		})
	}
}

// editionNote is the note's message, or "" when the command wrote none.
func editionNote(doc validateOutput) string {
	for _, n := range doc.Notes {
		if n.Path == "governance" && strings.Contains(n.Message, "the job images' edition unresolved") {
			return n.Message
		}
	}
	return ""
}

// editionProjectConfig writes a project config for aurora into a config
// directory of this test's own and selects it, so validate has a project to
// read the job images' records through. repos is the config's repos: block and
// bucket its bucket_url, either of which a case may write to make the read
// fail later than the last.
func editionProjectConfig(t *testing.T, repos, bucket string) {
	t.Helper()
	path := isolateProjects(t, t.TempDir())
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\n" +
		"user: someone@example.com\nmax_parallel: 2\nendpoints: { no_auth: true }\n"
	if bucket != "" {
		cfg += "bucket_url: " + bucket + "\n"
	}
	if repos != "" {
		cfg += "repos:\n" + repos
	}
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// editionRepoFile is a repository file that writes a governance: block — the
// key that makes the edition of the job images worth reading — naming project,
// in a directory of its own.
func editionRepoFile(t *testing.T, project string) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: " + project + "\n" +
		"git: { provider: github, base_branch: main }\nagent: { auth: api-key }\n" +
		"governance: { review_ratio: 3 }\n" +
		"workflows:\n  svc: { commands: { build: sh build.sh, test: sh test.sh } }\n"})
	return filepath.Join(dir, "fugaro.yaml")
}

// editionCheckout makes the working directory a checkout whose origin names
// repo and whose file names github as the provider, and returns the file's
// path. provider is what the checkout claims, so a local config that says
// otherwise is the slug resolution failing.
func editionCheckout(t *testing.T, repo, provider string) string {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:"+repo+".git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\n" +
		"git: { provider: " + provider + ", base_branch: main }\nagent: { auth: api-key }\n" +
		"governance: { review_ratio: 3 }\n" +
		"workflows:\n  svc: { commands: { build: sh build.sh, test: sh test.sh } }\n"})
	t.Chdir(dir)
	return filepath.Join(dir, "fugaro.yaml")
}

// validateEditionDoc runs validate --json on path and decodes what it wrote.
// A file that carries problems still writes the document, and a note is never
// a problem, so the error is not the thing this reads.
func validateEditionDoc(t *testing.T, path string) validateOutput {
	t.Helper()
	out, _, _ := execute(t, "validate", "--json", path)
	var doc validateOutput
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("validate wrote %q: %v", out, err)
	}
	return doc
}

// TestRunDoesNotGateAFileThatWritesNoGovernance: the gate is the key's, so a
// repository that writes none is never held to governanceSince.
func TestRunDoesNotGateAFileThatWritesNoGovernance(t *testing.T) {
	f := newCloudFixture(t)
	configSkewCheckout(t, f, configSkewHead+configSkewWorkflow)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, governanceSince, -1))
	if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
		t.Fatalf("a file that writes no governance: block must not be gated: %v", err)
	}
}

// validateGovernance runs fugaro validate on a repository file whose top level
// carries governanceLines, in a directory that is no checkout and has no
// project config: the file's own key is all this judges.
func validateGovernance(t *testing.T, governanceLines string) (validateOutput, error) {
	t.Helper()
	isolateProjects(t, t.TempDir())
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent: { auth: api-key }\n" +
		governanceLines + "workflows:\n  app: { commands: { build: go build ./..., test: go test ./... } }\n"})
	out, _, err := execute(t, "validate", "--json", filepath.Join(dir, "fugaro.yaml"))
	var doc validateOutput
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("output %q: %v", out, jerr)
	}
	return doc, err
}

// TestValidateWarnsThatGovernanceNeedsEveryJobImage: the file is valid — the
// skew is the installation's, not the file's — so validate warns rather than
// refusing, the shape rigorImageWarnings has, and names both ways to move an
// image (the installation's base, and a workflow's own). An explicit null
// counts: it is the key an older runner refuses.
func TestValidateWarnsThatGovernanceNeedsEveryJobImage(t *testing.T) {
	for _, lines := range []string{"governance: { review_ratio: 3 }\n", "governance:\n", "governance: { tiers: { trivial: { max_subagents: 3 } } }\n"} {
		doc, err := validateGovernance(t, lines)
		if err != nil {
			t.Fatalf("%q: err = %v, want a valid file", lines, err)
		}
		if !has(doc.Warnings, "governance", "needs fugaro "+governanceSince) {
			t.Errorf("%q: warnings = %+v, want the governance warning", lines, doc.Warnings)
		}
		for _, want := range []string{"fugaro update --base base", "fugaro image refresh"} {
			if !has(doc.Warnings, "governance", want) {
				t.Errorf("%q: the warning does not name %q: %+v", lines, want, doc.Warnings)
			}
		}
		if has(doc.Problems, "governance", "") {
			t.Errorf("%q: the key was refused, and the file is valid: %+v", lines, doc.Problems)
		}
	}
	if doc, err := validateGovernance(t, ""); err != nil || has(doc.Warnings, "governance", "") {
		t.Errorf("a file that writes no governance: block was warned about: %+v, %v", doc.Warnings, err)
	}
}

// TestValidateWarnsWhenTheProjectLayerSetsGovernance: the layer's key reaches
// every repository of the project, so validate says which of the two wrote it
// and that the fix is the layer's. The block named is the top-level one: the
// layer file has no defaults.governance to send its operator to.
func TestValidateWarnsWhenTheProjectLayerSetsGovernance(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithGovernance("governance: { review_ratio: 20 }\n"))
	layerCheckout(t, f, minimalAnchored)
	_, errOut, err := execute(t, "validate")
	if err != nil {
		t.Fatalf("a file that resolves over the layer is valid: %v", err)
	}
	for _, want := range []string{"project layer's governance: block", "needs fugaro " + governanceSince, "fugaro update --base base"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
	}
	if strings.Contains(errOut, "defaults.governance") {
		t.Errorf("the warning names defaults.governance, which is no key of the layer file: %q", errOut)
	}
}

// TestRunJudgesTheRefsGovernanceFileNotTheWorkingTree: the judge is the file the
// run checks out, not the launcher's tree (cascadeRefConfig's own reason, which
// the auth read has always followed). The checkout below commits one document
// and leaves another in the working tree, once each way round.
func TestRunJudgesTheRefsGovernanceFileNotTheWorkingTree(t *testing.T) {
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, governanceSince, -1)
	withBlock := configSkewHead + "governance: { review_ratio: 3 }\n" + configSkewWorkflow
	t.Run("the ref writes it", func(t *testing.T) {
		f := newCloudFixture(t)
		dir := configSkewCheckout(t, f, withBlock)
		testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": configSkewHead + configSkewWorkflow})
		writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", older)
		_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro.yaml's governance: block of acme/other") {
			t.Fatalf("err = %v, want the launch refused on the ref's file", err)
		}
	})
	t.Run("the working tree writes it", func(t *testing.T) {
		f := newCloudFixture(t)
		dir := configSkewCheckout(t, f, configSkewHead+configSkewWorkflow)
		if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(withBlock), 0o644); err != nil {
			t.Fatal(err)
		}
		writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", older)
		if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
			t.Fatalf("the ref writes no governance: block, so the launch must not be gated: %v", err)
		}
	})
}
