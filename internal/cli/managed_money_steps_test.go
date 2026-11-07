package cli

import (
	"bufio"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The money and permanence steps of the managed set, pinned as tests: the
// steps the ruling of 2026-11-01 takes the prompt from are the ones the gate's
// own verdict covers and the ordinary steps the gate does not judge, and two of
// the three below are the ones left out of both — the Firestore database's own
// location and a registry tag replacement. What decides them is D1026's class
// rule, and it is the design's rule rather than this build's choice: `Covers`
// is true only for YesOrTyped, and a step that is PERMANENT is one a plan
// verdict cannot stand in for.
//
// The first step — a billable first image build — is not one of them, and it is
// the test that says so (NP3, the owner's ruling of 2026-11-06). A build is
// money and neither data nor a secret, which is the whole of the distinction
// this file exists for: a step that spends is one the gate's read covers, and a
// step that cannot be undone is not. So the build's test asserts what tells the
// two worlds apart — nothing typed and the build submitted where the marks say
// the project is managed, the typed name submitted where they do not, and
// nothing submitted without it — while the two tests after it assert one answer
// in both worlds: the owner's own word, exactly as a project that is not
// managed is asked for it.
//
// Two mutations are what this file catches: an arm in askClass or askCovered
// that returns "confirmed" when gateUnderway() is true (which would answer the
// Firestore location and the tag replacement, and would pass every other test in
// this package), and a buildImages that stopped asking the unmanaged world for
// the name after reading marks it did not find.

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
// (cloudBuildBanner + its confirmation), reached by the repository stage of an
// update. The class is YesOrTyped in this door (D12's answer of 2026-10-20,
// which is what lets --yes cover the build), and a build is money and nothing
// else — no data is destroyed and no secret is read — so in a managed project
// the run's own read of the marks stands in for the name and the plan the run
// printed is what the owner was shown (buildAsksNothing, and the same treatment
// `fugaro image build` gives the same person there).
//
// The three worlds are the whole assertion. Where the marks say the project is
// managed the person's stdin is a reader that fails the test when it is read at
// all, so "nothing was asked" is a property of the run and not a reading of its
// output; the gate's own sentence is what they read instead, and the build is
// submitted. Where they do not say it, the typed name is what submits the build
// and nothing typed submits nothing — the direction a mistake has to fall in.
func TestManagedBillableFirstBuildTakesNoNameAtTheTerminal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		managed bool
		stdin   string
		builds  int
	}{
		{"managed: the marks and the printed plan stand in for the name", true, "", 1},
		{"not managed: the typed name submits the build", false, initProjectName + "\n", 1},
		{"not managed: nothing typed submits nothing", false, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb := useFakeBuilder(t)
			_, e, out := managedTerminalUpdate(t, tc.managed)
			// The person's own stdin, as the confirmation would read it: a
			// reader that fails the test if anything reaches it in the managed
			// world, and the typed line in the world that has to be asked.
			stdin := &unreadStdin{}
			if tc.stdin == "" {
				e.r.in = bufio.NewReader(stdin)
				e.r.cmd.SetIn(stdin)
			} else {
				e.r.in = bufio.NewReader(strings.NewReader(tc.stdin))
				e.r.cmd.SetIn(strings.NewReader(tc.stdin))
			}
			lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5",
				RunsBucket: initRunsBucket,
				BaseImages: map[string]string{"base": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:1.2.3"}}
			e.r.setProject(lc)
			cfg := &config.Config{Workflows: map[string]config.Workflow{"app": {}}}
			spec := infra.RepoSpec{Name: "acme/app", Slug: "acme-app", BuildServiceAccountEmail: "b@x.iam", RegistryPath: "r",
				Workflows: map[string]infra.WorkflowSpec{"app": {}}}
			what := cloudBuildBanner("acme/app", "app", lc.Build.MachineType, spec.BuildServiceAccountEmail, spec.RegistryPath)

			built, err := e.r.buildImages(t.Context(), lc, cfg, spec, []string{"app"})
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if built != tc.builds || len(fb.specs) != tc.builds {
				t.Fatalf("built %d (%d submitted), want %d\n%s", built, len(fb.specs), tc.builds, out)
			}
			if !strings.Contains(out.String(), what) {
				t.Errorf("the billable plan the step is about was not printed:\n%s", out)
			}
			if tc.managed {
				if stdin.read {
					t.Errorf("a managed project's own terminal was read, so the name was asked for:\n%s", out)
				}
				if !strings.Contains(out.String(), initflow.GateOrdinary()) {
					t.Errorf("the person did not read why nothing is typed:\n%s", out)
				}
				if strings.Contains(out.String(), "Type "+initProjectName+" to confirm: ") {
					t.Errorf("a managed project's own terminal was asked for the project's name:\n%s", out)
				}
				return
			}
			if !strings.Contains(out.String(), "Type "+initProjectName+" to confirm: ") {
				t.Errorf("a project that is not managed did not read the prompt this step has always printed:\n%s", out)
			}
			if strings.Contains(out.String(), initflow.GateOrdinary()) {
				t.Errorf("a step in a project that is not managed read the gate's own sentence:\n%s", out)
			}
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
