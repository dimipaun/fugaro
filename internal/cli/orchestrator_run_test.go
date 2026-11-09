package cli

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The daemon: its start-up, its loop and the boundary it holds (O8).

// daemonFixture is one installation with a daemon over it: the budget database
// the state node and the lease live in, the runs bucket, and the launch path
// every test starts from.
type daemonFixture struct {
	*launcherFixture
	// ctx is the daemon's own context, cancelled by the default wait: the loop
	// is what these tests are about and it runs until something stops it.
	ctx    context.Context
	cancel context.CancelFunc
	// now is the database's clock as the money-path tests read it once, before
	// they move it past the claim window.
	now time.Time
}

func newDaemonFixture(t *testing.T) *daemonFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &daemonFixture{launcherFixture: newLauncherFixture(t), ctx: ctx, cancel: cancel}
}

// daemon is the daemon the command builds, with the one flag a test names: the
// path the unit declares, empty for a foreground run.
//
// The output goes to a buffer rather than to a terminal: what the daemon says
// is part of what these tests pin.
func (f *daemonFixture) daemon(t *testing.T, binary string) (*orchestratorDaemon, *strings.Builder) {
	t.Helper()
	ctx := context.Background()
	lc, db, err := openBudget(ctx, cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.bucket.Close() })
	id, err := orchestratorID(lc)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := orchestrator.NewLease(db, id, daemonHolder, db.ServerNow)
	if err != nil {
		t.Fatal(err)
	}
	node, err := orchestrator.NewNode(orchestrator.NodeOptions{DB: db, AgentID: id, Role: orchestrator.RoleOrchestrator, Lease: lease})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	d := &orchestratorDaemon{
		lc: lc, id: id, db: db, env: env, node: node, lease: lease, holder: daemonHolder,
		interval: time.Minute, beat: orchestrator.LeaseBeat,
		binary: binary, running: "v1.2.3",
		out: &out, warn: &out, launched: &launchInFlight{},
	}
	d.cache = &orchestrator.BriefCacheWriter{Getenv: os.Getenv, GOOS: runtime.GOOS, ID: id, Log: func(err error) {
		out.WriteString("cache: " + err.Error() + "\n")
	}}
	// The wait that ends the loop: production waits on the node's own stream and
	// the interval, and a test that let that wait run would never return.
	d.wake = func(context.Context) error {
		f.cancel()
		return context.Canceled
	}
	return d, &out
}

// daemonHolder is the name the fixture's daemon holds the lease under, the way
// task.NewRunID names a real one.
const daemonHolder = "20261109-101500-abcd"

// statePath is where the fixture's daemon reads its node.
func (f *daemonFixture) statePath(t *testing.T) string {
	t.Helper()
	path, err := orchestrator.PathState("aurora")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// setState writes a state document at the node the way another release would
// have left it: the daemon under test never wrote this one.
func (f *daemonFixture) setState(t *testing.T, raw string) {
	t.Helper()
	f.db.Set(f.statePath(t), json.RawMessage(raw))
}

// TestMigrateRunsAtDaemonStart: a state node left at the previous
// schema_version is migrated by the daemon's own start-up, and what the node
// carries afterwards is this release's version. Nothing else converges it:
// `fugaro update` no longer touches anything under orchestrator/ (Q2's answer
// as T7's ruling of 2026-11-05 keeps it).
func TestMigrateRunsAtDaemonStart(t *testing.T) {
	f := newDaemonFixture(t)
	d, out := f.daemon(t, "")
	// A node the previous release wrote: schema_version 0 and the fields it
	// had. It is read through the store, so the raise is O0's Migrate and not a
	// shape this file knows.
	f.setState(t, prevReleaseState)

	ctx := f.ctx
	if err := d.start(ctx); err != nil {
		t.Fatalf("the daemon's start-up: %v", err)
	}
	v, found, err := d.stateVersion(context.Background())
	if err != nil || !found {
		t.Fatalf("reading the node after start-up: %v (found %v)", err, found)
	}
	if v != orchestrator.SchemaVersion {
		t.Fatalf("the node is at schema_version %d after the daemon's start-up, want %d", v, orchestrator.SchemaVersion)
	}
	if !strings.Contains(out.String(), "schema_version 0") {
		t.Errorf("the daemon did not say it migrated the node: %q", out.String())
	}
	if !strings.Contains(out.String(), "driving it as") {
		t.Errorf("the daemon never said it took the lease and is driving the id: %q", out.String())
	}
}

// prevReleaseState is a state node as the previous release wrote it:
// schema_version 0 and nothing this release added since.
const prevReleaseState = `{"schema_version":0,"rev":3,"agent_id":"aurora","role":"orchestrator",
	"goal":"keep the work moving","work_items":[],"decisions":[],"facts":{},"archived":[],"intents":[]}`

// TestRunReportsUpdateDueAndDoesNothingElse: a state node this release does not
// read is reported as an update being due and nothing else happens — no write
// to the node, no lease taken, no run started (G8). A daemon is never the thing
// that migrates a node a newer release wrote, and it never claims an id it is
// not going to drive.
func TestRunReportsUpdateDueAndDoesNothingElse(t *testing.T) {
	f := newDaemonFixture(t)
	d, out := f.daemon(t, "")
	raw := `{"schema_version":2,"rev":9,"agent_id":"aurora","role":"orchestrator",
		"goal":"from a newer release","work_items":[],"decisions":[],"facts":{},"archived":[],"intents":[]}`
	f.setState(t, raw)
	before := f.db.Value(f.statePath(t))

	ctx := f.ctx
	if err := d.start(ctx); err != nil {
		t.Fatalf("starting over a node a newer release wrote: %v", err)
	}
	if !strings.Contains(out.String(), "an update is due") || !strings.Contains(out.String(), "fugaro update") {
		t.Errorf("the report does not name the update it is about: %q", out.String())
	}
	if after := f.db.Value(f.statePath(t)); !reflect.DeepEqual(before, after) {
		t.Errorf("the daemon wrote to a state node it cannot read:\nafter  %v\nbefore %v", after, before)
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Errorf("a daemon that found an update due started %d execution(s)", len(execs))
	}
	state, err := d.lease.Read(ctx)
	if err != nil {
		t.Fatalf("reading the lease node: %v", err)
	}
	if state.Held || state.Holder != "" {
		t.Errorf("the lease names %q after a start-up that stopped for an update being due, and nothing may claim the id then", state.Holder)
	}
}

// TestRunStopsWhenTheLeaseIsLost: the lease is what makes one machine the
// driver, so a daemon whose id went to another machine stops cleanly — it does
// not take it back and it never writes the node again. Taking an id over is a
// start, and a start is Acquire.
func TestRunStopsWhenTheLeaseIsLost(t *testing.T) {
	f := newDaemonFixture(t)
	d, out := f.daemon(t, "")
	ctx := f.ctx
	state, err := d.lease.Acquire(ctx)
	if err != nil || !state.Held {
		t.Fatalf("taking the lease: %+v, %v", state, err)
	}
	tick, err := d.newTick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d.tick = tick

	leasePath, err := orchestrator.PathLease("aurora")
	if err != nil {
		t.Fatal(err)
	}
	waits := 0
	d.wake = func(context.Context) error {
		waits++
		if waits == 1 {
			// Another daemon takes the id over while this one waits, which is
			// what a second machine seeing a stale claim looks like from here.
			f.db.Set(leasePath, map[string]any{"holder": "20261109-111500-beef", "at": time.Now().UTC().Format(time.RFC3339Nano)})
		}
		return nil
	}
	before := f.db.Value(f.statePath(t))
	if err := d.loop(f.ctx); err != nil {
		t.Fatalf("the daemon returned %v where a lost lease is a clean stop", err)
	}
	if !strings.Contains(out.String(), "the lease went to another daemon") {
		t.Errorf("the daemon did not say why it stopped: %q", out.String())
	}
	if waits != 1 {
		t.Errorf("the daemon waited %d times, want the one cycle its lease lasted", waits)
	}
	if after := f.db.Value(f.statePath(t)); !reflect.DeepEqual(before, after) {
		t.Errorf("a daemon that lost its lease wrote the node:\nafter  %v\nbefore %v", after, before)
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Errorf("a daemon stopping over a lost lease started %d execution(s)", len(execs))
	}
	held, err := d.lease.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if held.Holder != "20261109-111500-beef" {
		t.Errorf("the lease names %q, so this daemon took back an id that is not its own", held.Holder)
	}
}

// TestDaemonNeverPassesYes: no confirmation is ever confirmed for anybody. The
// `run` command carries no --yes — a daemon that could pass one would answer
// its own questions — and a cycle over the one thing only a person may do (the
// approval of an item) starts nothing at all, whatever the state says.
func TestDaemonNeverPassesYes(t *testing.T) {
	cmd := newOrchestratorRunCmd()
	if f := cmd.Flags().Lookup("yes"); f != nil {
		t.Fatal("fugaro orchestrator run carries --yes, and a daemon must never confirm anything on a person's behalf")
	}
	f := newDaemonFixture(t)
	d, out := f.daemon(t, "")
	// An item nobody approved: starting it would mean confirming for a person.
	f.setState(t, `{"schema_version":1,"rev":1,"agent_id":"aurora","role":"orchestrator",
		"goal":"keep the work moving","work_items":[{"id":"wi-1","title":"land it","repo":"acme/app",
		"recipe":"standard","rigor_tier":"medium","brief":"Do the thing.","phase":"queued"}],
		"decisions":[],"facts":{},"archived":[],"intents":[]}`)
	ctx := context.Background()
	if state, err := d.lease.Acquire(ctx); err != nil || !state.Held {
		t.Fatalf("taking the lease: %+v, %v", state, err)
	}
	tick, err := d.newTick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d.tick = tick

	rep, err := tick.Step(ctx)
	if err != nil {
		t.Fatalf("a cycle over an unapproved item: %v", err)
	}
	if rep.Carried != 0 {
		t.Errorf("the cycle carried %d action(s) for an item nobody approved", rep.Carried)
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Errorf("an unapproved item started %d execution(s)", len(execs))
	}
	if strings.Contains(out.String(), "CONFIRM") {
		t.Errorf("the daemon asked a person to confirm something: %q", out.String())
	}
}

// TestDaemonDoesNotDependOnGatedCommands: the daemon's own files reach no
// package that converges or publishes anything, so no foreground run can apply
// Terraform, install a policy or re-render a unit behind the person's back
// (G8). It asks the toolchain what those files reach and not what their source
// looks like: an import inside another package's file, or behind a build tag,
// would pass a string check over this package.
func TestDaemonDoesNotDependOnGatedCommands(t *testing.T) {
	deps := daemonDependencies(t)
	if len(deps) == 0 {
		t.Fatal("no dependency was read at all, so this test proves nothing")
	}
	const gated = "github.com/dimipaun/fugaro/internal/infra"
	for _, dep := range deps {
		if dep == gated || strings.HasPrefix(dep, gated+"/") {
			t.Errorf("the daemon's own files reach %s, which is where every converge, publish and install lives: a foreground daemon must not be able to run one", dep)
		}
	}
}

// daemonDependencies is everything the daemon's own files reach: their imports
// read from the source with go/parser — a dependency of a file and not of the
// command family it sits in, because the family does reach gated packages and
// this is the check that they did not leak into the daemon — and then the
// closure of those imports, from the toolchain.
func daemonDependencies(t *testing.T) []string {
	t.Helper()
	files := []string{"orchestrator_run.go", "orchestrator_launcher.go", "orchestrator_version.go"}
	fset := token.NewFileSet()
	var roots []string
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("reading the imports of %s: %v", name, err)
		}
		for _, im := range f.Imports {
			p, err := strconv.Unquote(im.Path.Value)
			if err != nil {
				t.Fatalf("the import %s in %s is not a path: %v", im.Path.Value, name, err)
			}
			roots = append(roots, p)
		}
	}
	cmd := exec.Command("go", append([]string{"list", "-deps", "-f", "{{.ImportPath}}"}, roots...)...)
	cmd.Dir = "../.."
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps over the daemon's imports: %v\n%s", err, raw)
	}
	return strings.Fields(string(raw))
}

// TestOrchestratorRunIsRegistered: the command exists with the flags the unit
// and a person pass it.
func TestOrchestratorRunIsRegistered(t *testing.T) {
	out, _, err := execute(t, "orchestrator", "run", "--help")
	if err != nil {
		t.Fatalf("fugaro orchestrator run --help: %v", err)
	}
	for _, want := range []string{"--binary", "--interval", "--once"} {
		if !strings.Contains(out, want) {
			t.Errorf("fugaro orchestrator run --help never names %s", want)
		}
	}
}

// newerReleaseState is a state node a newer release wrote: this binary reads
// schema_version 1 and stops over it (G8).
const newerReleaseState = `{"schema_version":2,"rev":9,"agent_id":"aurora","role":"orchestrator",
	"goal":"from a newer release","work_items":[],"decisions":[],"facts":{},"archived":[],"intents":[]}`

// TestWaitHonoursTheInterval: the wait is the interval's, not the stream's.
// Every connection opens with a full put of the node as it stands, which is a
// snapshot and not a change — the cycle that just ran read the same node — so a
// wait that returned on it would end in microseconds and spin the loop at a
// speed --interval never sets.
func TestWaitHonoursTheInterval(t *testing.T) {
	f := newDaemonFixture(t)
	d, _ := f.daemon(t, "")
	d.wake = nil
	d.interval = 200 * time.Millisecond

	start := time.Now()
	if err := d.wait(context.Background()); err != nil {
		t.Fatalf("a wait with nothing moving: %v", err)
	}
	if elapsed := time.Since(start); elapsed < d.interval {
		t.Errorf("the wait ended after %s with nothing changed and an interval of %s: the stream's own connect put is being read as a change", elapsed, d.interval)
	}
}

// TestWaitEndsItsStreamAndWakesOnAChange: the two halves of the same wait. A
// change to the node ends it long before the interval, so the stream is still
// what wakes the daemon; and the stream is cancelled on the way out, because
// one left behind holds a connection, a goroutine and its 64KiB scanner buffer
// for ever — one per cycle is a leak with no bound at all.
func TestWaitEndsItsStreamAndWakesOnAChange(t *testing.T) {
	f := newDaemonFixture(t)
	d, _ := f.daemon(t, "")
	d.wake = nil
	d.interval = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	path := f.statePath(t)
	baseline := runtime.NumGoroutine()
	// A writer that keeps the node moving the way the database tells its
	// streams: a write through the client. A test's own seed (the fake's Set)
	// changes the node without notifying a stream, which is not what a wake-up
	// is about.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				_ = d.db.Patch(context.Background(), path, map[string]any{"rev": i})
			}
		}
	}()

	start := time.Now()
	if err := d.wait(ctx); err != nil {
		t.Fatalf("a wait over a moving node: %v", err)
	}
	if elapsed := time.Since(start); elapsed > d.interval/2 {
		t.Errorf("the wait ended after %s, and a change to the node must wake it long before its interval (%s)", elapsed, d.interval)
	}

	// Six more waits, so that a stream left behind each time would leave six
	// goroutines and six open connections here.
	for range 6 {
		if err := d.wait(ctx); err != nil {
			t.Fatalf("a repeated wait: %v", err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for got := runtime.NumGoroutine(); got > baseline+2 && time.Now().Before(deadline); got = runtime.NumGoroutine() {
		time.Sleep(20 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > baseline+2 {
		t.Errorf("goroutines went from %d to %d over seven waits, so each wait leaves its stream connected", baseline, got)
	}
}

// TestWaitBeatsTheLeaseWhileTheIntervalRuns: the wait refreshes the claim while
// it runs, on the lease's own cadence. --interval is a person's value and the
// claim expires at orchestrator.LeaseTTL, so a wait that honoured the flag
// alone would leave the id un-beaten for a whole interval — an interval a
// little under the TTL is a claim that lapses under the daemon holding it, and
// a second daemon takes that id while this one is still driving it. The
// interval still ends the wait: the cycle comes no sooner than the person asked
// for, however many beats it took.
//
// The fake's Date header has one-second resolution, so the server's clock is
// the test's own here: the only thing that writes the lease node is a beat, so
// a stamp moved forward is a beat the wait itself made.
func TestWaitBeatsTheLeaseWhileTheIntervalRuns(t *testing.T) {
	f := newDaemonFixture(t)
	d, _ := f.running(t, "")
	d.wake = nil
	d.interval = 300 * time.Millisecond
	d.beat = 50 * time.Millisecond
	before, err := d.lease.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	later := before.At.Add(5 * time.Second)
	f.db.SetClock(func() time.Time { return later })

	start := time.Now()
	if err := d.wait(context.Background()); err != nil {
		t.Fatalf("a wait longer than the lease's own beat: %v", err)
	}
	if elapsed := time.Since(start); elapsed < d.interval {
		t.Errorf("the wait came back after %s with nothing moving and an interval of %s", elapsed, d.interval)
	}
	after, err := d.lease.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !after.At.After(before.At) {
		t.Errorf("the lease was stamped %s before a %s wait and %s after it, so the claim went the whole interval without a beat and a daemon waiting longer than its own lease would have it taken",
			before.At, d.interval, after.At)
	}
}

// TestRunRefusesAnIntervalLongerThanTheLease: the flag is bounded by the claim
// it drives, and a value the daemon cannot serve is refused rather than quietly
// shortened — before anything is opened, so a mistyped duration costs nothing.
func TestRunRefusesAnIntervalLongerThanTheLease(t *testing.T) {
	_, _, err := execute(t, "orchestrator", "run", "--interval", (orchestrator.LeaseTTL + time.Second).String())
	if err == nil {
		t.Fatal("an interval longer than the lease the daemon holds was accepted")
	}
	if !strings.Contains(err.Error(), "no longer than the lease") {
		t.Errorf("the refusal is not the interval's own bound: %v", err)
	}
}

// TestRunHoldsWhileAnUpdateIsDue: a daemon the unit runs does not exit over an
// update being due. The unit starts it again after any exit (KeepAlive on
// launchd, Restart=always on systemd, O9), so exiting would be the same old
// binary dying and being started again to print the same line, for ever. It
// holds instead — no lease, no tick, no write — and the thing it waits for is
// the one thing that helps: the new binary arriving at the path the unit names,
// which is what `fugaro update` puts there.
func TestRunHoldsWhileAnUpdateIsDue(t *testing.T) {
	f := newDaemonFixture(t)
	binary := stubBinary(t, t.TempDir(), "fugaro", "echo v9.9.9")
	d, out := f.daemon(t, binary)
	f.setState(t, newerReleaseState)
	waits := 0
	d.wake = func(context.Context) error {
		waits++
		if waits > 1 {
			f.cancel()
		}
		return nil
	}
	before := f.db.Value(f.statePath(t))

	if err := d.start(f.ctx); err != nil {
		t.Fatalf("a daemon holding over an update being due returned %v", err)
	}
	if !strings.Contains(out.String(), "an update is due") || !strings.Contains(out.String(), "holding") {
		t.Errorf("the report does not say the daemon is holding over an update: %q", out.String())
	}
	if !strings.Contains(out.String(), "exiting cleanly") {
		t.Errorf("the binary at the unit's path moved on and the daemon did not exit for the unit to start it: %q", out.String())
	}
	if after := f.db.Value(f.statePath(t)); !reflect.DeepEqual(before, after) {
		t.Errorf("a daemon holding over an update wrote the node:\nafter  %v\nbefore %v", after, before)
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Errorf("a daemon holding over an update started %d execution(s)", len(execs))
	}
	state, err := d.lease.Read(context.Background())
	if err != nil {
		t.Fatalf("reading the lease node: %v", err)
	}
	if state.Held || state.Holder != "" {
		t.Errorf("the lease names %q, and a daemon that drives nothing claims nothing", state.Holder)
	}
}

// TestDaemonRearmsTheBreakerWhenTheAnswerLands is the loop's half of D1228:
// the count is this process's and the question is the node's, so a streak a
// person has answered has to start again here. A breaker that is never
// re-armed is silent from its first trip on — the observe reports only the
// cycle that reaches the threshold — so the process would ask one question in
// its whole life and then pause with nothing for anybody to look at.
func TestDaemonRearmsTheBreakerWhenTheAnswerLands(t *testing.T) {
	f := newDaemonFixture(t)
	d, _ := f.running(t, "")
	ctx := context.Background()

	// Three cycles with nothing to do, as the loop runs them: the third
	// reaches the threshold and leaves its question on the node.
	for i := range orchestrator.BreakerThreshold {
		if _, err := d.tick.Step(ctx); err != nil {
			t.Fatalf("cycle %d: %v", i+1, err)
		}
	}
	if !d.tick.Breaker.Tripped() {
		t.Fatal("three empty cycles did not trip the breaker, so this test proves nothing")
	}
	s, _, err := d.node.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !orchestrator.BreakerOpen(s) {
		t.Fatal("the trip left no unanswered question on the node, so there is nothing for a person to answer")
	}

	// The question stands: the streak is the pause's own, and a breaker
	// re-armed here would count a second streak over a node that is stopped.
	d.rearmBreaker(ctx)
	if !d.tick.Breaker.Tripped() {
		t.Error("the breaker was re-armed while its question was still unanswered")
	}

	answerBreakerQuestion(t, f, s, d)
	d.rearmBreaker(ctx)
	if d.tick.Breaker.Tripped() {
		t.Error("the breaker is still standing after the person answered, so the next streak reports nothing and the process asks once in its life")
	}
}

// answerBreakerQuestion closes the breaker's question the way a person's own
// patch does: a resolve from the person class, over the id the trip left.
func answerBreakerQuestion(t *testing.T, f *daemonFixture, s orchestrator.State, d *orchestratorDaemon) {
	t.Helper()
	id := ""
	for _, dec := range s.Decisions {
		if !dec.Resolved() {
			id = dec.ID
		}
	}
	if id == "" {
		t.Fatal("the node carries no unanswered decision to answer")
	}
	next, err := orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: []orchestrator.Op{
		{Op: orchestrator.OpDecisionResolve, Resolve: &orchestrator.ResolveOp{ID: id, Answer: "carry on"}},
	}}, orchestrator.Actor{Class: orchestrator.Person, Name: "owner", Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("answering the breaker's question: %v", err)
	}
	raw, err := orchestrator.EncodeState(next)
	if err != nil {
		t.Fatal(err)
	}
	f.setState(t, string(raw))
	if after, _, err := d.node.Load(context.Background()); err != nil {
		t.Fatal(err)
	} else if orchestrator.BreakerOpen(after) {
		t.Fatal("the answer did not close the question, so the re-arm below would prove nothing")
	}
}

// TestRunOnceTakesOneCycleAndReleasesTheLease: the daemon's contract is not to
// return, and --once is the one cycle a person can hold it to — the lease is
// taken for that cycle and released with it, so an inspection never keeps the
// id from the daemon that drives it for the TTL.
func TestRunOnceTakesOneCycleAndReleasesTheLease(t *testing.T) {
	newDaemonFixture(t)
	out, _, err := execute(t, "orchestrator", "run", "--once")
	if err != nil {
		t.Fatalf("fugaro orchestrator run --once: %v", err)
	}
	if !strings.Contains(out, "driving it as") {
		t.Errorf("the one cycle did not take the lease: %q", out)
	}
	if !strings.Contains(out, "one cycle as asked (--once)") {
		t.Errorf("the run did not say it took one cycle: %q", out)
	}
	_, db, err := openBudget(context.Background(), cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := orchestrator.NewLease(db, "aurora", daemonHolder, db.ServerNow)
	if err != nil {
		t.Fatal(err)
	}
	held, err := lease.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if held.Held || held.Holder != "" {
		t.Errorf("the lease still names %q after a --once run, and one cycle must not hold the id", held.Holder)
	}
}

// TestDaemonSeesTheGuardrailsMoveUnderIt: the kill switch and the project's
// caps are what decide whether a cycle plans anything, and the switch is a
// person's. A daemon that read them once at start-up would keep committing
// intents and starting runs for the rest of its life: a person stopping the
// work would stop the runs already going and none of the ones to come. The
// cycle below starts with the switch off — the fixture's own state — and a
// switch turned on before it runs is a cycle that plans nothing at all.
func TestDaemonSeesTheGuardrailsMoveUnderIt(t *testing.T) {
	f := newDaemonFixture(t)
	d, _ := f.running(t, "")
	d.once = true
	now := f.serverNow(t)
	item := approvedItem("wi-1", now)
	f.putState(t, orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion, AgentID: "aurora", Role: orchestrator.RoleOrchestrator,
		Goal: "keep the work moving", WorkItems: []orchestrator.WorkItem{item},
	})
	d.tick.Launcher = f.launcher(t)
	if d.tick.Caps.KillSwitch {
		t.Fatal("the fixture's daemon starts with the kill switch on, so this test proves nothing")
	}
	f.db.Set(budget.PathKillGlobal, map[string]any{"on": true, "by": "owner", "reason": "stop the work"})

	if err := d.loop(context.Background()); err != nil {
		t.Fatalf("a cycle over a kill switch turned on while the daemon ran: %v", err)
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Errorf("a person's kill switch was turned on while the daemon ran and it started %d run(s): %v", len(execs), execs)
	}
	if intents := f.state(t).Intents; len(intents) != 0 {
		t.Errorf("the cycle committed %d attempt(s) under a kill switch that is on: %+v", len(intents), intents)
	}
}

// TestDaemonStartsNothingWhenItsGuardrailsCannotBeRead: a guardrail that cannot
// be read is not a guardrail that is off. The kill switch is what stops the
// daemon starting anything at all, so the database refusing the read — of the
// switch itself, and of the policy the caps come from — is exactly when the
// daemon cannot tell, and the cycle plans nothing rather than reading the
// refusal as a switch that is open.
func TestDaemonStartsNothingWhenItsGuardrailsCannotBeRead(t *testing.T) {
	f := newDaemonFixture(t)
	d, out := f.running(t, "")
	d.once = true
	now := f.serverNow(t)
	item := approvedItem("wi-1", now)
	f.putState(t, orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion, AgentID: "aurora", Role: orchestrator.RoleOrchestrator,
		Goal: "keep the work moving", WorkItems: []orchestrator.WorkItem{item},
	})
	d.tick.Launcher = f.launcher(t)
	// The switch's own node cannot be read.
	f.db.DenyRead(budget.PathKillGlobal)

	if err := d.loop(context.Background()); err != nil {
		t.Fatalf("a cycle over a kill switch that cannot be read: %v", err)
	}
	if !d.tick.Caps.KillSwitch {
		t.Error("a switch that could not be read is being read as off, so a daemon that cannot tell starts runs nobody stopped")
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Errorf("a daemon that cannot read its own kill switch started %d run(s): %v", len(execs), execs)
	}
	if intents := f.state(t).Intents; len(intents) != 0 {
		t.Errorf("the cycle committed %d attempt(s) with its guardrails unreadable: %+v", len(intents), intents)
	}

	// And the policy the caps are read from cannot be read either: the read is
	// this cycle's, so the caps the daemon already has stay and the switch goes
	// on, rather than either of them becoming a gate that is open.
	f.db.DenyRead("")
	f.db.DenyRead(projectpolicy.PathRulesMarker())
	before := d.tick.Caps
	d.refreshCaps(context.Background())
	if !d.tick.Caps.KillSwitch {
		t.Error("an unreadable policy left the kill switch off")
	}
	if d.tick.Caps.MaxRunning != before.MaxRunning || d.tick.Caps.MaxPerCycle != before.MaxPerCycle {
		t.Errorf("an unreadable policy replaced the caps the daemon had with %+v, want %+v", d.tick.Caps, before)
	}
	if !strings.Contains(out.String(), "the guardrails could not be read") {
		t.Errorf("a guardrail read that failed said nothing: %q", out.String())
	}
}

// TestRunOnceReportsAFailedCycle: --once is the one cycle a person or a harness
// holds the daemon to, so its exit code is the answer to "did that cycle run".
// A cycle that failed — a launch the project's policy refuses, a lease that
// went to another machine, a node that cannot be read — exiting 0 would read as
// one cycle done, and the line that says one cycle was taken would be a lie.
// The lease is still released with it: a pass that failed must not keep the id
// from the daemon that drives it.
func TestRunOnceReportsAFailedCycle(t *testing.T) {
	f := newDaemonFixture(t)
	// A launch the project's policy does not admit, which is a cycle that fails
	// after the attempt was committed and with nothing started.
	publishPolicy(t, f.launchBudgetFixture, `{"version":4,"allow_repos":["github/acme/other"]}`)
	now := f.serverNow(t)
	item := approvedItem("wi-1", now)
	f.putState(t, orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion, AgentID: "aurora", Role: orchestrator.RoleOrchestrator,
		Goal: "keep the work moving", WorkItems: []orchestrator.WorkItem{item},
	})

	out, _, err := execute(t, "orchestrator", "run", "--once")
	if err == nil {
		t.Fatalf("a --once run whose cycle could not start its run exited 0: %q", out)
	}
	if !strings.Contains(err.Error(), "does not admit") {
		t.Errorf("the failure --once reported is not the cycle's own: %v", err)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("a refused launch exits %d, and a refusal is a user error", ExitCode(err))
	}
	if strings.Contains(out, "one cycle as asked") {
		t.Errorf("a pass that failed still says it took its one cycle: %q", out)
	}
	_, db, err := openBudget(context.Background(), cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := orchestrator.NewLease(db, "aurora", daemonHolder, db.ServerNow)
	if err != nil {
		t.Fatal(err)
	}
	held, err := lease.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if held.Held || held.Holder != "" {
		t.Errorf("a --once pass that failed still holds the lease as %q for the whole TTL", held.Holder)
	}
}

// TestDaemonGivesTheLeaseBackWhenItCannotStart: the lease is taken before the
// daemon has a tick to drive it with, and a start-up that then fails must take
// the id with it. A lease left behind by a daemon that never drove anything is
// not a daemon driving it: it keeps the unit's next attempt, and a person's own
// `fugaro orchestrator run`, out for the whole TTL.
func TestDaemonGivesTheLeaseBackWhenItCannotStart(t *testing.T) {
	f := newDaemonFixture(t)
	d, _ := f.daemon(t, "")
	// The guardrails the tick is built from cannot be read: the start-up fails
	// after the lease was taken.
	f.db.DenyRead(projectpolicy.PathRulesMarker())

	if err := d.start(f.ctx); err == nil {
		t.Fatal("a start-up whose guardrails could not be read returned no error")
	}
	state, err := d.lease.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Held || state.Holder != "" {
		t.Errorf("a daemon whose start-up failed left the lease as %q, so nothing else can drive the id for the whole TTL", state.Holder)
	}
}

// TestDaemonGivesTheLeaseBackWhenItIsStopped: Ctrl-C at a terminal and the
// unit's SIGTERM at a shutdown are the same thing to this loop — its context
// ends — and the lease goes back with it. A daemon that exited and left its
// claim behind keeps everything that would take the id over out for the whole
// TTL, refused with the name of a process that is not there: the unit's own
// restart, a person's `fugaro orchestrator run`, a harness's `--once` pass.
func TestDaemonGivesTheLeaseBackWhenItIsStopped(t *testing.T) {
	f := newDaemonFixture(t)
	d, out := f.running(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The stop itself: the loop's own context ends while it waits, which is
	// what the signal handler in cmd/fugaro does to it.
	d.wake = func(ctx context.Context) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}

	if err := d.loop(ctx); err != nil {
		t.Fatalf("a daemon stopped by its own context returned %v, and a stop is a clean exit", err)
	}
	state, err := d.lease.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Held || state.Holder != "" {
		t.Errorf("the daemon is gone and the lease still names %q, so nothing else can drive the id for the whole TTL", state.Holder)
	}
	if !strings.Contains(out.String(), "the lease is released") {
		t.Errorf("the daemon did not say what it did with the id: %q", out.String())
	}
}
