package cli

import (
	"strings"
	"testing"
)

// The money and permanence steps of the managed set, pinned as tests: the
// steps the ruling of 2026-11-01 takes the prompt from are the ones the gate's
// own verdict covers and the ordinary steps the gate does not judge, and these
// are the ones left out of both — a billable first image build, the Firestore
// database's own location, and a registry tag replacement. What decides them is
// D1026's class rule, and it is the design's rule rather than this build's
// choice: `Covers` is true only for YesOrTyped, and a step that is money or
// permanent is one a plan verdict cannot stand in for. The design's own
// billable path says the same thing from the other end — `fugaro image
// refresh` stops asking a session's per-build question only because D1013 gave
// that path a document and a bound, so a prompt is only ever replaced by
// something that bounds what the prompt was there for.
//
// What answers for them is the session and no longer a name typed under the
// banner (T7, docs/no-prompt-policy): a person at their own terminal confirms
// a step of this kind by being there and types nothing at all, --yes confirms
// the two whose class takes it, and where nobody is at the terminal — a pipe,
// --json, --non-interactive, a coding agent's session — the step is left for
// the user. So each test below asserts the SAME answer in the two worlds, and
// that is the assertion: a managed project's own terminal is answered for these
// steps exactly as a project that is not managed is, and what a reading of the
// marks may never do is answer a money step. The mutation it catches is the
// tempting one — an arm in askClass or askCovered that returns "confirmed" when
// gateUnderway() is true — which the world with nobody at the terminal fails,
// and which would pass every other test in this package.

// managedTerminalUpdate is one update door's run on a rig, in one of the two
// sessions a step of this kind ends in: a person at a real terminal, who
// answers it by being there, and a pipe, where nothing answers it and the step
// is left. Either way stdin is empty, no --yes and no --ask are given, and no
// coding agent's marker is set; the spy is on the run's own input and says
// whether anything read it. managed is the world the rig is in (both marks, or
// neither), and the gate is read where gateRun reads it — once, beside the loop
// — so a test asserts about the run the command actually builds.
func managedTerminalUpdate(t *testing.T, managed, terminal bool) (*initRig, *initEngine, *syncBuf, *readSpy) {
	t.Helper()
	r := updateRig(t)
	if managed {
		r.managedRuns(t, initProjectName, initProject)
	}
	isolateGateCredentials(t)
	noAgentSession(t)
	e := rigEngine(t, r, &initOptions{door: updateDoor.name})
	var out *syncBuf
	if terminal {
		out = atTerminal(t, e, "")
	} else {
		noTerminal(t)
		out = atNoTerminal(e)
	}
	spy := &readSpy{Reader: strings.NewReader("")}
	e.r.cmd.SetIn(spy)
	e.gateRun(t.Context())
	if got := e.r.gateUnderway(); got != managed {
		t.Fatalf("the gate read answered managed=%v, want %v", got, managed)
	}
	return r, e, out, spy
}

// moneyWorlds is the worlds each test below runs in: the two the ruling tells
// apart (managed or not) crossed with the two sessions a money step ends
// differently in. They are not a "managed or not" pair with two different
// answers: both are answered the same way in both sessions.
func moneyWorlds() []struct {
	what     string
	managed  bool
	terminal bool
} {
	return []struct {
		what     string
		managed  bool
		terminal bool
	}{
		{"managed, a person at the terminal: the owner's own word", true, true},
		{"managed, nobody at the terminal: the step is left", true, false},
		{"not managed, a person at the terminal: unchanged", false, true},
		{"not managed, nobody at the terminal: unchanged", false, false},
	}
}

// assertMoneyStep is the one assertion all three tests make, in both sessions:
// want says what this world's answer is, and it is the same one in the managed
// project and in the one that is not managed. A person at their own terminal
// confirms the step with nothing typed and nothing read (T7: the name is gone);
// with nobody there the step is left, reachable and confirmed both false, which
// is what fails an arm that let the gate's mark read answer a money step. The
// gate's sentence — what a step it stood in for would have printed instead — is
// nowhere in the output either way, and so is any question: the name typed at
// the terminal and the [y/N] are both absent, because --ask is the flag that
// puts a question back and these runs are not given it.
func assertMoneyStep(t *testing.T, out *syncBuf, spy *readSpy, reachable, confirmed, want bool) {
	t.Helper()
	if want && (!reachable || !confirmed) {
		t.Fatalf("the step was left at the person's own terminal (reachable=%v confirmed=%v):\n%s", reachable, confirmed, out)
	}
	if !want && (reachable || confirmed) {
		t.Fatalf("the step was answered with nobody at the terminal (reachable=%v confirmed=%v):\n%s", reachable, confirmed, out)
	}
	if spy.read {
		t.Errorf("the run read its own input: nothing is typed at a confirmation any more:\n%s", out)
	}
	if strings.Contains(out.String(), "Type "+initProjectName+" to confirm: ") {
		t.Errorf("the terminal was still asked for the project's name:\n%s", out)
	}
	if strings.Contains(out.String(), "[y/N]") {
		t.Errorf("a run without --ask asked a question it has no reader for:\n%s", out)
	}
	if strings.Contains(out.String(), "managed project") {
		t.Errorf("a money step read as one the gate answered:\n%s", out)
	}
}

// The first billable image build of a repository: buildImages' own step
// (cloudBuildBanner + askCovered), reached by the repository stage of an
// update. The class is YesOrTyped in this door (D12's answer of 2026-10-20,
// which is what lets --yes cover the build), and the gate judges no document
// that names it — the repository stage's document is the Terraform plan, not
// the Cloud Build the step submits — so the person at the terminal is the one
// who answers it, and a session with nobody there leaves it.
func TestManagedBillableFirstBuildKeepsTheOwnersTerminal(t *testing.T) {
	for _, tc := range moneyWorlds() {
		t.Run(tc.what, func(t *testing.T) {
			_, e, out, spy := managedTerminalUpdate(t, tc.managed, tc.terminal)
			banner := cloudBuildBanner("acme/app", "app", "E2_HIGHCPU_8", "builder@acme.iam.gserviceaccount.com", "us-docker.pkg.dev/acme/images")
			confirmed, reachable, err := e.r.askCovered(banner)
			if err != nil {
				t.Fatal(err)
			}
			assertMoneyStep(t, out, spy, reachable, confirmed, tc.terminal)
		})
	}
}

// The Firestore database's location: the one step of the firebase stage that is
// confirmed on its own (init_firebase.go's stepConfirmation(r.stepClass())),
// and permanent in the way a plan cannot judge — a database's location cannot
// be changed afterwards. It is YesOrTyped in `update` and Typed in `setup`
// (stepClass), and the person at the terminal answers it in both; which class
// the door gives it is what lets --yes reach it in `update` and never in
// `setup`.
func TestManagedFirestoreLocationKeepsTheOwnersTerminal(t *testing.T) {
	for _, tc := range moneyWorlds() {
		t.Run(tc.what, func(t *testing.T) {
			_, e, out, spy := managedTerminalUpdate(t, tc.managed, tc.terminal)
			confirmed, reachable, err := e.r.askClass(e.r.stepClass(),
				"creates the Firestore database in northamerica-northeast1 for the budget backend")
			if err != nil {
				t.Fatal(err)
			}
			assertMoneyStep(t, out, spy, reachable, confirmed, tc.terminal)
		})
	}
}

// A registry tag replacement is the third, and it is structural rather than
// classified: --replace-image is one of the flags that shape the installation
// (initOptions.installationFlags, asked by shapesTheInstallation), so a run
// that carries it is refused the gate before any mark is read — gateRun returns
// without a read, gateUnderway() is false, and askCovered (confirmReplace's own
// class) reaches the session in front of the banner with no verdict of any kind
// beside it. That is what makes the answer the same in a managed project, and
// the test asserts the reason and not only the outcome: the gate is nil, so
// nothing anywhere could have answered for this step but the person.
//
// The mutation it catches is a shapesTheInstallation that forgot
// --replace-image, or a gate read for a shaped run: either would put a mark
// read behind a step that changes what the installation IS.
func TestManagedReplaceImageRunNeverOpensTheGate(t *testing.T) {
	for _, tc := range []struct {
		what     string
		terminal bool
	}{
		{"a person at the terminal: the run's own step", true},
		{"nobody at the terminal: the step is left", false},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := updateRig(t)
			r.managedRuns(t, initProjectName, initProject)
			isolateGateCredentials(t)
			noAgentSession(t)
			e := rigEngine(t, r, &initOptions{door: updateDoor.name, replaceImages: []string{"app"}})
			var out *syncBuf
			if tc.terminal {
				out = atTerminal(t, e, "")
			} else {
				noTerminal(t)
				out = atNoTerminal(e)
			}
			spy := &readSpy{Reader: strings.NewReader("")}
			e.r.cmd.SetIn(spy)
			e.gateRun(t.Context())
			if e.r.gate != nil {
				t.Fatalf("a run that shapes the installation read the managed marks:\n%s", out)
			}
			confirmed, reachable, err := e.r.askCovered("REPLACES the app tag in your registry: whatever uses it then runs the new image")
			if err != nil {
				t.Fatal(err)
			}
			assertMoneyStep(t, out, spy, reachable, confirmed, tc.terminal)
		})
	}
}
