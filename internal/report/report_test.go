package report

import (
	"bytes"
	"errors"
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
