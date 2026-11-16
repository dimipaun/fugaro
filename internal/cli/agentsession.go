package cli

import "github.com/dimipaun/fugaro/internal/initflow"

// The agent-session refusal, as the commands outside the core reach it
// (design docs/design/ee-boundary-moves.md §10). `agentMarker` is unexported
// and `ee/` cannot import an unexported name, so a moved command that must
// refuse inside a coding agent's session calls this rather than reading the
// markers itself: the one list of markers and the one classification every
// core call site uses stay in this package, and a command that moved out of
// the core refuses in the same words as one that stayed in it.
//
// It is a speed bump and not a boundary, exactly as `agentMarker` is
// (internal/cli/creds.go says why at length): an agent's session runs under
// the operator's credentials and can unset its own marker. What it buys is
// that no agent goes through the program by accident — a cloud write an
// agent's own tool call reaches is refused with a sentence that says where to
// run it instead.
//
// getenv is the environment reader, so a test drives the refusal without
// setting the process environment, and it is a parameter rather than
// os.Getenv for the same reason `RefuseHTTP2Debug` takes one. A caller
// outside this package passes os.Getenv.
//
// clause is the caller's own sentence — what was not done, and why the command
// is a person's — up to but not including the colon before the marker. The
// reason the marker carries is appended here and never left to the caller: the
// marker is `agentMarker`'s answer, and a sentence assembled from a name the
// caller read would be a second spelling of it.
//
// nil is a person's session, so a caller returns this directly:
//
//	if err := fcli.AgentSessionRefusal(os.Getenv, "nothing was written: fugaro licence set …"); err != nil {
//		return err
//	}
func AgentSessionRefusal(getenv func(string) string, clause string) error {
	m := agentMarker(getenv, processTerminal())
	if m == "" {
		return nil
	}
	return userErr("%s: %s", clause, initflow.AgentRefusal(m))
}
