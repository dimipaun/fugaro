package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// TestTheIDEMarkerIsOneName: the IDE extension's variable is named in two
// packages — agentenv's classification, which decides whether a session is a
// person's, and initflow's IDEHint, which is the one refusal that tells a
// person to unset it — and a name that drifted between them would be a hint
// for a variable nobody sets, or a classification of one nobody reads. The
// two are asserted equal here rather than left to a reader: initflow keeps
// its own copy on purpose (its package doc: it names no other package), so
// this is the one place that can hold them together.
func TestTheIDEMarkerIsOneName(t *testing.T) {
	if agentenv.IDEMarker != initflow.IDEMarker {
		t.Fatalf("agentenv.IDEMarker is %q and initflow.IDEMarker is %q", agentenv.IDEMarker, initflow.IDEMarker)
	}
}

// A billable build was the typed confirmation the classification was visible
// through end to end until T4: now it asks nothing in any session, and the
// rows that pinned who could confirm it pin instead that every one of those
// sessions submits the build (TestImageBuildAsksNothing, image_gate_test.go).
// What is left here is the classification itself: the one line a run prints
// when it accepts a typed confirmation from the IDE extension's terminal.

// TestIDENoticeIsPrintedOncePerRun: the classification is asked many times in
// one run and the fact about who is there is one, so the line is printed once
// however many confirmations a run accepts — and never where the run is not a
// person's IDE terminal.
func TestIDENoticeIsPrintedOncePerRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		markers  []string
		terminal bool
		want     int
	}{
		{"the IDE marker, at a terminal", []string{agentenv.IDEMarker}, true, 1},
		{"the IDE marker, with no terminal", []string{agentenv.IDEMarker}, false, 0},
		{"the IDE marker beside an agent's", []string{agentenv.IDEMarker, "CURSOR_AGENT"}, true, 0},
		{"no marker", nil, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.terminal {
				fakeTerminal(t)
			} else {
				noTerminal(t)
			}
			for _, m := range tc.markers {
				t.Setenv(m, "1")
			}
			cmd := NewRootCmd()
			var out strings.Builder
			r := &initRun{cmd: cmd, o: &initOptions{}, w: &out}
			r.noteIDETerminal()
			r.noteIDETerminal()
			if got := strings.Count(out.String(), ideNotice); got != tc.want {
				t.Errorf("the IDE-terminal line is printed %d times, want %d:\n%s", got, tc.want, out.String())
			}
		})
	}
}
