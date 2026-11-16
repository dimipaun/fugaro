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
