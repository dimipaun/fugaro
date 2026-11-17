package gitprov

import "testing"

// The state of a commit's checks is the ladder's gate's whole reading of CI
// (E8, F25), so what it says for each shape is pinned here rather than left to
// the adapters: green means the checks RAN and passed, and everything else is
// unknown — never green.

func TestChecksZeroRunsIsUnknown(t *testing.T) {
	// F25's case: Fugaro's own repository runs CI only when dispatched by hand,
	// so a pull request there reports no checks at all. Nothing ran; nothing
	// passed; the gate must not read it as green.
	if got := ChecksState(nil); got != CIUnknown {
		t.Fatalf("ChecksState(nil) = %v, want unknown", got)
	}
	if got := ChecksState([]CheckRun{}); got != CIUnknown {
		t.Fatalf("ChecksState(empty) = %v, want unknown", got)
	}
	if got := CIState(0).String(); got != "unknown" {
		t.Fatalf("the zero CIState reads as %q", got)
	}
}

func TestChecksPendingIsUnknown(t *testing.T) {
	// A run that has not finished has no answer: not green, and not red
	// either, whatever the runs beside it already concluded.
	for _, runs := range [][]CheckRun{
		{{Status: "queued"}},
		{{Status: "in_progress"}},
		{{Status: "completed", Conclusion: "success"}, {Status: "in_progress"}},
		{{Status: "in_progress"}, {Status: "completed", Conclusion: "success"}},
		// Finished, with a conclusion this code has no reading for: an
		// empty one is a host that answered "completed" and nothing else.
		{{Status: "completed"}},
		{{Status: "completed", Conclusion: ""}},
	} {
		if got := ChecksState(runs); got != CIUnknown {
			t.Errorf("ChecksState(%+v) = %v, want unknown", runs, got)
		}
	}
}

func TestChecksPendingOnlyWhenACheckHasNotConcluded(t *testing.T) {
	// The three shapes ChecksState calls unknown are not one shape to a caller
	// that waits on the reading: only a run still going will report something.
	//
	// A status outside the four answers false, and that is the case the
	// ladder's gate leans on: GitHub's "waiting" is a deployment approval
	// nobody has answered, and a caller that waited on it would wait for ever
	// without telling anybody.
	for _, tc := range []struct {
		name string
		runs []CheckRun
		want bool
	}{
		{"nothing reported at all", nil, false},
		{"a check that finished, with nothing readable", []CheckRun{{Status: "completed"}}, false},
		{"a check that finished and failed", []CheckRun{{Status: "completed", Conclusion: "failure"}}, false},
		{"every check passed", []CheckRun{{Status: "completed", Conclusion: "success"}}, false},
		{"a check that is still queued", []CheckRun{{Status: "queued"}}, true},
		{"a check that has been requested", []CheckRun{{Status: "requested"}}, true},
		{"a commit status waiting to be posted", []CheckRun{{Status: "pending"}}, true},
		{"a check that finished beside one still running", []CheckRun{
			{Status: "completed", Conclusion: "success"}, {Status: "in_progress"},
		}, true},
		{"a check waiting on a person", []CheckRun{{Status: "waiting"}}, false},
		{"a status this code cannot place", []CheckRun{{Status: "invented"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChecksPending(tc.runs); got != tc.want {
				t.Fatalf("ChecksPending(%+v) = %v, want %v", tc.runs, got, tc.want)
			}
		})
	}
}

func TestChecksGreenOnlyWhenAllReportedPass(t *testing.T) {
	pass := CheckRun{Status: "completed", Conclusion: "success"}
	if got := ChecksState([]CheckRun{pass}); got != CIGreen {
		t.Fatalf("ChecksState(one passing run) = %v, want green", got)
	}
	if got := ChecksState([]CheckRun{pass, pass, pass}); got != CIGreen {
		t.Fatalf("ChecksState(three passing runs) = %v, want green", got)
	}
	// Every conclusion but success is a commit that was judged and did not
	// pass: red is a decision the gate reports, and green is not one of them.
	for _, conclusion := range []string{"failure", "cancelled", "timed_out", "neutral", "skipped", "stale", "action_required"} {
		runs := []CheckRun{pass, {Status: "completed", Conclusion: conclusion}}
		if got := ChecksState(runs); got != CIRed {
			t.Errorf("ChecksState(passing and %s) = %v, want red", conclusion, got)
		}
	}
	// One failure and one still-running run: the commit has not passed, and
	// unknown is the state that stops the gate rather than red — the run that
	// has not finished could be the one the gate would have to wait for.
	runs := []CheckRun{{Status: "completed", Conclusion: "failure"}, {Status: "queued"}}
	if got := ChecksState(runs); got != CIUnknown {
		t.Errorf("ChecksState(failed and queued) = %v, want unknown", got)
	}
	if got := CIGreen.String(); got != "green" {
		t.Errorf("CIGreen.String() = %q", got)
	}
	if got := CIRed.String(); got != "red" {
		t.Errorf("CIRed.String() = %q", got)
	}
}
