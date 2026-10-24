package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rigor"
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
