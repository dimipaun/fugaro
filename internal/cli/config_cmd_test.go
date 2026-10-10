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
