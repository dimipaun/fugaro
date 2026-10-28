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
