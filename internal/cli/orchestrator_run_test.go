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

	"github.com/dimipaun/fugaro/internal/orchestrator"
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
		interval: time.Minute, binary: binary, running: "v1.2.3",
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
