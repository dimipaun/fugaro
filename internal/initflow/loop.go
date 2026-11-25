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
	// Detail, when the caller has one, is where an applying run's plan view
	// goes instead of Out: the run's reporter stream, which is the log always
	// and the terminal under -v (D1352, D1354). The table is the run's own
	// account of what it is about to do, and the run's steps are the default
	// view; a plan-only run keeps the table on screen, because there it IS the
	// output and changes nothing (the loop asks PlanOnly below), and so does a
	// caller with no reporter at all, which has nowhere else to put it.
	Detail io.Writer
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
	// OnStep, OnStage and OnLeft, when set, are the run as it happens,
	// handed to a caller that renders it while it goes rather than reading
	// its result afterwards: the console's setup wizard spawns `fugaro
	// setup --events` and draws each line as it arrives
	// (docs/console-improvements ST2, D2904).
	//
	// OnStep is a stage the run has reached and is about to work on — the
	// moment a line goes up for it — and it is the one fact the results
	// cannot give a reader, because a stage's check is seconds and the
	// apply behind it is minutes. OnStage is a stage the run settled, once
	// per stage, with the state, the detail and the wait it settled in.
	// OnLeft is a thing only the user can do, recorded where the run
	// records it, with the step's own one line to run: a surface with no
	// terminal hands it back rather than waiting for one.
	//
	// They are nil in every caller but that one, and a nil one changes
	// nothing at all: what is handed out is what the run already records
	// for --json, and no hook here is a step, a confirmation or a word the
	// run would not otherwise take.
	// OnRepos is the per-repository lines a settled stage reported (C1's
	// RepoLine), handed over where the run's own account is built: a caller
	// prints them under the stage's header, in the order it keeps for a person
	// reading it. The loop calls it once per stage — the pass the run keeps,
	// never the review screen an applying run throws away — and never reorders
	// what it is handed.
	OnStep  func(stage string)
	OnStage func(StageResult)
	OnLeft  func(Left)
	OnRepos func([]RepoLine)
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
	// Undone is the check's own Undone (Status): a stage that skipped without
	// doing its work rather than one that found nothing to do. State is one
	// word for the two, and this is what tells them apart — a caller settling
	// a record of the steps a machine still owes reads it, and so does a
	// reader of the document. Written only when it is true, so a run with no
	// such stage reports what it always did.
	Undone bool `json:"undone,omitempty"`
	// Repos are the lines the stage reported about the repositories it read,
	// in canonical order (a stage that implements RepoReporter: C1, U13), and
	// Detail above is the summary built from them rather than the stage's own
	// reason — a reason that named the run's working directory would be a
	// different line from every directory, and the stream is the same one from
	// all of them. Absent for a stage that reports none, which is a stage that
	// does no per-repository work at all.
	Repos []RepoLine `json:"repos,omitempty"`
}

// Failure is the stage a run stopped at, in one line each.
type Failure struct {
	Stage string `json:"stage"`
	Error string `json:"error"`
	Fix   string `json:"fix"`
}

// Notice is one line a run reports outside its stages: the step that said it
// (the fold, the notices, a step after the stage loop), the repository or the
// admit pattern it is about, how it was left, and one line a person reads. It
// is a stage's RepoLine with the step's own name on it, because a step after
// the loop has no stage to report under (C1, R12, R19).
//
// The loop does not make these: the CLI fills them where the step reports its
// lines (reportRepos), and what carries them out is the document's `notices`
// member and the stream's `notice` event.
type Notice struct {
	Step   string    `json:"step"`
	Repo   string    `json:"repo,omitempty"`
	State  RepoState `json:"state,omitempty"`
	Detail string    `json:"detail,omitempty"`
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
	// Notices are the lines the run reported outside its stages: the steps
	// after the stage loop and the admit patterns the reader could not list
	// (R12, R19). Absent for a run that reported none.
	Notices []Notice `json:"notices,omitempty"`

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
			// A run nobody answers applies nothing: it is --plan-only.
			l.o.PlanOnly = true
			l.res.Note = "a run that never prompts and carries no answer of its own only plans; the owner's own run is what applies"
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
	// waits holds the stages that wait for a stop, in the order the loop
	// reached them: the run's end prints one line per thing waited for, with
	// the count (flushWaits). A run that stopped at a stage leaves every stage
	// behind it saying the same thing, and the account is not eleven copies of
	// it (the owner's feedback of 2026-11-09). They are all buffered to the
	// end rather than flushed as the loop passes them, so a run that stopped
	// early counts every step it never reached once, in one place.
	waits []StageResult
	// ranPast is the stop the stage being recorded ran past, and it is set for
	// that stage alone: the loop holds no reporter's state, and what the line
	// has to say is one fact about one stage (ranPastStop, MachineLocal). The
	// plan view does not read it: it runs nothing, so nothing runs past a stop
	// there, and what a reader of the plan sees is what the run would do.
	ranPast *stop
	// stageFix is the way out the failed stage itself gave (StageError.Fix),
	// or "" when it gave none. It is the failure block's own line under the
	// message (endStep): what a person does next is the stage's sentence where
	// there is one, and where there is none the closing line's rerun is the
	// whole of it — the generic fix the record carries (Failure.Fix) is that
	// same sentence and not a second one to print beside it.
	stageFix string
	// settling is the stage whose result the loop is recording: the lines a
	// stage reported about the repositories it read are read off it where its
	// result is made (reposOf), and every result is made through record or
	// through the helpers that call it, which carry the stage's name alone.
	// Both passes set it as they reach a stage.
	settling Stage
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

// left is one step a stage leaves for the person, as the run records it: the
// stage's name, a kind and a first line that are always there, and the reason
// the step was not done — the detail the caller passes, when the step carries
// none of its own (a stage that only said "run this in your own terminal" has
// said what it owes and not why).
//
// The callers that read a stage's check pass that check's detail: the stop a
// needs-you check makes, and the pause a billing-pending one makes. The
// applying path passes none, because the detail of a stage that applied work
// is its success line — "No changes", "installed v0.26.0" — and a success
// line is not why the step beside it was left; a step on that path carries a
// reason only where the stage set one itself.
func (l *loop) left(stage string, lf Left, why string) Left {
	lf.Stage = stage
	if lf.Kind == "" {
		lf.Kind = LeftCommand
	}
	if len(lf.Why) == 0 {
		lf.Why = why
	}
	lf.one(l.redact, l.redactAll)
	l.emitLeft(lf)
	return lf
}

// stepUp tells the caller's OnStep that the run has reached a stage and is
// about to work on it: the moment a line goes up for it, and the one fact a
// reader of the results cannot place afterwards, because a stage's check and
// plan are seconds and the apply behind them is minutes.
func (l *loop) stepUp(stage string) {
	if l.o.OnStep != nil {
		l.o.OnStep(stage)
	}
}

// emitStage hands a settled stage to the caller's OnStage. It is called where
// the run's own result is built — the execution pass (record, which a failure
// enters through fail), and the preview of a plan-only run, which is that
// run's whole account — so what a caller rendering the run sees is the run,
// and never the review screen an applying run throws away.
//
// The guard is the one emitLeft carries and it is load-bearing: an applying
// run's preview records the stage a failed check stopped at (fail), and the
// execution pass behind it resets that result and records the stage again, so
// without it a caller is handed the same stage twice — a `stage ... failed`
// line from the throwaway pass, ahead of the step line of the run's own pass,
// and then that stage again where converge records it.
func (l *loop) emitStage(r StageResult) {
	if l.o.OnStage == nil || !(l.executing || l.o.PlanOnly) {
		return
	}
	l.o.OnStage(r)
}

// emitLeft hands a step only the user can do to the caller's OnLeft, and it is
// the same rule as emitStage's: the preview of an applying run records lefts
// for the review screen and drops them when the execution pass starts, so a
// caller is handed the ones the run keeps and not the ones the screen showed.
func (l *loop) emitLeft(lf Left) {
	if l.o.OnLeft == nil || !(l.executing || l.o.PlanOnly) {
		return
	}
	l.o.OnLeft(lf)
}

// emitRepos hands a settled stage's per-repository lines to the caller's
// OnRepos, under the rule emitStage and emitLeft carry: they belong to the
// run's own account, so a stage that reported them during its Check — the
// check runs in both passes — has them printed once, in the pass the run
// keeps, and the review screen an applying run throws away prints none.
//
// The lines are the caller's to read and never to reorder: they are the same
// slice the result carries, which is in canonical order whatever order a
// person's own output puts them in (spec §Output).
func (l *loop) emitRepos(r StageResult) {
	if l.o.OnRepos == nil || len(r.Repos) == 0 || !(l.executing || l.o.PlanOnly) {
		return
	}
	l.o.OnRepos(r.Repos)
}

// reposOf is a settled stage's result with the lines it reported about the
// repositories it read (RepoReporter, C1): the lines themselves, and the
// summary built from them as the stage's own detail — what a reader of the
// document and of the feed gets is then the same from every directory, which
// the stage's own reason, naming the run's working directory, would not be.
//
// The lines are redacted like every other line that reaches the document or
// the screen (F6), and a stage that reported none — one that does no
// per-repository work, which is every stage until the per-repository tasks
// land — is the result as it stands.
func (l *loop) reposOf(s Stage, r StageResult) StageResult {
	rep, ok := s.(RepoReporter)
	if !ok {
		return r
	}
	lines := rep.RepoLines()
	if len(lines) == 0 {
		return r
	}
	r.Repos = make([]RepoLine, len(lines))
	for i, ln := range lines {
		ln.Repo, ln.Detail = l.redact(ln.Repo), l.redact(ln.Detail)
		r.Repos[i] = ln
	}
	r.Detail = repoSummary(r.Repos)
	return r
}

// repoSummary is what a stage that reported lines says about itself: how many
// repositories it read and how it left each of them, in the order of the four
// states — "3 repositories: 1 done, 1 needs you, 1 unread". It is the stage's
// detail in the document and in the feed, and it is built from the lines
// alone, so the same run says the same sentence wherever it is run from.
func repoSummary(lines []RepoLine) string {
	states := []RepoState{RepoDone, RepoNeedsYou, RepoUnread, RepoSkipped}
	counts := make([]int, len(states))
	for _, ln := range lines {
		i := slices.Index(states, ln.State)
		if i < 0 {
			// A state the contract does not have is the stage's own, and it is
			// counted as the last one rather than dropped: the numbers still
			// add up to the lines the stage reported.
			i = len(states) - 1
		}
		counts[i]++
	}
	parts := make([]string, 0, len(states))
	for i, st := range states {
		if counts[i] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[i], st))
		}
	}
	noun := "repositories"
	if len(lines) == 1 {
		noun = "repository"
	}
	return fmt.Sprintf("%d %s: %s", len(lines), noun, strings.Join(parts, ", "))
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
		l.settling = s
		if l.cancelled(ctx, name) {
			return nil
		}
		l.pastStops(s, name, stops)
		st, err := checkStage(ctx, s)
		if err != nil {
			l.fail(name, err)
			return nil
		}
		r := StageResult{Name: name, State: st.State, Detail: l.redact(oneLine(st.Detail)), Undone: st.Undone}
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
			l.res.Left = append(l.res.Left, l.left(name, lf, st.Detail))
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
		r = l.reposOf(s, r)
		l.res.Stages = append(l.res.Stages, r)
		if l.o.PlanOnly {
			// A plan-only run's preview IS the run: it applies nothing, so
			// the stages settled here are the ones it reports, and a caller
			// rendering it as it goes sees each one as its check comes back.
			// An applying run's rows are the review screen instead, and the
			// execution pass behind them owns the account (see below).
			l.emitStage(r)
			l.emitRepos(r)
		}
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
		l.stageFix = f.Fix
	} else {
		l.stageFix = ""
	}
	l.res.Failed, l.res.Cause = f, l.redactedCause(err)
	l.record(StageResult{Name: stage, State: Failed, Detail: f.Error})
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
// stages are linear, and the tolerance is the stopped stage's own
// (RunsPastStop) — beside the one exception a stage carries itself, a stage
// that is only this machine's (MachineLocal), which the cloud stopping in
// front of it does not touch.
func blocker(name string, stops []stop) *stop {
	if MachineLocal(name) {
		return nil
	}
	for i := range stops {
		if !stops[i].yours || !RunsPastStop(name, stops[i].name) {
			return &stops[i]
		}
	}
	return nil
}

// ranPastStop is the stop a machine-local stage is running past, nil when it
// is running past none. It is the same exception blocker makes, read the other
// way round, and it is what the stage's own line says: a stage that ran while
// the run was stopped is a stage a reader would otherwise believe had waited
// (MachineLocal).
func ranPastStop(name string, stops []stop) *stop {
	if !MachineLocal(name) || len(stops) == 0 {
		return nil
	}
	return &stops[0]
}

// ranPastText is what a machine-local stage's line says about the stop it ran
// past, in the words the rest of the run uses for that stage's own row: the
// step the user still owes ("waits for you"), or a stage that stopped for
// another reason — a failure, a stage whose own check reported it blocked —
// which is not the reader's to clear.
func ranPastText(s *stop) string {
	if s.yours {
		return "machine-local: ran although " + s.name + " waits for you"
	}
	return "machine-local: ran although " + s.name + " stopped"
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
	l.stageFix = ""
	l.executing = true
	var stops []stop // the stages the run stopped at: what everything after waits for
	paused := ""     // the stage that paused for billing: what needs billing waits for it
	for _, s := range stages {
		name := s.Name()
		l.settling = s
		l.ranPast = nil
		if b := blocker(name, stops); b != nil {
			l.record(StageResult{Name: name, State: Blocked, Detail: "waits for " + b.name})
			continue
		}
		l.ranPast = ranPastStop(name, stops)
		if paused != "" && NeedsBilling(name) {
			l.record(StageResult{Name: name, State: WaitingForBilling, Detail: "waiting for billing"})
			continue
		}
		l.pastStops(s, name, stops)
		// The line in flight goes up before the stage is looked at: its check
		// and its plan are seconds, and the apply behind them is minutes.
		l.stepUp(name)
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
			l.record(StageResult{Name: name, State: st.State, Detail: detail, Undone: st.Undone})
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
			l.needsYou(name, "needs your own confirmation, which no flag gives", s.Left())
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
	//
	// No reason is taken from the detail here, and that is the rule rather
	// than an omission: a stage that applied work says a success line in it
	// ("No changes", "installed v0.26.0 for this machine"), which is not why
	// the step was left. A step on this path carries a reason only where the
	// stage set one itself — the images stage's catalog half does, where what
	// it left is a stale image and not a success.
	if out.Left != nil {
		l.res.Left = append(l.res.Left, l.left(stage, *out.Left, ""))
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
		// The gate's sentence is the detail, and what stands beside it is the
		// refusal's own answer about who this step is for: the stage's Left
		// only when the step IS the owner's (no plan gate may judge it, D1026), and a
		// commandless refusal otherwise — a plan the cover would not admit is
		// nobody's to type past (CV1), and the stage's own prompt asks this
		// reader to do the one thing the gate exists to make impossible.
		l.needsYou(name, ge.Error(), ge.Left(s.Left()))
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
	l.res.Left = append(l.res.Left, l.left(stage, lf, detail))
	l.record(StageResult{Name: stage, State: BillingPending, Detail: l.redact(oneLine(detail))})
}

func (l *loop) needsYou(stage, detail string, lf Left) {
	l.res.Left = append(l.res.Left, l.left(stage, lf, detail))
	l.record(StageResult{Name: stage, State: NeedsYou, Detail: detail})
}

// record adds a stage's result to the run and, in the execution pass, prints
// its line as the stage ends rather than in one block at the end of the run:
// a stage that takes minutes — a Terraform apply, an image build — leaves a
// terminal nobody is drawing on, and a person who reads that as a hang stops
// the run in the middle of an apply. A run with no reporter records exactly
// what it recorded before, and the plan view's rows are not a run at all.
func (l *loop) record(r StageResult) {
	r = l.reposOf(l.settling, r)
	shown := r.Detail
	if l.ranPast != nil && !waitingState(r.State) {
		// The stage ran although the run was stopped: the result says so
		// before whatever the stage has to say about itself, because that is
		// the fact a reader of the run record cannot get anywhere else —
		// every stage behind a stop says it waited, and this one did not.
		// The screen's own line says only what the stage did — the stop it ran
		// past is the line above it — which is what shown carries.
		if r.Detail == "" {
			r.Detail = ranPastText(l.ranPast)
		} else {
			r.Detail = ranPastText(l.ranPast) + "; " + r.Detail
		}
	}
	l.res.Stages = append(l.res.Stages, r)
	l.emitStage(r)
	// The per-repository lines go under the stage's own line, which is the
	// header they belong to, and that line is the last thing record prints
	// (endStep): handed over here they would go above it (U13, spec §Output).
	defer l.emitRepos(r)
	if l.o.Report == nil || !l.executing {
		return
	}
	if waitingState(r.State) {
		l.waits = append(l.waits, r)
		return
	}
	l.endStep(r, shown)
}

// waitingState is a stage that did not run and is not this run's to give: it
// waits for a stop ahead of it. Those are the stages the account prints as
// one line.
func waitingState(s State) bool {
	return s == Blocked || s == WaitingForBilling
}

// flushWaits prints the buffered waiting stages as one line per run of them
// that waits for the same thing: the stage names and what they wait for. The
// loop's own detail names it ("waits for installation"), and the line reads
// "… wait for installation"; billing, which the loop records as its own
// state, is the one wait that is not a stage.
func (l *loop) flushWaits() {
	if len(l.waits) == 0 {
		return
	}
	waits := l.waits
	l.waits = nil
	for len(waits) > 0 {
		target := waitTarget(waits[0])
		n := 1
		for n < len(waits) && waitTarget(waits[n]) == target {
			n++
		}
		names := make([]string, 0, n)
		for _, r := range waits[:n] {
			names = append(names, r.Name)
		}
		l.o.Report.Waits(names, target)
		waits = waits[n:]
	}
}

// waitTarget is what a waiting stage says it waits for, in the words the
// account's waiting line uses.
func waitTarget(r StageResult) string {
	if t, ok := strings.CutPrefix(r.Detail, "waits for "); ok {
		return t
	}
	if r.State == WaitingForBilling {
		return "the billing account"
	}
	return r.Detail
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

// stateLabel is the word each state prints in the plan view. "waiting" is
// what Blocked says and not "blocked": what the state means to its reader is
// that something ahead of it has to happen first, and "waiting" is the word
// the account's own waiting line uses, so a stage reads the same in both
// views of one run.
var stateLabel = map[State]string{
	Done: "done", Changed: "changed", Todo: "will do", Blocked: "waiting",
	NeedsYou: "left for you", Skipped: "skipped", Failed: "failed",
	BillingPending: "billing pending", WaitingForBilling: "waiting for billing",
}

// stateKind is the mark and colour each state's word is drawn in: a step's
// four ends, plus the two a plan view has of its own (a stage with something
// to do, and one that waits). The reporter owns the alphabet; the words above
// are the converge's.
var stateKind = map[State]report.Kind{
	Done: report.KindDone, Changed: report.KindDone, Todo: report.KindTodo,
	Blocked: report.KindWaiting, WaitingForBilling: report.KindWaiting,
	NeedsYou: report.KindLeft, BillingPending: report.KindLeft,
	Skipped: report.KindSkipped, Failed: report.KindFailed,
}

// statusWidth is the column the plan view's status is padded to: wide enough
// for the longest word it prints ("waiting for billing" overflows it, which
// costs one line's alignment and not a character), so the stage names below
// it line up.
const statusWidth = 14

// view prints the plan view: one line per stage, its status as the mark and
// the coloured word the reporter draws (the plan view is not a run, and a
// run that reported no step prints no closing line), the stage's own name and
// its reason — one clause while the run's account is brief, and the whole of it
// otherwise (report.Short). Every row goes to the run's log whole: under
// --plan-only no step is printed at all, so the log's copy of the row is the
// only whole one there is, and the reason a stage left for the user carries
// (an instruction, one to a line) is the last thing that may be cut and lost.
//
// The reporter is nil-safe, so a caller driving the loop with no reporter at
// all prints the plain text it printed before — and there the reason is never
// cut, because that run has no log to keep the rest of it.
func (l *loop) view(rs []StageResult) {
	if l.o.Detail != nil && !l.o.PlanOnly {
		// The row written here is the log's, whole reason and plain status,
		// which is exactly what logRow puts there: routing the table to the
		// stream takes that write's place rather than adding to it.
		if l.o.Project != "" {
			fmt.Fprintf(l.o.Detail, "%s: project %s (GCP %s, %s)\n", l.program(), l.o.Project, l.o.GCPProject, l.o.Region)
		}
		for _, r := range rs {
			fmt.Fprintln(l.o.Detail, l.row(r, r.Detail, true))
		}
		return
	}
	if l.o.Project != "" {
		fmt.Fprintf(l.o.Out, "%s: project %s (GCP %s, %s)\n", l.program(), l.o.Project, l.o.GCPProject, l.o.Region)
	}
	for _, r := range rs {
		l.logRow(r)
		fmt.Fprintln(l.o.Out, l.screenRow(r))
	}
}

// logRow keeps one plan-view row in the run's log: the row the screen shows,
// with the status in plain text — a log is read with a pager, where the colour
// that helps a person watching is noise — and the reason whole. A plan-only run
// prints no step for any stage, so this is the only place a reason the screen
// cut is readable.
func (l *loop) logRow(r StageResult) {
	if l.o.Report == nil {
		return
	}
	fmt.Fprintln(l.o.Report.Log(), l.row(r, r.Detail, true))
}

// screenRow is that row as the person reads it: the status in the report's own
// alphabet and colour, and the reason cut to its first clause when the run's
// account is brief (report.Short). A run whose account is not brief — one with
// no log to keep the rest of a reason in — prints it whole.
func (l *loop) screenRow(r StageResult) string {
	detail := r.Detail
	if l.o.Report.IsBrief() && detail != "" {
		detail = report.Short(detail)
	}
	return l.row(r, detail, false)
}

// row renders one plan-view row: the status in its column, the stage's name in
// its own, and the detail its reader gets. plain draws the status without
// colour, which is what the log's copy of a row is.
func (l *loop) row(r StageResult, detail string, plain bool) string {
	kind, word := stateKind[r.State], stateLabel[r.State]
	status := l.o.Report.Status(kind, word, statusWidth)
	if plain {
		status = l.o.Report.PlainStatus(kind, word, statusWidth)
	}
	line := "  " + status + " " + fmt.Sprintf("%-16s", r.Name)
	switch {
	case r.After != "" && detail != "":
		line += " " + detail + " (after " + r.After + ")"
	case r.After != "":
		line += " after " + r.After
	case detail != "":
		line += " " + detail
	}
	return strings.TrimRight(line, " ")
}

// endStep prints one stage's line through the reporter: the marker the
// stage's state earns, which is the line the run's account has always been,
// printed where the stage ends instead of at the end of the run. The stage's
// own line in flight, when there is one, is the line it replaces. shown is
// the stage's detail as the screen has it, which is the result's own except
// for a machine-local stage that ran past a stop (record).
func (l *loop) endStep(r StageResult, shown string) {
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
			s.Left(l.stageText(r, shown))
		} else {
			s.Done(shown)
		}
	case Failed:
		// The whole error, under the cross, and not the one line the result
		// carries: the step that stopped the run is the one thing its reader
		// has to read in full, and the message's own later lines are the
		// context that goes under it (G8). The way out the stage gave follows
		// them, as the last of that context: the block reads what went wrong
		// and then what to do about it, before the paths to the run's log and
		// to the command that explains it.
		err := l.res.Cause
		if err == nil {
			err = errors.New(r.Detail)
		}
		var context []string
		if l.stageFix != "" {
			context = append(context, l.stageFix)
		}
		s.Failed(err, context)
	case NeedsYou, BillingPending, WaitingForBilling:
		// The user still owes this stage — a confirmation, a billing
		// account — so it is never a tick, whatever it says (G3).
		if lf := l.leftFor(r.Name); lf != nil && lf.Kind == LeftRefused {
			// And a step the gate refused is not a step to run either, so it
			// is drawn as the refusal it is: the left-for-you marker says
			// "you owe this", which is true of every other stage here and
			// false of this one.
			s.Refused(l.stageText(r, shown))
		} else {
			s.Left(l.stageText(r, shown))
		}
	default:
		// Skipped and blocked: the stage did not run, and what it waits for
		// is not this session's to give.
		s.Skipped(shown)
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
func (l *loop) stageText(r StageResult, shown string) string {
	if shown != "" {
		return shown
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
		// stage ended (record), so what is left is the closing line below,
		// the blocks a run owes the user, and the steps the run never reached,
		// which print here as one line each per thing they wait for.
		l.flushWaits()
		l.closeStep()
	} else {
		fmt.Fprintf(l.o.Out, "%s: result\n", l.program())
		l.view(l.res.Stages)
	}
	for _, lf := range l.res.Left {
		// A refusal prints its three short lines (Left.Brief) and not its
		// whole text: what a person reads there is what was refused, why in a
		// clause and what to do, and the argument the text makes — with the
		// gate's own sentence in it — is in the log the step's own line names
		// (the owner's feedback of 2026-11-09). The block's own last line says
		// where that sentence is, so a converge whose log could not be opened
		// prints the text instead of the block: the log the three lines promise
		// does not exist there, and the sentence is then the whole of what the
		// screen can be given (G5). The loop's own account — no reporter at all,
		// a caller that has not moved onto one — prints the block, which is what
		// it printed before.
		brief := len(lf.Brief) > 0 && (l.o.Report == nil || l.o.Report.KeepsLog())
		lines := []string{lf.Text}
		if brief {
			lines = lf.Brief
		}
		fmt.Fprintf(l.o.Out, "left for you (%s, %s): %s\n", lf.Stage, lf.Kind, lines[0])
		for _, extra := range lines[1:] {
			fmt.Fprintf(l.o.Out, "    %s\n", extra)
		}
		// The reason the step was not done, under the step: a person reads
		// what the run leaves them and, on the next line, why it is theirs
		// rather than the run's — the line the stage printed in flight is
		// gone from the view a reader of the log or of `--json` has. A
		// refusal's own three lines (Brief) already are that reason and are
		// not said twice.
		if lf.Why != "" && !brief {
			fmt.Fprintf(l.o.Out, "    %s\n", lf.Why)
		}
		for _, c := range lf.Commands {
			fmt.Fprintf(l.o.Out, "    %s\n", c)
		}
	}
	if l.o.Report != nil && !l.o.PlanOnly && !l.refused && !l.o.ReportBorrowed {
		if f := l.res.Failed; f != nil {
			// The step the run stopped at and the command that picks it up
			// there are the closing line's own, below: a stopped run says them
			// once, and its fix is in the error the caller prints (res.Failed,
			// --json) rather than on a line of its own. The error block above
			// already says what went wrong and where the whole of it is.
			l.o.Report.RerunFrom(l.program(), f.Stage)
		}
		// Last, so the closing line is the last thing the run says: the
		// reporter counts the steps it was given and names the outcome.
		// A refused run has no outcome to name — the caller's exit code is
		// not the account's — so it says nothing more (G3), and neither does a
		// borrowed reporter, whose owner prints the line where its own run
		// ends (ReportBorrowed).
		l.o.Report.Summary()
	} else if f := l.res.Failed; f != nil {
		// The account a reporter is not drawing a closing line for — the
		// loop's own, a plan-only run, a refusal, a borrowed reporter — says
		// the step and the fix itself, where it always has: there is no
		// closing line to resume from, so the fix is on no other one.
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
