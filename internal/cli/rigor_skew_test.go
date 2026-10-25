package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// rigorSkewLayer is a project layer that writes defaults.rigor, parsed the way
// repoEngine's resolution parses one.
func rigorSkewLayer(t *testing.T) *config.ProjectLayer {
	t.Helper()
	l, ps := config.ParseProjectLayer([]byte("version: 1\nproject: acme\ngcp_project: acme-fugaro\ndefaults:\n  rigor: { default: critical }\n"),
		config.LayerAnchor{Project: "acme", GCPProject: "acme-fugaro"})
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
	return l
}

// TestUpdateRepoRefusesRigorOnOlderBaseImage is rigorSince's gate, the same
// shape as cascadeSince's (TestInitRepoRefusesCascadeOnOlderBaseImage): the
// rigor: block and defaults.rigor are decoded strictly by every release, so a
// runner older than the release that reads them dies at bootstrap on the key,
// and fugaro update --repo refuses to deploy a job that would. The boundary is
// the constant's own: one release below is refused, the constant and one above
// pass, a base that is not a release only warns, and no base at all passes.
func TestUpdateRepoRefusesRigorOnOlderBaseImage(t *testing.T) {
	withBlock := &config.Config{RepoRigor: &config.Rigor{Default: rigor.Hotfix}}
	below, above := shiftRelease(t, rigorSince, -1), shiftRelease(t, rigorSince, 1)
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
	err := r.gateRigor(r.o, "acme/app", lc(older), withBlock)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want refused", err)
	}
	for _, want := range []string{
		"fugaro " + rigorSince + " or later",
		older + ", release " + below,
		"fugaro update --base base",
		"fugaro.yaml's rigor: block of acme/app",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want none (refused, not warned)", errOut.String())
	}

	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + rigorSince, "ghcr.io/dimipaun/fugaro-base:" + above, ""} {
		var warn bytes.Buffer
		if err := refuseRigorSkew("acme/app", true, false, ref, &warn); err != nil || warn.Len() != 0 {
			t.Errorf("%q: err = %v, warn = %q, want neither", ref, err, warn.String())
		}
	}
	var warn bytes.Buffer
	if err := refuseRigorSkew("acme/app", true, false, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", &warn); err != nil {
		t.Errorf("dev base: err = %v, want a warning only", err)
	}
	if !strings.Contains(warn.String(), "not a release base image") || !strings.Contains(warn.String(), "is not checked") {
		t.Errorf("dev base: warn = %q", warn.String())
	}

	// A configuration that writes no rigor anywhere asks nothing of the image.
	for _, cfg := range []*config.Config{nil, {}} {
		if err := r.gateRigor(r.o, "acme/app", lc(older), cfg); err != nil {
			t.Errorf("%+v: err = %v, want none", cfg, err)
		}
	}

	// --forget and --plan-only deploy nothing, so neither is refused;
	// --print-vars is judged like a deploy and writes nothing to stdout.
	for _, o := range []*initOptions{{planOnly: true}, {forget: true}} {
		if err := (&initRun{o: o, cmd: cmd, w: &out}).gateRigor(o, "acme/app", lc(older), withBlock); err != nil {
			t.Errorf("%+v: err = %v, want none", o, err)
		}
	}
	out.Reset()
	pv := &initOptions{printVars: true}
	if err := (&initRun{o: pv, cmd: cmd, w: io.Discard}).gateRigor(pv, "acme/app", lc(older), withBlock); err == nil {
		t.Fatal("--print-vars: want refused")
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars wrote to stdout: %q", out.String())
	}
}

// TestRigorSkewNamesTheLayerThatWroteIt: the subject names what wrote the
// key, because the fix differs. A repository's rigor: block is the
// repository's to remove; defaults.rigor in the project layer is read by every
// repository of the project, and its refusal says so, since every repository
// whose job image is older breaks the same way.
func TestRigorSkewNamesTheLayerThatWroteIt(t *testing.T) {
	r := &initRun{o: &initOptions{}, cmd: NewRootCmd(), w: io.Discard}
	older := &localcfg.Config{BaseImages: map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, rigorSince, -1)}}
	layer := rigorSkewLayer(t)
	for _, c := range []struct {
		name    string
		cfg     *config.Config
		subject string
		absent  string
	}{
		{"repo", &config.Config{RepoRigor: &config.Rigor{}}, "fugaro.yaml's rigor: block of acme/app", "defaults.rigor"},
		{"layer", &config.Config{Layer: layer}, "the project layer's defaults.rigor (read for acme/app)", "fugaro.yaml's rigor"},
		{"both", &config.Config{Layer: layer, RepoRigor: &config.Rigor{}}, "fugaro.yaml's rigor: block and the project layer's defaults.rigor of acme/app", ""},
	} {
		err := r.gateRigor(r.o, "acme/app", older, c.cfg)
		if err == nil || !strings.Contains(err.Error(), c.subject) {
			t.Errorf("%s: the refusal does not name %q: %v", c.name, c.subject, err)
			continue
		}
		if c.absent != "" && strings.Contains(err.Error(), c.absent) {
			t.Errorf("%s: the refusal names %q, which did not write the key: %v", c.name, c.absent, err)
		}
	}
	err := r.gateRigor(r.o, "acme/app", older, &config.Config{Layer: layer})
	if err == nil || !strings.Contains(err.Error(), "every repository of the project") {
		t.Errorf("the layer's refusal does not say it reaches every repository: %v", err)
	}
}

// TestRepoEngineRefusesRigorOnAnOlderBaseImage is the gate at its call site,
// before the plan or the apply, for both places the key can be written: the
// repository's own fugaro.yaml, and the project layer the checkout resolves
// over (the file:// bucket findLayer reads, as the cascade's layer test does).
func TestRepoEngineRefusesRigorOnAnOlderBaseImage(t *testing.T) {
	below := shiftRelease(t, rigorSince, -1)
	base := "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:" + below + "}\n"
	const wf = "workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	const head = "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: bitbucket }\n"
	for _, c := range []struct {
		name, yaml, layer, subject string
	}{
		{"repo", head + "rigor: { default: hotfix }\n" + wf, "", "fugaro.yaml's rigor: block of"},
		{"layer", head + wf, "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  rigor: { default: critical }\n", "the project layer's defaults.rigor"},
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
				t.Fatalf("exit = %d, err = %v: want the rigor key refused on this installation's base image", ExitCode(err), err)
			}
			for _, want := range []string{rigorSince, below, "fugaro update --base base", c.subject} {
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

// TestRigorSinceIsTheReleaseTheChecklistAndSchemasName holds rigorSince to the
// three readers that state its number: docs/release.md's Before-you-tag bullet
// and the rigor descriptions of both schemas.
func TestRigorSinceIsTheReleaseTheChecklistAndSchemasName(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `rigorSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no rigorSince bullet")
	}
	if !strings.Contains(bullet, "currently `\""+rigorSince+"\"`") {
		t.Errorf("docs/release.md's rigorSince bullet does not carry the constant's release (%q)", rigorSince)
	}
	for _, path := range []string{"../../schemas/fugaro.schema.json", "../../schemas/project-layer.schema.json"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var line string
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, `"description"`) && strings.Contains(l, "rigor-modes/spec.md") {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("%s: no rigor description", path)
		}
		if !strings.Contains(line, "before "+rigorSince) {
			t.Errorf("%s's rigor description does not say Fugaro before %s refuses the key", path, rigorSince)
		}
	}
}

// TestRigorSinceIsTheReleaseRigorShipsIn pins the constant to the release the
// rigor modes ship in. It is written out because the readers above move
// together and stay green with each other at any number.
func TestRigorSinceIsTheReleaseRigorShipsIn(t *testing.T) {
	if rigorSince != "0.11.0" {
		t.Errorf("rigorSince = %q: the rigor modes ship in CLI 0.11.0", rigorSince)
	}
}

// rigorSkewHead and rigorSkewWorkflow are the repository file the launch tests
// below check out: the workflow svc, whose job the fixture has.
const (
	rigorSkewHead     = "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nagent: { auth: api-key }\n"
	rigorSkewWorkflow = "workflows:\n  svc: { commands: { build: sh build.sh, test: sh test.sh } }\n"
)

// rigorSkewCheckout makes the working directory a checkout of acme/other whose
// fugaro.yaml is repoYAML.
func rigorSkewCheckout(t *testing.T, f *cloudFixture, repoYAML string) {
	t.Helper()
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": repoYAML, "build.sh": "true\n", "test.sh": "true\n"})
	t.Chdir(dir)
}

// TestRunRefusesRigorOnAnOlderBaseImage is rigorSince's launch gate, which
// 0.11.0 shipped without: only fugaro update --repo judged the key (the
// release note's own known limitation), so a launch from a repository whose
// file writes a rigor: block onto a job image older than rigorSince was
// deployed and then died at bootstrap, on a key nobody could do anything about
// any more. The judge is the workflow's build record, as it is for every other
// per-workflow image gate (checkImageSince), and the boundary is the
// constant's own: one release below is refused, the constant and one above
// pass.
func TestRunRefusesRigorOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, rigorSince, -1), shiftRelease(t, rigorSince, 1)
	for _, c := range []struct {
		rel     string
		refused bool
	}{
		{below, true},
		{rigorSince, false},
		{above, false},
	} {
		t.Run(c.rel, func(t *testing.T) {
			f := newCloudFixture(t)
			rigorSkewCheckout(t, f, rigorSkewHead+"rigor: { default: hotfix }\n"+rigorSkewWorkflow)
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
				"fugaro.yaml's rigor: block of acme/other",
				"fugaro " + rigorSince + " or later",
				"ghcr.io/dimipaun/fugaro-base:" + c.rel + ", release " + c.rel,
				"fugaro image refresh --repo acme/other --workflow svc",
				"; or remove the rigor: block from fugaro.yaml",
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

// TestRunRefusesANullRigorBlockOnAnOlderBaseImage: `rigor:` with nothing under
// it writes the key and decodes to no block at all, so the gate has to judge
// the key and not the value — an older runner's KnownFields reader refuses the
// whole file either way, and a gate that read RepoRigor alone would launch a
// run that dies at bootstrap on a line the operator did write.
func TestRunRefusesANullRigorBlockOnAnOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	rigorSkewCheckout(t, f, rigorSkewHead+"rigor:\n"+rigorSkewWorkflow)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, rigorSince, -1))
	wantRefused(t, f, "fugaro.yaml's rigor: block of acme/other needs a job image whose runner knows the rigor: block (fugaro "+rigorSince+" or later)",
		"run", "--run-id", "20261008-100000-abcd", "A task")
}

// TestRunRefusesTheLayersRigorOnAnOlderBaseImage: the other layer the key can
// come from is the project layer, and the subject says so — that block is the
// operator's to remove, and it breaks every repository of the project whose
// job image is older, while a repository's own file says nothing about it.
func TestRunRefusesTheLayersRigorOnAnOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithDefaults("  rigor: { default: critical }\n"))
	layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, rigorSince, -1))
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{
		"the project layer's defaults.rigor (read for acme/other)",
		"knows the project layer's defaults.rigor",
		"fugaro image refresh --repo acme/other --workflow " + config.ImplicitWorkflow,
		"remove defaults.rigor from the project layer",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "fugaro.yaml's rigor") {
		t.Errorf("the refusal blames the repository's file, which writes no rigor: %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunRefusesRigorOnACloneWorkflowsOlderBaseImage: a checkout: clone
// workflow has no build record to judge (it runs the installation's base image
// as it is), so the same gate is judged against base_images.base itself and
// names the fix that image has: fugaro update --base base, not an image
// refresh of a workflow that is never built.
func TestRunRefusesRigorOnACloneWorkflowsOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, rigorSince, -1)
	f.appendConfig(t, "base_images: {base: "+older+"}\n")
	rigorSkewCheckout(t, f, rigorSkewHead+"rigor: { default: hotfix }\n"+
		"workflows:\n  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n")
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{"fugaro.yaml's rigor: block of acme/other", "Run fugaro update --base base", older} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunDoesNotGateAFileThatWritesNoRigor: the gate is the key's, so a
// repository that writes none is never held to rigorSince — the same shape the
// title gate has (a launch with no --title carries no field to refuse).
func TestRunDoesNotGateAFileThatWritesNoRigor(t *testing.T) {
	f := newCloudFixture(t)
	rigorSkewCheckout(t, f, rigorSkewHead+rigorSkewWorkflow)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, rigorSince, -1))
	if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
		t.Fatalf("a file that writes no rigor: block must not be gated: %v", err)
	}
}

// validateRigor runs fugaro validate on a repository file whose top level
// carries rigorLines, in a directory that is no checkout and has no project
// config: the file's own key is all this judges.
func validateRigor(t *testing.T, rigorLines string) (validateOutput, error) {
	t.Helper()
	isolateProjects(t, t.TempDir())
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent: { auth: api-key }\n" +
		rigorLines + "workflows:\n  app: { commands: { build: go build ./..., test: go test ./... } }\n"})
	out, _, err := execute(t, "validate", "--json", filepath.Join(dir, "fugaro.yaml"))
	var doc validateOutput
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("output %q: %v", out, jerr)
	}
	return doc, err
}

// TestValidateWarnsThatRigorNeedsEveryJobImage: the file is valid — the skew is
// the installation's, not the file's — so validate warns rather than refusing,
// the shape usesProfiles' profile warning and cloneWorkflowWarnings have, and
// names both ways to move an image (the installation's base, and a workflow's
// own). An explicit null counts: it is the key an older runner refuses.
func TestValidateWarnsThatRigorNeedsEveryJobImage(t *testing.T) {
	for _, lines := range []string{"rigor: { default: hotfix }\n", "rigor:\n"} {
		doc, err := validateRigor(t, lines)
		if err != nil {
			t.Fatalf("%q: err = %v, want a valid file", lines, err)
		}
		if !has(doc.Warnings, "rigor", "needs fugaro "+rigorSince) {
			t.Errorf("%q: warnings = %+v, want the rigor warning", lines, doc.Warnings)
		}
		for _, want := range []string{"fugaro update --base base", "fugaro image refresh"} {
			if !has(doc.Warnings, "rigor", want) {
				t.Errorf("%q: the warning does not name %q: %+v", lines, want, doc.Warnings)
			}
		}
		if has(doc.Problems, "rigor", "") {
			t.Errorf("%q: the key was refused, and the file is valid: %+v", lines, doc.Problems)
		}
	}
	if doc, err := validateRigor(t, ""); err != nil || has(doc.Warnings, "rigor", "") {
		t.Errorf("a file that writes no rigor: block was warned about: %+v, %v", doc.Warnings, err)
	}
}

// TestValidateWarnsWhenTheProjectLayerSetsRigor: the layer's key reaches every
// repository of the project, so validate says which of the two wrote it and
// that the fix is the layer's.
func TestValidateWarnsWhenTheProjectLayerSetsRigor(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithDefaults("  rigor: { default: critical }\n"))
	layerCheckout(t, f, minimalAnchored)
	_, errOut, err := execute(t, "validate")
	if err != nil {
		t.Fatalf("a file that resolves over the layer is valid: %v", err)
	}
	for _, want := range []string{"project layer", "defaults.rigor", "needs fugaro " + rigorSince, "fugaro update --base base"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
	}
}
