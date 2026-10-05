package cli

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// firstRunEnv isolates a run with no project config anywhere, outside any
// checkout, with the environment that could name a project cleared.
func firstRunEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	isolateProjects(t, dir)
	t.Setenv("HOME", filepath.Join(dir, "home"))
	for _, k := range []string{"GOOGLE_CLOUD_PROJECT", "GOOGLE_PROJECT", "CLOUDSDK_CORE_PROJECT", "CLOUDSDK_CONFIG", "GOOGLE_APPLICATION_CREDENTIALS"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	return dir
}

func TestNameDefaultSanitised(t *testing.T) {
	for in, want := range map[string]string{
		"Acme Corp":                    "acme-corp",
		"acme":                         "acme",
		"  --Acme_Inc.--  ":            "acme-inc",
		"UPPER.case":                   "upper-case",
		"ünï":                          "n",
		"---":                          "",
		"":                             "",
		strings.Repeat("a", 60):        strings.Repeat("a", 40),
		strings.Repeat("a", 39) + "-b": strings.Repeat("a", 39),
		"1password":                    "1password",
	} {
		if got := sanitiseName(in); got != want {
			t.Errorf("sanitiseName(%q) = %q, want %q", in, got, want)
		}
	}
	// From the checkout's origin owner, else the directory's name.
	firstRunEnv(t)
	root := t.TempDir()
	dir := filepath.Join(root, "My App")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.IsolateGit(t)
	testutil.Git(t, dir, "init", "-q")
	t.Chdir(dir)
	if got := suggestName(t.Context()); got != "my-app" {
		t.Errorf("without an origin: %q, want my-app", got)
	}
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/Acme-Corp/webapp.git")
	if got := suggestName(t.Context()); got != "acme-corp" {
		t.Errorf("with an origin: %q, want acme-corp", got)
	}
}

// The GCP project is suggested from GOOGLE_CLOUD_PROJECT or the credentials'
// quota project, never from gcloud's configured default project, whichever
// way gcloud holds it.
func TestProjectIDNeverFromGcloudDefault(t *testing.T) {
	dir := firstRunEnv(t)
	t.Setenv("CLOUDSDK_CORE_PROJECT", "gcloud-default-1")
	gc := filepath.Join(dir, "home", ".config", "gcloud")
	if err := os.MkdirAll(filepath.Join(gc, "configurations"), 0o755); err != nil {
		t.Fatal(err)
	}
	for f, body := range map[string]string{
		"configurations/config_default": "[core]\nproject = gcloud-default-2\n",
		"properties":                    "[core]\nproject = gcloud-default-3\n",
	} {
		if err := os.WriteFile(filepath.Join(gc, f), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := suggestGCPProject(); got != "" {
		t.Fatalf("suggested %q from gcloud's own default project", got)
	}
	// At the prompt there is no suggestion, so Enter alone is not an answer.
	fakeTerminal(t)
	var out strings.Builder
	r := &initRun{o: &initOptions{name: "aurora", cloud: cloudOptions{region: "us-east5"}}, w: &out, in: bufio.NewReader(strings.NewReader("\n\nproj-5678\n")), cmd: NewRootCmd()}
	r.cmd.SetIn(strings.NewReader(""))
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.o.cloud.gcpProject != "proj-5678" || strings.Contains(out.String(), "gcloud-default") || strings.Contains(out.String(), "GCP project ID [") {
		t.Errorf("gcp project %q:\n%s", r.o.cloud.gcpProject, out.String())
	}

	// What is allowed: the environment, then the credentials' quota project.
	adc := filepath.Join(gc, "application_default_credentials.json")
	if err := os.WriteFile(adc, []byte(`{"type":"authorized_user","client_secret":"s","quota_project_id":"quota-proj-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := suggestGCPProject(); got != "quota-proj-1" {
		t.Errorf("quota project: %q", got)
	}
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-proj-1")
	if got := suggestGCPProject(); got != "env-proj-1" {
		t.Errorf("GOOGLE_CLOUD_PROJECT: %q", got)
	}
}

// A first run in a terminal asks for the three inputs once, each with its
// suggestion; Enter takes it.
func TestFirstRunAsksWithSuggestions(t *testing.T) {
	firstRunEnv(t)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-proj-1")
	root := t.TempDir()
	testutil.IsolateGit(t)
	testutil.Git(t, root, "init", "-q")
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(root)
	fakeTerminal(t)
	var out strings.Builder
	r := &initRun{o: &initOptions{}, w: &out, in: bufio.NewReader(strings.NewReader("\n\n\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatal(err)
	}
	if o := r.o; o.name != "acme" || o.cloud.gcpProject != "env-proj-1" || o.cloud.region != "us-east5" {
		t.Errorf("name %q, project %q, region %q\n%s", o.name, o.cloud.gcpProject, o.cloud.region, out.String())
	}
	for _, want := range []string{"[acme]", "[env-proj-1]", "[us-east5]"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no suggestion %s:\n%s", want, out.String())
		}
	}
	// A bad answer is asked again; input that ends is a refusal.
	r = &initRun{o: &initOptions{name: "aurora", cloud: cloudOptions{region: "us-east5"}}, w: &out, in: bufio.NewReader(strings.NewReader("Not A Project\nproj-1234\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil || r.o.cloud.gcpProject != "proj-1234" {
		t.Errorf("a bad answer: %v, %q", err, r.o.cloud.gcpProject)
	}
	r = &initRun{o: &initOptions{name: "aurora", cloud: cloudOptions{region: "us-east5", gcpProject: ""}}, w: &out, in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	if err := r.gatherInputs(t.Context()); err == nil || ExitCode(err) != ExitUserError {
		t.Errorf("ended input: exit %d, %v", ExitCode(err), err)
	}
}

// --non-interactive never asks and names every missing flag in one error:
// the three inputs, and the GitHub App's ID of a GitHub checkout whose
// default branch has its fugaro.yaml.
func TestNonInteractiveNamesFlags(t *testing.T) {
	firstRunEnv(t)
	fakeTerminal(t) // a terminal does not make it ask
	_, _, err := executeStdin(t, "aurora\n", "init", "--non-interactive")
	if ExitCode(err) != ExitUserError || err == nil || strings.Contains(err.Error(), "\n") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	for _, f := range []string{"--non-interactive: missing", "--name", "--gcp-project", "--region"} {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("the error lacks %q: %v", f, err)
		}
	}
	if strings.Contains(err.Error(), "--github-app-id") {
		t.Errorf("no checkout, no App ID: %v", err)
	}
	// What was given is not named again.
	_, _, err = executeStdin(t, "", "init", "--non-interactive", "--name", "aurora", "--region", "us-east5")
	if err == nil || !strings.Contains(err.Error(), "--gcp-project") || strings.Contains(err.Error(), "--name") || strings.Contains(err.Error(), "--region") {
		t.Errorf("only --gcp-project is missing: %v", err)
	}
	// --yes does not supply inputs either, and without --non-interactive and
	// without a terminal the same flags are named.
	_, _, err = executeStdin(t, "aurora\n", "init", "--yes")
	if err == nil || !strings.Contains(err.Error(), "--name") || !strings.Contains(err.Error(), "--gcp-project") {
		t.Errorf("--yes: %v", err)
	}
}

// In a GitHub checkout whose default branch has a fugaro.yaml, the App's ID
// is one more flag in the same error.
func TestNonInteractiveNamesAppIDToo(t *testing.T) {
	firstRunEnv(t)
	root := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(root)
	// A checkout's own fugaro.yaml does not name a new installation by itself.
	_, _, err := executeStdin(t, "", "init", "--non-interactive", "--region", "us-east5")
	if err == nil || !strings.Contains(err.Error(), "--name <name>") || !strings.Contains(err.Error(), "fugaro.yaml says aurora") || !strings.Contains(err.Error(), "--gcp-project") {
		t.Errorf("%v", err)
	}
	// Named, and the repository opted in: the App ID is one more.
	_, _, err = executeStdin(t, "", "init", "--non-interactive", "--region", "us-east5", "--name", "aurora", "--onboard-repo", "acme/app")
	if err == nil || !strings.Contains(err.Error(), "--gcp-project") || !strings.Contains(err.Error(), "--github-app-id") || strings.Contains(err.Error(), "--name") {
		t.Errorf("%v", err)
	}
	// A repository that is not opted in is never asked about here.
	_, _, err = executeStdin(t, "", "init", "--non-interactive", "--region", "us-east5", "--name", "aurora")
	if err == nil || strings.Contains(err.Error(), "--github-app-id") {
		t.Errorf("%v", err)
	}
}

// With a project config, nothing is asked: the answers waiting on stdin are
// never read.
func TestPromptsSkippedWhenConfigExists(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	fakeTerminal(t)
	spy := &readerSpy{Reader: strings.NewReader("never-read\n")}
	run := &initRun{o: &initOptions{}, w: &strings.Builder{}, in: bufio.NewReader(spy), cmd: NewRootCmd()}
	if err := run.gatherInputs(t.Context()); err != nil || spy.read {
		t.Fatalf("err %v, read %v", err, spy.read)
	}
	out, _, err := executeStdin(t, initProjectName+"\n", "init", "--plan-only")
	if err != nil || strings.Contains(out, "first run") || strings.Contains(out, "Region [") {
		t.Fatalf("err %v\n%s", err, out)
	}
}

// repoCheckout is a git checkout on main whose origin is origin and whose
// default branch holds yaml as fugaro.yaml ("" for none), the working
// directory's checkout once the caller chdirs into it.
func repoCheckout(t *testing.T, origin, yaml string) string {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "remote", "add", "origin", origin)
	file := "README"
	if yaml != "" {
		file = "fugaro.yaml"
	} else {
		yaml = "x\n"
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "add", ".")
	testutil.Git(t, dir, "commit", "-q", "-m", "init")
	fetched(t, dir)
	return dir
}

// fetched makes origin/main what main is, as a fetch would (the stages read
// only the remote-tracking ref).
func fetched(t *testing.T, dir string) {
	t.Helper()
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "main")
	testutil.Git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
}

// A checkout's fugaro.yaml does not silently name a new, permanent
// installation: the name is typed (no default), or passed with --name.
func TestCheckoutProjectNeverNamesANewInstallation(t *testing.T) {
	firstRunEnv(t)
	root := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "evil-project", ""))
	t.Chdir(root)
	fakeTerminal(t)
	var out strings.Builder
	r := &initRun{o: &initOptions{cloud: cloudOptions{gcpProject: "proj-1234", region: "us-east5"}}, w: &out, in: bufio.NewReader(strings.NewReader("\nmy-team\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.o.name != "my-team" || !strings.Contains(out.String(), "a value is needed") || strings.Contains(out.String(), "[evil-project]") || !strings.Contains(out.String(), "fugaro.yaml says evil-project") {
		t.Errorf("name %q\n%s", r.o.name, out.String())
	}
	// With --name it is the user's own word.
	r = &initRun{o: &initOptions{name: "evil-project", cloud: cloudOptions{gcpProject: "proj-1234", region: "us-east5"}}, w: &out, in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Errorf("--name: %v", err)
	}
}

// Adopt mode (a new machine, an existing installation): the name is asked
// after the GCP project and region, and its suggestion is the installation's
// own name, not the directory's.
func TestFirstRunSuggestsTheInstallationsName(t *testing.T) {
	firstRunEnv(t)
	root := filepath.Join(t.TempDir(), "My App")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.IsolateGit(t)
	testutil.Git(t, root, "init", "-q")
	t.Chdir(root)
	fakeTerminal(t)
	var asked []string
	old := installationName
	installationName = func(_ context.Context, gcpProject, region, runsBucket string) string {
		asked = append(asked, gcpProject+" "+region)
		return "team-x"
	}
	t.Cleanup(func() { installationName = old })
	var out strings.Builder
	r := &initRun{o: &initOptions{}, w: &out, in: bufio.NewReader(strings.NewReader("proj-1234\n\n\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.o.name != "team-x" || !strings.Contains(out.String(), "[team-x]") || strings.Contains(out.String(), "my-app") {
		t.Errorf("name %q:\n%s", r.o.name, out.String())
	}
	if len(asked) != 1 || asked[0] != "proj-1234 us-east5" {
		t.Errorf("the installation was looked up with %q, want once with the GCP project and region already known", asked)
	}
	// Typing another name is still the user's word (the run then refuses it
	// against the installation as before); --name is never replaced.
	asked = nil
	r = &initRun{o: &initOptions{name: "mine", cloud: cloudOptions{gcpProject: "proj-1234", region: "us-east5"}}, w: &out, in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil || r.o.name != "mine" || len(asked) != 0 {
		t.Errorf("--name: %v %q %v", err, r.o.name, asked)
	}
	// Nothing found: the ordinary suggestion.
	installationName = func(context.Context, string, string, string) string { return "" }
	out.Reset()
	r = &initRun{o: &initOptions{}, w: &out, in: bufio.NewReader(strings.NewReader("proj-1234\n\n\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil || r.o.name != "my-app" {
		t.Errorf("no installation: %v %q\n%s", err, r.o.name, out.String())
	}
}

// The installation's name comes from its runs bucket's marker, accepted only
// from a bucket of the GCP project that is Fugaro's.
func TestReadInstallationName(t *testing.T) {
	r := newInitRig(t)
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := readInstallationName(t.Context(), lc); got != "" {
		t.Errorf("no bucket: %q", got)
	}
	r.markedRuns(t, "team-x", initProject)
	if got := readInstallationName(t.Context(), lc); got != "team-x" {
		t.Errorf("marked: %q, want team-x", got)
	}
	// A marker naming another GCP project, or an invalid name, is not used.
	r2 := newInitRig(t)
	lc2, _ := localcfg.Load(r2.cfg)
	r2.markedRuns(t, "team-x", "other-proj-1")
	if got := readInstallationName(t.Context(), lc2); got != "" {
		t.Errorf("a marker for another GCP project: %q", got)
	}
	r3 := newInitRig(t)
	lc3, _ := localcfg.Load(r3.cfg)
	r3.markedRuns(t, "Not A Name", initProject)
	if got := readInstallationName(t.Context(), lc3); got != "" {
		t.Errorf("an invalid name: %q", got)
	}
}
