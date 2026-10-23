package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// wizard_stage.go is the wizard's input phase (task W3.5 of
// docs/setup-wizard/implementation-plan.md; reconciliation F12 and O5).
//
// The wizard is two phases. The questions are the first: one stage of one
// initflow.Run, run by gatherInputs (init_inputs.go) before the project config
// is loaded and before the applies start. The applies are the second, and their
// order is initflow's canonical one and not the order the questions were asked
// in (F12): the questions are about what to create, and the loop is what knows
// what has to exist before what.
//
// The phase is a run of the loop, and not a sequence of prompts in front of
// one, because the loop is where the two things a question needs already are.
// The first is the refusal: --non-interactive produces one error naming every
// flag the stage cannot answer for itself (initflow.Run's check over
// InputStage.Missing), which is the error gatherInputs has always produced.
// The second is the state that says a run is waiting for a person rather than
// failing: needs-you, with a Left whose kind says where the answer comes from —
// a prompt at a terminal, the flags where there is none.
//
// Nothing applies in that run. The phase's Options are plan-only, so the loop
// checks the stage once (Options.PlanOnly skips the execution pass, which would
// ask every question a second time) and nothing else. The stage's Check never
// reports will-do, so a run that did enter the execution pass still has nothing
// to apply.
//
// The one thing the phase does not carry is the answers themselves: they are
// recorded on the run's own options as they are given (--gcp-project, --region,
// --name, and the field --create-project sets), which is where the rest of the
// run already reads every input from — so a person at a terminal and a scripted
// run answer one question in one place, and the project stage is the one stage
// that acts on the answer.

// wizardCollected is what the input phase collected, and where it stopped: the
// questions it asked, in the order it asked them, the Left it stopped at when
// one of them was not answered, and whether every answer is in hand.
type wizardCollected struct {
	asked    []string       // the labels of the questions this run asked, in order
	left     *initflow.Left // the question the run stopped at; nil once every answer is in
	complete bool           // every answer is in hand
}

// wizardStage is the wizard's questions as one initflow.InputStage: the first
// thing a run does, and the only stage of the input phase's run.
//
// It is named for the first slot of the canonical order (preflight), because
// the loop orders the stages it is given by that list and not by the order they
// are passed in: a run that asks its questions has to be the one the loop runs
// first.
type wizardStage struct {
	r *initRun
	a wizardCollected

	// What the run looked like before a question was asked, read once when the
	// stage is built (newWizardStage): the name and the provenance the ID and
	// the name questions suggest from, and the flags a run that cannot ask is
	// refused with. Missing() is given no context, so the run is read here
	// rather than when the loop asks for the list — which is also when
	// gatherInputs read it, before any question was asked.
	name, fromCheckout string
	creating           bool
	missing            []string
}

// newWizardStage is the phase's stage for one run.
func newWizardStage(ctx context.Context, r *initRun) *wizardStage {
	name, fromCheckout, flags, creating := r.wizardInputs(ctx)
	return &wizardStage{r: r, name: name, fromCheckout: fromCheckout, creating: creating, missing: flags}
}

// wizardInputs is what the questions need to know about the run before they are
// asked, and the flags a run that cannot ask is refused with: the name this run
// already has (--name, or the selected config's) and where it came from, and
// every question's flag with what it is for. A run with a project config, or
// one that creates nothing (a config-only run), has no questions at all: the
// list is empty and the phase is done as soon as it is checked.
func (r *initRun) wizardInputs(ctx context.Context) (name, fromCheckout string, flags []string, creating bool) {
	name, fromCheckout, missing, creating := r.firstRunMissing(ctx)
	if !creating {
		return "", "", nil, false
	}
	flags = make([]string, len(missing))
	for i, m := range missing {
		flags[i] = m.String()
	}
	// The GitHub App's ID is a question this run cannot answer for itself
	// either, and it is named with the rest (W3.4, and firstRunMissing's own
	// list does not hold it: it is the checkout's repository that needs it).
	// Only where the run cannot ask and something else is already missing: a
	// run whose answers all came from flags is not refused here at all, and the
	// repository stage names its own missing flag where it is registered
	// (init_stages.go) — naming it here as well would name it twice.
	if len(flags) > 0 && !r.canAsk() && r.o.githubAppID == "" {
		if t, _ := resolveRepoTarget(ctx, name); t != nil && t.needsAppID(nil, r.o) && sameRepo(r.o.onboardRepo, t.repo) {
			flags = append(flags, appIDFlag)
		}
	}
	return name, fromCheckout, flags, true
}

// Name is the first slot of the canonical order, which is what makes the
// questions the first thing a run of the loop does.
func (s *wizardStage) Name() string { return initflow.Preflight }

// Missing is every question's flag, with what it is for, in the table's order.
// It is one list for the three readers that need it: the loop's
// --non-interactive check (one error naming all of them, exactly as
// gatherInputs produced it), the Left a run that cannot ask is left with, and
// --json.
func (s *wizardStage) Missing() []string { return append([]string(nil), s.missing...) }

// Check asks the questions and reports where the run stands: done once every
// answer is in hand, and needs-you with the question it stopped at before that.
// A question the run could not get an answer for is not a failure — it is still
// open, and a rerun resumes at it — which is what makes the questions a phase
// rather than a sequence of errors between applies.
func (s *wizardStage) Check(ctx context.Context) (initflow.Status, error) {
	// A run that has already stopped stays stopped: it is waiting for a person,
	// and asking its questions again is what a rerun is for, not a second
	// check of the same run.
	if s.a.left == nil && !s.a.complete {
		if err := s.ask(ctx); err != nil {
			return initflow.Status{}, err
		}
	}
	if s.a.left != nil {
		return initflow.Status{State: initflow.NeedsYou, Detail: s.a.left.Text, Left: s.a.left}, nil
	}
	s.a.complete = true
	return initflow.Status{State: initflow.Done, Detail: "the answers are in hand"}, nil
}

// Plan is empty by construction: the phase's work is its Check, and this is
// what it would do if a run ever planned it.
func (s *wizardStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	return initflow.Plan{NothingToDo: true, Detail: "the questions are asked, not planned"}, nil
}

// Apply is unreachable: Check reports done or needs-you and never will-do, so
// the loop has nothing to apply here. It refuses rather than applying anything,
// because a stage that is only ever asked its questions has no apply to get
// wrong.
func (s *wizardStage) Apply(context.Context, initflow.Env) (initflow.Outcome, error) {
	return initflow.Outcome{}, errors.New("the wizard's questions apply nothing")
}

// Verify is unreachable for Apply's reason.
func (s *wizardStage) Verify(context.Context) error { return nil }

// Left is where the phase stopped: the question it could not answer, with the
// kind that says where the answer comes from — a prompt at a terminal, in the
// words the refusal has always used, or the flags, where the run cannot ask at
// all.
func (s *wizardStage) Left() initflow.Left {
	if s.a.left != nil {
		return *s.a.left
	}
	return initflow.Left{Stage: initflow.Preflight, Kind: initflow.LeftCommand, Text: wizardFlagsLeft(s.missing)}
}

// stop records where the run stopped: the Left the phase is, and the question a
// rerun picks up at.
func (s *wizardStage) stop(kind initflow.LeftKind, text string) {
	s.a.left = &initflow.Left{Stage: initflow.Preflight, Kind: kind, Text: text}
}

// answer is what a question's own error is. A prompt that got no answer is not
// a refusal: the question is still open, and the phase stops at it with the
// message the run has always printed for it (noAnswerError, init_inputs.go).
// Anything else — the creation checks the answer runs, the billing question's
// own refusals — is the run's to report, exactly as it was.
func (s *wizardStage) answer(err error) error {
	var na *noAnswerError
	if errors.As(err, &na) {
		s.stop(initflow.LeftPrompt, na.Error())
		return nil
	}
	return err
}

// wizardFlagsLeft is the line a run that cannot ask its questions is left with:
// the flags that answer them, named one by one. It is the refusal gatherInputs
// has always printed, in the same words, and the list is the same one
// Missing() holds.
func wizardFlagsLeft(flags []string) string {
	if len(flags) == 0 {
		// Every answer came from a flag: there is no question left to point
		// at, and a run in that state is not needs-you at all (Check is done).
		return "the wizard's questions are answered"
	}
	return "there is no project config yet: pass " + strings.Join(flags, ", ") + " to create one"
}

// ask is the wizard's questions, in the table's order and only where the answer
// is one this run acts on — the body gatherInputs has always had, with one
// change: a question nobody answered stops the phase as the Left it is instead
// of failing the run.
func (s *wizardStage) ask(ctx context.Context) error {
	r, o := s.r, s.r.o
	if !s.creating {
		// A project config is selected: this run's inputs are already settled,
		// nothing is asked, and nothing is created.
		return r.settleCreation(false)
	}
	if len(s.missing) > 0 && !r.canAsk() {
		// Nobody can be asked: the flags are the answer. --non-interactive
		// never reaches this — the loop refuses first, with the same list — so
		// this is the run with no terminal and the run whose stdout is a
		// document (--json).
		s.stop(initflow.LeftCommand, wizardFlagsLeft(s.missing))
		return nil
	}
	// The banner goes with any question this run is about to ask.
	if len(s.missing) > 0 {
		fmt.Fprintln(r.w, "fugaro setup: this is the first run here; each answer has a suggestion, press Enter to accept it.")
	}
	if o.cloud.gcpProject == "" {
		q := questionFor("--gcp-project")
		def, alt, why := projectIDSuggestions(ctx, s.name, s.fromCheckout, o.door == updateDoor.name)
		s.a.asked = append(s.a.asked, q.label)
		v, err := r.promptChoice(q.label, def, alt, why, func(v string) error {
			if !infra.ValidProjectID(v) {
				return errors.New("a project ID is 6 to 30 of a-z, 0-9 and '-', starting with a letter")
			}
			return nil
		})
		if err != nil {
			return s.answer(err)
		}
		o.cloud.gcpProject = v
	}
	if o.cloud.region == "" {
		q := questionFor("--region")
		s.a.asked = append(s.a.asked, q.label)
		v, err := r.prompt(q.label, defaultRegion, func(v string) error {
			if !regionRE.MatchString(v) {
				return errors.New("a region looks like us-east5 or europe-west4")
			}
			return nil
		})
		if err != nil {
			return s.answer(err)
		}
		o.cloud.region = v
	}
	// An installation may already be there (one this machine has no project
	// config for: the owner's second machine, or a teammate's). `setup` only
	// starts fresh, so it refuses the ID before the name is asked and says
	// which command reads that installation's config instead. `update` is that
	// command: what it settles here is the name — the installation's own, so
	// it is not asked — and its installation stage then writes this machine's
	// local config from the installation and applies nothing
	// (init_new_machine.go).
	if s.name == "" && !o.configOnly {
		if installed := installationName(ctx, o.cloud.gcpProject, o.cloud.region, o.runsBucket); installed != "" {
			if o.door != updateDoor.name {
				return freshOnlyInstalled(o.cloud.gcpProject, installed, o.cloud.region)
			}
			o.name = installed
			fmt.Fprintf(r.w, "%sthe installation of project %s is in GCP project %s and this machine has no local config for it: writing it from the installation, and applying nothing\n",
				o.programPrefix(), pluginwire.Printable(installed), pluginwire.Printable(o.cloud.gcpProject))
			return nil
		}
	}
	// The project: a first run of `setup` creates it (settleCreation). There
	// is no question to ask: Fugaro only starts fresh.
	if err := r.settleCreation(true); err != nil {
		return s.answer(err)
	}
	if s.name == "" && !o.configOnly {
		def, label := suggestName(ctx), questionFor("--name").label+" (permanent"
		switch _, _, ok := originOwner(ctx); {
		case s.fromCheckout != "":
			// No default: the file may be a clone's, so the name is typed.
			def, label = "", label+"; this checkout's fugaro.yaml says "+pluginwire.Printable(s.fromCheckout)+", but a cloned repository's file can say anything: type the name you want"
		case ok:
			label += "; suggested from the origin's owner"
		case def != "":
			label += "; suggested from this directory's name"
		}
		label += ")"
		s.a.asked = append(s.a.asked, label)
		v, err := r.prompt(label, def, func(v string) error {
			if !config.ProjectNameRE.MatchString(v) {
				return errors.New("a project name is 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
			}
			return nil
		})
		if err != nil {
			return s.answer(err)
		}
		o.name = v
	}
	// The billing question is the last of the wizard's questions (W4.3): it is
	// about the GCP project the answers above settled, and it is asked only
	// where the answer is one this run acts on (asksBilling, billing_prompt.go).
	// Its own "no answer came" is the phase's Left like any other question's:
	// the run resumes at it, and the refusal is the one line it has always
	// been.
	return s.answer(r.askBilling(ctx))
}

// wizardOptions is the input phase's run: the run's own conditions — nobody is
// answering under --non-interactive, --json or --yes, and a terminal is what
// decides whether a question can be asked at all — and nothing that applies.
//
// Plan-only is what makes the phase a phase: the loop checks a stage in the
// preview pass and again in the execution pass, and a question asked twice is a
// question asked twice. The phase's transcript goes nowhere: the questions
// print themselves (to the run's own writer, r.w) and the loop's lines about
// the phase would be lines about a stage this run does not have.
func (r *initRun) wizardOptions() initflow.Options {
	o := r.o
	return initflow.Options{
		Yes:            o.yes,
		NonInteractive: o.nonInteractive,
		PlanOnly:       true,
		Terminal:       stdinIsTerminal(r.cmd.InOrStdin()),
		JSON:           o.asJSON,
		Out:            io.Discard,
		Program:        o.loopProgram(),
		Redact:         secretsHeld(),
	}
}
