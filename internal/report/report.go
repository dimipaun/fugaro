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
	"sync"
	"time"

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
	// IsTerminal reports whether a writer is a terminal. The CLI passes the
	// package variable its tests replace.
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
	// Now is the clock the summary's elapsed time is read from.
	Now func() time.Time
	// Sink is the run's log, when it has one: every byte of the run's own
	// output. A nil Sink is a run that keeps no log.
	Sink Sink
}

// Sink is the log a command's full output goes to. The log sink's own task
// returns one; Path is what the failure block prints, and is empty for a sink
// that could not open (G5, T6).
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
	// profile is the colour profile every style is rendered with. The
	// reporter decides it and forces it into termenv, which would otherwise
	// colour a pipe when the environment asks (T4(a)); see style.go.
	profile termenv.Profile

	mu         sync.Mutex
	start      time.Time
	steps      int
	state      State
	summarised bool
	// spin is whether the step in flight is drawn on the terminal's own line.
	// The rest is the state of that line, guarded by mu.
	spin      bool
	spinName  string
	spinFrame int
	spinStop  chan struct{}
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
	r := &Reporter{o: o, human: o.Out, start: o.Now()}
	if o.JSON {
		// stdout is the document. The human lines still have a home, which is
		// where the run's own writer already goes under --json.
		r.human = o.Err
	}
	// Colour is on only where every rule allows it: a terminal, colour not
	// turned off, markers not kept to ASCII, and no --json (G4).
	colour := !o.JSON && !o.ASCII && !NoColor(o.NoColor, o.Getenv) && o.IsTerminal(o.Out)
	if colour {
		// ANSI: the sixteen colours, which every terminal draws and which the
		// legacy Windows console draws once virtual terminal processing is on.
		r.profile = termenv.ANSI
	} else {
		r.profile = termenv.Ascii
	}
	// The line in flight follows the colour decision exactly — one rule for
	// what may be drawn — and is not drawn under --verbose, where the run's
	// own stream owns the terminal.
	r.spin = colour && !o.Verbose
	return r
}

// Step starts a step named name and returns it, to be ended by exactly one of
// Done, Skipped, Failed and Left. Ending it twice does nothing, so a caller
// that reports twice cannot print two lines for one step.
func (r *Reporter) Step(name string) *Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopSpinner()
	r.startSpinner(name)
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
	o := Outcome{State: r.state, Steps: r.steps, Elapsed: r.o.Now().Sub(r.start)}
	if !r.summarised && o.Steps > 0 {
		fmt.Fprintln(r.human, summaryLine(o))
	}
	r.summarised = true
	return o
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
// are both a tick.
func (s *Step) Done(detail string) {
	s.end(glyphDone, colourDone, Done, after(detail), nil)
}

// Skipped ends the step without a tick: the work was not needed, and reason
// is why. A skipped step is neither a failure nor a step the user owes.
func (s *Step) Skipped(reason string) {
	s.end(glyphSkipped, colourSkipped, Done, after(reason), nil)
}

// after is the tail of a result line that says more, or nothing.
func after(text string) string {
	if text == "" {
		return ""
	}
	return " " + text
}

// Failed ends the step with a cross and prints the error, the last lines of
// context behind it and the log's path. The context is on the screen rather
// than only in the log because a coding agent's session often has a read-only
// home and cannot open the log at all (G8).
func (s *Step) Failed(err error, context []string) {
	lines := append([]string(nil), context...)
	s.r.appendLogLine(&lines)
	s.end(glyphFailed, colourFailed, Failed, ": "+err.Error(), lines)
}

// Left ends the step with its own marker: the work needs the user before the
// run can finish, so the run exits 1, and a left-for-you step is never a tick
// (G3). The log's path goes under it for the same reason a failure prints it.
func (s *Step) Left(text string) {
	var lines []string
	s.r.appendLogLine(&lines)
	s.end(glyphLeft, colourLeft, Left, ": "+text, lines)
}

// end prints the step's one result line and the lines under it, and records
// what the result means for the run. Only the first result counts.
func (s *Step) end(g glyph, c termenv.ANSIColor, state State, tail string, under []string) {
	r := s.r
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	r.stopSpinner()
	r.steps++
	if state > r.state {
		r.state = state
	}
	line := r.painted(g.text(r.o.ASCII), c) + " " + s.name + tail
	fmt.Fprintln(r.human, line)
	for _, l := range under {
		fmt.Fprintf(r.human, "    %s\n", l)
	}
}

// appendLogLine adds the run's log to the lines under a failure or a step
// left for the user: the path is what saves a rerun with --verbose, and a run
// with no log (a read command, T6) adds nothing.
func (r *Reporter) appendLogLine(lines *[]string) {
	if r.o.Sink == nil {
		return
	}
	if p := r.o.Sink.Path(); p != "" {
		*lines = append(*lines, "full output: "+p)
	}
}

// summaryLine is the run's closing line, the spec's shape, with the three
// outcomes named rather than a tick that cannot show them (G3).
func summaryLine(o Outcome) string {
	steps := fmt.Sprintf("%d steps", o.Steps)
	if o.Steps == 1 {
		steps = "1 step"
	}
	elapsed := fmt.Sprintf("%.1fs", o.Elapsed.Seconds())
	switch o.State {
	case Failed:
		return fmt.Sprintf("Failed in %s, %s", elapsed, steps)
	case Left:
		return fmt.Sprintf("Left for you in %s, %s", elapsed, steps)
	default:
		return fmt.Sprintf("Done in %s, %s", elapsed, steps)
	}
}
