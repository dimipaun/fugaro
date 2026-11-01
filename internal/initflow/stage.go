// Package initflow is the converge loop behind the installation commands
// (design doc m11-setup-and-skills.md §3.1): an ordered list of stages, each
// with a read-only check and plan, a confirmed apply and a verify; a loop
// that applies the first stage that is not done, stops at the first stage
// that needs the user or fails, and runs nothing after it; and the plan view
// and the JSON result that report where the run stands.
//
// The package knows nothing about Google, Terraform or the CLI: the stages
// that exist (and the fakes the tests use) plug in through Stage. It does not
// know which command called it either — the lines it prints name the caller's
// Options.Program, never a verb of its own. What a stage must never be able to
// do is enforced here, not in the stage: --yes does not reach a stage that
// creates a project, links billing or takes a secret, no stage runs before
// the ones ahead of it are done, and a run that cannot prompt never prompts.
package initflow

import (
	"context"
	"fmt"
	"strings"
)

// State is where a stage stands.
type State string

const (
	Done     State = "done"      // nothing to do
	Changed  State = "changed"   // applied and verified in this run
	Blocked  State = "blocked"   // cannot be evaluated until something else is done
	NeedsYou State = "needs-you" // only the user can do the next step (see Left)
	Skipped  State = "skipped"   // does not apply to this run (Detail says why)
	Failed   State = "failed"    // the stage's apply, verify or check failed
	Todo     State = "will-do"   // something to apply
	// BillingPending is a stage that did its part and stopped at the billing
	// link, which the person running setup may not be allowed to make: not a
	// failure, a pause (BillingPausedError). WaitingForBilling is every later
	// stage that needs billing (NeedsBilling), which waits for it rather than
	// failing; a stage that needs none still runs.
	BillingPending    State = "billing-pending"
	WaitingForBilling State = "waiting-for-billing"
)

// NeedsBilling is whether a stage needs the GCP project to have billing: every
// stage but the ones before the project's own and the ones that write local
// files only — the plugin wiring, and the checkout's own steps, whose pin and
// plugin write the checkout's settings and the user's own Claude Code. The
// checkout's cloud step is the part of that stage that needs billing, and it is
// the step that says so: waiting for billing on the stage would leave the two
// local writes unmoved over a build that is asked for after both.
func NeedsBilling(name string) bool {
	switch name {
	case Preflight, Project, Plugin, Checkout:
		return false
	}
	return true
}

// Names of the stages, in the order the design runs them (§3.1). A stage
// is registered under exactly one of them.
const (
	Preflight     = "preflight"
	Project       = "project"
	Services      = "services"
	Installation  = "installation"
	Firebase      = "firebase"
	Images        = "images"
	Installation2 = "installation-2"
	Secrets       = "secrets"
	Plugin        = "plugin"
	Repository    = "repository"
	// Rules is the deployed security rules generation
	// (internal/cli/init_rules_stage.go): the document a project policy is
	// read under, and its marker. It comes after the repository stage and
	// BEFORE the policy stage, because the policy stage's write is gated on
	// that marker — a policy written under rules that predate the policy
	// model is a policy no run reads — so a run that migrates the node
	// without deploying the rules first refuses its own work and leaves the
	// whole upgrade for a second command (the owner's rule of 2026-10-30:
	// fugaro update is the whole upgrade).
	Rules = "rules"
	// Credentials is the owner-run stage that applies the credentials service
	// (docs/credentials-service, D146; C1.20). It is the only writer of the
	// installation's credentials_service variable, it is always typed at a
	// real terminal (--yes never covers it), and it comes BEFORE the
	// repository stage: a repository root reads the installation's
	// credentials_service output to decide whether its jobs mount a model
	// credential at all, and it grants each job account run.invoker on the
	// service — an apply that ran first would plan that grant against a
	// resource that is not there yet.
	Credentials = "credentials"
	// Policy is the published policy node's migration in place
	// (internal/cli/init_policy_migrate.go, the owner's rule of 2026-10-30:
	// fugaro update is the whole upgrade). It comes after the repository
	// stage because the node it reads and rewrites is what those jobs'
	// runs are admitted by, and it is the one stage whose change is a
	// document rather than an installation.
	Policy = "policy"
	// Layer is the published project layer and the one local layer file the
	// owner edits (internal/cli/init_layer_stage.go, D830/D831): the stage
	// seeds the file from the published object when it is absent, publishes
	// the file when it differs, and records the generation the file was
	// written from (D854). It comes after the policy stage and BEFORE the
	// checkout stage, and Checkout needs it: the checkout stage refreshes
	// the checkout's images off the project layer, so a layer that changed
	// commands or image settings has to be published before that refresh, or
	// the run builds from the layer the owner just replaced.
	Layer = "layer"
	// Checkout is what `fugaro upgrade` does for the checkout `fugaro update`
	// is run in, as a stage of the converge itself (internal/cli/
	// init_checkout_upgrade.go, the owner's rule of 2026-10-30: fugaro update
	// is the whole upgrade, so the two front doors may not be two commands).
	// It comes after the repository stage, whose onboarding is what the
	// images it refreshes belong to, and before compute, which stays last.
	Checkout = "checkout"
	// Compute is the Batch backend's installation-level provisioning
	// (design docs/design/compute-backend.md §8.1 task 6). It is last
	// because it is the only stage that may not run before everything else
	// is done: what it provisions is for a backend no run can select yet.
	Compute = "compute"
)

// slot is a stage's place in the canonical order: what it needs ahead of
// it, and whether --yes may reach it.
type slot struct {
	name  string
	needs []string
	// noYes marks the stages --yes never covers: the project's creation and
	// billing linkage (own flags and typed confirmations) and a secret (a
	// hidden prompt, never a flag).
	noYes bool
}

// Gating is linear: a stage runs only when every registered stage before it
// is done or skipped. needs records why the order is what it is, and the
// tests check it against the order; it does not gate anything itself.
//
// order is the dependency order the dogfooding run discovered by hand:
// installation, Firebase, images, the history job, secrets, repository.
var order = []slot{
	{name: Preflight},
	{name: Project, needs: []string{Preflight}, noYes: true},
	{name: Services, needs: []string{Project}},
	{name: Installation, needs: []string{Services}},
	{name: Firebase, needs: []string{Installation}},
	{name: Images, needs: []string{Installation, Firebase}},
	{name: Installation2, needs: []string{Firebase, Images}},
	{name: Secrets, needs: []string{Installation2}, noYes: true},
	{name: Plugin, needs: []string{Preflight}},
	// The credentials service is the owner's step (money, permanent, and the
	// identity the repository jobs' model credentials move to), so --yes never
	// covers it; it is applied before the repository roots, which read its
	// output and grant each job's account an invoker on it.
	{name: Credentials, needs: []string{Installation2}, noYes: true},
	{name: Repository, needs: []string{Installation2, Secrets}},
	// The rules generation is what the policy node is READ under, so it is
	// deployed before the node that depends on it: the policy stage's write
	// holds the publisher's gate, which refuses a node under rules that
	// predate the policy model, and a run whose rules are stale would refuse
	// its own migration and leave the upgrade unfinished.
	{name: Rules, needs: []string{Repository}},
	// The policy node is what every run of the project is admitted by, and
	// the repository stage is the last writer of the installation those runs
	// read it against.
	{name: Policy, needs: []string{Repository, Rules}},
	// The layer is what the checkout's images are built from, so it is
	// published before the refresh that reads it: the checkout stage's cloud
	// step builds this checkout's images off the layer that applies, and a
	// layer published after it would leave the run building from the one the
	// owner just replaced.
	{name: Layer, needs: []string{Repository}},
	// The checkout's own steps (fugaro upgrade's pin, plugin and cloud steps)
	// run after the repository stage: the images the cloud step refreshes are
	// that repository's, and the onboarding the repository stage does is what
	// the refresh builds for.
	{name: Checkout, needs: []string{Repository, Layer}},
	// The compute backend's provisioning is money, permanent and owner-run,
	// so --yes never covers it; it comes last, after every stage that
	// builds the identity, the registry and the installation it reads.
	{name: Compute, needs: []string{Repository}, noYes: true},
}

// runCovered are the stages whose ordinary steps the run's one confirmation
// covers (design m11 §9, "one confirmation"): the Terraform applies of the
// installation, the Firebase root, the history job and the repository, and
// what goes with them (the state bucket, the Viewer-read removal, the
// Firestore marks and rules, the deployed security rules generation, the image
// copies that add tags, the local config). Everything else is never covered by
// it: the project's creation and billing, a secret, and the plugin wiring (its
// own y/N).
//
// The map answers for WHOLE STAGES, and that is the whole of what it says. A
// step finer than its stage — the Firestore database's location inside
// Firebase, a billable image build inside Images, replacing a registry tag and
// onboarding an unlisted repository inside Repository — is not a row here and
// cannot be one: whether such a step is covered is the confirmation of the
// caller that takes it, decided where the step is taken, which is also the
// only place that knows which front door is running and so which steps that
// door owns.
var runCovered = map[string]bool{
	Installation: true, Firebase: true, Images: true, Installation2: true, Repository: true, Rules: true,
}

// RunCovers reports whether the run's one confirmation may cover the stage's
// ordinary steps. A stage --yes does not cover (noYes) is never covered.
func RunCovers(name string) bool { return runCovered[name] && YesCovers(name) }

// Names is the canonical order of every stage name.
func Names() []string {
	out := make([]string, len(order))
	for i, s := range order {
		out[i] = s.name
	}
	return out
}

// Needs is the stages name must come after.
func Needs(name string) []string {
	for _, s := range order {
		if s.name == name {
			return append([]string(nil), s.needs...)
		}
	}
	return nil
}

// YesCovers reports whether --yes may confirm the stage.
func YesCovers(name string) bool {
	for _, s := range order {
		if s.name == name {
			return !s.noYes
		}
	}
	return false
}

// LeftKind is how the user does what a stage leaves them.
type LeftKind string

const (
	LeftCommand LeftKind = "command" // one line to run
	LeftPrompt  LeftKind = "prompt"  // a prompt fugaro shows in a terminal
	LeftConsole LeftKind = "console" // something done in a web console
)

// Left is one thing only the user can do: a single line of text, never a
// continued or multi-line command and never a secret's value. When there are
// commands to paste, each is one complete line of its own in Commands, run in
// order, never joined by && or ;, and Text is the sentence that introduces
// them. A LeftCommand with no Commands is itself the one command.
type Left struct {
	Stage    string   `json:"stage"`
	Kind     LeftKind `json:"kind"`
	Text     string   `json:"text"`
	Commands []string `json:"commands,omitempty"`
}

// PrintedCommands is every command line the Left asks the user to run.
func (l Left) PrintedCommands() []string {
	if len(l.Commands) > 0 {
		return l.Commands
	}
	if l.Kind == LeftCommand && l.Text != "" {
		return []string{l.Text}
	}
	return nil
}

// NoTerminalAdvice is what to do when a typed confirmation or a hidden prompt
// has no real terminal: the one sentence every refusal ends with.
const NoTerminalAdvice = "run it in your own terminal window, not through a coding agent or a pipe"

// NeedsTerminal is the one refusal for a step that has to be typed at a real
// terminal; what names the step.
func NeedsTerminal(what string) string {
	return what + " needs a real terminal: " + NoTerminalAdvice
}

// Status is a stage's read-only check.
type Status struct {
	State  State
	Detail string // one line: what is missing, or why skipped or blocked
	Left   *Left  // for NeedsYou; else the stage's own Left() is used
}

// Env is what a stage's Plan and Apply are told.
type Env struct {
	// Yes is whether the stage may take --yes as its confirmation. It is
	// never true for a stage --yes does not cover, whatever the flag says.
	Yes bool
	// Interactive is whether the stage may prompt: a terminal, and not
	// --non-interactive.
	Interactive bool
}

// Plan is what a stage's real plan found.
type Plan struct {
	Detail      string // one line, such as "14 to create, 0 to change, 0 to delete"
	NothingToDo bool   // the plan is empty: the stage is done ("No changes")
}

// Outcome is what an apply did.
type Outcome struct {
	Changed bool   // false: the stage found nothing to do ("No changes")
	Detail  string // one line
	// Left is a step the stage applied what it could without: something of
	// its work this session may not do (the checkout's cloud half in a coding
	// agent's session, where every write must be on this machine —
	// LocalUnderAgent). The loop records it as left-for-you beside the
	// stage's line, so the run reports the step and exits 1 rather than
	// reporting a stage that skipped half of itself as done.
	Left *Left
}

// Stage is one step of the converge. Check, Plan and Verify only read;
// Apply is called only after the loop has decided a confirmation can be
// had, and keeps its own confirmations (typed project name, permanent
// location, billable build): the loop never confirms for a stage.
type Stage interface {
	Name() string
	// Check says what is missing or different, without changing anything.
	Check(ctx context.Context) (Status, error)
	// Plan shows what Apply would do (the stage's real plan, where there is
	// one) and changes nothing. Its one-line summary joins the plan view.
	Plan(ctx context.Context, env Env) (Plan, error)
	// Apply makes the stage's change after its own confirmation. A stage
	// that finds nothing to do returns Outcome{Changed: false}.
	Apply(ctx context.Context, env Env) (Outcome, error)
	// Verify re-reads what Apply claims to have done; an error fails the
	// stage and nothing after it runs.
	Verify(ctx context.Context) error
	// Left is what the user does when the stage needs them (a prompt the
	// loop cannot let it show, a command to run).
	Left() Left
}

// InputStage is a Stage that needs inputs only flags can give without a
// terminal. Missing names each flag (with its purpose) still absent.
type InputStage interface {
	Missing() []string
}

// SelfConfirming marks a stage that shows its own plan and takes its own
// confirmation (the typed project name, or --yes), and itself refuses, with
// a one-line instruction, to go on without a terminal and without --yes. The
// existing init engines are such stages: a run with neither still shows
// their plan before they refuse. The loop lets them run with
// Env.Interactive false; any other stage is not run at all in that case. A
// stage --yes does not cover can never be one.
type SelfConfirming interface {
	SelfConfirming()
}

// LocalUnderAgent marks a stage whose writes are all on this machine: in a
// coding agent's session it still applies, and it refuses there whatever
// reaches outside the machine. Nothing else checks that for it, so the refusal
// is the stage's own, and it must have one: a stage that implements this and
// then writes to the cloud would do it.
//
// It exists for one stage, the checkout's inside fugaro update (internal/cli/
// init_checkout_upgrade.go). A marker stops a run from doing what only a
// person should confirm; the pin in the checkout's settings and the plugin in
// the user's own Claude Code are neither — they are the same two writes the
// user's own session makes with fugaro upgrade, on files it reads anyway — and
// the cloud half of that stage keeps its refusal, in the step that makes it.
type LocalUnderAgent interface{ LocalUnderAgent() }

// StageError is a failure that carries the one-line fix.
type StageError struct {
	Err error
	Fix string
}

func (e *StageError) Error() string { return e.Err.Error() }
func (e *StageError) Unwrap() error { return e.Err }

// NeedsYouError is returned by Apply when it cannot proceed without the
// user (a typed confirmation not given): the loop stops with Left, which is
// not a failure.
type NeedsYouError struct{ Left Left }

func (e *NeedsYouError) Error() string { return e.Left.Text }

// BillingPausedError is returned by Apply (or wrapped by Check's status, see
// BillingPending) when the stage's own work is done but the project has no
// billing yet: the loop records it as BillingPending with Left, lets the
// stages that need no billing run, and marks the rest WaitingForBilling.
type BillingPausedError struct {
	Left   Left
	Detail string
}

func (e *BillingPausedError) Error() string { return e.Left.Text }

// MissingInputsError is --non-interactive's single refusal, listing every
// flag every stage still needs.
type MissingInputsError struct{ Flags []string }

func (e *MissingInputsError) Error() string {
	return "--non-interactive: missing " + strings.Join(e.Flags, "; ")
}

// NoTerminalError is the refusal to prompt without a terminal. With an
// agent's marker set it never suggests --yes: a coding agent does not get to
// confirm anything.
type NoTerminalError struct {
	Stage string
	Agent string // the coding agent's environment marker, "" for none
}

func (e *NoTerminalError) Error() string {
	if e.Agent != "" {
		return AgentRefusal(e.Agent)
	}
	return NeedsTerminal("typing the confirmation for "+e.Stage) + ", or pass --yes (with --non-interactive in scripts) once you have read what it does"
}

// AgentAdvice is the one sentence a refusal for a coding agent's session
// says. The check is a mitigation, not a barrier (an agent can unset its own
// environment): it stops the accident, not the adversary.
const AgentAdvice = "run this in your own terminal window, not through a coding agent"

// IDEMarker is the one marker the Claude Code IDE extension also sets in a
// person's own integrated terminal (VS Code, JetBrains).
const IDEMarker = "CLAUDE_CODE_SSE_PORT"

// IDEHint is the sentence for a person whose own IDE terminal tripped the
// check, and only for IDEMarker: for any other marker it is "", because a
// recipe for getting past the check has no business in text an agent reads.
// It is worded for the person, and says to do it themselves.
func IDEHint(marker string) string {
	if marker != IDEMarker {
		return ""
	}
	return "if this is your own IDE terminal and not a coding agent's, you can unset " + IDEMarker + " there yourself"
}

// AgentRefusal is why a run in a coding agent's session applies nothing; it
// names the marker, never offers --yes, and carries the IDE hint only for the
// marker an IDE extension also sets.
func AgentRefusal(marker string) string {
	msg := "a coding agent's session is present (" + marker + " is set), so nothing is applied: " + AgentAdvice
	if h := IDEHint(marker); h != "" {
		msg += " (" + h + ")"
	}
	return msg
}

// AgentError is the refusal of an applying step in a coding agent's session:
// the loop reports it as the user's to do, not as a failure.
type AgentError struct{ Marker string }

func (e *AgentError) Error() string { return AgentRefusal(e.Marker) }

// AgentProceeded is the other half of AgentRefusal: the sentence for the one
// case in which a step in a coding agent's session runs all the same — D55's
// answer, the managed project whose plan the gate admitted (Managed.Removes).
// It sits beside AgentRefusal because both are about the same marker, and
// because the reason is decided here: a caller that printed its own sentence
// would be a second copy of this rule.
//
// It names the marker, says what the step is proceeding on, and names no
// marker, variable or flag a reader could unset to get here — the same rule
// IDEHint follows, and for the same reason: a recipe for getting past the
// check has no business in text an agent reads. What it does NOT say is that
// the step is safe: the three things that replace the refusal (the gate, the
// owner's delete list, the marker) are named where they are decided, and the
// session that reads this is the one that could go around all three.
func AgentProceeded(marker string) string {
	return "a coding agent's session is present (" + marker + " is set), and this step proceeds: the project is managed and the plan passed the deterministic gate, which is what replaces the confirmation here (not a person's approval, and not a flag to pass)"
}

// ConfirmClass is who may confirm a step.
//
// The zero value is Typed, the strictest class, and that is deliberate: a
// caller that names no class — a field nobody set, a class left out of a
// table, a zero value passed on — refuses a confirmation rather than giving
// one. Every mistake in this file falls in the refusing direction (Managed's
// zero value is "not managed", and Covers and Removes are methods rather than
// fields so no caller can assert coverage), and the zero value of the class a
// step is confirmed under is the last place a mistake could fall the other
// way.
type ConfirmClass int

const (
	// Typed steps are money or permanent: a new project, billing, a secret, a
	// Firestore location, a billable image build, replacing a registry tag. A
	// person types the confirmation at a real terminal; --yes,
	// --non-interactive, --json, a pipe and a coding agent never give it.
	Typed ConfirmClass = iota
	// YesOrTyped steps take --yes, or the typed confirmation at a terminal:
	// the Terraform applies, the plugin wiring, a known repository.
	YesOrTyped
)

// Conditions are what a run says about who is there.
type Conditions struct {
	Yes, NonInteractive, JSON, Terminal bool
	Agent                               string // a coding agent's environment marker, "" for none
}

// Managed is the managed-project state a step's confirmation is decided
// under: the two facts about the plan that is about to be applied, which a
// caller has read and passes in. It exists so D55's answer — the coding
// agent's refusal is removed in a managed project — is one case of one rule
// rather than a second copy of it, and so a caller that has read nothing
// passes the zero value and keeps today's behaviour exactly.
//
// It is not a field of Conditions, and that is deliberate: Conditions is what
// a RUN says about who is there — its five fields are enumerated exactly by
// internal/cli's completeness test, and the property test runs over the whole
// product of them — while these two facts are about a plan ONE step is about
// to apply. A run has one Conditions, and one Managed per applying step.
//
// The zero value is "not managed", so every caller that does not know about
// managed projects — init's embedded roots, the plugin stage — refuses under a
// coding agent's marker as it did before this type existed, and the only
// caller that can reach the managed case is the managed apply itself.
type Managed struct {
	// Managed is whether the creation-fact read answered managed: true only
	// with a nil error (infra/managed.IsManaged's contract). A marker that is
	// absent, unreadable, of another project or of a version this build does
	// not know is false, and false is the answer that keeps the refusal.
	Managed bool
	// Admitted is whether the gate's verdict admitted THE PLAN OF THE STEP
	// BEING CONFIRMED (managed.Verdict.OK, Tasks 1.0-1.3). It is about this
	// step's plan and not about the project: a verdict for another plan, or a
	// refusal, or a gate that was never run, is false.
	Admitted bool
	// Fallback is whether the whole-project inventory (managed.Inventory,
	// design D59, wired in by the plan's Task 4.5) decided that this run keeps
	// its confirmations: a resource the installation's own state does not
	// account for, or a read that could not be made at all. It is the THIRD
	// fact of the managed case, and it is a field here rather than a caller's
	// decision for the reason this type exists: D59's fallback is exactly "the
	// confirmations this project's plan would otherwise not need", and the
	// removal of the coding agent's refusal IS that confirmation. So a
	// fallback suppresses Removes and nothing else — the plan may still be
	// applied, by a person who is asked, and never unattended beside a project
	// nobody looked at.
	//
	// The zero value is "no fallback", which keeps every caller that does not
	// know about the inventory exactly where it was; the caller that knows is
	// the managed apply, and it passes a value that falls back when the read
	// was not made (managed.InventoryCheck's own contract), so a caller that
	// forgets to thread the read through falls back rather than proceeds.
	Fallback bool
}

// Covers reports whether the gate covers a step of the class. The gate judges
// a saved plan, which is what an ordinary applying step is; a Typed step is
// money or permanent — a new project, billing, a secret, a Firestore location,
// a billable image build — and no plan gate says anything about any of them,
// so its confirmation is never removed. This is a method rather than a caller's
// field for that reason: the class is the step's own, and a caller that could
// assert coverage could assert it for a secret.
func (m Managed) Covers(class ConfirmClass) bool { return class == YesOrTyped }

// Removes reports whether the managed case removes the coding-agent refusal
// for a step of the class: every condition, held at once — the project is
// managed, the gate admitted this step's plan, the class is one the gate
// judges, and the whole-project inventory did not fall back. Any other
// combination keeps today's AgentRefusal exactly, which is the direction D59
// and D55 both end in: the confirmation a coding agent's session cannot give
// is kept wherever the inventory could not clear the project.
func (m Managed) Removes(class ConfirmClass) bool {
	return m.Managed && m.Admitted && m.Covers(class) && !m.Fallback
}

// CanConfirmManaged is the whole of the confirmation rule: CanConfirm is this
// function with a zero Managed, and the managed case is the branch below
// rather than a rule of its own. A step of a class the gate covers, in a
// project the marker read says is managed, whose plan the gate admitted, whose
// whole-project inventory did not fall back — proceeds under a coding agent's
// marker, and every other combination refuses, which is the direction the
// mistake has to fall in.
//
// Everything else is unchanged, and that is the property the tests hold: for
// every Conditions, a zero Managed decides what CanConfirm decided before the
// managed case existed.
func CanConfirmManaged(class ConfirmClass, c Conditions, m Managed) bool {
	switch {
	case c.Agent != "":
		// A coding agent's environment confirms nothing (AgentAdvice): it is
		// a mitigation against the accident, not a barrier against the
		// adversary, and the managed case does not claim to be one either.
		return m.Removes(class)
	case class == Typed:
		return c.Terminal && !c.Yes && !c.NonInteractive && !c.JSON
	}
	return c.Yes || (c.Terminal && !c.NonInteractive)
}

// CanConfirm reports whether a step of the class can be confirmed under the
// conditions. A coding agent's environment confirms nothing, unless the
// caller says (Managed) that the project is managed and the gate admitted the
// step's plan, which is the case CanConfirmManaged holds.
func CanConfirm(class ConfirmClass, c Conditions) bool {
	return CanConfirmManaged(class, c, Managed{})
}

// CanConfirmRun reports whether the run's one confirmation (the project's
// name typed once for all the ordinary steps) can be taken under the
// conditions: a person at a real terminal, as for a Typed step. --yes does
// not take it (it already covers the ordinary steps, with no review),
// --non-interactive, --json, a pipe and a coding agent never do, and under a
// coding agent's marker nothing applies at all. Plan-only runs show the
// review and never take it (the caller's; Yes is false there).
func CanConfirmRun(c Conditions) bool { return CanConfirm(Typed, c) }

// Validate checks stages are registered once each, under a known name, and
// returns them in the canonical order. A stage's predecessors that are not
// registered are not required (the stages later tasks provide may be
// absent), but every one that is registered is ahead of it.
func Validate(stages []Stage) ([]Stage, error) {
	pos := map[string]int{}
	for i, s := range order {
		pos[s.name] = i
	}
	byName := map[string]Stage{}
	for _, s := range stages {
		if s == nil {
			return nil, fmt.Errorf("initflow: a nil stage")
		}
		if _, ok := pos[s.Name()]; !ok {
			return nil, fmt.Errorf("initflow: unknown stage %q", s.Name())
		}
		if _, dup := byName[s.Name()]; dup {
			return nil, fmt.Errorf("initflow: stage %q registered twice", s.Name())
		}
		byName[s.Name()] = s
	}
	out := make([]Stage, 0, len(stages))
	for _, s := range order {
		if st, ok := byName[s.name]; ok {
			out = append(out, st)
		}
	}
	return out, nil
}
