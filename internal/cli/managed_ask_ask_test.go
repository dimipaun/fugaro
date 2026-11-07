package cli

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agentenv"
)

// managed_ask_ask_test.go: askConfirm, the one confirmation a command may still
// ask. The flag is the owner's (2026-11-07, D1544) and it is the whole of it:
// nothing is asked by default, so without --ask the helper reads nothing and
// answers "yes"; with it, a terminal asks and a session that cannot answer
// refuses. The three properties the policy rests on are written here — nothing
// is read without the flag, a coding agent's session is refused, and the
// refusal is reached without touching stdin at all, which is what makes "never
// hangs" a fact about the code rather than about the machine the test ran on.

// askTestCmd is the test's own streams with the flags the helper reads, so a
// row registers exactly the ones it is about: a command that never had a
// confirmation restored is the "no --ask at all" row, and a registered flag the
// row does not pass is a command that has it and a run that did not.
func askTestCmd(t *testing.T, in io.Reader, registers []string, passed ...string) testStreams {
	t.Helper()
	s := newTestStreams(in)
	for _, name := range registers {
		s.cmd.Flags().Bool(name, false, "test flag")
	}
	for _, name := range passed {
		if err := s.cmd.Flags().Set(name, "true"); err != nil {
			t.Fatalf("passing --%s: %v", name, err)
		}
	}
	return s
}

// TestAskConfirmNeverReadsOutsideAsk: without --ask a step that used to ask
// proceeds, prints nothing and reads nothing, whatever else the command was
// passed and whatever the session is. It is the policy's own direction — the
// default is no prompt — and it is why a command can call the helper on every
// path without asking whether the flag is set.
func TestAskConfirmNeverReadsOutsideAsk(t *testing.T) {
	clearAgentMarkers(t)
	rows := []struct {
		name      string
		registers []string
		passed    []string
		terminal  bool
	}{
		{"no --ask at all", []string{"yes"}, nil, true},
		{"--ask registered and not passed", []string{"ask", "yes", "json"}, []string{"yes"}, true},
		{"--yes with no --ask", []string{"ask", "yes"}, []string{"yes"}, true},
		{"--json with no --ask", []string{"ask", "json"}, []string{"json"}, true},
		{"no terminal and no --ask", []string{"ask"}, nil, false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			terminal(t, row.terminal)
			in := &unreadStdin{}
			s := askTestCmd(t, in, row.registers, row.passed...)
			ok, err := askConfirm(s.cmd, "write it?")
			if err != nil {
				t.Fatalf("askConfirm = %v, want the policy's own answer", err)
			}
			if !ok {
				t.Error("askConfirm answered no without asking anybody")
			}
			if in.read {
				t.Error("the helper read stdin without --ask")
			}
			if s.out.String() != "" || s.errOut.String() != "" {
				t.Errorf("the helper printed a prompt without --ask: %q, %q", s.out.String(), s.errOut.String())
			}
		})
	}
}

// assertAskConfirmRefuses runs --ask in a session that cannot answer it and
// asserts the whole refusal: a user error, nothing printed, the reason named,
// and stdin never touched — the last is what keeps a prompt nobody can answer
// from becoming a wait nobody can end.
func assertAskConfirmRefuses(t *testing.T, isTerminal bool, registers []string, why string, passed ...string) {
	t.Helper()
	terminal(t, isTerminal)
	in := &unreadStdin{}
	s := askTestCmd(t, in, registers, passed...)
	ok, err := askConfirm(s.cmd, "write it?")
	if err == nil {
		t.Fatalf("--ask was answered in a session that cannot answer it (returned %v)", ok)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), why) {
		t.Errorf("the refusal does not say why nobody can answer it (%q): %v", why, err)
	}
	if in.read {
		t.Error("the helper read stdin where nobody can answer a prompt")
	}
	if s.out.String() != "" || s.errOut.String() != "" {
		t.Errorf("a refusal printed a prompt: %q, %q", s.out.String(), s.errOut.String())
	}
}

// TestAskConfirmRefusesInAnAgentSession is the fence --ask does not lift: the
// flag asks for confirmations, and a coding agent's session is one that cannot
// give them, so the step is refused rather than answered by nobody. The loop is
// over agentenv.AgentMarkers itself, so a marker the tree adds is one this
// helper is held to, and it runs at a terminal because that is the worst case:
// the marker says "agent" while the input says "a person is there", and the
// marker wins.
//
// The other sessions that cannot answer — --json, --non-interactive, a pipe —
// are the same refusal one row further down, and the markers are cleared for
// them so the reason each row asserts is the reason it was refused for.
func TestAskConfirmRefusesInAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.AgentMarkers {
		t.Run(marker, func(t *testing.T) {
			t.Setenv(marker, "1")
			assertAskConfirmRefuses(t, true, []string{"ask"}, marker, "ask")
		})
	}
	rows := []struct {
		name     string
		terminal bool
		passed   []string
		why      string
	}{
		{"--json at a terminal", true, []string{"ask", "json"}, "--json"},
		{"--non-interactive at a terminal", true, []string{"ask", "non-interactive"}, "--non-interactive"},
		{"a pipe", false, []string{"ask"}, "no terminal"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			clearAgentMarkers(t)
			assertAskConfirmRefuses(t, row.terminal, []string{"ask", "json", "non-interactive"}, row.why, row.passed...)
		})
	}
}

// TestAskConfirmAsksInAPersonsIDETerminal is the one marker-and-terminal
// session that is not an agent's (agentenv.Session): the Claude Code IDE
// extension sets its marker in a person's own integrated terminal too, so with
// a real terminal on the input and no agent-set marker beside it, --ask is
// answered by the person who asked for it and the run says which terminal
// accepted it. It is the row that keeps the loop above honest: a fence that
// refused every marker would refuse a person at their own keyboard.
func TestAskConfirmAsksInAPersonsIDETerminal(t *testing.T) {
	clearAgentMarkers(t)
	t.Setenv(agentenv.IDEMarker, "1")
	terminal(t, true)
	s := askTestCmd(t, strings.NewReader("y\n"), []string{"ask"}, "ask")
	ok, err := askConfirm(s.cmd, "write it?")
	if err != nil {
		t.Fatalf("a person's own IDE terminal was refused: %v", err)
	}
	if !ok {
		t.Error("the y typed at the prompt did not confirm the step")
	}
	if !strings.Contains(s.out.String(), "write it? [y/N]") {
		t.Errorf("the question was not asked: %q", s.out.String())
	}
	if !strings.Contains(s.out.String(), agentenv.IDEMarker) {
		t.Errorf("the run does not say which terminal accepted the confirmation: %q", s.out.String())
	}
}

// blockedStdin is stdin that delivers nothing until the test closes stop: it is
// what makes "does not hang" a property of the code, because a read that
// happened would block until askWithin's deadline failed the test instead of
// passing quietly on a machine where stdin happened to be at EOF.
type blockedStdin struct{ stop chan struct{} }

func (r *blockedStdin) Read([]byte) (int, error) {
	<-r.stop
	return 0, io.EOF
}

// askWithin runs a call that must answer without a person typing and fails the
// test when it has not returned. Ten seconds is accounts_add_login_test's own
// bound for the same question, against the package's ten-minute timeout: a
// refusal that waits for input is the failure the no-prompt policy removes.
func askWithin(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not return: it is waiting for input nobody can type", what)
	}
}

// TestAskInAnAgentSessionFailsFastAndDoesNotHang is the property the whole
// policy is about, at both entries that read a confirmation: a coding agent's
// session, whose input is a terminal it can hold, with --ask passed. Neither
// the step-level confirmation nor the flag's own run-level decision may wait
// for a person there, and neither may print the prompt it will not read an
// answer to.
func TestAskInAnAgentSessionFailsFastAndDoesNotHang(t *testing.T) {
	terminal(t, true)
	t.Setenv("CLAUDECODE", "1")
	in := &blockedStdin{stop: make(chan struct{})}
	defer close(in.stop)
	s := askTestCmd(t, in, []string{"ask"}, "ask")

	var ok bool
	var confirmErr error
	askWithin(t, "askConfirm", func() { ok, confirmErr = askConfirm(s.cmd, "write it?") })
	if confirmErr == nil {
		t.Fatalf("askConfirm was answered in a coding agent's session (returned %v)", ok)
	}
	if !strings.Contains(confirmErr.Error(), "CLAUDECODE") {
		t.Errorf("the refusal does not name the marker that made the session an agent's: %v", confirmErr)
	}

	decideOut := &syncBuf{}
	var decideErr error
	askWithin(t, "askDecide", func() {
		decideErr = askDecide(decideOut, askFlags{ask: true}, true, askConditions(s.cmd, false, false))
	})
	if decideErr == nil {
		t.Fatal("the flag was honoured in a coding agent's session")
	}
	if s.out.String() != "" || s.errOut.String() != "" || decideOut.String() != "" {
		t.Errorf("a prompt was printed with nobody to answer it: %q, %q, %q", s.out.String(), s.errOut.String(), decideOut.String())
	}
}
