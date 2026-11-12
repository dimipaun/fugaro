package gitprov

// A commit's checks, read the way the escalation ladder's gate reads them
// (E8, D1245). Nothing here reads a branch protection rule: the gate's
// question is what the checks reported at the commit said, and a commit whose
// checks cannot be read is never green.

// CIState is the state of one commit's checks, as a Checks read reports it.
//
// The zero value is CIUnknown, which is what the ladder's gate must see when
// anything is missing: a read that failed, a caller that ignored an error, and
// a commit with no checks at all all fail closed.
type CIState int

const (
	// CIUnknown is a commit whose checks do not say it passed: no check was
	// reported at all, a reported one has not finished, or the read failed.
	// It is never green.
	CIUnknown CIState = iota
	// CIGreen is at least one reported check, every one of them concluded
	// success.
	CIGreen
	// CIRed is at least one reported check that concluded something other
	// than success.
	CIRed
)

// String names the state for a message; a value outside the three is unknown,
// which is the direction this type fails in.
func (s CIState) String() string {
	switch s {
	case CIGreen:
		return "green"
	case CIRed:
		return "red"
	default:
		return "unknown"
	}
}

// CheckRun is one check reported at a commit: whether it has finished, and
// what it concluded when it did. The spellings are GitHub's own — status
// "queued", "in_progress" or "completed", and, once completed, a conclusion
// such as "success", "failure", "neutral", "cancelled", "skipped" or
// "timed_out" — and an adapter that reads another host's reports maps them
// onto these two words rather than passing its own through, so ChecksState has
// one reading and not one per host.
type CheckRun struct {
	Status     string
	Conclusion string
}

// The two words ChecksState reads: a check that has finished, and the one
// conclusion that counts as passing. Every other status and conclusion is
// somebody else's vocabulary, and neither passes.
const (
	checkCompleted = "completed"
	checkSuccess   = "success"
)

// ChecksState is the state of a commit's checks: CIGreen only when at least
// one check is reported and every reported one concluded success.
//
// A commit with no checks, a check that has not finished, and a finished check
// whose conclusion this code cannot read are CIUnknown rather than green.
// That last one is the case F25 is about: a repository that runs no CI on
// pull requests reports no checks at all, and reading "nothing ran here" as
// green would merge on nothing. A check that concluded anything else —
// failure, cancelled, timed_out, neutral, skipped — is CIRed: the commit was
// judged, and it did not pass.
func ChecksState(runs []CheckRun) CIState {
	if len(runs) == 0 {
		return CIUnknown
	}
	state := CIGreen
	for _, r := range runs {
		switch {
		case r.Status != checkCompleted:
			// Still queued or running: the commit's checks have no answer
			// yet, whatever the ones that have finished said.
			return CIUnknown
		case r.Conclusion == "":
			// Finished with nothing this code can read.
			return CIUnknown
		case r.Conclusion != checkSuccess:
			state = CIRed
		}
	}
	return state
}
