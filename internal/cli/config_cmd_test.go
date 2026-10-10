package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// bareLayerCheckout is layerCheckout (layer_helpers_test.go) before main's
// PR #230 richened it for fugaro run's own tests: a job registered on a
// fake Cloud Run, an origin remote, and build/test/lockfile scripts —
// none of which the tests below need (they never launch a run, select a
// backend job, or read an origin remote; they only need a git checkout
// holding fugaro.yaml, with no cloud fixture at all). gitCheckout
// (project_test.go) already does that git-init-plus-yaml setup; this
// just adds the chdir the tests below rely on, instead of a second,
// parallel reimplementation of the same steps.
func bareLayerCheckout(t *testing.T, yaml string) string {
	t.Helper()
	dir := gitCheckout(t, t.TempDir(), yaml)
	t.Chdir(dir)
	return dir
}

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
	bareLayerCheckout(t, "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web: { base: go, commands: { build: make, test: make test } }\n")
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

// Item 2: a --project-layer FILE has no generation and was never
// "checked" at a particular time (CheckedAt stays the zero time): printing
// them regardless said "generation 0" for a layer that has none, and an
// age against time.Time's zero value ("2562047h47m16.854775807s ago").
//
// Mutation (run, restore): in printConfigShow, replace the `if
// l.Generation != 0` and `if !checked.IsZero()` guards with
// unconditional appends, and this test fails on both the text and the
// --json assertions.
func TestConfigShowProjectLayerFileOmitsGenerationAndAge(t *testing.T) {
	isolateCache(t)
	dir := bareLayerCheckout(t, minimalAnchored)
	layerPath := filepath.Join(dir, "project-layer.yaml")
	if err := os.WriteFile(layerPath, []byte(testProjectLayer), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "config", "show", "--project-layer", layerPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "generation") {
		t.Errorf("printed a generation for a file-sourced layer, which has none:\n%s", out)
	}
	if strings.Contains(out, "ago") {
		t.Errorf("printed a bogus age for a file-sourced layer, which was never \"checked\":\n%s", out)
	}
	if !strings.Contains(out, "workflows.default.commands.test") || !strings.Contains(out, "profile svc") {
		t.Fatalf("the file's content was not actually used to resolve:\n%s", out)
	}
	jsonOut, _, err := execute(t, "config", "show", "--project-layer", layerPath, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc configShowDoc
	if err := json.Unmarshal([]byte(jsonOut), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ProjectLayer == nil || doc.ProjectLayer.Generation != 0 {
		t.Fatalf("doc.ProjectLayer = %+v", doc.ProjectLayer)
	}
	if doc.CheckedAt != nil {
		t.Errorf("checked_at = %v, want omitted for a file-sourced layer", *doc.CheckedAt)
	}
}

// Item 3: --offline with nothing cached could not tell "no layer applies"
// from "a layer may apply, but this could not check" — both printed
// project_layer: null and every value's source as "default", leaving only
// Notes' free text (never meant for a script) to tell them apart.
//
// Mutation (run, restore): drop `LayerUnknown: rf.Layer.Unknown` from the
// configShowDoc literal in newConfigShowCmd (config_show.go), and this
// test fails: doc.LayerUnknown stays false for the unknown case too.
func TestConfigShowJSONTellsLayerUnknownFromNone(t *testing.T) {
	isolateCache(t)
	// Self-sufficient: no profile: anywhere, so it resolves fully without
	// a layer (as Parse always has) — the failure mode here is the
	// strict command refusing to launch without checking are (needs_layer
	// problems), not this.
	selfSufficient := "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: github }\n" +
		"workflows:\n  web: { base: go, commands: { build: make, test: make test } }\n"
	bareLayerCheckout(t, selfSufficient)
	out, _, err := execute(t, "config", "show", "--offline", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc configShowDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ProjectLayer != nil {
		t.Fatalf("doc.ProjectLayer = %+v, want nil", doc.ProjectLayer)
	}
	if !doc.LayerUnknown {
		t.Fatal("layer_unknown = false for an --offline check with nothing cached, want true")
	}
	if doc.CheckedAt != nil {
		t.Errorf("checked_at = %v, want nil: nothing was ever actually read", *doc.CheckedAt)
	}
	// The contrast case the field exists to distinguish: a file to which
	// no layer could ever apply (no gcp_project: at all) also prints
	// project_layer: null, but LayerUnknown must stay false.
	bareLayerCheckout(t, "version: 1\nproject: aurora\ngit: { provider: github }\n"+
		"workflows:\n  web: { base: go, commands: { build: make, test: make test } }\n")
	out2, _, err := execute(t, "config", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc2 configShowDoc
	if err := json.Unmarshal([]byte(out2), &doc2); err != nil {
		t.Fatal(err)
	}
	if doc2.ProjectLayer != nil || doc2.LayerUnknown {
		t.Fatalf("doc2 = %+v, want ProjectLayer nil and LayerUnknown false", doc2)
	}
}

// The raw JSON text, not the unmarshalled struct: a stray `,omitempty` on
// LayerUnknown's tag, or a wrong json name, would still round-trip
// through encoding/json's own zero-value handling and hide behind
// Go's Unmarshal (an absent "layer_unknown" key and an explicit `false`
// both decode to the same Go zero value). layer_unknown has no omitempty,
// so the key is always there, true or false; checked_at does, so the key
// exists only when a layer was actually read at a real time.
//
// Mutation (run, restore): add `,omitempty` to LayerUnknown's json tag in
// configShowDoc (config_show.go). The command's own LayerUnknown is false
// in the first scenario below (a layer was read), so the key vanishes
// from the raw text and the first assertion below fails.
func TestConfigShowJSONRawKeysLayerUnknownAndCheckedAt(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, _, err := execute(t, "config", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"layer_unknown"`) {
		t.Fatalf(`raw JSON has no "layer_unknown" key at all:%s`, out)
	}
	if !strings.Contains(out, `"checked_at"`) {
		t.Fatalf(`raw JSON has no "checked_at" key for a layer that was actually read:%s`, out)
	}

	// --project-layer FILE never has a checked time at all (findLayer's
	// o.File case sets no CheckedAt): checked_at must be entirely absent
	// from the raw text, not present as null or a zero time.
	dir := bareLayerCheckout(t, minimalAnchored)
	layerPath := filepath.Join(dir, "project-layer.yaml")
	if err := os.WriteFile(layerPath, []byte(testProjectLayer), 0o600); err != nil {
		t.Fatal(err)
	}
	out2, _, err := execute(t, "config", "show", "--project-layer", layerPath, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, `"layer_unknown"`) {
		t.Fatalf(`raw JSON has no "layer_unknown" key at all:%s`, out2)
	}
	if strings.Contains(out2, `"checked_at"`) {
		t.Fatalf(`raw JSON has a "checked_at" key for a layer that was never "checked":%s`, out2)
	}
}

// Item 7: --offline actually resolves against the cache, never the
// bucket (noLayerBucketReads fails the test the instant anything tries to
// open it).
func TestConfigShowOfflineUsesTheCacheWithoutTheBucket(t *testing.T) {
	isolateCache(t)
	bareLayerCheckout(t, minimalAnchored)
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", CheckedAt: time.Now(), YAML: testProjectLayer,
	}); err != nil {
		t.Fatal(err)
	}
	noLayerBucketReads(t)
	out, _, err := execute(t, "config", "show", "--offline", "--json")
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
	if got["workflows.default.commands.test"] != "profile svc" {
		t.Fatalf("the cached layer was not used to resolve: %+v", doc.Values)
	}
}

// Item 7: the --workflow filter keeps the top-level keys and the named
// workflow's, and drops every other workflow's.
func TestConfigShowWorkflowFilter(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored+
		"workflows:\n  web: { base: go, commands: { build: make, test: make test } }\n"+
		"  api: { base: go, commands: { build: make, test: make test } }\n")
	out, _, err := execute(t, "config", "show", "--workflow", "web", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc configShowDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	sawWeb, sawTop := false, false
	for _, v := range doc.Values {
		if strings.HasPrefix(v.Path, "workflows.api.") {
			t.Fatalf("the --workflow filter leaked %s", v.Path)
		}
		if strings.HasPrefix(v.Path, "workflows.web.") {
			sawWeb = true
		}
		if v.Path == "project" {
			sawTop = true
		}
	}
	if !sawWeb || !sawTop {
		t.Fatalf("sawWeb=%v sawTop=%v, doc.Values=%+v", sawWeb, sawTop, doc.Values)
	}
	if _, _, err := execute(t, "config", "show", "--workflow", "bogus"); err == nil {
		t.Fatal("an unknown workflow name was accepted")
	}
}

// Item 7: a value a repository controls (not the project layer, which
// config.ParseProjectLayer already refuses a hostile rune in) can still
// carry one — fugaro.yaml is reviewed, but config show must print a
// branch's commands before that review lands. printConfigShow's table
// JSON-encodes each value before printing it, which already escapes an
// ASCII control character (\u001b, the json package's own doing); a bidi
// override (U+202E) is not one json.Marshal touches, so it is the rune
// that actually exercises pluginwire.Printable's own job here.
//
// Mutation (run, restore): in printConfigShow, replace
// pluginwire.Printable(string(val)) with string(val), and this test
// fails: the raw override reaches the table.
func TestConfigShowEscapesHostileValues(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored+
		"workflows:\n  web: { base: go, commands: { build: \"evil\\u202eutc.sh\", test: make test } }\n")
	out, _, err := execute(t, "config", "show")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "‮") {
		t.Fatalf("a raw bidi override reached the terminal:\n%s", out)
	}
	if !strings.Contains(out, "\\u202e") {
		t.Fatalf("the escaped form is missing:\n%s", out)
	}
}

// Item 7: Notes, both the text "note:" line and --json's notes, carry the
// cache-fallback warning when the bucket becomes unreachable after a
// first successful read cached the layer (decision L13's cache fallback
// applies to a strict command exactly as a lenient one; only "no usable
// cache" behaves differently between them).
func TestConfigShowPrintsNotes(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	if _, _, err := execute(t, "config", "show"); err != nil {
		t.Fatal(err)
	}
	old := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = old })
	layerBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("no route to host")}
	}
	out, _, err := execute(t, "config", "show")
	if err != nil || !strings.Contains(out, "note: using the cached project layer") {
		t.Fatalf("out %q, err %v", out, err)
	}
	jsonOut, _, err := execute(t, "config", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc configShowDoc
	if err := json.Unmarshal([]byte(jsonOut), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Notes) != 1 || !strings.Contains(doc.Notes[0], "unreachable") {
		t.Fatalf("doc.Notes = %v", doc.Notes)
	}
}

// Item 3: config layer --json's shape is a typed struct now, not an
// ad-hoc map, so its keys are pinned.
//
// Mutation (run, restore): add an extra field to configLayerDoc
// (config_show.go), and this test fails on the exact-key-count check.
func TestConfigLayerJSONShape(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, _, err := execute(t, "config", "layer", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	want := []string{"where", "generation", "sha256", "yaml"}
	if len(raw) != len(want) {
		got := make([]string, 0, len(raw))
		for k := range raw {
			got = append(got, k)
		}
		t.Fatalf("keys = %v, want exactly %v", got, want)
	}
	for _, k := range want {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	var doc configLayerDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Where == "" || doc.SHA256 == "" || !strings.Contains(doc.YAML, "default_profile: svc") {
		t.Fatalf("doc = %+v", doc)
	}
}

// Item 7: the "no project layer applies" error, for a project that
// publishes none.
func TestConfigLayerErrorsWhenNoneApplies(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	layerCheckout(t, f, minimalAnchored)
	_, _, err := execute(t, "config", "layer")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no project layer applies to this checkout") {
		t.Fatalf("err = %v", err)
	}
}

// Item 7: printableLines (config layer's own escaping of the published
// layer's raw text) has no direct test anywhere yet. A published layer
// itself can never carry a hostile rune (config.ParseProjectLayer refuses
// one, in a value or even a comment — verified directly), so this pins
// the helper itself rather than trying to smuggle one through a command.
//
// Mutation (run, restore): change printableLines to `return s`, and this
// test fails (the raw override survives).
func TestPrintableLinesEscapesHostileRunes(t *testing.T) {
	in := "line one\nevil‮override\nline three"
	out := printableLines(in)
	if strings.Contains(out, "‮") {
		t.Fatalf("a raw bidi override survived: %q", out)
	}
	if !strings.Contains(out, "\\u202e") {
		t.Fatalf("the escaped form is missing: %q", out)
	}
	if strings.Count(out, "\n") != strings.Count(in, "\n") {
		t.Fatalf("newlines changed: %q -> %q", in, out)
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

// writeMinimalFugaroYAML opens with mode 0o644 (config_init.go); the
// process umask can mask bits out of whatever a writer asks for, so this
// pins the actual on-disk mode against a umask of 0 (set for the
// duration, restored after) rather than guessing or depending on the
// test runner's ambient umask.
//
// Mutation (run, restore): change the 0o644 argument to
// os.OpenFile in writeMinimalFugaroYAML (config_init.go) to 0o600, and
// this test fails: the written file is rw------- instead of rw-r--r--.
func TestConfigInitWritesFileMode0644(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	if _, _, err := execute(t, "config", "init", "--project", "aurora", "--yes"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "fugaro.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %o, want 0644", got)
	}
}

// Item 7: --profile is written to the file, and resolves to that profile,
// not just "a" profile.
func TestConfigInitWritesTheNamedProfile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "config", "init", "--project", "aurora", "--profile", "svc", "--yes")
	if err != nil || !strings.Contains(out, "wrote") {
		t.Fatalf("out %q, %v", out, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "fugaro.yaml"))
	if want := minimalAnchored + "profile: svc\n"; string(data) != want {
		t.Fatalf("fugaro.yaml = %q, want %q", data, want)
	}
}

// Item 7: --base-branch main is the implicit default, so it must be
// omitted from the file exactly as leaving the flag off would (decision
// L12: base_branch only when it is not main), not written literally.
func TestConfigInitOmitsAnExplicitMainBaseBranch(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "config", "init", "--project", "aurora", "--base-branch", "main", "--yes")
	if err != nil || !strings.Contains(out, "wrote") {
		t.Fatalf("out %q, %v", out, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "fugaro.yaml"))
	if string(data) != minimalAnchored {
		t.Fatalf("fugaro.yaml = %q, want the plain minimal file %q", data, minimalAnchored)
	}
}

// Item 7: --profile and --base-branch are validated before anything else
// runs; a newline in --base-branch is the concrete risk (minimalFugaroYAML
// writes it unquoted into a YAML line, so a newline would inject an
// arbitrary key — TestValidBranchNameRejectsANewline in internal/config
// pins ValidBranchName itself).
func TestConfigInitRejectsBadProfileAndBranchShapes(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	if _, _, err := execute(t, "config", "init", "--project", "aurora", "--profile", "not a profile!", "--yes"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is not a profile name") {
		t.Fatalf("bad --profile: %v", err)
	}
	if _, _, err := execute(t, "config", "init", "--project", "aurora", "--base-branch", "main\nagent:\n  auth: api-key", "--yes"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is not a plain git branch name") {
		t.Fatalf("a newline in --base-branch: %v", err)
	}
}

// Item 7: removing the `len(rf.Problems) > 0` check would panic on a nil
// rf.Cfg (config.Resolve never returns both a Cfg and Problems) the
// moment an unresolvable --profile reaches the switch below it that reads
// rf.Cfg.Workflows.
//
// Mutation (run, restore): delete the `if len(rf.Problems) > 0 { ... }`
// block in config_init.go, and this test panics instead of failing
// cleanly.
func TestConfigInitRefusesAnUnresolvableProfile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer) // only profile "svc"
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "config", "init", "--project", "aurora", "--profile", "ghost", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "would not resolve against project") {
		t.Fatalf("err = %v", err)
	}
}

// Item 5: design §12 promises config init is idempotent ("exits 0 when
// the file already says exactly that"); that must hold even when the
// bucket the strict layer check would otherwise need is unreachable, so a
// script can rerun it across many repositories without every one of them
// needing a live connection. noLayerBucketReads fails the test outright
// if the command ever tries.
func TestConfigInitIsIdempotentWithoutTheNetwork(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "config", "init", "--project", "aurora", "--yes"); err != nil {
		t.Fatal(err)
	}
	noLayerBucketReads(t)
	out, _, err := execute(t, "config", "init", "--project", "aurora", "--yes")
	if err != nil || !strings.Contains(out, "already this minimal file") {
		t.Fatalf("out %q, err %v", out, err)
	}
}

// Item 5, the refusal half: an existing, different fugaro.yaml is refused
// without the network either — the same rule, the same reason.
func TestConfigInitRefusesADifferentFileWithoutTheNetwork(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored+
		"workflows:\n  web: { base: go, commands: { build: make, test: make test } }\n")
	noLayerBucketReads(t)
	_, _, err := execute(t, "config", "init", "--project", "aurora", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "never overwrites") {
		t.Fatalf("err = %v", err)
	}
}

// Item 4: a plain os.ReadFile-then-os.WriteFile leaves a window, between
// the existence check and the write, where another process can create
// the file first. writeMinimalFugaroYAML is a var so this test can make
// that race happen deterministically: write path itself, with content
// that differs from what this command would write, immediately before
// calling through to the real O_EXCL implementation, which must then see
// ErrExist and re-apply the idempotent-or-refuse rule rather than
// overwriting it.
func TestConfigInitHandlesATOCTOURaceAgainstADifferentFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	real := writeMinimalFugaroYAML
	t.Cleanup(func() { writeMinimalFugaroYAML = real })
	path := filepath.Join(dir, "fugaro.yaml")
	writeMinimalFugaroYAML = func(path, text string) error {
		if err := os.WriteFile(path, []byte("version: 1\nproject: other\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return real(path, text)
	}
	_, _, err := execute(t, "config", "init", "--project", "aurora", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "never overwrites") {
		t.Fatalf("err = %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "version: 1\nproject: other\n" {
		t.Fatalf("the raced-in file was overwritten: %q", data)
	}
}

// Item 4, the other race outcome: the file that won the race happens to
// be byte-identical to what this command would have written itself (two
// concurrent `config init --yes` runs) — that must still exit 0, not
// refuse.
func TestConfigInitHandlesATOCTOURaceAgainstTheSameFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	real := writeMinimalFugaroYAML
	t.Cleanup(func() { writeMinimalFugaroYAML = real })
	writeMinimalFugaroYAML = func(path, text string) error {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return real(path, text)
	}
	out, _, err := execute(t, "config", "init", "--project", "aurora", "--yes")
	if err != nil || !strings.Contains(out, "already this minimal file") {
		t.Fatalf("out %q, err %v", out, err)
	}
}

// Item 8: config init's Long text documents the dry-run exit code and the
// orchestrator's ruling that, unlike config publish, it never refuses in
// a coding-agent session.
func TestConfigInitLongDocumentsDryRunAndAgentSessions(t *testing.T) {
	cmd := newConfigInitCmd()
	if !strings.Contains(cmd.Long, "exits 1") {
		t.Errorf("Long does not document the dry-run exit code: %q", cmd.Long)
	}
	if !strings.Contains(cmd.Long, "coding-agent session") {
		t.Errorf("Long does not document the agent-session ruling: %q", cmd.Long)
	}
}

// Item 8: the ruling itself — config init writes a repository file that
// still goes through a reviewed pull request, so unlike config publish it
// never refuses just because it is running in a coding-agent session.
func TestConfigInitDoesNotRefuseInAnAgentSession(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDECODE", "1")
	out, _, err := execute(t, "config", "init", "--project", "aurora", "--yes")
	if err != nil || !strings.Contains(out, "wrote") {
		t.Fatalf("refused in an agent session: out %q, err %v", out, err)
	}
}
