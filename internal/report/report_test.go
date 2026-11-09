package report

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// noEnv is an environment with nothing set, so a rule that reads a variable
// reads it unset.
func noEnv(string) string { return "" }

// envOf is an environment of exactly the variables given.
func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// notATerminal is the writer decision of every hermetic test: the reporter's
// lines are bytes in a buffer, and nothing may be drawn for a screen.
func notATerminal(io.Writer) bool { return false }

// aTerminal is the decision a real run makes on a person's terminal.
func aTerminal(io.Writer) bool { return true }

// fakeSink is CL3's log without its file: the reporter only ever writes to it
// and reads its path.
type fakeSink struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	path string
}

func (s *fakeSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *fakeSink) Path() string { return s.path }

func (s *fakeSink) Close() error { return nil }

func (s *fakeSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// lines is the output's non-empty lines: the reporter's view is one line per
// step plus the summary, so an empty line is a bug and not a line.
func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestNoSpinnerAndNoAnsiOffTerminal(t *testing.T) {
	// G4: no ANSI, no spinner and no carriage return when stdout is not a
	// terminal, NO_COLOR is set, TERM=dumb, or --no-color.
	cases := []struct {
		name       string
		env        map[string]string
		isTerminal func(io.Writer) bool
		noColor    bool
		ascii      bool
	}{
		{"stdout is not a terminal", nil, notATerminal, false, false},
		{"NO_COLOR", map[string]string{"NO_COLOR": "1"}, aTerminal, false, false},
		{"TERM=dumb", map[string]string{"TERM": "dumb"}, aTerminal, false, false},
		{"--no-color", nil, aTerminal, true, false},
		{"--ascii", nil, aTerminal, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			r := New(Options{
				Out: &out, Getenv: envOf(c.env), IsTerminal: c.isTerminal,
				NoColor: c.noColor, ASCII: c.ascii,
			})
			r.Step("Publishing the policy").Done("changed")
			r.Summary()
			got := out.String()
			if strings.Contains(got, "\x1b") {
				t.Errorf("ANSI reached a writer no rule allows colour on:\n%q", got)
			}
			if strings.Contains(got, "\r") {
				t.Errorf("a carriage return reached a writer no rule allows a spinner on:\n%q", got)
			}
			if n := len(lines(got)); n != 2 {
				t.Errorf("the view is %d lines, want one per step and one summary:\n%q", n, got)
			}
		})
	}
}

func TestJSONModePrintsNothingOnStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r := New(Options{Out: &stdout, Err: &stderr, Getenv: noEnv, IsTerminal: aTerminal, JSON: true})
	r.Step("Publishing the policy").Done("changed")
	r.Summary()
	if stdout.Len() != 0 {
		t.Errorf("--json wrote %q to stdout: stdout is one document (G2)", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Publishing the policy") {
		t.Errorf("the human line went nowhere; stderr holds %q", stderr.String())
	}
}

func TestLeftForYouIsNotATick(t *testing.T) {
	var out bytes.Buffer
	sink := &fakeSink{path: "/state/fugaro/logs/update-20261107T000000Z-1.log"}
	r := New(Options{Out: &out, Getenv: noEnv, NoColor: true, Sink: sink})
	r.Step("Enable the billing account").Left("run this in your own terminal window")
	o := r.Summary()
	got := out.String()
	if strings.Contains(got, glyphDone.sym) {
		t.Errorf("a step left for the user printed the tick:\n%q", got)
	}
	if !strings.Contains(got, glyphLeft.sym+" Enable the billing account: run this in your own terminal window") {
		t.Errorf("the left-for-you line is missing its own marker and text:\n%q", got)
	}
	if !strings.Contains(got, "full output: "+sink.path) {
		t.Errorf("the left-for-you line does not say where the log is:\n%q", got)
	}
	if o.State != Left {
		t.Errorf("Summary reported %v, want Left: a step left for the user is exit 1, never a done (G3)", o.State)
	}
}

func TestSummaryUsesTheInjectedClock(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	r := New(Options{Out: &out, Getenv: noEnv, NoColor: true, Now: func() time.Time { return now }})
	r.Step("Updating recipes").Done("")
	now = now.Add(4200 * time.Millisecond)
	r.Step("Publishing the policy").Done("")
	o := r.Summary()
	if want := 4200 * time.Millisecond; o.Elapsed != want {
		t.Errorf("Summary elapsed is %v, want %v: the time comes from the injected clock, not the wall", o.Elapsed, want)
	}
	if o.Steps != 2 {
		t.Errorf("Summary counts %d steps, want 2", o.Steps)
	}
	if o.State != Done {
		t.Errorf("Summary state is %v, want Done", o.State)
	}
	if want := "Done in 4.2s, 2 steps\n"; !strings.HasSuffix(out.String(), want) {
		t.Errorf("the closing line is missing: want it to end with %q, got:\n%q", want, out.String())
	}
}

func TestFailedStepPrintsContextAndSinkPath(t *testing.T) {
	var out bytes.Buffer
	sink := &fakeSink{path: "/state/fugaro/logs/update-20261107T000000Z-1.log"}
	r := New(Options{Out: &out, Getenv: noEnv, NoColor: true, Sink: sink})
	r.Step("Publish the policy").Failed(errors.New("exit status 1"), []string{
		"Error: permission denied on the policy node",
		"  with google_project_iam_member.policy",
	})
	o := r.Summary()
	got := out.String()
	for _, want := range []string{
		"Publish the policy",
		"exit status 1",
		"Error: permission denied on the policy node",
		"with google_project_iam_member.policy",
		"full output: " + sink.path,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the failure block does not print %q:\n%q", want, got)
		}
	}
	if o.State != Failed {
		t.Errorf("Summary state is %v, want Failed", o.State)
	}
}

// TestTheLogHoldsTheRunsOwnLines: the log is the run's full output (D1352), so
// a command whose own output is its steps keeps them — a run that reported a
// step and a closing line cannot leave an empty file at the path a failure
// prints. The log gets the plain line: the colour that helps a person watching
// is noise in a file.
func TestTheLogHoldsTheRunsOwnLines(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	sink := &fakeSink{path: "/state/fugaro/logs/deploy-20261107T090000Z-1.log"}
	// A terminal, so the person's copy is styled and the log's is not.
	r := New(Options{Out: &out, Getenv: noEnv, IsTerminal: aTerminal, Sink: sink, Now: func() time.Time { return now }})
	r.Step("Publish the policy").Done("published")
	r.Step("Backfill").Failed(errors.New("exit status 1"), []string{"Error: permission denied"})
	r.Summary()
	log := sink.String()
	for _, want := range []string{
		glyphDone.sym + " Publish the policy published",
		glyphFailed.sym + " Backfill: exit status 1",
		"    Error: permission denied",
		"full output: " + sink.path,
		"Failed in 0.0s, 2 steps",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log does not hold %q:\n%q", want, log)
		}
	}
	if strings.Contains(log, "\x1b") {
		t.Errorf("the log carries the terminal's colour codes:\n%q", log)
	}
}

// TestLogIsTheSinkAlone: Reporter.Log is for a command that prints its own
// lines to its own writer and wants them kept beside it — the log, and not the
// terminal a second time, and nothing at all for a run with no log.
func TestLogIsTheSinkAlone(t *testing.T) {
	var out bytes.Buffer
	sink := &fakeSink{}
	r := New(Options{Out: &out, Getenv: noEnv, NoColor: true, Sink: sink})
	if n, err := r.Log().Write([]byte("the marker is removed\n")); err != nil || n != len("the marker is removed\n") {
		t.Fatalf("a log write = %d, %v", n, err)
	}
	if !strings.Contains(sink.String(), "the marker is removed") {
		t.Errorf("the line did not reach the log: %q", sink.String())
	}
	if out.Len() != 0 {
		t.Errorf("the log writer wrote to the terminal as well: %q", out.String())
	}
	bare := New(Options{Out: &out, Getenv: noEnv, NoColor: true})
	if _, err := bare.Log().Write([]byte("nowhere\n")); err != nil {
		t.Fatalf("a run with no log failed a write: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("a run with no log wrote to the terminal: %q", out.String())
	}
}

func TestStreamIsVerboseOnlyAndAlwaysSunk(t *testing.T) {
	t.Run("the log only", func(t *testing.T) {
		var out bytes.Buffer
		sink := &fakeSink{}
		r := New(Options{Out: &out, Getenv: noEnv, NoColor: true, Sink: sink})
		if _, err := r.Stream().Write([]byte("terraform: creating...\n")); err != nil {
			t.Fatalf("a stream write failed: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("the default view streamed %q to the terminal: it is one line per step", out.String())
		}
		if !strings.Contains(sink.String(), "terraform: creating...") {
			t.Errorf("the log does not hold the run's own output: %q", sink.String())
		}
	})

	t.Run("the terminal too under --verbose", func(t *testing.T) {
		var out bytes.Buffer
		sink := &fakeSink{}
		r := New(Options{Out: &out, Getenv: noEnv, NoColor: true, Verbose: true, Sink: sink})
		if _, err := r.Stream().Write([]byte("terraform: creating...\n")); err != nil {
			t.Fatalf("a stream write failed: %v", err)
		}
		if !strings.Contains(out.String(), "terraform: creating...") {
			t.Errorf("--verbose did not stream the run's own output: %q", out.String())
		}
		if !strings.Contains(sink.String(), "terraform: creating...") {
			t.Errorf("the log does not hold the run's own output: %q", sink.String())
		}
	})
}

func TestAStepEndsOnce(t *testing.T) {
	var out bytes.Buffer
	r := New(Options{Out: &out, Getenv: noEnv, NoColor: true})
	step := r.Step("Updating recipes")
	step.Done("changed")
	// A caller that reports the same step twice — a deferred failure after a
	// success, a stage that both left something for the user and did not —
	// must not print a second line or count a second time.
	step.Done("changed again")
	step.Failed(errors.New("too late"), nil)
	o := r.Summary()
	if o.Steps != 1 {
		t.Errorf("the run counts %d steps, want 1: a step ends once", o.Steps)
	}
	if got := lines(out.String()); len(got) != 2 {
		t.Errorf("a step ended twice printed %d lines, want the step and the summary:\n%q", len(got), out.String())
	}
	if strings.Contains(out.String(), "too late") || strings.Contains(out.String(), "changed again") {
		t.Errorf("the second result reached the screen:\n%q", out.String())
	}
	if o.State != Done {
		t.Errorf("a late failure moved the run to %v, want Done: only the first result counts", o.State)
	}
}

func TestNoRunningLineUnderVerbose(t *testing.T) {
	// --verbose streams the run's own output through the reporter, so the
	// terminal belongs to the stream: a carriage-return line in front of it
	// would be drawn through the middle of it.
	was := spinInterval
	spinInterval = time.Hour
	t.Cleanup(func() { spinInterval = was })

	var out bytes.Buffer
	r := New(Options{Out: &out, Getenv: noEnv, IsTerminal: aTerminal, Verbose: true})
	step := r.Step("Updating recipes")
	if out.Len() != 0 {
		t.Errorf("a step in flight drew %q on a --verbose terminal", out.String())
	}
	if _, err := r.Stream().Write([]byte("terraform: creating...\n")); err != nil {
		t.Fatalf("a stream write failed: %v", err)
	}
	step.Done("")
	got := out.String()
	if strings.Contains(got, "\r") {
		t.Errorf("a --verbose run drew a carriage return:\n%q", got)
	}
	if !strings.Contains(got, "terraform: creating...") {
		t.Errorf("--verbose did not stream the run's own output:\n%q", got)
	}
}

func TestSpinnerDrawsOnTheTerminalsOwnLine(t *testing.T) {
	// The animation is driven by a ticker; a test that wants one frame sets
	// the interval out of its own way.
	was := spinInterval
	spinInterval = time.Hour
	t.Cleanup(func() { spinInterval = was })

	var term bytes.Buffer
	r := New(Options{Out: &term, Getenv: noEnv, IsTerminal: aTerminal})
	step := r.Step("Updating recipes")
	running := term.String()
	if !strings.HasPrefix(running, "\r") || strings.HasSuffix(running, "\n") {
		t.Errorf("the step in flight is not drawn on the line itself: %q", running)
	}
	if !strings.Contains(running, "Updating recipes") {
		t.Errorf("the line in flight does not name the step: %q", running)
	}
	step.Done("")
	got := term.String()
	if !strings.Contains(got, glyphDone.sym+"\x1b[0m") {
		t.Errorf("the result line does not carry the tick:\n%q", got)
	}
	if !strings.Contains(got, "Updating recipes") {
		t.Errorf("the result line lost the step's name:\n%q", got)
	}
}

func TestPauseKeepsAPromptOnItsOwnLine(t *testing.T) {
	// G1: a prompt is written on a line of its own. The line in flight goes
	// down before the first byte of it, and comes back when the answer
	// arrives — a stage that prompts in the middle of a long apply is still
	// one somebody is watching.
	was := spinInterval
	spinInterval = time.Hour
	t.Cleanup(func() { spinInterval = was })

	var out bytes.Buffer
	r := New(Options{Out: &out, Getenv: noEnv, IsTerminal: aTerminal})
	step := r.Step("Creating the project")
	// Nothing may be drawn on the prompt's line: it holds a carriage return
	// back to column 0 and then the clear, and nothing between them.
	r.Pause()
	if got := out.String(); !strings.HasSuffix(got, "\r\x1b[K") {
		t.Errorf("the line in flight was not taken down before the prompt: %q", got)
	}
	fmt.Fprint(&out, "Type aurora: ")
	if got := out.String(); !strings.HasSuffix(got, "Type aurora: ") {
		t.Errorf("the prompt is not the last thing on the screen: %q", got)
	}
	r.Resume()
	if got := out.String(); !strings.HasSuffix(got, "Creating the project") {
		t.Errorf("the answer did not put the line in flight back: %q", got)
	}
	// A resume with nothing paused draws nothing: the reporter is not a
	// second caller's to animate.
	before := out.String()
	r.Resume()
	if out.String() != before {
		t.Errorf("a second resume drew again: %q", out.String())
	}
	step.Done("created")
	// Two lines in flight — the step's own, before the pause and after it —
	// and then the one result.
	if got := strings.Count(out.String(), "Creating the project"); got != 3 {
		t.Errorf("the run named the step %d times, want two lines in flight and one result:\n%q", got, out.String())
	}
	// And nothing is left animating after the run: a resume once the step
	// ended draws nothing.
	after := out.String()
	r.Resume()
	if out.String() != after {
		t.Errorf("a resume after the step ended drew over the account: %q", out.String())
	}
}

func TestAbandonTakesAStoppedRunsLineDown(t *testing.T) {
	// A refusal stops the run before the stage has a result: nothing is
	// printed for it, and the line in flight still comes down — it must not
	// outlive the run that drew it.
	was := spinInterval
	spinInterval = time.Hour
	t.Cleanup(func() { spinInterval = was })

	var out bytes.Buffer
	r := New(Options{Out: &out, Getenv: noEnv, IsTerminal: aTerminal})
	step := r.Step("Creating the project")
	step.Abandon()
	if got := out.String(); !strings.HasSuffix(got, "\r\x1b[K") {
		t.Errorf("the line in flight is still on the screen: %q", got)
	}
	if o := r.Summary(); o.Steps != 0 {
		t.Errorf("an abandoned step counted %d steps, want none", o.Steps)
	}
	if s := out.String(); strings.Contains(s, "Creating the project\n") {
		t.Errorf("an abandoned step printed a line:\n%q", s)
	}
	step.Done("late")
	if s := out.String(); strings.Contains(s, glyphDone.sym) {
		t.Errorf("a result after Abandon printed a line:\n%q", s)
	}
}

// TestShortIsOneClauseAndTheLogKeepsTheWholeOfIt: the converge's account is
// the brief one (Brief), and what makes it brief is this — a reason longer
// than one line is cut at the clause the reason itself carries, and the step's
// whole reason is still written to the log, which is where the command already
// sends a person for the rest. A command that leaves Brief false prints the
// reason it was given, byte for byte.
func TestShortIsOneClauseAndTheLogKeepsTheWholeOfIt(t *testing.T) {
	const reason = "not in a checkout of a repository: run `fugaro update` there to take the repository's own steps, and `fugaro setup --repo` in a checkout to enrol one"
	want := "not in a checkout of a repository: run `fugaro update` there to take…"
	if got := Short(reason); got != want {
		t.Errorf("Short(%q) = %q, want %q", reason, got, want)
	}
	if n := len([]rune(want)); n > shortMax+1 {
		t.Errorf("Short gave %d runes, want at most %d and the ellipsis", n, shortMax+1)
	}
	for _, o := range []struct {
		name  string
		brief bool
		want  string
	}{
		{"the converge's account", true, "– firebase " + want + "\n"},
		{"every other command's", false, "– firebase " + reason + "\n"},
	} {
		t.Run(o.name, func(t *testing.T) {
			var out bytes.Buffer
			sink := &fakeSink{path: "/tmp/x.log"}
			r := New(Options{Out: &out, Getenv: noEnv, Sink: sink, Brief: o.brief})
			r.Step("firebase").Skipped(reason)
			if got := out.String(); got != o.want {
				t.Errorf("the step line is %q, want %q", got, o.want)
			}
			if log := sink.String(); log != "– firebase "+reason+"\n" {
				t.Errorf("the log holds %q, want the whole reason", log)
			}
		})
	}
}

// TestShortLeavesAShortReasonAlone: most reasons are short phrases and must not
// move — not a character, not a trailing space.
func TestShortLeavesAShortReasonAlone(t *testing.T) {
	for _, reason := range []string{
		"",
		"No changes",
		"applied",
		"14 to create",
		"no --firebase-project given",
		"the project layer's bucket could not be read",
		strings.Repeat("a", shortMax-1),
	} {
		if got := Short(reason); got != reason {
			t.Errorf("Short(%q) = %q, want it unchanged", reason, got)
		}
	}
	// A reason that carries a clause boundary inside the window stops there
	// rather than at the window's own edge.
	long := "the given reason is long enough to be cut at the clause (and there is more of it after that)"
	if got := Short(long); got != "the given reason is long enough to be cut at the clause…" {
		t.Errorf("Short cut at %q, want the clause", got)
	}
}

// TestWaitsIsOneLineForARunOfStages: a run that stopped at a stage leaves every
// stage behind it saying the same thing, and the account says it once: at most
// three stages are named and the rest are counted, the verb agrees with the
// subject, and the summary still counts the stages rather than the lines.
func TestWaitsIsOneLineForARunOfStages(t *testing.T) {
	var out bytes.Buffer
	r := New(Options{Out: &out, Getenv: noEnv, Sink: &fakeSink{path: "/tmp/x.log"}})
	r.Waits([]string{"firebase", "images", "secrets", "plugin", "repository"}, "installation")
	got := lines(out.String())
	if len(got) != 1 {
		t.Fatalf("the waiting account is %d lines, want one:\n%q", len(got), out.String())
	}
	if want := "↳ firebase, images, secrets and 2 more wait for installation"; got[0] != want {
		t.Errorf("the waiting line is %q, want %q", got[0], want)
	}
	if o := r.Summary(); o.Steps != 5 {
		t.Errorf("the run reported %d steps, want the 5 stages the one line stands for", o.Steps)
	}

	var one bytes.Buffer
	r = New(Options{Out: &one, Getenv: noEnv})
	r.Waits([]string{"repository"}, "secrets")
	if want := "↳ repository waits for secrets"; lines(one.String())[0] != want {
		t.Errorf("one stage's line is %q, want %q", lines(one.String())[0], want)
	}

	var none bytes.Buffer
	r = New(Options{Out: &none, Getenv: noEnv})
	r.Waits(nil, "secrets")
	if none.String() != "" {
		t.Errorf("a run with no waiting stage printed %q", none.String())
	}
}
