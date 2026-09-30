package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const projectHeader = "project: aurora (GCP proj-1234)"

// discard is a command's stderr, for functions tested without a command.
func discard() io.Writer { return io.Discard }

// firstLine is s's first line.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// gitCheckout makes a git checkout at dir whose fugaro.yaml is yaml.
func gitCheckout(t *testing.T, dir, yaml string) string {
	t.Helper()
	testutil.IsolateGit(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeProject writes project name's config, in GCP project gcp, to the
// project config directory.
func writeProject(t *testing.T, name, gcp string) string {
	t.Helper()
	path, err := localcfg.ProjectPath(os.Getenv, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := "version: 1\nname: " + name + "\ngcp_project: " + gcp + "\nregion: us-east5\nruns_bucket: fugaro-runs-" + gcp + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Every cloud command says which project it acts on, as the first line
// of its stderr, even when it fails after the project is selected.
func TestCloudCommandsPrintProjectHeader(t *testing.T) {
	newCloudFixture(t)
	const ref = "20260927-100000-abcd"
	for _, args := range [][]string{
		{"ls"}, {"run", "--retry", ref}, {"cancel", ref}, {"logs", ref}, {"diagnose", ref},
		{"secrets", "ls", "--repo", "acme/app"}, {"image", "status"}, {"image", "check"},
	} {
		_, stderr, _ := execute(t, args...)
		if got := firstLine(stderr); got != projectHeader {
			t.Errorf("%s: first stderr line %q, want %q (stderr %q)", strings.Join(args, " "), got, projectHeader, stderr)
		}
	}
}

// --gcp-project names the selected project's GCP project, or is refused:
// it can't point a project config at another GCP project.
func TestGCPProjectFlagMustAgree(t *testing.T) {
	newCloudFixture(t)
	_, stderr, err := execute(t, "ls", "--gcp-project", "other-proj-99")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "other-proj-99") || !strings.Contains(err.Error(), "proj-1234") {
		t.Fatalf("another GCP project: exit %d, err %v", ExitCode(err), err)
	}
	if firstLine(stderr) != projectHeader {
		t.Errorf("stderr %q, want the header first", stderr)
	}
	if _, _, err := execute(t, "ls", "--gcp-project", "proj-1234"); err != nil {
		t.Fatalf("the same GCP project: %v", err)
	}
}

// --project takes a project's name; one with no project config is refused,
// listing the projects there are, and saying where the GCP project goes.
func TestProjectFlagNamesNoProject(t *testing.T) {
	newCloudFixture(t)
	writeProject(t, "borealis", "proj-5678")
	for _, name := range []string{"cyan", "proj-1234"} {
		_, _, err := execute(t, "ls", "--project", name)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("--project %s: exit %d, err %v", name, ExitCode(err), err)
		}
		for _, want := range []string{"aurora, borealis", "--gcp-project"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("--project %s: err %q, want it to say %q", name, err, want)
			}
		}
	}
	// The fixture's own name selects it.
	if _, stderr, err := execute(t, "ls", "--project", "aurora"); err != nil || firstLine(stderr) != projectHeader {
		t.Fatalf("--project aurora: %v, stderr %q", err, stderr)
	}
}

// --config names a project config, and --project a project: when they
// name different projects, the command is refused rather than guessing.
func TestSelectConfigWithOtherProjectFlag(t *testing.T) {
	newCloudFixture(t)
	aurora := os.Getenv("FUGARO_CONFIG")
	writeProject(t, "borealis", "proj-5678")
	_, _, err := execute(t, "ls", "--config", aurora, "--project", "borealis")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--config names project aurora, but --project says borealis") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// Each --json object output names the project.
func TestJSONOutputsCarryProject(t *testing.T) {
	f := newCloudFixture(t)
	project := func(args ...string) string {
		t.Helper()
		out, _, err := execute(t, args...)
		if err != nil {
			t.Fatalf("%s: %v", strings.Join(args, " "), err)
		}
		var doc struct {
			Project string `json:"project"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return doc.Project
	}
	const launched, finished = "20260927-100000-abcd", "20260927-100000-bbbb"
	exec := seedRun(t, f, finished, "", "", true)
	f.run.SetState(exec, backend.StateSucceeded)
	for _, args := range [][]string{
		{"ls", "--json"},
		{"run", "--repo", "acme/app", "--run-id", launched, "--json", "Add a feature"},
		{"cancel", "--json", finished},
		{"diagnose", "--json", finished},
	} {
		if got := project(args...); got != "aurora" {
			t.Errorf("%s: project %q, want aurora", strings.Join(args, " "), got)
		}
	}
}

// fugaro init with no project config creates projects/<name>.yaml from
// --name, --gcp-project and --region. (Writing it needs the installation's
// outputs, a cloud call, so the file is written here the way writeConfig
// does.)
func TestInitCreatesProjectConfig(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir()) // outside any checkout
	o := &initOptions{name: "aurora", cloud: cloudOptions{gcpProject: "proj-1234", region: "us-east5", stderr: discard}}
	lc, path, old, err := loadInitConfig(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := localcfg.ProjectPath(os.Getenv, "aurora")
	if path != want || old != nil || lc.Name != "aurora" || lc.GCPProject != "proj-1234" || lc.Region != "us-east5" || lc.RunsBucket != "fugaro-runs-proj-1234" {
		t.Fatalf("config %+v at %s (old %q)", lc, path, old)
	}
	cmd := newInitCmd()
	cmd.SetOut(io.Discard)
	r := newInitRun(cmd, &initOptions{yes: true})
	if err := r.writeLocalConfig(lc, path, old, true); err != nil {
		t.Fatal(err)
	}
	got, file, err := localcfg.LoadProject(os.Getenv, "aurora")
	if err != nil || file != want || got.Name != "aurora" || got.GCPProject != "proj-1234" {
		t.Fatalf("written: %+v at %s, %v", got, file, err)
	}
	data, _ := os.ReadFile(want)
	if !strings.Contains(string(data), "name: aurora\n") || !strings.Contains(string(data), "gcp_project: proj-1234\n") {
		t.Fatalf("file:\n%s", data)
	}
	// With the project config there, --name must be its name.
	o.name = "borealis"
	if _, _, _, err := loadInitConfig(context.Background(), o); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "borealis") {
		t.Fatalf("another --name: %v", err)
	}
	o.name = "aurora"
	if lc, _, old, err := loadInitConfig(context.Background(), o); err != nil || lc.Name != "aurora" || old == nil {
		t.Fatalf("the same --name: %+v, %v", lc, err)
	}
	// Another project beside it is set up by naming it with --project.
	o.name, o.cloud.project, o.cloud.gcpProject = "borealis", "borealis", "proj-5678"
	lc, path, old, err = loadInitConfig(context.Background(), o)
	want, _ = localcfg.ProjectPath(os.Getenv, "borealis")
	if err != nil || lc.Name != "borealis" || lc.GCPProject != "proj-5678" || path != want || old != nil {
		t.Fatalf("another project: %+v at %s, %v", lc, path, err)
	}
}

// With no project config and nothing naming the project, fugaro init needs
// --name; it refuses before any cloud call.
func TestInitWithoutNameRefused(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"init", "--gcp-project", "proj-1234", "--region", "us-east5"},
		{"init", "--config-only", "--gcp-project", "proj-1234", "--region", "us-east5"},
	} {
		_, _, err := execute(t, args...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "pass --name") {
			t.Errorf("%s: exit %d, err %v", strings.Join(args, " "), ExitCode(err), err)
		}
	}
	_, _, err := execute(t, "init", "--name", "aurora", "--region", "us-east5")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--gcp-project") {
		t.Errorf("no --gcp-project: exit %d, err %v", ExitCode(err), err)
	}
}

// A checkout's fugaro.yaml that doesn't parse still says which project it
// belongs to.
func TestCheckoutProjectFromBrokenFugaroYAML(t *testing.T) {
	ctx := context.Background()
	dir := gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 7\nproject: aurora\ncolour: blue\nworkflows: 3\n")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	co, err := checkoutProject(ctx, filepath.Join(dir, "sub"))
	if err != nil || co == nil || co.Project != "aurora" {
		t.Fatalf("checkout = %+v, %v", co, err)
	}
	if real, _ := filepath.EvalSymlinks(dir); co.Root != real && co.Root != dir {
		t.Fatalf("root = %s, want %s", co.Root, dir)
	}
	// No fugaro.yaml at the toplevel, or no checkout: no checkout.
	bare := filepath.Join(t.TempDir(), "bare")
	testutil.Git(t, "", "init", "-q", bare)
	if co, err := checkoutProject(ctx, bare); err != nil || co != nil {
		t.Fatalf("no fugaro.yaml: %+v, %v", co, err)
	}
	if co, err := checkoutProject(ctx, t.TempDir()); err != nil || co != nil {
		t.Fatalf("no checkout: %+v, %v", co, err)
	}
	// YAML that doesn't decode at all can't say: an error, never a guess.
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte("project: [aurora\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := checkoutProject(ctx, dir); ExitCode(err) != ExitUserError {
		t.Fatalf("undecodable: %v", err)
	}
}

// init --repo PATH onboards PATH's checkout, so PATH's fugaro.yaml selects
// the project, not the working directory's.
func TestInitRepoSelectsFromPath(t *testing.T) {
	isolateProjects(t, t.TempDir())
	writeProject(t, "aurora", "aurora-gcp-1")
	writeProject(t, "borealis", "proj-1234")
	src := t.TempDir()
	gitCheckout(t, filepath.Join(src, "app"), "version: 1\nproject: aurora\n")
	gitCheckout(t, filepath.Join(src, "aurora-app"), "version: 1\nproject: aurora\n")
	gitCheckout(t, filepath.Join(src, "borealis-app"), "version: 1\nproject: borealis\n")
	ctx := context.Background()
	quiet := &initOptions{cloud: cloudOptions{stderr: discard}}

	outside := filepath.Join(src, "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(outside)
	lc, path, old, err := loadRepoConfig(ctx, quiet, "../app")
	if err != nil || lc.Name != "aurora" || lc.GCPProject != "aurora-gcp-1" || !strings.HasSuffix(path, "aurora.yaml") || old == nil {
		t.Fatalf("from outside a checkout: %+v at %s, %v", lc, path, err)
	}
	t.Chdir(filepath.Join(src, "borealis-app"))
	if lc, _, _, err = loadRepoConfig(ctx, quiet, "../aurora-app"); err != nil || lc.Name != "aurora" {
		t.Fatalf("from a borealis checkout: %+v, %v", lc, err)
	}
	// With no PATH, the working directory's checkout.
	if lc, _, _, err = loadRepoConfig(ctx, quiet, "."); err != nil || lc.Name != "borealis" {
		t.Fatalf("the working directory's: %+v, %v", lc, err)
	}
}

// Every Google call of fugaro init uses the GCP project ID, never the
// project's name.
func TestInitGCPCallsUseID(t *testing.T) {
	noEnableWait(t, 5)
	r := newInitRig(t)
	r.stateBucket()
	r.su.Disable("cloudresourcemanager.googleapis.com", r.crm.Server)
	_, _, err := executeStdin(t, "", "init", "--plan-only")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), enableCommand) {
		t.Fatalf("the enable hint: exit %d, err %v", ExitCode(err), err)
	}
	if _, errOut, err := executeStdin(t, "", "init", "--plan-only", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	sawID := false
	for _, reqs := range [][]gcpfake.Request{r.crm.Requests(), r.su.Requests(), r.gcs.Requests()} {
		for _, req := range reqs {
			all := req.Method + " " + req.Path + "?" + req.Query + " " + string(req.Body)
			if strings.Contains(all, "aurora") {
				t.Errorf("a Google call names the project's name: %s", all)
			}
			sawID = sawID || strings.Contains(all, initProject)
		}
	}
	if !sawID {
		t.Fatal("no Google call named the GCP project ID")
	}
}

// fugaro init's typed confirmation is the project's name, not the GCP ID,
// and the prompt names both.
func TestInitConfirmationTypesName(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	fakeTerminal(t)
	out, _, err := executeStdin(t, initProject+"\n", "init")
	if ExitCode(err) != ExitUserError || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("typing the GCP ID: exit %d, err %v, calls %q", ExitCode(err), err, r.calls(t))
	}
	if !strings.Contains(out, "Type aurora to apply to GCP project proj-1234: ") {
		t.Errorf("prompt:\n%s", out)
	}
	if _, _, err := executeStdin(t, "aurora\n", "init"); err != nil || len(r.ran(t, "apply")) != 1 {
		t.Fatalf("typing the name: %v, calls %q", err, r.calls(t))
	}
}

// In a checkout of a project with no project config yet, init creates
// that project's config (the checkout names it); a --name for another
// project is refused.
func TestInitInCheckoutWithoutConfig(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: aurora\n"))
	ctx := context.Background()
	o := &initOptions{configOnly: true, cloud: cloudOptions{gcpProject: "proj-1234", region: "us-east5", stderr: discard}}
	lc, path, _, err := loadInitConfig(ctx, o)
	want, _ := localcfg.ProjectPath(os.Getenv, "aurora")
	if err != nil || lc.Name != "aurora" || lc.GCPProject != "proj-1234" || path != want {
		t.Fatalf("config-only: %+v at %s, %v", lc, path, err)
	}
	o.name = "borealis"
	_, _, _, err = loadInitConfig(ctx, o)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "this checkout belongs to project aurora; --name says borealis") {
		t.Fatalf("--name borealis: exit %d, err %v", ExitCode(err), err)
	}
	// Creating at a --config path that doesn't exist yet: still the
	// checkout's project, and never another --name.
	missing := filepath.Join(t.TempDir(), "local.yaml")
	o.name, o.cloud.config = "", missing
	if lc, path, _, err = loadInitConfig(ctx, o); err != nil || lc.Name != "aurora" || path != missing {
		t.Fatalf("--config %s: %+v at %s, %v", missing, lc, path, err)
	}
	o.name = "borealis"
	if _, _, _, err = loadInitConfig(ctx, o); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--name says borealis") {
		t.Fatalf("--config %s --name borealis: exit %d, err %v", missing, ExitCode(err), err)
	}
	o.name, o.cloud.project = "", "borealis"
	if _, _, _, err = loadInitConfig(ctx, o); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--project says borealis") {
		t.Fatalf("--config %s --project borealis: exit %d, err %v", missing, ExitCode(err), err)
	}
	// Every other command is refused there: it has no config to act on.
	_, _, err = execute(t, "ls")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro init --config-only --gcp-project <id>") {
		t.Fatalf("ls: exit %d, err %v", ExitCode(err), err)
	}
}

// fugaro validate and fugaro config example never need a project config.
func TestValidateWithoutProjectConfig(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir())
	if out, _, err := execute(t, "validate", writeConfig(t, cliMinimalYAML)); err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("validate: %q, %v", out, err)
	}
	if out, _, err := execute(t, "config", "example"); err != nil || !strings.Contains(out, "version: 1") {
		t.Fatalf("config example: %v", err)
	}
}

// A selection's notes come after the header, which is always first.
func TestSelectionNotesFollowHeader(t *testing.T) {
	newCloudFixture(t)
	writeProject(t, "borealis", "proj-5678")
	t.Setenv("FUGARO_PROJECT", "borealis")
	_, stderr, err := execute(t, "ls", "--project", "aurora")
	lines := strings.Split(stderr, "\n")
	if err != nil || len(lines) < 2 || lines[0] != projectHeader || lines[1] != "fugaro: note: ignoring FUGARO_PROJECT (borealis): --project selects aurora" {
		t.Fatalf("ls: %v, stderr %q", err, stderr)
	}

	// fugaro init creating a project config announces it once it exists.
	t.Setenv("FUGARO_PROJECT", "")
	t.Setenv("FUGARO_CONFIG", writeProject(t, "cyan", "proj-9999"))
	t.Chdir(gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: delta\n"))
	var errw strings.Builder
	o := &initOptions{cloud: cloudOptions{gcpProject: "proj-4321", region: "us-east5", stderr: func() io.Writer { return &errw }}}
	if _, _, _, err := loadInitConfig(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(errw.String(), "\n")
	if len(lines) < 2 || lines[0] != "project: delta (GCP proj-4321)" || !strings.HasPrefix(lines[1], "fugaro: note: ignoring FUGARO_CONFIG (project cyan)") {
		t.Fatalf("init: stderr %q", errw.String())
	}
}
