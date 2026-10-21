package cli

import (
	"bufio"
	"context"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// setup_refusals_test.go pins the refusal matrix both front doors must pass
// (docs/setup-wizard/implementation-plan.md, Task W0.1; the reconciliation's
// F7, F8 and D12). It is a pin, not a red test: it passes on today's code by
// design, and the mutation that checks it is a row relaxed locally — a noYes
// taken off a slot, a runCovered entry added, a Typed step made confirmable by
// --yes — which fails this file.
//
// The rows are the stages internal/cli/init_stages.go's converge() registers,
// and the four things inside them that are money or permanent: project
// creation, the billing link, the Firestore location and a billable first
// image build. The columns are the plan's five run modes — --yes,
// --non-interactive with and without --yes, --json, no terminal and a coding
// agent's session — with the --yes variants the seed
// (internal/initflow/loop_test.go's TestYesDoesNotCoverCreateOrBilling) and
// the loop's own mode table carry, plus the one control column: a person at a
// real terminal, the only mode that may confirm a Typed step. Without the
// control a matrix of refusals cannot tell "refused" from "never ran".
//
// No task may relax a row of it (the plan's Global Constraint 4). W5.1 adds
// the second front door's column, where the owner's D12 answer covers more in
// `update`; nothing here changes for `setup`.

// cliStage is one stage initStages() registers: the constructor it is built
// with, the flag it is behind, and the three facts the loop decides about it —
// noYes (--yes never confirms the stage), runCovered (the run's one
// confirmation covers its ordinary steps) and self (the stage keeps its own
// confirmations: initflow.SelfConfirming, which is what lets the loop run it
// with no terminal at all and refuse only where the stage's own apply says so).
//
// Every column is checked against the product and not only recorded: build is
// the constructor itself, called on a real engine and compared by type with
// the stage initStages() put in the list, and behind is checked by building
// the list twice, with and without the flag. ctor is the name initStages()
// has to use, matched inside that function's body.
type cliStage struct {
	stage  string
	ctor   string // the marker initStages()' body must contain: the constructor call, or the literal for the one stage built without one
	build  func(*initEngine) initflow.Stage
	behind string // the flag the stage is registered under, "" for always
	// firstRun is the second way a stage is registered beside behind: the
	// stage `setup` has on a first run whatever the flags say (W1.3, D11 —
	// the wizard's first question is the project, so the stage that acts on
	// the answer is registered with the question rather than with a flag).
	// False for every row but the project stage. It is checked the way behind
	// is — by building the list — so an arm that stops registering it, and a
	// stage that starts being registered on a first run, both fail here.
	firstRun bool
	noYes    bool
	covered  bool
	self     bool
}

// cliStages is the pin: a slot that loses its noYes, a runCovered entry that
// appears or goes, a stage that becomes SelfConfirming (or stops being), a
// stage dropped from initStages()' list, or a flag that stops gating one,
// fails TestRefusalMatrixListsEveryStageTheCLIRegisters.
var cliStages = []cliStage{
	{stage: initflow.Preflight, ctor: "&preflightStage{",
		build: func(e *initEngine) initflow.Stage { return &preflightStage{e} }},
	{stage: initflow.Installation, ctor: "newInstallationStage(", covered: true, self: true,
		build: func(e *initEngine) initflow.Stage { return newInstallationStage(e) }},
	{stage: initflow.Firebase, ctor: "newFirebaseStage(", covered: true, self: true,
		build: func(e *initEngine) initflow.Stage { return newFirebaseStage(e) }},
	{stage: initflow.Images, ctor: "newImagesStage(", covered: true, self: true,
		build: func(e *initEngine) initflow.Stage { return newImagesStage(e) }},
	{stage: initflow.Installation2, ctor: "newInstallation2Stage(", covered: true, self: true,
		build: func(e *initEngine) initflow.Stage { return newInstallation2Stage(e) }},
	{stage: initflow.Secrets, ctor: "newSecretsStage(", noYes: true,
		build: func(e *initEngine) initflow.Stage { return newSecretsStage(e) }},
	{stage: initflow.Plugin, ctor: "newPluginStage(",
		build: func(e *initEngine) initflow.Stage { return newPluginStage(e) }},
	{stage: initflow.Repository, ctor: "newRepositoryStage(", covered: true, self: true,
		build: func(e *initEngine) initflow.Stage { return newRepositoryStage(e) }},
	{stage: initflow.Project, ctor: "newProjectStage(", behind: "--create-project", firstRun: true, noYes: true,
		build: func(e *initEngine) initflow.Stage { return newProjectStage(e) }},
	{stage: initflow.Compute, ctor: "newComputeStage(", behind: "--provision-batch", noYes: true,
		build: func(e *initEngine) initflow.Stage { return newComputeStage(e) }},
}

// prodEngine is an engine with nothing but the flags: the stage constructors
// read the run's options and nothing else, so initStages() builds the real
// list from it without a terminal, a config or a cloud.
func prodEngine(o initOptions) *initEngine {
	return &initEngine{r: &initRun{o: &o}}
}

// flagOptions is the run the flag in a row's behind turns on: the two flags
// the CLI registers a stage behind, named as the flag the row records. ok is
// false for a name that turns nothing on, which is a row whose behind is not a
// flag at all.
func flagOptions(behind string) (o initOptions, ok bool) {
	switch behind {
	case "--create-project":
		return initOptions{createProject: true}, true
	case "--provision-batch":
		return initOptions{provisionBatch: true}, true
	}
	return initOptions{}, false
}

func namesOf(stages []initflow.Stage) []string {
	out := make([]string, len(stages))
	for i, s := range stages {
		out[i] = s.Name()
	}
	return out
}

// stageNamed is the stage the list has under a name.
func stageNamed(stages []initflow.Stage, name string) (initflow.Stage, bool) {
	for _, s := range stages {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}

// initStagesBody is the text of initStages in init_stages.go: the one region a
// row's constructor has to be named in. A whole-file search would pass on a
// constructor that appears anywhere else in the file — and would still pass
// with the stage dropped from the list, which is the mistake the list check
// above is for.
func initStagesBody(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("init_stages.go")
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(src), "\nfunc initStages(")
	if i < 0 {
		t.Fatalf("init_stages.go has no func initStages(: the rows of this table are its registration, and converge() must run what it returns")
	}
	body := string(src)[i+1:]
	if j := strings.Index(body, "\n}\n"); j >= 0 {
		body = body[:j+2]
	}
	return body
}

// TestRefusalMatrixListsEveryStageTheCLIRegisters crosses the table above with
// the product and with the loop's own declarations, in both directions: the
// list initStages() builds — with and without the two flags — has to be the
// table, each stage has to be the type its row's constructor makes and has to
// be SelfConfirming exactly where the row says, and a canonical stage with no
// row fails.
// TestRefusalMatrixListsEveryStageTheCLIRegisters crosses the table above with
// the product and with the loop's own declarations, in both directions: the
// list initStages() builds — with no flags, and with both flags on — has to be
// the table, each stage has to be the type its row's constructor makes and has
// to be SelfConfirming exactly where the row says, and a canonical stage with
// no row fails.
//
// It reads the list by calling initStages(), the function converge() runs, and
// not a copy of it: a stage dropped from the list is a row the list does not
// have, and a stage added to it is a stage no row records.
func TestRefusalMatrixListsEveryStageTheCLIRegisters(t *testing.T) {
	// The table itself: no stage twice, and the flag each row names is a flag.
	registered := map[string]bool{}
	for _, s := range cliStages {
		if registered[s.stage] {
			t.Errorf("%s is listed twice", s.stage)
		}
		registered[s.stage] = true
		if s.behind != "" {
			if _, ok := flagOptions(s.behind); !ok {
				t.Errorf("%s: the table puts it behind %q, which turns nothing on", s.stage, s.behind)
			}
		}
	}

	plain := initStages(prodEngine(initOptions{}))
	all := initStages(prodEngine(initOptions{createProject: true, provisionBatch: true}))

	// The set of stages, both directions, and the flag each is behind: a
	// stage behind a flag is not in the unflagged list, and every other row is.
	plainNames, allNames := setOf(namesOf(plain)), setOf(namesOf(all))
	for _, s := range cliStages {
		if !allNames[s.stage] {
			t.Errorf("%s: a row, and initStages() does not register it: the row and the list have to be the same stages", s.stage)
		}
		if plainNames[s.stage] != (s.behind == "") {
			t.Errorf("%s: in the unflagged list %v, and the table puts it behind %q: a stage behind a flag is registered under that flag and nowhere else", s.stage, plainNames[s.stage], s.behind)
		}
	}
	for _, n := range append(namesOf(plain), namesOf(all)...) {
		if !registered[n] {
			t.Errorf("%s: initStages() registers it and no row records it: add the row, with the flag it is registered under", n)
		}
	}

	// And the stage set each door has, the third way a row can be registered:
	// `setup` on a first run — the door the wizard is, no project config on
	// this machine — has the project stage whether or not --create-project was
	// given, and `update`, and a `setup` whose config exists, never have it.
	// The answer to the wizard's first question is what the stage acts on.
	firstNames := setOf(namesOf(initStages(&initEngine{r: &initRun{o: &initOptions{door: setupDoor.name}}})))
	configured := &initEngine{r: &initRun{o: &initOptions{door: setupDoor.name}}, old: []byte("fugaro.yaml\n")}
	for _, s := range cliStages {
		want := s.behind == "" || s.firstRun
		if got := firstNames[s.stage]; got != want {
			t.Errorf("%s: in setup's first-run list %v, want %v (behind %q, firstRun %v)", s.stage, got, want, s.behind, s.firstRun)
		}
		if s.firstRun && firstNames[s.stage] {
			// The two runs the wizard does not create in: it converges as
			// `fugaro update` does, and update has no project stage.
			upNames := setOf(namesOf(initStages(&initEngine{r: &initRun{o: &initOptions{door: updateDoor.name}}})))
			if upNames[s.stage] {
				t.Errorf("%s: initStages() registers it for the update door, which creates nothing", s.stage)
			}
			if setOf(namesOf(initStages(configured)))[s.stage] {
				t.Errorf("%s: initStages() registers it for a setup whose project config already exists", s.stage)
			}
		}
	}

	// The canonical stages: every one but the slot the CLI never fills, in the
	// loop's own order.
	if sorted, err := initflow.Validate(all); err != nil {
		t.Errorf("the stages initStages() registers are not a run the loop accepts: %v", err)
	} else if got, want := namesOf(sorted), withoutServices(initflow.Names()); !slices.Equal(got, want) {
		t.Errorf("the loop orders the registered stages as %v, want %v: the CLI registers every canonical stage but %s", got, want, initflow.Services)
	}
	// Each row's type and its SelfConfirming, against the constructor's own
	// value. The stage value is the one in the list; the row's build makes the
	// comparison, so a row cannot claim a constructor it does not use.
	body := initStagesBody(t)
	for _, s := range cliStages {
		if !strings.Contains(body, s.ctor) {
			t.Errorf("initStages() does not contain %q, the marker the table gives for %s: the constructor a row names has to be the one the registration uses", s.ctor, s.stage)
		}
		p, ok := stageNamed(plain, s.stage)
		if !ok {
			p, ok = stageNamed(all, s.stage)
		}
		if !ok {
			continue // reported above
		}
		if got, want := reflect.TypeOf(p), reflect.TypeOf(s.build(prodEngine(initOptions{}))); got != want {
			t.Errorf("%s: initStages() registers a %v, and the table's constructor makes a %v", s.stage, got, want)
		}
		if _, isSelf := p.(initflow.SelfConfirming); isSelf != s.self {
			t.Errorf("%s: SelfConfirming %v, and the table says %v: it is what lets the loop run a stage with no terminal at all, so a stage that gains or loses it changes the refusal matrix", s.stage, isSelf, s.self)
		}
	}

	// And the four typed steps sit inside stages the CLI registers, in stages
	// whose SelfConfirming is the table's.
	for _, s := range typedSteps() {
		row, ok := rowFor(s.stage)
		if !ok {
			t.Errorf("%s: %s is not a stage the CLI registers", s.what, s.stage)
			continue
		}
		if row.self != s.self {
			t.Errorf("%s: the row says %s is SelfConfirming %v, and the step's row says %v", s.what, s.stage, row.self, s.self)
		}
	}

	// The slot the CLI leaves empty is still not one --yes covers.
	if !initflow.YesCovers(initflow.Services) || initflow.RunCovers(initflow.Services) {
		t.Errorf("services: YesCovers %v, RunCovers %v; want true, false", initflow.YesCovers(initflow.Services), initflow.RunCovers(initflow.Services))
	}

	// The loop's own declarations, per row.
	for _, s := range cliStages {
		if !slices.Contains(initflow.Names(), s.stage) {
			t.Errorf("%s is not a stage the loop knows", s.stage)
		}
		if got := initflow.YesCovers(s.stage); got != !s.noYes {
			t.Errorf("YesCovers(%s) = %v, and the table says noYes = %v: a slot's noYes is what --yes never reaches", s.stage, got, s.noYes)
		}
		if got := initflow.RunCovers(s.stage); got != s.covered {
			t.Errorf("RunCovers(%s) = %v, and the table says covered = %v: the run's one confirmation covers runCovered's stages and nothing a stage declares noYes", s.stage, got, s.covered)
		}
		if s.covered && !initflow.YesCovers(s.stage) {
			t.Errorf("%s: the run's one confirmation covers a stage --yes does not", s.stage)
		}
	}
}

// rowFor is the table's row for a stage.
func rowFor(stage string) (cliStage, bool) {
	for _, s := range cliStages {
		if s.stage == stage {
			return s, true
		}
	}
	return cliStage{}, false
}

func setOf(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// withoutServices is the canonical stage names but the one slot the CLI never
// fills (converge() has no services stage, and W5.1's update list is built the
// same way).
func withoutServices(names []string) []string {
	return slices.DeleteFunc(slices.Clone(names), func(n string) bool { return n == initflow.Services })
}

// typedStep is one thing only a person may confirm, and the stage that owns
// it: the stage's own left-for-you, and whether that stage is SelfConfirming
// (the loop lets such a stage run without a terminal, and refuses any other
// before its apply).
type typedStep struct {
	what  string
	stage string
	left  func(t *testing.T) initflow.Left
	// self is whether the owning stage is SelfConfirming: the loop lets such a
	// stage run without a terminal (its own apply refuses), and refuses any
	// other before its apply.
	self bool
	// yesCovers is whether --yes may confirm the stage: false for the two
	// inside initflow.Project, which noYes declares, and true for the two
	// inside a covered stage, whose own step stays Typed. It is written down
	// here and not read off initflow.YesCovers, so that taking a noYes away
	// fails the matrix and not only the table.
	yesCovers bool
}

// typedSteps is the plan's list. Two of the four are steps inside a stage the
// run's one confirmation does cover — runCovered names Firebase and Repository
// — and that is the plan's own description of them (D12: "within those stages
// the Firestore database's location, a Cloud Build ... stay Typed"): what is
// never covered is the step, not the stage. The other two are inside
// initflow.Project, which noYes declares and --yes therefore never reaches at
// all. The left-for-you is the real one wherever the CLI builds it in one
// place, so a change to the prompt moves here too.
func typedSteps() []typedStep {
	return []typedStep{
		{what: "project creation", stage: initflow.Project, left: projectCreateLeft},
		{what: "the billing link", stage: initflow.Project, left: projectLinkLeft},
		{what: "the Firestore location", stage: initflow.Firebase, left: firestoreLocationLeft, self: true, yesCovers: true},
		{what: "a billable first image build", stage: initflow.Repository, left: billableBuildPrompt, self: true, yesCovers: true},
	}
}

// projectCreateLeft is the project stage's own left-for-you for a creation: the
// real Left, from the stage's own branch, with nothing read.
func projectCreateLeft(t *testing.T) initflow.Left {
	t.Helper()
	s := &projectStage{e: &initEngine{r: &initRun{o: &initOptions{cloud: cloudOptions{gcpProject: initProject}}}}, id: initProject}
	s.create = true
	return s.Left()
}

// projectLinkLeft is the same for the billing link: the second of the project
// stage's three typed banners, whose own left-for-you is a prompt as well.
func projectLinkLeft(t *testing.T) initflow.Left {
	t.Helper()
	o := &initOptions{cloud: cloudOptions{gcpProject: initProject}, linkBilling: "ACCOUNT"}
	s := &projectStage{e: &initEngine{r: &initRun{o: o}}, id: initProject}
	s.link = true
	return s.Left()
}

// firestoreLocationLeft is the Firestore location's own refusal, from the
// engine that owns it: confirmLocation at a run that cannot type it.
func firestoreLocationLeft(t *testing.T) initflow.Left {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return false }
	t.Cleanup(func() { stdinIsTerminal = old })
	canConfirmTyped = initflow.CanConfirm
	r := &initRun{o: &initOptions{firebase: "aurora-fp"}, w: io.Discard, in: bufio.NewReader(strings.NewReader(""))}
	err := r.confirmLocation()
	var ny *initflow.NeedsYouError
	if !asNeedsYou(err, &ny) {
		t.Fatalf("the Firestore location's refusal is %v, want needs-you", err)
	}
	return ny.Left
}

// billableBuildPrompt is the left-for-you the repository stage leaves for a
// billable first image build: init.go's own billableBuildLeft, the one function
// repoEngine builds that step's left with, called the way the converge calls it
// (embedded: the repository stage is the run's own, not fugaro update --repo). It
// is not a copy of the prompt here, so a change to the prompt — its kind
// included, which is the thing this matrix is about — moves this row with it.
func billableBuildPrompt(*testing.T) initflow.Left {
	return billableBuildLeft(true, "setup")
}

// TestTheFourTypedStepsLeaveAPrompt is the other half of the plan's sentence:
// each of the four answers needs-you with a LeftKind of prompt — the person is
// told to type the confirmation in their own terminal, and the left-for-you is
// a prompt, not a command to paste.
func TestTheFourTypedStepsLeaveAPrompt(t *testing.T) {
	for _, s := range typedSteps() {
		if lf := s.left(t); lf.Kind != initflow.LeftPrompt || lf.Stage != s.stage {
			t.Errorf("%s: %+v, want a prompt on %s", s.what, lf, s.stage)
		}
	}
}

// TestRefusalMatrixTheTypedRuleRefusesEveryMode pins the rule itself and not
// only the state the loop reaches: initflow.CanConfirm(Typed, ...) — what
// init's askTyped and the project stage's canType consult — is false in every
// mode but the control. It is the same columns as the matrix, so a branch that
// lets --yes, --json or a coding agent's marker give a Typed confirmation fails
// here even where the loop's own path would have refused the stage first.
func TestRefusalMatrixTheTypedRuleRefusesEveryMode(t *testing.T) {
	for _, m := range refusalModes() {
		t.Run(m.name, func(t *testing.T) {
			run, _, _ := m.condition.run(t, "")
			c := run.conditions()
			if got := canConfirmTyped(initflow.Typed, c); got != m.control {
				t.Errorf("CanConfirm(Typed, %+v) = %v, want %v: the step is money or permanent, and only a person at a real terminal confirms it", c, got, m.control)
			}
		})
	}
}

// refusalMode is one column of the matrix: the CLI's own reading of a run's
// flags and environment (init_confirm_test.go's condition harness), whether
// the loop turns it into a plan-only run (--non-interactive without --yes),
// and whether it is the control.
type refusalMode struct {
	condition
	planOnly bool
	control  bool
}

// refusalModes are the plan's five modes, with the --yes variants the seed and
// the loop's own mode table carry, and the control. The wider table of
// conditions init_confirm_test.go drives (--yes through a pipe, and the rest of
// the product) is covered there by its own TestTypedOnly... matrices.
func refusalModes() []refusalMode {
	return []refusalMode{
		{condition: condition{"--yes at a terminal", initOptions{yes: true}, true, ""}},
		{condition: condition{"--non-interactive --yes", initOptions{yes: true, nonInteractive: true}, true, ""}},
		{condition: condition{"--non-interactive", initOptions{nonInteractive: true}, true, ""}, planOnly: true},
		{condition: condition{"--json at a terminal", initOptions{asJSON: true}, true, ""}},
		{condition: condition{"--yes --json at a terminal", initOptions{yes: true, asJSON: true}, true, ""}},
		{condition: condition{"no terminal", initOptions{}, false, ""}},
		{condition: condition{"no terminal --yes", initOptions{yes: true}, false, ""}},
		{condition: condition{"a coding agent's session", initOptions{}, true, "CLAUDECODE"}},
		{condition: condition{"a coding agent's session --yes", initOptions{yes: true}, true, "CURSOR_AGENT"}},
		{condition: condition{"a person at a terminal (the control)", initOptions{}, true, ""}, control: true},
	}
}

// loopOptions is the loop's Options for this mode, built from the CLI's own
// reading of the run (initRun.conditions, init.go) rather than from the flags
// again, so a change to conditions() shows up here. Yes is the flag and not the
// derived field: the two agree in every mode of this table, because a run that
// only plans is the loop's own decision for --non-interactive without --yes.
func (m refusalMode) loopOptions(c initflow.Conditions) initflow.Options {
	return initflow.Options{
		Yes: m.opts.yes, NonInteractive: c.NonInteractive, JSON: c.JSON,
		Terminal: c.Terminal, Agent: c.Agent, Out: io.Discard,
	}
}

// runRefusal runs the step's stage through the loop in this mode, on the
// harness init_confirm_test.go already has: a terminal swapped in, the agent's
// marker set, and canConfirmTyped the real rule.
func (m refusalMode) runRefusal(t *testing.T, s typedStep) (*initflow.Result, error, *refusalStage) {
	t.Helper()
	run, _, _ := m.condition.run(t, "")
	conds := run.conditions()
	st := &refusalStage{step: s, left: s.left(t), conds: conds}
	var stage initflow.Stage = st
	if s.self {
		stage = &refusalEngineStage{refusalStage: st}
	}
	res, err := initflow.Run(context.Background(), []initflow.Stage{stage}, m.loopOptions(conds))
	return res, err, st
}

// refusalStage is one of the CLI's stages with no cloud behind it: it plans
// when applied (as every engine stage does) and its apply is the step the row
// names. The apply consults the real rule — initflow.CanConfirm(Typed, ...),
// which is what init's askTyped (init.go) and the project stage's canType
// (init_project.go) consult — so a run that cannot type the confirmation leaves
// the step, exactly as the engines do, and nothing else about the stage
// decides.
type refusalStage struct {
	step  typedStep
	left  initflow.Left
	conds initflow.Conditions

	applied bool
	gotEnv  *initflow.Env
}

func (s *refusalStage) Name() string { return s.step.stage }
func (s *refusalStage) Check(context.Context) (initflow.Status, error) {
	return initflow.Status{State: initflow.Todo, Detail: "plans when applied"}, nil
}
func (s *refusalStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	return initflow.Plan{Detail: "will do " + s.step.what}, nil
}
func (s *refusalStage) Apply(_ context.Context, env initflow.Env) (initflow.Outcome, error) {
	s.gotEnv = &env
	if !canConfirmTyped(initflow.Typed, s.conds) {
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.left}
	}
	s.applied = true
	return initflow.Outcome{Changed: true, Detail: "applied"}, nil
}
func (s *refusalStage) Verify(context.Context) error { return nil }
func (s *refusalStage) Left() initflow.Left          { return s.left }

// refusalEngineStage is the same stage as one the loop lets run without a
// terminal (engineStage, imagesStage): the refusal is then the stage's own, and
// not the loop's.
type refusalEngineStage struct{ *refusalStage }

func (*refusalEngineStage) SelfConfirming() {}

// TestRefusalMatrixNeverConfirmsWhatIsMoneyOrPermanent is the matrix itself:
// for each of the four and each mode, the state the loop reaches, whether the
// step was applied, and whether --yes reached a stage it never covers.
func TestRefusalMatrixNeverConfirmsWhatIsMoneyOrPermanent(t *testing.T) {
	for _, s := range typedSteps() {
		for _, m := range refusalModes() {
			t.Run(s.what+"/"+m.name, func(t *testing.T) {
				res, err, st := m.runRefusal(t, s)
				if st.applied != m.control {
					t.Fatalf("applied = %v, want %v: %s is money or permanent, and only a person typing the confirmation at a real terminal gives it (err %v, result %+v)", st.applied, m.control, s.what, err, res)
				}
				if got := initflow.YesCovers(s.stage); got != s.yesCovers {
					t.Errorf("YesCovers(%s) = %v, and the row says %v: --yes reaches the stage the plan's list says it must not", s.stage, got, s.yesCovers)
				}
				if st.gotEnv != nil && st.gotEnv.Yes != (m.opts.yes && s.yesCovers) {
					t.Errorf("%s: the loop handed the stage --yes = %v, want %v (--yes %v, the stage covered %v)", s.what, st.gotEnv.Yes, m.opts.yes && s.yesCovers, m.opts.yes, s.yesCovers)
				}
				switch {
				case m.control:
					if err != nil || res == nil || res.ExitCode() != 0 || stateOf(res, s.stage) != initflow.Changed {
						t.Fatalf("the control did not confirm the step: err %v, result %+v", err, res)
					}
				case m.planOnly:
					if err != nil || res == nil || res.ExitCode() != 0 || stateOf(res, s.stage) != initflow.Todo {
						t.Fatalf("--non-interactive without --yes did not only plan: err %v, result %+v", err, res)
					}
				case err != nil:
					// The loop refused before the stage's apply: a covered
					// stage that is not SelfConfirming, with no terminal and no
					// --yes, is not let prompt at all.
					if !strings.Contains(err.Error(), "needs a real terminal") {
						t.Fatalf("%s: %v, want the loop's no-terminal refusal", m.name, err)
					}
				default:
					if err != nil {
						t.Fatalf("%s: %v", m.name, err)
					}
					if stateOf(res, s.stage) != initflow.NeedsYou || res.ExitCode() != 1 {
						t.Fatalf("%s: state %q, exit %d, want needs-you and 1 (result %+v)", m.name, stateOf(res, s.stage), res.ExitCode(), res)
					}
					lf, ok := leftOf(res, s.stage)
					if !ok || lf.Kind != initflow.LeftPrompt {
						t.Fatalf("%s: left-for-you %+v (found %v), want a prompt on %s", m.name, lf, ok, s.stage)
					}
				}
				if m.agent == "" {
					return
				}
				// The refusal an agent reads offers it no --yes: not the
				// stage's own text, and not the loop's.
				all := refusalMatrixText(res)
				if err != nil {
					all += err.Error()
				}
				if strings.Contains(all, "--yes") {
					t.Errorf("an agent session was told to pass --yes:\n%s", all)
				}
				if !strings.Contains(all, m.agent) {
					t.Errorf("the refusal does not name the marker %s:\n%s", m.agent, all)
				}
			})
		}
	}
}

func leftOf(res *initflow.Result, stage string) (initflow.Left, bool) {
	if res == nil {
		return initflow.Left{}, false
	}
	for _, lf := range res.Left {
		if lf.Stage == stage {
			return lf, true
		}
	}
	return initflow.Left{}, false
}

// refusalMatrixText is everything a run said about a refusal: each stage's
// detail and each left-for-you, text and commands.
func refusalMatrixText(res *initflow.Result) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, s := range res.Stages {
		b.WriteString(s.Detail + "\n")
	}
	for _, lf := range res.Left {
		b.WriteString(lf.Text + "\n" + strings.Join(lf.Commands, "\n") + "\n")
	}
	if res.Failed != nil {
		b.WriteString(res.Failed.Error + "\n" + res.Failed.Fix + "\n")
	}
	return b.String()
}
