package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/streams"
)

// rigorMembersLayerWith parses a layer whose defaults: block is block, the way
// repoEngine's resolution parses one.
func rigorMembersLayerWith(t *testing.T, block string) *config.ProjectLayer {
	t.Helper()
	l, ps := config.ParseProjectLayer([]byte("version: 1\nproject: acme\ngcp_project: acme-fugaro\ndefaults:\n"+block),
		config.LayerAnchor{Project: "acme", GCPProject: "acme-fugaro"})
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
	return l
}

// rigorMembersLayer is a project layer that writes an area with members — the
// shape LD4 adds — and one that writes the per-repository map beside it.
func rigorMembersLayer(t *testing.T) *config.ProjectLayer {
	t.Helper()
	return rigorMembersLayerWith(t, "  rigor:\n    areas:\n      - name: ui\n        members:\n          - {repo: acme/app, paths: [\"ui/**\"]}\n        level: pro\n")
}

func rigorReposLayer(t *testing.T) *config.ProjectLayer {
	t.Helper()
	return rigorMembersLayerWith(t, "  rigor:\n    repos: { acme/app: pro }\n")
}

// layerWithRigorMembers is testProjectLayer with extra taken under defaults:,
// which is where the layer's rigor block lives.
func layerWithRigorMembers(extra string) string {
	return layerWithDefaults(extra)
}

// TestRigorMembersSinceHasAReleaseBullet: rigorMembersSince names the release
// that ships members: and the repos: map, and that release is not tagged yet,
// so docs/release.md's Before-you-tag list is where a release verifies it
// against the tag actually cut.
func TestRigorMembersSinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `rigorMembersSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no rigorMembersSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+rigorMembersSince+"\"`") {
		t.Errorf("docs/release.md's rigorMembersSince bullet does not carry the constant's release (%q)", rigorMembersSince)
	}
	for _, want := range []string{
		"`members:`", "`defaults.rigor.repos`", "not tagged yet",
		"gateRigorMembers", "checkRigorMembersImage", "checkCloneRigorMembersImage",
	} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's rigorMembersSince bullet does not say %q", want)
		}
	}
	if !strings.Contains(bullet, "rigorSpellingSince") {
		t.Error("docs/release.md's rigorMembersSince bullet does not say why it is not rigorSpellingSince")
	}
}

// TestRigorMembersSinceIsTheReleaseThatShipsTheKeys pins the constant to the
// release LD4's shape ships in, the way jobsSince is pinned to 0.22.0: the
// bullet test above moves with the constant and stays green at any number.
func TestRigorMembersSinceIsTheReleaseThatShipsTheKeys(t *testing.T) {
	if rigorMembersSince != "0.27.0" {
		t.Errorf("rigorMembersSince = %q: members: and the repos: map ship in CLI 0.27.0", rigorMembersSince)
	}
}

// TestRigorMembersSinceIsNotAheadOfTheReleaseThatShipsIt is
// TestRigorSpellingSinceIsNotAheadOfTheReleaseThatShipsIt for the second
// constant this work adds: a value above the release that ships the keys
// refuses update --repo and every launch on installations whose base image is
// that very release.
func TestRigorMembersSinceIsNotAheadOfTheReleaseThatShipsIt(t *testing.T) {
	cat, err := streams.Parse("docs/release-streams.yaml", []byte(readDoc(t, "docs/release-streams.yaml")))
	if err != nil {
		t.Fatalf("docs/release-streams.yaml: %v", err)
	}
	cli, ok := cat.Lookup(streams.CLI)
	if !ok {
		t.Fatal("docs/release-streams.yaml carries no cli stream, so the release this tree is on cannot be read")
	}
	shipping := cli.Current
	if imagePredates(cli.Current.String(), rigorMembersSince) {
		shipping = streams.Version{Major: cli.Current.Major, Minor: cli.Current.Minor + 1}
	}
	if imagePredates(shipping.String(), rigorMembersSince) {
		t.Errorf("rigorMembersSince = %q is ahead of %s, the release that ships the keys: the cli stream is at %s, and one release spends one minor", rigorMembersSince, shipping, cli.Current)
	}
}

// TestRigorMembersSinceIsTheReleaseTheDocsName: the release the keys ship in is
// what an operator reads before they write members: into a layer, and every
// place that says it has to name the release the constant does.
func TestRigorMembersSinceIsTheReleaseTheDocsName(t *testing.T) {
	schema := readDoc(t, "schemas/project-layer.schema.json")
	layer := readDoc(t, "internal/config/layer.go")
	for _, c := range []struct {
		what string
		doc  string
		want string
	}{
		{"the layer schema's members:", schema, "Needs fugaro " + rigorMembersSince + "."},
		{"the layer schema's repos:", schema, "Needs fugaro " + rigorMembersSince + "."},
		{"internal/config's SetsRigorMembers", layer, "rigorMembersSince"},
	} {
		if !strings.Contains(c.doc, c.want) {
			t.Errorf("%s does not name the release the keys ship in: want %q", c.what, c.want)
		}
	}
}

// TestUpdateRepoRefusesRigorMembersOnAnOlderBaseImage is rigorMembersSince's
// gate, the same shape as jobsSince's
// (TestUpdateRepoRefusesJobsOnAnOlderBaseImage): the layer is decoded strictly
// by every release, so a runner older than the release that reads these keys
// dies at bootstrap on one, and fugaro update --repo refuses to deploy a job
// that would. The boundary is the constant's own: one release below is refused,
// the constant and one above pass, a base that is not a release only warns, and
// no base at all passes.
//
// The judged half is the key being written and not the value it holds:
// `members:` with nothing under it, `members: []` and `repos: {}` are lines an
// older runner's KnownFields decoder refuses, so they are refused here too,
// while a layer that writes neither asks nothing of the image.
func TestUpdateRepoRefusesRigorMembersOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, rigorMembersSince, -1), shiftRelease(t, rigorMembersSince, 1)
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
	for _, layer := range []*config.ProjectLayer{
		rigorMembersLayer(t),
		rigorReposLayer(t),
		// The map's empty spelling: an older runner's decoder refuses it too.
		rigorMembersLayerWith(t, "  rigor: { repos: {} }\n"),
	} {
		cfg := &config.Config{Layer: layer}
		err := r.gateRigorMembers(r.o, "acme/app", lc(older), cfg)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("%+v: err = %v, want refused", cfg, err)
		}
		for _, want := range []string{
			"fugaro " + rigorMembersSince + " or later",
			older + ", release " + below,
			"fugaro update --base base",
			"read for acme/app",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%+v: %v lacks %q", cfg, err, want)
			}
		}
		if errOut.Len() != 0 {
			t.Errorf("stderr = %q, want none (refused, not warned)", errOut.String())
		}
	}

	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + rigorMembersSince, "ghcr.io/dimipaun/fugaro-base:" + above, ""} {
		var warn bytes.Buffer
		if err := refuseRigorMembersSkew("acme/app", true, ref, &warn); err != nil || warn.Len() != 0 {
			t.Errorf("%q: err = %v, warn = %q, want neither", ref, err, warn.String())
		}
	}
	var warn bytes.Buffer
	if err := refuseRigorMembersSkew("acme/app", true, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", &warn); err != nil {
		t.Errorf("dev base: err = %v, want a warning only", err)
	}
	if !strings.Contains(warn.String(), "not a release base image") || !strings.Contains(warn.String(), "is not checked") {
		t.Errorf("dev base: warn = %q", warn.String())
	}

	// A configuration whose layer writes neither key asks nothing of the image:
	// the released shape of a project area is a match, and a layer that writes
	// only the keys the older constants already judge is silence here.
	for _, cfg := range []*config.Config{
		nil,
		{},
		{Layer: &config.ProjectLayer{}},
		{Layer: rigorMembersLayerWith(t, "  rigor:\n    areas:\n      - {name: docs, match: \"docs/**\", level: pro}\n")},
		{Layer: jobsSkewLayer(t)},
	} {
		if err := r.gateRigorMembers(r.o, "acme/app", lc(older), cfg); err != nil {
			t.Errorf("%+v: err = %v, want none", cfg, err)
		}
	}

	// --forget and --plan-only deploy nothing, so neither is refused;
	// --print-vars is judged like a deploy and writes nothing to stdout.
	for _, o := range []*initOptions{{planOnly: true}, {forget: true}} {
		if err := (&initRun{o: o, cmd: cmd, w: &out}).gateRigorMembers(o, "acme/app", lc(older), &config.Config{Layer: rigorMembersLayer(t)}); err != nil {
			t.Errorf("%+v: err = %v, want none", o, err)
		}
	}
	out.Reset()
	pv := &initOptions{printVars: true}
	if err := (&initRun{o: pv, cmd: cmd, w: io.Discard}).gateRigorMembers(pv, "acme/app", lc(older), &config.Config{Layer: rigorMembersLayer(t)}); err == nil {
		t.Fatal("--print-vars: want refused")
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars wrote to stdout: %q", out.String())
	}
}

// TestRunRefusesTheLayersRigorMembersOnAnOlderBaseImage is rigorMembersSince's
// launch gate: the judge is the workflow's build record, as it is for every
// other per-workflow image gate, and the boundary is the constant's own. Both
// keys are the layer's, so the refusal names the layer, every repository it
// reaches and the one fix its operator has — the repository's own file cannot
// write either key.
func TestRunRefusesTheLayersRigorMembersOnAnOlderBaseImage(t *testing.T) {
	below, above := shiftRelease(t, rigorMembersSince, -1), shiftRelease(t, rigorMembersSince, 1)
	for _, c := range []struct {
		rel     string
		refused bool
	}{
		{below, true},
		{rigorMembersSince, false},
		{above, false},
	} {
		t.Run(c.rel, func(t *testing.T) {
			f := newCloudFixture(t)
			isolateCache(t)
			// The rule states its level with mode:, the spelling every release
			// reads: the fixture is the members: key alone, so the refusal here
			// is this constant's and not rigorSpellingSince's, which would
			// pre-empt it on the level: spelling (both are 0.27.0).
			publishedLayer(t, f, layerWithRigorMembers("  rigor:\n    areas:\n      - name: ui\n        members:\n          - {repo: acme/other, paths: [\"ui/**\"]}\n        mode: critical\n"))
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
				"read for acme/other",
				"knows the project layer's rigor members",
				"fugaro " + rigorMembersSince + " or later",
				"ghcr.io/dimipaun/fugaro-base:" + c.rel + ", release " + c.rel,
				imageGateRemedy,
				"; or remove the members: key or the repos: map from the project layer and publish it again",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v lacks %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "fugaro.yaml's rigor") {
				t.Errorf("the refusal blames the repository's file, which writes neither key: %v", err)
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Errorf("%d executions, want none", n)
			}
		})
	}
}

// TestRunRefusesTheLayersRigorReposOnAnOlderBaseImage is the same gate for the
// half of the constant a per-repository map writes: one key under defaults.rigor
// and no members list, so the layer-set question is the repos: key's own.
func TestRunRefusesTheLayersRigorReposOnAnOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, rigorMembersSince, -1)
	publishedLayer(t, f, layerWithRigorMembers("  rigor:\n    repos: { acme/other: critical }\n"))
	dir := layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	commitRef(t, dir, "main")
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, older)
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the launch refused", err)
	}
	for _, want := range []string{"read for acme/other", "defaults.rigor.repos", older} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunRefusesTheLayersRigorMembersOnACloneWorkflowsOlderBaseImage: a
// checkout: clone workflow has no build record to judge — it runs the
// installation's base image as it is — so the same gate is judged against
// base_images.base itself and names the fix that image has.
func TestRunRefusesTheLayersRigorMembersOnACloneWorkflowsOlderBaseImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, rigorMembersSince, -1)
	publishedLayer(t, f, layerWithRigorMembers("  rigor:\n    repos: { acme/other: pro }\n"))
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
	for _, want := range []string{"read for acme/other", "Run fugaro update --base base", older} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestValidateWarnsWhenTheProjectLayerSetsRigorMembers: the keys reach every
// repository of the project, so validate says so and names both ways to move an
// image. A warning rather than a Problem: the layer is valid, and the skew is
// the installation's.
func TestValidateWarnsWhenTheProjectLayerSetsRigorMembers(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithRigorMembers("  rigor:\n    areas:\n      - name: ui\n        members:\n          - {repo: acme/other, paths: [\"ui/**\"]}\n        level: pro\n"))
	layerCheckout(t, f, pinnedMinimal)
	_, errOut, err := execute(t, "validate")
	if err != nil {
		t.Fatalf("a file that resolves over the layer is valid: %v", err)
	}
	for _, want := range []string{"project layer's rigor members", "need fugaro " + rigorMembersSince, "fugaro update --base base", "fugaro image refresh"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("validate's output lacks %q: %q", want, errOut)
		}
	}
}
