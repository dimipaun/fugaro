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
// So each test below asserts the SAME answer in the two worlds, and that is the
// assertion: a managed project's own terminal is asked for these steps exactly
// as a project that is not managed is, and what a reading of the marks may
// never do is answer a money step. The mutation it catches is the tempting one
// — an arm in askClass or askCovered that returns "confirmed" when
// gateUnderway() is true — which would pass every other test in this package
// and fail these three.

// managedTerminalUpdate is one update door's run on a rig, in the session the
// ruling is about: a real terminal, an empty stdin, no --yes, and no coding
// agent's marker. managed is the world the rig is in (both marks, or neither),
// and the gate is read where gateRun reads it — once, beside the loop — so a
// test asserts about the run the command actually builds.
func managedTerminalUpdate(t *testing.T, managed bool) (*initRig, *initEngine, *syncBuf) {
	t.Helper()
	r := updateRig(t)
	if managed {
		r.managedRuns(t, initProjectName, initProject)
	}
	isolateGateCredentials(t)
	noAgentSession(t)
	e := rigEngine(t, r, &initOptions{door: updateDoor.name})
	out := atTerminal(t, e, "")
	e.gateRun(t.Context())
	if got := e.r.gateUnderway(); got != managed {
		t.Fatalf("the gate read answered managed=%v, want %v", got, managed)
	}
	return r, e, out
}

// moneyWorlds is the two worlds each test below runs in. It is not a
// "managed or not" pair with two different answers: both want the prompt.
func moneyWorlds() []struct {
	what    string
	managed bool
} {
	return []struct {
		what    string
		managed bool
	}{
		{"managed: money keeps the owner's own word", true},
		{"not managed: unchanged", false},
	}
}

// assertAsked is the one assertion all three tests make: the step's own typed
// confirmation is asked, nothing was confirmed with an empty stdin, and the
// gate's sentence — which is what a step it stood in for would have printed
// instead — is nowhere in the output.
func assertAsked(t *testing.T, out *syncBuf, reachable, confirmed bool) {
	t.Helper()
	if !reachable {
		t.Fatalf("the step was not reached for a confirmation at all:\n%s", out)
	}
	if confirmed {
		t.Fatalf("the step was confirmed with nothing typed:\n%s", out)
	}
	if !strings.Contains(out.String(), "Type "+initProjectName+" to confirm: ") {
		t.Errorf("the terminal was not asked for the project's name:\n%s", out)
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
// the Cloud Build the step submits — so the typed name stays.
func TestManagedBillableFirstBuildKeepsItsTypedName(t *testing.T) {
	for _, tc := range moneyWorlds() {
		t.Run(tc.what, func(t *testing.T) {
			_, e, out := managedTerminalUpdate(t, tc.managed)
			banner := cloudBuildBanner("acme/app", "app", "E2_HIGHCPU_8", "builder@acme.iam.gserviceaccount.com", "us-docker.pkg.dev/acme/images")
			confirmed, reachable, err := e.r.askCovered(banner)
			if err != nil {
				t.Fatal(err)
			}
			assertAsked(t, out, reachable, confirmed)
		})
	}
}

// The Firestore database's location: the one step of the firebase stage that is
// confirmed on its own (init_firebase.go's stepConfirmation(r.stepClass())),
// and permanent in the way a plan cannot judge — a database's location cannot
// be changed afterwards. It is YesOrTyped in `update` and Typed in `setup`
// (stepClass), and it keeps the typed name in both.
func TestManagedFirestoreLocationKeepsItsTypedName(t *testing.T) {
	for _, tc := range moneyWorlds() {
		t.Run(tc.what, func(t *testing.T) {
			_, e, out := managedTerminalUpdate(t, tc.managed)
			confirmed, reachable, err := e.r.askClass(e.r.stepClass(),
				"creates the Firestore database in northamerica-northeast1 for the budget backend")
			if err != nil {
				t.Fatal(err)
			}
			assertAsked(t, out, reachable, confirmed)
		})
	}
}

// A registry tag replacement is the third, and it is structural rather than
// classified: --replace-image is one of the flags that shape the installation
// (initOptions.installationFlags, asked by shapesTheInstallation), so a run
// that carries it is refused the gate before any mark is read — gateRun returns
// without a read, gateUnderway() is false, and askCovered (confirmReplace's own
// class) asks the typed name. That is what makes the answer the same in a
// managed project, and the test asserts the reason and not only the outcome:
// the gate is nil, so no arm anywhere could have answered for this step.
//
// The mutation it catches is a shapesTheInstallation that forgot
// --replace-image, or a gate read for a shaped run: either would put a mark
// read behind a step that changes what the installation IS.
func TestManagedReplaceImageRunNeverOpensTheGate(t *testing.T) {
	r := updateRig(t)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	noAgentSession(t)
	e := rigEngine(t, r, &initOptions{door: updateDoor.name, replaceImages: []string{"app"}})
	out := atTerminal(t, e, "")
	e.gateRun(t.Context())
	if e.r.gate != nil {
		t.Fatalf("a run that shapes the installation read the managed marks:\n%s", out)
	}
	confirmed, reachable, err := e.r.askCovered("REPLACES the app tag in your registry: whatever uses it then runs the new image")
	if err != nil {
		t.Fatal(err)
	}
	assertAsked(t, out, reachable, confirmed)
}
