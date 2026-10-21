package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// setup_wizard_test.go pins the wizard task W1.3 of
// docs/setup-wizard/implementation-plan.md gives `fugaro setup`: its first
// question is the project, and the answer to it — create one, or adopt one that
// already exists — is what the project stage (init_project.go) acts on
// (reconciliation F24, D11, and D12's half of it: creation is never covered by
// --yes, on either answer).
//
// The question list itself is setup.go's `setupQuestions`, and
// TestEveryWizardQuestionHasAFlag is the contract on it: every question has a
// flag, so a scripted run never needs a terminal for anything but the typed
// confirmations. The rest of the file is what the two answers mean.

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
	out := &strings.Builder{}
	return &initRun{o: o, w: out, in: bufio.NewReader(strings.NewReader(stdin)), cmd: NewRootCmd()}, out
}

// wizardAnswers is the line that answers each of the wizard's questions, by
// the flag that answers the same question without a terminal: the default
// answer for the ones that have a suggestion, and a real value for the GCP
// project (there is none to suggest in this environment, so Enter is not an
// answer).
var wizardAnswers = map[string]string{
	"--create-project": "\n",
	"--gcp-project":    "proj-1234\n",
	"--region":         "\n",
	"--name":           "\n",
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
	o := &initOptions{}
	switch flag {
	case "--create-project":
		o.createProject = true
	case "--gcp-project":
		o.cloud.gcpProject = "proj-1234"
	case "--region":
		o.cloud.region = defaultRegion
	case "--name":
		o.name = "aurora"
	}
	return o
}

// TestEveryWizardQuestionHasAFlag is the plan's new red step, and it is the
// contract in one place: a question the wizard asks is a question a scripted
// run can answer, and the flag that answers it is on `fugaro setup`.
//
// It is checked against the wizard rather than trusted. Every row's label is a
// prompt the run actually prints, in the table's order, when nothing else
// answers it; it disappears when the row's own flag answers the same question;
// and its flag is one `fugaro setup` declares and shows. A question added
// without a row — or a row for a question nobody asks any more — fails here.
//
// The last column is the one --non-interactive prints, and it is read off the
// product too: a run with no terminal names every question it cannot answer,
// and a row whose answer a run that cannot ask simply takes names nothing.
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

	// Asked: the wizard prints the table's labels, and in its order.
	r, out := wizardRun(t, stdinFor(""), nil)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	at := 0
	for _, q := range setupQuestions {
		i := strings.Index(out.String()[at:], q.label)
		if i < 0 {
			t.Errorf("%s: the wizard did not ask %q, in the order the table lists it:\n%s", q.flag, q.label, out)
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
			if strings.Contains(out.String(), q.label) {
				t.Errorf("%s answered the question, and the wizard asked %q anyway:\n%s", q.flag, q.label, out)
			}
		})
	}

	// Cannot be asked: --non-interactive names exactly the rows whose answer
	// it needs, and no others.
	t.Run("--non-interactive", func(t *testing.T) {
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
		// One error, and the answer it did not need is the one that creates
		// nothing: no project may be made by a run nobody answered.
		if r.o.createProject {
			t.Error("a run with no terminal and no flag answered its first question with creation")
		}
	})
}

// TestSetupTakesNoAnswerNobodyGave: the question is not asked of a run that
// cannot be asked one, and the answer such a run takes is adoption — the one
// that creates nothing. Creation is the answer a person at a terminal gives,
// or the one a scripted run names with --create-project; never one a run takes
// by reading a pipe's stdin or by being handed --yes (D12: --yes covers
// everything the re-converge owns, and never creating the project).
//
// The stdin below says "create" on purpose: a run that reads it has answered a
// question nobody was asked.
func TestSetupTakesNoAnswerNobodyGave(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
		o        *initOptions
	}{
		{"no terminal", false, &initOptions{}},
		{"--yes at a terminal", true, &initOptions{yes: true}},
		{"--json at a terminal", true, &initOptions{asJSON: true}},
		{"--non-interactive at a terminal", true, &initOptions{nonInteractive: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			firstRunEnv(t)
			if tc.terminal {
				fakeTerminal(t)
			}
			out := &strings.Builder{}
			r := &initRun{o: tc.o, w: out, in: bufio.NewReader(strings.NewReader(setupAnswerCreate + "\n")), cmd: NewRootCmd()}
			if err := r.askProjectAnswer(); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if r.o.createProject {
				t.Errorf("the run took the answer %q with nobody to ask:\n%s", setupAnswerCreate, out)
			}
			if out.Len() != 0 {
				t.Errorf("the question was asked of a run that cannot be asked one:\n%s", out)
			}
		})
	}
}

// TestSetupAsksCreateOrAdoptWithCreateDefault: the wizard's first question is
// the project, and creation — `fugaro-<name>` (new) — is the answer Enter
// takes (D11, answered 2026-10-20 as recommended). What the answer is recorded
// as is --create-project's own field, so the person at the terminal and the
// scripted run answer one question, and the project stage is registered for
// either of them: it is the stage that acts on the answer, never the flag.
func TestSetupAsksCreateOrAdoptWithCreateDefault(t *testing.T) {
	r, out := wizardRun(t, stdinFor(""), nil)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), setupAskProject) {
		t.Errorf("the wizard does not ask whether to create the project:\n%s", out)
	}
	if !strings.Contains(out.String(), "["+setupAnswerCreate+"]") {
		t.Errorf("creation is not the default answer:\n%s", out)
	}
	if !strings.Contains(out.String(), setupAnswerAdopt) {
		t.Errorf("the question does not offer the second answer:\n%s", out)
	}
	if !r.o.createProject {
		t.Error("the default answer did not record creation")
	}

	// The stage set follows the answer: a first run in `setup` registers the
	// project stage whether or not --create-project was given (D11), so the
	// question has something to decide.
	first := initStages(&initEngine{r: &initRun{o: &initOptions{door: setupDoor.name}}, lc: &localcfg.Config{}})
	if _, ok := stageNamed(first, initflow.Project); !ok {
		t.Errorf("setup's first run does not register the project stage: %v", namesOf(first))
	}
	// And `update` never does: it creates nothing.
	up := initStages(&initEngine{r: &initRun{o: &initOptions{door: updateDoor.name}}, lc: &localcfg.Config{}})
	if _, ok := stageNamed(up, initflow.Project); ok {
		t.Errorf("update registers the project stage: %v", namesOf(up))
	}
}

// TestSetupAdoptsWhenTheUserTypesAnExistingID: the second answer is adoption,
// and it is the answer that reads nothing and creates nothing. The stage is
// registered (it is `setup` on a first run) and it has nothing to do: an
// adopted project that is not there is the installation's own error, and a
// project that is there is left exactly as it is.
func TestSetupAdoptsWhenTheUserTypesAnExistingID(t *testing.T) {
	r, out := wizardRun(t, "proj-1234\n"+"\n"+setupAnswerAdopt+"\n"+"\n", nil)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.createProject {
		t.Errorf("the answer %q did not record adoption:\n%s", setupAnswerAdopt, out)
	}
	if r.o.cloud.gcpProject != "proj-1234" {
		t.Errorf("gcp project %q, want the id the user typed", r.o.cloud.gcpProject)
	}

	// The stage, on an engine with no clients at all: adoption is decided from
	// the answer before anything is read, so this cannot reach a cloud (a read
	// would fail on the missing endpoints and say so here).
	e := &initEngine{r: &initRun{o: &initOptions{door: setupDoor.name}, w: &strings.Builder{}, in: bufio.NewReader(strings.NewReader(""))}, lc: &localcfg.Config{}}
	s := newProjectStage(e)
	st, err := s.Check(t.Context())
	if err != nil {
		t.Fatalf("the adopt answer read something: %v", err)
	}
	if st.State != initflow.Skipped {
		t.Errorf("state %q, want skipped: the answer was to adopt, so there is nothing to create", st.State)
	}
	if _, err := s.Apply(t.Context(), initflow.Env{Interactive: true}); err != nil {
		t.Errorf("the adopt answer refused a run that cannot type a confirmation: %v", err)
	}
	if s.started || s.create || s.addFirebase || s.link {
		t.Errorf("the adopt answer acted: started %v create %v firebase %v link %v", s.started, s.create, s.addFirebase, s.link)
	}
	if pl, err := s.Plan(t.Context(), initflow.Env{}); err != nil || !pl.NothingToDo {
		t.Errorf("plan %+v, %v: want nothing to do", pl, err)
	}
	if err := s.Verify(t.Context()); err != nil {
		t.Errorf("the adopt answer failed its own verify: %v", err)
	}
}

// TestSetupCreatesOnlyWithTheTypedConfirmation: the answer is not the
// confirmation. With it — --create-project here, which is the field the
// wizard's first question records, so this is the same answer — the project is
// created only by the ID typed at a real terminal, and --yes, --json, a pipe
// and --non-interactive each leave it for you (D12: creating the project is
// `setup`'s, and `--yes` never covers it). This is the row W0.1's matrix gains
// for the door that creates.
func TestSetupCreatesOnlyWithTheTypedConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
		stdin    string
		args     []string
		created  bool
	}{
		{"the ID typed at a terminal", true, newProj + "\n", nil, true},
		{"--yes does not stand in for it", true, "yes\n", []string{"--yes"}, false},
		{"--yes at a terminal, the ID typed", true, newProj + "\n", []string{"--yes"}, true},
		{"a pipe cannot type it", false, newProj + "\n", nil, false},
		{"--json cannot type it", true, newProj + "\n", []string{"--json"}, false},
		{"--non-interactive cannot type it", true, newProj + "\n", []string{"--non-interactive", "--yes"}, false},
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

// TestSetupOnAnExistingInstallationConvergesAndSaysSo: an installation is
// already there, so setup says so in one line, keeps it and converges as
// `fugaro update` does — it does not offer to create a second one, and it
// registers no project stage (D11). Both answers to "is it already there" are
// here: this machine's own project config, and an installation the runs
// bucket's marker names while this machine has no config for it yet.
func TestSetupOnAnExistingInstallationConvergesAndSaysSo(t *testing.T) {
	t.Run("a project config this machine has", func(t *testing.T) {
		newInitRig(t)
		out, _, err := executeStdin(t, "", "setup", "--yes")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if !strings.Contains(out, setupAlreadySetUp) {
			t.Errorf("the run does not say the installation is already set up:\n%s", out)
		}
		if strings.Contains(out, setupAskProject) {
			t.Errorf("the run offered to create a second installation:\n%s", out)
		}
		// The stage set is update's: no project stage at all.
		js, _, err := executeStdin(t, "", "setup", "--yes", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, js)
		}
		var res convergeJSON
		if err := json.Unmarshal([]byte(js), &res); err != nil {
			t.Fatalf("%v:\n%s", err, js)
		}
		if got := res.state(initflow.Project); got != "" {
			t.Errorf("the project stage ran on an installation that exists: %q\n%s", got, js)
		}
	})

	t.Run("an installation in the GCP project", func(t *testing.T) {
		old := installationName
		installationName = func(context.Context, string, string, string) string { return initProjectName }
		t.Cleanup(func() { installationName = old })
		r, out := wizardRun(t, "proj-1234\n"+"\n", nil)
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if !strings.Contains(out.String(), setupAlreadySetUp) {
			t.Errorf("the run does not say the installation is already set up:\n%s", out)
		}
		if strings.Contains(out.String(), setupAskProject) {
			t.Errorf("the run offered to create a second installation:\n%s", out)
		}
		if strings.Contains(out.String(), "Fugaro project name") {
			t.Errorf("the run asked for a name, and the installation has one:\n%s", out)
		}
		if r.o.name != initProjectName || r.o.createProject {
			t.Errorf("name %q, create %v: want the installation's own name and no creation", r.o.name, r.o.createProject)
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
		if !strings.Contains(out.String(), q.label) {
			t.Errorf("the wizard did not ask %q outside a checkout:\n%s", q.label, out)
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
