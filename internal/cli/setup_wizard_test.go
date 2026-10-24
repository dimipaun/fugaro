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
// docs/setup-wizard/implementation-plan.md gives `fugaro setup`, as the owner's
// ruling of 2026-10-23 reshaped it: Fugaro only starts fresh, so there is no
// question about the project — a first run creates it, the project stage
// (init_project.go) refuses an ID that already exists, and the creation is
// never covered by --yes (D12).
//
// The question list itself is setup.go's `setupQuestions`, and
// TestEveryWizardQuestionHasAFlag is the contract on it: every question has a
// flag, so a scripted run never needs a terminal for anything but the typed
// confirmations.

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
// and every row is one it cannot answer for itself.
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
// creating too (its inputs are flags); what it cannot do is type the creation's
// confirmation, which TestSetupCreatesOnlyWithTheTypedConfirmation pins.
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

// TestSetupCreatesOnlyWithTheTypedConfirmation: the creation is not its own
// confirmation. A first run's project is created only by the ID typed at a real
// terminal, and --yes, --json, a pipe and --non-interactive each leave it for
// you (D12: creating the project is `setup`'s, and `--yes` never covers it).
// This is the row W0.1's matrix gains for the door that creates.
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

// TestSetupOnAnExistingInstallationConvergesAndSaysSo: this machine's own
// project config means the installation is there, so setup says so in one line,
// keeps it and converges as `fugaro update` does — a re-run, not a creation —
// and registers no project stage (D11).
func TestSetupOnAnExistingInstallationConvergesAndSaysSo(t *testing.T) {
	newInitRig(t)
	out, _, err := executeStdin(t, "", "setup", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, setupAlreadySetUp) {
		t.Errorf("the run does not say the installation is already set up:\n%s", out)
	}
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
			if strings.Contains(out.String(), "Fugaro project name") {
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
		if strings.Contains(out.String(), "Fugaro project name") {
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
