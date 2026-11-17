package orchestrator

// This file is compiled into this package's tests and into nothing else, and
// it is separate from export_test.go because O0 wrote that one, from
// export_launch_test.go because O6 wrote that one: two writers of one table is
// the drift the plan's file list avoids.
//
// It exposes the values the tick's tests pin so that a test asserts against the
// one the code uses: the cap on one notification line. A copy of it written
// into a test beside the value it is meant to be would go on passing after the
// value moved. The fold's mapping, the other value this file once carried, is
// export_test.go's now: G3 gave it the pull request a run left to read, and one
// exposure of one function belongs in one file.
const (
	// MaxNotifyLineRunes caps one notification line.
	MaxNotifyLineRunes = maxNotifyLineRunes
)
