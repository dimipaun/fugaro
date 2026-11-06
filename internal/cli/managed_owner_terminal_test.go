package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// managed_owner_terminal_test.go is the owner's ruling of 2026-11-06: a person
// at a real terminal on a managed project is never asked to type a
// confirmation, except for data loss and secret values.
//
// Each test below is one of the ruling's cases, and each is run in the two
// worlds that have to be told apart — the same run with a coding agent's
// marker in the environment, and the same run on a project that is NOT managed
// — because the exception is about WHO is at the terminal and never about what
// the gate admitted. The mutation the file exists to catch is the tempting one:
// reading "a terminal" as "the owner" (a coding agent's session has a terminal
// too, and its own refusal is the whole of what keeps an agent out of these
// writes), or reading the exception as one about the project rather than about
// the session (a project that is not managed keeps every prompt it had).
//
// The rigs are managed_money_steps_test.go's managedTerminalUpdate (the gate
// read once, beside the loop, in the owner's own session) and updateRig's own
// helpers; the data-loss case is asserted through the very call the --forget
// rollback makes, because what the ruling leaves asked is that call and not a
// step's.

// TestManagedOwnerTerminalOrdinaryStepTypesNothing: the ordinary steps of the
// converge — the state bucket, the Viewer-read removal, a step the review did
// not cover — are the ones the run's one confirmation used to cover, and on a
// managed project the owner types nothing for them either. What is printed is
// the step's own banner and OwnerProceeded's sentence, which claims no verdict
// because no rule judged these steps; the note of why a step was not covered
// is still shown, because it is what the sentence would have carried.
//
// The assertion about the question is the empty stdin: the rig's terminal has
// nothing to read, so a step that asked would read no answer and report false,
// and `ok` being true is the step having proceeded with nothing typed.
func TestManagedOwnerTerminalOrdinaryStepTypesNothing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		what       string
		notCovered string
	}{
		{"covered", "writes the Terraform state bucket (cents a month)", ""},
		{"its own", "removes project Viewers' read access to the runs bucket", ""},
		{"outside the review", "applies the plan as shown", "the plan is outside what the review announced (a destroy of one resource)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e, out := managedTerminalUpdate(t, true)
			if tc.notCovered != "" {
				// The note is printed only for a step the run's review had
				// already been taken for (init_review.go's askOrdinary), which
				// is the state this rig's run is in after its review.
				e.r.review = &runReview{state: reviewTaken}
			}
			ok, err := e.r.askOrdinary(tc.what, tc.notCovered)
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
			if tc.notCovered != "" && !strings.Contains(out.String(), tc.notCovered) {
				t.Errorf("the step does not say what the run's confirmation did not cover:\n%s", out)
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

// TestManagedOwnerTerminalWithYesReadsNoFlag is the ruling's third item: the
// banner on a plain managed update names no flag, because the run needed none.
// The same step with --yes reads the managed sentence and not "confirmed by
// --yes" — the flag is not asked for and does not decide (initflow's
// OwnerAtTerminal deliberately ignores it), so an owner who passed it is told
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
// guard in front of it, so the marker read inside OwnerAtTerminal is the whole
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

// TestManagedOwnerTerminalPolicyLooseningNotesAndWrites is the ruling's first
// named case: a publish that loosens a ceiling — or widens an account key —
// takes the Typed class (D846) because it rewrites what every run of the
// project is admitted by, and D1026 admits no plan gate's answer for it. The
// owner's own terminal on a managed project is not a coding agent's session,
// though, and a ceiling is not data and not a secret value: the step prints
// what changes and writes, and nothing is typed. The document the gate judged
// is still judged and still recorded (the class is Typed, so no admission is
// recorded for it — the record is about what the gate ADMITTED).
func TestManagedOwnerTerminalPolicyLooseningNotesAndWrites(t *testing.T) {
	r, db := updateBudgetRig(t)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	noAgentSession(t)
	fakeTerminal(t)
	updating(t, r)
	r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))
	// The node states 3 and the local config states 5 (updateBudgetRig): the
	// publish raises what every run of the project is admitted by, which is the
	// loosening the step notes and the owner proceeds with.
	db.Set(projectpolicy.PathShared(), staleBudgetNodeAt(3))

	got, err := updateRun(t, r, "update")
	if err != nil {
		t.Fatalf("a managed project's own terminal could not loosen the ceiling: %v\n%s", err, got)
	}
	if got := ceilingOn(t, db); got != 5 {
		t.Errorf("the published ceiling is %v, want the local config's 5: the step did not write", got)
	}
	if !strings.Contains(got, initflow.OwnerProceeded()) {
		t.Errorf("the loosening does not say why nothing is typed:\n%s", got)
	}
	if !strings.Contains(got, "per_run_usd") {
		t.Errorf("the step does not print what it changes:\n%s", got)
	}
	if strings.Contains(got, "Type "+initProjectName+" to confirm") {
		t.Errorf("a managed project's own terminal was asked to type the project's name:\n%s", got)
	}
}

// TestManagedOwnerTerminalEmptyAllowListNotes: the first publish of a policy
// that admits no repository is the one warning an empty allow-list takes
// (#1674), and on the owner's own terminal in a managed project it is printed
// as the note and the publish goes on. The explicit door and a coding agent's
// session keep the typed name (confirmEmptyAllowList's own two arms), which is
// what the second half below holds.
func TestManagedOwnerTerminalEmptyAllowListNotes(t *testing.T) {
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	cmd.SetIn(strings.NewReader(""))

	if err := confirmEmptyAllowList(cmd, &localcfg.Config{Name: initProjectName}, "", true); err != nil {
		t.Fatalf("a managed project's own terminal was refused the empty allow-list: %v\n%s", err, out)
	}
	if !strings.Contains(out.String(), policyEmptyAllowListWarning()) || !strings.Contains(out.String(), initflow.OwnerProceeded()) {
		t.Errorf("the warning is not printed as the note:\n%s", out)
	}
	if strings.Contains(out.String(), "Type "+initProjectName) {
		t.Errorf("a managed project's own terminal was asked for the project's name:\n%s", out)
	}

	out = &syncBuf{}
	cmd = NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	cmd.SetIn(strings.NewReader(""))
	if err := confirmEmptyAllowList(cmd, &localcfg.Config{Name: initProjectName}, agentMarkers[0], true); err == nil {
		t.Errorf("a coding agent's session published an empty allow-list:\n%s", out)
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
