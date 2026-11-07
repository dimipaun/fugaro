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

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/report"
)

// stepRun is one mutating command's run: the reporter that prints its steps,
// and the log the run's own output is kept in. A nil log is a run that could
// not open one, which is never a failure and never changes an exit code (G5).
type stepRun struct {
	rep *report.Reporter
	log report.Sink
}

// stepRunFor opens the run for the command name the log is filed under (D1351)
// and its redaction source, which is the process-wide list of values a command
// may hold (secretsHeld): no value one of these commands reads is ever written
// to the run's own output, and the list is what keeps a child that echoes one
// from putting it in the log.
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
	rep, log := reporterFor(cmd, name, secretsHeld)
	cmd.SetOut(answer)
	return &stepRun{rep: rep, log: log}
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
