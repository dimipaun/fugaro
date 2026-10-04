// Package initflow is the converge loop behind fugaro init (design doc
// m11-setup-and-skills.md §3.1): an ordered list of stages, each with a
// read-only check and plan, a confirmed apply and a verify; a loop that
// applies the first stage that is not done, stops at the first stage that
// needs the user or fails, and runs nothing after it; and the plan view and
// the JSON result that report where the run stands.
//
// The package knows nothing about Google, Terraform or the CLI: the stages
// that exist (and the fakes the tests use) plug in through Stage. What a
// stage must never be able to do is enforced here, not in the stage: --yes
// does not reach a stage that creates a project, links billing or takes a
// secret, no stage runs before the ones ahead of it are done, and a run
// that cannot prompt never prompts.
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
)

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
	{name: Repository, needs: []string{Installation2, Secrets}},
}

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

// MissingInputsError is --non-interactive's single refusal, listing every
// flag every stage still needs.
type MissingInputsError struct{ Flags []string }

func (e *MissingInputsError) Error() string {
	return "--non-interactive: missing " + strings.Join(e.Flags, "; ")
}

// NoTerminalError is the refusal to prompt without a terminal.
type NoTerminalError struct{ Stage string }

func (e *NoTerminalError) Error() string {
	return NeedsTerminal("typing the confirmation for "+e.Stage) + ", or pass --yes (with --non-interactive in scripts) once you have read what it does"
}

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
