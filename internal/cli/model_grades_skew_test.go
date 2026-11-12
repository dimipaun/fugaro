package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// model_grades_skew_test.go is E2's half of the plan's second Convention: a key
// a strict decoder refuses on an older runner needs its "Needs fugaro X.Y.Z"
// gate in the release that adds it. The two model grades have two homes — a
// repository's own fugaro.yaml and the project layer's defaults.model_grades —
// so the gates are planningSince's: both halves judged, from the key being
// written rather than the grade it holds.
//
// The repository's half is gated although the merge drops and records what it
// writes (G3): what an older runner refuses is the line, not the grade, and the
// line is the operator's.

// gradesLayerParsed parses a layer that writes defaults.model_grades, the way
// repoEngine's resolution parses one.
func gradesLayerParsed(t *testing.T) *config.ProjectLayer {
	t.Helper()
	l, ps := config.ParseProjectLayer([]byte("version: 1\nproject: acme\ngcp_project: acme-fugaro\ndefaults:\n  model_grades:\n    strong: { model: anthropic/claude-sonnet-5.5 }\n"),
		config.LayerAnchor{Project: "acme", GCPProject: "acme-fugaro"})
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
	return l
}

// TestModelGradesSinceIsTheReleaseTheKeyShipsIn pins the constant to the
// release the key ships in, and to the number the policy version E2 bumps names
// for the same release: a runner whose reader knows defaults.model_grades is
// the runner whose node walk carries version 7 forward, and the two numbers
// moving apart would send an operator to a release that does not carry one of
// them. It is written out because every other reader moves with the constant
// and stays green at any number (docs/release-streams.yaml has 0.23.0 current
// and Uakari 0.24.0 next, so this is the minor after the one slotSince names).
func TestModelGradesSinceIsTheReleaseTheKeyShipsIn(t *testing.T) {
	if modelGradesSince != "0.25.0" {
		t.Errorf("modelGradesSince = %q: the model_grades key ships in CLI 0.25.0", modelGradesSince)
	}
	if rel, ok := projectpolicy.VersionRelease(projectpolicy.Version); !ok || rel != modelGradesSince {
		t.Errorf("policy version %d arrived in %q, want %q: the node walk that carries it and the reader that knows the key are the same release", projectpolicy.Version, rel, modelGradesSince)
	}
}

// TestModelGradesSinceHasAReleaseBullet: modelGradesSince names 0.25.0, which
// is not tagged, so docs/release.md's Before-you-tag list is what holds the
// constant to the release actually cut — the job every other *Since constant's
// bullet does, and the one this constant's own comment claims. The test above
// pins the constant to the release this work is slotted for, and it stays green
// at any number: what a release that cut the key elsewhere would have to move
// is this constant, and the bullet is where the person cutting it reads that.
func TestModelGradesSinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `modelGradesSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no modelGradesSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+modelGradesSince+"\"`") {
		t.Errorf("docs/release.md's modelGradesSince bullet does not carry the constant's release (%q)", modelGradesSince)
	}
	// Both homes, both gates and the two directions a wrong value fails in: a
	// bullet that named the repository's block alone would have a release verify
	// the constant against an image that reads fugaro.yaml and refuses the
	// layer, which is the half that reaches every repository of the project.
	for _, want := range []string{"model_grades", "defaults.model_grades", "gateModelGrades", "checkModelGradesImage", "checkCloneModelGradesImage", "not tagged yet"} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's modelGradesSince bullet does not say %q", want)
		}
	}
}

// TestUpdateRepoRefusesModelGradesOnAnOlderBaseImage is modelGradesSince's gate
// in fugaro update --repo, the same shape as planningSince's: both files are
// decoded with KnownFields by every release, so a runner older than the key dies
// at bootstrap on it, and the gate refuses to deploy a job that would. The
// boundary is the constant's own: one release below is refused, the constant and
// one above pass, a base that is not a release only warns, and no base at all
// passes.
//
// The judged half is the key being written and not the grade it holds: a
// `model_grades:` with nothing under it, one grade and two are the same line to
// an older runner's decoder.
func TestUpdateRepoRefusesModelGradesOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, modelGradesSince, -1), shiftRelease(t, modelGradesSince, 1)
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
	for _, cfg := range []*config.Config{
		{RepoModelGradesSet: true},
		{RepoModelGradesSet: true, Layer: gradesLayerParsed(t)},
		{Layer: gradesLayerParsed(t)},
	} {
		err := r.gateModelGrades(r.o, "acme/app", lc(older), cfg)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("%+v: err = %v, want refused", cfg, err)
		}
		for _, want := range []string{"fugaro " + modelGradesSince + " or later", older + ", release " + below, "fugaro update --base base"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%+v: %v lacks %q", cfg, err, want)
			}
		}
		if errOut.Len() != 0 {
			t.Errorf("stderr = %q, want none (refused, not warned)", errOut.String())
		}
	}
	if err := r.gateModelGrades(r.o, "acme/app", lc(older), &config.Config{RepoModelGradesSet: true, Layer: gradesLayerParsed(t)}); !strings.Contains(err.Error(), "fugaro.yaml's model_grades: block and the project layer's defaults.model_grades of acme/app") {
		t.Errorf("both files: %v does not name both", err)
	}
	if err := r.gateModelGrades(r.o, "acme/app", lc(older), &config.Config{Layer: gradesLayerParsed(t)}); !strings.Contains(err.Error(), "the project layer's defaults.model_grades (read for acme/app)") {
		t.Errorf("the layer alone: %v does not name the layer", err)
	}

	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + modelGradesSince, "ghcr.io/dimipaun/fugaro-base:" + above, ""} {
		var warn bytes.Buffer
		if err := refuseModelGradesSkew("acme/app", true, false, ref, &warn); err != nil || warn.Len() != 0 {
			t.Errorf("%q: err = %v, warn = %q, want neither", ref, err, warn.String())
		}
	}
	var warn bytes.Buffer
	if err := refuseModelGradesSkew("acme/app", true, false, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", &warn); err != nil {
		t.Errorf("dev base: err = %v, want a warning only", err)
	}
	if !strings.Contains(warn.String(), "not a release base image") || !strings.Contains(warn.String(), "is not checked") {
		t.Errorf("dev base: warn = %q", warn.String())
	}

	// A configuration that writes no model_grades asks nothing of the image,
	// in either home.
	for _, cfg := range []*config.Config{nil, {}, {Layer: &config.ProjectLayer{}}} {
		if err := r.gateModelGrades(r.o, "acme/app", lc(older), cfg); err != nil {
			t.Errorf("%+v: err = %v, want none", cfg, err)
		}
	}

	// --forget and --plan-only deploy nothing, so neither is refused;
	// --print-vars is judged like a deploy and writes nothing to stdout.
	for _, o := range []*initOptions{{planOnly: true}, {forget: true}} {
		if err := (&initRun{o: o, cmd: cmd, w: &out}).gateModelGrades(o, "acme/app", lc(older), &config.Config{RepoModelGradesSet: true}); err != nil {
			t.Errorf("%+v: err = %v, want none", o, err)
		}
	}
	out.Reset()
	pv := &initOptions{printVars: true}
	if err := (&initRun{o: pv, cmd: cmd, w: io.Discard}).gateModelGrades(pv, "acme/app", lc(older), &config.Config{RepoModelGradesSet: true}); err == nil {
		t.Fatal("--print-vars: want refused")
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars wrote to stdout: %q", out.String())
	}
}

// TestRunRefusesModelGradesOnAnOlderBaseImage is modelGradesSince's launch gate
// for the repository's own half: the judge is the workflow's build record, as it
// is for every other per-workflow image gate (checkImageSince), and the boundary
// is the constant's own. The workflow here is a base-kind one, so the record is
// what the gate reads.
func TestRunRefusesModelGradesOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, modelGradesSince, -1), shiftRelease(t, modelGradesSince, 1)
	for _, c := range []struct {
		rel     string
		refused bool
	}{
		{below, true},
		{modelGradesSince, false},
		{above, false},
	} {
		t.Run(c.rel, func(t *testing.T) {
			f := newCloudFixture(t)
			configSkewCheckout(t, f, configSkewHead+
				"model_grades:\n  strong: { model: anthropic/claude-sonnet-5.5, access: ai-gateway-main }\n"+configSkewWorkflow)
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
				"fugaro.yaml's model_grades: block of acme/other",
				"knows the model_grades: block",
				"fugaro " + modelGradesSince + " or later",
				"ghcr.io/dimipaun/fugaro-base:" + c.rel + ", release " + c.rel,
				imageGateRemedy,
				"; or remove the model_grades: block from fugaro.yaml",
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

// TestRunRefusesANullGradesBlockOnAnOlderBaseImage: `model_grades:` with nothing
// under it writes the key and decodes to no grades at all, so the gate judges
// the key and not the value — an older runner's KnownFields reader refuses the
// whole file either way, and a gate that read the resolved block alone would
// launch a run that dies at bootstrap on a line the operator did write.
func TestRunRefusesANullGradesBlockOnAnOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	configSkewCheckout(t, f, configSkewHead+"model_grades:\n"+configSkewWorkflow)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, modelGradesSince, -1))
	wantRefused(t, f, "fugaro.yaml's model_grades: block of acme/other needs a job image whose runner knows the model_grades: block (fugaro "+modelGradesSince+" or later)",
		"run", "--run-id", "20261008-100000-abcd", "A task")
}

// TestRunRefusesTheLayersModelGradesOnAnOlderBaseImage: the other file the key
// can come from is the project layer's defaults.model_grades, and the subject
// says so — that block is the operator's to remove and publishing it again fixes
// every repository it reaches, while a repository's own file says nothing about
// it. The workflow is a checkout: clone one, so the layer's half is judged
// against the installation's own base image too.
func TestRunRefusesTheLayersModelGradesOnAnOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithDefaults("  model_grades:\n    strongest: { model: anthropic/claude-opus-5.5 }\n"))
	dir := layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	// Committed at the ref the launch resolves (the local config's base_branch
	// for acme/other, main): the gate reads the ref's own file.
	commitRef(t, dir, "main")
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, modelGradesSince, -1))
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{
		"the project layer's defaults.model_grades (read for acme/other)",
		"knows the project layer's defaults.model_grades",
		imageGateRemedy,
		"remove defaults.model_grades from the project layer",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "fugaro.yaml's model_grades") {
		t.Errorf("the refusal blames the repository's file, which writes no model_grades: %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// validateGrades runs fugaro validate on a repository file whose model_grades is
// gradesBlock, in a directory that is no checkout and has no project config: the
// file's own key is all this judges. It is validatePlanning's shape for the
// grades key, and the file is minimal otherwise so the grades warning is the one
// read back.
func validateGrades(t *testing.T, gradesBlock string) (validateOutput, error) {
	t.Helper()
	isolateProjects(t, t.TempDir())
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: example\ngit: { provider: github }\n" + gradesBlock +
		"agent: { auth: api-key }\nworkflows:\n  app: { commands: { build: go build ./..., test: go test ./... } }\n"})
	out, _, err := execute(t, "validate", "--json", filepath.Join(dir, "fugaro.yaml"))
	var doc validateOutput
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("output %q: %v", out, jerr)
	}
	return doc, err
}

// TestValidateWarnsThatModelGradesNeedEveryJobImage: the file is valid — the
// skew is the installation's, not the file's — so validate warns rather than
// refusing, the shape planningImageWarnings has, and names both ways to move an
// image. Every spelling of the key counts, since it is the key an older runner
// refuses, and a file that writes none is warned about nothing. A grade that
// names no model is a Problem and not a warning, which is the other half: the
// key is admitted, its value is still held to the craft rules.
func TestValidateWarnsThatModelGradesNeedEveryJobImage(t *testing.T) {
	for _, block := range []string{
		"model_grades:\n  strong: { model: anthropic/claude-sonnet-5.5 }\n",
		"model_grades:\n  strong: { model: anthropic/claude-sonnet-5.5, access: ai-gateway-main }\n",
		"model_grades:\n",
	} {
		doc, err := validateGrades(t, block)
		if err != nil {
			t.Fatalf("%q: err = %v, want a valid file", block, err)
		}
		if !has(doc.Warnings, "model_grades", "needs fugaro "+modelGradesSince) {
			t.Errorf("%q: warnings = %+v, want the grades warning", block, doc.Warnings)
		}
		for _, want := range []string{"fugaro update --base base", "fugaro image refresh"} {
			if !has(doc.Warnings, "model_grades", want) {
				t.Errorf("%q: the warning does not name %q: %+v", block, want, doc.Warnings)
			}
		}
		if has(doc.Problems, "model_grades", "") {
			t.Errorf("%q: the key was refused, and the file is valid: %+v", block, doc.Problems)
		}
	}
	if doc, err := validateGrades(t, ""); err != nil || has(doc.Warnings, "model_grades", "") {
		t.Errorf("a file that writes no model_grades was warned about: %+v, %v", doc.Warnings, err)
	}
	doc, _ := validateGrades(t, "model_grades:\n  strong: {}\n")
	if !has(doc.Problems, "model_grades.strong.model", "names the model") {
		t.Errorf("a grade with no model was not refused: %+v", doc.Problems)
	}
}

// TestValidateWarnsWhenTheProjectLayerSetsModelGrades: the layer's key reaches
// every repository of the project, so validate says which of the two wrote it
// and that the fix is the layer's.
func TestValidateWarnsWhenTheProjectLayerSetsModelGrades(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithDefaults("  model_grades:\n    strong: { model: anthropic/claude-sonnet-5.5 }\n"))
	layerCheckout(t, f, minimalAnchored)
	_, errOut, err := execute(t, "validate")
	if err != nil {
		t.Fatalf("a file that resolves over the layer is valid: %v", err)
	}
	for _, want := range []string{"project layer's defaults.model_grades", "needs fugaro " + modelGradesSince, "fugaro update --base base", "fugaro image refresh"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
	}
}

// TestPolicyPreviousVersionConverges is E2's own convergence test, the one
// CLAUDE.md's HARD RULE asks of every format task: an installation at the
// previous policy version, in a checkout whose fugaro.yaml writes model_grades,
// reaches this release with `fugaro update` alone — nothing else, and no second
// command for the key or for the node. The node the fixture carries is version 6
// (v6Node), the version the release before this one leaves behind, so the policy
// stage has to carry it forward as well as move the rules generation, the base
// image and the plugin.
//
// The gate judges the base image the installation recorded (the previous
// release's, 1.2.2, which is newer than modelGradesSince) and lets the run
// through: writing the key costs no second command. The other reading is the
// point — a gate that refused here would make writing the key an operator step,
// and `fugaro update` is the one command the user is told to run.
func TestPolicyPreviousVersionConverges(t *testing.T) {
	r := newConvergeRigYAML(t, v6Node(), refreshYAML+
		"model_grades:\n  strong: { model: anthropic/claude-sonnet-5.5, access: ai-gateway-main }\n")
	if got := convergeNode(t, r).Version; got != 6 {
		t.Fatalf("the fixture's node is version %d, want the release before this one's 6: this is not the installation the test is about", got)
	}

	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if got := convergeNode(t, r).Version; got != projectpolicy.Version {
		t.Errorf("after the update the published node is version %d, want this binary's %d: the policy stage is what carries it forward", got, projectpolicy.Version)
	}
}

// TestPolicyConvergesFromABaseImageThatPredatesGrades is the convergence the
// test above cannot reach: an installation whose recorded base image is older
// than modelGradesSince, which is what a person upgrading from the release
// before the key has, in a checkout whose fugaro.yaml writes model_grades.
// TestPolicyPreviousVersionConverges records 1.2.2, above the constant, so it
// would pass whether or not the gate was ever reached.
//
// `fugaro update` still converges in one run, and the second assertion is why:
// the images stage moves the base image onto this release before the repository
// stage reads the config its gate judges (repoEngine's loadRepoConfig, after the
// images stage in initflow's order), so the gate is handed this release's image.
// A gate that judged the config as the run found it would refuse here and turn
// writing the key into an operator step.
func TestPolicyConvergesFromABaseImageThatPredatesGrades(t *testing.T) {
	r := newConvergeRigYAML(t, v6Node(), refreshYAML+
		"model_grades:\n  strong: { model: anthropic/claude-sonnet-5.5 }\n")
	predateBaseImageTo(t, r, shiftRelease(t, modelGradesSince, -1))
	if got, want := projectConfigOf(t, r).BaseImages["base"], pinnedRef("base", shiftRelease(t, modelGradesSince, -1)); got != want {
		t.Fatalf("the installation records the base image %s, want the release before the key, %s: this is not the installation the test is about", got, want)
	}

	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if got, want := projectConfigOf(t, r).BaseImages["base"], r.pinnedBase("base", convergeRelease); got != want {
		t.Errorf("after the update the installation records the base image %s, want this release's %s: the images stage is what moves it before the repository stage's grades gate reads the config", got, want)
	}
}
