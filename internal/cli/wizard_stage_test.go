package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// wizard_stage_test.go pins the two-phase wizard (task W3.5 of
// docs/setup-wizard/implementation-plan.md; reconciliation F12 and O5): the
// wizard's questions are one phase — one initflow.InputStage, the first thing
// a run does — and the apply phase, whose order initflow's canonical list
// decides, starts only once every answer is in hand.
//
// What the questions themselves ask, and what their answers mean, is
// init_inputs_test.go's and setup_wizard_test.go's; this file is about the
// phase: that it is an InputStage the loop can refuse with, that it is
// needs-you with the question it stopped at, that it asks nothing where it
// cannot, and that it collects every answer before anything applies.

// wizardStageRun is the input phase's stage on the harness the wizard's own
// tests use (wizardRun, setup_wizard_test.go): a first run outside any
// checkout, a terminal this test's, and stdout collected.
func wizardStageRun(t *testing.T, stdin string, o *initOptions) (*initRun, *wizardStage, *strings.Builder) {
	t.Helper()
	r, out := wizardRun(t, stdin, o)
	return r, newWizardStage(t.Context(), r), out
}

// TestWizardCollectsEveryAnswerBeforeTheFirstApply is the plan's red step. The
// wizard's questions are all asked up front, and the loop applies nothing
// until they are: the answers decide the project config the run loads (the
// name, the GCP project and the region are what select it) and the stages
// initStages registers, so an apply that ran first would be applying another
// run's answers.
//
// The instrument is a fake applying stage in the same run, which records what
// it saw when it applied: not whether the questions were asked somewhere
// before, but whether every answer was in hand at the moment the first apply
// ran. The stage beside it is the production one, and it is the first of the
// two in the loop's canonical order — preflight, which is where the input
// phase sits.
func TestWizardCollectsEveryAnswerBeforeTheFirstApply(t *testing.T) {
	r, ws, out := wizardStageRun(t, stdinFor(""), nil)
	app := &orderStage{r: r, a: &ws.a}
	// The phase's own run is plan-only (the questions are its whole work), and
	// this is the other run: the one that applies, where the fake stage below
	// is the applying stage.
	opts := r.wizardOptions()
	opts.PlanOnly = false
	res, err := initflow.Run(t.Context(), []initflow.Stage{app, ws}, opts)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if res.Failed != nil {
		t.Fatalf("the phase failed at %s: %s\n%s", res.Failed.Stage, res.Failed.Error, out)
	}
	if !ws.a.complete {
		t.Errorf("the phase did not collect every answer:\n%s", out)
	}
	// The loop runs the input phase first: the answers are not merely
	// complete, they were complete before the apply.
	if !app.applied {
		t.Fatalf("the fake stage never applied, so this test proves nothing:\n%s", out)
	}
	if app.sawIncomplete != "" {
		t.Errorf("an apply ran with %s still unanswered:\n%s", app.sawIncomplete, out)
	}
	// Every question the table lists was asked, and asked by the phase.
	for _, q := range setupQuestions {
		if !slicesContains(ws.a.asked, q.label) && !askedByPrefix(ws.a.asked, q.label) {
			t.Errorf("%s: the phase did not ask %q, and the table says it does:\n%s", q.flag, q.label, out)
		}
	}
}

// askedByPrefix is whether the phase asked a question whose printed label
// begins with the table's: the name question's label carries the suggestion's
// provenance on the end of it.
func askedByPrefix(asked []string, label string) bool {
	for _, a := range asked {
		if strings.HasPrefix(a, label) {
			return true
		}
	}
	return false
}

// orderStage is a fake applying stage that records the moment it applies: the
// one instrument that can tell "the questions were asked" from "the questions
// were asked *before anything was applied*".
type orderStage struct {
	r             *initRun
	a             *wizardCollected
	applied       bool
	sawIncomplete string // the answers still missing when Apply ran
}

func (s *orderStage) Name() string { return initflow.Project }
func (s *orderStage) Check(context.Context) (initflow.Status, error) {
	return initflow.Status{State: initflow.Todo, Detail: "records what it saw"}, nil
}
func (s *orderStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	return initflow.Plan{Detail: "one fake change"}, nil
}
func (s *orderStage) Apply(context.Context, initflow.Env) (initflow.Outcome, error) {
	s.applied = true
	o := s.r.o
	for _, miss := range []struct {
		what string
		ok   bool
	}{
		{"the Fugaro project's name", o.name != ""},
		{"the GCP project's ID", o.cloud.gcpProject != ""},
		{"the region", o.cloud.region != ""},
		{"the create-or-adopt answer", s.a.complete},
	} {
		if !miss.ok && s.sawIncomplete == "" {
			s.sawIncomplete = miss.what
		}
	}
	return initflow.Outcome{Changed: true, Detail: "applied"}, nil
}
func (s *orderStage) Verify(context.Context) error { return nil }
func (s *orderStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Project, Kind: initflow.LeftCommand, Text: "type the project's name"}
}
func (s *orderStage) SelfConfirming() {}

// TestWizardStageIsAnInputStage: the input phase is an initflow.InputStage, so
// --non-interactive's refusal is the loop's own — one error naming every flag
// the phase cannot answer for itself, exactly as the wizard named them before
// this task. It is read off the product: the loop's check is what produces the
// error, and the stage under it is the real one.
func TestWizardStageIsAnInputStage(t *testing.T) {
	r, ws, out := wizardStageRun(t, "", &initOptions{door: setupDoor.name, nonInteractive: true})
	var _ initflow.InputStage = ws

	_, err := initflow.Run(t.Context(), wizardStages(t.Context(), r), r.wizardOptions())
	var missing *initflow.MissingInputsError
	if !errors.As(err, &missing) {
		t.Fatalf("the loop did not refuse with the flags the phase still needs (%v):\n%s", err, out)
	}
	for _, q := range setupQuestions {
		if q.missing == "" {
			// The one question whose answer a run that cannot ask takes
			// itself (creation is never taken on nobody's behalf: setup.go's
			// askProjectAnswer).
			if strings.Contains(strings.Join(missing.Flags, "; "), q.flag) {
				t.Errorf("%s: the refusal names %s, and a run that cannot ask takes that answer itself: %v", q.what, q.flag, missing.Flags)
			}
			continue
		}
		if !strings.Contains(strings.Join(missing.Flags, "; "), q.missing) {
			t.Errorf("%s: the refusal does not name %q: %v", q.what, q.missing, missing.Flags)
		}
	}
	// The stage's own Missing() is that list: the flags, each with what it is
	// for, and the same ones the loop just printed.
	if got, want := strings.Join(ws.Missing(), "; "), strings.Join(missing.Flags, "; "); got != want {
		t.Errorf("Missing() = %q, the loop refused with %q: the two have to be the one list", got, want)
	}
}

// TestWizardNeedsYouNamesTheQuestionAndAKind: a run that cannot answer every
// question does not fail — it is needs-you, with the question it stopped at
// and the way the user does it. The kind is what tells the reader where the
// answer comes from: `command` when the run cannot ask at all (the flags are
// the answer), `prompt` when it asked and no answer came (the question is
// answered at a terminal).
func TestWizardNeedsYouNamesTheQuestionAndAKind(t *testing.T) {
	t.Run("no way to ask: the flags, as commands", func(t *testing.T) {
		// --json at a terminal: the run cannot ask (a document is its stdout),
		// and the answers are the flags.
		_, ws, out := wizardStageRun(t, stdinFor(""), &initOptions{door: setupDoor.name, asJSON: true})
		st, err := ws.Check(t.Context())
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if st.State != initflow.NeedsYou {
			t.Fatalf("state %q, want needs-you: the run has no answers and no way to ask:\n%s", st.State, out)
		}
		lf := ws.Left()
		if st.Left != nil {
			lf = *st.Left
		}
		if lf.Kind != initflow.LeftCommand {
			t.Errorf("kind %q, want %q: this run's answer is a flag, not a prompt", lf.Kind, initflow.LeftCommand)
		}
		for _, q := range setupQuestions {
			if q.missing != "" && !strings.Contains(lf.Text, q.missing) {
				t.Errorf("the left-for-you line does not name %q: %s", q.missing, lf.Text)
			}
		}
		if strings.Contains(out.String(), "[fugaro-") {
			t.Errorf("the run asked a question it cannot ask:\n%s", out)
		}
	})

	t.Run("asked, and no answer came: the question, as a prompt", func(t *testing.T) {
		// The input ends at the first question: the run stopped there, and
		// that is the question the line names — with the kind that says it is
		// answered at a terminal, not with a flag.
		_, ws, out := wizardStageRun(t, "", nil)
		st, err := ws.Check(t.Context())
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if st.State != initflow.NeedsYou {
			t.Fatalf("state %q, want needs-you: the input ended at the first question:\n%s", st.State, out)
		}
		lf := ws.Left()
		if st.Left != nil {
			lf = *st.Left
		}
		if lf.Kind != initflow.LeftPrompt {
			t.Errorf("kind %q, want %q: the run stopped at a prompt, and a prompt is where the answer is", lf.Kind, initflow.LeftPrompt)
		}
		if !strings.Contains(lf.Text, "GCP project ID") {
			t.Errorf("the left-for-you line does not name the question it stopped at: %s", lf.Text)
		}
	})
}

// TestWizardUnderJSONAsksNothingAndNamesEveryFlag: --json's stdout is one
// document, so the run asks nothing there — no prompt is printed, nothing is
// read from stdin — and what it says instead is every flag the answers need.
// The flags are the same ones --non-interactive names, because they are the
// same list: this is the stage's own Missing(), not a second copy of it.
func TestWizardUnderJSONAsksNothingAndNamesEveryFlag(t *testing.T) {
	_, ws, out := wizardStageRun(t, stdinFor(""), &initOptions{door: setupDoor.name, asJSON: true})
	// The stdin the terminal would have answered from is left unread.
	if _, err := ws.Check(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if ws.a.complete {
		t.Errorf("--json recorded answers nobody gave:\n%s", out)
	}
	if got := out.String(); got != "" {
		t.Errorf("--json printed a prompt; its stdout is one document:\n%s", got)
	}
	flags := ws.Missing()
	if len(flags) == 0 {
		t.Fatal("the run names no flag under --json, and it has no answers")
	}
	for _, q := range setupQuestions {
		if q.missing == "" {
			if strings.Contains(strings.Join(flags, "; "), q.flag) {
				t.Errorf("%s: the run names %s, and it takes that answer itself", q.what, q.flag)
			}
			continue
		}
		if !strings.Contains(strings.Join(flags, "; "), q.missing) {
			t.Errorf("%s: the run does not name %q: %v", q.what, q.missing, flags)
		}
	}
	// And it is the same list the phase's own Left carries, so a reader of
	// --json and a reader of --non-interactive are told the same thing.
	lf := ws.Left()
	if !strings.Contains(lf.Text, strings.TrimSuffix(ws.Missing()[0], ")")) && !strings.Contains(lf.Text, flags[0]) {
		t.Errorf("the left-for-you line (%s) and Missing() (%v) disagree", lf.Text, flags)
	}
}

// TestWizardResumesAtTheUnansweredQuestion: a rerun of an interrupted wizard
// picks the questions up where they stopped. What says where that is, is the
// Left: the phase asks the questions it has no answer for, in the table's
// order, and the first one it cannot get is the one it names — so a run that
// stopped at the question after the first two says so, rather than naming the
// first question of the table.
func TestWizardResumesAtTheUnansweredQuestion(t *testing.T) {
	for _, tc := range []struct {
		name string
		// stdin and o are the run that stops.
		stdin string
		o     *initOptions // nil for a bare first run
		// at is the question the run stops at, as the table labels it.
		at string
		// And these are the rerun: the same questions with the one it stopped
		// at now answered, which is the flag of that question.
		reO  *initOptions
		reIn string
	}{
		{
			name: "the first question",
			at:   setupQuestions[0].label,
			reO:  optionsFor(setupQuestions[0].flag),
			reIn: stdinFor(setupQuestions[0].flag),
		},
		{
			name: "a later question, the ones before it answered",
			// --gcp-project answered the first question and the region was
			// answered at the terminal; the input ends at the question after
			// them, which is the one this run stopped at.
			stdin: "\n",
			o:     optionsFor(setupQuestions[0].flag),
			at:    setupQuestions[2].label,
			reO:   optionsFor(setupQuestions[2].flag),
			reIn:  "\n\n\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ws, out := wizardStageRun(t, tc.stdin, tc.o)
			st, err := ws.Check(t.Context())
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if st.State != initflow.NeedsYou {
				t.Fatalf("state %q, want needs-you:\n%s", st.State, out)
			}
			lf := ws.Left()
			if st.Left != nil {
				lf = *st.Left
			}
			if lf.Kind != initflow.LeftPrompt {
				t.Errorf("kind %q, want %q", lf.Kind, initflow.LeftPrompt)
			}
			if !strings.Contains(lf.Text, tc.at) {
				t.Errorf("the run stopped at %q, and its left-for-you line names %s", tc.at, lf.Text)
			}
			if ws.a.complete {
				t.Error("an interrupted run recorded every answer")
			}
			// The rerun: the question it stopped at is answered now (by its
			// flag), and the phase goes on to the end instead of stopping
			// again at the same question.
			_, ws2, out2 := wizardStageRun(t, tc.reIn, tc.reO)
			st2, err := ws2.Check(t.Context())
			if err != nil || st2.State != initflow.Done || !ws2.a.complete {
				t.Fatalf("the resumed run: state %q, complete %v, %v\n%s", st2.State, ws2.a.complete, err, out2)
			}
		})
	}
}

// TestWizardPhaseIsOneStageAndItIsFirst: the phase is one run of one stage —
// so `converge`'s own list (initStages) is untouched by it, and the stage's
// name is the canonical order's first slot, which is where the input phase
// sits. A second stage in the phase would be a stage whose answers arrive
// after the ones the loop has already read.
func TestWizardPhaseIsOneStageAndItIsFirst(t *testing.T) {
	r, _, _ := wizardStageRun(t, stdinFor(""), nil)
	stages := wizardStages(t.Context(), r)
	if len(stages) != 1 {
		t.Fatalf("the input phase runs %d stages (%v), and it is one: the questions are one phase", len(stages), namesOf(stages))
	}
	if got := stages[0].Name(); got != initflow.Names()[0] {
		t.Errorf("the input phase's stage is named %q, and the first slot of the canonical order is %q", got, initflow.Names()[0])
	}
	if _, ok := stages[0].(initflow.InputStage); !ok {
		t.Errorf("the input phase's stage is not an initflow.InputStage: %T", stages[0])
	}
	// The apply phase's list does not carry it: the two phases are two runs,
	// and a stage under one name twice in one run is what initflow.Validate
	// refuses.
	apply := initStages(prodEngine(initOptions{}))
	if got := namesOf(apply); slicesContains(got, stages[0].Name()) && !slicesContains(got, initflow.Preflight) {
		t.Errorf("the apply phase's list (%v) carries the input phase's stage", got)
	}
	if _, err := initflow.Validate(append(append([]initflow.Stage{}, apply...), stages...)); err == nil {
		t.Errorf("initflow.Validate accepted the two phases in one run, and the input phase is a run of its own: %v", namesOf(apply))
	}
}

// TestWizardPhaseOptionsMatchTheRun: the phase is run with the run's own
// conditions — the flags that say nobody is answering (--non-interactive,
// --json, --yes) and whether there is a terminal — because those are what
// decide whether a question may be asked at all. And its transcript is not a
// second one: the loop's own lines about the phase go nowhere, since the
// questions print themselves and a view of one stage would be a line about a
// stage this run does not have.
func TestWizardPhaseOptionsMatchTheRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    *initOptions
		term bool
	}{
		{"plain", &initOptions{door: setupDoor.name}, true},
		{"--non-interactive", &initOptions{door: setupDoor.name, nonInteractive: true}, false},
		{"--json", &initOptions{door: setupDoor.name, asJSON: true}, true},
		{"--yes", &initOptions{door: setupDoor.name, yes: true}, true},
		{"no terminal", &initOptions{door: setupDoor.name}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			firstRunEnv(t)
			if tc.term {
				fakeTerminal(t)
			}
			r := &initRun{o: tc.o, w: &strings.Builder{}, in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}
			got := r.wizardOptions()
			want := initflow.Options{
				Yes: tc.o.yes, NonInteractive: tc.o.nonInteractive, Terminal: tc.term,
				// The phase never applies: plan-only is what runs its check
				// once and nothing else (wizardOptions).
				PlanOnly: true,
				JSON:     tc.o.asJSON, Program: tc.o.loopProgram(), Out: got.Out,
			}
			if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
				t.Errorf("the phase is run with\n  %+v\nand this run is\n  %+v", got, want)
			}
			if got.Out == nil {
				t.Error("the phase's transcript goes nowhere, and nil is not nowhere")
			}
		})
	}
}
