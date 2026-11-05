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

// ideTerminalTypedConfirmationCase is one row of the ruling of 2026-11-05: the
// Claude Code IDE extension sets CLAUDE_CODE_SSE_PORT in a person's own
// terminal too, so that marker alone, with a real terminal on the command's
// input, is a person's terminal and not a coding agent's session.
type ideTerminalTypedConfirmationCase struct {
	name     string
	markers  []string
	terminal bool
	want     bool
	notice   int
}

// ideTerminalTypedConfirmationCases is the table, with its oracle written by
// hand rather than derived from the code under test: the markers a coding
// agent's own processes set are the five named here, and the IDE extension's
// marker is the sixth, accepted only where a terminal is there and no
// agent-set marker is beside it.
func ideTerminalTypedConfirmationCases() []ideTerminalTypedConfirmationCase {
	agentSet := []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_REMOTE", "CURSOR_AGENT", "AI_AGENT"}
	cases := []ideTerminalTypedConfirmationCase{
		{"the IDE marker alone, at a terminal", []string{agentenv.IDEMarker}, true, true, 1},
		{"the IDE marker alone, with no terminal", []string{agentenv.IDEMarker}, false, false, 0},
		{"no marker at all, at a terminal", nil, true, true, 0},
	}
	for _, m := range agentSet {
		cases = append(cases,
			ideTerminalTypedConfirmationCase{m + " alone, at a terminal", []string{m}, true, false, 0},
			ideTerminalTypedConfirmationCase{m + " alone, with no terminal", []string{m}, false, false, 0},
			ideTerminalTypedConfirmationCase{m + " beside the IDE marker, at a terminal", []string{m, agentenv.IDEMarker}, true, false, 0},
			ideTerminalTypedConfirmationCase{m + " beside the IDE marker, with no terminal", []string{m, agentenv.IDEMarker}, false, false, 0},
		)
	}
	return cases
}

// TestIDETerminalTypedConfirmation: a billable image build is the typed
// confirmation never covered by --yes, so it is where the classification is
// visible end to end — the name typed at a person's own IDE terminal submits
// the build, with one line saying which terminal it was accepted from, and
// nothing else about the fence moves.
func TestIDETerminalTypedConfirmation(t *testing.T) {
	for _, tc := range ideTerminalTypedConfirmationCases() {
		t.Run(tc.name, func(t *testing.T) {
			fb, _ := cloudBuildCheckout(t, false)
			if tc.terminal {
				fakeTerminal(t)
			} else {
				noTerminal(t)
			}
			for _, m := range tc.markers {
				t.Setenv(m, "1")
			}
			out, errOut, err := executeStdin(t, "aurora\n", "image", "build", "--base", "b:1")
			// The line is counted per stream, not over both: the build's
			// confirmation shows its ⚠ CONFIRM banner and reads the name on
			// stderr, so the line that says the name was accepted from an IDE
			// terminal belongs there and nowhere else. Counting the two
			// together would also pass for a line printed by some other
			// writer of the same command, which is the thing this asserts
			// against.
			noticeErr, noticeOut := strings.Count(errOut, ideNotice), strings.Count(out, ideNotice)
			notices := noticeErr + noticeOut
			if noticeOut != 0 {
				t.Errorf("the IDE-terminal line is on stdout, where the build's confirmation does not write:\n%s", out)
			}
			if tc.want {
				if err != nil || buildPosts(fb) != 1 {
					t.Fatalf("the typed confirmation was not accepted: err %v, %d build(s)\n%s%s", err, buildPosts(fb), out, errOut)
				}
			} else {
				if buildPosts(fb) != 0 {
					t.Fatal("a build was submitted")
				}
				if err == nil {
					t.Fatalf("nothing was refused:\n%s%s", out, errOut)
				}
				// The refusal is the marker's and not the route's: every row
				// here carries a marker the classification reads as an agent's
				// session — the IDE marker's rows with no terminal included —
				// so the variable is named. A refusal that only said "type it
				// at a terminal" would leave the marker rule unpinned, which is
				// the half this test exists for.
				if !strings.Contains(err.Error(), tc.markers[0]) {
					t.Errorf("the refusal does not name the marker %s: %v", tc.markers[0], err)
				}
			}
			if notices != tc.notice {
				t.Errorf("the IDE-terminal line is printed %d times, want %d:\n%s%s", notices, tc.notice, out, errOut)
			}
		})
	}
}

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
