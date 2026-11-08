package orchestrator

// This file is compiled into this package's tests and into nothing else, and
// it is separate from export_test.go because O0 wrote that one, from
// export_launch_test.go because O6 wrote that one: two writers of one table is
// the drift the plan's file list avoids.
//
// It exposes the values the tick's tests pin so that a test asserts against the
// one the code uses: the fold's mapping, which is the whole decision a finished
// run makes about an item, and the cap on one notification line. A copy of
// either written into a test beside the value it is meant to be would go on
// passing after the value moved.
const (
	// MaxNotifyLineRunes caps one notification line.
	MaxNotifyLineRunes = maxNotifyLineRunes
)

// FoldPhase is the phase an item takes when the run behind its attempt reports
// this outcome, and false for an outcome no run reports.
func FoldPhase(o Outcome) (Phase, bool) { return foldPhase(o) }
