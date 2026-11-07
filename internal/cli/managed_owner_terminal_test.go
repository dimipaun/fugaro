package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// managed_owner_terminal_test.go is the owner's ruling of 2026-11-06: a person
// at a real terminal on a managed project is never asked to type a
// confirmation, except for data loss and secret values.
//
// Each test below is one of the ruling's cases, and each is run in the worlds
// that have to be told apart — the same run with a coding agent's marker in the
// environment, the same run on a project that is NOT managed, and the same run
// with nobody at the terminal at all (stdin a pipe, managed marks and no
// --yes) — because the exception is about WHO is at the terminal and never
// about what the gate admitted. The mutations the file exists to catch are the
// tempting ones: reading "a terminal" as "the owner" (a coding agent's session
// has a terminal too, and the marker read in PersonAtTheTerminal is the whole
// of what keeps an agent out of these writes), reading the exception as one
// about the project rather than about the session (a project that is not
// managed keeps every prompt it had), and dropping the terminal itself out of
// the predicate — a pipe carries no marker, so a run nobody is at reads exactly
// like the owner's own, and an arm that asked only the marker would write
// there.
//
// The rigs are managed_money_steps_test.go's managedTerminalUpdate (the gate
// read once, beside the loop, in the owner's own session), its no-terminal
// sibling below, and updateRig's own helpers; the data-loss case is asserted
// through the very call the --forget rollback makes, because what the ruling
// leaves asked is that call and not a step's.

// managedUpdateNoTerminal is managedTerminalUpdate's rig in the world a person
// is not in: the same managed marks, the same one read beside the loop, and
// stdin a pipe instead of a fake terminal, so the gate stands in for the run
// and PersonAtTheTerminal is false for the half of it that is not about the
// marker. No --yes is passed: a flag is what a run nobody is at must not be
// able to lean on, and this rig is what says so.
func managedUpdateNoTerminal(t *testing.T) (*initRig, *initEngine, *syncBuf) {
	t.Helper()
	r := updateRig(t)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	noAgentSession(t)
	e := rigEngine(t, r, &initOptions{door: updateDoor.name})
	out := atNoTerminal(e)
	e.gateRun(t.Context())
	if !e.r.gateStandsIn() {
		t.Fatalf("the rig's gate did not stand in for the run:\n%s", out)
	}
	return r, e, out
}

// TestManagedOwnerTerminalOrdinaryStepRefusesWithNobodyAtTheTerminal is the
// other half of the owner's own sentence, and the half a reader of it gets
// wrong: the ruling is about a PERSON at the terminal, so a managed run whose
// stdin is a pipe is asked for the name exactly as before — the gate's read
// says who may act, and this run has nobody there to act. Both steps are
// asserted, because the arm is drawn inside the covered branch and the two
// arrive there differently: the covered one is the step the arm would otherwise
// answer for with nothing typed, and the one outside the review keeps the note
// the arm would have carried beside its sentence, with the prompt instead.
func TestManagedOwnerTerminalOrdinaryStepRefusesWithNobodyAtTheTerminal(t *testing.T) {
	for _, tc := range []struct {
		name       string
		what       string
		notCovered string
	}{
		{"covered", "writes the Terraform state bucket (cents a month)", ""},
		{"outside the review", "applies the plan as shown", "the plan is outside what the review announced (a destroy of one resource)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e, out := managedUpdateNoTerminal(t)
			e.r.review = &runReview{state: reviewTaken}

			ok, err := e.r.askOrdinary(tc.what, tc.notCovered)
			if err != nil {
				t.Fatalf("a run nobody is at was refused with an error rather than asked: %v\n%s", err, out)
			}
			if ok {
				t.Fatalf("a step of a managed run nobody is at proceeded with nothing typed:\n%s", out)
			}
			// A pipe cannot type, so what the step's own confirmation is here
			// is its banner: ask prints it and answers false for a run with
			// nobody at the terminal (init.go's ask).
			if !strings.Contains(out.String(), "⚠ CONFIRM (project "+initProjectName+", GCP project "+initProject+"): "+tc.what) {
				t.Errorf("a run nobody is at was not asked for the step:\n%s", out)
			}
			if tc.notCovered != "" && !strings.Contains(out.String(), tc.notCovered+"; it asks for its own") {
				t.Errorf("the step's own confirmation was not the one asked:\n%s", out)
			}
			if strings.Contains(out.String(), initflow.OwnerProceeded()) {
				t.Errorf("a run nobody is at read the sentence written for the owner's own terminal:\n%s", out)
			}
			if strings.Contains(out.String(), "confirmed by --yes") {
				t.Errorf("a run nobody is at was told a flag it did not pass confirmed the step:\n%s", out)
			}
		})
	}
}

// TestManagedOwnerTerminalOrdinaryStepTypesNothing: the ordinary steps of the
// converge — the state bucket, the Viewer-read removal — are the ones the run's
// one confirmation used to cover, and on a managed project the owner types
// nothing for them either. What is printed is the step's own banner and
// OwnerProceeded's sentence, which claims no verdict because no rule judged
// these steps.
//
// The assertion about the question is the empty stdin: the rig's terminal has
// nothing to read, so a step that asked would read no answer and report false,
// and `ok` being true is the step having proceeded with nothing typed.
func TestManagedOwnerTerminalOrdinaryStepTypesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		what string
	}{
		{"covered", "writes the Terraform state bucket (cents a month)"},
		{"its own", "removes project Viewers' read access to the runs bucket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e, out := managedTerminalUpdate(t, true)

			ok, err := e.r.askOrdinary(tc.what, "")
			if err != nil {
				t.Fatalf("a managed project's own terminal was refused: %v\n%s", err, out)
			}
			if !ok {
				t.Fatalf("the step was not confirmed with nothing typed:\n%s", out)
			}
			if !strings.Contains(out.String(), initflow.OwnerProceeded()) {
				t.Errorf("the step does not say why nothing is typed:\n%s", out)
			}
			for _, no := range []string{"Type " + initProjectName, "confirmed by --yes"} {
				if strings.Contains(out.String(), no) {
					t.Errorf("a managed project's own terminal read %q:\n%s", no, out)
				}
			}
		})
	}
}

// TestManagedOwnerTerminalPlanOutsideTheReviewStillAsks is the boundary of the
// sentence above, and the one a reader of the ruling gets wrong: what is not
// asked is a step the run's one confirmation would have covered. A plan the
// review did not announce is the plan that may delete, replace or remove an IAM
// grant — tf.Cover's own refusal, the same cover the managed gate judges the
// step by — so the owner's terminal is asked for the project's name there
// exactly as a project that is not managed is, note and all. The mutation it
// catches is an owner arm that answers for any step at all, which is the one
// direction this ruling must not move: a refused or unannounced plan applying
// itself with nothing typed.
func TestManagedOwnerTerminalPlanOutsideTheReviewStillAsks(t *testing.T) {
	const notCovered = "the plan is outside what the review announced (a destroy of one resource)"
	for _, tc := range []struct {
		name    string
		managed bool
	}{
		{"managed", true},
		{"not managed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e, out := managedTerminalUpdate(t, tc.managed)
			// The note is printed only for a step of a run whose review had
			// already been taken (init_review.go's askOrdinary), which is the
			// state this rig's run is in after its review.
			e.r.review = &runReview{state: reviewTaken}

			ok, err := e.r.askOrdinary("applies the plan as shown", notCovered)
			if err != nil {
				t.Fatalf("the step was refused rather than asked: %v\n%s", err, out)
			}
			if ok {
				t.Fatalf("a plan the review did not cover proceeded with nothing typed:\n%s", out)
			}
			if !strings.Contains(out.String(), notCovered+"; it asks for its own") {
				t.Errorf("the step does not say what the run's confirmation did not cover:\n%s", out)
			}
			if !strings.Contains(out.String(), "Type "+initProjectName) {
				t.Errorf("the step was not asked for the project's name:\n%s", out)
			}
			if strings.Contains(out.String(), initflow.OwnerProceeded()) {
				t.Errorf("a plan the review did not cover read the sentence written for the steps it did:\n%s", out)
			}
		})
	}
}

// TestManagedOwnerTerminalOrdinaryStepRefusesAnAgentSession is the other half
// of the same case, and it is the mutation a "person at a terminal" read as
// "a terminal" would make: a coding agent's session is at a terminal too, and
// the marker in its environment is what keeps it out. The refusal comes before
// anything is printed — a session told only that a step proceeded learns
// nothing about why it did not.
func TestManagedOwnerTerminalOrdinaryStepRefusesAnAgentSession(t *testing.T) {
	_, e, out := managedTerminalUpdate(t, true)
	t.Setenv(agentMarkers[0], "1")

	ok, err := e.r.askOrdinary("writes the Terraform state bucket (cents a month)", "")
	var refused *initflow.AgentError
	if ok || !errors.As(err, &refused) {
		t.Fatalf("a coding agent's session on a managed project got (%v, %v), want the agent's own refusal:\n%s", ok, err, out)
	}
	if strings.Contains(out.String(), initflow.OwnerProceeded()) {
		t.Errorf("a coding agent's session read the owner's own sentence:\n%s", out)
	}
	if strings.Contains(out.String(), "⚠ CONFIRM") {
		t.Errorf("a coding agent's session was shown the step's banner before its refusal:\n%s", out)
	}
}

// TestManagedOwnerTerminalUnmanagedStillAsks: the exception is the managed
// gate's and never the terminal's, so a project whose marks did not answer
// keeps the typed name exactly as it was — the same empty terminal, the same
// step, and the question asked.
func TestManagedOwnerTerminalUnmanagedStillAsks(t *testing.T) {
	_, e, out := managedTerminalUpdate(t, false)

	ok, err := e.r.askOrdinary("writes the Terraform state bucket (cents a month)", "")
	if err != nil {
		t.Fatalf("a project that is not managed was refused rather than asked: %v\n%s", err, out)
	}
	if ok {
		t.Fatalf("a project that is not managed proceeded with nothing typed:\n%s", out)
	}
	if !strings.Contains(out.String(), "Type "+initProjectName+" to apply to GCP project ") {
		t.Errorf("a project that is not managed was not asked for the project's name:\n%s", out)
	}
	if strings.Contains(out.String(), initflow.OwnerProceeded()) {
		t.Errorf("a project that is not managed read the managed sentence:\n%s", out)
	}
}

// TestManagedOwnerTerminalInstallationFlagKeepsTheTypedName is the boundary
// every arm in this file is drawn at, and it is the one a reader of the ruling
// gets wrong: the exception is decided under the gate's own READ, so a run
// whose own arguments shape the installation has nothing to act on. Such a run
// is refused the gate before any mark is read (shapesTheInstallation), gateRun
// returns without one, r.gate stays nil and gateStandsIn() is false — so an
// ordinary step of that run keeps the typed name it always had, on a managed
// project exactly as on any other.
//
// The flags are what the live checks use: `fugaro update --firebase-project`
// is the command whose database step check 82 tells the operator to type the
// name for, and it is an installation flag, so the step is asked there and the
// operator reading the check is told the truth. The rig asserts the reason and
// not only the outcome — the gate is nil, so no arm anywhere could have
// answered for this step — and the control is the same step on the same rig
// without the flag, which is TestManagedOwnerTerminalOrdinaryStepTypesNothing.
//
// The mutation it catches is an owner arm drawn at the door rather than at the
// read — `initflow.PersonAtTheTerminal(r.conditions())` alone, or beside a
// condition a shaped run satisfies: it would take a step of a run the gate
// never judged, which is the one direction a shaped run's confirmations must
// not move.
func TestManagedOwnerTerminalInstallationFlagKeepsTheTypedName(t *testing.T) {
	r := updateRig(t)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	noAgentSession(t)
	e := rigEngine(t, r, &initOptions{door: updateDoor.name, firebase: "proj-1234"})
	out := atTerminal(t, e, "")
	e.gateRun(t.Context())
	if e.r.gateable() {
		t.Fatalf("the rig's run is gateable, so it is not the case this test is about:\n%s", out)
	}
	if e.r.gate != nil {
		t.Fatalf("a run that shapes the installation read the managed marks:\n%s", out)
	}
	if e.r.gateStandsIn() {
		t.Fatalf("the gate stands in for a run that never read it:\n%s", out)
	}

	ok, err := e.r.askOrdinary("writes the database as listed, and then the local config", "")
	if err != nil {
		t.Fatalf("the step was refused rather than asked: %v\n%s", err, out)
	}
	if ok {
		t.Fatalf("a shaped run's step proceeded with nothing typed:\n%s", out)
	}
	if !strings.Contains(out.String(), "Type "+initProjectName+" to apply to GCP project ") {
		t.Errorf("the step was not asked for the project's name:\n%s", out)
	}
	if strings.Contains(out.String(), initflow.OwnerProceeded()) {
		t.Errorf("a run that read no mark printed the managed sentence:\n%s", out)
	}
}

// TestManagedOwnerTerminalWithYesReadsNoFlag is the ruling's third item: the
// banner on a plain managed update names no flag, because the run needed none.
// The same step with --yes reads the managed sentence and not "confirmed by
// --yes" — the flag is not asked for and does not decide (initflow's
// PersonAtTheTerminal deliberately ignores it), so an owner who passed it is told
// what actually decided the step rather than that a flag they did not need did.
func TestManagedOwnerTerminalWithYesReadsNoFlag(t *testing.T) {
	r := updateRig(t)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	noAgentSession(t)
	e := rigEngine(t, r, &initOptions{door: updateDoor.name, yes: true})
	out := atTerminal(t, e, "")
	e.gateRun(t.Context())
	if !e.r.gateStandsIn() {
		t.Fatalf("the rig's gate did not stand in for the run:\n%s", out)
	}

	ok, err := e.r.askOrdinary("writes the Terraform state bucket (cents a month)", "")
	if err != nil || !ok {
		t.Fatalf("the step was not confirmed with nothing typed (%v, %v):\n%s", ok, err, out)
	}
	if !strings.Contains(out.String(), initflow.OwnerProceeded()) {
		t.Errorf("the step does not say why nothing is typed:\n%s", out)
	}
	if strings.Contains(out.String(), "confirmed by --yes") {
		t.Errorf("a managed project's own terminal was told a flag confirmed a step it did not need:\n%s", out)
	}
}

// TestManagedOwnerTerminalAnchorWritesWithoutAsking: the fugaro.yaml anchor's
// y/N is one of the prompts a person meets during `fugaro update`, and it goes
// the same way — the diff is still printed, the file is written, and nothing
// is typed. The controls are the same call with no gate (a terminal, the same
// empty stdin, and the y/N) and with a coding agent's marker in the
// environment: this arm is the one of the ruling's that has no separate marker
// guard in front of it, so the marker read inside PersonAtTheTerminal is the whole
// of what keeps an agent's session out of the write.
func TestManagedOwnerTerminalAnchorWritesWithoutAsking(t *testing.T) {
	for _, tc := range []struct {
		name    string
		managed bool
		agent   bool
	}{
		{"managed", true, false},
		{"managed, an agent's session", true, true},
		{"not managed", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e, out := managedTerminalUpdate(t, tc.managed)
			if tc.agent {
				t.Setenv(agentMarkers[0], "1")
			}
			path := filepath.Join(t.TempDir(), "fugaro.yaml")
			old := []byte("name: aurora\n")
			if err := os.WriteFile(path, old, 0o600); err != nil {
				t.Fatal(err)
			}
			a := &anchorEdit{path: path, id: initProject, old: old,
				out: []byte("name: aurora\ngcp_project: " + initProject + "\n"), changed: true}

			written, err := e.r.writeAnchor(a, initflow.Env{Interactive: true}, false)
			if err != nil {
				t.Fatalf("the anchor's write failed: %v\n%s", err, out)
			}
			got, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if tc.managed && !tc.agent {
				if !written || string(got) != string(a.out) {
					t.Errorf("a managed project's own terminal did not write the line (written %v):\n%s", written, out)
				}
				if !strings.Contains(out.String(), initflow.OwnerProceeded()) {
					t.Errorf("the write does not say why nothing is typed:\n%s", out)
				}
				for _, no := range []string{"[y/N]", "confirmed by --yes"} {
					if strings.Contains(out.String(), no) {
						t.Errorf("a managed project's own terminal read %q:\n%s", no, out)
					}
				}
				return
			}
			if written || string(got) != string(old) {
				t.Fatalf("the line was written with nothing typed:\n%s", out)
			}
			if !strings.Contains(out.String(), "Write this to "+path+"? [y/N]") {
				t.Errorf("the y/N was not asked:\n%s", out)
			}
			if strings.Contains(out.String(), initflow.OwnerProceeded()) {
				t.Errorf("the run read the managed sentence:\n%s", out)
			}
		})
	}
}

// TestManagedOwnerTerminalAnchorWritesNothingWithNobodyAtTheTerminal is the
// anchor's other half: the same line, the same managed project, and stdin a
// pipe. The arm is the owner's own terminal's, so a run nobody is at writes
// nothing and is handed the line to add instead — the diff is printed either
// way, and the write the ruling makes for the owner is not one a pipe gets.
// Nothing needs a marker to be refused here: with no terminal, writeAnchor's
// own default arm leaves the file alone, which is the behaviour an arm that
// asked only about the marker would have replaced.
func TestManagedOwnerTerminalAnchorWritesNothingWithNobodyAtTheTerminal(t *testing.T) {
	_, e, out := managedUpdateNoTerminal(t)
	path := filepath.Join(t.TempDir(), "fugaro.yaml")
	old := []byte("name: aurora\n")
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	a := &anchorEdit{path: path, id: initProject, old: old,
		out: []byte("name: aurora\ngcp_project: " + initProject + "\n"), changed: true}

	written, err := e.r.writeAnchor(a, initflow.Env{}, false)
	if err != nil {
		t.Fatalf("the anchor's write failed: %v\n%s", err, out)
	}
	if written {
		t.Errorf("the line was written with nobody at the terminal:\n%s", out)
	}
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != string(old) {
		t.Errorf("the file changed with nobody at the terminal:\n%s", got)
	}
	if !strings.Contains(out.String(), "to let teammates use this installation without setup, add this line to "+path) {
		t.Errorf("a run nobody is at was not handed the line to add:\n%s", out)
	}
	if strings.Contains(out.String(), initflow.OwnerProceeded()) {
		t.Errorf("a run nobody is at read the sentence written for the owner's own terminal:\n%s", out)
	}
}

// TestManagedOwnerTerminalLayerWritesWithoutAsking: the project layer stage's
// three writes — seeding the local file from the published layer, publishing
// the file, and D854's re-seed — are the stage's own confirmations, and a
// person at a managed project's terminal types nothing for any of them. What
// they read instead is the plan the stage printed above (the file, the object,
// the generation, and the backup a re-seed leaves) and OwnerProceeded's
// sentence: a layer publish rewrites a document every repository resolves
// against, but it loses no data — the write is conditional on the generation
// the run read, and a re-seed keeps the file it replaces beside it — and takes
// no secret value, which is the whole of what the ruling leaves asked.
//
// The control is the same seed on a project that is not managed: the same
// empty terminal and the typed name, with no file written.
func TestManagedOwnerTerminalLayerWritesWithoutAsking(t *testing.T) {
	t.Run("managed", func(t *testing.T) {
		r := newLayerRig(t, testProjectLayer)
		r.managedRuns(t, initProjectName, initProject)
		isolateGateCredentials(t)
		noAgentSession(t)
		fakeTerminal(t)

		out, errOut, err := executeStdin(t, "", "update")
		if err != nil {
			t.Fatalf("a managed project's own terminal did not seed the layer: %v\n%s%s", err, out, errOut)
		}
		data, rerr := os.ReadFile(localLayerPath(t))
		if rerr != nil {
			t.Fatalf("the local layer file was not seeded with nothing typed: %v\n%s%s", rerr, out, errOut)
		}
		if string(data) != testProjectLayer {
			t.Errorf("the local layer file is not the published layer:\n%s", data)
		}
		if all := out + errOut; !strings.Contains(all, initflow.OwnerProceeded()) {
			t.Errorf("the seed does not say why nothing is typed:\n%s", all)
		}
		if all := out + errOut; strings.Contains(all, "Type "+initProjectName) {
			t.Errorf("a managed project's own terminal was asked for the project's name:\n%s", all)
		}
	})

	t.Run("not managed", func(t *testing.T) {
		r := newLayerRig(t, testProjectLayer)
		noAgentSession(t)
		e := rigEngine(t, r.initRig, &initOptions{door: updateDoor.name})
		out := atTerminal(t, e, "")
		e.r.cmd.SetOut(out) // confirmWrite writes through the command, not through r.w

		_, err := newLayerStage(e).Apply(t.Context(), initflow.Env{Interactive: true})
		if err == nil {
			t.Fatalf("a project that is not managed seeded the layer with nothing typed:\n%s", out)
		}
		if _, rerr := os.Stat(localLayerPath(t)); !errors.Is(rerr, os.ErrNotExist) {
			t.Errorf("the local layer file was seeded with nothing typed: %v", rerr)
		}
		if !strings.Contains(out.String(), "Type "+initProjectName+" to confirm") {
			t.Errorf("a project that is not managed was not asked:\n%s", out)
		}
		if strings.Contains(out.String(), initflow.OwnerProceeded()) {
			t.Errorf("a project that is not managed read the managed sentence:\n%s", out)
		}
	})
}

// TestManagedOwnerTerminalLayerRefusesAnAgentSession is the layer stage's own
// half of the same case, driven at its own call: a coding agent's session
// writes nothing there and reads no sentence of the owner's, and the
// confirmation it meets is the typed one it has always met. The gate is read
// first, so the arm is reachable at all — readUpdateGate answers the marks
// whoever is sitting there, and only PersonAtTheTerminal's marker is what
// keeps the session out. Apply refuses a marker on its own before any of this
// (the loop's agentStage does the same), so the arm itself is asked directly:
// the marker read inside PersonAtTheTerminal is the whole of what stands
// between an agent's session and a write the gate stands in for.
func TestManagedOwnerTerminalLayerRefusesAnAgentSession(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	noAgentSession(t)
	e := rigEngine(t, r.initRig, &initOptions{door: updateDoor.name})
	out := atTerminal(t, e, "")
	e.r.cmd.SetOut(out)
	e.gateRun(t.Context())
	if !e.r.gateStandsIn() {
		t.Fatalf("the rig's gate did not stand in for the run:\n%s", out)
	}
	// The marker goes in after the read, as in every other test here: it is
	// what a run's session is, and the gate's read is not a second one.
	t.Setenv(agentMarkers[0], "1")

	// A config with the rig's own project name is all the arm reads (the name
	// is what confirmRemoval compares and prints).
	if err := newLayerStage(e).confirmWrite(&localcfg.Config{Name: initProjectName}, initflow.Env{Interactive: true}, layerSeedWhat); err == nil {
		t.Fatalf("a coding agent's session on a managed project confirmed the layer's write:\n%s", out)
	}
	if _, err := newLayerStage(e).Apply(t.Context(), initflow.Env{Interactive: true}); err == nil {
		t.Fatalf("a coding agent's session on a managed project seeded the layer with nothing typed:\n%s", out)
	}
	if _, rerr := os.Stat(localLayerPath(t)); !errors.Is(rerr, os.ErrNotExist) {
		t.Errorf("the local layer file was seeded in a coding agent's session: %v", rerr)
	}
	if strings.Contains(out.String(), initflow.OwnerProceeded()) {
		t.Errorf("a coding agent's session read the owner's own sentence:\n%s", out)
	}
}

// TestManagedOwnerTerminalLayerRefusesWithNobodyAtTheTerminal is the layer
// stage's own half of the other case, and it is the one that matters most: a
// pipe carries no marker, so a managed update nobody is watching reads exactly
// like the owner's own session, and the terminal is the whole of what tells
// them apart. The step keeps the confirmation it always had — the name typed
// at a terminal, or --yes — and the file is not written.
func TestManagedOwnerTerminalLayerRefusesWithNobodyAtTheTerminal(t *testing.T) {
	r := newLayerRig(t, testProjectLayer)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	noAgentSession(t)
	e := rigEngine(t, r.initRig, &initOptions{door: updateDoor.name})
	out := atNoTerminal(e)
	e.r.cmd.SetOut(out)
	e.gateRun(t.Context())
	if !e.r.gateStandsIn() {
		t.Fatalf("the rig's gate did not stand in for the run:\n%s", out)
	}

	_, err := newLayerStage(e).Apply(t.Context(), initflow.Env{})
	if err == nil {
		t.Fatalf("a managed run nobody is at seeded the layer with nothing typed:\n%s", out)
	}
	if _, rerr := os.Stat(localLayerPath(t)); !errors.Is(rerr, os.ErrNotExist) {
		t.Errorf("the local layer file was seeded with nobody at the terminal: %v", rerr)
	}
	if strings.Contains(out.String(), initflow.OwnerProceeded()) {
		t.Errorf("a run nobody is at read the sentence written for the owner's own terminal:\n%s", out)
	}
}

// TestManagedOwnerTerminalUnverifiedFirebaseStillAsks is that boundary at the
// one production call site that reaches it without a Terraform plan: the
// database write carries no document for the gate to judge, and its own
// notCovered is firebaseReason — a Firebase project this run cannot prove is
// Fugaro's (init_firebase.go's deployDatabase). Nothing judged that write, so
// the owner's own terminal types the project's name for it, exactly as it did
// before the ruling, and the note says why.
func TestManagedOwnerTerminalUnverifiedFirebaseStillAsks(t *testing.T) {
	_, e, out := managedTerminalUpdate(t, true)
	e.r.review = &runReview{state: reviewTaken, firebase: func(context.Context) (bool, string) {
		return false, "another project than " + initProject + ", with no Fugaro mark"
	}}
	notCovered := e.r.firebaseReason()
	if notCovered == "" {
		t.Fatalf("the rig's Firebase project is covered, so it is not the case this test is about:\n%s", out)
	}

	ok, err := e.r.askOrdinary("writes the database as listed, and then the local config", notCovered)
	if err != nil {
		t.Fatalf("the database write was refused rather than asked: %v\n%s", err, out)
	}
	if ok {
		t.Fatalf("a database write no rule judged proceeded with nothing typed:\n%s", out)
	}
	if !strings.Contains(out.String(), notCovered+"; it asks for its own") {
		t.Errorf("the write does not say why it asks:\n%s", out)
	}
	if !strings.Contains(out.String(), "Type "+initProjectName) {
		t.Errorf("the database write was not asked for the project's name:\n%s", out)
	}
	if strings.Contains(out.String(), initflow.OwnerProceeded()) {
		t.Errorf("a database write no rule judged read the sentence written for the steps the review covered:\n%s", out)
	}
}

// TestManagedOwnerTerminalRefusedPlanIsNotApplied is the same boundary driven
// through the whole command instead of one call: the owner's own terminal, a
// managed project, and a plan the gate refuses — a grant to a member the review
// screen does not list, and a delete of a grant that is not one of the
// launcher's own replacements. Nothing is applied, no OwnerProceeded sentence
// is printed, and the run stops on the gate's own refusal, which is what the
// owner reads: the refusal is the gate's error (managed_gate.go's admit), so
// the step never reaches a confirmation at all, and an arm that answered for
// any plan at all would be applying one the gate refused.
func TestManagedOwnerTerminalRefusedPlanIsNotApplied(t *testing.T) {
	for _, tc := range []struct {
		name string
		plan planChange
	}{
		{"a grant to a member the review did not list", grantChange("state", "user:not-listed@example.com", "roles/storage.objectViewer", "create")},
		{"a delete of a grant that is not the launcher's", grantChange("state", "user:a@example.com", "roles/storage.objectAdmin", "delete")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := updateRig(t)
			r.managedRuns(t, initProjectName, initProject)
			isolateGateCredentials(t)
			noAgentSession(t)
			fakeTerminal(t)
			updating(t, r)
			r.setPlan(t, tc.plan)

			out, err := updateRun(t, r, "update")
			if err == nil {
				t.Fatalf("a plan the gate refused applied itself:\n%s", out)
			}
			if !strings.Contains(out, "the managed gate did not admit this step") {
				t.Errorf("the run did not stop on the gate's own refusal:\n%s", out)
			}
			if n := len(r.ran(t, "apply")); n != 0 {
				t.Errorf("a plan the gate refused was applied %d time(s):\n%s", n, out)
			}
			if strings.Contains(out, initflow.OwnerProceeded()) {
				t.Errorf("a plan the gate refused read the sentence written for the steps it admitted:\n%s", out)
			}
		})
	}
}

// TestManagedOwnerTerminalDataLossStillAsks is the exception the ruling is
// written around, and it is asserted through the call the --forget rollback
// makes: deleting the log isolation and removing the installation from
// Terraform's state are data loss, so the owner's own terminal in a managed
// project still types the project's name. The mutation caught is the tempting
// one — moving the owner's exemption down into initRun.ask, which every
// destructive step of this command goes through.
func TestManagedOwnerTerminalDataLossStillAsks(t *testing.T) {
	_, e, out := managedTerminalUpdate(t, true)

	err := e.r.confirm("rollback step 1 of 2: applies 0 to add, 0 to change, 3 to destroy: deletes the log isolation (the _Default exclusion, the sink, the view and its grants, the log bucket) and turns registry cleanup off",
		"nothing was applied or forgotten")
	if err == nil {
		t.Fatalf("a managed project's own terminal confirmed a destructive rollback with nothing typed:\n%s", out)
	}
	if !strings.Contains(out.String(), "Type "+initProjectName+" to apply to GCP project ") {
		t.Errorf("the destructive step did not ask for the project's name:\n%s", out)
	}
	if strings.Contains(out.String(), initflow.OwnerProceeded()) {
		t.Errorf("a destructive step read the sentence the ruling writes for steps that lose nothing:\n%s", out)
	}
}
