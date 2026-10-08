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

const yamlRest = "git: { provider: github }\nagent: { auth: oauth }\nworkflows:\n  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"

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

// anchorRig is a checkout whose fugaro.yaml the repository stage edits, with
// a fake runs bucket holding build records.
func anchorRig(t *testing.T, lc *localcfg.Config, yaml, stdin string) (*repositoryStage, *syncBuf, string) {
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
	records = map[string]string{}
	prev := openRecordBucket
	openRecordBucket = openFakeRecords
	t.Cleanup(func() { openRecordBucket = prev })
	return s, out, dir
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

func putRecord(t *testing.T, version string) {
	t.Helper()
	putRecordData(t, `{"version":1,"fugaro_version":"`+version+`","base_ref":"`+releaseBase+`"}`)
}

func anchorLC() *localcfg.Config {
	return &localcfg.Config{Name: "aurora", GCPProject: "fugaro-aurora", RunsBucket: "fugaro-runs-fugaro-aurora", Repos: map[string]localcfg.Repo{"acme/app": {Provider: "github"}}}
}

func anchorYAML() string { return checkoutYAML("github", "oauth", "aurora", "") }

func withAnchor(yaml string) string {
	return strings.Replace(yaml, "project: aurora\n", "project: aurora\ngcp_project: fugaro-aurora\n", 1)
}

func readYAML(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "fugaro.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Exact texts of what the stage prints.
func diffText(path string) string { return path + "\n  + gcp_project: fugaro-aurora\n" }
func addLineText(path string) string {
	return "to let teammates use this installation without setup, add this line to " + path + ": gcp_project: fugaro-aurora (every teammate's CLI and every CI job or pin that runs fugaro against the repository must be on this release before the line is merged: older versions refuse the key as unknown)\n"
}
func updatedText(path string) string {
	return "Updated " + path + " with gcp_project. Review it with git diff and commit it like any change. " + cliCaution + "\n"
}

const cliCaution = "Every teammate's CLI and every CI job or pin that runs fugaro against the repository must be on this release before you merge a change that adds gcp_project: older versions refuse the key as unknown."

func warnedLine(msg string) string { return "warning: " + msg + "\n" }

// stageWarn is the stage's one warning about acme/app's workflow app (base
// web-node): the same text as fugaro init --anchor's FAIL line.
func stageWarn(lcPath string, customBase bool, reasons ...string) string {
	return warnedLine(anchorProblemText("acme/app", "app", "web-node", lcPath, reasons, customBase))
}

func TestRepoStageAnchorYesWrites(t *testing.T) {
	yaml := anchorYAML()
	s, out, dir := anchorRig(t, anchorLC(), yaml, "")
	putRecord(t, "0.3.1")
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.tg.root, "fugaro.yaml")
	if got := readYAML(t, dir); got != withAnchor(yaml) {
		t.Errorf("file:\n%s", got)
	}
	want := diffText(path) + "  confirmed by --yes\n" + updatedText(path) + stageWarn("", false, reasonRecordVersionOld("0.3.1"))
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
}

func TestRepoStageAnchorInteractiveYes(t *testing.T) {
	yaml := anchorYAML()
	s, out, dir := anchorRig(t, anchorLC(), yaml, "y\n")
	putRecord(t, "0.4.0")
	if _, err := s.Apply(t.Context(), initflow.Env{Interactive: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.tg.root, "fugaro.yaml")
	if got := readYAML(t, dir); got != withAnchor(yaml) {
		t.Errorf("file:\n%s", got)
	}
	want := diffText(path) + "Write this to " + path + "? [y/N]: " + updatedText(path)
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
}

func TestRepoStageAnchorInteractiveNoLeavesTheFile(t *testing.T) {
	yaml := anchorYAML()
	s, out, dir := anchorRig(t, anchorLC(), yaml, "n\n")
	putRecord(t, "0.4.0")
	if _, err := s.Apply(t.Context(), initflow.Env{Interactive: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.tg.root, "fugaro.yaml")
	want := diffText(path) + "Write this to " + path + "? [y/N]: " + addLineText(path)
	if got := readYAML(t, dir); got != yaml || out.String() != want {
		t.Errorf("file:\n%s\noutput:\n%s\nwant:\n%s", got, out, want)
	}
}

// No --yes and no terminal: nothing is written, the run does not fail, and
// the line to add is printed.
func TestRepoStageAnchorNonInteractiveWithoutYes(t *testing.T) {
	yaml := anchorYAML()
	s, out, dir := anchorRig(t, anchorLC(), yaml, "")
	putRecord(t, "0.3.1")
	if _, err := s.Apply(t.Context(), initflow.Env{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.tg.root, "fugaro.yaml")
	want := diffText(path) + addLineText(path) + stageWarn("", false, reasonRecordVersionOld("0.3.1"))
	if got := readYAML(t, dir); got != yaml || out.String() != want {
		t.Errorf("file:\n%s\noutput:\n%s\nwant:\n%s", got, out, want)
	}
}

func TestRepoStageAnchorPlanWritesNothing(t *testing.T) {
	yaml := anchorYAML()
	s, out, dir := anchorRig(t, anchorLC(), yaml, "")
	putRecord(t, "0.4.0")
	if _, err := s.Plan(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.tg.root, "fugaro.yaml")
	want := diffText(path) + "  (plan only: fugaro.yaml is not written)\n"
	if got := readYAML(t, dir); got != yaml || out.String() != want {
		t.Errorf("file:\n%s\noutput:\n%s\nwant:\n%s", got, out, want)
	}
	// --plan-only through Apply is the same.
	s.e.r.o.planOnly = true
	out2 := out.String()
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if got := readYAML(t, dir); got != yaml || strings.TrimPrefix(out.String(), out2) != want {
		t.Errorf("apply under --plan-only:\n%s\n%s", got, out)
	}
}

// A repository the project does not list yet: its typed authorization
// comes first, and the line still has its own single prompt.
func TestRepoStageAnchorNewRepoKeepsOnePromptForTheLine(t *testing.T) {
	yaml := anchorYAML()
	lc := anchorLC()
	lc.Repos = nil
	dir := repoCheckout(t, githubOrigin, yaml)
	t.Chdir(dir)
	fakeTerminal(t)
	e, out := stageEngine(t, "acme/app\ny\n", &initOptions{githubAppID: "12345"})
	e.lc = lc
	s := newRepositoryStage(e)
	s.engineStage.run = func(context.Context) error { return nil }
	records = map[string]string{}
	prev := openRecordBucket
	openRecordBucket = openFakeRecords
	t.Cleanup(func() { openRecordBucket = prev })
	if _, err := s.Apply(t.Context(), initflow.Env{Interactive: true}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readYAML(t, dir); got != withAnchor(yaml) {
		t.Errorf("file:\n%s\noutput:\n%s", got, out)
	}
	if n := strings.Count(out.String(), "[y/N]"); n != 1 {
		t.Errorf("%d line prompts:\n%s", n, out)
	}
}

func TestRepoStageAnchorCustomBucketSkips(t *testing.T) {
	lc := anchorLC()
	lc.RunsBucket = "my-own-bucket"
	yaml := anchorYAML()
	s, out, dir := anchorRig(t, lc, yaml, "")
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatal(err)
	}
	want := "note: gcp_project is not written to fugaro.yaml: shared config needs the default runs-bucket name fugaro-runs-fugaro-aurora (this installation's is \"my-own-bucket\")\n"
	if got := readYAML(t, dir); got != yaml || out.String() != want {
		t.Errorf("file:\n%s\noutput:\n%s", got, out)
	}
}

// A differing value stops the stage after the engine: the message says the
// onboarding is done and rerunning is safe.
func TestRepoStageAnchorDiffersRefused(t *testing.T) {
	yaml := strings.Replace(anchorYAML(), "project: aurora\n", "project: aurora\ngcp_project: fugaro-other\n", 1)
	s, _, dir := anchorRig(t, anchorLC(), yaml, "")
	_, err := s.Apply(t.Context(), initflow.Env{Yes: true})
	if err == nil || !strings.Contains(err.Error(), "the repository is onboarded; only the gcp_project line of fugaro.yaml was not written") ||
		!strings.Contains(err.Error(), "gcp_project: fugaro-other, not fugaro-aurora") || !strings.Contains(err.Error(), "rerunning fugaro init is safe") {
		t.Fatalf("err = %v", err)
	}
	if ExitCode(err) != ExitUserError || readYAML(t, dir) != yaml {
		t.Errorf("exit %d, file changed: %v", ExitCode(err), readYAML(t, dir) != yaml)
	}
}

// A file shape the edit refuses is told to the user, never guessed at, and
// does not fail the run.
func TestRepoStageAnchorUnsafeShapeIsByHand(t *testing.T) {
	yaml := strings.Replace(anchorYAML(), "project: aurora\n", "\"project\": aurora\n", 1)
	s, out, dir := anchorRig(t, anchorLC(), yaml, "")
	putRecord(t, "0.4.0")
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatal(err)
	}
	want := "note: the project: line is not a simple one-line value; fugaro.yaml is not edited: add this line to it by hand, at the top level: gcp_project: fugaro-aurora\n"
	if readYAML(t, dir) != yaml || out.String() != want {
		t.Errorf("output:\n%s", out)
	}
}

// Already set: no edit, and the old-image warning still comes on a second
// run before the change is merged.
func TestRepoStageAnchorAlreadySetStillWarns(t *testing.T) {
	yaml := withAnchor(anchorYAML())
	s, out, dir := anchorRig(t, anchorLC(), yaml, "")
	putRecord(t, "0.3.1")
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if readYAML(t, dir) != yaml || out.String() != stageWarn("", false, reasonRecordVersionOld("0.3.1")) {
		t.Errorf("output:\n%s", out)
	}
	// A current image: silent.
	s, out, _ = anchorRig(t, anchorLC(), yaml, "")
	putRecord(t, "0.4.0")
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil || out.String() != "" {
		t.Errorf("err %v, output:\n%s", err, out)
	}
}

// The stage's warning is --anchor's FAIL text: a development CLI's image and
// a base image the local config pins to a dev tag are warned about too.
func TestRepoStageAnchorWarnsDevImageAndCustomBase(t *testing.T) {
	s, out, _ := anchorRig(t, anchorLC(), withAnchor(anchorYAML()), "")
	putRecord(t, "dev")
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if want := stageWarn("", false, reasonDevCLI("dev")); out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
	lc := anchorLC()
	const ref = "us-east5-docker.pkg.dev/fugaro-aurora/fugaro-base/fugaro-web-node:dev-8fe2647"
	lc.Region = "us-east5"
	lc.BaseImages = map[string]string{"web-node": ref}
	s, out, _ = anchorRig(t, lc, withAnchor(anchorYAML()), "")
	s.e.path = "/home/me/.config/fugaro/projects/aurora.yaml"
	putRecord(t, "0.4.0")
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if want := stageWarn(s.e.path, true, reasonConfigBaseCustom(ref, "web-node")); out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
}

func TestRepoStageAnchorNoRecordWarnsOld(t *testing.T) {
	s, out, _ := anchorRig(t, anchorLC(), withAnchor(anchorYAML()), "")
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if out.String() != stageWarn("", false, reasonNoRecord()) {
		t.Errorf("output:\n%s", out)
	}
}

// A record that cannot be read or parsed is not "old": the warning says the
// image's age is unknown, and the run does not fail.
func TestRepoStageAnchorUnreadableRecord(t *testing.T) {
	s, out, _ := anchorRig(t, anchorLC(), withAnchor(anchorYAML()), "")
	putRecordData(t, "not json")
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "its build record could not be read (") || !strings.Contains(got, "so its age is unknown") ||
		!strings.HasSuffix(got, "(1) fugaro image refresh --repo acme/app --workflow app in the checkout, in your own terminal window (interactive: needs a real terminal, cannot run in CI, has no --yes; it copies this release's web-node base image, points the daily image check job at it and rebuilds the image), (2) fugaro init --anchor, before you merge a change that adds gcp_project\n") {
		t.Errorf("output:\n%s", got)
	}
}

// The bucket itself failing to open: a warning per workflow, never a failure.
func TestRepoStageAnchorBucketFailure(t *testing.T) {
	s, out, _ := anchorRig(t, anchorLC(), withAnchor(anchorYAML()), "")
	openRecordBucket = func(context.Context, string) (*blobx.Bucket, error) { return nil, errors.New("boom") }
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if want := stageWarn("", false, reasonUnreadable("boom")); out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
}

// With fake endpoints in the local config the record read never opens the
// real gs:// bucket: the warning says the image age is unknown.
func TestRepoStageAnchorFakeEndpointsSkipTheRecordRead(t *testing.T) {
	lc := anchorLC()
	lc.Endpoints.NoAuth = true
	s, out, _ := anchorRig(t, lc, withAnchor(anchorYAML()), "")
	old := skipGSOnFakeEndpoints
	skipGSOnFakeEndpoints = true
	t.Cleanup(func() { skipGSOnFakeEndpoints = old })
	openRecordBucket = func(context.Context, string) (*blobx.Bucket, error) {
		t.Error("the record bucket was opened against fake endpoints")
		return nil, errors.New("opened")
	}
	if _, err := s.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "so its age is unknown") {
		t.Errorf("output:\n%s", out)
	}
}
