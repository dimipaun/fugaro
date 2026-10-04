package initflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
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
	// Out receives the human-readable account (the plan view and the
	// closing lines); with --json the CLI points it at stderr.
	Out io.Writer
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
	return lf
}

// preview checks every stage (read-only) and, in a plan-only run, plans the
// first one with something to do. A stage that waits for one with something
// to do is shown as will-do with the stage it waits for. It prints the view
// first in an applying run, and the whole result in a plan-only one.
func (l *loop) preview(ctx context.Context, stages []Stage) error {
	var waiting string // the first stage with something to do or someone to wait for
	for _, s := range stages {
		name := s.Name()
		if l.cancelled(ctx, name) {
			return nil
		}
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
		case NeedsYou:
			lf := s.Left()
			if st.Left != nil {
				lf = *st.Left
			}
			l.res.Left = append(l.res.Left, l.left(name, lf))
		default:
			r.State = Todo
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
		if st.State == NeedsYou && waiting == "" {
			waiting = name
		}
		l.res.Stages = append(l.res.Stages, r)
	}
	if !l.o.PlanOnly {
		l.view(l.res.Stages)
		// The execution pass owns the final account.
		l.res.Stages, l.res.Left = nil, []Left{}
	}
	return nil
}

func (l *loop) env(stage string) Env {
	return Env{Yes: l.o.Yes && YesCovers(stage), Interactive: l.interactive}
}

// fail records a failed stage and blocks what follows it.
func (l *loop) fail(stage string, err error) {
	f := &Failure{Stage: stage, Error: l.redact(oneLine(err.Error())), Fix: "fix what the error says and rerun fugaro init: it resumes at " + stage}
	var se *StageError
	if errors.As(err, &se) && se.Fix != "" {
		f.Fix = l.redact(oneLine(se.Fix))
	}
	l.res.Failed, l.res.Cause = f, l.redactedCause(err)
	l.res.Stages = append(l.res.Stages, StageResult{Name: stage, State: Failed, Detail: f.Error})
}

// converge applies, in order, each stage that is not done, re-checking it
// right before and verifying it right after, and stops at the first stage
// that needs the user or fails: nothing after it runs.
func (l *loop) converge(ctx context.Context, stages []Stage) error {
	l.res.Stages, l.res.Left, l.res.Failed, l.res.Cause = nil, []Left{}, nil, nil
	stopped := "" // the stage everything after waits for
	for _, s := range stages {
		name := s.Name()
		if stopped != "" {
			l.res.Stages = append(l.res.Stages, StageResult{Name: name, State: Blocked, Detail: "waits for " + stopped})
			continue
		}
		if l.cancelled(ctx, name) {
			return nil
		}
		st, err := checkStage(ctx, s)
		if err != nil {
			l.fail(name, err)
			stopped = name
			continue
		}
		detail := l.redact(oneLine(st.Detail))
		switch st.State {
		case Done, Skipped:
			l.res.Stages = append(l.res.Stages, StageResult{Name: name, State: st.State, Detail: detail})
			continue
		case Blocked:
			l.res.Stages = append(l.res.Stages, StageResult{Name: name, State: Blocked, Detail: detail})
			stopped = name
			continue
		case NeedsYou:
			lf := s.Left()
			if st.Left != nil {
				lf = *st.Left
			}
			l.needsYou(name, detail, lf)
			stopped = name
			continue
		}

		// Something to apply.
		if !YesCovers(name) && !l.interactive {
			// Never auto-confirmed, never prompted: left for the user.
			l.needsYou(name, "needs your own confirmation, which --yes does not give", s.Left())
			stopped = name
			continue
		}
		if _, self := s.(SelfConfirming); !l.o.Yes && !l.interactive && !(self && YesCovers(name)) {
			return &NoTerminalError{Stage: name}
		}
		out, err := applyStage(ctx, s, l.env(name))
		var ny *NeedsYouError
		if errors.As(err, &ny) {
			l.needsYou(name, "", ny.Left)
			stopped = name
			continue
		}
		if err == nil {
			err = verifyStage(ctx, s)
		}
		if err != nil {
			l.fail(name, err)
			stopped = name
			continue
		}
		r := StageResult{Name: name, State: Changed, Detail: l.redact(oneLine(out.Detail))}
		if !out.Changed {
			r.State = Done
		}
		l.res.Stages = append(l.res.Stages, r)
	}
	return nil
}

func (l *loop) needsYou(stage, detail string, lf Left) {
	l.res.Left = append(l.res.Left, l.left(stage, lf))
	l.res.Stages = append(l.res.Stages, StageResult{Name: stage, State: NeedsYou, Detail: detail})
}

var stateLabel = map[State]string{
	Done: "done", Changed: "changed", Todo: "will do", Blocked: "blocked",
	NeedsYou: "needs you", Skipped: "skipped", Failed: "failed",
}

// view prints the plan view: one line per stage.
func (l *loop) view(rs []StageResult) {
	if l.o.Project != "" {
		fmt.Fprintf(l.o.Out, "fugaro init: project %s (GCP %s, %s)\n", l.o.Project, l.o.GCPProject, l.o.Region)
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

// finish prints the closing account of the run.
func (l *loop) finish() {
	if l.o.PlanOnly {
		l.view(l.res.Stages)
		if l.res.Note != "" {
			fmt.Fprintln(l.o.Out, l.res.Note)
		}
	} else {
		fmt.Fprintln(l.o.Out, "fugaro init: result")
		l.view(l.res.Stages)
	}
	for _, lf := range l.res.Left {
		fmt.Fprintf(l.o.Out, "left for you (%s, %s): %s\n", lf.Stage, lf.Kind, lf.Text)
	}
	if f := l.res.Failed; f != nil {
		// The error itself is the caller's to print (the CLI returns it).
		fmt.Fprintf(l.o.Out, "stopped at %s; fix: %s\n", f.Stage, f.Fix)
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
		l.fail(stage, &StageError{Err: fmt.Errorf("cancelled: %w", err), Fix: "rerun fugaro init: it resumes at " + stage})
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
	case Todo, Done, Skipped, Blocked, NeedsYou:
		return st, nil
	}
	return st, fmt.Errorf("stage %s: Check returned the state %q, which a check may not (done, will-do, blocked, needs-you or skipped)", s.Name(), st.State)
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
