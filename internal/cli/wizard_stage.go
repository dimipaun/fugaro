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
	"github.com/dimipaun/fugaro/internal/place"
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
		// A still-creating resume owes one question (task W): see
		// wizardResumesCreation. The rest are settled in the config it selects,
		// which is why this is not the first-run list above.
		if r.wizardResumesCreation(ctx) && r.o.rigor == "" {
			return "", "", []string{questionFor("--rigor").missing}, false
		}
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
	// The rigor question is a first run's, and a run that cannot ask is refused
	// with the flag that answers it like every other question (task W): the
	// recommended mode is not taken for the caller, because a scripted setup is
	// where a default nobody typed would be least visible. It is named after
	// the flags above, which are the ones the run cannot start without, and the
	// list comes from the table so the sentence and the row cannot drift.
	if r.o.setupCreates(creating) && r.o.rigor == "" {
		flags = append(flags, questionFor("--rigor").missing)
	}
	return name, fromCheckout, flags, true
}

// wizardResumesCreation is whether this run picks up a first run that paused at
// billing (localcfg.Config.SetupPending): the config it selects is that run's,
// and the creation — with the one question about the new project that run could
// not have written anywhere — is this run's to finish.
//
// That question is the rigor answer (task W), and it is the reason this exists:
// the paused run asked it before it applied anything, and then stopped at the
// Project stage, which is before the Layer stage that writes and publishes the
// answer. Keeping it only in memory lost it with that run, and no later run can
// recover it — a config that exists means the wizard asks nothing and refuses
// --rigor — so the resume asks it again, or takes it from the flag, and its own
// layer stage writes it. The bake answer, which the paused run also had to
// carry, is written into the config itself instead (carryBakeAnswer), which is
// why it needs no question here.
func (r *initRun) wizardResumesCreation(ctx context.Context) bool {
	return r.o.setupCreates(true) && r.selectedPending(ctx)
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
		// The config a first run wrote when it paused at billing, this run's to
		// finish: it creates, so it is the run the questions about a new project
		// are asked on, and the ones it still owes are the answers the paused run
		// could not carry anywhere — the rigor answer, which the paused run gave
		// before it stopped and which only the layer stage writes, and the budget
		// answer, which no config member holds either and which a resumed
		// creation records like any other creating run (R8 of
		// docs/managed-flag-ergonomics; askBudget). The bake answer is not among
		// them: the paused run wrote it into the config itself
		// (carryBakeAnswer), which is why it needs no question here.
		if r.wizardResumesCreation(ctx) {
			if len(s.missing) > 0 && !r.canAsk() {
				s.stop(initflow.LeftCommand, wizardFlagsLeft(s.missing))
				return nil
			}
			if err := r.settleCreation(true); err != nil {
				return s.answer(err)
			}
			if err := s.askRigor(); err != nil {
				return s.answer(err)
			}
			if err := s.askBudget(); err != nil {
				return s.answer(err)
			}
			// settleBudget follows the question here for the reason it follows it
			// on a first run: a budget is an alert on a billing account, and this
			// run links none (the pause is past), so an amount with no account is
			// dropped with the one line that says what adds it rather than
			// reaching the installation's Terraform, whose own rule refuses a
			// budget without an account.
			return s.answer(s.settleBudget())
		}
		// A project config is selected: this run's inputs are already settled
		// and nothing is asked. Nothing is created either.
		//
		// --rigor is refused here rather than ignored (task W): the mode of a
		// project that already exists is the project's own, and the one command
		// that changes it is `fugaro rigor set`.
		// --location is refused first, and for the same reason (LP4's review):
		// a run that selects a project config creates nothing either, and the
		// answer would be materialized into the place's default region and then
		// meet the region-move guard — which names a --region the caller never
		// typed — or be dropped in silence where the two agree. It comes before
		// --rigor below because the place is the question the wizard reaches
		// first (setupQuestions).
		if err := setupLocationRefusal(o.location); err != nil {
			return err
		}
		if err := setupRigorRefusal(o.rigor); err != nil {
			return err
		}
		return r.settleCreation(r.selectedPending(ctx))
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
		v, err := r.promptChoice(valueSpec{what: q.what, flag: q.flag}, def, alt, why, func(v string) error {
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
	if o.cloud.region == "" && o.location == "" {
		// The place question (LP4, D3080, D3081): where the installation
		// lives, with US as the suggestion. It is the wizard's region question
		// in a person's words rather than a second question (F2) — the answer
		// may be a place or a region typed in full — and it is asked on the
		// run that creates the installation only: `fugaro update` on a machine
		// with no config asks for the region itself, because the installation
		// it is looking for has one already (setupCreates).
		if o.setupCreates(true) {
			q := questionFor("--location")
			s.a.asked = append(s.a.asked, q.label)
			v, err := r.prompt(valueSpec{what: q.what, flag: q.flag}, string(place.US), func(v string) error {
				_, _, verr := placeAnswer(v)
				return verr
			})
			if err != nil {
				return s.answer(err)
			}
			p, region, err := placeAnswer(v)
			if err != nil {
				// The prompt validated the same value one line above, so this
				// cannot be reached; the region is set only from an answer
				// that read back.
				return err
			}
			// A region typed in full settles the answer by itself, and the
			// place is derived from it wherever it is read (D3080) — so only
			// a place is recorded as the place, and a region is recorded as
			// the region. Neither has a second copy to drift from.
			if region != "" {
				o.cloud.region = region
			} else {
				o.location = string(p)
			}
		} else {
			q := questionFor("--region")
			s.a.asked = append(s.a.asked, q.label)
			v, err := r.prompt(valueSpec{what: q.what, flag: q.flag}, defaultRegion, func(v string) error {
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
	}
	if err := o.settleLocation(); err != nil {
		return s.answer(err)
	}
	// An installation may already be there (one this machine has no project
	// config for: the owner's second machine, or a teammate's). `setup` adopts
	// only a project that holds nothing of Fugaro's, so it refuses an ID that
	// holds an installation before the name is asked and says
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
	// The project: a first run of `setup` makes it Fugaro's (settleCreation),
	// creating it or adopting one that is already there and holds nothing of
	// Fugaro's. There is no question to ask.
	if err := r.settleCreation(true); err != nil {
		return s.answer(err)
	}
	if s.name == "" && !o.configOnly {
		q := questionFor("--name")
		def, label := suggestName(ctx), q.what+" (permanent"
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
		v, err := r.prompt(valueSpec{what: label, flag: q.flag}, def, func(v string) error {
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
	// The rigor question (task W; D900) is asked after the name, because the
	// mode is about the project the name settles, and before the bake one: the
	// run that asked it writes the answer in the same run (the Layer stage),
	// and a question this phase cannot answer stops here like any other.
	if err := s.askRigor(); err != nil {
		return s.answer(err)
	}
	// The budget question (T2b of docs/managed-flag-ergonomics; D2210) is asked
	// after the rigor one: it is about the project the answers above settled,
	// and it is asked only on a run that creates the installation, where a
	// project that is already there keeps the budget its own (askBudget). A run
	// that cannot ask takes the default here rather than stopping: the question
	// is a value with a default (D2212), which is what its own empty missing
	// says (setupQuestions).
	if err := s.askBudget(); err != nil {
		// The question's own no-answer stops the phase here like the two above
		// it, and the rerun resumes at this one (s.answer).
		return s.answer(err)
	}
	// The bake question (IS7, D1901) is asked after the name and before the
	// billing one: it is about the project the answers above settled rather
	// than about an input, and it is asked only on a run that creates the
	// installation — a run on an installation that is already there keeps the
	// answer the project's own and takes it from its flag or from the
	// published node, never from this machine (askBake, setup.go).
	if err := s.askBake(); err != nil {
		// The question's own no-answer stops the phase here, as the three above
		// do: the billing question below is not reached, and the rerun resumes
		// at this one (s.answer).
		return s.answer(err)
	}
	// Where each thing goes (R7): the one thing the run says about the
	// placement rather than asks — it is printed once the region is settled
	// and before anything is created, so a person reading it can still change
	// an answer or stop, and the question the placement is spelled in is the
	// region question above, never one of its own.
	if o.setupCreates(true) && o.cloud.region != "" {
		r.printWhereEachThingGoes()
	}
	// The billing question is the last of the wizard's questions (W4.3): it is
	// about the GCP project the answers above settled, and it is asked only
	// where the answer is one this run acts on (asksBilling, billing_prompt.go).
	// Its own "no answer came" is the phase's Left like any other question's:
	// the run resumes at it, and the refusal is the one line it has always
	// been.
	if err := r.askBilling(ctx); err != nil {
		return s.answer(err)
	}
	// The questions are all answered, and the one thing a question's answer
	// still needs is settled here: the account a budget's alert is on
	// (settleBudget). It is after the billing question because that is where
	// the account the run links appears, and it is not reached where the run
	// stopped at that question.
	return s.settleBudget()
}

// budgetNoAccount is the line a run prints when it has a budget and no billing
// account for the alert to sit on: what happens to the amount, and what adds
// the alert later. It is one line and asks nothing (settleBudget).
const budgetNoAccount = "no billing account is linked, so this run creates no budget alert: the amount it has (%d) is not written into the installation's variables. A budget is an alert on a billing account — add it later with --budget, --budget-currency and --billing-account together, on a run whose project has an account"

// settleBudget is what the phase does with the budget its question answered,
// once the billing question has answered too: a budget is an alert on a
// billing account, so the account the run links is the one the alert is on
// (D2216) — and a run that links none carries no budget at all, because the
// alternative is an installation the Terraform validation refuses (its budget
// object needs a billing account ID,
// deploy/terraform/gcp/modules/installation/variables.tf) or an alert on an
// account nobody chose.
//
// T2a of docs/managed-flag-ergonomics owns this rule at the run's own door
// (D2216's other half: the amount recorded in the creation fact, and the one
// command that adds the alert printed). This is the smaller thing the phase
// owes the answer it collected: the door's own check has already run by the
// time these questions are asked (init.go's trio check runs before the
// wizard), so an answer collected here never meets it, and the invariant it
// states — a budget takes an amount, a currency and an account together — is
// kept here for the answer instead of refused later by a cloud step.
//
// It says nothing where nobody can answer this run (canAsk: --json,
// --print-vars, --yes, --non-interactive, a pipe): a run nobody answers writes
// nothing, and the line is a transcript's.
func (s *wizardStage) settleBudget() error {
	r, o := s.r, s.r.o
	if o.budget <= 0 && o.budgetCurrency == "" {
		return nil
	}
	if o.billingAccount == "" {
		o.billingAccount = o.linkBilling
	}
	if o.billingAccount != "" {
		return nil
	}
	// A run nobody answers asks nothing and writes nothing (T7,
	// docs/no-prompt-policy), and a run whose stdout is a document (--json,
	// --print-vars) is one of those here: the line below is a transcript's, and
	// the fact is in the run's own result either way. The amount a person
	// answered on a run that paused at billing is the one case this leaves
	// silent, and it is the door's to say (D2216, T2a).
	if r.canAsk() {
		fmt.Fprintf(r.w, budgetNoAccount+"\n", o.budget)
	}
	o.budget, o.budgetCurrency = 0, ""
	return nil
}

// whereEachThingGoesIntro is the line above the mapping: what the block is,
// and the one thing a reader has to know about it — there is nothing to answer
// here, because the placement is the region answer they have already given
// (R7).
const whereEachThingGoesIntro = "where each thing goes (the region above decides it; the fallbacks are stated under their own line, and there is nothing to answer here):"

// printWhereEachThingGoes is R7: where each thing the installation creates
// will go, one line per row of the mapping, with every fallback's reason under
// the line it belongs to — before anything is created, and asking nothing.
//
// It says the region itself for the things that follow it (the compute, the
// registry, the buckets and the builds), the run history's Firestore database,
// the budget's Realtime Database, the secret containers and the run log
// bucket, in the mapping's own words (place.For), so a fallback cannot be
// described here as anything other than what it is. A region in neither place
// (London, Zürich, anything but us-* and europe-*) is said to carry no promise,
// which is what D3081 promises it: nothing is refused, and everything it falls
// back to is on a line above.
//
// The scheduler is not one of the things that follow the region, and so is not
// named among them: its own region is gcp.SchedulerRegion's answer, which for
// the US default is us-east4 rather than us-east5 (F3), and R7's mapping has no
// row for it to be read from. Saying nothing about it is better than saying
// where it goes in a line that is the region's own.
func (r *initRun) printWhereEachThingGoes() {
	m := place.For(r.o.cloud.region)
	fmt.Fprintf(r.w, "%s%s\n", r.o.programPrefix(), whereEachThingGoesIntro)
	fmt.Fprintf(r.w, "  the installation's own region: %s (the jobs, the image registry and the runs bucket)\n", m.Region)
	rows := []struct {
		what string
		loc  place.Location
	}{
		{"the run history (Firestore)", m.Firestore},
		{"the budget's database (the Realtime Database)", m.RealtimeDatabase},
		{"the secret containers", m.Secrets},
		{"the run log bucket", m.LogBucket},
	}
	for _, row := range rows {
		fmt.Fprintf(r.w, "  %s: %s\n", row.what, row.loc.Where)
		if row.loc.Reason != "" {
			fmt.Fprintf(r.w, "    %s\n", row.loc.Reason)
		}
	}
	if m.Place == place.None {
		fmt.Fprintf(r.w, "  %s is in neither US nor EU: it carries no promise, so every fallback above is stated rather than assumed\n", m.Region)
	}
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
