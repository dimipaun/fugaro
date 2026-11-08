package cli

// The account a mutating command prints through (CL7a, D1354).
//
// Spec §1's default view is one line per step, the closing line, and nothing
// else on the human writer, and D1352 keeps the run's own output — a child's
// stream, a stage's own detail — in a log beside it. Both are the reporter's
// (CL1) and the log is CL3's; what is here is the one place a command's run is
// assembled and torn down, so no command forgets to close the log it opened or
// to name the run's outcome. `fugaro setup` and `fugaro update` build theirs
// through reporterFor directly (CL5b, init.go), because the converge's run
// carries a redaction list of its own.

import (
	"io"
	"slices"
	"sync"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/report"
)

// stepRun is one mutating command's run: the reporter that prints its steps,
// and the log the run's own output is kept in. A nil log is a run that could
// not open one, which is never a failure and never changes an exit code (G5).
type stepRun struct {
	rep *report.Reporter
	log report.Sink

	// The redaction source the log reads at each line is guarded because a
	// step of the run and the goroutine the log is written from are not the
	// same one. held is what this run holds (hold).
	mu   sync.Mutex
	held []string
}

// stepRunFor opens the run for the command name the log is filed under (D1351)
// and its redaction source: the values the process held from the start
// (secretsHeld) and the values the run itself registers as it reads them
// (hold). No value one of these commands reads is ever written to the run's
// own output, and the list is what keeps a child that echoes one out of the
// log; it is read at each line rather than copied, so a value registered after
// the log was opened is still one the log holds (F6).
//
// out is where the human lines go: the command's own stdout for a command
// whose stdout is a person's, and its stderr for one whose stdout is an answer
// of its own — accounts add's and accounts rm's block — so the answer stays a
// document and the account of the run goes where the stages' own output
// already went. reporterFor reads the writer it is handed to decide a terminal
// and a colour, and the writer it reads is the command's own Out, so the
// command's Out is pointed at out for the construction alone.
func stepRunFor(cmd *cobra.Command, name string, out io.Writer) *stepRun {
	answer := cmd.OutOrStdout()
	if out != answer {
		cmd.SetOut(out)
	}
	s := &stepRun{}
	s.rep, s.log = reporterFor(cmd, name, s.redaction)
	cmd.SetOut(answer)
	return s
}

// redaction is what the log is filtered with at the moment a line is written.
func (s *stepRun) redaction() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(slices.Clone(secretsHeld()), s.held...)
}

// hold registers value with this run's own redaction for the rest of the run:
// the value a command read, and each long enough line of a multi-line one, the
// way the converge's own slots hold what a stage prompts for (initEngine.hold,
// heldValues). The run keeps it rather than releasing it at the store call,
// because the log covers the whole command and a child behind the store may
// echo what it was given. It cannot fail: the converge's slots are a fixed
// list that has to refuse a value it has no room for, while one of these
// commands holds one value at a time and this list simply grows.
func (s *stepRun) hold(value []byte) {
	vals := heldValues(value)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held = append(s.held, vals...)
}

// close ends the run: the closing line, and then the log shut. It is deferred
// by the command, so a run that returns on a refusal still brings the line in
// flight down — and a run that ended no step prints no closing line at all
// (Reporter.Summary), which is a refusal's account.
func (s *stepRun) close() {
	s.rep.Summary()
	if s.log != nil {
		_ = s.log.Close()
	}
}

// ask runs f with the line in flight taken down for its whole length. f prints
// something a person has to read — a diff, a CONFIRM block, the prompt under
// it — and a frame drawn over those swallows them or the answer typed under
// them (G1). Ending a step takes the line down too, so a run whose step ends
// inside f needs nothing more.
func (s *stepRun) ask(f func() error) error {
	s.rep.Pause()
	defer s.rep.Resume()
	return f()
}
