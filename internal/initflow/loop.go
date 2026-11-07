package initflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"

	"github.com/dimipaun/fugaro/internal/report"
)

// Options is how one run behaves; the CLI fills it from the flags.
type Options struct {
	// Yes confirms the stages --yes covers (not project creation, billing
	// or a secret: see YesCovers).
	Yes bool
	// NonInteractive means no prompt is ever shown: a missing input is one
	// error naming every missing flag, and an apply needs Yes (else the run
	// only plans).
	NonInteractive bool
	// PlanOnly checks and plans every stage and changes nothing.
	PlanOnly bool
	// Terminal is whether stdin is a terminal, where a confirmation can be
	// typed.
	Terminal bool
	// JSON is --json: it never prompts for what --yes does not cover.
	JSON bool
	// Agent is the environment marker of a coding agent's session (CLAUDECODE
	// and the like), "" for none. With one set nothing is applied, --yes or
	// not: every applying stage is needs-you ("run this in your own terminal
	// window"); checks and plans still run. A mitigation, not a barrier.
	Agent string
	// ManagedGate is the managed project's gate (D1016), when the caller has
	// one, asked per stage: true means the project is managed and this stage
	// is one the gate judges, so the loop applies it here and the stage's own
	// steps judge their own documents at the point of each write — a step the
	// gate refuses comes back as this stage's needs-you, in the gate's own
	// words (GatedError) and not in the session's.
	//
	// It is asked in a coding agent's session and in the owner's own terminal
	// (the ruling of 2026-11-01: a managed project converges with no prompt),
	// and never in a run nobody is at: a pipe, `--json` and `--non-interactive`
	// keep the refusal they always had, because what the gate stands in for is
	// a document a person reads.
	//
	// It is a bool and not an error because every way it can go wrong is the
	// same answer: a mark that is absent, unreadable or of another project is
	// "not managed", which is today's refusal word for word, and a caller that
	// could not tell would otherwise have to invent a third sentence for it.
	// The gate is never a widening — a nil field is every caller that is not a
	// command in the managed set, and this field can only ever hand a stage to
	// the loop's own apply path, which is the same path LocalUnderAgent takes.
	ManagedGate func(stage string) bool
	// OnPreview, when set, receives the read-only preview's stages before the
	// first apply of an applying run (the one confirmation's review screen
	// says what is left to do, and what asks separately, from it).
	OnPreview func([]StageResult)
	// Out receives the human-readable account (the plan view and the
	// closing lines); with --json the CLI points it at stderr.
	Out io.Writer
	// Report, when the caller has one, is the reporter the run's account is
	// printed through: one step per stage, with the marker its state earns,
	// and the closing line. The plan view is not a run and keeps its own
	// shape (a run that reported no step prints no closing line), and the
	// left-for-you block and the stopped-at line print as they always have.
	//
	// A nil reporter is the loop's own account, word for word: a caller that
	// has not moved onto the reporter prints exactly what it printed before.
	// The caller points the reporter and Out at the same writer, so the two
	// are one view.
	Report *report.Reporter
	// ReportBorrowed says the reporter is the caller's and not this run's, so
	// the loop prints its steps and the blocks it owes and leaves the closing
	// line to whoever opened the reporter. A run whose steps do not end with
	// the loop — `accounts add` accounts for eight of them and runs three
	// through the loop, with the read-back after it — would otherwise be
	// accounted "Done" in the middle of itself, and a step that failed after
	// that line exits 1 behind an account that says the run finished (G3).
	//
	// False is a run whose reporter is its own: the converge (init.go) opens
	// it, the loop is the whole of the run, and the closing line is its last
	// word.
	ReportBorrowed bool
	// Program is the command the run's own lines name: the front door the
	// caller is running ("fugaro setup", "fugaro update"). The loop is
	// behind both and cannot know which one it is, so it never names a
	// command of its own. Empty names the program alone ("fugaro"), which
	// is all a caller that does not name itself gets.
	Program string
	// Project, GCPProject and Region head the plan view.
	Project, GCPProject, Region string
	// Redact lists values (a secret the process holds) that must never
	// appear in any output, error or JSON; they are replaced before
	// anything is recorded or printed.
	Redact []string
}

// StageResult is one stage's line in the result.
type StageResult struct {
	Name   string `json:"name"`
	State  State  `json:"state"`
	Detail string `json:"detail,omitempty"`
	// After names the stage a will-do stage waits for: the plan cannot be
	// complete up front (the Firebase root needs the installation's
	// outputs), and the view says so.
	After string `json:"after,omitempty"`
}

// Failure is the stage a run stopped at, in one line each.
type Failure struct {
	Stage string `json:"stage"`
	Error string `json:"error"`
	Fix   string `json:"fix"`
}

// Result is what a run reports, as the human view and as data.
type Result struct {
	Project    string        `json:"project"`
	GCPProject string        `json:"gcp_project"`
	Region     string        `json:"region"`
	PlanOnly   bool          `json:"plan_only"`
	Stages     []StageResult `json:"stages"`
	Left       []Left        `json:"left_for_you"`
	Failed     *Failure      `json:"failed,omitempty"`
	Note       string        `json:"note,omitempty"`

	// Cause is the failed stage's own error, for a caller that keeps its
	// exit code and type.
	Cause error `json:"-"`
}

// ExitCode is 2 for a failed stage, 1 when the user has to do something
// before the converge can finish (a script must not read that as done),
// else 0.
func (r *Result) ExitCode() int {
	switch {
	case r.Failed != nil:
		return 2
	case len(r.Left) > 0:
		return 1
	}
	return 0
}

// JSON is the result as stable, indented JSON: fixed field order, and
// left_for_you an array even when empty.
func (r *Result) JSON() ([]byte, error) {
	c := *r
	if c.Left == nil {
		c.Left = []Left{}
	}
	if c.Stages == nil {
		c.Stages = []StageResult{}
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Run converges the stages (in the canonical order, whatever order they
// are passed in). It returns an error only for a refusal before any stage
// changed anything: an unknown stage, missing flags under --non-interactive,
// or a confirmation that cannot be had without a terminal. Everything else,
// a failed stage included, is in the Result.
func Run(ctx context.Context, stages []Stage, o Options) (*Result, error) {
	stages, err := Validate(stages)
	if err != nil {
		return nil, err
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	l := &loop{o: o, res: &Result{Project: o.Project, GCPProject: o.GCPProject, Region: o.Region, Left: []Left{}}}

	if o.NonInteractive {
		var missing []string
		for _, s := range stages {
			if in, ok := s.(InputStage); ok {
				missing = append(missing, in.Missing()...)
			}
		}
		if len(missing) > 0 {
			return nil, &MissingInputsError{Flags: l.redactAll(missing)}
		}
		if !o.Yes && !o.PlanOnly {
			// An apply needs --yes; without it the run is --plan-only.
			l.o.PlanOnly = true
			l.res.Note = "--non-interactive without --yes only plans; pass --yes to apply"
		}
	}
	l.res.PlanOnly = l.o.PlanOnly
	l.interactive = o.Terminal && !o.NonInteractive

	if err := l.preview(ctx, stages); err != nil {
		return nil, err
	}
	if l.o.PlanOnly {
		l.finish()
		return l.res, nil
	}
	err = l.converge(ctx, stages)
	l.finish()
	// A refusal comes with the partial result: nothing was applied before
	// it, and a caller printing JSON still has the account to print.
	return l.res, err
}

type loop struct {
	o           Options
	res         *Result
	interactive bool
	// failedContext is the failed stage's error under its first line: the
	// lines the reporter prints on the screen beside the message, because a
	// coding agent's session often cannot open the log (G8). It is not part
	// of the Result: --json carries the error and the fix, as it always has.
	failedContext []string
	// refused is set when converge stopped on a refusal rather than on a
	// stage's own result: the error is the caller's, and the exit code that
	// goes with it is not the account's, so the reporter has no outcome to
	// close the run with and prints no closing line.
	refused bool
	// executing is the execution pass (converge): the results it records are
	// the run's account and print as the stages happen. The plan view's own
	// rows (preview) are the review screen, and print as the one block they
	// always were.
	executing bool
	// step is the line in flight for the stage the run is on, opened when the
	// loop reaches it (openStep) and ended by the result it records (record):
	// what a person watches through a stage's minutes. stepStage is the stage
	// it belongs to. Both are nil when no reporter is drawing one.
	step      *report.Step
	stepStage string
}

// defaultProgram is what the run's lines name when the caller names no
// program: the binary, because the loop cannot know the verb.
const defaultProgram = "fugaro"

// program is the name the run's own lines use: the caller's Program, or the
// program alone when it did not name one. It goes through the same redaction
// as every other line the loop prints: the name reaches a header, a fix text
// and --json, and a caller's string is not trusted to be free of a value the
// process holds.
func (l *loop) program() string {
	if l.o.Program == "" {
		return defaultProgram
	}
	return l.redact(l.o.Program)
}

func (l *loop) redact(s string) string {
	for _, v := range l.o.Redact {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	return s
}

func (l *loop) redactAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = l.redact(s)
	}
	return out
}

// oneLine keeps text to its first non-blank line, with no continuation
// backslash and no control character (an escape sequence in an error from a
// remote service must not reach the terminal): a stage's one-line
// instruction is one line wherever it is printed.
func oneLine(s string) string {
	s = strings.TrimLeft(s, "\r\n \t")
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "\\"))
}

func (l *loop) left(stage string, lf Left) Left {
	lf.Stage = stage
	if lf.Kind == "" {
		lf.Kind = LeftCommand
	}
	lf.Text = l.redact(oneLine(lf.Text))
	if len(lf.Commands) > 0 {
		lf.Commands = l.redactAll(lf.Commands)
		for i, c := range lf.Commands {
			lf.Commands[i] = oneLine(c)
		}
	}
	return lf
}

// preview checks every stage (read-only) and, in a plan-only run, plans the
// first one with something to do. A stage that waits for one with something
// to do is shown as will-do with the stage it waits for, and the wait is
// converge's own: a stop holds everything after it that does not run past it,
// while a project paused for billing holds only what needs billing
// (NeedsBilling). It prints the view first in an applying run, and the whole
// result in a plan-only one.
func (l *loop) preview(ctx context.Context, stages []Stage) error {
	var waiting string // the first stage with something to do or someone to wait for
	var paused string  // the stage that paused for billing: what needs billing waits for it
	var stops []stop   // the stops the stages after them wait for, and those they run past
	for _, s := range stages {
		name := s.Name()
		if l.cancelled(ctx, name) {
			return nil
		}
		l.pastStops(s, name, stops)
		st, err := checkStage(ctx, s)
		if err != nil {
			l.fail(name, err)
			return nil
		}
		r := StageResult{Name: name, State: st.State, Detail: l.redact(oneLine(st.Detail))}
		switch st.State {
		case Done, Skipped:
		case Blocked:
			if waiting == "" {
				waiting = name
			}
			stops = append(stops, stop{name: name})
		case NeedsYou, BillingPending:
			lf := s.Left()
			if st.Left != nil {
				lf = *st.Left
			}
			l.res.Left = append(l.res.Left, l.left(name, lf))
			if st.State == BillingPending {
				// Not a stop, and not what the stages after it wait for:
				// converge pauses there and lets the stages billing does not
				// gate run on (NeedsBilling), so the wait is said below, at
				// the stages that have it.
				paused = name
				break
			}
			if waiting == "" {
				waiting = name
			}
			stops = append(stops, stop{name: name, yours: true})
		default:
			r.State = Todo
			if b := blocker(name, stops); b != nil {
				r.After = b.name
			} else if paused != "" && NeedsBilling(name) {
				r.After = paused
			} else {
				if waiting != "" && stoppedYours(waiting, stops) {
					// The stage the view was waiting for is a stop this one
					// does not read: it is not what this stage waits for, so
					// the stages after it are not waiting for it either. Only
					// a stop is cleared this way: a stage with something to do
					// is one the run applies first, and this stage waits for
					// it like every other stage behind it.
					waiting = ""
				}
				if waiting != "" {
					r.After = waiting
				} else {
					waiting = name
					if l.o.PlanOnly {
						p, err := planStage(ctx, s, l.env(name))
						if err != nil {
							l.fail(name, err)
							return nil
						}
						if d := oneLine(p.Detail); d != "" {
							r.Detail = l.redact(d)
						}
						if p.NothingToDo {
							// The real plan is empty: nothing waits for this stage.
							r.State, waiting = Done, ""
							if r.Detail == "" {
								r.Detail = "No changes"
							}
						}
					}
				}
			}
		}
		l.res.Stages = append(l.res.Stages, r)
	}
	if !l.o.PlanOnly {
		l.view(l.res.Stages)
		if l.o.OnPreview != nil {
			l.o.OnPreview(slices.Clone(l.res.Stages))
		}
		// The execution pass owns the final account.
		l.res.Stages, l.res.Left = nil, []Left{}
	}
	return nil
}

// env is what a stage is told. A plan-only run never has --yes: it creates
// nothing, so a stage's plan that needs a prerequisite made (the state bucket,
// an API) takes the typed confirmation or stops needs-you.
func (l *loop) env(stage string) Env {
	return Env{Yes: l.o.Yes && !l.o.PlanOnly && YesCovers(stage), Interactive: l.interactive}
}

// fail records a failed stage and blocks what follows it.
func (l *loop) fail(stage string, err error) {
	f := &Failure{Stage: stage, Error: l.redact(oneLine(err.Error())), Fix: "fix what the error says and rerun " + l.program() + ": it resumes at " + stage}
	var se *StageError
	if errors.As(err, &se) && se.Fix != "" {
		f.Fix = l.redact(oneLine(se.Fix))
	}
	l.res.Failed, l.res.Cause = f, l.redactedCause(err)
	l.failedContext = l.failureContext(err)
	// After the context, which the line the reporter prints carries under it.
	l.record(StageResult{Name: stage, State: Failed, Detail: f.Error})
}

// failureContext is a stage's error under its first line: the last lines of
// what went wrong, which the failure block prints on the screen. The first
// line is the message the result carries (oneLine keeps it to one), so what
// is left here is the context behind it.
func (l *loop) failureContext(err error) []string {
	lines := strings.Split(err.Error(), "\n")
	// The message is the first line oneLine does not trim away, so the blank
	// lines above it are not context — and taking the line after them is what
	// keeps the message from printing twice under the cross.
	for len(lines) > 0 && strings.Trim(lines[0], "\r\n \t") == "" {
		lines = lines[1:]
	}
	if len(lines) == 0 {
		return nil
	}
	var out []string
	for _, ln := range lines[1:] {
		if s := l.redact(oneLine(ln)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// stop is a stage the run stopped at, and whether what stopped it is the
// user's to clear.
type stop struct {
	name string
	// yours is whether the stop is a step only the user can give (NeedsYou),
	// which is the one stop a later stage may run past (RunsPastStop). A
	// failure and a block a stage's own Check reported are the user's to fix
	// too, and are still not: nothing is known about the state they left.
	yours bool
}

// blocker is the first stop before name that name may not run past, nil when
// it runs past every one of them. It is the whole of the gating rule: the
// stages are linear, and the tolerance is the stopped stage's own (RunsPastStop).
func blocker(name string, stops []stop) *stop {
	for i := range stops {
		if !stops[i].yours || !RunsPastStop(name, stops[i].name) {
			return &stops[i]
		}
	}
	return nil
}

// stoppedYours is whether name is one of the stops of stops that is the user's
// to clear: the only kind a later stage's row runs past (RunsPastStop). A
// stage with something to do is not one of them, however close the row's
// tolerance comes to it.
func stoppedYours(name string, stops []stop) bool {
	for _, s := range stops {
		if s.name == name && s.yours {
			return true
		}
	}
	return false
}

// pastStops tells a stage the stops it is running past (PastStops), before the
// loop reads anything of it. They are the run's own, and a stage whose own
// steps are not all alike about a stop has no other way to them: the world
// records what the stopped stage left behind, never that the run stopped.
func (l *loop) pastStops(s Stage, name string, stops []stop) {
	x, ok := s.(PastStops)
	if !ok {
		return
	}
	var out []string
	for _, st := range stops {
		if st.yours && RunsPastStop(name, st.name) {
			out = append(out, st.name)
		}
	}
	x.PastStops(out)
}

// converge applies, in order, each stage that is not done, re-checking it
// right before and verifying it right after, and stops at the first stage
// that needs the user or fails: nothing after it runs, except the stages that
// run past that stop (RunsPastStop).
func (l *loop) converge(ctx context.Context, stages []Stage) error {
	l.res.Stages, l.res.Left, l.res.Failed, l.res.Cause = nil, []Left{}, nil, nil
	l.failedContext = nil
	l.executing = true
	var stops []stop // the stages the run stopped at: what everything after waits for
	paused := ""     // the stage that paused for billing: what needs billing waits for it
	for _, s := range stages {
		name := s.Name()
		if b := blocker(name, stops); b != nil {
			l.record(StageResult{Name: name, State: Blocked, Detail: "waits for " + b.name})
			continue
		}
		if paused != "" && NeedsBilling(name) {
			l.record(StageResult{Name: name, State: WaitingForBilling, Detail: "waiting for billing"})
			continue
		}
		l.pastStops(s, name, stops)
		// The line in flight goes up before the stage is looked at: its check
		// and its plan are seconds, and the apply behind them is minutes.
		l.openStep(name)
		if l.cancelled(ctx, name) {
			return nil
		}
		st, err := checkStage(ctx, s)
		if err != nil {
			l.fail(name, err)
			stops = append(stops, stop{name: name})
			continue
		}
		detail := l.redact(oneLine(st.Detail))
		switch st.State {
		case Done, Skipped:
			l.record(StageResult{Name: name, State: st.State, Detail: detail})
			continue
		case Blocked:
			l.record(StageResult{Name: name, State: Blocked, Detail: detail})
			stops = append(stops, stop{name: name})
			continue
		case NeedsYou:
			lf := s.Left()
			if st.Left != nil {
				lf = *st.Left
			}
			l.needsYou(name, detail, lf)
			stops = append(stops, stop{name: name, yours: true})
			continue
		case BillingPending:
			lf := s.Left()
			if st.Left != nil {
				lf = *st.Left
			}
			l.billingPending(name, detail, lf)
			paused = name
			continue
		}

		// Something to apply.
		if l.o.Agent != "" {
			if done, err := l.agentStage(ctx, s); err != nil {
				l.fail(name, err)
				stops = append(stops, stop{name: name})
				continue
			} else if done {
				continue
			}
			stops = append(stops, stop{name: name, yours: true})
			continue
		}
		// The managed project's gate, for the owner at their own terminal:
		// the same path the agent's session takes below, and the reason it is
		// asked here and not only there is the owner's ruling of 2026-11-01 —
		// a managed project is one Fugaro converges on their behalf, with no
		// prompt, whoever started the run. The stage's own steps judge their
		// own documents at the point of each write, so what this opens is the
		// run, not the write; a step the gate did not admit comes back as a
		// GatedError and stops the stage in the gate's own sentence.
		//
		// A run nobody is at is not this case: interactive is a real terminal
		// with no --non-interactive, and --json is asked for here because a
		// document a person reads is what the gate stands in for. A pipe
		// without --yes keeps its refusal (NoTerminalError below), which is
		// the one thing a managed project must never lose: an unattended run
		// that applies because a mark is present.
		if l.interactive && !l.o.JSON && l.o.ManagedGate != nil && l.o.ManagedGate(name) {
			if done, err := l.gatedStage(ctx, s); err != nil {
				l.fail(name, err)
				stops = append(stops, stop{name: name})
				continue
			} else if done {
				continue
			}
			stops = append(stops, stop{name: name, yours: true})
			continue
		}
		if !YesCovers(name) && (!l.interactive || l.o.JSON) {
			// Never auto-confirmed, never prompted: left for the user.
			l.needsYou(name, "needs your own confirmation, which --yes does not give", s.Left())
			stops = append(stops, stop{name: name, yours: true})
			continue
		}
		if _, self := s.(SelfConfirming); !l.o.Yes && !l.interactive && !(self && YesCovers(name)) {
			l.refused = true
			return &NoTerminalError{Stage: name, Agent: l.o.Agent}
		}
		out, err := applyStage(ctx, s, l.env(name))
		var ny *NeedsYouError
		var ag *AgentError
		var bp *BillingPausedError
		if errors.As(err, &bp) {
			l.billingPending(name, bp.Detail, bp.Left)
			paused = name
			continue
		}
		if errors.As(err, &ag) {
			l.needsYou(name, ag.Error(), s.Left())
			stops = append(stops, stop{name: name, yours: true})
			continue
		}
		if errors.As(err, &ny) {
			l.needsYou(name, "", ny.Left)
			stops = append(stops, stop{name: name, yours: true})
			continue
		}
		if err == nil {
			err = verifyStage(ctx, s)
		}
		if err != nil {
			l.fail(name, err)
			stops = append(stops, stop{name: name})
			continue
		}
		l.applied(name, out)
	}
	return nil
}

// applied records a stage's line after its apply, and the Left beside it when
// the stage applied what it could and left a step for the user (Outcome.Left):
// the stage is done with what this session may do, and the run still has
// something only the user can do, which is what the exit code has to say.
func (l *loop) applied(stage string, out Outcome) {
	r := StageResult{Name: stage, State: Changed, Detail: l.redact(oneLine(out.Detail))}
	if !out.Changed {
		r.State = Done
	}
	// The Left first: the stage's own line is a left-for-you marker when the
	// stage applied what this session may do and left the rest (G3), and the
	// line prints the moment it is recorded.
	if out.Left != nil {
		l.res.Left = append(l.res.Left, l.left(stage, *out.Left))
	}
	l.record(r)
}

// agentStage is a stage's turn in a coding agent's session, which applies
// nothing. A stage that can only say what it would do by planning (an engine)
// plans, read-only: an empty plan is done, anything else is the user's. done
// is true when the stage needs nothing and the loop goes on.
func (l *loop) agentStage(ctx context.Context, s Stage) (done bool, err error) {
	name := s.Name()
	if _, local := s.(LocalUnderAgent); local {
		return l.localStage(ctx, s)
	}
	// The managed project's gate comes before the read-only plan below: a
	// gated stage is applied here, which is the one case in which a coding
	// agent's session writes anything (D1016), and the stage's own steps are
	// what judge it.
	if l.o.ManagedGate != nil && l.o.ManagedGate(name) {
		return l.gatedStage(ctx, s)
	}
	if _, self := s.(SelfConfirming); self {
		p, perr := planStage(ctx, s, l.env(name))
		var ag *AgentError
		var ny *NeedsYouError
		switch {
		case errors.As(perr, &ag):
		case errors.As(perr, &ny):
			l.needsYou(name, "", ny.Left)
			return false, nil
		case perr != nil:
			return false, perr
		case p.NothingToDo:
			d := oneLine(p.Detail)
			if d == "" {
				d = "No changes"
			}
			l.record(StageResult{Name: name, State: Done, Detail: l.redact(d)})
			return true, nil
		}
	}
	l.needsYou(name, AgentRefusal(l.o.Agent), s.Left())
	return false, nil
}

// localStage is a LocalUnderAgent stage's turn in a coding agent's session.
// Every write of the stage's own is on this machine (see LocalUnderAgent), so
// the loop applies it and leaves the confirmations to the stage: an AgentError
// it returns is the same needs-you it is in the ordinary path, and it is what
// keeps whatever the stage would have done outside the machine undone.
func (l *loop) localStage(ctx context.Context, s Stage) (done bool, err error) {
	return l.agentApplied(ctx, s)
}

// gatedStage is a stage the managed project's gate opened in a coding agent's
// session (D1016): the loop applies it, exactly as it applies a
// LocalUnderAgent stage, because the gate's admission is per step and it is
// the stage's own steps that judge their own documents before they write. The
// difference between the two is only why the stage runs — a write that is
// this machine's own, or a plan the gate admitted — so the handling of what
// comes back is one function: a step the gate refused is a GatedError, and it
// is the user's to do in the gate's own sentence, which names the document
// rather than the session.
func (l *loop) gatedStage(ctx context.Context, s Stage) (done bool, err error) {
	return l.agentApplied(ctx, s)
}

// agentApplied applies a stage in a coding agent's session and sorts what it
// returned by who the step is for: the session's own refusal, a step left to
// the user, and the gate's refusal are all the user's (needs-you, and the
// stage stops without failing the run), and anything else is a failure.
func (l *loop) agentApplied(ctx context.Context, s Stage) (done bool, err error) {
	name := s.Name()
	out, err := applyStage(ctx, s, l.env(name))
	var ag *AgentError
	var ny *NeedsYouError
	var ge *GatedError
	switch {
	case errors.As(err, &ag):
		l.needsYou(name, ag.Error(), s.Left())
		return false, nil
	case errors.As(err, &ge):
		// The gate's sentence is the detail, and the stage's own Left is where
		// the user is told to run it: the step is theirs to confirm, at their
		// own terminal, and the gate says nothing a session could act on.
		l.needsYou(name, ge.Error(), s.Left())
		return false, nil
	case errors.As(err, &ny):
		l.needsYou(name, "", ny.Left)
		return false, nil
	case err != nil:
		return false, err
	}
	if err := verifyStage(ctx, s); err != nil {
		return false, err
	}
	l.applied(name, out)
	return true, nil
}

// billingPending records a stage that paused for billing: its Left is the
// step left for you, and it is not a failure.
func (l *loop) billingPending(stage, detail string, lf Left) {
	l.res.Left = append(l.res.Left, l.left(stage, lf))
	l.record(StageResult{Name: stage, State: BillingPending, Detail: l.redact(oneLine(detail))})
}

func (l *loop) needsYou(stage, detail string, lf Left) {
	l.res.Left = append(l.res.Left, l.left(stage, lf))
	l.record(StageResult{Name: stage, State: NeedsYou, Detail: detail})
}

// record adds a stage's result to the run and, in the execution pass, prints
// its line as the stage ends rather than in one block at the end of the run:
// a stage that takes minutes — a Terraform apply, an image build — leaves a
// terminal nobody is drawing on, and a person who reads that as a hang stops
// the run in the middle of an apply. A run with no reporter records exactly
// what it recorded before, and the plan view's rows are not a run at all.
func (l *loop) record(r StageResult) {
	l.res.Stages = append(l.res.Stages, r)
	if l.o.Report == nil || !l.executing {
		return
	}
	l.endStep(r)
}

// openStep starts the stage's line in flight, before the stage is even looked
// at: its checks and its plan are seconds and what follows them is minutes,
// and a stage somebody is watching is a stage nobody interrupts.
func (l *loop) openStep(name string) {
	if l.o.Report == nil || !l.executing {
		return
	}
	l.step, l.stepStage = l.o.Report.Step(name), name
}

// closeStep takes down a line in flight that no result ended: a refusal stops
// the run before the stage it was started for has one, and a line in flight
// must not outlive the run that drew it. It prints nothing and counts
// nothing, so a refused run's account is what it was.
func (l *loop) closeStep() {
	if l.step == nil {
		return
	}
	l.step.Abandon()
	l.step, l.stepStage = nil, ""
}

var stateLabel = map[State]string{
	Done: "done", Changed: "changed", Todo: "will do", Blocked: "blocked",
	NeedsYou: "needs you", Skipped: "skipped", Failed: "failed",
	BillingPending: "billing pending", WaitingForBilling: "waiting for billing",
}

// view prints the plan view: one line per stage.
func (l *loop) view(rs []StageResult) {
	if l.o.Project != "" {
		fmt.Fprintf(l.o.Out, "%s: project %s (GCP %s, %s)\n", l.program(), l.o.Project, l.o.GCPProject, l.o.Region)
	}
	for _, r := range rs {
		line := fmt.Sprintf("  %-12s %-16s", "["+stateLabel[r.State]+"]", r.Name)
		switch {
		case r.After != "" && r.Detail != "":
			line += " " + r.Detail + " (after " + r.After + ")"
		case r.After != "":
			line += " after " + r.After
		case r.Detail != "":
			line += " " + r.Detail
		}
		fmt.Fprintln(l.o.Out, strings.TrimRight(line, " "))
	}
}

// endStep prints one stage's line through the reporter: the marker the
// stage's state earns, which is the line the run's account has always been,
// printed where the stage ends instead of at the end of the run. The stage's
// own line in flight, when there is one, is the line it replaces.
func (l *loop) endStep(r StageResult) {
	s := l.step
	if s == nil || l.stepStage != r.Name {
		// A stage nothing was opened for: one that waits for a stage before
		// it. Its whole line prints at once, the moment the loop reaches it.
		s = l.o.Report.Step(r.Name)
	}
	l.step, l.stepStage = nil, ""
	switch r.State {
	case Done, Changed:
		if l.leftFor(r.Name) != nil {
			// The stage applied what this session may do and left the rest
			// for the user (Outcome.Left): the result carries the entry and
			// the run exits 1, so a tick here would say the run is done when
			// it is not (G3).
			s.Left(l.stageText(r))
		} else {
			s.Done(r.Detail)
		}
	case Failed:
		// The stage's detail is the error's first line; the lines under it
		// are the context the failure block prints (G8).
		s.Failed(errors.New(r.Detail), l.failedContext)
	case NeedsYou, BillingPending, WaitingForBilling:
		// The user still owes this stage — a confirmation, a billing
		// account — so it is never a tick, whatever it says (G3).
		s.Left(l.stageText(r))
	default:
		// Skipped and blocked: the stage did not run, and what it waits for
		// is not this session's to give.
		s.Skipped(r.Detail)
	}
}

// leftFor is the step the user still owes for a stage: a stage that only
// refused has one, and so does a stage that applied what it could and left
// the rest (Outcome.Left), which is why a stage's marker follows this and not
// its state alone (G3).
func (l *loop) leftFor(stage string) *Left {
	for i, lf := range l.res.Left {
		if lf.Stage == stage {
			return &l.res.Left[i]
		}
	}
	return nil
}

// stageText is the one-line tail of a stage the user owes: what the stage
// said about itself, or, when it said nothing — a stage that only refused —
// the instruction its left-for-you entry carries.
func (l *loop) stageText(r StageResult) string {
	if r.Detail != "" {
		return r.Detail
	}
	if lf := l.leftFor(r.Name); lf != nil {
		return lf.Text
	}
	return ""
}

// finish prints the closing account of the run.
func (l *loop) finish() {
	if l.o.PlanOnly {
		l.view(l.res.Stages)
		if l.res.Note != "" {
			fmt.Fprintln(l.o.Out, l.res.Note)
		}
	} else if l.o.Report != nil {
		// The account is already on the screen: every step printed as its
		// stage ended (record), so what is left is the closing line below
		// and the blocks a run owes the user.
		l.closeStep()
	} else {
		fmt.Fprintf(l.o.Out, "%s: result\n", l.program())
		l.view(l.res.Stages)
	}
	for _, lf := range l.res.Left {
		fmt.Fprintf(l.o.Out, "left for you (%s, %s): %s\n", lf.Stage, lf.Kind, lf.Text)
		for _, c := range lf.Commands {
			fmt.Fprintf(l.o.Out, "    %s\n", c)
		}
	}
	if f := l.res.Failed; f != nil {
		// The error itself is the caller's to print (the CLI returns it).
		fmt.Fprintf(l.o.Out, "stopped at %s; fix: %s\n", f.Stage, f.Fix)
	}
	if l.o.Report != nil && !l.o.PlanOnly && !l.refused && !l.o.ReportBorrowed {
		// Last, so the closing line is the last thing the run says: the
		// reporter counts the steps it was given and names the outcome.
		// A refused run has no outcome to name — the caller's exit code is
		// not the account's — so it says nothing more (G3), and neither does a
		// borrowed reporter, whose owner prints the line where its own run
		// ends (ReportBorrowed).
		l.o.Report.Summary()
	}
}

// redactedError is a cause whose message is redacted but whose type chain
// (an ExitError's code) is kept.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

func (l *loop) redactedCause(err error) error {
	if msg := l.redact(err.Error()); msg != err.Error() {
		return &redactedError{msg: msg, err: err}
	}
	return err
}

// cancelled records a cancelled context as the failure of the stage about
// to run, so nothing after it starts.
func (l *loop) cancelled(ctx context.Context, stage string) bool {
	if err := ctx.Err(); err != nil {
		l.fail(stage, &StageError{Err: fmt.Errorf("cancelled: %w", err), Fix: "rerun " + l.program() + ": it resumes at " + stage})
		return true
	}
	return false
}

// A stage's panic is its failure, not the process's.
func recovered(err *error, stage, what string) {
	if p := recover(); p != nil {
		*err = fmt.Errorf("stage %s panicked in %s: %v", stage, what, p)
	}
}

func checkStage(ctx context.Context, s Stage) (st Status, err error) {
	defer recovered(&err, s.Name(), "Check")
	if st, err = s.Check(ctx); err != nil {
		return st, err
	}
	switch st.State {
	case Todo, Done, Skipped, Blocked, NeedsYou, BillingPending:
		return st, nil
	}
	return st, fmt.Errorf("stage %s: Check returned the state %q, which a check may not (done, will-do, blocked, needs-you, billing-pending or skipped)", s.Name(), st.State)
}

func planStage(ctx context.Context, s Stage, env Env) (p Plan, err error) {
	defer recovered(&err, s.Name(), "Plan")
	return s.Plan(ctx, env)
}

func applyStage(ctx context.Context, s Stage, env Env) (o Outcome, err error) {
	defer recovered(&err, s.Name(), "Apply")
	return s.Apply(ctx, env)
}

func verifyStage(ctx context.Context, s Stage) (err error) {
	defer recovered(&err, s.Name(), "Verify")
	return s.Verify(ctx)
}
