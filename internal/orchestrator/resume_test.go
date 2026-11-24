package orchestrator_test

// PR4's tests: the resident orchestrator resumes what is due (D3185, D3189).
//
// A pause is the credentials service's own record — the node under
// runstore.PauseNodeRoot — and the run that suspended itself ended with the
// status `paused`. What these tests pin is the whole of what the resumer
// decides: a pause whose resume time has passed and whose run really ended
// `paused` is started once as the run that continues it; a cancelled pause and a
// run that did not end `paused` are not started at all; and two ticks never
// start one pause twice.
//
// The reader and the launcher are the surroundings a tick is wired to, exactly
// as the run view is in tick_test.go: what is under test is the decision and the
// carrying, not the database the CLI reads the nodes out of (I2 wires that) and
// not the launch itself (the CLI's own path).

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// pauseStub is the credentials service's record as a test sets it: the pauses
// the reader answers with, the failure it is set to answer instead, and how
// many times it was asked.
type pauseStub struct {
	paused []orchestrator.PausedRun
	err    error
	reads  int
}

func (p *pauseStub) Pauses(context.Context) ([]orchestrator.PausedRun, error) {
	p.reads++
	if p.err != nil {
		return nil, p.err
	}
	return p.paused, nil
}

// resumeStub is the launcher a resume goes through: what it was asked to
// continue, the run id it answers with, and the refusal it is set to answer
// instead.
type resumeStub struct {
	asked  []orchestrator.ResumeRequest
	answer string
	refuse error
}

func (r *resumeStub) Resume(_ context.Context, req orchestrator.ResumeRequest) (string, error) {
	r.asked = append(r.asked, req)
	if r.refuse != nil {
		return "", r.refuse
	}
	return r.answer, nil
}

// pauseNode is one node of the service's record, as the database holds it: the
// node's own bytes, which is what a reader answers with and what the resumer
// parses.
func pauseNode(t *testing.T, n runstore.PauseNode) []byte {
	t.Helper()
	raw, err := n.Bytes()
	if err != nil {
		t.Fatalf("building the paused node: %v", err)
	}
	return raw
}

// pauseOf is one pause as the reader answers it: the node, the status the paused
// run's own record ended with, and the run that has resumed it.
func pauseOf(t *testing.T, slug, run string, node runstore.PauseNode, status runstore.Status, resumedBy string) orchestrator.PausedRun {
	t.Helper()
	return orchestrator.PausedRun{Slug: slug, Run: run, Node: pauseNode(t, node), Status: status, ResumedBy: resumedBy}
}

// pauseHarness is a tick wired to a pause reader and a resume launcher, over the
// state a paused run's chain leaves behind: the item is running and its intent
// is open, which is what a run in flight looks like — the paused run's item
// stays in flight and the resume is not a plan action.
func pauseHarness(t *testing.T, now time.Time, paused []orchestrator.PausedRun, answer string) (*harness, *pauseStub, *resumeStub) {
	t.Helper()
	const chainRun = "20261107-090000-abcd"
	w, it := o6OpenItem("wi-1", chainRun, now.Add(-time.Hour), now.Add(-30*time.Minute))
	return pauseHarnessOver(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now, paused, answer)
}

// pauseHarnessOver is the same tick over a state the test built itself, for the
// tests that seed the node with something else as well (the breaker's question).
func pauseHarnessOver(t *testing.T, s orchestrator.State, now time.Time, paused []orchestrator.PausedRun, answer string) (*harness, *pauseStub, *resumeStub) {
	t.Helper()
	h := newHarness(t, s, now)
	pauses := &pauseStub{paused: paused}
	launcher := &resumeStub{answer: answer}
	h.tick.Pauses, h.tick.Resume = pauses, launcher
	return h, pauses, launcher
}

// dueNode is the node of a pause that is due now: the service's own record of
// the grant, with a resume time a minute behind the tick's clock.
func dueNode(now time.Time) runstore.PauseNode {
	return runstore.PauseNode{
		Launcher:      "launcher@example.invalid",
		Pool:          "shared",
		ResumeAt:      now.Add(-time.Minute).UnixMilli(),
		Chain:         "20261107-090000-abcd",
		PausedSeconds: 600,
	}
}

// TestADuePauseIsResumedOnce is PR4's headline: the tick reads the service's
// record of a pause whose resume time has passed and starts one run that
// continues it — the same repository, the same paused run named, and the pause's
// own node handed to the launcher whole — and a second tick of the same pause
// starts nothing, because the chain now carries the run that resumed it.
//
// What the resumed run is told is the launcher's business (the task it writes
// names `resumes`, I2): what is pinned here is that this side decides nothing
// about the pause's time, launcher or pool — the node travels whole and the
// figures in it are the service's — and that a resume writes nothing to the
// state node. The record of a resume is the chain's own run, and a mark written
// here would be a second writer of the service's record (R19).
func TestADuePauseIsResumedOnce(t *testing.T) {
	now := o6Now()
	const (
		slug      = "acme/web"
		pausedRun = "20261107-090000-abcd"
		newRun    = "20261107-120500-beef"
	)
	h, pauses, launcher := pauseHarness(t, now,
		[]orchestrator.PausedRun{pauseOf(t, slug, pausedRun, dueNode(now), runstore.StatusPaused, "")}, newRun)
	before := h.rev()

	rep := h.step()
	if rep.Resumed != 1 {
		t.Fatalf("the tick resumed %d pause(s), want the one that is due: %+v", rep.Resumed, rep)
	}
	if rep.Outcome != orchestrator.CycleProgress {
		t.Errorf("a cycle that resumed a pause ended %q, want %q: it started a run", rep.Outcome, orchestrator.CycleProgress)
	}
	if rep.ResumeErr != nil {
		t.Errorf("the tick refused a pause it resumed: %v", rep.ResumeErr)
	}
	if len(launcher.asked) != 1 {
		t.Fatalf("the launcher was asked to resume %d pause(s), want exactly 1: %+v", len(launcher.asked), launcher.asked)
	}
	got := launcher.asked[0]
	if got.Slug != slug || got.Run != pausedRun {
		t.Errorf("the launcher was handed %q/%q, want the pause's own repository and run (%q/%q)", got.Slug, got.Run, slug, pausedRun)
	}
	if got.Node.Chain != pausedRun || got.Node.Launcher != "launcher@example.invalid" || got.Node.ResumeAt != dueNode(now).ResumeAt {
		t.Errorf("the launcher was handed the node %+v, want the service's own record of the pause whole", got.Node)
	}
	if h.rev() != before {
		t.Errorf("the resume wrote the state node: the record of a resume is the chain's own run and not a mark this side writes (R19)")
	}

	// The run that resumed the pause is in the chain now, and the reader is what
	// says so: the node carries no resumed mark, and a reader that answered one
	// from the node would be reading a member the service never wrote
	// (runstore.PauseMemberResumedBy).
	pauses.paused[0].ResumedBy = newRun
	rep = h.step()
	if rep.Resumed != 0 {
		t.Errorf("a second tick resumed the same pause again: %+v", rep)
	}
	if len(launcher.asked) != 1 {
		t.Errorf("the pause was handed to the launcher %d times, want once: two ticks never resume one node twice", len(launcher.asked))
	}
}

// TestACancelledPauseIsNotResumed: a pause whose node carries the cancellation
// its launcher set is never resumed (D3197) — it is still readable as the pause
// it was, and it is not a refusal either: nothing is wrong with it, the person
// who owns the run ended it.
func TestACancelledPauseIsNotResumed(t *testing.T) {
	now := o6Now()
	node := dueNode(now)
	node.Cancelled = true
	h, _, launcher := pauseHarness(t, now,
		[]orchestrator.PausedRun{pauseOf(t, "acme/web", "20261107-090000-abcd", node, runstore.StatusPaused, "")},
		"20261107-120500-beef")

	rep := h.step()
	if rep.Resumed != 0 {
		t.Errorf("a cancelled pause was resumed: %+v", rep)
	}
	if len(launcher.asked) != 0 {
		t.Errorf("a cancelled pause reached the launcher: %+v", launcher.asked)
	}
	if rep.ResumeErr != nil {
		t.Errorf("a cancelled pause was refused as an error (%v), want the plain answer that it is not resumed", rep.ResumeErr)
	}
}

// TestARunThatDidNotEndPausedIsNotResumed: the node is the service's record of a
// pause, and the run it was written for did not end `paused` — a `/pause` posted
// by an agent whose runner went on running resumes nothing. The tick reads the
// status the run's own record ended with, and only a run that really ended
// `paused` is continued.
func TestARunThatDidNotEndPausedIsNotResumed(t *testing.T) {
	now := o6Now()
	const slug = "acme/web"
	h, _, launcher := pauseHarness(t, now, []orchestrator.PausedRun{
		pauseOf(t, slug, "20261107-090000-aaaa", dueNode(now), runstore.StatusRunning, ""),
		pauseOf(t, slug, "20261107-090000-bbbb", dueNode(now), runstore.StatusSucceeded, ""),
		pauseOf(t, slug, "20261107-090000-cccc", dueNode(now), runstore.StatusPaused, ""),
	}, "20261107-120500-beef")

	rep := h.step()
	if rep.Resumed != 1 {
		t.Fatalf("the tick resumed %d pause(s), want exactly the one whose run ended paused: %+v", rep.Resumed, rep)
	}
	if len(launcher.asked) != 1 || launcher.asked[0].Run != "20261107-090000-cccc" {
		t.Fatalf("the launcher was asked about %+v, want the run that really ended paused and no other", launcher.asked)
	}
}

// TestAResumeWhoseTimeHasNotComeIsNotStarted: the resume time is the service's
// own figure and the tick reads it and decides nothing about it — a pause whose
// instant is still ahead is left alone, and the next tick that reaches the
// instant is the one that resumes it.
func TestAResumeWhoseTimeHasNotComeIsNotStarted(t *testing.T) {
	now := o6Now()
	node := dueNode(now)
	node.ResumeAt = now.Add(time.Minute).UnixMilli()
	h, _, launcher := pauseHarness(t, now,
		[]orchestrator.PausedRun{pauseOf(t, "acme/web", "20261107-090000-abcd", node, runstore.StatusPaused, "")},
		"20261107-120500-beef")

	rep := h.step()
	if rep.Resumed != 0 {
		t.Errorf("a pause whose resume time had not come was resumed: %+v", rep)
	}
	if len(launcher.asked) != 0 {
		t.Errorf("a pause that is not due reached the launcher: %+v", launcher.asked)
	}
}

// TestARefusedResumeDoesNotHoldTheOthersBack: a launcher that will not start one
// pause is a line the report carries and not a failed cycle — the pause is still
// due at the next tick, and the pauses behind it are started — which is the
// resident pass's own rule, and the reason a resident pass may carry this half
// at all (ResidentResumes).
func TestARefusedResumeDoesNotHoldTheOthersBack(t *testing.T) {
	now := o6Now()
	const slug = "acme/web"
	h, _, launcher := pauseHarness(t, now, []orchestrator.PausedRun{
		pauseOf(t, slug, "20261107-090000-aaaa", dueNode(now), runstore.StatusPaused, ""),
		pauseOf(t, slug, "20261107-090000-bbbb", dueNode(now), runstore.StatusPaused, ""),
	}, "20261107-120500-beef")
	launcher.refuse = errors.New("no room for another run")

	rep := h.step()
	if rep.Resumed != 0 {
		t.Errorf("a refused resume was counted as resumed: %+v", rep)
	}
	if rep.ResumeErr == nil {
		t.Errorf("the launcher's refusal is not in the report: %+v", rep)
	}
	if len(launcher.asked) != 2 {
		t.Errorf("the launcher was asked about %d pause(s), want both: one refusal must not hold the others back", len(launcher.asked))
	}
}

// TestANodeThisReleaseCannotReadIsNotResumed: the service's node is read by this
// release, and a node carrying a member this release does not know is refused
// rather than half-read (a pause spans a fugaro update, F36) — the pass carries
// on with the others and the report says which one it would not act on.
func TestANodeThisReleaseCannotReadIsNotResumed(t *testing.T) {
	now := o6Now()
	unknown, err := json.Marshal(map[string]any{
		"launcher": "launcher@example.invalid", "resume_at": dueNode(now).ResumeAt,
		"chain": "20261107-090000-abcd", "paused_for": 60,
	})
	if err != nil {
		t.Fatalf("building the node: %v", err)
	}
	h, _, launcher := pauseHarness(t, now, []orchestrator.PausedRun{
		{Slug: "acme/web", Run: "20261107-090000-aaaa", Node: unknown, Status: runstore.StatusPaused},
		pauseOf(t, "acme/web", "20261107-090000-bbbb", dueNode(now), runstore.StatusPaused, ""),
	}, "20261107-120500-beef")

	rep := h.step()
	if rep.Resumed != 1 {
		t.Fatalf("the tick resumed %d pause(s), want the one it can read: %+v", rep.Resumed, rep)
	}
	if len(launcher.asked) != 1 || launcher.asked[0].Run != "20261107-090000-bbbb" {
		t.Fatalf("the launcher was asked about %+v, want the readable pause and no other", launcher.asked)
	}
	if rep.ResumeErr == nil {
		t.Errorf("the unreadable node is not in the report: %+v", rep)
	}
}

// TestAResumeAsksTheKillSwitchAndThePause is the guard every launch path asks
// before it starts anything (Caps.KillSwitch's own note): a resume starts a run,
// so it asks the two switches the plan, the ladder and the decision agent ask —
// the kill switch, which carries both of them folded into one and is ON where
// the reading failed, and the daemon's pause, the breaker's question standing on
// a node (BreakerOpen). A pass under either starts nothing and reads nothing:
// a daemon that could not read its budget, a project whose orchestrator switch
// is off, and a daemon a breaker has paused all resume nothing.
//
// Mutation: drop the guard from resumeDue — both halves resume the pause.
func TestAResumeAsksTheKillSwitchAndThePause(t *testing.T) {
	now := o6Now()
	const (
		slug      = "acme/web"
		pausedRun = "20261107-090000-abcd"
		newRun    = "20261107-120500-beef"
	)
	due := func(t *testing.T) []orchestrator.PausedRun {
		t.Helper()
		return []orchestrator.PausedRun{pauseOf(t, slug, pausedRun, dueNode(now), runstore.StatusPaused, "")}
	}

	t.Run("the kill switch", func(t *testing.T) {
		h, pauses, launcher := pauseHarness(t, now, due(t), newRun)
		h.tick.Caps = withCaps(func(c *orchestrator.Caps) { c.KillSwitch = true })

		rep := h.step()
		if rep.Resumed != 0 || len(launcher.asked) != 0 {
			t.Errorf("a cycle under the kill switch resumed a pause: %+v, launcher asked %+v", rep, launcher.asked)
		}
		if pauses.reads != 0 {
			t.Errorf("a cycle under the kill switch read the project's pauses %d time(s): a stopped daemon has no business on the project's database", pauses.reads)
		}
	})

	t.Run("the breaker's question standing on the node", func(t *testing.T) {
		w, it := o6OpenItem("wi-1", pausedRun, now.Add(-time.Hour), now.Add(-30*time.Minute))
		s := breakerQuestion(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
		h, pauses, launcher := pauseHarnessOver(t, s, now, due(t), newRun)

		rep := h.step()
		if rep.Resumed != 0 || len(launcher.asked) != 0 {
			t.Errorf("a cycle whose node carries the breaker's question resumed a pause: %+v, launcher asked %+v", rep, launcher.asked)
		}
		if pauses.reads != 0 {
			t.Errorf("a paused cycle read the project's pauses %d time(s)", pauses.reads)
		}
	})
}

// TestATickWithNoPauseReaderResumesNothing: the pauses are read through the
// reader the daemon is given and through nothing else — there is no second path
// to the paused run's own bucket in this package, because the record a job wrote
// about its own pause is a hostile agent's word (R23) and a resume derived from
// it is the case D3189 does not cover (F52). A tick with no reader resumes
// nothing at all, which is what a daemon that was given no path to the project's
// database must do.
func TestATickWithNoPauseReaderResumesNothing(t *testing.T) {
	now := o6Now()
	w, it := o6OpenItem("wi-1", "20261107-090000-abcd", now.Add(-time.Hour), now.Add(-30*time.Minute))
	h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
	launcher := &resumeStub{answer: "20261107-120500-beef"}
	h.tick.Resume = launcher

	rep := h.step()
	if rep.Resumed != 0 || len(launcher.asked) != 0 {
		t.Errorf("a tick with no pause reader resumed something: %+v", rep)
	}
}

// TestThePauseReadFailingFailsTheCycle: a project whose pauses could not be read
// is a project whose daemon is working blind — the resume it owes is one it
// cannot see — so the cycle fails rather than carrying on as though nothing were
// paused, exactly as a run view that could not be read does.
func TestThePauseReadFailingFailsTheCycle(t *testing.T) {
	now := o6Now()
	h, pauses, _ := pauseHarness(t, now, nil, "20261107-120500-beef")
	pauses.err = errors.New("the database would not answer")

	if _, err := h.tick.Step(context.Background()); err == nil {
		t.Fatalf("a cycle whose pauses could not be read ended with no error")
	}
}
