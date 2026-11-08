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
	"github.com/dimipaun/fugaro/internal/testutil"
)

// planningSkewLayer parses a layer that writes defaults.planning, the way
// repoEngine's resolution parses one.
func planningSkewLayer(t *testing.T) *config.ProjectLayer {
	t.Helper()
	l, ps := config.ParseProjectLayer([]byte("version: 1\nproject: acme\ngcp_project: acme-fugaro\ndefaults:\n  planning: { decoupling: false }\n"),
		config.LayerAnchor{Project: "acme", GCPProject: "acme-fugaro"})
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
	return l
}

// TestPlanningSinceIsTheReleaseTheChecklistAndSchemasName holds planningSince
// to the three readers that state its number: docs/release.md's Before-you-tag
// bullet and the planning descriptions of both schemas. The schemas said no
// gate judged the block while the plan's CD7 was unwritten; that switch now
// exists in this tree, so both sentences name the release with it.
func TestPlanningSinceIsTheReleaseTheChecklistAndSchemasName(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `planningSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no planningSince bullet")
	}
	if !strings.Contains(bullet, "currently `\""+planningSince+"\"`") {
		t.Errorf("docs/release.md's planningSince bullet does not carry the constant's release (%q)", planningSince)
	}
	for _, path := range []string{"schemas/fugaro.schema.json", "schemas/project-layer.schema.json"} {
		var line string
		for _, l := range strings.Split(readDoc(t, path), "\n") {
			if strings.Contains(l, `"description"`) && strings.Contains(l, "docs/contract-first-decoupling") {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("%s: no planning description", path)
		}
		if !strings.Contains(line, "Fugaro before "+planningSince+" refuses the key") {
			t.Errorf("%s's planning description does not say Fugaro before %s refuses the key", path, planningSince)
		}
	}
}

// TestPlanningSinceIsTheReleaseTheKeyShipsIn pins the constant to the release
// the planning: block ships in. It is written out because the readers above
// move with it and stay green at any number, and because 0.22.0 is the release
// in flight (docs/release-streams.yaml's CLI stream is at 0.21.0, and
// docs/contract-first-decoupling/implementation-plan.md's CD6 lands the key),
// not the release after the last tag.
func TestPlanningSinceIsTheReleaseTheKeyShipsIn(t *testing.T) {
	if planningSince != "0.22.0" {
		t.Errorf("planningSince = %q: the planning: block ships in CLI 0.22.0", planningSince)
	}
}

// TestUpdateRepoRefusesPlanningOnAnOlderBaseImage is planningSince's gate in
// fugaro update --repo, the same shape as governanceSince's (a key either layer
// may write) and rigorSince's: both files are decoded with KnownFields by every
// release, so a runner older than the key dies at bootstrap on it, and the gate
// refuses to deploy a job that would. The boundary is the constant's own: one
// release below is refused, the constant and one above pass, a base that is not
// a release only warns, and no base at all passes.
//
// The judged half is the key being written and not the value it holds:
// planning.decoupling: false, planning.decoupling: true and an explicit
// `planning:` are the same line to an older runner's decoder.
func TestUpdateRepoRefusesPlanningOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, planningSince, -1), shiftRelease(t, planningSince, 1)
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
		{RepoPlanningSet: true},
		{RepoPlanningSet: true, Layer: planningSkewLayer(t)},
		{Layer: planningSkewLayer(t)},
	} {
		err := r.gatePlanning(r.o, "acme/app", lc(older), cfg)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("%+v: err = %v, want refused", cfg, err)
		}
		for _, want := range []string{"fugaro " + planningSince + " or later", older + ", release " + below, "fugaro update --base base"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%+v: %v lacks %q", cfg, err, want)
			}
		}
		if errOut.Len() != 0 {
			t.Errorf("stderr = %q, want none (refused, not warned)", errOut.String())
		}
	}
	if err := r.gatePlanning(r.o, "acme/app", lc(older), &config.Config{RepoPlanningSet: true, Layer: planningSkewLayer(t)}); !strings.Contains(err.Error(), "fugaro.yaml's planning: block and the project layer's defaults.planning of acme/app") {
		t.Errorf("both files: %v does not name both", err)
	}
	if err := r.gatePlanning(r.o, "acme/app", lc(older), &config.Config{Layer: planningSkewLayer(t)}); !strings.Contains(err.Error(), "the project layer's defaults.planning (read for acme/app)") {
		t.Errorf("the layer alone: %v does not name the layer", err)
	}

	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + planningSince, "ghcr.io/dimipaun/fugaro-base:" + above, ""} {
		var warn bytes.Buffer
		if err := refusePlanningSkew("acme/app", true, false, ref, &warn); err != nil || warn.Len() != 0 {
			t.Errorf("%q: err = %v, warn = %q, want neither", ref, err, warn.String())
		}
	}
	var warn bytes.Buffer
	if err := refusePlanningSkew("acme/app", true, false, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", &warn); err != nil {
		t.Errorf("dev base: err = %v, want a warning only", err)
	}
	if !strings.Contains(warn.String(), "not a release base image") || !strings.Contains(warn.String(), "is not checked") {
		t.Errorf("dev base: warn = %q", warn.String())
	}

	// A configuration that writes no planning: block asks nothing of the image.
	for _, cfg := range []*config.Config{nil, {}, {Layer: &config.ProjectLayer{}}} {
		if err := r.gatePlanning(r.o, "acme/app", lc(older), cfg); err != nil {
			t.Errorf("%+v: err = %v, want none", cfg, err)
		}
	}

	// --forget and --plan-only deploy nothing, so neither is refused;
	// --print-vars is judged like a deploy and writes nothing to stdout.
	for _, o := range []*initOptions{{planOnly: true}, {forget: true}} {
		if err := (&initRun{o: o, cmd: cmd, w: &out}).gatePlanning(o, "acme/app", lc(older), &config.Config{RepoPlanningSet: true}); err != nil {
			t.Errorf("%+v: err = %v, want none", o, err)
		}
	}
	out.Reset()
	pv := &initOptions{printVars: true}
	if err := (&initRun{o: pv, cmd: cmd, w: io.Discard}).gatePlanning(pv, "acme/app", lc(older), &config.Config{RepoPlanningSet: true}); err == nil {
		t.Fatal("--print-vars: want refused")
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars wrote to stdout: %q", out.String())
	}
}

// TestRunRefusesPlanningOnAnOlderBaseImage is planningSince's launch gate for
// the repository's own half: the judge is the workflow's build record, as it is
// for every other per-workflow image gate (checkImageSince), and the boundary
// is the constant's own. The file is read at the ref the launch resolves, the
// way rigorLaunchSources reads it, so this is the file the run will check out.
func TestRunRefusesPlanningOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, planningSince, -1), shiftRelease(t, planningSince, 1)
	for _, c := range []struct {
		rel     string
		refused bool
	}{
		{below, true},
		{planningSince, false},
		{above, false},
	} {
		t.Run(c.rel, func(t *testing.T) {
			f := newCloudFixture(t)
			configSkewCheckout(t, f, configSkewHead+"planning: { decoupling: false }\n"+configSkewWorkflow)
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
				"fugaro.yaml's planning: block of acme/other",
				"knows the planning: block",
				"fugaro " + planningSince + " or later",
				"ghcr.io/dimipaun/fugaro-base:" + c.rel + ", release " + c.rel,
				"fugaro image refresh --repo acme/other --workflow svc",
				"; or remove the planning: block from fugaro.yaml",
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

// TestRunRefusesANullPlanningBlockOnAnOlderBaseImage: `planning:` with nothing
// under it writes the key and decodes to no block at all, so the gate has to
// judge the key and not the value — an older runner's KnownFields reader
// refuses the whole file either way, and a gate that read the resolved block
// alone would launch a run that dies at bootstrap on a line the operator did
// write.
func TestRunRefusesANullPlanningBlockOnAnOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	configSkewCheckout(t, f, configSkewHead+"planning:\n"+configSkewWorkflow)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, planningSince, -1))
	wantRefused(t, f, "fugaro.yaml's planning: block of acme/other needs a job image whose runner knows the planning: block (fugaro "+planningSince+" or later)",
		"run", "--run-id", "20261008-100000-abcd", "A task")
}

// TestRunRefusesTheLayersPlanningOnAnOlderBaseImage: the other file the key can
// come from is the project layer's defaults.planning, and the subject says so —
// that block is the operator's to remove and publishing it again fixes every
// repository it reaches, while a repository's own file says nothing about it.
func TestRunRefusesTheLayersPlanningOnAnOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithDefaults("  planning: { decoupling: false }\n"))
	dir := layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	// Committed at the ref the launch resolves (the local config's base_branch
	// for acme/other, main): the gate reads the ref's own file.
	commitRef(t, dir, "main")
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, planningSince, -1))
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{
		"the project layer's defaults.planning (read for acme/other)",
		"knows the project layer's defaults.planning",
		"fugaro image refresh --repo acme/other --workflow " + config.ImplicitWorkflow,
		"remove defaults.planning from the project layer",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "fugaro.yaml's planning") {
		t.Errorf("the refusal blames the repository's file, which writes no planning: %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunRefusesPlanningOnACloneWorkflowsOlderBaseImage: a checkout: clone
// workflow has no build record to judge — it runs the installation's base image
// as it is — so the same gate is judged against base_images.base itself and
// names the fix that image has: fugaro update --base base, not an image refresh
// of a workflow that is never built.
func TestRunRefusesPlanningOnACloneWorkflowsOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, planningSince, -1)
	publishedLayer(t, f, layerWithDefaults("  planning: { decoupling: false }\n"))
	dir := layerCheckout(t, f, minimalAnchored+
		"workflows:\n  default: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n")
	registerOtherLocally(t, f)
	// After registerOtherLocally, which appends into the config's repos: block:
	// a top-level key written in between would leave its indented line under it.
	f.appendConfig(t, "base_images: {base: "+older+"}\n")
	commitRef(t, dir, "main")
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{"the project layer's defaults.planning (read for acme/other)", "Run fugaro update --base base", older} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// validatePlanning runs fugaro validate on a repository file whose planning: is
// planningBlock, in a directory that is no checkout and has no project config:
// the file's own key is all this judges. It is validateAuthor's shape for the
// planning key.
func validatePlanning(t *testing.T, planningBlock string) (validateOutput, error) {
	t.Helper()
	isolateProjects(t, t.TempDir())
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: example\ngit: { provider: github }\n" + planningBlock +
		"agent: { auth: api-key }\nworkflows:\n  app: { commands: { build: go build ./..., test: go test ./... } }\n"})
	out, _, err := execute(t, "validate", "--json", filepath.Join(dir, "fugaro.yaml"))
	var doc validateOutput
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("output %q: %v", out, jerr)
	}
	return doc, err
}

// TestValidateWarnsThatPlanningNeedsEveryJobImage: the file is valid — the skew
// is the installation's, not the file's — so validate warns rather than
// refusing, the shape rigorImageWarnings has, and names both ways to move an
// image (the installation's base, and a workflow's own). Every spelling counts,
// since it is the key an older runner refuses, and a file that writes none is
// warned about nothing.
func TestValidateWarnsThatPlanningNeedsEveryJobImage(t *testing.T) {
	for _, block := range []string{
		"planning: { decoupling: false }\n",
		"planning: { decoupling: true }\n",
		"planning:\n",
	} {
		doc, err := validatePlanning(t, block)
		if err != nil {
			t.Fatalf("%q: err = %v, want a valid file", block, err)
		}
		if !has(doc.Warnings, "planning", "needs fugaro "+planningSince) {
			t.Errorf("%q: warnings = %+v, want the planning warning", block, doc.Warnings)
		}
		for _, want := range []string{"fugaro update --base base", "fugaro image refresh"} {
			if !has(doc.Warnings, "planning", want) {
				t.Errorf("%q: the warning does not name %q: %+v", block, want, doc.Warnings)
			}
		}
		if has(doc.Problems, "planning", "") {
			t.Errorf("%q: the key was refused, and the file is valid: %+v", block, doc.Problems)
		}
	}
	if doc, err := validatePlanning(t, ""); err != nil || has(doc.Warnings, "planning", "") {
		t.Errorf("a file that writes no planning: was warned about: %+v, %v", doc.Warnings, err)
	}
}

// TestValidateWarnsWhenTheProjectLayerSetsPlanning: the layer's key reaches
// every repository of the project, so validate says which of the two wrote it
// and that the fix is the layer's.
func TestValidateWarnsWhenTheProjectLayerSetsPlanning(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithDefaults("  planning: { decoupling: false }\n"))
	layerCheckout(t, f, minimalAnchored)
	_, errOut, err := execute(t, "validate")
	if err != nil {
		t.Fatalf("a file that resolves over the layer is valid: %v", err)
	}
	for _, want := range []string{"project layer's defaults.planning", "needs fugaro " + planningSince, "fugaro update --base base", "fugaro image refresh"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
	}
}
