package cli

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// setup_events.go is `fugaro setup --events` and `fugaro update --events`: the
// converge's own account written as newline-delimited events on stdout, so a
// surface that drives the CLI draws each stage as it settles instead of holding
// the whole --json document before it can draw anything (docs/console-improvements
// ST2, D2904: the console's first-run wizard spawns
// `setup --plan-only --answers-file - --events --json` and renders the lines as
// they arrive).
//
// The six kinds are the console's own closed set (SETUP_EVENT_TYPES in the
// console's copy of this contract, on the other side of the boundary this
// module keeps: no Go file here names a path under the console), and the feed
// is the --json document split
// into lines: `step` is the run reaching a stage, `stage` is a stage it
// settled, `left_for_you` is one thing only the user can do — with the step's
// own line to run, because a surface reading this has no terminal to hand it
// to — `notice` is one line the run reported outside its stages (the steps
// after the stage loop, the admit patterns: R12, R19) — and the last line is
// `done` with the run's own exit code, or `error` naming the stage the run
// stopped at. A run that ends with steps left for the
// user ends `done` with exit 1 and not `error`: it did not fail, the steps are
// the lines above it, and a surface draws its hand-off off that distinction.
// A feed with no last line is an interrupted read, and that is the failure a
// consumer is written against either way: a killed child, a truncated pipe, a
// cap reached.
//
// It is an output and never a way in. --json confirms no step (unconfirmedWhy,
// init.go), so every step that needs a confirmation arrives here as a
// `left_for_you` line exactly as it is left today, and nothing in this file
// adds a word that confirms one, a prompt or a flag.
//
// The shapes are the schema's (schemas/setup-events.schema.json) and the
// golden's (testdata/setup_events.golden.ndjson, the file the console's decoder
// is compared against), and the writer below is the one place a line is
// written: a member added to `stage` or to `left_for_you` is added here, in the
// schema and in the console's copy of the contract together.
//
// A caller that did not ask for --events builds no setupEvents at all: the nil
// it leaves is today's output, byte for byte — no feed, and the loop's three
// hooks (initflow.Options.OnStep, OnStage and OnLeft, set in
// initEngine.options) stay nil, which is a loop that prints what it always
// printed.

// The line kinds, the words the `type` member carries. They are the console's
// closed set, and a kind that is not one of these is a line the feed does not
// have (the schema's oneOf refuses it).
const (
	setupEventStep       = "step"
	setupEventStage      = "stage"
	setupEventLeftForYou = "left_for_you"
	setupEventNotice     = "notice"
	setupEventDone       = "done"
	setupEventError      = "error"
)

// setupStepEvent is the run reaching a stage and starting to work on it: the
// moment a person watching sees a line go up for it, and the one fact a
// consumer of the results cannot place afterwards — a stage's check and plan
// are seconds and the apply behind them is minutes.
//
// stage names the stage the step is inside and name is the step, and for a
// converge the two are the same string: the stages are the steps. detail is
// what the run has to say about it while it is in flight, which is nothing
// until it settles (the `stage` line behind this one is where it speaks).
type setupStepEvent struct {
	Type   string `json:"type"`
	Stage  string `json:"stage"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// setupStageEvent is one stage the run settled: the same members the --json
// document's `stages` element carries, so a consumer that read the document
// reads the same stage here, with the state word and the wait it settled in.
type setupStageEvent struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
	After  string `json:"after"`
	Undone bool   `json:"undone"`
	// Repos are the stage's own lines about the repositories it read (C1's
	// RepoLine, U13), always an array — `[]` for a stage that reports none,
	// which is every stage today — so a consumer reads it without a case for
	// "absent".
	Repos []initflow.RepoLine `json:"repos"`
}

// setupNoticeEvent is one line the run reported outside its stages: a step
// after the stage loop, or an admit pattern the reader could not expand into
// names (R12, R19). step is the step's own name — a step after the loop has no
// stage to report under — and repo and state are empty for a notice that is
// about no repository at all.
type setupNoticeEvent struct {
	Type   string `json:"type"`
	Step   string `json:"step"`
	Repo   string `json:"repo"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// setupLeftForYouEvent is one thing only the user can do: the --json
// document's own left_for_you entry, and the step's one line to run among its
// commands — the line a surface with no terminal hands back to the person
// rather than waiting for one. commands is always an array (never null), so a
// consumer reads it without a case for "absent".
//
// It carries the members the console's copy of this contract declares and no
// more (SetupLeftForYouEvent): the reason the step was left is the `stage`
// line's own detail where the stage's check is what left it, and a member the
// console has no text class for is a value its display layer would have to
// guess about (NEW_OPERATION_TEXT_CLASSES).
type setupLeftForYouEvent struct {
	Type     string   `json:"type"`
	Stage    string   `json:"stage"`
	Kind     string   `json:"kind"`
	Text     string   `json:"text"`
	Commands []string `json:"commands"`
}

// setupDoneEvent is the last line of a feed that ran to completion. It is what
// makes a feed complete rather than interrupted, and exit_code is the code the
// process exits with — the same number `--json`'s caller reads from the
// process, made explicit so a consumer that only reads the feed still has it
// (a run that left steps for the user is exit 1 behind a feed that ended done).
type setupDoneEvent struct {
	Type     string `json:"type"`
	ExitCode int    `json:"exit_code"`
}

// setupErrorEvent is the last line of a feed whose run failed: the stage it
// stopped at with the sentence and the fix the CLI printed, or — where the run
// refused before any stage — the refusal itself, with stage empty.
type setupErrorEvent struct {
	Type     string `json:"type"`
	Stage    string `json:"stage"`
	Error    string `json:"error"`
	Fix      string `json:"fix"`
	ExitCode int    `json:"exit_code"`
}

// setupEvents writes one run's feed. It is built only for a run that was given
// --events (init.go), and every method is nil-safe so the loop's hooks can be
// set from a run that has none.
type setupEvents struct {
	enc *json.Encoder
}

// newSetupEvents returns the feed w receives: stdout, because the feed is the
// command's answer under --events and takes the place of the one --json
// document. Each line is one Encode of one object, so nothing is buffered and a
// consumer reads a stage as the run settles it.
func newSetupEvents(w io.Writer) *setupEvents {
	return &setupEvents{enc: json.NewEncoder(w)}
}

// line writes one event. A failed write is swallowed on purpose: the feed is
// the answer to a consumer that is reading it, and a consumer that stopped
// reading — the console kills the child it no longer wants — is one the rest of
// the run has nothing to say to. What a broken writer leaves is a feed with no
// last line, which is exactly the interrupted read a consumer must already
// handle, and the run's own exit code is the run's and never the pipe's.
func (e *setupEvents) line(v any) {
	if e == nil || e.enc == nil {
		return
	}
	_ = e.enc.Encode(v)
}

// step is the loop's OnStep.
func (e *setupEvents) step(stage string) {
	e.line(setupStepEvent{Type: setupEventStep, Stage: stage, Name: stage})
}

// stage is the loop's OnStage.
func (e *setupEvents) stage(r initflow.StageResult) {
	repos := r.Repos
	if repos == nil {
		repos = []initflow.RepoLine{}
	}
	e.line(setupStageEvent{
		Type: setupEventStage, Name: r.Name, State: string(r.State),
		Detail: r.Detail, After: r.After, Undone: r.Undone, Repos: repos,
	})
}

// notice is one line the run reported outside its stages, written as the
// document's own `notices` entry.
func (e *setupEvents) notice(n initflow.Notice) {
	e.line(setupNoticeEvent{
		Type: setupEventNotice, Step: n.Step, Repo: n.Repo, State: string(n.State), Detail: n.Detail,
	})
}

// left is the loop's OnLeft: the left as the run recorded it, with the
// commands always an array.
func (e *setupEvents) left(lf initflow.Left) {
	commands := lf.Commands
	if commands == nil {
		commands = []string{}
	}
	e.line(setupLeftForYouEvent{
		Type: setupEventLeftForYou, Stage: lf.Stage, Kind: string(lf.Kind),
		Text: lf.Text, Commands: commands,
	})
}

// finish writes the feed's last line: `error` for a run that failed — the
// stage it stopped at, or the refusal that stopped it before any stage — and
// `done` for every other run, whatever it left for the user.
//
// A run with steps left for the user ends `done` with exit 1, and that is the
// line a surface reads as its hand-off: it applied everything it had, the
// `left_for_you` lines above are the steps that remain, and it did not fail —
// the console's wizard shows its list of steps off the back of it. A refusal
// is the other end: `error`, with no stage of its own, because there is no
// stage it stopped at. The two are the same exit code and the same kind of
// error, and init_stages.go's notFinished is what tells them apart.
//
// err is the command's own result, so exit is the very number the process
// exits with (ExitCode is what main reads), and the feed cannot come to say
// something the process does not do. The sentences are made safe for a
// terminal and for one line the way every other stream's are (oneLine of
// ErrorText): a consumer draws them, and a value from a remote service can
// reach one.
func (e *setupEvents) finish(res *initResult, err error) {
	if e == nil {
		return
	}
	if res != nil {
		// The lines the run reported outside its stages — the steps after the
		// loop and the admit patterns (R12, R19) — go out before the line that
		// ends the feed: they are part of the account the last line closes.
		for _, n := range res.Notices {
			e.notice(n)
		}
	}
	exit := ExitCode(err)
	var leftForYou *notFinished
	switch {
	case res != nil && res.Failed != nil:
		e.line(setupErrorEvent{
			Type: setupEventError, Stage: res.Failed.Stage,
			Error: oneLine(res.Failed.Error), Fix: oneLine(res.Failed.Fix),
			ExitCode: exit,
		})
	case err != nil && !errors.As(err, &leftForYou):
		e.line(setupErrorEvent{Type: setupEventError, Error: oneLine(ErrorText(err)), ExitCode: exit})
	default:
		e.line(setupDoneEvent{Type: setupEventDone, ExitCode: exit})
	}
}
