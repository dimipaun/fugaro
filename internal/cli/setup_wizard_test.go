package cli

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// setup_wizard_test.go pins the wizard task W1.3 of
// docs/setup-wizard/implementation-plan.md gives `fugaro setup`, as the owner's
// ruling of 2026-10-23 reshaped it: Fugaro only starts fresh, so there is no
// question about the project — a first run creates it, the project stage
// (init_project.go) refuses an ID that already exists, and the creation is
// never covered by --yes (D12).
//
// The question list itself is setup.go's `setupQuestions`, and
// TestEveryWizardQuestionHasAFlag is the contract on it: every question has a
// flag, so a scripted run never needs a terminal to answer one.

// wizardRun is the wizard on the harness init_inputs_test.go already has: a
// first run (no project config anywhere), a working directory outside any
// checkout, a terminal this test's, and stdout collected. Nothing the wizard
// asks reaches a cloud.
func wizardRun(t *testing.T, stdin string, o *initOptions) (*initRun, *strings.Builder) {
	t.Helper()
	firstRunEnv(t)
	fakeTerminal(t)
	if o == nil {
		o = &initOptions{}
	}
	// The door `fugaro setup` builds, because that is the door these runs are:
	// the wizard asks the one door that can act on the answer.
	o.door = setupDoor.name
	out := &strings.Builder{}
	return &initRun{o: o, w: out, in: bufio.NewReader(strings.NewReader(stdin)), cmd: NewRootCmd()}, out
}

// wizardAnswers is the line that answers each of the wizard's questions, by
// the flag that answers the same question without a terminal: the default
// answer for the ones whose suggestion Enter takes, and a typed value for the
// GCP project — its suggestion is fugaro-<name> (D4), and typing another id is
// what keeps these runs at an id of the test's own choosing rather than one
// built from the temporary directory's name.
var wizardAnswers = map[string]string{
	"--gcp-project": "proj-1234\n",
	"--region":      "\n",
	"--name":        "\n",
	// The bake question's suggestion is yes (IS7), and Enter is the answer
	// every run of this harness that is about something else gives it.
	"--bake-repos": "\n",
}

// stdinFor builds the stdin that answers every question but the one a test
// gives its flag to, in the order the wizard asks them, and returns the
// options that flag sets.
func stdinFor(flag string) string {
	var b strings.Builder
	for _, q := range setupQuestions {
		if q.flag != flag {
			b.WriteString(wizardAnswers[q.flag])
		}
	}
	return b.String()
}

func optionsFor(flag string) *initOptions {
	o := &initOptions{door: setupDoor.name}
	switch flag {
	case "--gcp-project":
		o.cloud.gcpProject = "proj-1234"
	case "--region":
		o.cloud.region = defaultRegion
	case "--name":
		o.name = "aurora"
	case "--bake-repos":
		// The value the flag names, and the fact that it was passed: a run that
		// answers the question with the flag is the run whose answer the local
		// config keeps, either way (init.go's bakeReposChanged).
		o.bakeRepos, o.bakeReposChanged = true, true
	}
	return o
}

// TestEveryWizardQuestionHasAFlag is the plan's new red step, and it is the
// contract in one place: a question the wizard asks is a question a scripted
// run can answer, and the flag that answers it is on `fugaro setup`.
//
// It is checked against the wizard rather than trusted. Every row's question
// (`what`, the name the run prints above its value entry) is printed in the
// table's order when nothing else answers it; it disappears when the row's own
// flag answers the same question; and its flag is one `fugaro setup` declares
// and shows. A question added without a row — or a row for a question nobody
// asks any more — fails here.
//
// The last column is the one a run that never prompts prints, and it is read
// off the product too: a run with no terminal names every question it cannot
// answer, and every row is one it cannot answer for itself.
func TestEveryWizardQuestionHasAFlag(t *testing.T) {
	if len(setupQuestions) == 0 {
		t.Fatal("setupQuestions is empty: the wizard asks something, and every question has a flag")
	}
	seen := map[string]bool{}
	for _, q := range setupQuestions {
		if seen[q.flag] {
			t.Errorf("%s: two rows with one flag", q.flag)
		}
		seen[q.flag] = true
		if q.what == "" || q.label == "" {
			t.Errorf("%s: a row with no question or no label names nothing a run can be held to", q.flag)
		}
		if _, ok := wizardAnswers[q.flag]; !ok {
			t.Errorf("%s: the table names a flag this test has no answer for, so the question it asks cannot be checked", q.flag)
		}
		f := newSetupCmd().Flags().Lookup(strings.TrimPrefix(q.flag, "--"))
		if f == nil {
			t.Errorf("%s: %s asks this question and declares no such flag", q.what, newSetupCmd().Name())
			continue
		}
		if f.Hidden {
			t.Errorf("%s: %s hides the flag that answers it, and the door is the wizard", q.what, newSetupCmd().Name())
		}
	}

	// Asked: the wizard prints the table's questions, and in its order.
	r, out := wizardRun(t, stdinFor(""), nil)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	at := 0
	for _, q := range setupQuestions {
		i := strings.Index(out.String()[at:], q.what)
		if i < 0 && q.flag == "--bake-repos" {
			// Asked under its label: its what is a clause and not a thing to
			// type after "Type".
			i = strings.Index(out.String()[at:], q.label)
		}
		if i < 0 {
			t.Errorf("%s: the wizard did not ask %q, in the order the table lists it:\n%s", q.flag, q.what, out)
			continue
		}
		at += i
	}

	// Answered by the flag: the same question is not asked at all.
	for _, q := range setupQuestions {
		t.Run("answered by "+q.flag, func(t *testing.T) {
			r, out := wizardRun(t, stdinFor(q.flag), optionsFor(q.flag))
			if err := r.gatherInputs(t.Context()); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if strings.Contains(out.String(), q.what) {
				t.Errorf("%s answered the question, and the wizard asked %q anyway:\n%s", q.flag, q.what, out)
			}
		})
	}

	// Cannot be asked: a run with no terminal and no question asked of it names
	// exactly the rows whose answer it needs, and no others.
	t.Run("a run that never prompts", func(t *testing.T) {
		firstRunEnv(t)
		r := &initRun{o: &initOptions{nonInteractive: true}, w: &strings.Builder{}, in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}
		err := r.gatherInputs(t.Context())
		if err == nil {
			t.Fatal("a first run with no terminal and no flags exited 0")
		}
		for _, q := range setupQuestions {
			if q.missing == "" {
				// The run takes this question's answer itself, and the answer
				// it takes creates nothing (see askProjectAnswer): it is not
				// missing, and the error must not demand its flag.
				if strings.Contains(err.Error(), q.flag) {
					t.Errorf("%s: the refusal names %s, and a run that cannot ask takes this answer itself: %v", q.what, q.flag, err)
				}
				continue
			}
			if !strings.Contains(err.Error(), q.missing) {
				t.Errorf("%s: the refusal does not name %q: %v", q.what, q.missing, err)
			}
		}
		// One error, before anything: a run refused for its inputs has not
		// reached the creation at all.
		if r.o.createProject {
			t.Error("a run with no terminal and no flag was set to create a project")
		}
	})
}

// TestSetupAsksNoCreateOrAdoptQuestion: Fugaro only starts fresh, so a first
// run of `setup` asks for the new project's ID, the region and the name, and
// nothing about whether to create the project or use another: the answer is
// creation, recorded on --create-project's own field without a question, and
// the project stage is registered for every first run.
func TestSetupAsksNoCreateOrAdoptQuestion(t *testing.T) {
	r, out := wizardRun(t, stdinFor(""), nil)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, no := range []string{"adopt", "an existing one", "create a new one"} {
		if strings.Contains(out.String(), no) {
			t.Errorf("the wizard still offers a choice about the project (%q):\n%s", no, out)
		}
	}
	if !r.o.createProject {
		t.Error("a first run of setup did not record that it creates its project")
	}
	first := initStages(&initEngine{r: &initRun{o: &initOptions{door: setupDoor.name}}, lc: &localcfg.Config{}})
	if _, ok := stageNamed(first, initflow.Project); !ok {
		t.Errorf("setup's first run does not register the project stage: %v", namesOf(first))
	}
	up := initStages(&initEngine{r: &initRun{o: &initOptions{door: updateDoor.name}}, lc: &localcfg.Config{}})
	if _, ok := stageNamed(up, initflow.Project); ok {
		t.Errorf("update registers the project stage: %v", namesOf(up))
	}
}

// TestFirstRunImpliesCreationOnlyWhereItCanAct: settleCreation sets the
// creation for a first run of `setup` and for nothing else — not `update`,
// which creates nothing, not --config-only or --print-vars, which touch no
// project, and not a run with a project config. A scripted first run is
// creating too (its inputs are flags); what it cannot do is put a person at the
// terminal, which TestSetupCreatesOnlyForAPersonAtTheTerminal pins.
func TestFirstRunImpliesCreationOnlyWhereItCanAct(t *testing.T) {
	for _, tc := range []struct {
		name     string
		o        *initOptions
		firstRun bool
		want     bool
	}{
		{"setup, first run", &initOptions{door: setupDoor.name, cloud: cloudOptions{gcpProject: "proj-1234"}}, true, true},
		{"setup, first run, --yes", &initOptions{door: setupDoor.name, yes: true, cloud: cloudOptions{gcpProject: "proj-1234"}}, true, true},
		{"setup with a project config", &initOptions{door: setupDoor.name, cloud: cloudOptions{gcpProject: "proj-1234"}}, false, false},
		{"update, no project config", &initOptions{door: updateDoor.name, cloud: cloudOptions{gcpProject: "proj-1234"}}, true, false},
		{"setup --config-only", &initOptions{door: setupDoor.name, configOnly: true, cloud: cloudOptions{gcpProject: "proj-1234"}}, true, false},
		{"setup --print-vars", &initOptions{door: setupDoor.name, printVars: true, cloud: cloudOptions{gcpProject: "proj-1234"}}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &initRun{o: tc.o, w: &strings.Builder{}, in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}
			if err := r.settleCreation(tc.firstRun); err != nil {
				t.Fatal(err)
			}
			if r.o.createProject != tc.want {
				t.Errorf("create = %v, want %v", r.o.createProject, tc.want)
			}
		})
	}
}

// TestTheCreationIsHeldToTheFlagsChecks: the implied creation is held to what
// --create-project is held to. check() validates the creation's flags only
// where --create-project is explicit (init.go), so the rules have to be applied
// where a first run's creation is settled — and one of them is a rule the
// prompt cannot know: a project ID may not contain Google's restricted words
// (infra.ValidateNewProjectID). The other is the Firebase pairing: Fugaro uses
// no Firebase project but the installation's own.
func TestTheCreationIsHeldToTheFlagsChecks(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    *initOptions
		want string
	}{
		{
			name: "an ID Google restricts",
			o:    &initOptions{door: setupDoor.name, cloud: cloudOptions{gcpProject: "my-google-proj", region: "us-east5"}},
			want: "google",
		},
		{
			name: "--firebase-project names another project",
			o:    &initOptions{door: setupDoor.name, cloud: cloudOptions{gcpProject: "proj-1234", region: "us-east5"}, firebase: "proj-9999"},
			want: "--firebase-project",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &initRun{o: tc.o, w: &strings.Builder{}, in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}
			err := r.settleCreation(true)
			if err == nil {
				t.Fatalf("%s was taken for a creation", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say what is wrong (%q): %v", tc.want, err)
			}
		})
	}
}

// TestCreationFlagsNeedACreation: a run that creates no project (one with a
// project config) refuses the flags only a creation takes, by name, rather
// than ignoring them: --link-billing would otherwise link nothing, and
// --managed would mark nothing.
func TestCreationFlagsNeedACreation(t *testing.T) {
	for _, tc := range []struct {
		flag string
		o    *initOptions
	}{
		{"--link-billing", &initOptions{door: setupDoor.name, linkBilling: "0123AB-4567CD-89EF01"}},
		{"--managed", &initOptions{door: setupDoor.name, managed: true}},
		{"--parent", &initOptions{door: setupDoor.name, parent: "organizations/123"}},
		{"--display-name", &initOptions{door: setupDoor.name, displayName: "Mine"}},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			r := &initRun{o: tc.o, w: &strings.Builder{}, in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}
			err := r.settleCreation(false)
			if err == nil || !strings.Contains(err.Error(), tc.flag) || ExitCode(err) != ExitUserError {
				t.Errorf("%s on a run that creates nothing: %v", tc.flag, err)
			}
		})
	}
}

// TestSetupCreatesOnlyForAPersonAtTheTerminal: the creation is not its own
// confirmation, and no flag takes a person's place. A first run's project is
// created only where a person is at the terminal, with nothing typed for it
// (T7, docs/no-prompt-policy): --json and a pipe leave it for you, and the
// flags that used to answer it are gone (T11). This is the row W0.1's matrix
// gains for the door that creates.
func TestSetupCreatesOnlyForAPersonAtTheTerminal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
		stdin    string
		args     []string
		created  bool
	}{
		{"a person at the terminal", true, newProj + "\n", nil, true},
		{"a pipe cannot confirm it", false, newProj + "\n", nil, false},
		{"--json cannot confirm it", true, newProj + "\n", []string{"--json"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newProjectRig(t)
			out, _, _ := r.run(t, tc.terminal, tc.stdin, append([]string{"--create-project"}, tc.args...)...)
			cs := r.crm.Creates()
			if tc.created {
				if len(cs) != 1 || cs[0].ID != newProj {
					t.Errorf("creates = %+v, want the one the ID confirmed\n%s", cs, out)
				}
				return
			}
			r.nothingDone(t, tc.name)
			if len(cs) != 0 {
				t.Errorf("%s created a project: %+v", tc.name, cs)
			}
		})
	}
}

// TestSetupOnAnExistingInstallationConvergesAndSaysSo: this machine's own
// project config means the installation is there, so setup says so in one line,
// keeps it and converges as `fugaro update` does — a re-run, not a creation —
// and registers no project stage (D11).
func TestSetupOnAnExistingInstallationConvergesAndSaysSo(t *testing.T) {
	newInitRig(t)
	fakeTerminal(t)
	out, _, err := executeStdin(t, "", "setup")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, setupAlreadySetUp) {
		t.Errorf("the run does not say the installation is already set up:\n%s", out)
	}
	// The account the run prints is where the one --json result document used to
	// be read: --json confirms no step (T7), and the question here is only which
	// steps the run opened.
	if names := stepNames(out); slices.Contains(names, initflow.Project) {
		t.Errorf("the project stage ran on an installation that exists: %q\n%s", names, out)
	}
}

// TestSetupRefusesAnIDThatHoldsAnInstallation: a first run of `setup` whose ID
// already holds a Fugaro installation (the runs bucket's marker names it) is
// refused before the name is asked — Fugaro only starts fresh — and the
// refusal names the one command that uses that installation from this machine
// instead. --create-project changes nothing about it. `update` on the same
// machine is that command: it takes the installation's own name and asks for
// nothing (the installation stage then writes the local config,
// init_new_machine_test.go).
func TestSetupRefusesAnIDThatHoldsAnInstallation(t *testing.T) {
	old := installationName
	installationName = func(context.Context, string, string, string) string { return initProjectName }
	t.Cleanup(func() { installationName = old })

	for name, o := range map[string]*initOptions{
		"the wizard":       nil,
		"--create-project": {cloud: cloudOptions{gcpProject: "proj-1234", region: defaultRegion}, createProject: true, linkBilling: "0123AB-4567CD-89EF01"},
	} {
		t.Run(name, func(t *testing.T) {
			r, out := wizardRun(t, "proj-1234\n"+"\n", o)
			err := r.gatherInputs(t.Context())
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("setup went on with an ID that holds an installation: %v\n%s", err, out)
			}
			for _, want := range []string{"Fugaro only starts fresh: proj-1234 already exists; pick another id", "fugaro update --gcp-project proj-1234 --region " + defaultRegion} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal lacks %q: %v", want, err)
				}
			}
			if strings.Contains(out.String(), questionFor("--name").what) {
				t.Errorf("the run asked for a name before refusing:\n%s", out)
			}
		})
	}

	t.Run("update on a new machine", func(t *testing.T) {
		r, out := wizardRun(t, "proj-1234\n"+"\n", &initOptions{})
		r.o.door = updateDoor.name
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if r.o.name != initProjectName || r.o.createProject {
			t.Errorf("name %q, create %v: want the installation's own name and no creation", r.o.name, r.o.createProject)
		}
		if strings.Contains(out.String(), questionFor("--name").what) {
			t.Errorf("the run asked for a name, and the installation has one:\n%s", out)
		}
	})
}

// TestSetupOutsideACheckoutSkipsTheRepositoryQuestion: outside a checkout there
// is no repository to ask about, and the wizard asks the table's questions and
// nothing else. It is pinned here as a pass-through — W3.4 owns the wording of
// the line that says how to enrol one later.
func TestSetupOutsideACheckoutSkipsTheRepositoryQuestion(t *testing.T) {
	testutil.IsolateGit(t)
	r, out := wizardRun(t, stdinFor(""), nil)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, q := range setupQuestions {
		if !strings.Contains(out.String(), q.what) && !strings.Contains(out.String(), q.label) {
			t.Errorf("the wizard did not ask %q outside a checkout:\n%s", q.what, out)
		}
	}
	for _, repo := range []string{"onboard", "repository", "GitHub App"} {
		if strings.Contains(strings.ToLower(out.String()), strings.ToLower(repo)) {
			t.Errorf("the run asks about %s outside any checkout:\n%s", repo, out)
		}
	}
	if r.o.githubAppID != "" || r.o.onboardRepo != "" {
		t.Errorf("outside a checkout there is no repository to enrol: %q", r.o.onboardRepo)
	}
}

// bakeQuestion is the wizard's row for the switch (IS7), read where the run
// reads it: the sentence the plan pins, and the reason the run can take its
// answer itself.
var bakeQuestion = questionFor("--bake-repos")

// TestSetupAsksBakeDefaultYes is IS7's own test (docs/image-setup-skill/
// implementation-plan.md; D1901): a first run of `fugaro setup` asks whether a
// repository the build can read is baked into the image its job runs, the
// suggestion is yes, and the answer is the switch the run writes — which the
// same run's policy stage publishes, so a job reads it.
//
// The question is asked where a person can answer it and nowhere else, which is
// the owner's rule for a managed project and the reason the row's missing is
// empty: a run that cannot ask (--yes, --json, a pipe, --non-interactive) is
// already at this answer, because an absent key means yes — the suggestion the
// prompt offers. --bake-repos is that answer without a terminal, and the run
// that writes the switch says what it costs a reader: a rebuild.
func TestSetupAsksBakeDefaultYes(t *testing.T) {
	// The rig the last sub-test builds compiles the fake terraform with `go
	// build`, and the wizard runs above it move this test's working directory
	// out of the module: the one build every test of this package shares
	// (fakeTerraform's sync.Once) happens here, while this is still the module.
	fakeTerraform(t)
	const sentence = "Bake your repos into the Docker images? This makes jobs start faster."
	if bakeQuestion.label != sentence {
		t.Errorf("the wizard's bake question is %q, want the sentence the plan pins (%q)", bakeQuestion.label, sentence)
	}
	if bakeQuestion.missing != "" {
		t.Errorf("%s: the row names %q as a missing input, and this is the one question a run that cannot ask answers itself: an absent key means yes",
			bakeQuestion.flag, bakeQuestion.missing)
	}
	f := newSetupCmd().Flags().Lookup(strings.TrimPrefix(bakeQuestion.flag, "--"))
	if f == nil || f.Hidden {
		t.Fatalf("fugaro setup does not declare a visible %s, so a scripted run cannot answer the question it asks", bakeQuestion.flag)
	}

	// Every other question is answered, so the bake question is this run's only
	// one and every line read below belongs to it.
	only := func() *initOptions {
		return &initOptions{name: initProjectName, cloud: cloudOptions{gcpProject: initProject, region: defaultRegion}}
	}
	ask := func(stdin string) (*initRun, *strings.Builder) {
		t.Helper()
		r, out := wizardRun(t, stdin, only())
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return r, out
	}

	// Enter takes the suggestion: yes.
	r, out := ask("\n")
	if !r.o.bakeReposChanged || !r.o.bakeRepos {
		t.Errorf("Enter at the bake question recorded (answer %v, answered %v), want the suggestion: yes", r.o.bakeRepos, r.o.bakeReposChanged)
	}
	if want := bakeQuestion.label + " [yes]\nType " + bakeQuestion.label + ": "; !strings.Contains(out.String(), want) {
		t.Errorf("the prompt is not %q:\n%s", want, out)
	}

	// "no" is the answer a run has to write down: an absent key means yes, so a
	// no is the one spelling only the file can carry (localcfg.BakeRepos).
	r, _ = ask("no\n")
	if !r.o.bakeReposChanged || r.o.bakeRepos {
		t.Errorf("`no` at the bake question recorded (answer %v, answered %v), want no", r.o.bakeRepos, r.o.bakeReposChanged)
	}

	// An answer that is neither is asked again rather than taken for one.
	r, out = ask("maybe\nn\n")
	if !strings.Contains(out.String(), "answer yes or no") {
		t.Errorf("an unusable answer was taken instead of being asked again:\n%s", out)
	}
	if !r.o.bakeReposChanged || r.o.bakeRepos {
		t.Errorf("`maybe` then `n` recorded (answer %v, answered %v), want no", r.o.bakeRepos, r.o.bakeReposChanged)
	}

	// The input ending here stops the run at this question, exactly as it does
	// at the three above it: the suggestion is what an absent key means for a
	// run that cannot ask, and that is not the same thing as a person's input
	// ending — nothing was typed, so there is no answer to record, and what the
	// run says is which question it is waiting for. The row's empty missing is
	// about a run with no terminal at all, not about this one.
	r, out = wizardRun(t, "", only())
	err := r.gatherInputs(t.Context())
	if err == nil || !strings.Contains(err.Error(), bakeQuestion.label) || ExitCode(err) != ExitUserError {
		t.Errorf("the input ended at the bake question and the run reported %v, want the question named as the one it is waiting for:\n%s", err, out)
	}
	if r.o.bakeReposChanged {
		t.Errorf("the input ended at the bake question and the run recorded an answer (%v): nothing was typed", r.o.bakeRepos)
	}

	// Where nobody can be asked there is no question, and the answer the run
	// already has is the one it acts on.
	for _, tc := range []struct {
		name  string
		args  func(*initOptions)
		plain bool
	}{
		{"a run that carries its own answer", func(o *initOptions) { o.yes = true }, false},
		{"--json", func(o *initOptions) { o.asJSON = true }, false},
		{"a run that never prompts", func(o *initOptions) { o.nonInteractive = true }, false},
		{"--print-vars", func(o *initOptions) { o.printVars = true }, false},
		// The billing question refuses a pipe by naming its flag (T7), so the
		// run carries the answer and the bake question is the one left to see.
		{"a pipe", func(o *initOptions) { o.linkBilling = "0123AB-4567CD-89EF01" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := only()
			tc.args(o)
			r, out := wizardRun(t, "", o)
			if tc.plain {
				// No terminal on stdin: the same state a run that carries its
				// own answer is in, without a flag saying so.
				noTerminal(t)
			}
			if err := r.gatherInputs(t.Context()); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if strings.Contains(out.String(), bakeQuestion.label) {
				t.Errorf("%s: the run asked the bake question, and nobody can answer it there:\n%s", tc.name, out)
			}
			if r.o.bakeReposChanged {
				t.Errorf("%s: the run recorded an answer nobody gave", tc.name)
			}
		})
	}

	// And the answer reaches the local config the run writes, with the rebuild
	// it costs said out loud — that is what the switch is: the build's input,
	// not something the images that exist can be given afterwards.
	t.Run("the answer is what the run writes", func(t *testing.T) {
		r := newInitRig(t)
		out, _, err := executeStdin(t, "", "setup", "--bake-repos=false")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		lc, err := localcfg.Load(r.cfg)
		if err != nil {
			t.Fatalf("the local config the run wrote does not load: %v", err)
		}
		if lc.BakeRepos == nil || *lc.BakeRepos {
			t.Errorf("the local config carries bake_repos = %v after --bake-repos=false, want a written false", lc.BakeRepos)
		}
		if !strings.Contains(out, "fugaro settings set bake_repos") || !strings.Contains(out, "rebuilt") {
			t.Errorf("the run changed the bake switch and does not say what that costs — the answer is the build's, so the images that exist keep what they were built with:\n%s", out)
		}
	})
}

// convergeLocalConfigPath is the local project config a run of the converge rig
// reads and writes: the copy registerInstallation leaves in the projects
// directory, which is the file the run's own writeConfig replaces (the rig
// keeps FUGARO_CONFIG empty, so the run selects its installation by name).
func convergeLocalConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initProjectName+".yaml")
}

// convergeLocalConfig is that file, read by the loader a run of the project
// reads it with.
func convergeLocalConfig(t *testing.T) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(convergeLocalConfigPath(t))
	if err != nil {
		t.Fatalf("the local config the run wrote does not load: %v", err)
	}
	return lc
}

// TestSetupUpdateConvergesBake is the second test IS7 names, and it is the HARD
// RULE's (F15; CLAUDE.md): an installation from the release before this one —
// whose local config and whose published node both say nothing about baking,
// which is every installation there is, the key being new — reaches the switch
// with `fugaro update` alone, and what it reaches is the answer the wizard's own
// question suggests: yes (D1901).
//
// Two fixtures, one run each, because that is the whole journey of the key and
// because one checkout takes one converge: the anchor writes the repository's
// `gcp_project:` line into fugaro.yaml, so the next run in the same checkout
// finds a working tree that is not the default branch's and stops at the
// repository stage, as it should (init_repo_target.go).
//
//   - `fugaro update` on the installation that never answered the question:
//     nothing is asked (the door has no terminal here, and a run whose project
//     config exists — a managed project — is never asked), nothing is written,
//     and both readers of the switch already answer yes: the file says nothing
//     and the node says nothing, which is one statement with a written true to
//     every reader (localcfg.BakeReposEffective, projectpolicy.Policy.BakesRepos).
//     The upgrade owes no second command, which is the HARD RULE.
//   - `fugaro setup --bake-repos=false` — the other door, which converges what
//     exists rather than creating — writes the answer into the local config and
//     publishes it to the node in the same run. It is the scripted run's answer
//     to the question, and the only way a written false can exist: an absent key
//     means yes, so only a value can say no.
func TestSetupUpdateConvergesBake(t *testing.T) {
	t.Run("an installation that never answered the question", func(t *testing.T) {
		r := newConvergeRig(t, v6Node())
		// The fixture is an installation of the release before this one: neither
		// the file nor the node names the key, or none of this is about a switch
		// that did not exist.
		if strings.Contains(readFileString(t, convergeLocalConfigPath(t)), "bake_repos") {
			t.Fatal("the fixture's local config already states bake_repos: it is not an installation from before the key")
		}
		if p := convergeNode(t, r); p.BakeRepos != nil {
			t.Fatalf("the fixture's node already carries bake_repos = %v: it is not an installation from before the key", p.BakeRepos)
		}

		out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})
		if strings.Contains(out, bakeQuestion.label) {
			t.Errorf("the update door asked the bake question:\n%s", out)
		}
		if p := convergeNode(t, r); p.BakeRepos != nil || !p.BakesRepos() {
			t.Errorf("after one fugaro update the node's bake_repos = %v: an installation that never answered the question has to read as the wizard's suggestion (yes), or the upgrade owes a second command", p.BakeRepos)
		}
		if lc := convergeLocalConfig(t); lc.BakeRepos != nil {
			t.Errorf("the update wrote bake_repos = %v into a config that said nothing about it, want it left alone: a run that does not state the switch keeps the project's answer", *lc.BakeRepos)
		}
		if strings.Contains(out, "fugaro settings set bake_repos") {
			t.Errorf("the run never changed the bake switch and told the reader about a rebuild anyway:\n%s", out)
		}
	})

	t.Run("the answer a run gives the question", func(t *testing.T) {
		r := newConvergeRig(t, v6Node())
		fakeTerminal(t)
		out, errOut, err := executeStdin(t, "", "setup", "--bake-repos=false")
		if err != nil {
			t.Fatalf("fugaro setup --bake-repos=false stopped at %s (exit %d): %v\n%s\n%s", stoppedStage(out), ExitCode(err), err, out, errOut)
		}
		lc := convergeLocalConfig(t)
		if lc.BakeRepos == nil || *lc.BakeRepos {
			t.Errorf("after the run, the local config's bake_repos = %v, want the written false the flag names: an absent key and a written true are one statement, so a no is the only value the file has to carry", lc.BakeRepos)
		}
		if p := convergeNode(t, r); p.BakeRepos == nil || *p.BakeRepos {
			t.Errorf("after the run, the published node's bake_repos = %v, want the false the same run's local config states: the node is what a job reads", p.BakeRepos)
		}
		if !strings.Contains(out, "fugaro settings set bake_repos") {
			t.Errorf("the run changed the bake switch and does not say what that costs (a rebuild) or where a later change is made:\n%s", out)
		}
	})
}
