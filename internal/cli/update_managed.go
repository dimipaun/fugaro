package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// update_managed.go is `fugaro update` under the managed gate (M1, D1016):
// the door the HARD RULE makes the whole upgrade, which is the door the agent
// must be able to run, and the largest surface in the managed set.
//
// What is here and what is not. The gate itself is internal/managed (D1021,
// M0): Converge, the class rule, the digest. The four things every command in
// the set makes around its own write are internal/cli/managed_gate.go (M6):
// read the two marks per command, ask the ownership question first, re-read
// the marks immediately before the write, and write the audit record for an
// admission and for a refusal alike. What is left — and all that is here — is
// this command's own document and this command's own rules: which stages the
// gate may open, which plan is the document of a step, and the class that
// step's confirmation is.
//
// Why the gate is asked at the STEP and not at the stage (D1022). A stage of
// this run is an engine, and an engine plans, shows, judges and applies one
// saved plan per step — the installation root is fused once and then again
// with the Firebase outputs. The judged document and the applied one have to
// be the same artifact, so the gate is consulted where the plan is in hand,
// between the show and the apply, and the digest it carries is of the bytes
// terraform printed for exactly that plan (tf.TF.ShowJSON). A gate at the
// stage would judge a plan the loop's own preview made and apply whatever the
// step then planned, which is the swap D1022 exists to make impossible.
//
// Why the loop has to know at all. In a coding agent's session nothing is
// applied (initflow.AgentRefusal), so a gate consulted only inside the engine
// would never be reached: the loop is the caller that decides whether a stage
// runs at all, and Options.ManagedGate is how this command tells it that the
// project is managed and this stage is one the gate judges. The stage's own
// steps then judge the documents, and a step the gate refuses comes back as a
// GatedError: the session reads what about the document the gate would not
// admit, in the gate's own sentence, and the step is the user's.
//
// What the gate can never do here. It can only narrow. A stage it does not
// open keeps today's refusal exactly — the secrets stage is never asked about
// (with a secret, the session has nothing the gate could read), and the
// Credentials and Compute stages are `noYes` in the stage table and are not
// gated (spec §4.2: they create an identity and provision money-bearing
// infrastructure, and neither is a plan the gate can bound). A step whose
// class is Typed is refused by the Converge itself, whatever the plan looked
// like (D1026), and a step the gate does not admit falls back to its own
// confirmation, which in a coding agent's session is the refusal it always
// was.

// gatedStages are the stages of this run the gate may open (D1016, spec §4.2):
// the Terraform plan stages, whose steps are the roots Fugaro embeds and whose
// document is a saved plan. Installation and Installation2 are the same root
// planned twice in one run (before and after the Firebase outputs exist) and
// are one entry each, because the gate judges the plan of the step in front of
// it and not the stage's name.
//
// The stages that are NOT here are as much a part of the decision:
// preflight (it changes nothing), images (it copies and pins images, and what
// bounds a build is the recorded build bound and not a plan), secrets (spec
// §3.2: never admitted, and an agent's session is where a secret would have to
// be typed), plugin (local files), the rules and policy stages (their
// documents are the binary's own rules generation and the published node, and
// they are M-tasks of their own), credentials and compute (`noYes` in the
// stage table today and under a managed project), the layer stage and the
// checkout's own steps.
var gatedStages = map[string]bool{
	initflow.Installation:  true,
	initflow.Firebase:      true,
	initflow.Installation2: true,
	initflow.Repository:    true,
}

// initGate is this run's use of the gate: M6's helper, and the one read of the
// two marks the whole run makes.
//
// One read and not one per stage, because the helper's own contract is one
// readManagedGate per command followed by an admit per judged document, and
// because the marks of a project that is being revoked mid-run are re-read
// there (D1024) — where the write is, which is the interval that matters. A
// gate nobody read is the zero value: not managed, which is every step's
// confirmation kept exactly as it is today.
type initGate struct {
	g managedGate
	// read is whether this run asked at all. It is what keeps a project that
	// is not managed — and a mark that could not be read — from being asked
	// about once per stage: the answer cannot change within a run's own read
	// of it, and the re-read before a write is the helper's, not this field's.
	read bool
	// managed is what the read answered: both marks there, which is the only
	// answer that lets a stage through to the loop.
	managed bool
}

// managedStage is initflow.Options.ManagedGate: whether the stage named runs
// under the gate in this session (D1016). The loop asks it only in a coding
// agent's session, and everything that is not a yes is today's refusal — an
// unreadable mark, a project that is not managed, a stage this door does not
// gate.
func (e *initEngine) managedStage(name string) bool {
	r := e.r
	if r.o.door != updateDoor.name || !gatedStages[name] {
		return false
	}
	if r.gate == nil {
		r.gate = &initGate{}
	}
	if !r.gate.read {
		r.gate = readUpdateGate(e, r)
	}
	return r.gate.managed
}

// readUpdateGate reads the two marks for this run, once, through M6's helper:
// the marks per command (D1015), and the ownership question before anything is
// judged (D1017).
//
// The budget database is left out of the environment (managedGateEnv.db): the
// question it asks is about the installation's own Realtime Database, and the
// steps this gate judges touch no database at all — the roots it plans are
// Terraform's and the stages that do reach the database (rules, policy) ask
// the question themselves, where the database is in hand, exactly as they do
// today. Every failure here is the zero gate: the marks could not be read, so
// this program cannot prove the project is managed, and an unproven project
// keeps every confirmation it has.
func readUpdateGate(e *initEngine, r *initRun) *initGate {
	g := &initGate{read: true}
	ctx := r.ctx()
	// The clients alone, and not setup's whole readiness: this read is made
	// BEFORE the run has decided to do anything, and a run whose project turns
	// out not to be managed must not have paid for the gate in any way a
	// reader could notice — the environment check prints the local config's
	// warnings, and an unmanaged installation has never printed them in a
	// coding agent's session. The clients themselves are the run's, kept for
	// the engines that follow.
	c := e.c
	if c == nil {
		var err error
		if c, err = newInitClients(ctx, e.lc); err != nil {
			return g
		}
		e.c = c
	}
	env := managedGateEnv{lc: e.lc, s: c.Storage, crm: c.CRM}
	facts := managedGateFacts{
		Subcommand: "update",
		// The session's own arguments as the process has them: what a reader
		// of the record sees is the command line that ran, and this command
		// takes no secret on one.
		Argv:        os.Args[1:],
		Declined:    "nothing was applied",
		AgentMarker: agentMarker(os.Getenv),
	}
	gate, err := readManagedGate(ctx, env, facts)
	if err != nil {
		return g
	}
	g.g, g.managed = gate, gate.managed
	return g
}

// gateUnderway is whether this run has the managed gate open, which is the one
// condition under which a step of this command writes in a coding agent's
// session at all.
//
// It is asked by the three steps whose write is NOT a saved plan: removing
// project Viewers' read access to the state bucket and to the runs bucket is a
// Storage IAM call of its own (infra.RemoveProjectViewers), so D1016's gate —
// a verdict about a document a session may be admitted to — has nothing to
// judge for it. A session that is admitted to the plan of a stage is therefore
// not admitted to the IAM call beside it, and the call waits for the owner,
// who makes it with their own run and the confirmation they always had. This
// is the gate narrowing and never widening: nothing this run does in a managed
// project is a write the gate did not read.
func (r *initRun) gateUnderway() bool { return r.gate != nil && r.gate.managed }

// gateNamesAnIAMCall is the one sentence such a step leaves behind: what is
// still there, why nothing removed it here, and the door the owner's own run
// goes through. It is a warning and not a stop, because the step it stands for
// is a removal of access that Fugaro's own plan does not carry, and a run that
// stopped here would leave the installation half-converged over an IAM call
// the owner's next run makes anyway.
func (r *initRun) gateNamesAnIAMCall(bucket string) string {
	return fmt.Sprintf("project Viewers can still read %s: removing their read access is an IAM call of its own and not a plan, so the gate a managed project admits a coding agent's session to has nothing to judge here and it was not made; run %s in your own terminal window to remove it", bucket, r.o.doorProgram())
}

// gateStep judges one Terraform step's saved plan, and answers whether the
// gate admitted it: true means the step writes under the gate, and false means
// it keeps its own guard and its own confirmation, which is what every step of
// every other run does.
//
// It is called between the show of the plan and the step's own apply, with the
// cover the step's confirmation would have used, so the gate's question and
// the review's are one question. A gated step does NOT run its ordinary guard
// or its ordinary confirmation: the gate is the guard (it runs tf.GuardEntries
// inside managed.Gate, with the owner's list and no session-supplied address)
// and the gate's verdict is the confirmation, which is the whole of D1016. A
// step it does not admit is not refused here — false sends it back to its own
// guard and confirmation, where a session that may not confirm anything is
// refused exactly as it was before this file existed.
//
// AllowDelete is empty and is not the session's. In a managed project the
// addresses a run may delete or replace come from the owner's own list, and
// that list approves deletes in the project's agent-authored root and never in
// Fugaro's own roots, which is what these four stages plan (D72/D74, and
// initManagedDeleteAdvice says the same to a person). So the gate feeds the
// guard no list at all: every removal in these roots is refused, whatever the
// session passed on its own command line, which is D1011's own rule that
// `allow-delete` is never admitted. An installation whose upgrade deletes
// something is the owner's to run — the gate can only narrow, and this is the
// narrowing.
func (r *initRun) gateStep(ctx context.Context, t *tf.TF, imports []tf.ImportKey, cover managedmode.Cover) (bool, error) {
	g := r.gate
	if g == nil || !g.managed {
		return false, nil
	}
	// --plan-only writes nothing, so there is nothing for the gate to admit:
	// the run keeps its own plan-only path, which applies nothing either way.
	if r.o.planOnly {
		return false, nil
	}
	doc, err := t.ShowJSON(ctx, infra.PlanFile)
	if err != nil {
		return true, &initflow.GatedError{Reason: gateUnreadable, Err: err}
	}
	verdict, err := managedmode.Gate(ctx, infra.PlanFile, managedmode.Spec{
		Project: r.projectName, GCPProject: r.gcpProject,
		Cover: cover, Imports: imports, TF: t,
	})
	if err != nil {
		// A plan terraform cannot read is never admitted (managed.Gate's own
		// rule, and M1's fail-closed shape): the step stops, and it stops in
		// the gate's words, because a session that read its own refusal here
		// would be told to hand the step to someone else rather than what
		// about the plan could not be read.
		return true, &initflow.GatedError{Reason: gateUnreadable, Err: err}
	}
	class := gateClass()
	conv := managedmode.ConvergeRefused(doc, class, refusedRules(verdict))
	if verdict.OK {
		conv = managedmode.ConvergeAdmitted(doc, class, admittedRules)
	}
	m, err := g.g.admit(ctx, class, conv)
	if err != nil {
		return true, &initflow.GatedError{Reason: err.Error(), Err: err}
	}
	// D55's one rule, asked with this run's conditions (a coding agent's
	// marker is set, which is the only way this code is reached) and the
	// verdict M6's helper just built: a gate that admitted this document
	// removes the session's refusal for this step, and anything else keeps it.
	if !initflow.CanConfirmManaged(class, r.conditions(), m) {
		return false, nil
	}
	fmt.Fprintf(r.w, "  %s\n", initflow.AgentProceeded(agentMarker(os.Getenv)))
	return true, nil
}

// gateApplied records the step the gate admitted, AFTER the step's own write
// (D1014): an apply that cannot leave a trail fails the command, which is what
// a record that is a control rather than a courtesy means. A run with no gate,
// or one whose step the gate did not admit, records nothing — the record is
// the gate's, and a record about a write that never happened is worse than no
// record at all.
func (r *initRun) gateApplied(ctx context.Context) error {
	if r.gate == nil {
		return nil
	}
	if err := r.gate.g.recorded(ctx); err != nil {
		return initErr(err)
	}
	return nil
}

// gateUnreadable is the sentence a step reads when the document the gate was
// to judge could not be read at all: terraform's own error follows it, and
// what it must not do is read as "nothing was refused". It names no flag and
// no marker, the way every sentence this gate writes does not.
const gateUnreadable = "the managed gate did not admit this step: the plan it was to judge could not be read, and a plan nobody could read is never applied here (D1012)"

// gateClass is the confirmation class of a step the gate judges: these are the
// ordinary applies of their stages, asked with confirmOrdinary, and an
// ordinary step is initflow.YesOrTyped in both doors (D12's answer of
// 2026-10-20). It is a function rather than a literal at each call site so
// there is one place to read what class the gate was asked about, and it is
// deliberately not r.stepClass(): that one answers for the Firestore
// database's location, which is Typed in one door and not a step this gate
// ever sees.
func gateClass() initflow.ConfirmClass { return initflow.YesOrTyped }

// admittedRules is what the record says admitted the plan: the three rules
// Gate runs, in the order it runs them (internal/managed/plan.go), each one
// true by the time a verdict is OK. A record that said only "admitted" would
// be one an owner cannot check against the plan they kept, and these three are
// what makes an admission mean something — the plan removes nothing, every
// change is one the review's own cover announced, and no cost-bearing
// attribute is blocked.
var admittedRules = []string{
	"the guard refused no delete and no replace, so the plan removes nothing of fugaro's own (the owner's delete list admits no removal in the embedded roots)",
	"the cover of the embedded roots admitted every change in the plan",
	"the cost list blocked no attribute of the plan",
}

// refusedRules is the verdict's refusals as the rules that fired, one per
// refusal, in the gate's own gate/address/why shape. They are what the
// refusal sentence names after NotConverged and what the audit record holds,
// and internal/managed bounds them where it renders them: a rule can quote a
// plan, and a plan is not this program's text.
func refusedRules(v managedmode.Verdict) []string {
	rules := make([]string, 0, len(v.Refusals))
	for _, ref := range v.Refusals {
		why := ref.Gate + ": " + ref.Why
		if ref.Address != "" {
			why = ref.Gate + ": " + ref.Address + " — " + ref.Why
		}
		rules = append(rules, why)
	}
	return rules
}

// stepCover is the cover of one step in the shape internal/managed reads
// (managed.Cover), built from the very function the step's confirmation asks
// (initRun.notCovered, initRun.notCoveredFirebase): what the gate judged and
// what a person would have been shown about the same plan are then one answer
// rather than two that happen to agree.
//
// A nil notCovered refuses every plan, which is managed.Spec's own rule for a
// Cover nobody gave: a step whose review was never built is exactly the case a
// mistake here could turn into "nothing refused it", and the gate's answer for
// it is the refusal.
type stepCover struct {
	notCovered func(*tf.Plan, []tf.ImportKey) string
}

func (c stepCover) NotCovered(p *tf.Plan, imports []tf.ImportKey) []string {
	if c.notCovered == nil {
		return []string{"this step's cover was not available to the gate, and a plan no cover judged is not admitted"}
	}
	if why := c.notCovered(p, imports); why != "" {
		return []string{why}
	}
	return nil
}

// coverOf is the cover of one step of this run, or one that refuses: a run
// whose review was never built has no cover for any root, and notCovered
// answers "" in that case because it has no plan to ask about — an answer the
// gate must not read as "nothing to refuse".
func (r *initRun) coverOf(which func(*tf.Plan, []tf.ImportKey) string) stepCover {
	if r.review == nil || r.review.cover == nil {
		return stepCover{}
	}
	return stepCover{notCovered: which}
}
