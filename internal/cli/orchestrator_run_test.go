package cli

import (
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
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
	// out is what the daemon says. daemon() returns it too, and a test whose
	// daemon comes through running (the crash fixtures do) reads the same
	// builder here.
	out *strings.Builder
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
	lease, err := orchestrator.NewLease(db, id, daemonHolder, daemonDriver, db.ServerNow)
	if err != nil {
		t.Fatal(err)
	}
	node, err := orchestrator.NewNode(orchestrator.NodeOptions{DB: db, AgentID: id, Role: orchestrator.RoleOrchestrator, Lease: lease})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	f.out = &out
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
// task.NewRunID names a real one, and daemonDriver is the principal whose
// credential holds it — what `fugaro orchestrator run` reads from the local
// config (D3218).
const (
	daemonHolder = "20261109-101500-abcd"
	daemonDriver = "driver@example.com"
)

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
	f.setState(t, newerReleaseState())
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

// newerReleaseState is a state node a newer release wrote: one schema_version
// above this release's, which this binary stops over rather than migrating
// (G8). It is built from the constant and not written out, so a release that
// raises SchemaVersion cannot leave this test handing the daemon a version the
// daemon reads — the node would migrate, and the test would pass while
// measuring nothing.
func newerReleaseState() string {
	return fmt.Sprintf(`{"schema_version":%d,"rev":9,"agent_id":"aurora","role":"orchestrator",
		"goal":"from a newer release","work_items":[],"decisions":[],"facts":{},"archived":[],"intents":[]}`,
		orchestrator.SchemaVersion+1)
}

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

// TestWaitPassesOverTheSnapshotAReconnectOpensWith: the stream reconnects on
// the same channel after a drop (internal/rtdb), and a new connection opens
// with the full put of the node again — the same event the first connection
// opened with, and the same reason to pass it over. A wait that took it for a
// change would end on every reconnect: a proxied connection that closes and is
// reopened, or a credential revoked and reconnected, would take the daemon from
// one cycle per --interval to one per reconnect (the backoff starts at a
// second), each one a beat, a tick, a cache write and a version check, and the
// spawn caps reached at that pace rather than at the one the person set.
//
// Mutation (run, restored): clear the latch on every event rather than when the
// connection's opening put has been consumed, as the wait did before, and this
// wait ends at the reconnect (about a second in, with an interval of four).
func TestWaitPassesOverTheSnapshotAReconnectOpensWith(t *testing.T) {
	f := newDaemonFixture(t)
	d, _ := f.running(t, "")
	d.wake = nil
	d.interval = 4 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ended := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		_ = d.wait(ctx)
		ended <- time.Since(start)
	}()
	// The connections are up and their opening puts have been passed over: the
	// wait holds four here — the daemon's own nodes, which are the state node and
	// the two beside it that a plan's items and the standing backlog live in
	// (D2735), and the day's outcomes node — and none for a repository, because
	// this state names none (E1).
	waitForStream(t, f, 4)
	time.Sleep(200 * time.Millisecond)
	// The connections go away without a word. Each stream reconnects after its
	// own backoff and opens with the node again: that put is not news, and the
	// wait has to sit it out.
	f.db.DropStreams()
	waitForStream(t, f, 4)
	select {
	case took := <-ended:
		t.Fatalf("the wait ended %s in, on the put the reconnected stream opened with and not at its interval (%s): the daemon then runs a cycle per reconnect", took, d.interval)
	case <-time.After(2 * time.Second):
	}
	select {
	case took := <-ended:
		if took < 2*time.Second {
			t.Errorf("the wait ended %s in over a node that never moved", took)
		}
	case <-time.After(d.interval + 2*time.Second):
		t.Fatal("the wait never ended: the interval no longer ends it")
	}
}

// waitForStream waits for the fake to hold n streams, which is how a test knows
// the daemon's connection is up (or has been re-established) before it acts on
// it.
func waitForStream(t *testing.T, f *daemonFixture, n int) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if f.db.Streams() == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the daemon holds %d stream(s) and this step needs %d", f.db.Streams(), n)
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

// TestWaitWakesOnTheNodesARunWrites: the run-side half of the wait, at the
// daemon's own boundary (E1, D2708). A run writes nothing under the
// orchestrator subtree: it holds an entry under `agents/<slug>` while it is in
// flight and leaves one under `outcomes/<day>` as it ends. So the wait holds a
// stream on each of those for every repository the state names — without them a
// run's end reached the daemon at its next cycle, up to the whole interval
// later, and the interval is a person's flag and not the delay a run's end
// deserves.
//
// The heartbeat is the half that must not wake it: every run in flight rewrites
// its own registry entry every fifteen seconds, and on the wire that is the same
// write a run's start is, so a wait that woke on every put of an entry would run
// several cycles a second (F45).
//
// Mutation (run, restored): wake on every put under `agents/`, and the
// heartbeats below end the wait long before the interval; return from wait()
// only at the ceiling, and the outcome's own write does not end it at all.
func TestWaitWakesOnTheNodesARunWrites(t *testing.T) {
	f, d, cs := crashDaemon(t, true, true)
	d.wake = nil
	d.interval = 10 * time.Second
	// Two seconds per burst is the production value; a test that spent it per
	// step would spend minutes here.
	d.debounce = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// startWait runs one wait as the loop runs it — in the background, over the
	// three nodes — and hands back how long it took when it ends.
	startWait := func() chan time.Duration {
		waitForStream(t, f, 0)
		ended := make(chan time.Duration, 1)
		go func() {
			start := time.Now()
			_ = d.wait(ctx)
			ended <- time.Since(start)
		}()
		// The daemon's own nodes — the state node and the two beside it, where a
		// plan's items and the standing backlog live (D2735) — the registry of
		// the repository the state's item names, and today's outcomes node.
		waitForStream(t, f, 5)
		// Long enough for every stream's opening snapshot to be read: what the
		// snapshot holds is not news, and a wait that had not read it yet would
		// read the writes below as part of it.
		time.Sleep(200 * time.Millisecond)
		return ended
	}
	// The entry the run in flight already holds, seeded rather than written
	// through the client: it is what a daemon starting beside a run reads in
	// its opening snapshot, and a write here would be a wake of its own — a
	// run's start is one, which the last step of this test pins.
	entry := map[string]any{"repo": "acme/app", "execution": "exec-1", "started": f.now.UnixMilli()}
	f.db.Set(budget.PathAgent(appSlug, cs.runID), entry)

	ended := startWait()

	// A heartbeat: the same entry rewritten by the run in flight every fifteen
	// seconds, which on the wire is the same put of the same entry a run's
	// start is (budget.Session.heartbeat). A wait that woke on it would run
	// several cycles a second (F45).
	for i := 0; i < 3; i++ {
		if err := d.db.Patch(ctx, "", map[string]any{budget.PathAgent(appSlug, cs.runID): entry}); err != nil {
			t.Fatalf("rewriting the registry entry a run in flight holds: %v", err)
		}
	}
	select {
	case took := <-ended:
		t.Fatalf("a run's heartbeat ended the wait %s in, so the daemon ticks at the heartbeat's pace (F45)", took)
	case <-time.After(300 * time.Millisecond):
	}

	// The run's end, as the registry writes it: the outcome under the day the
	// database's own clock names (budget.Session.Finish).
	day := budget.Day(f.serverNow(t))
	if err := d.db.Patch(ctx, "", map[string]any{budget.PathOutcome(day, appSlug, cs.runID): map[string]any{"status": "succeeded"}}); err != nil {
		t.Fatalf("writing the outcome a run leaves as it ends: %v", err)
	}
	select {
	case took := <-ended:
		if took > d.interval/2 {
			t.Errorf("the wait ended %s in over an outcome written just after it began, and a run's end must wake the daemon long before its interval (%s)", took, d.interval)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an outcome was written and the wait did not end: the daemon learns of a run's end at its next cycle, up to the interval later")
	}

	// And the other half of what `agents/` carries: an entry that appears is a
	// wake, which is what a run a person started under this repository's node
	// asks for.
	ended = startWait()
	next := "20261117-101600-beef"
	if err := d.db.Patch(ctx, "", map[string]any{budget.PathAgent(appSlug, next): entry}); err != nil {
		t.Fatalf("writing the registry entry a run's start leaves: %v", err)
	}
	select {
	case took := <-ended:
		if took > d.interval/2 {
			t.Errorf("the wait ended %s in over a registry entry that appeared, and a run's start must wake the daemon long before its interval (%s)", took, d.interval)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a registry entry appeared and the wait did not end")
	}
}

// TestWaitWakesOnAVanishedEntryAndTheCycleChecksIt: the registry entry of a run
// that died is what something else clears — a crashed container never writes an
// outcome, and the budget's own sweep clears the entry five minutes later — and
// the run it named is one whose execution the cycle then has to check (D2725).
// The wait wakes on the disappearance like any other change, and the check is
// the one the cycle already makes for every run that has not reported: nothing
// new is added to the view for it, because a run with no outcome has its intent
// open, and the view asks the platform about the execution of every such run.
func TestWaitWakesOnAVanishedEntryAndTheCycleChecksIt(t *testing.T) {
	f, d, cs := crashDaemon(t, true, true)
	// The platform has ended the run's execution the way a killed container
	// does, and what the runner left behind is the record it wrote as it
	// began: the pair executionOver exists for.
	f.run.SetState(f.run.Executions()[0], backend.StateFailed)
	rec := &runstore.Record{
		Version: 1, RunID: cs.runID, Execution: "exec-1", Status: runstore.StatusRunning,
		Stage: "bootstrap", Outcome: runstore.OutcomeNone, StartedAt: f.now,
	}
	if err := f.runStore(t, cs.runID).WriteRecord(context.Background(), rec); err != nil {
		t.Fatalf("writing the record a run in flight carries: %v", err)
	}
	// The entry it holds, seeded rather than written through the client: it is
	// what a daemon that starts with runs already in flight reads in its
	// opening snapshot, and a write here would be a wake of its own.
	f.db.Set(budget.PathAgent(appSlug, cs.runID), map[string]any{"repo": "acme/app", "execution": "exec-1"})

	d.wake = nil
	d.interval = 10 * time.Second
	d.debounce = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ended := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		_ = d.wait(ctx)
		ended <- time.Since(start)
	}()
	// The daemon's own nodes (the state node and the plans and backlog nodes
	// beside it, D2735), the repository's registry, and today's outcomes.
	waitForStream(t, f, 5)
	time.Sleep(200 * time.Millisecond)

	// The container died: the entry goes with no outcome beside it.
	if err := d.db.Patch(ctx, "", map[string]any{budget.PathAgent(appSlug, cs.runID): nil}); err != nil {
		t.Fatalf("clearing the registry entry of a run that died: %v", err)
	}
	select {
	case took := <-ended:
		if took > d.interval/2 {
			t.Errorf("the wait ended %s in over a registry entry that vanished, and that has to wake the daemon long before its interval (%s)", took, d.interval)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a registry entry vanished with no outcome and the wait did not end: nothing wakes the daemon for a run that died without reporting")
	}
	if out := f.out.String(); !strings.Contains(out, "vanished with no outcome") {
		t.Errorf("the daemon did not name the run whose entry vanished, and a person reading its output cannot tell it from an ordinary cycle:\n%s", out)
	}

	// The cycle the wake asked for is the check: the record still says running,
	// the platform says the execution is over, and the attempt is folded.
	rep, err := d.tick.Step(context.Background())
	if err != nil {
		t.Fatalf("the cycle a vanished entry woke: %v", err)
	}
	if rep.Folded == 0 {
		t.Fatalf("the woken cycle folded nothing over a run whose entry vanished and whose execution the platform had ended: %+v", rep)
	}
	if it := f.intent(t, orchestrator.IntentID(cs.item.ID, 1)); it.Outcome != orchestrator.OutcomeFailed {
		t.Errorf("the attempt behind the vanished entry carries %q, and a run that ended without reporting failed", it.Outcome)
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

// claimNode is one claim node as the database carries it: which orchestrator
// holds it and the server time it was last written at, in the spelling the fake
// stores.
func claimNode(t *testing.T, db *gcpfake.RTDB, ref orchestrator.ItemRef) (string, string) {
	t.Helper()
	path, err := orchestrator.ClaimPath(ref)
	if err != nil {
		t.Fatalf("building the claim path of %q: %v", ref.Item, err)
	}
	v := db.Value(path)
	doc, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("the claim node at %s is %#v, not a document", path, v)
	}
	holder, _ := doc["holder"].(string)
	at, _ := doc["at"].(string)
	return holder, at
}

// TestBeatRestampsTheClaimsTheDaemonHolds: the beat writes this daemon's own
// claims beside the lease it holds (beatAndRefresh), and writes nothing else.
//
// The claim node records the server time its holder last confirmed an item was
// still its own, so a daemon that beat only its lease would leave that stamp on
// the day the item was taken — and a claim another orchestrator holds is never
// written: a claim is released by its item finishing and by nothing else (the
// owner's ruling of 2026-11-23), so a beat that re-stamped a rival's node would
// be taking an item whose run is still going.
//
// What the beat re-stamps is the picture the daemon's own cycle read off its
// nodes (orchestrator.Tick.Held) and never a walk of the project's claims node:
// a plan task's claim stands for good once the task is finished (claim.go's
// header), so that node is a growing record of every claim the project ever made
// and a beat that walked it would read one node more for each of them, for ever.
// The cycle below is what puts the item in that picture, which is also the only
// way a claim of this daemon's can exist: a claim is taken inside a cycle.
func TestBeatRestampsTheClaimsTheDaemonHolds(t *testing.T) {
	f := newDaemonFixture(t)
	d, _ := f.running(t, "")
	ctx := context.Background()

	// The item the claim is taken on, as the daemon's own node carries it: a
	// plan task approved and started, so the cycle's reading names it (and
	// nothing else) as work this daemon holds.
	f.setState(t, `{"schema_version":1,"rev":1,"agent_id":"aurora","role":"orchestrator",
		"goal":"keep the work moving","work_items":[{"id":"wi-1","title":"land demo/A","repo":"acme/app",
		"recipe":"standard","rigor_tier":"medium","brief":"Do the thing.","plan_task":"demo/A",
		"phase":"running","run_id":"20261109-101500-abcd","attempts":1}],
		"decisions":[],"facts":{},"archived":[],"intents":[]}`)
	if _, err := d.tick.Step(ctx); err != nil {
		t.Fatalf("the cycle that reads the node: %v", err)
	}

	mine := orchestrator.ItemRef{Orchestrator: d.id, Item: "demo/A"}
	other := orchestrator.ItemRef{Orchestrator: "aurora~night", Item: "demo/B"}
	rival, err := orchestrator.NewClaim(d.db, other.Orchestrator, d.db.ServerNow)
	if err != nil {
		t.Fatalf("building the other orchestrator's claims: %v", err)
	}
	for _, c := range []struct {
		claims *orchestrator.Claim
		ref    orchestrator.ItemRef
	}{{d.claims, mine}, {rival, other}} {
		if c.claims == nil {
			t.Fatal("the daemon has no claims to beat: running it built no guard")
		}
		if took, err := c.claims.Take(ctx, c.ref); err != nil || !took {
			t.Fatalf("the take of %q: took=%v, err=%v", c.ref.Item, took, err)
		}
	}
	held := d.tick.Held()
	if len(held) != 1 || held[0].Ref != mine {
		t.Fatalf("the daemon's own picture of what it holds is %+v, and the beat re-stamps what the cycle read off this daemon's nodes", held)
	}
	holder, takenAt := claimNode(t, f.db, mine)
	if holder != d.id {
		t.Fatalf("the claim names %q after the take, not this daemon (%q)", holder, d.id)
	}
	otherBefore := f.db.Value(claimPathOf(t, other))

	// The clock moves on, and the beat is the only thing that writes: the stamp
	// this daemon's claim carries afterwards is the server's time at the beat.
	stampAt, err := time.Parse(time.RFC3339, takenAt)
	if err != nil {
		t.Fatalf("the claim's own stamp %q: %v", takenAt, err)
	}
	later := stampAt.Add(5 * time.Second)
	f.db.SetClock(func() time.Time { return later })

	if err := d.beatAndRefresh(ctx); err != nil {
		t.Fatalf("the daemon's beat: %v", err)
	}
	holder, at := claimNode(t, f.db, mine)
	if holder != d.id {
		t.Errorf("the claim names %q after the beat, not this daemon (%q)", holder, d.id)
	}
	if want := later.UTC().Truncate(time.Second).Format(time.RFC3339); at != want {
		t.Errorf("the claim is stamped %q after the beat, not the server time the beat was made at (%s)", at, want)
	}
	if after := f.db.Value(claimPathOf(t, other)); !reflect.DeepEqual(otherBefore, after) {
		t.Errorf("the other orchestrator's claim changed under a beat:\nafter  %#v\nbefore %#v", after, otherBefore)
	}
	lease, err := d.lease.Read(ctx)
	if err != nil {
		t.Fatalf("reading the lease node after the beat: %v", err)
	}
	if want := later.UTC().Truncate(time.Second); !lease.At.Equal(want) {
		t.Errorf("the lease is stamped %s after the beat, not the server time the beat was made at (%s)", lease.At, want)
	}
}

// TestTheCycleLineSaysTheSkips: a cycle that skipped an action because another
// orchestrator of the project holds the item says so where a person reads the
// cycles. Without the count, the line is the same as an idle daemon's, and the
// one person who needs to know why this daemon is doing nothing while the
// project's other orchestrator works is the one reading it (Skipped, D3209).
func TestTheCycleLineSaysTheSkips(t *testing.T) {
	var out strings.Builder
	d := &orchestratorDaemon{id: "fugaro~night", out: &out, warn: &out}
	d.report(orchestrator.Report{Outcome: orchestrator.CycleInFlight, Skipped: 2}, nil)
	if said := out.String(); !strings.Contains(said, "in-flight") || !strings.Contains(said, "2 skipped") {
		t.Errorf("the cycle line is %q, and a cycle that skipped two items another orchestrator holds says so", said)
	}
}

// claimPathOf is one claim node's path, through the package's own builder: a
// path a test spelled itself could name a node the package never writes.
func claimPathOf(t *testing.T, ref orchestrator.ItemRef) string {
	t.Helper()
	path, err := orchestrator.ClaimPath(ref)
	if err != nil {
		t.Fatalf("building the claim path of %q: %v", ref.Item, err)
	}
	return path
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
	f.setState(t, newerReleaseState())
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
	if !orchestrator.BreakerOpen(s, f.serverNow(t)) {
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
	} else if orchestrator.BreakerOpen(after, f.serverNow(t)) {
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
	lease, err := orchestrator.NewLease(db, "aurora", daemonHolder, daemonDriver, db.ServerNow)
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
// refusal as a switch that is open. The switch is read as off immediately
// before the failing read that is pinned here, because a switch already on from
// the cycle above would make the assertion hold for the wrong reason.
//
// Mutation (run, restored): drop `caps.KillSwitch = true` from refreshCaps —
// the policy half of this test fails, finding the daemon holding a switch that
// is off under guardrails it cannot read.
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
	// on, rather than either of them becoming a gate that is open. The switch
	// is read as *off* first, and that is the point of this half: the cycle
	// above left it on with a read that failed, so a switch that is simply
	// still on from before would prove nothing about the failed read that is
	// being pinned here.
	f.db.DenyRead("") // the empty path lifts every DenyRead, the switch's included
	d.refreshCaps(context.Background())
	if d.tick.Caps.KillSwitch {
		t.Fatalf("the kill switch read fine as off, and the daemon holds it on: %+v", d.tick.Caps)
	}
	lastRead := d.tick.Caps
	f.db.DenyRead(projectpolicy.PathRulesMarker())
	d.refreshCaps(context.Background())
	if !d.tick.Caps.KillSwitch {
		t.Error("an unreadable policy left the kill switch off, and a guardrail that cannot be read is not one that is off")
	}
	if d.tick.Caps.MaxRunning != lastRead.MaxRunning || d.tick.Caps.MaxPerCycle != lastRead.MaxPerCycle {
		t.Errorf("an unreadable policy replaced the caps the daemon had with %+v, want %+v", d.tick.Caps, lastRead)
	}
	if !strings.Contains(out.String(), "the guardrails could not be read") {
		t.Errorf("a guardrail read that failed said nothing: %q", out.String())
	}
}

// TestDisabledPolicyLaunchesNothing: the project's own switch reaches the
// daemon on every caps refresh, and while it is off the daemon launches
// nothing (D2701, D2739). The state below is one a daemon starts a run from —
// a queued item under a standing approval — and the policy the project
// published is what holds it: the cycle commits no attempt, starts no run and
// leaves the item where it was, and the daemon says once why.
func TestDisabledPolicyLaunchesNothing(t *testing.T) {
	f := newDaemonFixture(t)
	// A policy that admits the fixture's repository and turns the project's
	// orchestrator off: the switch is the one thing between the item and a run.
	publishPolicy(t, f.launchBudgetFixture, fmt.Sprintf(`{"version":%d,"orchestrator_enabled":false,"allow_repos":["github/acme/app"]}`, projectpolicy.Version))
	d, out := f.running(t, "")
	d.once = true
	now := f.serverNow(t)
	item := approvedItem("wi-1", now)
	f.putState(t, orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion, AgentID: "aurora", Role: orchestrator.RoleOrchestrator,
		Goal: "keep the work moving", WorkItems: []orchestrator.WorkItem{item},
	})
	d.tick.Launcher = f.launcher(t)

	if err := d.loop(context.Background()); err != nil {
		t.Fatalf("a cycle over a project that turned its orchestrator off: %v", err)
	}
	if !d.tick.Caps.KillSwitch {
		t.Fatal("the project's switch is off and the caps are open, so what this cycle did proves nothing")
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Errorf("a project that turned its orchestrator off started %d run(s): %v", len(execs), execs)
	}
	if intents := f.state(t).Intents; len(intents) != 0 {
		t.Errorf("the cycle committed %d attempt(s) under the project's own switch: %+v", len(intents), intents)
	}
	if w, ok := f.state(t).Item("wi-1"); !ok || w.Phase != orchestrator.PhaseQueued {
		t.Errorf("wi-1 is %+v, and the switch holds it rather than deciding about it", w)
	}
	// The switch is read at start-up and again before every cycle, and it is
	// said once: a daemon under a login unit writes to a log nobody watches, and
	// one sentence per reading is a log of one sentence.
	if n := strings.Count(out.String(), "orchestrator_enabled"); n != 1 {
		t.Errorf("the daemon said the project's orchestrator is off %d times, and one reading of the switch is one line: %q", n, out.String())
	}
}

// TestUnreadableSwitchKeepsLastValue: the project's switch is a key of the
// policy node, and a policy read that fails is not a switch that is on. What
// the daemon keeps is the value it last read (D2701, D2739): a project that
// turned its orchestrator off and then lost the node the key lives in is still
// a project that turned it off, so the reading survives a failure that the
// caps themselves do not — refreshCaps keeps the caps it had and turns the
// kill switch on, and the reading it kept is what the daemon would go on
// saying.
//
// The reading is the daemon's own and not the caps', which is why it is
// asserted here: the caps a failed read leaves behind have the kill switch on
// whatever the project wrote, so they cannot say whether the project turned its
// orchestrator off or the daemon simply could not tell.
func TestUnreadableSwitchKeepsLastValue(t *testing.T) {
	f := newDaemonFixture(t)
	d, _ := f.running(t, "")
	if d.orchestratorOff {
		t.Fatal("the fixture's daemon starts with the project's orchestrator off, so this test proves nothing")
	}
	publishPolicy(t, f.launchBudgetFixture, fmt.Sprintf(`{"version":%d,"orchestrator_enabled":false}`, projectpolicy.Version))
	d.refreshCaps(context.Background())
	if !d.orchestratorOff {
		t.Fatal("a policy that writes orchestrator_enabled false did not reach the daemon's reading of the switch")
	}
	if !d.tick.Caps.KillSwitch {
		t.Fatalf("the switch read as off left the caps open: %+v", d.tick.Caps)
	}
	lastRead := d.tick.Caps

	f.db.DenyRead(projectpolicy.PathRulesMarker())
	d.refreshCaps(context.Background())
	if !d.orchestratorOff {
		t.Error("a read that failed turned the project's orchestrator back on, and a switch that cannot be read is not one that is off")
	}
	if !d.tick.Caps.KillSwitch {
		t.Error("an unreadable policy left the kill switch off, and a guardrail that cannot be read is not one that is off")
	}
	if d.tick.Caps.MaxRunning != lastRead.MaxRunning || d.tick.Caps.MaxPerCycle != lastRead.MaxPerCycle {
		t.Errorf("an unreadable policy replaced the caps the daemon had with %+v, want %+v", d.tick.Caps, lastRead)
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
	publishPolicy(t, f.launchBudgetFixture, `{"version":9,"allow_repos":["github/acme/other"]}`)
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
	lease, err := orchestrator.NewLease(db, "aurora", daemonHolder, daemonDriver, db.ServerNow)
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

// TestDaemonGivesTheLeaseBackWhenItsBeatFails: a beat that failed for any
// reason but a lost lease is the process's failure, and the id goes back with
// it. A daemon that returned while still holding its own claim left the id held
// by a process that is gone: the unit restarts the daemon at once (systemd's
// RestartSec is 100ms), the restart's Acquire is refused as "driven by another
// daemon" by that fresh claim, and after its start-limit burst the unit gives
// up for good — an installation that stops driving its id over one refused
// write. The second Acquire below is what the unit's next attempt does.
func TestDaemonGivesTheLeaseBackWhenItsBeatFails(t *testing.T) {
	f := newDaemonFixture(t)
	d, _ := f.running(t, "")

	// The next write the daemon makes is its own beat, and it is refused: a
	// database answering an error is the one thing this loop cannot answer
	// for itself.
	f.db.DenyNext(1)
	if err := d.loop(f.ctx); err == nil {
		t.Fatal("a daemon whose beat the database refused returned no error, and a beat that failed is the process's failure")
	}
	ctx := context.Background()
	state, err := d.lease.Read(ctx)
	if err != nil {
		t.Fatalf("reading the lease node: %v", err)
	}
	if state.Held || state.Holder != "" {
		t.Errorf("the lease still names %q after the daemon stopped on a failed beat, so the unit's restart is refused for the whole TTL", state.Holder)
	}
	again, err := d.lease.Acquire(ctx)
	if err != nil || !again.Held {
		t.Fatalf("the restart the unit makes could not take the id: %+v, %v", again, err)
	}
}

// TestRunViewCarriesTheWorkspaceRefusal pins the one thing the CLI owns in
// O19's chain: the reader hands the run's own record to the fold whole. The
// marker the workspace's credentials service wrote and the halt the member's
// gateway recorded are two separate facts a reader fills from two separate
// places, and nothing is decided here — a reader that judged the record would
// be the second place the rule lives, and the rule is ee/orchestrator's
// (WorkspaceRefused).
//
// A run no workspace refused carries neither: the field is absent from the
// record, and what the reader answers for it is the zero Halt, which is the
// report every other reader in this release already produced.
func TestRunViewCarriesTheWorkspaceRefusal(t *testing.T) {
	f, d, cs := crashDaemon(t, true, false)
	ctx := context.Background()
	reason, scope, detail := gateway.WorkspaceRefusalHalt()

	rec := &runstore.Record{
		Version: 1, RunID: cs.runID, Execution: "exec-1", Status: runstore.StatusHalted,
		Stage: "run", Outcome: runstore.OutcomeNone, StartedAt: f.now,
		Halt: &runstore.Halt{
			Reason: runstore.HaltReason(reason), Scope: scope, Detail: detail,
			WorkspaceRefusal: gateway.WorkspaceRefusalStop, At: f.now,
		},
	}
	if err := f.runStore(t, cs.runID).WriteRecord(ctx, rec); err != nil {
		t.Fatalf("writing the record a workspace refused: %v", err)
	}

	reports, err := d.tick.Runs.Runs(ctx, []string{cs.runID})
	if err != nil {
		t.Fatalf("the reader over a run a workspace refused: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("the reader answered %d report(s) for one run id", len(reports))
	}
	rep := reports[0]
	if rep.WorkspaceRefusal != gateway.WorkspaceRefusalStop {
		t.Errorf("the report carries the marker %q, want the one the record holds (%q)", rep.WorkspaceRefusal, gateway.WorkspaceRefusalStop)
	}
	if rep.Halt != (orchestrator.Halt{Reason: reason, Scope: scope, Detail: detail}) {
		t.Errorf("the report carries the halt %+v, want the record's own: the rule holds the marker against it and decides nothing here", rep.Halt)
	}
	if !rep.Finished || rep.Outcome != orchestrator.OutcomeRefused {
		t.Errorf("the report is finished=%v outcome=%q, want the refusal a halted record means (G4)", rep.Finished, rep.Outcome)
	}

	// A run that ended with no halt at all — the ordinary failure — carries no
	// marker and no halt, and one that carries a marker this release does not
	// know is handed on as it stands: the reader reports the record, and
	// whether a value is one of the workspace's is the rule's to answer.
	plain := &runstore.Record{
		Version: 1, RunID: cs.runID, Execution: "exec-1", Status: runstore.StatusFailed,
		Stage: "run", Outcome: runstore.OutcomeNone, StartedAt: f.now,
	}
	if err := f.runStore(t, cs.runID).WriteRecord(ctx, plain); err != nil {
		t.Fatalf("writing an ordinary record: %v", err)
	}
	reports, err = d.tick.Runs.Runs(ctx, []string{cs.runID})
	if err != nil {
		t.Fatalf("the reader over an ordinary run: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("the reader answered %d report(s) for one ordinary run id", len(reports))
	}
	if reports[0].WorkspaceRefusal != "" || reports[0].Halt != (orchestrator.Halt{}) {
		t.Errorf("an ordinary run's report is %+v, want no marker and no halt", reports[0])
	}
}

// TestRunViewCarriesThePauseNode is the filler C3's node needs on the reader
// side: a run that suspended itself is reported with the node the credentials
// service recorded for it (D3189), which is the only place the instant it comes
// back is written — the run's own record carries none, and a job that could name
// its own resume time would be an agent choosing when to spend (R8). Without it
// the fold has nothing to tell a person a pause with, and the notice the
// orchestrator draws for a suspended run reaches no sink at all.
//
// The node is read for the runs whose own record ended `paused` and no others:
// the service records one for exactly those, so a node standing over any other
// run is not a pause this reader hands on.
func TestRunViewCarriesThePauseNode(t *testing.T) {
	f, d, cs := crashDaemon(t, true, false)
	ctx := context.Background()
	resumeAt := f.now.Add(45 * time.Minute)
	node := runstore.PauseNode{Launcher: "person@example.com", ResumeAt: resumeAt.UnixMilli(), Chain: cs.runID}
	f.db.Set(runstore.PauseNodePath(appSlug, cs.runID), node)

	rec := &runstore.Record{
		Version: 1, RunID: cs.runID, Execution: "exec-1", Status: runstore.StatusPaused,
		Stage: "implement", Outcome: runstore.OutcomeNone, StartedAt: f.now,
	}
	if err := f.runStore(t, cs.runID).WriteRecord(ctx, rec); err != nil {
		t.Fatalf("writing the record of the run that suspended itself: %v", err)
	}

	reports, err := d.tick.Runs.Runs(ctx, []string{cs.runID})
	if err != nil {
		t.Fatalf("the reader over a suspended run: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("the reader answered %d report(s) for one run id", len(reports))
	}
	rep := reports[0]
	if rep.Pause == nil {
		t.Fatalf("the report of a run whose record says it suspended itself carries no pause, so nothing tells a person it is coming back: %+v", rep)
	}
	if *rep.Pause != node {
		t.Errorf("the report carries the pause %+v, want the node the credentials service recorded: %+v", *rep.Pause, node)
	}

	// The same node over a run that did not suspend itself is nothing this
	// reader reports: a pause is what the service recorded it for the run whose
	// own record ended `paused` (C3), and a stale node is not one.
	rec.Status = runstore.StatusFailed
	if err := f.runStore(t, cs.runID).WriteRecord(ctx, rec); err != nil {
		t.Fatalf("writing the record of a run that failed: %v", err)
	}
	reports, err = d.tick.Runs.Runs(ctx, []string{cs.runID})
	if err != nil {
		t.Fatalf("the reader over a failed run: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("the reader answered %d report(s) for one run id", len(reports))
	}
	if reports[0].Pause != nil {
		t.Errorf("a run whose record does not say it suspended itself was reported with the pause %+v", *reports[0].Pause)
	}
}

// TestRunViewTellsNoPauseItCouldNotRead is the other half of the reader the
// test above pins, and the half pauseNode's own rule is about: the node is
// handed on as a pause only when the service's record is there and this release
// can place it. A read that did not happen, a run the service recorded no node
// for and a node this release cannot read are each no pause — and each is said
// on the daemon's own output, because an operator has nothing else. What none
// of them may be is a pause: the record behind the read already says the run
// ended, so a pause drawn from less than a whole node is the line promising a
// return that nothing backs, which is the one lie this reader could tell
// (D3197, R18). What none of them may do either is stop the cycle — a daemon
// that refused its tick over a broken pause resumes nobody, which is a worse
// answer than the run ending as a halt.
func TestRunViewTellsNoPauseItCouldNotRead(t *testing.T) {
	cases := []struct {
		name string
		// seed is what stands at the node the service writes, or what keeps the
		// read from reaching it: the three answers pauseNode reads and the one
		// the node itself gives.
		seed  func(t *testing.T, f *daemonFixture, path string)
		wants string
	}{
		{
			// A read the database refused — the rules no longer grant it, or
			// the backend answered nothing at all. It is the case a rule
			// deployment is most likely to produce and the one a reader must
			// not read past.
			name: "a read that did not happen",
			seed: func(_ *testing.T, f *daemonFixture, path string) {
				f.db.DenyRead(path)
			},
			wants: "could not be read",
		},
		{
			// The service recorded no pause for a run whose own record says it
			// suspended itself: the contract's order ends a run paused only
			// after the ask that writes the node (C3), so this is a broken
			// pause and not a pause nothing has got round to marking.
			name: "a run the service recorded no pause for",
			seed: func(_ *testing.T, _ *daemonFixture, _ string) {},
			// The absent-node line is the one of the three that says what
			// follows from it, and that is the fact a person acts on.
			wants: "recorded no pause",
		},
		{
			// A node carrying a member this release does not know: the pause
			// spans a fugaro update (F36), so a node written by another release
			// is a shape this one refuses rather than reads as the members it
			// happens to recognise (R18, R25's own rule).
			name: "a node this release cannot place",
			seed: func(_ *testing.T, f *daemonFixture, path string) {
				f.db.Set(path, map[string]any{
					"launcher": "person@example.com", "resume_at": f.now.Add(time.Hour).UnixMilli(),
					"chain": "20261122-120000-abcd", "resumed_by": "20261122-130000-bbbb",
				})
			},
			wants: "not one this release can read",
		},
		{
			// Half a node: every member known, and one of them naming no usable
			// pause. Half a node is not one the orchestrator may resume on
			// (R18), and the instant it does not back is exactly what the
			// notice would promise.
			name: "half a node",
			seed: func(_ *testing.T, f *daemonFixture, path string) {
				f.db.Set(path, map[string]any{"resume_at": f.now.Add(time.Hour).UnixMilli(), "chain": "20261122-120000-abcd"})
			},
			wants: "not one this release can read",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, d, cs := crashDaemon(t, true, false)
			ctx := context.Background()
			path := runstore.PauseNodePath(appSlug, cs.runID)
			c.seed(t, f, path)

			// The record is the run's own: it says the run ended paused, which
			// is the one thing that makes the reader look at the node at all.
			rec := &runstore.Record{
				Version: 1, RunID: cs.runID, Execution: "exec-1", Status: runstore.StatusPaused,
				Stage: "implement", Outcome: runstore.OutcomeNone, StartedAt: f.now.Add(-time.Hour),
			}
			if err := f.runStore(t, cs.runID).WriteRecord(ctx, rec); err != nil {
				t.Fatalf("writing the record of the run that suspended itself: %v", err)
			}

			reports, err := d.tick.Runs.Runs(ctx, []string{cs.runID})
			if err != nil {
				t.Fatalf("the reader failed over a run whose pause node is %s: %v — a broken pause is a warning and never a stopped cycle, because a daemon that will not tick resumes nobody", c.name, err)
			}
			if len(reports) != 1 {
				t.Fatalf("the reader answered %d report(s) for one run id", len(reports))
			}
			if reports[0].Pause != nil {
				t.Errorf("a run whose pause node is %s was reported with the pause %+v: the notice would promise a return nothing backs", c.name, *reports[0].Pause)
			}

			// The daemon's own output is the only place this failure is
			// visible — the report handed to the fold carries no pause and no
			// word about why — and it names the run the way every warning about
			// one does: the short form, which is what a person types back into
			// `fugaro diagnose`.
			out := f.out.String()
			if short := task.ShortRun(cs.runID); !strings.Contains(out, short) || !strings.Contains(out, c.wants) {
				t.Errorf("the daemon said %q over a run whose pause node is %s, and the operator's only signal is a line naming the run (%s) and what was wrong (%q)", out, c.name, short, c.wants)
			}
		})
	}
}
