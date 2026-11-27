package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

const yamlRest = "git: { provider: github }\nagent: { auth: oauth }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"

func TestSetGCPProjectLine(t *testing.T) {
	const id = "fugaro-belong"
	const ins = "gcp_project: fugaro-belong\n"
	for _, c := range []struct {
		name, in, want, err string
	}{
		{"plain", "# fugaro config\nversion: 1\nproject: belong\n" + yamlRest, "# fugaro config\nversion: 1\nproject: belong\n" + ins + yamlRest, ""},
		{"trailing comment", "version: 1\nproject: belong # the project\n" + yamlRest, "version: 1\nproject: belong # the project\n" + ins + yamlRest, ""},
		{"double-quoted project", "version: 1\nproject: \"belong\"\n" + yamlRest, "version: 1\nproject: \"belong\"\n" + ins + yamlRest, ""},
		{"single-quoted project with comment", "version: 1\nproject: 'belong'  # x\n" + yamlRest, "version: 1\nproject: 'belong'  # x\n" + ins + yamlRest, ""},
		{"same value, quoted key, is a no-op", "version: 1\nproject: belong\n\"gcp_project\": fugaro-belong\n" + yamlRest, "version: 1\nproject: belong\n\"gcp_project\": fugaro-belong\n" + yamlRest, ""},
		{"same value, quoted value, is a no-op", "version: 1\nproject: belong\ngcp_project: 'fugaro-belong'\n" + yamlRest, "version: 1\nproject: belong\ngcp_project: 'fugaro-belong'\n" + yamlRest, ""},
		{"different value refused", "version: 1\nproject: belong\ngcp_project: fugaro-other\n" + yamlRest, "", "gcp_project: fugaro-other, not fugaro-belong"},
		{"different quoted value refused", "version: 1\nproject: belong\n\"gcp_project\": \"fugaro-other\"\n" + yamlRest, "", "gcp_project: fugaro-other, not fugaro-belong"},
		{"empty gcp_project refused", "version: 1\nproject: belong\ngcp_project:\n" + yamlRest, "", "gcp_project: line with no usable value"},
		{"indented project refused", "version: 1\nx:\n  project: belong\n" + yamlRest, "", "no top-level project: line"},
		{"flow mapping refused", "{version: 1, project: belong}\n", "", "no top-level project: line"},
		{"block scalar refused", "version: 1\nproject: >\n  belong\n" + yamlRest, "", "not a simple one-line value"},
		{"literal scalar refused", "version: 1\nproject: |\n  belong\n" + yamlRest, "", "not a simple one-line value"},
		{"multi-line quoted refused", "version: 1\nproject: \"bel\n  ong\"\n" + yamlRest, "", "not a simple one-line value"},
		{"continued plain scalar refused", "version: 1\nproject: bel\n  ong\n" + yamlRest, "", "continues on the next line"},
		{"quoted project key refused", "version: 1\n\"project\": belong\n" + yamlRest, "", "not a simple one-line value"},
		{"empty file refused", "", "", "no top-level project: line"},
		{"no project refused", "version: 1\n" + yamlRest, "", "no top-level project: line"},
		{"CRLF kept", "version: 1\r\nproject: belong\r\n" + strings.ReplaceAll(yamlRest, "\n", "\r\n"), "version: 1\r\nproject: belong\r\ngcp_project: fugaro-belong\r\n" + strings.ReplaceAll(yamlRest, "\n", "\r\n"), ""},
		{"no trailing newline", "version: 1\nproject: belong\n" + strings.TrimSuffix(yamlRest, "\n"), "version: 1\nproject: belong\n" + ins + strings.TrimSuffix(yamlRest, "\n"), ""},
		{"project last, no trailing newline", "version: 1\n" + yamlRest + "project: belong", "version: 1\n" + yamlRest + "project: belong\ngcp_project: fugaro-belong", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, changed, err := setGCPProjectLine([]byte(c.in), id)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) || changed || out != nil {
					t.Fatalf("out %q changed %v err %v, want error %q", out, changed, err, c.err)
				}
				return
			}
			if err != nil || string(out) != c.want || changed != (c.want != c.in) {
				t.Fatalf("out %q changed %v err %v\nwant %q", out, changed, err, c.want)
			}
			if cfg, probs := config.Parse(out); cfg == nil || len(probs) != 0 || cfg.GCPProject != id {
				t.Errorf("the result does not parse: %+v %v", cfg, probs)
			}
		})
	}
	// A refusal for the file's shape says the exact line to add by hand.
	_, _, err := setGCPProjectLine([]byte("version: 1\nproject: >\n  belong\n"), id)
	var bh *byHandError
	if !errors.As(err, &bh) || !strings.HasSuffix(err.Error(), "add this line to it by hand, at the top level: gcp_project: fugaro-belong") {
		t.Errorf("err = %v", err)
	}
	if _, _, err := setGCPProjectLine([]byte("project: belong\n"), "../x"); err == nil || errors.As(err, &bh) {
		t.Errorf("a bad id: %v", err)
	}
}

func TestImagePredates(t *testing.T) {
	for _, c := range []struct {
		v    string
		want bool
	}{{"0.3.1", true}, {"v0.3.1", true}, {"0.4.0", false}, {"0.10.0", false}, {"1.0.0", false}, {"", true}, {"dev-abc", false}} {
		if got := imagePredates(c.v, "0.4.0"); got != c.want {
			t.Errorf("imagePredates(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

// A workflow with no base: (the implicit Fugaro base) is seen by the anchor
// health check as "base", not as an unknown/empty kind: gcpProjectProblems
// skips the custom-base and stale-base checks entirely for a kind not in
// config.Bases (bases[wf], customBase := ...; if
// !slices.Contains(config.Bases, kind) { kind = "" }), so an implicit-base
// workflow must come out of workflowBases as "base" to be checked at all.
//
// Mutation (run, restore): change workflowBases' m[name] = w.BaseKind() back
// to w.Base, and this test fails: m["app"] is "" instead of "base".
// TestWorkflowBasesSeesTheImplicitBaseKind: every workflow resolves to the
// one Fugaro base (Workflow.BaseKind ignores Base, a legacy field Validate
// refuses outright if a real fugaro.yaml sets it); a name workflowBases is
// told about but that is not one of cfg.Workflows (extra, from a local
// config's own repo listing) maps to "", not the base kind, since there is
// no workflow to resolve a kind for.
func TestWorkflowBasesSeesTheImplicitBaseKind(t *testing.T) {
	cfg := &config.Config{Workflows: map[string]config.Workflow{"app": {}, "api": {Base: "go"}}}
	m := workflowBases(cfg, []string{"api", "gone"})
	if m["app"] != config.BaseKind || m["api"] != config.BaseKind || m["gone"] != "" {
		t.Fatalf("workflowBases = %v, want app and api: %s, gone: \"\"", m, config.BaseKind)
	}
}

// records are the fake runs bucket's build records, key to content; the stage
// closes each bucket it opens, so every open is a fresh one holding them.
var records map[string]string

func openFakeRecords(ctx context.Context, _ string) (*blobx.Bucket, error) {
	b, err := blobx.Open(ctx, "mem://")
	if err != nil {
		return nil, err
	}
	for k, v := range records {
		if _, err := b.Create(ctx, k, []byte(v), "application/json"); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func putRecordData(t *testing.T, data string) {
	t.Helper()
	slug, _ := task.Slug("github", "acme/app")
	records[imagecheck.RecordKey(slug, "app")] = data
}
func anchorLC() *localcfg.Config {
	return &localcfg.Config{Name: "aurora", GCPProject: "fugaro-aurora", RunsBucket: "fugaro-runs-fugaro-aurora", Repos: map[string]localcfg.Repo{"acme/app": {Provider: "github"}}}
}

func anchorYAML() string { return checkoutYAML("github", "oauth", "aurora", "") }
func readYAML(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "fugaro.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// repoStageRig is a checkout the repository stage is run over: the init rig's
// engine with lc set and the stage's own engine run stubbed, so what the stage
// itself does is the whole of the output.
func repoStageRig(t *testing.T, lc *localcfg.Config, yaml, stdin string) (*repositoryStage, *syncBuf, string) {
	t.Helper()
	dir := repoCheckout(t, githubOrigin, yaml)
	t.Chdir(dir)
	e, out := stageEngine(t, stdin, &initOptions{githubAppID: "12345"})
	e.lc = lc
	s := newRepositoryStage(e)
	s.engineStage.run = func(context.Context) error { return nil }
	s.resolve(t.Context())
	if s.tg == nil {
		t.Fatalf("no target: %+v", s.st)
	}
	return s, out, dir
}

// TestRepositoryStageWritesNoAnchor is U9b's third red step: the repository
// stage no longer writes the checkout's gcp_project line (R14), so
// `fugaro update` converges the cloud and leaves every file of the checkout
// alone — the line is `fugaro project repos add`'s to write now.
func TestRepositoryStageWritesNoAnchor(t *testing.T) {
	yaml := anchorYAML()
	s, out, dir := repoStageRig(t, anchorLC(), yaml, "")
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readYAML(t, dir); got != yaml {
		t.Errorf("the repository stage wrote the checkout's fugaro.yaml:\n%s", got)
	}
	if out.String() != "" {
		t.Errorf("the repository stage printed about the anchor:\n%s", out.String())
	}
	// Plan prints nothing either: no diff of a line it will not write.
	s, out, dir = repoStageRig(t, anchorLC(), yaml, "")
	if _, err := s.Plan(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readYAML(t, dir); got != yaml || out.String() != "" {
		t.Errorf("plan wrote or printed about the anchor:\n%s\n%s", got, out.String())
	}
}
