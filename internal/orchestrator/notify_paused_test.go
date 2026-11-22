package orchestrator_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The notice an exhausted chain leaves (PR8, C3).
//
// A chain that runs out of models ends a run in one of two ways, and a person
// is told which: the run suspended itself and comes back at the instant the
// credentials service computed, or it halted where it stood and nothing will
// resume it. The two are different kinds because they ask different things of
// whoever reads them — one needs nothing from a person and the other needs the
// person — and both are lines on the sink the daemon already has: no channel is
// built here (R13, D141 stands), and the mobile push plan delivers the pause
// when it lands.
//
// Every report below is what a tick's reader answers. What a reader answers for
// a run whose record says `paused` is that reader's own work (PR11, PR12), so
// the outcome here is deliberately the one the view answers today: these cases
// are about what the person is told, which the outcome does not decide.

// logLines is the notifications as the daemon's own sink writes them: the one
// line per notification that a person reads, which is what "printed" means
// here.
func logLines(t *testing.T, told []orchestrator.Notification) []string {
	t.Helper()
	var buf bytes.Buffer
	sink := orchestrator.LogSink{W: &buf}
	for _, n := range told {
		if err := sink.Notify(context.Background(), n); err != nil {
			t.Fatalf("the log sink answered %v", err)
		}
	}
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

// onlyNotification is the one thing a tick said, and it fails rather than
// returning the first of several: a cycle that told a person twice about one
// run is itself the finding.
func onlyNotification(t *testing.T, told []orchestrator.Notification) orchestrator.Notification {
	t.Helper()
	if len(told) != 1 {
		t.Fatalf("the tick told a person %d time(s): %+v, and one run that ended is one notification", len(told), told)
	}
	return told[0]
}

func TestNoticeForAPausedRun(t *testing.T) {
	now := o6Now()
	const runID = "20261122-120000-abcd"

	// The instant the credentials service computed: beyond the in-job wait, so
	// the run suspended itself rather than waiting inside the job (D3184).
	resumeAt := now.Add(45 * time.Minute)

	// pausedNode is the node the service writes for the run that suspended
	// itself (C3): the launcher it was launched by, the instant it comes back,
	// and the chain it belongs to.
	pausedNode := func() runstore.PauseNode {
		return runstore.PauseNode{Launcher: "person@example.com", ResumeAt: resumeAt.UnixMilli(), Chain: runID}
	}

	// runTick is a tick whose reader answers one report about the run behind
	// wi-1's first attempt. The retry id is minted because a report the view
	// answers `failed` is one a standing approval retries; what a paused
	// attempt should be to the fold is PR11's and PR12's, and this test is
	// about the notice.
	runTick := func(t *testing.T, r orchestrator.RunReport) *harness {
		t.Helper()
		w, it := o6OpenItem("wi-1", runID, now.Add(-time.Hour), now.Add(-30*time.Minute))
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now, "20261122-130000-bbbb")
		r.RunID, r.Started = runID, true
		h.reader.reports = []orchestrator.RunReport{r}
		return h
	}

	t.Run("a run that suspended itself is told, with the instant it comes back", func(t *testing.T) {
		node := pausedNode()
		h := runTick(t, orchestrator.RunReport{Finished: true, Outcome: orchestrator.OutcomeFailed, Pause: &node})

		h.step()

		n := onlyNotification(t, h.sink.got)
		if n.Kind != orchestrator.NotifyRunPaused {
			t.Errorf("a run that suspended itself was told as %q, and the pause is a kind of its own", n.Kind)
		}
		if n.Subject != runID {
			t.Errorf("the notice is about %q, and the run that paused is %q", n.Subject, runID)
		}
		// The instant is the service's and not the run's (R8, D3189): no
		// record carries one, and a job that named it would be choosing when
		// to spend.
		if !strings.Contains(n.Text, resumeAt.UTC().Format(time.RFC3339)) {
			t.Errorf("the line is %q, and it does not say when the run comes back (%q)", n.Text, resumeAt.UTC().Format(time.RFC3339))
		}

		lines := logLines(t, h.sink.got)
		if len(lines) != 1 || !strings.Contains(lines[0], string(orchestrator.NotifyRunPaused)) || !strings.Contains(lines[0], runID) {
			t.Errorf("the log sink printed %q, and one paused run is one line naming what it is and what it is about", lines)
		}
	})

	// A cancelled pause is never resumed (D3197), so this line may not promise
	// the return the other reading of the same node carries.
	t.Run("a pause its launcher cancelled says so", func(t *testing.T) {
		node := pausedNode()
		node.Cancelled = true
		h := runTick(t, orchestrator.RunReport{Finished: true, Outcome: orchestrator.OutcomeFailed, Pause: &node})

		h.step()

		n := onlyNotification(t, h.sink.got)
		if n.Kind != orchestrator.NotifyRunPaused {
			t.Errorf("a cancelled pause was told as %q, and the run is still a suspended one", n.Kind)
		}
		if !strings.Contains(n.Text, "cancelled") || !strings.Contains(n.Text, "nothing resumes it") {
			t.Errorf("the line is %q, and a cancelled pause is never resumed", n.Text)
		}
	})

	t.Run("a pause that ended as halted is told, with the halt's own sentence", func(t *testing.T) {
		// The halt the service's `halt` answer — or a `/pause` it refused —
		// leaves on the record: the run ended at once and named the reset it
		// would not wait for (C3).
		const detail = "the earliest reset is 2026-11-22T14:00:00Z, and this job must be finished by 2026-11-22T12:30:00Z"
		h := runTick(t, orchestrator.RunReport{
			Finished: true,
			Outcome:  orchestrator.OutcomeRefused,
			Halt:     orchestrator.Halt{Reason: string(runstore.HaltNoCredential), Scope: "run", Detail: detail},
		})

		h.step()

		n := onlyNotification(t, h.sink.got)
		if n.Kind != orchestrator.NotifyPauseHalted {
			t.Errorf("a pause that ended as a halt was told as %q, and nothing will resume that run", n.Kind)
		}
		if n.Subject != runID {
			t.Errorf("the notice is about %q, and the run that halted is %q", n.Subject, runID)
		}
		if !strings.Contains(n.Text, "nothing resumes it") || !strings.Contains(n.Text, detail) {
			t.Errorf("the line is %q, and it does not say that no resume is coming and what the run's operator read (%q)", n.Text, detail)
		}

		lines := logLines(t, h.sink.got)
		if len(lines) != 1 || !strings.Contains(lines[0], string(orchestrator.NotifyPauseHalted)) || !strings.Contains(lines[0], runID) {
			t.Errorf("the log sink printed %q, and one halted run is one line naming what it is and what it is about", lines)
		}
	})

	// Every other ending keeps the line it always had: the two new kinds are
	// the pause's own, not a second reading of every finished run.
	t.Run("a run that ended for any other reason keeps the run-finished line", func(t *testing.T) {
		h := runTick(t, orchestrator.RunReport{
			Finished: true,
			Outcome:  orchestrator.OutcomeRefused,
			// A cap, the kill switch or a governance tripwire: a halt the
			// ladder already knows about and not the pause's failure.
			Halt: orchestrator.Halt{Reason: string(runstore.HaltRunCap), Scope: "run", Detail: "run spent $5.00 of $5.00"},
		})

		h.step()

		if n := onlyNotification(t, h.sink.got); n.Kind != orchestrator.NotifyRunFinished {
			t.Errorf("a run that ended under a cap was told as %q, and that is the run-finished line and not the pause's", n.Kind)
		}
	})
}
