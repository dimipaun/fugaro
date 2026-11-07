package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// jobsSkewLayerWith parses a layer that writes block as a top-level key, the
// way repoEngine's resolution parses one.
func jobsSkewLayerWith(t *testing.T, block string) *config.ProjectLayer {
	t.Helper()
	l, ps := config.ParseProjectLayer([]byte("version: 1\nproject: acme\ngcp_project: acme-fugaro\n"+block),
		config.LayerAnchor{Project: "acme", GCPProject: "acme-fugaro"})
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
	return l
}

// jobsSkewLayer is a project layer that writes the jobs: block with a value in
// it — paused: false, the direction that starts a schedule, and so the one an
// older runner has to be refused for.
func jobsSkewLayer(t *testing.T) *config.ProjectLayer {
	t.Helper()
	return jobsSkewLayerWith(t, "jobs: { catalog_check: { paused: false } }\n")
}

// layerWithJobs is testProjectLayer with extra taken as a top-level jobs:
// block: unlike defaults.rigor the layer file gives the block the root, because
// it is a policy the project sets for all its repositories rather than a default
// a branch may replace.
func layerWithJobs(extra string) string {
	return strings.Replace(testProjectLayer, "gcp_project: proj-1234\n", "gcp_project: proj-1234\n"+extra, 1)
}

// TestJobsSinceIsTheReleaseTheChecklistNames holds jobsSince to the reader that
// states its number: docs/release.md's Before-you-tag bullet.
func TestJobsSinceIsTheReleaseTheChecklistNames(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `jobsSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no jobsSince bullet")
	}
	if !strings.Contains(bullet, "currently `\""+jobsSince+"\"`") {
		t.Errorf("docs/release.md's jobsSince bullet does not carry the constant's release (%q)", jobsSince)
	}
}

// TestJobsSinceIsTheReleaseTheJobsBlockShipsIn pins the constant to the release
// the jobs: block ships in. It is written out because the reader above moves
// with it and stays green at any number, and because 0.22.0 is the slot
// docs/periodic-reviews/implementation-plan.md §P7 gives this work, not the
// release after the last tag: v0.20.3 and v0.21.0 ship no such key.
func TestJobsSinceIsTheReleaseTheJobsBlockShipsIn(t *testing.T) {
	if jobsSince != "0.22.0" {
		t.Errorf("jobsSince = %q: the jobs: block ships in CLI 0.22.0 (docs/periodic-reviews/implementation-plan.md §P7)", jobsSince)
	}
}

// TestJobsSinceIsTheReleaseTheDocsName: the release the key ships in is what an
// operator reads before they write jobs: into a layer, and both places that say
// it have to name the release the constant does. The layer's schema and
// internal/config's Jobs carried 0.20.0 — a release that shipped without the
// key — which told an operator on a released binary that their layer would
// work, when that runner refuses the layer at bootstrap. Unlike the sibling
// keys' sentences, nothing pinned either one.
func TestJobsSinceIsTheReleaseTheDocsName(t *testing.T) {
	schema := readDoc(t, "schemas/project-layer.schema.json")
	layer := readDoc(t, "internal/config/layer.go")
	for _, c := range []struct {
		what string
		doc  string
		want string
	}{
		{"the layer schema's jobs: block", schema, "Fugaro before " + jobsSince + " refuses the key, in every repository of the project."},
		{"the layer schema's jobs.reports", schema, "The reports job: the project's one writer of reports/ into the runs bucket. Fugaro before " + jobsSince + " refuses it, in every repository of the project."},
		{"internal/config's Jobs.Reports", layer, "Fugaro before " + jobsSince + " refuses"},
	} {
		if !strings.Contains(c.doc, c.want) {
			t.Errorf("%s does not name the release the key ships in: want %q", c.what, c.want)
		}
	}
	if strings.Contains(schema, "Fugaro before 0.20.0 refuses the key") || strings.Contains(layer, "Fugaro before 0.20.0 refuses") {
		t.Error("0.20.0 is named for the jobs: key, and v0.20.0 shipped without it")
	}
}

// TestUpdateRepoRefusesJobsOnAnOlderBaseImage is jobsSince's gate, the same
// shape as governanceSince's (TestUpdateRepoRefusesGovernanceOnOlderBaseImage):
// the layer is decoded strictly by every release, so a runner older than the
// release that reads it dies at bootstrap on the key, and fugaro update --repo
// refuses to deploy a job that would. The boundary is the constant's own: one
// release below is refused, the constant and one above pass, a base that is not
// a release only warns, and no base at all passes.
//
// The judged half is the key being written and not the block being set: `jobs:`
// with nothing under it, and `jobs: {}`, are lines an older runner's
// KnownFields decoder refuses, so they are refused here too, while a layer that
// writes no key at all asks nothing of the image.
func TestUpdateRepoRefusesJobsOnAnOlderBaseImage(t *testing.T) {
	nullBlock := &config.Config{Layer: jobsSkewLayerWith(t, "jobs:\n")}
	below, above := shiftRelease(t, jobsSince, -1), shiftRelease(t, jobsSince, 1)
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
	for _, cfg := range []*config.Config{{Layer: jobsSkewLayer(t)}, nullBlock} {
		err := r.gateJobs(r.o, "acme/app", lc(older), cfg)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("%+v: err = %v, want refused", cfg, err)
		}
		for _, want := range []string{
			"fugaro " + jobsSince + " or later",
			older + ", release " + below,
			"fugaro update --base base",
			"the project layer's jobs: block (read for acme/app)",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%+v: %v lacks %q", cfg, err, want)
			}
		}
		if errOut.Len() != 0 {
			t.Errorf("stderr = %q, want none (refused, not warned)", errOut.String())
		}
	}

	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + jobsSince, "ghcr.io/dimipaun/fugaro-base:" + above, ""} {
		var warn bytes.Buffer
		if err := refuseJobsSkew("acme/app", true, ref, &warn); err != nil || warn.Len() != 0 {
			t.Errorf("%q: err = %v, warn = %q, want neither", ref, err, warn.String())
		}
	}
	var warn bytes.Buffer
	if err := refuseJobsSkew("acme/app", true, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", &warn); err != nil {
		t.Errorf("dev base: err = %v, want a warning only", err)
	}
	if !strings.Contains(warn.String(), "not a release base image") || !strings.Contains(warn.String(), "is not checked") {
		t.Errorf("dev base: warn = %q", warn.String())
	}

	// A configuration whose layer writes no jobs: block asks nothing of the
	// image.
	for _, cfg := range []*config.Config{nil, {}, {Layer: &config.ProjectLayer{}}, {Layer: jobsSkewLayerWith(t, "governance: { review_ratio: 3 }\n")}} {
		if err := r.gateJobs(r.o, "acme/app", lc(older), cfg); err != nil {
			t.Errorf("%+v: err = %v, want none", cfg, err)
		}
	}

	// --forget and --plan-only deploy nothing, so neither is refused;
	// --print-vars is judged like a deploy and writes nothing to stdout.
	for _, o := range []*initOptions{{planOnly: true}, {forget: true}} {
		if err := (&initRun{o: o, cmd: cmd, w: &out}).gateJobs(o, "acme/app", lc(older), &config.Config{Layer: jobsSkewLayer(t)}); err != nil {
			t.Errorf("%+v: err = %v, want none", o, err)
		}
	}
	out.Reset()
	pv := &initOptions{printVars: true}
	if err := (&initRun{o: pv, cmd: cmd, w: io.Discard}).gateJobs(pv, "acme/app", lc(older), &config.Config{Layer: jobsSkewLayer(t)}); err == nil {
		t.Fatal("--print-vars: want refused")
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars wrote to stdout: %q", out.String())
	}
}

// TestRunRefusesTheLayersJobsOnAnOlderBaseImage is jobsSince's launch gate: the
// judge is the workflow's build record, as it is for every other per-workflow
// image gate, and the boundary is the constant's own. The key is the layer's,
// so the refusal names the layer, every repository it reaches and the one fix
// its operator has — the repository's own file writes nothing about it.
func TestRunRefusesTheLayersJobsOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, jobsSince, -1), shiftRelease(t, jobsSince, 1)
	for _, c := range []struct {
		rel     string
		refused bool
	}{
		{below, true},
		{jobsSince, false},
		{above, false},
	} {
		t.Run(c.rel, func(t *testing.T) {
			f := newCloudFixture(t)
			isolateCache(t)
			publishedLayer(t, f, layerWithJobs("jobs: { catalog_check: { paused: false } }\n"))
			dir := layerCheckout(t, f, minimalAnchored)
			registerOtherLocally(t, f)
			commitRef(t, dir, "main")
			writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, "ghcr.io/dimipaun/fugaro-base:"+c.rel)
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
				"the project layer's jobs: block (read for acme/other)",
				"knows the project layer's jobs: block",
				"fugaro " + jobsSince + " or later",
				"ghcr.io/dimipaun/fugaro-base:" + c.rel + ", release " + c.rel,
				"fugaro image refresh --repo acme/other --workflow " + config.ImplicitWorkflow,
				"; or remove the jobs: block from the project layer and publish it again",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v lacks %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "fugaro.yaml's jobs") {
				t.Errorf("the refusal blames the repository's file, which has no jobs: key: %v", err)
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Errorf("%d executions, want none", n)
			}
		})
	}
}

// TestRunRefusesTheLayersJobsOnACloneWorkflowsOlderBaseImage: a checkout: clone
// workflow has no build record to judge — it runs the installation's base image
// as it is — so the same gate is judged against base_images.base itself and
// names the fix that image has: fugaro update --base base, not an image refresh
// of a workflow that is never built.
func TestRunRefusesTheLayersJobsOnACloneWorkflowsOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, jobsSince, -1)
	publishedLayer(t, f, layerWithJobs("jobs: { reports: { paused: false } }\n"))
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
	for _, want := range []string{"the project layer's jobs: block (read for acme/other)", "Run fugaro update --base base", older} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestValidateWarnsWhenTheProjectLayerSetsJobs: the layer's key reaches every
// repository of the project, so validate says so and names both ways to move an
// image. A warning rather than a Problem: the layer is valid, and the skew is
// the installation's. The block named is the top-level one: the layer file has
// no defaults.jobs to send its operator to.
func TestValidateWarnsWhenTheProjectLayerSetsJobs(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithJobs("jobs: { catalog_check: { paused: false } }\n"))
	layerCheckout(t, f, minimalAnchored)
	_, errOut, err := execute(t, "validate")
	if err != nil {
		t.Fatalf("a file that resolves over the layer is valid: %v", err)
	}
	for _, want := range []string{"project layer's jobs: block", "needs fugaro " + jobsSince, "fugaro update --base base", "fugaro image refresh"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("validate's output lacks %q: %q", want, errOut)
		}
	}
	if strings.Contains(errOut, "defaults.jobs") {
		t.Errorf("validate names defaults.jobs, which is no key of the layer file: %q", errOut)
	}
}
