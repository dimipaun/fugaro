package budget_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

const (
	sweepSlug = "acme-app-0123456789abcdef"
	sweepFP   = "aurora-fp"
)

var sweepNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

type sweepEnv struct {
	t     *testing.T
	db    *gcpfake.RTDB
	run   *gcpfake.Run
	idt   *gcpfake.IdentityToolkit
	sw    *budget.Sweeper
	jobID string
}

func newSweepEnv(t *testing.T) *sweepEnv {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	fr := gcpfake.NewRun(t)
	fr.Project, fr.Region = "proj-1234", "us-east5"
	iam := gcpfake.NewIAMCredentials(t)
	idt := gcpfake.NewIdentityToolkit(t, iam, "key", sweepFP)
	be, err := gcp.New(context.Background(), gcp.Options{GCPProject: "proj-1234", Region: "us-east5",
		Endpoints: gcp.Endpoints{Run: fr.URL + "/", NoAuth: true}})
	if err != nil {
		t.Fatal(err)
	}
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	job := gcp.JobName(sweepSlug, "web")
	fr.AddJob(job, "1", "2Gi")
	db.Set("config/mode", "enforce")
	db.Set("config/caps/global/dailyMicros", 50_000_000)
	db.Set("fugaro/project", "aurora")
	return &sweepEnv{t: t, db: db, run: fr, idt: idt, jobID: job, sw: &budget.Sweeper{
		DB: client, Execs: be, Auth: &budget.AuthAdmin{Endpoint: idt.URL, Project: sweepFP, HTTP: http.DefaultClient},
		JobOf: gcp.JobName, Now: func() time.Time { return sweepNow },
	}}
}

// entry registers a run in the registry, heartbeating age ago.
func (e *sweepEnv) entry(run string, age time.Duration) {
	e.t.Helper()
	e.db.Set("agents/"+sweepSlug+"/"+run, map[string]any{
		"repo": "acme/app", "workflow": "web", "requestedBy": "dimi@example.invalid", "stage": "implement",
		"startedAt": sweepNow.Add(-age - time.Hour).UnixMilli(), "updatedAt": sweepNow.Add(-age).UnixMilli(),
	})
}

// ledger gives the run a lifetime ledger that still holds money.
func (e *sweepEnv) ledger(run string) {
	e.db.Set("runs/"+sweepSlug+"/"+run, map[string]any{"reserved": 5_000_000, "released": 1_000_000, "spent": 2_000_000})
}

func (e *sweepEnv) exec(run string, st backend.State) string {
	e.t.Helper()
	name := e.run.StartWithEnv(e.jobID, map[string]string{"FUGARO_RUN": sweepSlug + "/" + run})
	e.run.SetState(name, st)
	return name
}

func (e *sweepEnv) sweep() budget.SweepReport {
	e.t.Helper()
	rep, err := e.sw.Sweep(context.Background())
	if err != nil {
		e.t.Fatalf("Sweep: %v", err)
	}
	return rep
}

func (e *sweepEnv) has(path string) bool { return e.db.Value(path) != nil }

const (
	runA = "20261002-100000-aaaa"
	runB = "20261002-100100-bbbb"
	runC = "20261002-100200-cccc"
)

func outcomePath(run string) string {
	return fmt.Sprintf("outcomes/%d/%s/%s", budget.Day(sweepNow), sweepSlug, run)
}

func TestSweepRemovesEndedExecutions(t *testing.T) {
	e := newSweepEnv(t)
	e.entry(runA, 30*time.Minute) // its execution failed
	e.entry(runB, 30*time.Minute) // its execution is gone altogether
	e.entry(runC, 30*time.Minute) // a clean end whose registry delete failed
	e.exec(runA, backend.StateFailed)
	e.exec(runC, backend.StateSucceeded)
	rep := e.sweep()
	for _, r := range []string{runA, runB, runC} {
		if e.has("agents/" + sweepSlug + "/" + r) {
			t.Errorf("entry of %s is still registered", r)
		}
	}
	if len(rep.Removed) != 3 {
		t.Fatalf("Removed = %v", rep.Removed)
	}
}

func TestSweepKeepsLiveRuns(t *testing.T) {
	e := newSweepEnv(t)
	e.entry(runA, 30*time.Minute) // running; the heartbeat is old but the execution is alive
	e.exec(runA, backend.StateRunning)
	e.entry(runB, time.Minute) // its execution ended a moment ago: the entry may still be about to be deleted
	e.exec(runB, backend.StateFailed)
	e.entry(runC, 30*time.Minute) // an active execution of its job that does not say which run it is
	e.run.Start(e.jobID)
	rep := e.sweep()
	for _, r := range []string{runA, runB, runC} {
		if !e.has("agents/" + sweepSlug + "/" + r) {
			t.Errorf("entry of %s was removed", r)
		}
	}
	if len(rep.Removed) != 0 || rep.Kept != 3 {
		t.Fatalf("report = %+v", rep)
	}
	// Only an active execution of the right run counts: another run's does not.
	e2 := newSweepEnv(t)
	e2.entry(runA, 30*time.Minute)
	e2.exec(runB, backend.StateRunning)
	e2.sweep()
	if e2.has("agents/" + sweepSlug + "/" + runA) {
		t.Fatal("a run kept its entry because of another run's execution")
	}
}

// A run writes its own heartbeat time: one in the future must not keep a dead
// run's entry for ever.
func TestSweepDoesNotTrustRunWrittenTimes(t *testing.T) {
	e := newSweepEnv(t)
	e.entry(runA, -48*time.Hour)                                                                   // updatedAt two days ahead
	e.db.Set("agents/"+sweepSlug+"/"+runB, map[string]any{"repo": "acme/app", "requestedBy": "x"}) // no times at all
	e.exec(runA, backend.StateFailed)
	e.sweep()
	for _, r := range []string{runA, runB} {
		if e.has("agents/" + sweepSlug + "/" + r) {
			t.Errorf("entry of %s survived", r)
		}
	}
	// The outcome of an entry with a future time lands on the sweep's day, not a day the run chose.
	if !e.has(outcomePath(runA)) {
		t.Errorf("no outcome on today's node for %s", runA)
	}
}

// A run's own heartbeat may not date the outcome the sweeper writes. The entry
// is run-written (the rules' AMOUNT && RBOK allows any non-negative number), so
// a hostile run can set updatedAt before its launch day and land its outcome on
// a day a liveness scan never reads — the run then stays admitted until its fx,
// which is the W2 hole reached through the sweeper. The launch day is the run
// id's, which no run token writes.
func TestSweepDatesTheOutcomeNoEarlierThanTheLaunchDay(t *testing.T) {
	e := newSweepEnv(t)
	e.entry(runA, 3*24*time.Hour) // heartbeat three days before the run id's day
	e.sweep()
	if !e.has(outcomePath(runA)) {
		t.Fatalf("no outcome on the run's launch day (%d) for %s: the sweeper dated it from the run's own heartbeat",
			budget.Day(sweepNow), runA)
	}
	hbDay := budget.Day(sweepNow.Add(-3 * 24 * time.Hour))
	if e.has(fmt.Sprintf("outcomes/%d/%s/%s", hbDay, sweepSlug, runA)) {
		t.Errorf("the outcome was dated %d, from the run's own heartbeat, before the run launched", hbDay)
	}
}

// The launch day is a bound on how *early* the outcome may be dated, never on
// how late: a run id dated ahead of the sweeper's clock is this clock being
// behind, and the day it names is one no liveness scan reads (the rules' DAYOK
// allows today or yesterday), so an outcome dated there would keep the run
// admitted until its fx — the same hole from the other side.
//
// The heartbeat here is days old on purpose: with a fresh one the day is today
// with or without the clamp, and the test would pass against a sweeper that
// had no launch-day rule at all (review finding). This is the branch where the
// launch day is not reachable, so what the outcome must not do is fall back to
// the run-written heartbeat's own, older day.
func TestSweepDoesNotDateTheOutcomeAheadOfToday(t *testing.T) {
	e := newSweepEnv(t)
	future := sweepNow.AddDate(0, 0, 3).Format("20060102-150405") + "-aaaa"
	e.entry(future, 72*time.Hour)
	e.sweep()

	today := budget.Day(sweepNow)
	if !e.has(fmt.Sprintf("outcomes/%d/%s/%s", today, sweepSlug, future)) {
		t.Fatalf("no outcome on today's node (%d) for %s: the sweeper dated it from the run id",
			today, future)
	}
	hbDay := budget.Day(sweepNow.Add(-72 * time.Hour))
	if e.has(fmt.Sprintf("outcomes/%d/%s/%s", hbDay, sweepSlug, future)) {
		t.Errorf("the outcome was dated %d, the run's own heartbeat's day: no liveness scan reads it", hbDay)
	}
}

// The heartbeat dates the outcome only when it is plausible for a run that
// has just died; past the window the day falls back to today. This is the
// bound that holds when the run id is not a launch id at all, where the
// launch-day clamp above has nothing to say — otherwise a stale entry could
// date its outcome onto a day no liveness scan reads too.
func TestSweepDoesNotDateTheOutcomeFromAStaleHeartbeat(t *testing.T) {
	e := newSweepEnv(t)
	old := sweepNow.AddDate(0, 0, -30).Format("20060102-150405") + "-aaaa"
	e.entry(old, 10*24*time.Hour)
	e.sweep()

	stale := budget.Day(sweepNow.Add(-10 * 24 * time.Hour))
	if e.has(fmt.Sprintf("outcomes/%d/%s/%s", stale, sweepSlug, old)) {
		t.Errorf("the outcome was dated %d, from a heartbeat ten days old: no liveness scan reads it", stale)
	}
	if !e.has(outcomePath(old)) {
		t.Errorf("no outcome on today (%d) for %s", budget.Day(sweepNow), old)
	}
}

func TestSweepMarksCrashedKeepsCounted(t *testing.T) {
	e := newSweepEnv(t)
	day := budget.Day(sweepNow)
	e.entry(runA, 30*time.Minute)
	e.ledger(runA)
	e.db.Set(fmt.Sprintf("spend/%d/global", day), map[string]any{"counted": 4_000_000, "spent": 2_000_000})
	e.db.Set(fmt.Sprintf("spend/%d/repos/%s", day, sweepSlug), map[string]any{"counted": 4_000_000, "spent": 2_000_000})
	e.db.Set(fmt.Sprintf("spend/%d/runs/%s/%s", day, sweepSlug, runA), map[string]any{"reserved": 5_000_000, "released": 1_000_000})
	before := map[string]any{}
	for _, p := range []string{fmt.Sprintf("spend/%d", day), "config"} {
		before[p] = e.db.Value(p)
	}
	e.sweep()
	led := e.db.Value("runs/" + sweepSlug + "/" + runA).(map[string]any)
	if led["crashed"] != true {
		t.Fatalf("ledger = %v, want crashed", led)
	}
	for k, want := range map[string]string{"reserved": "5000000", "released": "1000000", "spent": "2000000"} {
		if fmt.Sprint(led[k]) != want {
			t.Errorf("ledger %s = %v, want %s: the sweeper must not release anything", k, led[k], want)
		}
	}
	for p, v := range before {
		if !reflect.DeepEqual(v, e.db.Value(p)) {
			t.Errorf("%s changed: counters and config are not the sweeper's to touch", p)
		}
	}
}

// A run can write its own outcome and exp (rules allow the run's own ledger
// fields to move); neither may stop the sweeper from marking it.
func TestSweepDoesNotTrustRunWrittenOutcomeOrExp(t *testing.T) {
	e := newSweepEnv(t)
	e.entry(runA, 30*time.Minute)
	e.ledger(runA)
	e.db.Set("runs/"+sweepSlug+"/"+runA+"/exp", sweepNow.Add(72*time.Hour).UnixMilli())
	e.db.Set(outcomePath(runA), map[string]any{"status": "ok", "requestedBy": "dimi@example.invalid"})
	e.sweep()
	if e.has("agents/" + sweepSlug + "/" + runA) {
		t.Fatal("a claimed outcome or a far expiry kept a dead run out of the sweep")
	}
	if e.db.Value("runs/"+sweepSlug+"/"+runA+"/crashed") != nil {
		t.Fatal("a run that already has an outcome was labelled crashed")
	}
	if got := e.db.Value(outcomePath(runA) + "/status"); got != "ok" {
		t.Fatalf("the sweeper overwrote an existing outcome: %v", got)
	}
}

func TestSweepWritesOutcomeOnce(t *testing.T) {
	e := newSweepEnv(t)
	e.entry(runA, 30*time.Minute)
	e.sweep()
	oc, _ := e.db.Value(outcomePath(runA)).(map[string]any)
	if oc["status"] != "infra_error" || oc["requestedBy"] != "dimi@example.invalid" {
		t.Fatalf("outcome = %v", oc)
	}
	// A second sweep, and a second entry that comes back for the same run
	// (a stale writer), must not write it again.
	writes := e.writesTo(outcomePath(runA))
	e.entry(runA, 30*time.Minute)
	e.sweep()
	if got := e.writesTo(outcomePath(runA)); got != writes {
		t.Fatalf("outcome written %d times, want %d", got, writes)
	}
	if writes != 1 {
		t.Fatalf("outcome written %d times, want once", writes)
	}
}

// writesTo counts the database writes whose body mentions the path.
func (e *sweepEnv) writesTo(path string) int {
	n := 0
	for _, r := range e.db.Requests() {
		if r.Method != http.MethodPatch && r.Method != http.MethodPut {
			continue
		}
		for _, p := range writtenPaths(r) {
			if p == path {
				n++
			}
		}
	}
	return n
}

// writtenPaths are the absolute paths a PUT or PATCH touches.
func writtenPaths(r gcpfake.Request) []string {
	base := strings.Trim(strings.TrimSuffix(r.Path, ".json"), "/")
	if r.Method == http.MethodPut {
		return []string{base}
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(r.Body, &m)
	var out []string
	for k := range m {
		out = append(out, strings.Trim(base+"/"+k, "/"))
	}
	sort.Strings(out)
	return out
}

func TestSweepNeverWritesConfig(t *testing.T) {
	e := newSweepEnv(t)
	e.db.Set("config/kill/global", map[string]any{"on": true, "by": "x"})
	e.db.Set("config/caps/repos/"+sweepSlug, map[string]any{"dailyMicros": 1_000_000})
	for i, r := range []string{runA, runB, runC} {
		e.entry(r, time.Duration(30+i)*time.Minute)
		e.ledger(r)
	}
	before := e.db.Value("config")
	markBefore := e.db.Value("fugaro")
	e.sweep()
	var written []string
	for _, r := range e.db.Requests() {
		if r.Method == http.MethodPatch || r.Method == http.MethodPut {
			written = append(written, writtenPaths(r)...)
		}
	}
	if len(written) == 0 {
		t.Fatal("the sweep wrote nothing: the test would be vacuous")
	}
	for _, p := range written {
		top, _, _ := strings.Cut(p, "/")
		if top != "agents" && top != "runs" && top != "outcomes" && top != "pairing" {
			t.Errorf("the sweeper wrote %q; its delete set is a registry entry, a ledger, an outcome and a pairing node of a user it deletes — nothing under config, and nothing of another family", p)
		}
	}
	if !reflect.DeepEqual(before, e.db.Value("config")) || !reflect.DeepEqual(markBefore, e.db.Value("fugaro")) {
		t.Fatal("config or the project mark changed")
	}
}

func TestSweepDeletesOldAuthUsers(t *testing.T) {
	e := newSweepEnv(t)
	old := sweepNow.Add(-72 * time.Hour)
	e.idt.AddUser(budgetUID(runA), old)                         // old run: deleted
	e.idt.AddUser(budgetUID(runB), sweepNow.Add(-24*time.Hour)) // yesterday's: kept
	e.idt.AddUser("someone-else", old)                          // not a run's identity: never touched
	e.idt.AddUser(budgetUID(runC), old)                         // a live run's identity: kept
	e.entry(runC, time.Minute)
	e.exec(runC, backend.StateRunning)
	rep := e.sweep()
	got := e.idt.Users()
	sort.Strings(got)
	want := []string{budgetUID(runB), budgetUID(runC), "someone-else"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("users left = %v, want %v", got, want)
	}
	if rep.UsersDeleted != 1 {
		t.Fatalf("UsersDeleted = %d", rep.UsersDeleted)
	}
}

func budgetUID(run string) string { return token.UID(sweepSlug, run) }

func TestSweepUIDMatchesToken(t *testing.T) {
	if got, want := budget.RunUID(sweepSlug, runA), token.UID(sweepSlug, runA); got != want {
		t.Fatalf("budget.RunUID = %q, token.UID = %q", got, want)
	}
}

func TestSweepIdempotent(t *testing.T) {
	e := newSweepEnv(t)
	e.entry(runA, 30*time.Minute)
	e.ledger(runA)
	e.entry(runB, time.Minute)
	e.exec(runB, backend.StateRunning)
	e.idt.AddUser(budgetUID(runC), sweepNow.Add(-72*time.Hour))
	e.sweep()
	snap := e.db.Value("")
	users := e.idt.Users()
	writes := countWrites(e.db.Requests())
	rep := e.sweep()
	if !reflect.DeepEqual(snap, e.db.Value("")) {
		t.Fatal("the second sweep changed the database")
	}
	if got := countWrites(e.db.Requests()); got != writes {
		t.Fatalf("second sweep wrote %d times", got-writes)
	}
	if len(rep.Removed) != 0 || rep.UsersDeleted != 0 || !reflect.DeepEqual(users, e.idt.Users()) {
		t.Fatalf("second report = %+v", rep)
	}
}

func countWrites(rs []gcpfake.Request) int {
	n := 0
	for _, r := range rs {
		if r.Method == http.MethodPatch || r.Method == http.MethodPut {
			n++
		}
	}
	return n
}

// When the platform cannot be asked, nothing is removed: a missing execution
// is only evidence when the listing worked.
func TestSweepListFailureWritesNothing(t *testing.T) {
	e := newSweepEnv(t)
	e.entry(runA, 30*time.Minute)
	e.idt.AddUser(budgetUID(runB), sweepNow.Add(-72*time.Hour))
	e.run.Refuse(http.StatusServiceUnavailable, "UNAVAILABLE", "", "down")
	if _, err := e.sw.Sweep(context.Background()); err == nil {
		t.Fatal("Sweep succeeded without a listing")
	}
	if !e.has("agents/"+sweepSlug+"/"+runA) || countWrites(e.db.Requests()) != 0 || len(e.idt.Users()) != 1 {
		t.Fatal("a failed listing changed state")
	}
}

// A failure of the user cleanup does not stop the registry sweep, and is
// still reported.
func TestSweepReportsAuthFailureAfterSweeping(t *testing.T) {
	e := newSweepEnv(t)
	e.entry(runA, 30*time.Minute)
	e.idt.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "", "no")
	if _, err := e.sw.Sweep(context.Background()); err == nil {
		t.Fatal("an auth failure was swallowed")
	}
	if e.has("agents/" + sweepSlug + "/" + runA) {
		t.Fatal("the registry sweep did not run")
	}
}

func TestAuthAdminPagesAndBatches(t *testing.T) {
	e := newSweepEnv(t)
	for i := range 2100 {
		e.idt.AddUser(fmt.Sprintf("r~s~%05d", i), sweepNow.Add(-72*time.Hour))
	}
	a := e.sw.Auth.(*budget.AuthAdmin)
	users, err := a.ListUsers(context.Background())
	if err != nil || len(users) != 2100 {
		t.Fatalf("ListUsers = %d, %v", len(users), err)
	}
	uids := make([]string, len(users))
	for i, u := range users {
		uids[i] = u.UID
	}
	if _, err := a.DeleteUsers(context.Background(), uids); err != nil || len(e.idt.Users()) != 0 {
		t.Fatalf("DeleteUsers: %v, %d left", err, len(e.idt.Users()))
	}
}

// The outcome is created conditionally (a PUT that only matches an absent
// node), so a run that writes its own outcome in the meantime wins.
func TestSweepCreatesOutcomeConditionally(t *testing.T) {
	e := newSweepEnv(t)
	e.entry(runA, 30*time.Minute)
	e.sweep()
	found := false
	for _, r := range e.db.Requests() {
		if r.Method == http.MethodPut && strings.Contains(r.Path, strings.TrimPrefix(outcomePath(runA), "")) {
			found = true
		}
		if r.Method == http.MethodPatch && strings.Contains(string(r.Body), "outcomes/") {
			t.Fatalf("outcome written by an unconditional PATCH: %s", r.Body)
		}
	}
	if !found {
		t.Fatal("no conditional PUT of the outcome")
	}
	if e.db.Value(outcomePath(runA)+"/status") != "infra_error" {
		t.Fatal("outcome missing")
	}
}

// An entry of the wrong types cannot be read, but a run cannot keep itself
// registered that way for ever: past a day it is swept like any dead one.
func TestSweepRemovesWrongTypedEntriesWhenOld(t *testing.T) {
	e := newSweepEnv(t)
	oldRun, youngRun, liveRun := "20260930-100000-aaaa", "20261002-110000-bbbb", "20260930-100100-cccc"
	for _, r := range []string{oldRun, youngRun, liveRun} {
		e.db.Set("agents/"+sweepSlug+"/"+r, map[string]any{"repo": 7, "workflow": "web", "startedAt": "soon", "updatedAt": []int{1}})
	}
	e.exec(liveRun, backend.StateRunning)
	e.sweep()
	if e.has("agents/" + sweepSlug + "/" + oldRun) {
		t.Error("an old wrong-typed entry was kept for ever")
	}
	if !e.has("agents/"+sweepSlug+"/"+youngRun) || !e.has("agents/"+sweepSlug+"/"+liveRun) {
		t.Error("a young or live wrong-typed entry was removed")
	}
}

// If the registry can't be read nothing is written and no user is deleted.
func TestSweepRegistryReadFailureWritesNothing(t *testing.T) {
	e := newSweepEnv(t)
	e.entry(runA, 30*time.Minute)
	e.idt.AddUser(budgetUID(runB), sweepNow.Add(-72*time.Hour))
	e.db.Refuse(http.StatusServiceUnavailable, "UNAVAILABLE", "", "down")
	if _, err := e.sw.Sweep(context.Background()); err == nil {
		t.Fatal("Sweep succeeded without the registry")
	}
	if countWrites(e.db.Requests()) != 0 || len(e.idt.Users()) != 1 {
		t.Fatal("a failed registry read changed state")
	}
}

// A 200 from batchDelete can still list users it did not delete: they are
// reported and not counted.
func TestSweepReportsPerUserDeleteErrors(t *testing.T) {
	e := newSweepEnv(t)
	e.idt.AddUser(budgetUID(runA), sweepNow.Add(-72*time.Hour))
	e.idt.AddUser(budgetUID(runB), sweepNow.Add(-72*time.Hour))
	e.idt.FailDelete(budgetUID(runB), "USER_NOT_FOUND_LOCKED")
	var warns []string
	e.sw.Warn = func(s string) { warns = append(warns, s) }
	rep := e.sweep()
	if rep.UsersDeleted != 1 {
		t.Fatalf("UsersDeleted = %d, want 1", rep.UsersDeleted)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], budgetUID(runB)) || !strings.Contains(warns[0], "USER_NOT_FOUND_LOCKED") {
		t.Fatalf("warnings = %q", warns)
	}
}

// TestSweepLinesSanitiseUIDs: both halves of the failed-deletion warning come
// from outside this program — the uid is the Auth directory's own, the message
// the API's — and a control character in either would otherwise rewrite the
// terminal that reads the line or open a line of its own. They go through the
// package's own sanitiser, the one a registry entry's text already goes through,
// so the line says what the sweep could not delete and nothing the value wrote.
func TestSweepLinesSanitiseUIDs(t *testing.T) {
	e := newSweepEnv(t)
	uid := "m~\x1b[2Jred\nsecond"
	e.idt.AddUser(uid, sweepNow.Add(-72*time.Hour))
	e.idt.FailDelete(uid, "USER_NOT_FOUND_LOCKED\x1b[0m")
	var warns []string
	e.sw.Warn = func(s string) { warns = append(warns, s) }
	e.sweep()
	if len(warns) != 1 {
		t.Fatalf("warnings = %q, want the one failed deletion reported", warns)
	}
	if got := warns[0]; strings.ContainsAny(got, "\x1b\n\r") {
		t.Errorf("the warning carries a control character: %q", got)
	}
	if !strings.Contains(warns[0], "m~redsecond") || !strings.Contains(warns[0], "USER_NOT_FOUND_LOCKED") {
		t.Errorf("warnings = %q, want the uid and the API message with their controls dropped", warns)
	}
}
