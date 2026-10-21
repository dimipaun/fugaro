package cli

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
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

// noInstallation stubs the read-only "is an installation already there?" lookup
// (installationName) with "no": these tests are about the questions, and none of
// them reaches a cloud.
func noInstallation(t *testing.T) {
	t.Helper()
	old := installationName
	installationName = func(context.Context, string, string, string) string { return "" }
	t.Cleanup(func() { installationName = old })
}

// TestProjectIDDefaultIsFugaroName: the wizard's ID question suggests a new
// project named after this one — "fugaro-" + the Fugaro project's name (D4,
// reconciliation F3) — and Enter takes it. The default is a name to *create*
// (D4, answered 2026-10-20 as recommended), which is why it is not
// suggestGCPProject()'s answer: that project already exists, and adopting it is
// the second suggestion's job.
func TestProjectIDDefaultIsFugaroName(t *testing.T) {
	dir := firstRunEnv(t)
	noInstallation(t)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	fakeTerminal(t)
	// --name is the run's own word for the project, and the region is a flag, so
	// the two Enters are the ID question and the create/adopt question after it
	// (adopt: nothing is created and no check of a creation's rules runs here).
	var out strings.Builder
	r := &initRun{o: &initOptions{name: "aurora", cloud: cloudOptions{region: defaultRegion}}, w: &out, in: bufio.NewReader(strings.NewReader("\n" + setupAnswerAdopt + "\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := r.o.cloud.gcpProject; got != "fugaro-aurora" {
		t.Errorf("Enter took %q, want the new project fugaro-aurora\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "GCP project ID [fugaro-aurora]") {
		t.Errorf("the question does not suggest fugaro-aurora:\n%s", out.String())
	}

	// Without --name the suggestion is built from the name the name question
	// would itself suggest: the checkout's origin owner, else the directory.
	work := filepath.Join(dir, "Acme Webapp")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.IsolateGit(t)
	testutil.Git(t, work, "init", "-q")
	t.Chdir(work)
	out.Reset()
	r = &initRun{o: &initOptions{cloud: cloudOptions{region: defaultRegion}}, w: &out, in: bufio.NewReader(strings.NewReader("\n" + setupAnswerAdopt + "\n\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := r.o.cloud.gcpProject; got != "fugaro-acme-webapp" {
		t.Errorf("the directory's name: id %q, want fugaro-acme-webapp\n%s", got, out.String())
	}
	if got := r.o.name; got != "acme-webapp" {
		t.Errorf("the name question suggests %q, and the id was built from another name\n%s", got, out.String())
	}
}

// TestProjectIDSuggestionIsTheCredentialProjectSecond: the project this
// machine is already working in — GOOGLE_CLOUD_PROJECT or the credentials'
// quota project — is offered as the **second** suggestion, after the
// fugaro-<name> default and with the line saying why (D4). Enter takes the
// first; the second is an answer a person types.
func TestProjectIDSuggestionIsTheCredentialProjectSecond(t *testing.T) {
	firstRunEnv(t)
	noInstallation(t)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-proj-1")
	fakeTerminal(t)
	var out strings.Builder
	r := &initRun{o: &initOptions{name: "aurora", cloud: cloudOptions{region: defaultRegion}}, w: &out, in: bufio.NewReader(strings.NewReader("\n" + setupAnswerAdopt + "\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := r.o.cloud.gcpProject; got != "fugaro-aurora" {
		t.Errorf("Enter took %q, want the default\n%s", got, out.String())
	}
	s := out.String()
	at, second := strings.Index(s, "[fugaro-aurora]"), strings.Index(s, "env-proj-1")
	if second < 0 || !strings.Contains(s, "you are already working in env-proj-1") {
		t.Errorf("the credentials' project is not offered with the line saying why:\n%s", s)
	}
	if at < 0 || second < at {
		t.Errorf("the credentials' project is not the second suggestion:\n%s", s)
	}

	// Typed, it is the answer: the second suggestion is not only a sentence.
	out.Reset()
	r = &initRun{o: &initOptions{name: "aurora", cloud: cloudOptions{region: defaultRegion}}, w: &out, in: bufio.NewReader(strings.NewReader("env-proj-1\n" + setupAnswerAdopt + "\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := r.o.cloud.gcpProject; got != "env-proj-1" {
		t.Errorf("the credentials' project typed: %q\n%s", got, out.String())
	}

	// Nothing to offer second when the credentials name no project, or name the
	// one the default already is.
	t.Setenv("GOOGLE_CLOUD_PROJECT", "fugaro-aurora")
	out.Reset()
	r = &initRun{o: &initOptions{name: "aurora", cloud: cloudOptions{region: defaultRegion}}, w: &out, in: bufio.NewReader(strings.NewReader("\n" + setupAnswerAdopt + "\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "(or ") {
		t.Errorf("the same project is offered twice:\n%s", out.String())
	}
}

// TestProjectIDIsCheckedBeforeAnyCreate: the ID the wizard takes is held to
// what a creation is held to, inside gatherInputs — before the engine exists,
// so before anything is read, planned or created. The prompt accepts an ID
// Google reserves a word in (that rule is not a shape rule), and the create
// answer's own checks refuse it; and the *default* the wizard offers is checked
// the same way, so a project named after this one cannot reach Google with an
// ID Google refuses.
func TestProjectIDIsCheckedBeforeAnyCreate(t *testing.T) {
	firstRunEnv(t)
	noInstallation(t)
	fakeTerminal(t)
	for _, tc := range []struct {
		name   string
		stdin  string
		o      *initOptions
		projID string
	}{
		{
			name:   "an id typed that Google restricts",
			stdin:  "fugaro-google-ads\n" + setupAnswerCreate + "\n",
			projID: "fugaro-google-ads",
		},
		{
			name:   "the default the run suggests",
			stdin:  "\n" + setupAnswerCreate + "\n",
			o:      &initOptions{name: "google-ads", cloud: cloudOptions{region: defaultRegion}},
			projID: "fugaro-google-ads",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := tc.o
			if o == nil {
				o = &initOptions{name: "aurora", cloud: cloudOptions{region: defaultRegion}}
			}
			var out strings.Builder
			r := &initRun{o: o, w: &out, in: bufio.NewReader(strings.NewReader(tc.stdin)), cmd: NewRootCmd()}
			err := r.gatherInputs(t.Context())
			if err == nil {
				t.Fatalf("the run took %q as an ID to create with\n%s", tc.projID, out.String())
			}
			if !strings.Contains(err.Error(), "google") || ExitCode(err) != ExitUserError {
				t.Errorf("err = %v (exit %d), want the refusal naming the restricted word", err, ExitCode(err))
			}
			if r.o.cloud.gcpProject != tc.projID {
				t.Errorf("the id checked is %q, want %q\n%s", r.o.cloud.gcpProject, tc.projID, out.String())
			}
		})
	}

	// The same ID adopted is the installation's business, not this run's: the
	// check belongs to creation (D4's answer, askProjectAnswer).
	var out strings.Builder
	r := &initRun{o: &initOptions{name: "aurora", cloud: cloudOptions{region: defaultRegion}}, w: &out, in: bufio.NewReader(strings.NewReader("fugaro-google-ads\n" + setupAnswerAdopt + "\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Errorf("adopting the id was refused: %v\n%s", err, out.String())
	}
}

// TestProjectIDDefaultIsSanitised: the default is built from a project name,
// and a project name may hold capitals, spaces, an underscore, a non-ASCII
// letter or a leading digit — none of which a project ID holds. Enter is always
// a legal answer: what comes out is a project ID, and a name with nothing
// usable in it suggests nothing rather than something illegal.
func TestProjectIDDefaultIsSanitised(t *testing.T) {
	for in, want := range map[string]string{
		"aurora":                "fugaro-aurora",
		"Acme Corp":             "fugaro-acme-corp",
		"  --Acme_Inc.--  ":     "fugaro-acme-inc",
		"UPPER.case":            "fugaro-upper-case",
		"1password":             "fugaro-1password",
		"ünï":                   "fugaro-n",
		strings.Repeat("a", 60): "fugaro-" + strings.Repeat("a", 23),
		"---":                   "",
		"":                      "",
	} {
		got := defaultGCPProject(in)
		if got != want {
			t.Errorf("defaultGCPProject(%q) = %q, want %q", in, got, want)
			continue
		}
		if got != "" && !infra.ValidProjectID(got) {
			t.Errorf("defaultGCPProject(%q) = %q, which is not a project ID", in, got)
		}
	}
}

// TestProjectIDDefaultIsNotTakenWhenItExists: the ID the wizard suggests is a
// name to create, and a project that already carries it is not created twice.
// The run reads the ID before anything is created — availability and ownership,
// the check the typed confirmation has always rested on — keeps the project it
// found, and asks again (its ID typed at the confirmation) rather than failing.
func TestProjectIDDefaultIsNotTakenWhenItExists(t *testing.T) {
	r := newProjectRig(t)
	id := defaultGCPProject(initProjectName)
	if id == "" {
		t.Fatalf("the wizard suggests no id for the project name %q", initProjectName)
	}
	// There and readable, with no Firebase.
	r.crm.AddProject(id, 424242)
	r.gcs.AddProject(id, 424242)
	r.bill.SetBilling(id, true)
	fakeTerminal(t)
	out := &strings.Builder{}
	cmd := NewRootCmd()
	cmd.SetIn(strings.NewReader(id + "\n"))
	e := &initEngine{
		r: &initRun{o: &initOptions{door: setupDoor.name, createProject: true, cloud: cloudOptions{gcpProject: id, region: defaultRegion}},
			w: out, in: bufio.NewReader(strings.NewReader(id + "\n")), cmd: cmd},
		lc: &localcfg.Config{GCPProject: id, Region: defaultRegion, Name: initProjectName, RunsBucket: "fugaro-runs-" + id},
	}
	e.lc.Endpoints = localcfg.Endpoints{ResourceManager: r.crm.URL + "/", FirebaseManagement: r.fb.URL + "/", CloudBilling: r.bill.URL + "/", NoAuth: true}
	s := newProjectStage(e)
	// First the read, which is where availability and ownership are settled —
	// before anything is created — and it finds the project there.
	st, err := s.Check(t.Context())
	if err != nil {
		t.Fatalf("the check of the wizard's own default id: %v", err)
	}
	if st.State != initflow.Todo || !strings.Contains(st.Detail, "adopting an EXISTING project "+id) {
		t.Errorf("state %q, detail %q: want the existing project noticed, not a creation\n%s", st.State, st.Detail, out)
	}
	// Then the step, which asks again — the id typed at the confirmation — and
	// creates nothing: a project that already carries the default is never made
	// twice, and the run does not fail on it either.
	if _, err := s.Apply(t.Context(), initflow.Env{Interactive: true}); err != nil {
		t.Fatalf("the apply: %v\n%s", err, out)
	}
	if n := len(r.crm.Creates()); n != 0 {
		t.Errorf("the id already exists and %d project(s) were created:\n%s", n, out)
	}
	if adds := r.fb.Adds(); len(adds) != 1 || adds[0] != id {
		t.Errorf("Firebase adds = %v, want the existing project once:\n%s", adds, out)
	}
	if !strings.Contains(out.String(), "adopting an EXISTING project "+id) {
		t.Errorf("the confirmation does not say the project is already there:\n%s", out)
	}
}

// TestGatherInputsNonInteractiveNamesEveryFlag: the ID's default is a
// suggestion, never an answer taken on nobody's behalf. A run that cannot ask —
// --non-interactive, or any of the other flags that turn the questions off —
// still refuses, and the one error names every flag that is missing, the ID's
// among them (D4 changes the suggestion, not what a scripted run must pass).
func TestGatherInputsNonInteractiveNamesEveryFlag(t *testing.T) {
	firstRunEnv(t)
	noInstallation(t)
	fakeTerminal(t) // a terminal does not make it ask
	// Every flag but the ID's: the ID's own default is what a run may not take
	// for a person, so it is the one still missing.
	_, _, err := executeStdin(t, "aurora\n", "setup", "--non-interactive", "--name", "aurora", "--region", defaultRegion)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "--non-interactive: missing") || !strings.Contains(err.Error(), "--gcp-project <id>") {
		t.Errorf("the refusal does not name the id's flag: %v", err)
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("more than one error: %v", err)
	}
	for _, f := range []string{"--name", "--region"} {
		if strings.Contains(err.Error(), f) {
			t.Errorf("the refusal names %s, which the run was given: %v", f, err)
		}
	}

	// Nothing given: every question with a flag is named, in one error.
	_, _, err = executeStdin(t, "", "setup", "--non-interactive")
	if err == nil {
		t.Fatal("a first run with no terminal and no flags exited 0")
	}
	for _, q := range setupQuestions {
		if q.missing == "" {
			continue
		}
		if !strings.Contains(err.Error(), q.missing) {
			t.Errorf("the refusal does not name %q: %v", q.missing, err)
		}
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
	// At the prompt gcloud's default is named nowhere: the suggestion is this
	// run's own name for the project (D4's fugaro-<name>), and Enter takes it.
	// The answers: Enter at the ID, and Enter at the project question the
	// wizard asks next (W1.3).
	noInstallation(t)
	fakeTerminal(t)
	var out strings.Builder
	r := &initRun{o: &initOptions{name: "aurora", cloud: cloudOptions{region: "us-east5"}}, w: &out, in: bufio.NewReader(strings.NewReader("\n\n")), cmd: NewRootCmd()}
	r.cmd.SetIn(strings.NewReader(""))
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.o.cloud.gcpProject != "fugaro-aurora" || strings.Contains(out.String(), "gcloud-default") || strings.Contains(out.String(), "GOOGLE_CLOUD_PROJECT") {
		t.Errorf("gcp project %q:\n%s", r.o.cloud.gcpProject, out.String())
	}
	// A person who means another id types it, and it is what the run takes.
	out.Reset()
	r = &initRun{o: &initOptions{name: "aurora", cloud: cloudOptions{region: "us-east5"}}, w: &out, in: bufio.NewReader(strings.NewReader("proj-5678\n\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.o.cloud.gcpProject != "proj-5678" {
		t.Errorf("the typed id: gcp project %q:\n%s", r.o.cloud.gcpProject, out.String())
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
// suggestion; Enter takes it. (The project question W1.3 puts between the
// region and the name is one more Enter, and is pinned in setup_wizard_test.go.)
func TestFirstRunAsksWithSuggestions(t *testing.T) {
	firstRunEnv(t)
	noInstallation(t)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-proj-1")
	root := t.TempDir()
	testutil.IsolateGit(t)
	testutil.Git(t, root, "init", "-q")
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(root)
	fakeTerminal(t)
	var out strings.Builder
	r := &initRun{o: &initOptions{}, w: &out, in: bufio.NewReader(strings.NewReader("\n\n\n\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The ID's suggestion is the new project named after this one (D4); the
	// project the credentials name is offered beside it, second.
	if o := r.o; o.name != "acme" || o.cloud.gcpProject != "fugaro-acme" || o.cloud.region != "us-east5" {
		t.Errorf("name %q, project %q, region %q\n%s", o.name, o.cloud.gcpProject, o.cloud.region, out.String())
	}
	for _, want := range []string{"[acme]", "[fugaro-acme]", "env-proj-1", "[us-east5]"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no suggestion %s:\n%s", want, out.String())
		}
	}
	// A bad answer is asked again; input that ends is a refusal.
	r = &initRun{o: &initOptions{name: "aurora", cloud: cloudOptions{region: "us-east5"}}, w: &out, in: bufio.NewReader(strings.NewReader("Not A Project\nproj-1234\n\n")), cmd: NewRootCmd()}
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
	_, _, err := executeStdin(t, "aurora\n", "setup", "--non-interactive")
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
	_, _, err = executeStdin(t, "", "setup", "--non-interactive", "--name", "aurora", "--region", "us-east5")
	if err == nil || !strings.Contains(err.Error(), "--gcp-project") || strings.Contains(err.Error(), "--name") || strings.Contains(err.Error(), "--region") {
		t.Errorf("only --gcp-project is missing: %v", err)
	}
	// --yes does not supply inputs either, and without --non-interactive and
	// without a terminal the same flags are named.
	_, _, err = executeStdin(t, "aurora\n", "setup", "--yes")
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
	_, _, err := executeStdin(t, "", "setup", "--non-interactive", "--region", "us-east5")
	if err == nil || !strings.Contains(err.Error(), "--name <name>") || !strings.Contains(err.Error(), "fugaro.yaml says aurora") || !strings.Contains(err.Error(), "--gcp-project") {
		t.Errorf("%v", err)
	}
	// Named, and the repository opted in: the App ID is one more.
	_, _, err = executeStdin(t, "", "setup", "--non-interactive", "--region", "us-east5", "--name", "aurora", "--onboard-repo", "acme/app")
	if err == nil || !strings.Contains(err.Error(), "--gcp-project") || !strings.Contains(err.Error(), "--github-app-id") || strings.Contains(err.Error(), "--name") {
		t.Errorf("%v", err)
	}
	// A repository that is not opted in is never asked about here.
	_, _, err = executeStdin(t, "", "setup", "--non-interactive", "--region", "us-east5", "--name", "aurora")
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
	out, _, err := executeStdin(t, initProjectName+"\n", "setup", "--plan-only")
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
	// Enter to the project question, an empty answer to the name, then the name.
	r := &initRun{o: &initOptions{cloud: cloudOptions{gcpProject: "proj-1234", region: "us-east5"}}, w: &out, in: bufio.NewReader(strings.NewReader("\n\nmy-team\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.o.name != "my-team" || !strings.Contains(out.String(), "a value is needed") || strings.Contains(out.String(), "[evil-project]") || !strings.Contains(out.String(), "fugaro.yaml says evil-project") {
		t.Errorf("name %q\n%s", r.o.name, out.String())
	}
	// With --name it is the user's own word. The project question is still
	// asked: the three flags answered every other question, and that one is
	// not a row `missing` can hold (setup_wizard_test.go), so a first run at a
	// terminal answers it rather than having an answer taken for it.
	r = &initRun{o: &initOptions{name: "evil-project", cloud: cloudOptions{gcpProject: "proj-1234", region: "us-east5"}}, w: &out, in: bufio.NewReader(strings.NewReader(setupAnswerAdopt + "\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Errorf("--name: %v", err)
	}
	if r.o.name != "evil-project" {
		t.Errorf("--name: name %q, want the user's own word", r.o.name)
	}
}

// Adopt mode (a new machine, an existing installation): once the GCP project
// and region are known, the installation in them is read, and its name is the
// run's — never the directory's, and never a question: the run keeps the
// installation it found and converges as `fugaro update` does (D11, W1.3). A
// name typed by hand is still the user's own word.
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
	r := &initRun{o: &initOptions{}, w: &out, in: bufio.NewReader(strings.NewReader("proj-1234\n\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.o.name != "team-x" || r.o.createProject || !strings.Contains(out.String(), setupAlreadySetUp) || strings.Contains(out.String(), "my-app") {
		t.Errorf("name %q, create %v:\n%s", r.o.name, r.o.createProject, out.String())
	}
	if len(asked) != 1 || asked[0] != "proj-1234 us-east5" {
		t.Errorf("the installation was looked up with %q, want once with the GCP project and region already known", asked)
	}
	// Typing another name is still the user's word (the run then refuses it
	// against the installation as before); --name is never replaced, and it is
	// not an answer to the project question: that one is asked, because no flag
	// gave it, and the answer here is adoption.
	asked = nil
	r = &initRun{o: &initOptions{name: "mine", cloud: cloudOptions{gcpProject: "proj-1234", region: "us-east5"}}, w: &out, in: bufio.NewReader(strings.NewReader(setupAnswerAdopt + "\n")), cmd: NewRootCmd()}
	if err := r.gatherInputs(t.Context()); err != nil || r.o.name != "mine" || len(asked) != 0 {
		t.Errorf("--name: %v %q %v", err, r.o.name, asked)
	}
	// Nothing found: the ordinary suggestion (region, the project question, the
	// name — three Enters after the ID).
	installationName = func(context.Context, string, string, string) string { return "" }
	out.Reset()
	r = &initRun{o: &initOptions{}, w: &out, in: bufio.NewReader(strings.NewReader("proj-1234\n\n\n\n")), cmd: NewRootCmd()}
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
