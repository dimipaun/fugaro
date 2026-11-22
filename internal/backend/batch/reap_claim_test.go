package batch

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/backendtest"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// This file is the reaper's half of the run's identity (BR14, D3106 as amended,
// F31; docs/batch-run-identity/reconciliation.md): a Batch run that carries the
// ticket its launch put on it does not outlive that ticket in the queue. The
// ticket is the identity the launcher minted for the run -- the variable the
// execution carries (identitywire.TicketEnv) and the value the issuer minted --
// and it lives for a stated hour, so a job that has still not been given a VM
// when the life is spent can only fail at its first call. The queue deadline
// alone would leave such a job waiting out Batch's own capacity, and a VM
// billed for a run that cannot claim; the ticket's deadline ends it a pass
// early instead.
//
// The reaper reads the ticket's NAME and never its value (see jobCarriesTicket
// and the last test here): the presence of the variable is the whole of the
// fact the shorter deadline rests on.

// addTicketJob is addJob with a task environment: the shape a Batch launch
// creates, one task group whose taskSpec carries the variables the job runs
// with (internal/backend/batch/run.go sets exactly this for a launch). The
// reaper's own harness writes no taskGroups, so this is the only way a test
// builds the job a ticket is on -- and the fake returns the body it was created
// with from jobs.get and jobs.list alike, which is what the reaper reads.
func (rt *reapTest) addTicketJob(t *testing.T, runID, state string, age time.Duration, env map[string]string) string {
	t.Helper()
	id := JobID(backendtest.Slug, backendtest.Workflow, runID)
	labels, err := Labels(backendtest.Slug, backendtest.Workflow, runID)
	if err != nil {
		t.Fatalf("Labels: %v", err)
	}
	jobLabels, allocLabels := map[string]string{}, map[string]string{}
	for k, v := range labels {
		jobLabels[k], allocLabels[k] = v, v
	}
	body := map[string]any{
		"labels":           jobLabels,
		"allocationPolicy": map[string]any{"labels": allocLabels},
		"status":           map[string]any{"state": "QUEUED"},
		"taskGroups": []any{map[string]any{
			"taskSpec": map[string]any{"environment": map[string]any{"variables": env}},
		}},
	}
	rt.create(t, id, body)
	rt.bf.SetStateAt(id, state, rt.now.Add(-age))
	rt.bf.SetCreated(id, rt.now.Add(-age))
	return id
}

// ticket is the environment of a job the launcher handed a ticket to. The
// value is deliberately not a ticket: this package must decide on the name
// alone (TestTheReaperNeverReadsTheTicketsValue), so the value the tests use is
// one nothing could parse.
func ticketEnv() map[string]string {
	return map[string]string{identitywire.TicketEnv: "a-ticket-nobody-could-parse"}
}

// actionReason is the reason of the action on a target, which is where the
// report says WHICH deadline a job was ended by: a delete that names the queue
// deadline for a job whose ticket ended it tells an operator the wrong thing
// about a launch.
func actionReason(rep Report, kind ActionKind, target string) string {
	for _, a := range rep.Actions {
		if a.Kind == kind && a.Target == target {
			return a.Reason
		}
	}
	return ""
}

// TestAJobQueuedPastItsTicketIsDeleted is BR14's red step (a job queued for two
// hours is not left to start): a job carrying a run's identity ticket, still in
// QUEUED long past the ticket's life, is deleted with a reason that names the
// ticket, and the queue it was waiting in no longer holds it.
//
// The delete is affordable for the same reason the queue deadline's is: a job
// that never left QUEUED never ran a task, so there is no result.json and no
// VM, and the run's launch.json is the launcher's own object in the runs
// bucket, which the delete leaves in place (design §1.4).
func TestAJobQueuedPastItsTicketIsDeleted(t *testing.T) {
	rt := newReaperTest(t, nil)
	const run = "20260927-100000-abcd"
	// Two hours: the ticket's life is the hour the launcher plan states (R2),
	// so the wait is long past the deadline that life implies whatever the
	// queue deadline is -- and the job carries the ticket its launch put on it,
	// so the deadline that applies is the ticket's.
	id := rt.addTicketJob(t, run, "QUEUED", 2*time.Hour, ticketEnv())
	// Its VM, old enough that only the job behind it stands between it and the
	// reaper: the run is over, and Batch tears a deleted job's VMs down itself.
	rt.vm("vm-1", runLabels(t, run), rt.old())
	rep := rt.reap(t)
	if got := rt.bf.Deletes(); len(got) != 1 || got[0] != id {
		t.Fatalf("jobs.delete = %v, want [%s]: a job queued for two hours past its ticket's life was left to start", got, id)
	}
	if !hasAction(rep, ActionDeleteJob, rt.name(id)) {
		t.Fatalf("report = %v, want a delete of %s", rep.Lines(), id)
	}
	// Reported with that reason, and with the ticket's own words: the operator
	// reading the report has to see that this run's ticket ended it, not that
	// it waited out the queue deadline.
	want := fmt.Sprintf("queued for 120 minutes and never left QUEUED, past the ticket deadline of %.0f", rt.r.TicketDeadline().Minutes())
	if got := actionReason(rep, ActionDeleteJob, rt.name(id)); got != want {
		t.Fatalf("the delete's reason = %q, want %q", got, want)
	}
	// Ended in the queue: the job's record is gone, so nothing reading the
	// queue shows a run still waiting for capacity, and the pass after this one
	// has no job left to delete.
	if got := rt.bf.IDs(); len(got) != 0 {
		t.Fatalf("the queue still holds %v, want the run's job gone", got)
	}
	for _, a := range rt.reap(t).Actions {
		if a.Kind == ActionDeleteJob {
			t.Fatalf("a second pass deleted a job again: %v", a)
		}
	}
}

// TestTheTicketDeadlineLeavesATickAndAMargin pins the deadline itself and both
// sides of it: the ticket's life less the last of it a run needs to boot and
// claim, and less the interval the reaper's own schedule runs it on, with a job
// exactly at it left alone and one a second past it deleted.
//
// Both subtractions are load-bearing. Without the start margin the reaper would
// end a job that Batch could still have started in time; without the interval
// it would leave jobs inside the deadline at one pass to spend it before the
// next, which is a job given a VM with a ticket that cannot cover its first
// call -- the state the whole rule exists to keep a run out of (F31).
func TestTheTicketDeadlineLeavesATickAndAMargin(t *testing.T) {
	rt := newReaperTest(t, nil)
	want := TicketLife - ReapMargin - TicketStartMargin
	if got := rt.r.TicketDeadline(); got != want {
		t.Fatalf("TicketDeadline() = %v, want %v: the ticket's life %v less the reaper's own interval %v and the start margin %v", got, want, TicketLife, ReapMargin, TicketStartMargin)
	}
	// And it is the SHORTER deadline on this installation, which is what makes
	// it the one that decides: a ticket deadline past the queue deadline would
	// change nothing about when a job goes.
	if want >= rt.r.QueueDeadline() {
		t.Fatalf("the ticket deadline %v is not shorter than the queue deadline %v, so this test proves nothing about which is used", want, rt.r.QueueDeadline())
	}
	// One tick either side, both jobs carrying the ticket: what saves the first
	// is the boundary and not the absence of one.
	at := rt.addTicketJob(t, "20260927-100000-abcd", "QUEUED", want, ticketEnv())
	past := rt.addTicketJob(t, "20260927-100001-abcd", "QUEUED", want+time.Second, ticketEnv())
	rep := rt.reap(t)
	if got := rt.bf.Deletes(); len(got) != 1 || got[0] != past {
		t.Fatalf("jobs.delete = %v, want [%s] and nothing else: a job exactly at the ticket deadline is inside it", got, past)
	}
	if lines := strings.Join(rep.Lines(), "\n"); strings.Contains(lines, at) {
		t.Fatalf("report = %v, want no line about the job that is exactly at the deadline", rep.Lines())
	}
	if got := actionReason(rep, ActionDeleteJob, rt.name(past)); !strings.Contains(got, "past the ticket deadline of") {
		t.Fatalf("the delete's reason = %q, want the ticket deadline named", got)
	}
	// One tick either side, on the queue itself: the job at the deadline is
	// still waiting, the one a second past it is gone.
	if got := rt.bf.IDs(); len(got) != 1 || got[0] != at {
		t.Fatalf("the queue holds %v, want only the job at the deadline", got)
	}
}

// TestAJobWithoutATicketKeepsTheQueueDeadline is the other half of the rule: a
// job carrying no ticket is judged by the queue deadline it always was, and a
// job that carries other variables and not the ticket is one of those. Nothing
// about the ticket's presence may be inferred from anything else on the job.
func TestAJobWithoutATicketKeepsTheQueueDeadline(t *testing.T) {
	rt := newReaperTest(t, nil)
	// Longer than the ticket's deadline (43 minutes on this installation) and
	// far inside the queue deadline (a day): a job deleted at this age would be
	// one the ticket's deadline had been applied to.
	inTicketDeadline := 2 * rt.r.TicketDeadline()
	// A job created the way the launcher creates one of a run that mints its
	// own identity: no task environment at all. And one whose environment holds
	// other variables: the NAME is the whole of the fact, so a variable sitting
	// beside it changes nothing.
	bare := rt.addJob(t, "20260927-100000-abcd", "QUEUED", inTicketDeadline)
	other := rt.addTicketJob(t, "20260927-100001-abcd", "QUEUED", inTicketDeadline, map[string]string{"FUGARO_SOMETHING_ELSE": "1"})
	// And the boundary of the deadline they do keep: a job with no ticket
	// exactly at the queue deadline is inside it.
	at := rt.addJob(t, "20260927-100002-abcd", "QUEUED", rt.r.QueueDeadline())
	// While one past it goes, with the queue deadline's own words -- so this is
	// a rule about which deadline applies and not a pass that deletes nothing.
	past := rt.addJob(t, "20260927-100003-abcd", "QUEUED", rt.r.QueueDeadline()+time.Second)
	rep := rt.reap(t)
	if got := rt.bf.Deletes(); len(got) != 1 || got[0] != past {
		t.Fatalf("jobs.delete = %v, want [%s]: a job with no ticket keeps the queue deadline", got, past)
	}
	for _, id := range []string{bare, other, at} {
		if lines := strings.Join(rep.Lines(), "\n"); strings.Contains(lines, id) {
			t.Fatalf("report = %v, want no line about %s: the ticket's deadline is not its deadline", rep.Lines(), id)
		}
	}
	if got := actionReason(rep, ActionDeleteJob, rt.name(past)); !strings.Contains(got, "past the queue deadline of") {
		t.Fatalf("the delete's reason = %q, want the queue deadline named for a job that carries no ticket", got)
	}
}

// TestTheReaperNeverReadsTheTicketsValue is the rule the two doc comments state
// and no other test can reach: the ticket is a bearer credential for a run, and
// the ONLY thing this package does with it is notice that the variable is
// there. Nothing decodes it, validates it, compares it, or prints it -- so the
// decision is the same for an unparseable value, an empty one and a plausible
// one, and the value reaches no line of the report, no warning and no error.
func TestTheReaperNeverReadsTheTicketsValue(t *testing.T) {
	const marker = "TICKET-VALUE-THAT-MUST-NEVER-BE-READ"
	var warns []string
	rt := newReaperTest(t, func(o *ReaperOptions) { o.Warn = func(s string) { warns = append(warns, s) } })
	// Two jobs, the same age and the same deadline, whose ticket values could
	// not be more different: a marker string and nothing at all. Both are ended
	// at the ticket's deadline, because the name is the whole of the fact.
	marked := rt.addTicketJob(t, "20260927-100000-abcd", "QUEUED", rt.r.TicketDeadline()+time.Minute,
		map[string]string{identitywire.TicketEnv: marker})
	empty := rt.addTicketJob(t, "20260927-100001-abcd", "QUEUED", rt.r.TicketDeadline()+time.Minute,
		map[string]string{identitywire.TicketEnv: ""})
	// A third inside the deadline with a VM, so the pass writes the other half
	// of the wording too -- the refusal that names the ticket's deadline -- and
	// the value is checked against that as well.
	rt.addTicketJob(t, "20260927-100002-abcd", "QUEUED", time.Minute, map[string]string{identitywire.TicketEnv: marker})
	rt.vm("vm-inside", runLabels(t, "20260927-100002-abcd"), rt.old())
	rep := rt.reap(t)
	if got := rt.bf.Deletes(); len(got) != 2 || !slices.Contains(got, marked) || !slices.Contains(got, empty) {
		t.Fatalf("jobs.delete = %v, want [%s %s]: the decision rests on the variable being there, not on what it holds", got, marked, empty)
	}
	if !refusedBecause(rep, "fake-region-a/vm-inside", "still within the ticket deadline") {
		t.Fatalf("report = %v, want the job inside the deadline's VM refused by the ticket's deadline", rep.Lines())
	}
	for _, line := range append(rep.Lines(), warns...) {
		if strings.Contains(line, marker) {
			t.Fatalf("a line the pass wrote carries the ticket's value: %q", line)
		}
	}
	// Nor does an error: a delete the API refuses is the one failure this pass
	// reports, and it must not carry the value either.
	rt = newReaperTest(t, nil)
	rt.addTicketJob(t, "20260927-100003-abcd", "QUEUED", rt.r.TicketDeadline()+time.Minute,
		map[string]string{identitywire.TicketEnv: marker})
	rt.bf.FailDeleteWith = http.StatusForbidden
	_, err := rt.r.Reap(context.Background())
	if err == nil {
		t.Fatal("Reap with a jobs.delete the API refused: want an error")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("the error carries the ticket's value: %v", err)
	}
}

// TestReapRefusesATicketLifeWithNoDeadlineInIt: the life a caller
// hands over is validated rather than trusted, like the two deadlines beside
// it. A life with no room for the reaper's own interval and the start margin
// leaves a deadline at or before zero, and a deadline that is not in the future
// deletes EVERY job carrying a ticket the moment it is created -- a launcher's
// job cannot have claimed in the same second either -- which is not a deadline
// but a switch that ends runs.
func TestReapRefusesATicketLifeWithNoDeadlineInIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		life time.Duration
	}{
		{"a negative life", -time.Minute},
		{"a life with nothing left after the margins", ReapMargin + TicketStartMargin},
		{"a life shorter than the margins", time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewReaper(context.Background(), ReaperOptions{
				GCPProject: testProject, Region: testRegion,
				Timeouts:   testTimeouts{task: 30 * time.Minute, longest: 30 * time.Minute},
				TicketLife: tc.life,
			})
			if err == nil {
				t.Fatal("NewReaper: want an error")
			}
			if !strings.Contains(err.Error(), "ticket") {
				t.Fatalf("error = %v, want it to name the ticket life it refused", err)
			}
		})
	}
	// And the values that are inside the bound are taken as they are: the
	// deadline is the life less the two margins, and the accessor answers what
	// the decisions use.
	life := ReapMargin + TicketStartMargin + 5*time.Minute
	rt := newReaperTest(t, func(o *ReaperOptions) { o.TicketLife = life })
	if got, want := rt.r.TicketDeadline(), 5*time.Minute; got != want {
		t.Fatalf("TicketDeadline() with a %v ticket life = %v, want %v", life, got, want)
	}
}
