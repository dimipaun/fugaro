package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/shellword"
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
// The rules and policy stages are here with them, and neither is a Terraform
// stage: the rules stage's document is the generation this binary carries
// (rules.Generate, D1022's own answer for "a document the binary carries, so
// the plan IS the generation") and the policy stage's is the node it is about
// to publish (policyMigration.payload, the bytes the conditional write puts at
// the shared path). Each is judged where its own document and its own two
// questions — the database is this installation's (budgetDBUnadopted, D1017)
// and the generation in play is one this binary's own may write over (D854) —
// are in hand.
//
// The stages that are NOT here are as much a part of the decision:
// preflight (it changes nothing), images (it copies blobs and pins digests
// through the registry and writes this machine's config — it plans nothing, so
// the pair this gate judges a Terraform stage with has no document to read),
// secrets (spec §3.2: never admitted, and an agent's session is where a secret
// would have to be typed), plugin (local files), credentials and compute
// (`noYes` in the stage table today and under a managed project), the layer
// stage and the checkout's own steps.
//
// The workspace stage is here with the policy one, and for the same reason: its
// document is the node it publishes (docs/workspaces/spec.md §6 — W2's publish
// "runs under the gate update already opens"), and its two questions are the
// policy stage's two, the database is this installation's (D1017) and the node
// is one this binary's own may write over. What the gate may never admit there
// is a publish that admits more to the group than the node already carries:
// that step is initflow.Typed (D846) and D1026 admits no plan gate's answer for
// a Typed step, so the one write that decides who is in the group stays the
// owner's own word and never a mark's (SEC, rule 1).
//
// The policy stage's own two branches that write NO node are outside the gate
// as well, and they are the ones this map cannot express: D854's bare re-seed
// writes this machine's file alone and the generation-record step writes it
// alone, so neither has a document for a verdict to be about and both keep
// today's refusal in a coding agent's session (init_policy_migrate.go).
var gatedStages = map[string]bool{
	initflow.Installation:  true,
	initflow.Firebase:      true,
	initflow.Installation2: true,
	initflow.Repository:    true,
	initflow.Rules:         true,
	initflow.Policy:        true,
	initflow.Workspace:     true,
}

// initGate is this run's use of the gate: M6's helper, and the one read of the
// two marks the whole run makes.
//
// One read and not one per stage, because the helper's own contract is one
// readManagedGate per command followed by an admit per judged document, and
// because the marks of a project that is being revoked mid-run are re-read
// there (D1024) — where the write is, which is the interval that matters. A
// run that never read has a nil gate (gateUnderway, managedStage): not
// managed, which is every step's confirmation kept exactly as it is today.
type initGate struct {
	g managedGate
	// managed is what the read answered: both marks there, which is the only
	// answer that lets a stage through to the loop. A read that answered "not
	// managed" is this value false, and the two are the same answer to every
	// caller — which is why nothing here counts the reads: the answer cannot
	// change within a run's own read of it, and a re-read before a write is the
	// helper's, not this struct's.
	managed bool
}

// underway is whether this run's gate is open now: the marks were read, they
// said the project is managed, and no admit since has found that it is not.
//
// The second half is asked of the helper (managedGate.revoked) rather than
// copied into managed at each call of admit, because a revocation is detected
// in one place — the helper's own second read, immediately before a write
// (D1024) — and every reader of this answer has to close at the same instant:
// the run's one confirmation, the plugin wiring's y/N, the two IAM removals,
// and the admission of whatever stage comes next. A copy taken at the three
// call sites of this package's gateSteps is a copy the fourth one would not
// take, and the run would be back to applying a step under the gate's sentence
// for a project the last read said is not managed.
func (g *initGate) underway() bool { return g != nil && g.managed && !g.g.revoked }

// gateRun reads the managed project's marks for this run, once, before the
// loop's first stage (update_managed.go's one read per command, D1015).
//
// It is called by the converge beside gateEarly and not lazily by the first
// stage that needs the answer, because the answer is needed by steps that are
// not stages at all: the run's one confirmation (init_review.go's runCovers)
// and the plugin wiring's y/N are reached by a run whose gated stages all have
// nothing to do, and a read made by the first gated stage that applies would
// never happen there.
//
// The read is not made for a run that cannot act on the answer. --plan-only
// applies nothing by its own definition, and a shaped run's own arguments are
// what the gate must never judge, so both pay for no read at all — the same
// property their stages keep, and the reason the plan-only review's closing
// line is written to be true either way (init_review.go).
//
// --ask is decided here, once, right after the read and before the loop: the
// line the flag prints, or the refusal that stops the run. It is the flag's
// only entry point on this door, so there is one place to read what it does
// (managed_ask.go holds the rule and the reasons). A run the gate never covers
// gets its own line instead of a read: --ask has no confirmation to restore in
// a run that applies nothing the gate stands in for, and saying so is truer
// than reading marks to announce that nothing changed.
func (e *initEngine) gateRun(ctx context.Context) error {
	r := e.r
	if r.o.ask && !r.gateable() {
		fmt.Fprintln(r.w, initflow.AskNoGate())
		return nil
	}
	if r.gate == nil && r.gateable() {
		r.gate = readUpdateGate(e, r)
	}
	// The refusal is the user's to read and the run's to stop: it is returned
	// whole (a user error, exit 1), and nothing after it runs — no stage, no
	// write, no record.
	return askDecide(r.w, askFlags{ask: r.o.ask, yes: r.o.yes}, r.gateUnderway(), r.conditions())
}

// gateable is whether this door has a gate for this run at all. A run whose own
// arguments shape the installation rather than converge it as it is
// (shapesTheInstallation) keeps today's confirmations whole and pays for no
// read: what the gate would judge is a document that argument just wrote, which
// is the one thing it must never do. --plan-only is the same answer for the
// same kind of reason: it applies nothing, so there is nothing for the marks
// to decide.
func (r *initRun) gateable() bool {
	return r.o.door == updateDoor.name && !r.o.planOnly && !r.shapesTheInstallation()
}

// managedStage is initflow.Options.ManagedGate: whether the stage named runs
// under the gate in this session (D1016). The loop asks it in a coding agent's
// session and for the owner at their own terminal, and everything that is not
// a yes is today's refusal — an unreadable mark, a project that is not
// managed, a project whose marks a step's admit has since found gone
// (initGate.underway: the stages after that one are a project that is not
// managed's, and they keep the coding agent's refusal the whole time), a stage
// this door does not gate, and a run whose own arguments shape the installation
// rather than converge it as it is.
func (e *initEngine) managedStage(name string) bool {
	r := e.r
	// --plan-only applies nothing by its own definition, and the loop asks this
	// before it has looked at the flag: a gate that opened a stage under it
	// would be the one way a plan-only run writes. The flag is asked FIRST,
	// beside the door, so no stage is ever handed to the loop's apply path by
	// a run that may not apply.
	if !gatedStages[name] || r.o.planOnly || !r.gateable() {
		return false
	}
	if r.gate == nil {
		// The converge reads this once for the whole run (gateRun). This is
		// the same read for a caller that reached a stage without it — the
		// helpers of a test, or an engine that drives the loop itself.
		r.gate = readUpdateGate(e, r)
	}
	return r.gate.underway()
}

// shapesTheInstallation reports whether this run's own arguments shape the
// installation instead of converging it as it is: the flags that only mean
// something for the installation (initOptions.installationFlags), the state
// bucket, and the owner's delete list.
//
// Such a run is refused the gate and keeps today's coding-agent refusal, whole
// and unmoved, and the reason is the same one §3.2 gives for the flags
// themselves. --launcher, --operator and --budget-admin are the members the
// run's own review lists (accessLists reads them from this run's options into
// the cover's Listed), so a gate asked with one of them would be judging a
// plan against the list that argument just wrote: a plan granting that member
// a role is exactly what §4.1's rules 4 and 5 refuse — an added member — and
// the flag would walk around both by making the addition covered. --budget
// with --billing-account sets the cover's Billing the same way, and
// --firebase-project puts another project in it, while --replace-image,
// --expect-digest, --alert-email, the image and bucket and region flags and
// the rest change what the installation IS, which is no converge of a
// document. The state bucket is the same answer (the installation is the
// state it names), and the delete list is the one §3.2 and D1011 give it: the
// owner's own, never an argument of a command in the set.
//
// It is asked beside the door, before any mark is read, so a session that
// shaped the installation pays for nothing and learns nothing: M6's read is
// not made, no verdict exists, and no record is written — the same property
// --plan-only has, and for the same reason.
func (r *initRun) shapesTheInstallation() bool {
	return len(r.o.installationFlags()) > 0 || r.o.stateBucket != "" || len(r.o.allowDelete) > 0 ||
		r.o.cloud.region != "" || r.o.githubAppID != ""
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
	// A gate that was returned at all is a gate that was read: the callers ask
	// r.gate == nil for "this run never read", so every path out of here hands
	// back the struct, with managed left false where the marks said nothing.
	g := &initGate{}
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
		AgentMarker: agentMarker(os.Getenv, r.stdinTerminal()),
	}
	gate, err := readManagedGate(ctx, env, facts)
	if err != nil {
		return g
	}
	g.g, g.managed = gate, gate.managed
	return g
}

// gateUnderway is whether this run has the managed gate open: the marks were
// read and they say the project is managed, and no step's admit has since found
// the marks gone (initGate.underway — a project revoked mid-run is a project
// whose confirmations come back at once). It is what a step asks when what it
// needs is not a verdict about its own document but the fact that this run is a
// managed project's — the run's one confirmation (init_review.go), the plugin
// wiring's y/N (init_plugin_stage.go), and the two IAM removals below.
//
// It is not a permission of its own: a step that writes a document the gate
// judges goes through gateStep, and gateUnderway says nothing about any
// document. What it does say is that a prompt this project's owner asked not to
// see (the ruling of 2026-11-01) is replaced here — which is why every caller
// of it is a step whose confirmation the gate does not judge, and never a step
// the gate refused.
func (r *initRun) gateUnderway() bool { return r.gate.underway() }

// gateStandsIn is whether the gate stands in for the confirmations it does NOT
// judge document by document — the run's one confirmation (init_review.go's
// runCovers and the ordinary steps it covers), the plugin wiring's y/N
// (init_plugin_stage.go) and the lines the review prints about them. It is
// gateUnderway with --ask struck out, and it is the whole of what the flag does
// to this door beyond its own line: the steps whose documents the gate judges
// ask the same question through initflow.GateConfirms (gateStep), which
// Conditions.Ask answers false to, and the gate's own read, admit and record
// are untouched either way.
//
// A run with no gate is not this case (r.gate == nil), and neither is one whose
// marks said the project is not managed: both keep every confirmation they
// have, exactly as they did before the managed case existed.
func (r *initRun) gateStandsIn() bool { return r.gateUnderway() && !r.o.ask }

// gateLeavesTheIAMCall is whether the one write of these stages that the gate
// has no document for is left to the owner rather than made here: removing
// project Viewers' read access to the state bucket and to the runs bucket is a
// Storage IAM call of its own (infra.RemoveProjectViewers), so D1016's gate — a
// verdict about a document a session may be admitted to — has nothing to judge
// for it, and a session the gate admitted to a plan is not admitted to the call
// beside it.
//
// True only in a coding agent's session, and the terminal is the whole of the
// difference: the owner at their own terminal in a managed project gets no
// prompt for it either (runCovers, the gate's own read of the marks), so the
// call is made there with the line the ordinary path prints, exactly as the
// owner's own run has always made it. Leaving it for an owner who is already
// looking at the screen would be a prompt by another name.
func (r *initRun) gateLeavesTheIAMCall() bool {
	return r.gateUnderway() && agentMarker(os.Getenv, r.stdinTerminal()) != ""
}

// gateProceeded is the line a gate-admitted step prints where its confirmation
// would have been: the agent's own sentence in a coding agent's session, and
// the owner's in their own terminal (initflow's AgentProceeded and
// GateProceeded, which are two sentences because they are addressed to two
// readers).
func (r *initRun) gateProceeded() string {
	return gateSentence(agentMarker(os.Getenv, r.stdinTerminal()))
}

// gateSentence is the same choice for a caller that holds the marker rather
// than the run: the settings write's reader is a surface of its own (it is
// handed initflow.Conditions, not an initRun), and a second copy of the rule
// here would be the copy that drifts. An empty marker is the owner's own
// terminal, which reads GateProceeded — the sentence that says a verdict was
// taken, not GateOrdinary's, which says a step was announced and no rule read
// anything. A step that reaches a sentence at all has a verdict behind it: the
// gate's answer is what the caller passed.
func gateSentence(marker string) string {
	if marker != "" {
		return initflow.AgentProceeded(marker)
	}
	return initflow.GateProceeded()
}

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
// AllowDelete is empty, and that is the reading of "tf.Guard with the owner's
// list" this command lands on. Three things say so. The list the owner writes
// with fugaro allow-delete approves deletes in the project's agent-authored
// root, which is what fugaro managed apply applies, and nothing the embedded
// roots plan (managed.DeleteAdvice states it, and initManagedDeleteAdvice says
// the same to a person): a gated step of this run that read it would approve
// removals of Fugaro's own resources the owner never meant to approve there.
// The list is never an argument of a command in the set (D1011, reconciliation
// F6), so the session's own --allow-delete is refused rather than honoured —
// and it is refused by the emptiness here rather than by a sentence, because
// nothing this gate admits may read as "pass an address and it will pass". And
// the gate can only narrow: an empty list refuses every delete and every
// replace in these roots (managed.Spec's own rule), which is a subset of what
// D74 requires, never a widening of it.
//
// So an installation whose upgrade deletes something is the owner's to run,
// with the flag they have always passed and the confirmations they have always
// been asked.
//
// upgrade is the one thing the gate is told and the owner's list is not: the
// replaces this binary's own upgrade makes of a grant an earlier release wrote,
// as infra.LauncherGrantUpgrade matched them out of this plan
// (managed.Spec.Upgrade). A launcher's condition is ForceNew, so every
// installation that has not been re-applied since it last changed plans one
// replace per launcher who is not an operator, and a gate that refused it would
// make `fugaro update` two steps — the flag the refusal names, on the owner's
// own terminal — which is the one thing CLAUDE.md's rule about that command
// forbids. It is the installation root's own and no other step's: the
// runs_launcher grants live in that module, and every other call site passes
// nil.
func (r *initRun) gateStep(ctx context.Context, t *tf.TF, imports []tf.ImportKey, cover managedmode.Cover, upgrade []string) (bool, error) {
	g := r.gate
	if g == nil || !g.managed {
		return false, nil
	}
	// --plan-only writes nothing, so there is nothing for the gate to admit:
	// the run keeps its own plan-only path, which applies nothing either way.
	if r.o.planOnly {
		return false, nil
	}
	// The file is read twice: here for the document whose bytes are digested
	// and recorded, and again inside the gate below, which parses the same path
	// (tf.TF.Show is tf.TF.ShowJSON plus parsePlan). Two reads of one immutable
	// saved plan, with nothing planning or applying between them, are one
	// artifact — which is what D1022 requires of them. Handing the gate the
	// document read here instead would mean a seam in managed.Spec for a plan
	// the caller already holds, and that is M0's gate, not a swap this step
	// leaves open.
	doc, err := t.ShowJSON(ctx, infra.PlanFile)
	if err != nil {
		return true, &initflow.GatedError{Reason: gateUnreadable, Err: err, Refusal: initflow.GateRefusedUnread}
	}
	verdict, err := managedmode.Gate(ctx, infra.PlanFile, managedmode.Spec{
		Project: r.projectName, GCPProject: r.gcpProject,
		Cover: cover, Imports: imports, TF: t, Upgrade: upgrade,
	})
	if err != nil {
		// A plan terraform cannot read is never admitted (managed.Gate's own
		// rule, and M1's fail-closed shape): the step stops, and it stops in
		// the gate's words, because a session that read its own refusal here
		// would be told to hand the step to someone else rather than what
		// about the plan could not be read.
		return true, &initflow.GatedError{Reason: gateUnreadable, Err: err, Refusal: initflow.GateRefusedUnread}
	}
	class := gateClass()
	conv := managedmode.ConvergeRefused(doc, class, refusedRules(verdict))
	if verdict.OK {
		conv = managedmode.ConvergeAdmitted(doc, class, admittedRules(upgrade))
	}
	m, err := g.g.admit(ctx, class, conv)
	if err != nil {
		return true, r.gateRefused(err, doc, verdict, cover, imports)
	}
	// D55's one rule, asked with this run's conditions — a coding agent's
	// session or the owner's own terminal, which is the whole of who may
	// reach this code — and the verdict M6's helper just built: a gate that
	// admitted this document replaces the confirmation for this step, and
	// anything else keeps it.
	if !initflow.GateConfirms(class, r.conditions(), m) {
		return false, nil
	}
	fmt.Fprintf(r.w, "  %s\n", r.gateProceeded())
	return true, nil
}

// gateRulesStep judges the security rules generation the stage is about to
// deploy, and answers whether the gate admitted it: the rules stage's own
// version of gateStep, for the one stage of this command whose document is not
// a Terraform plan.
//
// The document is the generation this binary carries (spec §4.2: "it is a
// document the binary carries, so the plan IS the generation"), and the digest
// D1022 records is of exactly those bytes. The two questions that make it a
// verdict are the ones the step has already asked of the database by the time
// this is called — that the database is this installation's (budgetDBUnadopted,
// D1017) and that the deployed generation is one this binary's own may replace
// (rulesStageState) — and a stage whose state is neither of those has skipped
// before reaching here. What this adds is what the rules stage never had: the
// step is judged and the verdict is recorded, so an owner can read afterwards
// which generation an agent's session deployed and under which ownership.
//
// A generation that cannot be produced is the one failure this call has of its
// own: rules.Generate failing means the document the gate would judge does not
// exist, and a step with no document is refused rather than applied (the same
// direction managed.Gate's unreadable plan falls in).
func (s *rulesStage) gateStep(ctx context.Context) (bool, error) {
	r := s.e.r
	if !r.gateUnderway() {
		return false, nil
	}
	doc, err := rules.Generate()
	if err != nil {
		return true, &initflow.GatedError{Reason: gateUnreadable, Err: err, Refusal: initflow.GateRefusedUnread}
	}
	class := gateClass()
	m, err := r.gate.g.admit(ctx, class, managedmode.ConvergeAdmitted(doc, class, rulesAdmittedRules))
	if err != nil {
		return true, &initflow.GatedError{Reason: err.Error(), Err: err, Refusal: gateRefusalOf(err)}
	}
	if !initflow.GateConfirms(class, r.conditions(), m) {
		return false, nil
	}
	fmt.Fprintf(r.w, "  %s\n", r.gateProceeded())
	return true, nil
}

// rulesAdmittedRules is what the record says admitted the rules step: the two
// questions the stage answered before its write, in the order it asked them.
// The generation is named by the constant and not by a message, because what an
// owner checks afterwards is which generation landed, and that is the number
// this binary carries.
var rulesAdmittedRules = []string{
	fmt.Sprintf("the rules document is this binary's own generation %d (projectpolicy.RulesVersion)", projectpolicy.RulesVersion),
	"the database is this installation's: budgetDBUnadopted admitted it (D1017), and the deployed generation is one this binary's own may be deployed over",
}

// gateStep judges the policy node this stage is about to publish, and answers
// whether the gate admitted it: the policy stage's version of gateStep, for the
// one step of this command that rewrites what every run of the project is
// admitted by (M1's rule: "the policy stage against budgetDBUnadopted first
// (D1017) and the D854 generation after").
//
// The document is m.payload — the bytes the stage's conditional write puts at
// the shared path, read once and confirmed against, so the document the gate
// judged and the document that lands are the same bytes (the ETag the write is
// conditional on is the read the diff was made from). The two questions the
// record names are the two the stage's own read answered before this call:
// budgetDBUnadopted first (the database is this installation's, and a database
// the predicate refuses is a stage that has already skipped), and the D854
// generation after (this machine's file is not behind the published node, which
// is the state in which publishing from here would replace a newer node with an
// older file).
//
// The class is the step's own and not this function's choice: a publish that
// loosens a ceiling takes the Typed class (D846), which D1026 never admits, so
// nothing is recorded for it and the write waits on the tail's own
// confirmation (policyMigrationConfirm) — a note on a managed project at the
// owner's own terminal (the ruling of 2026-11-06), and the refusal below in a
// coding agent's session (D1552). The helper decides the class from the step,
// and this call passes the class it read rather than the one it would have
// preferred.
//
// The two branches that write no node are not judged at all, and the reason is
// the one above: D854's bare re-seed and the generation-record step write this
// machine's file and nothing the gate could read.
func (s *policyMigrateStage) gateStep(ctx context.Context, m *policyMigration) (bool, error) {
	r := s.e.r
	if !r.gateUnderway() || !m.writesNode() {
		return false, nil
	}
	class := gateClass()
	if len(m.loosening()) > 0 {
		class = initflow.Typed
	}
	doc := m.payload()
	if len(doc) == 0 {
		// A node write with no bytes to write is the stage's own contract
		// broken, and it is refused rather than published: an empty document is
		// the one thing this gate must never hand to a write.
		return true, &initflow.GatedError{Reason: gateUnreadable, Err: errors.New("the policy node this step would publish is empty"), Refusal: initflow.GateRefusedUnread}
	}
	conv := managedmode.ConvergeAdmitted(doc, class, policyAdmittedRules)
	mg, err := r.gate.g.admit(ctx, class, conv)
	if err != nil {
		return true, &initflow.GatedError{Reason: err.Error(), Err: err, Refusal: gateRefusalOf(err)}
	}
	marker := agentMarker(os.Getenv, s.e.r.stdinTerminal())
	if !initflow.GateConfirms(class, r.conditions(), mg) {
		// The publish that widens the node is the owner's own step, and the
		// fence on it is the session's rather than its class's (D1552): a
		// loosening is what refuses here — m.loosening() is the node's
		// ceilings, its account-choice members and its first publish, the three
		// widens this stage has — and the marker is what asks it, so the fence
		// keeps refusing a coding agent's session when the class system goes
		// (D1552's own reason for the fence being this one and not Typed). The
		// class stays beside it for everything else: it is what D1026 keeps out
		// of a plan gate's reach, and a step this gate admits is one the rules
		// above admitted, so what the sentence below names is the class rule
		// and no rule that passed.
		//
		// The sentence is the coding agent's alone, and the person at a real
		// terminal on a managed project is not asked for the typed name (the
		// owner's ruling of 2026-11-06, policyMigrationConfirm), so the tail
		// writes on a note with the diff above it — what this loosens is read
		// there, in the members that changed, rather than in a sentence worded
		// for a confirmation nobody is asked for. What it leaves the owner is
		// the command they run themselves (Left, below): this refusal is about
		// the step and not about the plan, so the step is theirs.
		//
		// Every other not-admitted answer keeps the caller's refusal, because
		// the gate has no sentence for it: a mark revoked between the two reads
		// is a project that is not managed at this moment, and a fallback
		// inventory is not a verdict about this document either.
		if len(m.loosening()) > 0 && marker != "" {
			refused := conv.Refuse()
			return true, &initflow.GatedError{Reason: refused.Error(), Err: refused, Refusal: initflow.GateRefusedClass}
		}
		return false, nil
	}
	fmt.Fprintf(r.w, "  %s\n", r.gateProceeded())
	return true, nil
}

// policyAdmittedRules is what the record says admitted the policy step: the two
// questions the stage asked before its write, in the order it asked them
// (D1017's ownership first, D854's generation after), and what the node is.
// Neither the document's own content nor a value in it is named here — the
// record's digest is what an owner checks the node against.
var policyAdmittedRules = []string{
	"the database is this installation's: budgetDBUnadopted admitted it (D1017)",
	fmt.Sprintf("this machine's config is not behind the published node (D854), which is the state in which publishing writes version %d over a node this binary's own may write", projectpolicy.Version),
}

// gateStep judges the workspace node this stage is about to publish, and answers
// whether the gate admitted it: the workspace stage's version of the policy
// stage's gateStep, for the second stage of this command whose document is the
// database node it writes rather than a plan.
//
// The document is p.body — the bytes the stage's conditional write puts at
// workspaces/<name>, built once and confirmed against, so the document the gate
// judged and the document that lands are the same bytes (the ETag the write is
// conditional on is the read the diff was made from). The two questions are
// workspaceAdmittedRules, which the stage answered before it built the
// document: the database is this installation's (budgetDBUnadopted, D1017) and
// the node in play is one this binary's own may write over.
//
// A publish that ADMITS something is initflow.Typed, the class D846 gives the
// policy stage's raised ceiling and the one D1026 refuses whatever a plan gate
// said: the record decides who is in the group, so a member this stage adds —
// and the whole membership, on a workspace that has no published record yet —
// may only be the owner's own word and never a mark's. A first publish is that
// class for the policy stage's reason (M3): the document is chosen where none
// was, and a session that edited `workspace_members` in the YAML by hand would
// otherwise publish the group's whole membership behind a mark. Every other
// publish — a plain rewrite, a migration, a removal — is the ordinary
// YesOrTyped step the gate may stand in for, which is what the workspaces design
// says of this write ("it runs under the gate `update` already opens", §6;
// reconciliation F32 names it the cloud write of the two that the gate ADMITS).
func (s *workspaceStage) gateStep(ctx context.Context, p *workspacePublication) (bool, error) {
	r := s.e.r
	if !r.gateUnderway() {
		return false, nil
	}
	class := gateClass()
	if len(p.widens) > 0 {
		class = initflow.Typed
	}
	doc := p.body
	if len(doc) == 0 {
		// A node write with no bytes to write is the stage's own contract
		// broken, and it is refused rather than published: an empty document is
		// the one thing this gate must never hand to a write.
		return true, &initflow.GatedError{Reason: gateUnreadable, Err: errors.New("the workspace node this step would publish is empty"), Refusal: initflow.GateRefusedUnread}
	}
	conv := managedmode.ConvergeAdmitted(doc, class, workspaceAdmittedRules)
	mg, err := r.gate.g.admit(ctx, class, conv)
	if err != nil {
		return true, &initflow.GatedError{Reason: err.Error(), Err: err, Refusal: gateRefusalOf(err)}
	}
	marker := agentMarker(os.Getenv, s.e.r.stdinTerminal())
	if !initflow.GateConfirms(class, r.conditions(), mg) {
		// The Typed class is the one not-admitted answer with a sentence of the
		// gate's own, and it is given here rather than left to the caller's
		// coding-agent refusal: this step's document WAS admitted by the rules
		// above, so what refuses it is the class rule and nothing about the
		// session. The marker is asked for beside the class, and it is what
		// keeps this sentence inside the session the gate is about: it is the
		// coding agent's alone. A person at a real terminal on a managed project
		// is not asked for the typed name (the owner's ruling of 2026-11-06,
		// workspacePublication.confirm), so the widening writes there on a note
		// with the diff above it — what this admits is read in the added lines
		// rather than in a sentence worded for a confirmation nobody is asked
		// for — and every other not-admitted answer (a pipe, --json and
		// --non-interactive carry no marker and no answer either) keeps the
		// confirmation the caller would have asked.
		if class == initflow.Typed && marker != "" {
			refused := conv.Refuse()
			return true, &initflow.GatedError{Reason: refused.Error(), Err: refused, Refusal: initflow.GateRefusedClass}
		}
		return false, nil
	}
	fmt.Fprintf(r.w, "  %s\n", r.gateProceeded())
	return true, nil
}

// workspaceAdmittedRules is what the record says admitted the workspace step:
// the two questions the stage answered before its write, in the order it asked
// them — the database is this installation's (D1017), and the node in play is
// one this binary's own may write over (a first publish, where there is no node
// to replace, or a node at the version this binary writes and the migration
// carries). Neither the document's content nor a member it names is here: the
// record's digest is what an owner checks the node against, and a record that
// listed the group would be a second copy of the thing the node exists to be.
var workspaceAdmittedRules = []string{
	"the database is this installation's: budgetDBUnadopted admitted it (D1017)",
	fmt.Sprintf("the published node is one this binary's own may write over: there is none, or it is at version %d (or at a version the migration carries to it in place)", projectpolicy.WorkspaceVersion),
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
// to judge could not be produced or read at all: terraform's own error follows
// it, and what it must not do is read as "nothing was refused". It names no
// flag and no marker, the way every sentence this gate writes does not. It says
// document rather than plan because one step of this command judges a plan and
// two judge a document the binary or the database holds, and the sentence a
// session reads must not name a shape the step never had.
const gateUnreadable = "the managed gate did not admit this step: the document it was to judge could not be produced, and a document nobody could read is never applied here (D1012)"

// gateRefused is the GatedError of a step this gate did not admit, from the
// error M6's admit answered with, the plan's own bytes, the verdict they were
// judged by, and the cover and the imports the gate asked them with.
//
// It is one function rather than a struct literal at the call site because the
// shape of the refusal is a decision about this command's own document, and it
// takes all five: the plan and the verdict are what say whether the owner's
// flag admits a removal here (gateDeleteLeft — which rule refused, and whether
// the flag clears what they refused), the cover and its imports are what say
// whether the same flag would apply something no rule read (a replace's create
// half), and the error is what says whether the gate judged anything at all
// (gateRefusalOf). The two stages whose document is not a plan can only ever be
// the last, so they ask gateRefusalOf themselves.
//
// The plan's answer is asked only of a plan the gate REFUSED (gateRefusalOf's
// plan answer, i.e. managed.ErrNotConverged): a step the gate reached no verdict
// on — the plan could not be printed, the creation fact could not be read again
// immediately before the write — is a failure of the run, and a run's failure
// is not the owner's removal however many deletes the document it never judged
// turns out to carry. A mark read that failed takes the same document down a
// different road than a rule that refused it, and the two roads' answers must
// not be handed one another's command.
func (r *initRun) gateRefused(err error, doc []byte, verdict managedmode.Verdict, cover managedmode.Cover, imports []tf.ImportKey) *initflow.GatedError {
	refusal := gateRefusalOf(err)
	if refusal != initflow.GateRefusedPlan {
		return &initflow.GatedError{Reason: err.Error(), Err: err, Refusal: refusal}
	}
	if owner, ok := r.gateDeleteLeft(err.Error(), doc, verdict, cover, imports); ok {
		return &initflow.GatedError{Reason: err.Error(), Err: err, Refusal: initflow.GateRefusedDelete, Owner: owner}
	}
	return &initflow.GatedError{Reason: err.Error(), Err: err, Refusal: refusal}
}

// gateRefusalOf is what a step's refusal leaves the person who reads it, read
// off the error the gate answered with: a verdict against the document
// (managed.ErrNotConverged, whether the cover, the cost list or a write's own
// record failed beside it) or a failure with no verdict at all — a plan
// terraform could not print, a mark that could not be read a second time
// immediately before the write.
//
// The two are different LEFTS and not two spellings of one: a plan the rules
// would not admit is nobody's to type past (initflow.GateRefusedPlan), while a
// step the gate never judged says so rather than blaming a cover that read
// nothing (GateRefusedUnread). It is decided here rather than in the loop
// because only this side of the seam can match on managed's own sentinel, and
// a guess would put a wrong sentence — "a person files an issue", or "the plan
// is fine, the run failed" — in front of the owner.
//
// The one plan refusal it does not cover is gateDeleteLeft's (D1012's delete
// rule): a removal the owner's own flag admits is theirs to approve, so the
// refusal leaves them that command rather than an issue, and it is asked of
// the plan's bytes rather than of the sentence — no error value says which
// resource a plan removes.
func gateRefusalOf(err error) initflow.GateRefusal {
	if errors.Is(err, managedmode.ErrNotConverged) {
		return initflow.GateRefusedPlan
	}
	return initflow.GateRefusedUnread
}

// gateDeleteLeft is the left-for-you of a refusal the owner's own --allow-delete
// admits: the step is theirs and not a session's (spec §4.3's "For the owner"),
// the command already exists, and a person who is told to file an issue about
// a removal they can approve is being sent to work that does not close the loop
// the refusal opened.
//
// Four questions come before it, and all of them are about the command being
// one that can help rather than about the sentence: the verdict says whether
// the delete rule is the only thing that refused the plan
// (RefusedOnlyForRemovals — the flag turns the whole gate off, not one rule of
// it), the plan's own bytes say whether the step the flag produces will apply
// it (the guard it will run, and the project-job rule that runs beside it), and
// the cover the gate itself asked says whether what the plan does BESIDES those
// removals is judged at all (tf.WithoutRemovals, which is the create half of
// every replace among them). Each of them, answered the other way, is a command
// the owner would run and watch refuse — or worse, one that applies a change no
// rule ever read.
//
// What it asks the plan, and not the gate's sentence: tf.Listable is exactly
// the set --allow-delete admits — a removal (a delete, or a replace, whose
// actions carry one) of an action the guard knows, at an address the plan does
// not reach from another one — so a plan whose removals are all outside that
// set (an object the plan moves, an action the guard does not know) is NOT this
// shape: no flag admits them, and naming a command that cannot help is the same
// mistake as offering the stage's prompt. The plan is read from the bytes the gate
// itself judged, which are already in hand (D1022's document), so the addresses
// in the command and the identity printed beside them are the ones that plan
// carries.
//
// A replace is the one shape the addresses cannot tell from a removal, and the
// cover is where the difference shows. --allow-delete admits a delete AND a
// replace (tf.Listable), while the cover refuses a change carrying a delete as
// ONE fact — "<address> (actions delete, create)" — and reads nothing inside it
// (tf.Cover.NotCovered's first branch). So the create half of a replace at an
// address on that list is a change no rule of the gate ever judged, and the run
// that passes the flag applies it with the gate stood down. The owner's
// approval is the removal, so the create half is asked here, as the create it
// is, of the cover the gate itself asked (tf.WithoutRemovals): a replace whose
// create half grants a role to somebody the review did not list, or to
// allUsers, leaves no command, while one whose create half the cover admits
// keeps the command a removal leaves.
//
// The identity is the promise §4.3 makes to the owner — "where the refusal is a
// delete the owner did not approve, it prints the identity the plan's before
// view carries" — and tf.RemoveIdentityText is where it comes from: the object
// the plan takes away, or the reason the plan does not identify one. The
// address is quoted with shellword, because it is the plan's text and it is
// going onto a command line; --allow-delete takes the address alone (fugaro's
// own roots are not the owner's delete list, D72/D74), so the identity is what
// the owner reads to know which object they are approving.
//
// The address alone is also why a removal the plan identifies nothing for stays
// in this shape rather than being filtered out of it: the guard the owner's own
// flag reaches compares the address and nothing else, so it clears the removal
// whether or not the before view names the object — and the reason
// RemoveIdentityText gives is then the whole of what the owner has to go on,
// which is why it is printed rather than dropped. What no flag reaches is what
// tf.Listable already leaves out (the paragraph above), and nothing else.
func (r *initRun) gateDeleteLeft(reason string, doc []byte, verdict managedmode.Verdict, cover managedmode.Cover, imports []tf.ImportKey) (initflow.Left, bool) {
	var plan tf.Plan
	if err := json.Unmarshal(doc, &plan); err != nil {
		// A document this binary cannot read is not one whose removals can be
		// named: the refusal keeps the shape the error gave it.
		return initflow.Left{}, false
	}
	// What the flag would clear here: the removals --allow-delete admits, which
	// is the set the questions below are both asked about.
	admits := tf.Listable(&plan, nil)
	if len(admits) == 0 {
		return initflow.Left{}, false
	}
	// And only a plan the delete rule is the whole of: the flag this shape hands
	// the owner shapes the installation, so the gate stands down for the run
	// that passes it and nothing reads the plan again. A plan the cover or the
	// cost list refused something else in — an outsider's grant, a GPU — is
	// nobody's in the same way it was before this shape existed, and offering
	// the flag for it would be offering a command that applies exactly what
	// those rules refused. A removal's own refusal is not that, from either gate
	// (RefusedOnlyForRemovals's doc says why the question is asked of the
	// addresses).
	if !verdict.RefusedOnlyForRemovals(admits) {
		return initflow.Left{}, false
	}
	// And only where the flag clears the whole plan: what the run that passes
	// it will ask is the step's own guard (initRun.guard), so the command
	// offered has to be one that guard admits. Anything it still refuses — a
	// removal the flag does not reach (tf.Guard's own rules), a project job the
	// retirement rule holds whatever the list says (infra.RefuseProjectJobDeletes)
	// — is a run the owner would make and watch refuse, which is the same
	// mistake as an issue about work that does close the loop.
	if err := infra.RefuseProjectJobDeletes(&plan); err != nil {
		return initflow.Left{}, false
	}
	if err := tf.Guard(&plan, admits); err != nil {
		return initflow.Left{}, false
	}
	// And only where what the flag applies BESIDE those removals is judged: the
	// removals themselves are the owner's approval and nothing reads them
	// again, but a replace carries a create beside its removal, and the gate's
	// own cover never looked inside the change it refused for the delete
	// (tf.Cover.NotCovered). The create half is asked of that same cover here,
	// so a plan whose create half it refuses — a grant to somebody the review
	// did not list, an allUsers one — leaves the owner no command rather than
	// one that applies it with the gate stood down and no rule left to read it.
	if why := cover.NotCovered(tf.WithoutRemovals(&plan, admits), imports); len(why) > 0 {
		return initflow.Left{}, false
	}
	identities := make(map[string]string, len(admits))
	for _, rc := range plan.ResourceChanges {
		if slices.Contains(admits, rc.Address) {
			identities[rc.Address] = tf.RemoveIdentityText(rc)
		}
	}
	words, args := make([]string, 0, len(admits)), make([]string, 0, len(admits))
	for _, addr := range admits {
		words = append(words, addr+" — "+identities[addr])
		args = append(args, "--allow-delete", shellword.Quote(addr))
	}
	return initflow.Left{
		Kind: initflow.LeftCommand,
		Text: "the managed gate refused this step and nothing was written: " + reason +
			". The removal this plan carries is your own decision and not a session's, and no typed confirmation reaches past a refusal (D1012): the command below is the one you run yourself, at your own terminal, where the flag admits every removal named here and the step keeps its own confirmation. The plan removes " + strings.Join(words, ", "),
		Commands: []string{selfCommand() + " " + r.o.doorName() + " " + strings.Join(args, " ")},
	}, true
}

// gateClass is the confirmation class of a step the gate judges: these are the
// ordinary applies of their stages, asked with confirmOrdinary, and an
// ordinary step is initflow.YesOrTyped in both doors (D12's answer of
// 2026-10-20). It is a function rather than a literal at each call site so
// there is one place to read what class the gate was asked about, and it is
// deliberately not r.stepClass(): that one answers for the Firestore
// database's location, which is Typed in one door and not a step this gate
// ever sees.
func gateClass() initflow.ConfirmClass { return initflow.YesOrTyped }

// admittedRules is what the record says admitted the plan: the rules Gate ran,
// in the order it ran them (internal/managed/plan.go), each one true of the
// plan the record digests. A record that said only "admitted" would be one an
// owner cannot check against the plan they kept, and these are what makes an
// admission mean something — the plan removes nothing of the owner's own, every
// change is one the review's own cover announced, and no cost-bearing attribute
// is blocked.
//
// upgrade is the replaces this binary's own upgrade makes, as the step matched
// them out of this plan (infra.LauncherGrantUpgrade), and it is the reason the
// sentences are the plan's own facts rather than one fixed list: on a plan that
// carries one, the guard DID admit a replace and the cover DID NOT read the
// change whole. A record that repeated the empty case's sentences there would
// be an admission record stating two things false about the very plan it
// digests — and an owner reading it back cannot check a rule against the plan
// unless the rule says what the gate did. The empty case is the three sentences
// every record this program wrote before the issues grant holds, unchanged.
func admittedRules(upgrade []string) []string {
	const costs = "the cost list blocked no attribute of the plan"
	if len(upgrade) == 0 {
		return []string{
			"the guard refused no delete and no replace, so the plan removes nothing of fugaro's own (the owner's delete list admits no removal in the embedded roots)",
			"the cover of the embedded roots admitted every change in the plan",
			costs,
		}
	}
	return []string{
		"the guard refused no delete and no replace this binary's own upgrade does not make, so the plan removes nothing of fugaro's own (the owner's delete list admits no removal in the embedded roots)",
		"the cover of the embedded roots admitted every change in the plan, each of the upgrade's replaces asked as the create it makes (its removal half is this binary's own, and no rule of the cover read it)",
		costs,
		"the replaces this binary's own upgrade makes were matched out of this plan, both halves of each change compared before it was named (infra.LauncherGrantUpgrade): " + safetext.FieldListLine(upgrade),
	}
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
