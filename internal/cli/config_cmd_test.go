package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// Review Focus 1.
func TestConfigShowNamesEverySource(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored+"agent:\n  review_rounds: 3\n")
	out, _, err := execute(t, "config", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc configShowDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range doc.Values {
		got[v.Path] = v.Source
	}
	for path, want := range map[string]string{
		"project":                            "repo",
		"agent.review_rounds":                "repo",
		"agent.auth":                         "project",
		"git.provider":                       "project",
		"git.base_branch":                    "default",
		"workflows.default.commands.test":    "profile svc",
		"workflows.default.timeouts.total":   "default",
		"workflows.default.resources.memory": "default",
	} {
		if got[path] != want {
			t.Errorf("%s: source %q, want %q", path, got[path], want)
		}
	}
	if doc.ProjectLayer == nil || doc.ProjectLayer.SHA256 != config.LayerSum([]byte(testProjectLayer)) || len(doc.ConfigSHA256) != 64 {
		t.Fatalf("doc = %+v", doc)
	}
	text, _, err := execute(t, "config", "show")
	if err != nil || !strings.Contains(text, "workflows.default.commands.test") || !strings.Contains(text, "profile svc") || !strings.Contains(text, "project layer:") {
		t.Fatalf("text %q, %v", text, err)
	}
}

// config.Config tags project, gcp_project, profile and a workflow's own
// profile yaml:"...,omitempty" (internal/config/config.go): a round trip
// through yaml.Marshal/Unmarshal drops each one from the tree entirely
// when it resolves to "" — exactly as it would drop it from the file
// itself — so a naive walk of that tree never visits the path at all, and
// a script checking it resolves to the project's default (the feature
// this whole plan implements) gets no row instead of a source: default
// one.
//
// Mutation (run, restore): delete the "ensure" calls and the have/have[]
// backfill block from showValues in config_show.go, keeping only
// walk("", tree), and this test fails: "profile" and
// "workflows.web.profile" are both missing from doc.Values entirely.
func TestConfigShowNamesAnOmittedZeroValuesSource(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	// An explicit workflows: block, not the implicit default workflow: its
	// "web" workflow names no profile: of its own, so Resolve never writes
	// a profile key for it at all (resolve.go's resolveWorkflows only sets
	// one when the workflow names one), the same gap as the top-level
	// profile: of a file with workflows: (always unset, since the two are
	// mutually exclusive, decision L10).
	layerCheckout(t, f, minimalAnchored+"workflows:\n  web:\n    base: go\n    commands: { build: make, test: make test }\n")
	out, _, err := execute(t, "config", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc configShowDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range doc.Values {
		got[v.Path] = v.Source
	}
	for _, path := range []string{"profile", "workflows.web.profile"} {
		src, ok := got[path]
		if !ok {
			t.Fatalf("%s: missing from config show entirely", path)
		}
		if src != "default" {
			t.Errorf("%s: source %q, want %q", path, src, "default")
		}
	}
}

// gcp_project is the third field the same omitempty gap drops: a file
// anchored to no project at all (the pre-0.6.0 shape) never carries it,
// so Resolve's merge never does either.
//
// Mutation: same as TestConfigShowNamesAnOmittedZeroValuesSource.
func TestConfigShowNamesSourceOfAnUnsetGCPProject(t *testing.T) {
	layerCheckout(t, nil, "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web: { base: go, commands: { build: make, test: make test } }\n")
	out, _, err := execute(t, "config", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc configShowDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	for _, v := range doc.Values {
		if v.Path == "gcp_project" {
			if v.Source != "default" {
				t.Errorf("gcp_project: source %q, want %q", v.Source, "default")
			}
			return
		}
	}
	t.Fatal("gcp_project: missing from config show entirely")
}

func TestConfigLayerPrintsThePublishedLayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, _, err := execute(t, "config", "layer")
	if err != nil || !strings.Contains(out, "default_profile: svc") || !strings.Contains(out, "generation") {
		t.Fatalf("out %q, %v", out, err)
	}
}

func TestConfigInitWritesTheMinimalFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "config", "init", "--project", "aurora"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("without --yes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fugaro.yaml")); err == nil {
		t.Fatal("written without --yes")
	}
	out, _, err := execute(t, "config", "init", "--project", "aurora", "--base-branch", "develop", "--yes")
	if err != nil || !strings.Contains(out, "wrote") {
		t.Fatalf("out %q, %v", out, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "fugaro.yaml"))
	if want := minimalAnchored + "git:\n  base_branch: develop\n"; string(data) != want {
		t.Fatalf("fugaro.yaml = %q, want %q", data, want)
	}
	if _, _, err := execute(t, "config", "init", "--project", "aurora", "--base-branch", "develop", "--yes"); err != nil {
		t.Fatalf("rerun on the same file: %v", err)
	}
	if _, _, err := execute(t, "config", "init", "--project", "aurora", "--profile", "svc", "--yes"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "never overwrites") {
		t.Fatalf("a different existing file: %v", err)
	}
}

func TestConfigInitRefusesWithoutALayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "config", "init", "--project", "aurora", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "publishes no project layer") {
		t.Fatalf("err = %v", err)
	}
}
