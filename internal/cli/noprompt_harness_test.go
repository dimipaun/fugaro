package cli

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// noprompt_harness_test.go is the closed-stdin harness of the no-prompt policy
// (docs/no-prompt-policy/implementation-plan.md, T2b). Its constraint is the
// plan's first one — "no command waits on closed stdin" — and the shape of the
// check is the plan's: walk NewRootCmd, run every leaf with stdin a closed pipe
// and a short deadline, against a fake, and name every leaf that still asks.
//
// What a closed pipe proves, and what it does not. A pipe whose write end is
// closed is not a terminal and delivers EOF to the first read, so a prompt site
// that refuses when it cannot see a terminal returns at once — and still prints
// the question it will not read an answer to, which is what the second half of
// this harness fails on. A site that reads, gets EOF and asks again instead of
// giving up never returns at all, which is what the deadline fails on. Between
// the two, no leaf may wait and no leaf may ask: the question is the red signal
// today, and the deadline is the one that a prompt answering EOF by asking
// again trips.
//
// The world a leaf runs in is the hermetic installation this package already
// builds — newBudgetFixture's temporary XDG, the local config naming project
// aurora, the RTDB fake and a file:// runs bucket — rebuilt for each leaf, so
// that a run which writes cannot decide what the next one finds. Two things are
// added to it. The outbound network is refused at once, by the same guard
// the ee CLI mains use (HTTPS_PROXY to a closed loopback port, which Go bypasses
// for loopback, so the fakes still answer); and no stream is a terminal and no
// agent marker is set, so what a leaf does about the closed stdin is the only
// thing the harness reads. A leaf that needs arguments of its own to reach its
// work is given them by closedStdinFixtures; a leaf that cannot be run
// in-process at all belongs in closedStdinNotRun, with its reason.
//
// The list this file exists to hold — closedStdinPrompts — is the allow-list the
// plan gives the lint of T1: a leaf that prints a question today is named there
// with the task that removes it, a leaf that prints one and is not named fails,
// and a named leaf that no longer prints one fails too, so the entry is deleted
// by the commit that removes the prompt rather than left to rot. It is the
// harness's one debt list, and reaching empty is the goal.

// closedStdinDeadline is how long a leaf may take with its stdin closed before
// the harness calls it a wait. It is accounts_add_login_test's and askWithin's
// own bound for the same question, against the package's ten-minute timeout,
// and it is generous on purpose: every leaf returns in milliseconds, and the
// one that does not is the failure this file is about.
const closedStdinDeadline = 10 * time.Second

// closedStdinQuestion matches the two shapes a run prints when it asks a person
// something: the ⚠ CONFIRM banner every typed step shows before it reads the
// project's name (confirmTyped, confirmTypedStrict, confirmBuild, the initflow
// steps, managed apply, managed flag), and the prompt line itself. A value's
// prompt — readValue's "Type the billing account ID: " — is the same shape and
// is matched too, which is deliberate: it is only printed at a terminal, so a
// leaf that reaches it here has found a way to ask where nobody can answer.
var closedStdinQuestion = regexp.MustCompile(`⚠ CONFIRM|Type [^\n]*: $`)

// closedStdinFixture is one leaf's run: the arguments that put it on the path
// where it would ask somebody something, and the sentence the run must still
// print, which is what says the fixture really reached that path. Without the
// second the harness would call a leaf quiet that its own arguments had merely
// stopped before, which is a green answer for the wrong reason.
type closedStdinFixture struct {
	args    []string
	reaches string
}

// closedStdinFixtures is the per-leaf fixture. A leaf not named here runs as the
// bare command, which is its own answer: it refuses at its first argument or
// config check, before anything reads stdin. The fixture is per leaf and not per
// run — one set of arguments, the shallowest that reaches the confirmation.
var closedStdinFixtures = map[string]closedStdinFixture{
	// The raise is what asks: the caps are unset in the fixture's database, so
	// setting one from nothing is a loosening.
	"fugaro budget set": {args: []string{"--global", "--per-run", "50"}, reaches: "global caps per-run: not set -> $50"},
	// The project-wide kill switch is the one that asks; a repository's is not.
	// Its sentence is the banner itself, because the kill is the whole of what
	// the run has said by the time it asks: T3, which removes the ask, changes
	// this sentence to whatever the run prints instead.
	"fugaro budget kill": {args: []string{"--all"}, reaches: "⚠ CONFIRM (project aurora): this halts every run"},
	// Nothing is killed in the fixture, so this one only has to reach the
	// database to read what it changed.
	"fugaro budget resume": {args: []string{"--all"}, reaches: "is not killed"},
	// The write asks before it rewrites the project's claude_accounts block.
	"fugaro accounts add": {args: []string{"team-a", "--repos", "acme/app"}, reaches: "claude_accounts"},
	// watch is the one leaf whose contract is not to return: it follows the
	// project until it is interrupted. --once is its own one snapshot, which is
	// the run this harness can hold to a deadline.
	"fugaro watch": {args: []string{"--once"}, reaches: "counted"},
}

// closedStdinNotRun is the listed exemption: a leaf the harness does not run,
// and why. It is empty, and that is the finding — every leaf of the tree
// reaches a verdict under a closed stdin, so none needs excusing. The list is
// here so that the first leaf that does need one is a decision written down
// beside its reason, and not an omission a reader has to notice.
var closedStdinNotRun = map[string]string{}

// closedStdinPrompts is the allow-list: the leaves that still print a question
// with a closed stdin, each with the task of the plan that removes it. Every
// entry is a promise to delete it in that task's commit, and the run's own check
// holds the promise — a leaf that stops asking fails until its entry is gone.
// The list reaching empty is what the policy's first constraint looks like from
// here.
var closedStdinPrompts = map[string]string{
	// A raise asks for the project's name before it loosens the budget; T3
	// removes the ask and leaves the owner-session fence in its place.
	"fugaro budget set": "T3",
	// The project-wide kill switch asks the same way, and T3 owns it too. A
	// repository's kill switch does not ask, which is why this fixture is
	// --all.
	"fugaro budget kill": "T3",
	// The account write asks before it rewrites the project's claude_accounts
	// block and mints the account's secret version; T5 removes the ask.
	"fugaro accounts add": "T5",
}

// TestEveryCommandRunsWithClosedStdin runs every leaf of the released tree with
// its stdin closed and holds it to the policy: it returns, it reaches the work
// its fixture is for, and it asks nothing.
func TestEveryCommandRunsWithClosedStdin(t *testing.T) {
	leaves := closedStdinLeaves(t)
	ran := 0
	for _, leaf := range leaves {
		path := leaf.CommandPath()
		if why, exempt := closedStdinNotRun[path]; exempt {
			if why == "" {
				t.Errorf("%s is exempt with no reason: an exemption nobody can read is an omission", path)
			}
			continue
		}
		ran++
		t.Run(path, func(t *testing.T) {
			// One world per leaf: a run that writes must not decide what the
			// next leaf finds, whatever order the tree walks in.
			closedStdinWorld(t)
			out, returned := closedStdinRun(t, path)
			if !returned {
				t.Fatalf("%s did not return within %s with its stdin closed: a read that waits for a person is a wait nobody can end", path, closedStdinDeadline)
			}
			if fixture, ok := closedStdinFixtures[path]; ok && !strings.Contains(out, fixture.reaches) {
				t.Fatalf("the fixture for %s no longer reaches %q, so it refuses before the work it is here to reach: the run printed %q", path, fixture.reaches, out)
			}
			closedStdinAsked(t, path, out)
		})
	}
	if ran == 0 {
		t.Fatal("the walk found no leaf to run: the harness is reading nothing")
	}
	// A name that must be a leaf whatever else moves, so that a walk which
	// stopped finding the tree fails here rather than passing over nothing.
	if !slices.Contains(closedStdinPaths(leaves), "fugaro version") {
		t.Fatal("the walk does not find `fugaro version`, which every build holds: the tree it walks is not the released one")
	}
	for _, named := range [][]string{
		closedStdinNamed(closedStdinFixtures),
		closedStdinNamed(closedStdinNotRun),
		closedStdinNamed(closedStdinPrompts),
	} {
		for _, path := range named {
			if !slices.Contains(closedStdinPaths(leaves), path) {
				t.Errorf("%s is named a leaf and is not one: a fixture, an exemption or a prompt entry that outlives its command is a rule about nothing", path)
			}
		}
	}
}

// closedStdinAsked holds one leaf's output to the policy's other half: a
// question printed with a closed stdin is a prompt nobody can answer, and it is
// either named in closedStdinPrompts or it is a failure. The reverse is checked
// here too, so the list cannot keep an entry whose prompt the task that owns it
// has removed.
func closedStdinAsked(t *testing.T, path, out string) {
	t.Helper()
	question := closedStdinQuestion.FindString(out)
	if question == "" {
		if owner, listed := closedStdinPrompts[path]; listed {
			t.Errorf("%s no longer prints a question with its stdin closed, so its closedStdinPrompts entry (%s) is stale: delete it in this commit", path, owner)
		}
		return
	}
	if _, listed := closedStdinPrompts[path]; listed {
		return
	}
	t.Errorf("%s printed a question with its stdin closed (%q): a closed stdin is nobody to answer, so the run asks a person who is not there\n%s", path, question, out)
}

// closedStdinWorld is the state every leaf is run in: one hermetic
// installation, no outbound network, no terminal and no agent marker.
func closedStdinWorld(t *testing.T) {
	t.Helper()
	// A leaf must not be able to write into the checkout the tests run from
	// (fugaro.yaml, .claude/, a layer file): the harness runs in a directory of
	// its own, which is also the honest "no checkout here" case.
	t.Chdir(t.TempDir())
	newBudgetFixture(t, "")
	// The same guard the ee CLI mains carry: an outbound request must not leave
	// this machine. Go bypasses the proxy for loopback, so the fixtures' own
	// servers still answer.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	// The update check is a GitHub request on the command's own stderr: the
	// harness's streams are buffers, so it never starts, and the switch says so
	// rather than leaving it to a stream's type.
	t.Setenv("FUGARO_NO_UPDATE_CHECK", "1")
	// Which machine this is must not decide what a run does: a coding agent's
	// session refuses writes the closed-stdin case would otherwise reach, and a
	// real terminal is what this harness is the opposite of.
	clearAgentMarkers(t)
	terminal(t, false)
}

// closedStdinRun runs one leaf with its stdin a closed pipe and reports what it
// printed and whether it returned before the deadline. The output is a
// syncBuf's because a run that timed out is still writing to it.
func closedStdinRun(t *testing.T, path string) (string, bool) {
	t.Helper()
	// The leaf's own words, without the program name: cobra is handed the
	// arguments after `fugaro`, and the path's first word is the binary.
	argv := strings.Fields(path)[1:]
	if fixture, ok := closedStdinFixtures[path]; ok {
		argv = append(argv, fixture.args...)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// The write end is closed before the run, so the pipe is at EOF: this is
	// the closed stdin the policy names, and it is an *os.File, which is what
	// makes stdinIsTerminal answer false for the unpatched reason too.
	w.Close()
	defer r.Close()
	out := &syncBuf{}
	root := NewRootCmd()
	root.SetIn(r)
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(NormalizeArgs(argv))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = root.Execute()
	}()
	select {
	case <-done:
		return out.String(), true
	case <-time.After(closedStdinDeadline):
		return out.String(), false
	}
}

// closedStdinLeaves is every command of the tree with no subcommand: the
// commands a run can end on. Cobra's own `help` and `completion` are not in the
// tree NewRootCmd builds — cobra adds them when a tree is executed — so a leaf
// here is always a command fugaro ships.
func closedStdinLeaves(t *testing.T) []*cobra.Command {
	t.Helper()
	var leaves []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if len(c.Commands()) == 0 {
			leaves = append(leaves, c)
			return
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(NewRootCmd())
	return leaves
}

// closedStdinPaths is the leaves' own paths, for the checks that a name in one
// of the lists is a command the tree still holds.
func closedStdinPaths(leaves []*cobra.Command) []string {
	paths := make([]string, 0, len(leaves))
	for _, leaf := range leaves {
		paths = append(paths, leaf.CommandPath())
	}
	return paths
}

// closedStdinNamed is one list's keys, as a slice.
func closedStdinNamed[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}

// TestClosedStdinHarnessSeesAQuestion holds the harness's own reading against
// runs whose answer is known, so that a green
// TestEveryCommandRunsWithClosedStdin means the leaves ask nothing rather than
// that a question went unnoticed. Both shapes are here — the banner and the
// prompt line — beside the refusals that must not be read as either.
func TestClosedStdinHarnessSeesAQuestion(t *testing.T) {
	for _, c := range []struct {
		name string
		out  string
		want bool
	}{
		{
			name: "the typed confirmation of a budget raise",
			out:  "config/caps/global: global caps per-run: not set -> $50\n⚠ CONFIRM (project aurora): this loosens the project's budget: runs may spend more than before\n",
			want: true,
		},
		{
			name: "the prompt line on its own",
			out:  "Type aurora to confirm: ",
			want: true,
		},
		{
			name: "a value's prompt",
			out:  "Type the billing account ID: ",
			want: true,
		},
		{
			name: "the refusal that replaces the prompt",
			out:  "the typed confirmation of this step needs a real terminal: run it in your own terminal window, not through a coding agent or a pipe\n",
			want: false,
		},
		{
			name: "a note that merely mentions a prompt",
			out:  "the value is empty; pipe it in on stdin or paste it at the prompt\n",
			want: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := closedStdinQuestion.MatchString(c.out); got != c.want {
				t.Errorf("the harness reads a question in %q as %v, want %v", c.out, got, c.want)
			}
		})
	}
}
