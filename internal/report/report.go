// Package report prints a command's steps for a person: one line per step, a
// line of its own while one runs, and a line saying how the run ended.
//
// It is the CLI's one place that decides whether colour is on, and the one
// place that keeps the run's full output (the log) beside the short view of
// it, so no command invents its own rule. It is inert on its own: a command
// builds one, prints its steps through it and keeps printing its
// machine-readable document on stdout exactly as it did before, because with
// --json no human line reaches stdout at all (G2).
package report

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/muesli/termenv"
)

// Options is what a command tells the reporter about itself. Every field has
// a default that keeps a test hermetic and a forgotten one harmless: a nil
// writer discards, a nil Getenv reads no variable, a nil IsTerminal is "not a
// terminal" (so nothing is drawn that a test did not ask for) and a nil Now is
// the real clock.
type Options struct {
	// Out is where the human lines go: the command's stdout.
	Out io.Writer
	// Err is where they go instead under JSON, which keeps stdout one
	// document. A command that shows no human line in JSON mode leaves it nil.
	Err io.Writer
	// Getenv reads the environment for NO_COLOR and TERM.
	Getenv func(string) string
	// IsTerminal reports whether a writer is a terminal. The flag plumbing
	// (CL4) is what passes the package variable its tests replace.
	IsTerminal func(io.Writer) bool
	// Verbose is --verbose: the run's own output (Reporter.Stream) reaches
	// the terminal as well as the log, which is today's behaviour.
	Verbose bool
	// JSON is --json: stdout is a document, so no human line goes there.
	JSON bool
	// NoColor is --no-color.
	NoColor bool
	// ASCII keeps the markers to ASCII, for a console or a font that cannot
	// draw the symbols. The watch screen has the same flag.
	ASCII bool
	// Brief is the converge's account: a step's reason is one clause on the
	// terminal, with the whole of it in the log. The converge is the one
	// command family that prints reasons a paragraph long — what a stage
	// leaves alone, and why — and its reader asked for one line per step
	// (the owner's feedback of 2026-11-09). A command that prints its own
	// short phrases leaves this false and prints exactly what it did before.
	Brief bool
	// Now is the clock the summary's elapsed time is read from, and the one
	// the line in flight and the heartbeat count a step with.
	Now func() time.Time
	// Sink is the run's log, when it has one: every byte of the run's own
	// output. A nil Sink is a run that keeps no log.
	Sink Sink
}

// Sink is the log a command's full output goes to. CL3's OpenLog is the task
// that returns one; Path is what the failure block prints, and is empty for a
// sink that could not open (G5, T6).
type Sink interface {
	io.Writer
	// Path is the file the output is in, or "" when there is none.
	Path() string
	// Close ends the log. The reporter never calls it: the command owns the
	// sink it opened and closes it on its way out.
	Close() error
}

// State is how a run ended. There are three, and only three (G3): a step left
// for the user is never a done. The CLI's exit code follows from this — 0 for
// done or only planned, 1 for a failure or a step left for the user.
type State int

const (
	// Done is a run whose steps all finished, were skipped or are waiting on
	// something that is not the user.
	Done State = iota
	// Left is a run with a step the user has to finish themselves.
	Left
	// Failed is a run that stopped on a step.
	Failed
)

// Outcome is how a run ended, with what the closing line needs.
type Outcome struct {
	// State is the run's result: Done, Failed or Left.
	State State
	// Steps counts the steps that reached a result. It is stages, not the
	// work under them.
	Steps int
	// Elapsed is the run's wall time, read from the injected clock.
	Elapsed time.Duration
}

// Reporter prints one command's run. It carries the colour decision, the
// clock and the log for the whole run, and every method is safe for
// concurrent use: a command streams its children's output from more than one
// goroutine.
type Reporter struct {
	o     Options
	human io.Writer
	// Style is the colour every line is drawn with: the profile the reporter
	// decided, which style.go's helpers render through. It is a field rather
	// than a decision made at each print so that one run draws in one syntax
	// (T4(a), D1355), and so that a caller with no reporter yet — the wizard's
	// questions — draws in that same one.
	Style

	mu         sync.Mutex
	start      time.Time
	steps      int
	state      State
	summarised bool
	// rerunProgram and rerunStep are what a stopped run's closing line resumes
	// with (RerunFrom): empty for a run that is not a converge.
	rerunProgram string
	rerunStep    string
	// spin is whether the step in flight is drawn on the terminal's own line.
	// Where it is not, the step says it is still running once a minute
	// instead. The rest is the state of that line, guarded by mu.
	spin      bool
	spinName  string
	spinSince time.Time
	spinFrame int
	spinStop  chan struct{}
	// spinPaused is a line in flight that is down on purpose: a prompt is on
	// the screen, and Resume draws the line again once it is answered.
	spinPaused bool
}

// New returns the reporter for one command's run.
func New(o Options) *Reporter {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Err == nil {
		o.Err = io.Discard
	}
	if o.Getenv == nil {
		o.Getenv = func(string) string { return "" }
	}
	if o.IsTerminal == nil {
		o.IsTerminal = func(io.Writer) bool { return false }
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	r := &Reporter{o: o, human: o.Out, Style: NewStyle(o), start: o.Now()}
	if o.JSON {
		// stdout is the document. The human lines still have a home, which is
		// where the run's own writer already goes under --json.
		r.human = o.Err
	}
	// Colour is on only where every rule allows it: a terminal, colour not
	// turned off, markers not kept to ASCII, and no --json (G4) — the one
	// decision NewStyle makes, read back here as "is there a colour at all".
	colour := !r.Style.plain()
	// The line in flight follows the colour decision exactly — one rule for
	// what may be drawn — and is not drawn under --verbose, where the run's
	// own stream owns the terminal.
	r.spin = colour && !o.Verbose
	return r
}

// KeepsLog reports whether this run keeps a log (Options.Sink): the file a
// failure prints the path of, and the place a reason the screen shortened is
// whole. A caller that prints a sentence promising a log — the converge's
// refusal block says the whole of its sentence is in one — asks first, because
// a log that could not be opened (G5) must be promised by nobody. A nil
// reporter keeps none.
func (r *Reporter) KeepsLog() bool {
	return r != nil && r.o.Sink != nil
}

// IsBrief reports whether this run is on the brief account (Options.Brief): a
// step's reason is one clause on the screen, with the whole of it in the log.
// A caller that prints its own lines — the converge's plan view — asks before
// it cuts one, so a run whose account is not brief keeps its reasons whole. A
// nil reporter is never brief.
func (r *Reporter) IsBrief() bool {
	return r != nil && r.o.Brief
}

// Step starts a step named name and returns it, to be ended by exactly one of
// Done, Skipped, Failed, Left and Abandon. Ending it twice does nothing, so a
// caller that reports twice cannot print two lines for one step.
func (r *Reporter) Step(name string) *Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopSpinner()
	r.spinPaused = false
	r.spinName, r.spinSince = name, r.o.Now()
	r.startSpinner()
	return &Step{r: r, name: name}
}

// Summary ends the run: it clears the line in flight, prints the closing line
// and returns how the run went. A run that reported no step prints no line —
// the plan view of a converge is not a run — and a second call prints nothing
// again and answers the same.
func (r *Reporter) Summary() Outcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopSpinner()
	r.spinName, r.spinPaused = "", false
	o := Outcome{State: r.state, Steps: r.steps, Elapsed: r.o.Now().Sub(r.start)}
	if !r.summarised && o.Steps > 0 {
		line := r.closingLine(o)
		r.logLine(line + "\n")
		fmt.Fprintln(r.human, line)
	}
	r.summarised = true
	return o
}

// Pause takes the line in flight down and stops drawing it, without ending
// the step: a command about to write something a person has to read on a line
// of its own — a prompt it then reads stdin on — pauses first, so no frame is
// ever drawn over it (G1). Resume draws the line again; ending the step ends
// it whether it was paused or not, and a run with nothing in flight pauses
// nothing.
func (r *Reporter) Pause() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.spin || r.spinName == "" {
		return
	}
	r.stopSpinner()
	r.spinPaused = true
}

// Resume draws the line in flight again after the prompt it made room for has
// been answered, where the rules allow one to be drawn at all. It is what
// keeps a stage that prompts in the middle of its work — the project's typed
// confirmation, an image build's question — a stage somebody is still
// watching, and it does nothing for a run whose step ended meanwhile.
func (r *Reporter) Resume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.spinPaused {
		return
	}
	r.spinPaused = false
	r.startSpinner()
}

// Stream is where a run's own output goes: Terraform's progress, a stage's
// own detail. It reaches the terminal only under --verbose, because the
// default view is one line per step, and the log always, because the log is
// what a failure sends the user to read (D1352). A write that fails never
// fails the command: Stream consumes the writer's error, so a closed pipe or
// a log that cannot be written changes no exit code (G5).
func (r *Reporter) Stream() io.Writer {
	if r.o.Sink == nil && !r.o.Verbose {
		return io.Discard
	}
	return &stream{r: r}
}

// stream is Reporter.Stream's writer: the run's own output, split between the
// terminal and the log.
type stream struct{ r *Reporter }

func (s *stream) Write(p []byte) (int, error) {
	r := s.r
	r.mu.Lock()
	defer r.mu.Unlock()
	if sink := r.o.Sink; sink != nil {
		_, _ = sink.Write(p)
	}
	if r.o.Verbose {
		_, _ = r.human.Write(p)
	}
	return len(p), nil
}

// Step is one unit of work a person watches: a short phrase, and the one
// result it ends in.
type Step struct {
	r     *Reporter
	name  string
	ended bool // guarded by r.mu
}

// Done ends the step with a tick. detail says what changed, when there is
// anything to say: a stage that did nothing and one that changed something
// are both a tick. Under Brief the screen carries its first clause and the log
// the whole of it (end); DoneWhole is the same result for a detail that is not
// a reason.
func (s *Step) Done(detail string) {
	s.end(glyphDone, colourDone, Done, " ", detail, nil, cutReason)
}

// DoneWhole ends the step with a tick and a detail the screen keeps whole:
// Done's result for a step whose detail is the answer itself rather than a
// reason for it — a plan's counts, and where the run's whole account of that
// plan is kept. The brief account's cut is for a reason read while a run goes
// past, and the detail this ends with is what that reader is reading: cutting
// it would take away the count and the path and leave the one step line saying
// less than nothing.
func (s *Step) DoneWhole(detail string) {
	s.end(glyphDone, colourDone, Done, " ", detail, nil, keepWhole)
}

// Skipped ends the step without a tick: the work was not needed, and reason
// is why. A skipped step is neither a failure nor a step the user owes.
func (s *Step) Skipped(reason string) {
	s.end(glyphSkipped, colourSkipped, Done, " ", reason, nil, cutReason)
}

// reasonView is what end puts on the screen for a step's reason under the
// brief account: its first clause, or the whole of it. Failed does not go
// through end at all — a failure prints its message whole, for the reason its
// own comment gives — so this belongs to Done, Skipped, Left and Refused, and
// DoneWhole is the one that turns the cut off.
type reasonView bool

const (
	cutReason reasonView = true
	keepWhole reasonView = false
)

// Failed ends the step with a cross and prints what went wrong, the whole of
// it: the error's own message, the last lines of context behind it, the log's
// path and the command that explains the run. The context is on the screen
// rather than only in the log because a coding agent's session often has a
// read-only home and cannot open the log at all (G8).
//
// The message is not cut to one line the way an ordinary step's reason is
// (Short): the step that stopped the run is the one thing its reader has to
// read in full, and a trailing "…" there sends them to a log for the sentence
// that told them what to do.
func (s *Step) Failed(err error, context []string) {
	r := s.r
	r.mu.Lock()
	defer r.mu.Unlock()
	if !s.begin(Failed) {
		return
	}
	var msg []string
	if err != nil {
		msg = append(msg, splitLines(err.Error())...)
	}
	msg = append(msg, context...)
	// A stage's message can open with a blank line, which is layout rather than
	// anything to read: the label goes on the first line that says something,
	// and the rest of the message is printed whole under it either way.
	for len(msg) > 0 && strings.TrimSpace(msg[0]) == "" {
		msg = msg[1:]
	}
	body := msg
	r.appendLogLine(&body)
	r.appendDiagnoseLine(&body)
	r.logLine(glyphFailed.text(r.o.ASCII) + " " + s.name + "\n")
	fmt.Fprintln(r.human, r.painted(glyphFailed.text(r.o.ASCII), colourFailed)+" "+s.name)
	if len(body) == 0 {
		return
	}
	// The label goes on the message's first line, at the step line's own
	// column; everything after it — the rest of the message, the context, the
	// log and the diagnose command — lines up two spaces in.
	if len(msg) == 0 {
		r.under(body)
		return
	}
	r.logLine("Error: " + body[0] + "\n")
	fmt.Fprintln(r.human, r.Error(body[0]))
	r.under(body[1:])
}

// splitLines is a message as the lines it is printed on: one line for a
// message that has none, and no trailing empty line for one that ends in a
// newline.
func splitLines(msg string) []string {
	if msg == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(msg, "\n"), "\n")
}

// Left ends the step with its own marker: the work needs the user before the
// run can finish, so the run exits 1, and a left-for-you step is never a tick
// (G3). The log's path goes under it for the same reason a failure prints it.
func (s *Step) Left(text string) {
	var lines []string
	s.r.appendLogLine(&lines)
	s.end(glyphLeft, colourLeft, Left, ": ", text, lines, cutReason)
}

// Refused ends the step with a cross and the log's path, and it is Left all
// the same: a step the managed gate refused is not a failure of the run —
// nothing was written and nothing this reader types or runs writes it — and it
// is nobody's to type past. What it does not do is keep the left-for-you
// marker: a person reading a run that stopped has to tell the two apart at a
// glance (the owner's feedback of 2026-11-09).
func (s *Step) Refused(text string) {
	var lines []string
	s.r.appendLogLine(&lines)
	s.end(glyphFailed, colourFailed, Left, ": ", text, lines, cutReason)
}

// Abandon ends the step without a line and without counting it: the run never
// got to a result for it — a refusal stops the run before the stage ran — so
// there is nothing to report, and a line in flight still has to come down.
func (s *Step) Abandon() {
	r := s.r
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	r.stopSpinner()
	r.spinName, r.spinPaused = "", false
}

// end prints the step's one result line and the lines under it, and records
// what the result means for the run. Only the first result counts.
//
// sep and reason build the tail: sep is " " for a result that carries a
// detail and ": " for one that carries the error or the instruction the step
// is left with. Under Brief the reason is shortened to its first clause on
// the screen and kept whole in the log — the log is what a person goes to for
// the rest, and its path is under a failure and under a step left for the
// user (D1352) — so `under` and the log line stay exactly as they were. view
// is whether that cut applies at all, and it is what DoneWhole turns off for
// a detail the cut would be the loss of.
func (s *Step) end(g glyph, c termenv.ANSIColor, state State, sep, reason string, under []string, view reasonView) {
	r := s.r
	r.mu.Lock()
	defer r.mu.Unlock()
	if !s.begin(state) {
		return
	}
	tail := ""
	human := ""
	if reason != "" {
		tail, human = sep+reason, sep+reason
		if r.o.Brief && view == cutReason {
			human = sep + Short(reason)
		}
	}
	// The log gets the same line unstyled: a log is read with a pager, where the
	// colour that helps a person watching is noise, and a reader comparing two
	// runs should not have to look past it. Each line goes in one write,
	// because the sink's redaction is by line and a line split across two could
	// carry half a value.
	r.logLine(g.text(r.o.ASCII) + " " + s.name + tail + "\n")
	line := r.painted(g.text(r.o.ASCII), c) + " " + s.name + human
	fmt.Fprintln(r.human, line)
	r.under(under)
}

// begin takes the step's result: it clears the line in flight, counts the step
// and records what the result means for the run. It answers false for a step
// that already ended, so the caller prints nothing the second time. The lock
// is the caller's, held on the way in and released on the way out.
func (s *Step) begin(state State) bool {
	r := s.r
	if s.ended {
		return false
	}
	s.ended = true
	r.stopSpinner()
	r.spinName, r.spinPaused = "", false
	r.steps++
	if state > r.state {
		r.state = state
	}
	return true
}

// under prints the lines below a step's own line: two spaces in, on the screen
// and in the log alike, so the block under a step reads as one thing. Called
// with the lock held.
func (r *Reporter) under(lines []string) {
	for _, l := range lines {
		r.logLine("  " + l + "\n")
		fmt.Fprintf(r.human, "  %s\n", l)
	}
}

// shortMax is the longest reason a step's line carries on the screen under
// Brief: one clause, which is what a person reads while a run goes past. The
// log keeps the whole of it.
const shortMax = 70

// Short is a reason as the one line a person reads it on: the first clause,
// no longer than shortMax, with an ellipsis where the rest was left to the
// log.
//
// The cut prefers a boundary the reason itself carries — a sentence end, a
// dash, a semicolon or a comma — so the line stops where the reason does
// rather than mid-thought, and only falls back to a word boundary inside the
// window when there is none. A reason that already fits is returned exactly
// as it is, byte for byte: most of them are short phrases and must not move.
func Short(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if len([]rune(trimmed)) <= shortMax {
		return trimmed
	}
	cut := shortMax
	for _, sep := range []string{". ", " — ", "; ", ", ", " ("} {
		if i := strings.Index(trimmed, sep); i > 0 {
			if n := len([]rune(trimmed[:i])); n < cut {
				cut = n
			}
		}
	}
	runes := []rune(trimmed)
	if cut == shortMax {
		// No boundary inside the window: stop at the last whole word rather
		// than in the middle of one.
		for cut > 0 && !unicode.IsSpace(runes[cut-1]) {
			cut--
		}
		if cut == 0 {
			cut = shortMax
		}
	}
	return strings.TrimRight(string(runes[:cut]), " ,;—-") + "…"
}

// Waits prints one dim line for the steps a stop left unrun: how many there
// are and what they wait for, because a run that stopped at a step leaves
// every step behind it saying the same thing, and eleven lines naming them is
// not a view of anything (the owner's feedback of 2026-11-09). The count is
// the whole of what a reader needs there; which ones they are is what the
// rerun that resumes at the stop will walk through. The summary counts the
// steps rather than the lines, so the account's "N steps" is what it was.
//
// names are the steps, in the run's canonical order and only read for their
// number; target is what they wait for, in the words the loop's own detail
// used.
func (r *Reporter) Waits(names []string, target string) {
	if len(names) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopSpinner()
	r.spinName, r.spinPaused = "", false
	r.steps += len(names)
	text := waitText(len(names), target)
	r.logLine(glyphSkipped.text(r.o.ASCII) + " " + text + "\n")
	fmt.Fprintln(r.human, r.painted(glyphSkipped.text(r.o.ASCII), colourSkipped)+" "+text)
}

// waitText is the one waiting line: the count, and what they wait for. The
// pronoun agrees with the count, because one step behind a stop is as common a
// line as eleven.
func waitText(n int, target string) string {
	if n == 1 {
		return "1 step not run (it needs " + target + ")"
	}
	return fmt.Sprintf("%d steps not run (they need %s)", n, target)
}

// logLine keeps one of the run's own lines — a step's result, the lines under
// it, the closing line — in the log, whole. The log is the run's full output
// (D1352) and the path a failure prints has to hold what the run showed, so a
// command whose own output is its steps cannot leave an empty file behind that
// line. Called with the lock held.
func (r *Reporter) logLine(text string) {
	if r.o.Sink == nil {
		return
	}
	_, _ = r.o.Sink.Write([]byte(text))
}

// Log is the run's log as a writer, for a command that prints its own lines to
// its own writer and wants them kept beside them (D1352). Stream is for a run's
// own output — a child's, a stage's — and reaches the terminal under --verbose;
// this one is the log alone, so a command that prints to a person and to the
// log writes each of them once. A run with no log discards, and a write that
// fails never fails the command (G5).
func (r *Reporter) Log() io.Writer {
	if r.o.Sink == nil {
		return io.Discard
	}
	return &logWriter{r: r}
}

// logWriter is Reporter.Log's writer: the sink, and nothing else.
type logWriter struct{ r *Reporter }

func (w *logWriter) Write(p []byte) (int, error) {
	r := w.r
	r.mu.Lock()
	defer r.mu.Unlock()
	if sink := r.o.Sink; sink != nil {
		_, _ = sink.Write(p)
	}
	return len(p), nil
}

// appendLogLine adds the run's log to the lines under a failure or a step
// left for the user: the path is what saves a rerun with --verbose, and a run
// with no log (a read command, T6) adds nothing.
func (r *Reporter) appendLogLine(lines *[]string) {
	if r.o.Sink == nil {
		return
	}
	if p := r.o.Sink.Path(); p != "" {
		*lines = append(*lines, "Log:  "+p)
	}
}

// diagnoseLine is the labelled line a failure prints about the command that
// explains it, up to the run ID the line ends with (T-DG9). The label is the
// same width as the log's above it, so the two paths start at one column.
const diagnoseLine = "Help: fugaro troubleshoot "

// appendDiagnoseLine adds that line under a failure, with the log's own name as
// its run ID: the stem of the file the path beside it points at, which is what
// `fugaro troubleshoot` routes by (D1609). A person passes on one word rather
// than a path, and the file that word names is the one the failure already
// sent them to. A run with no log has no run ID to name and adds nothing
// (T6) — the same rule the log's path follows.
func (r *Reporter) appendDiagnoseLine(lines *[]string) {
	if r.o.Sink == nil {
		return
	}
	if id, ok := strings.CutSuffix(filepath.Base(r.o.Sink.Path()), logSuffix); ok && id != "" {
		*lines = append(*lines, diagnoseLine+id)
	}
}

// summaryLine is the run's closing line, the spec's shape, with the three
// outcomes named rather than a tick that cannot show them (G3).
func summaryLine(o Outcome) string {
	steps := fmt.Sprintf("%d steps", o.Steps)
	if o.Steps == 1 {
		steps = "1 step"
	}
	switch o.State {
	case Failed:
		return fmt.Sprintf("Failed in %s, %s", elapsedOf(o), steps)
	case Left:
		return fmt.Sprintf("Left for you in %s, %s", elapsedOf(o), steps)
	default:
		return fmt.Sprintf("Done in %s, %s", elapsedOf(o), steps)
	}
}

func elapsedOf(o Outcome) string { return fmt.Sprintf("%.1fs", o.Elapsed.Seconds()) }

// RerunFrom is the step the closing line sends a stopped run back to: the
// program to type again, and the step the run stopped at, which is where it
// picks up (G6). The converge sets it before Summary, and a run that is not
// stopped never reads it.
func (r *Reporter) RerunFrom(program, step string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rerunProgram, r.rerunStep = program, step
}

// closingLine is what a run says last: summaryLine, and for a run that stopped
// the one thing the closing line is there for — the command that resumes it
// where it stopped. The step count is left out of that line because the steps
// of a stopped run are mostly the ones it never reached, and "Failed in 2.2s,
// 19 steps" reads as if it had done nineteen of them.
func (r *Reporter) closingLine(o Outcome) string {
	if o.State == Failed && r.rerunProgram != "" && r.rerunStep != "" {
		return fmt.Sprintf("Failed in %s. Rerun %s to resume at %s.", elapsedOf(o), r.rerunProgram, r.rerunStep)
	}
	return summaryLine(o)
}
