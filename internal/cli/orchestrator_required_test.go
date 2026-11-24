package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/updatecheck"
	"github.com/dimipaun/fugaro/internal/versionstamp"
)

// The daemon below the project's required CLI (T6, R7).
//
// `fugaro orchestrator run` is exempt from the root fence, because the login
// unit starts this binary again after any exit: a root refusal would be the
// same old binary started again to print the same sentence, for ever — the very
// loop `holdForUpdate` exists to avoid (F9). So the daemon reads the stamp
// itself, at start and at every tick, and holds below it exactly as it holds
// over a state node a newer release wrote.

// fakeRequiredCLI is a versionstamp.Source a test decides: the reader is a
// database read over ADC with a cache file per project (T1), and none of that
// is what these tests are about.
type fakeRequiredCLI struct {
	res versionstamp.Result
	err error
}

func (f *fakeRequiredCLI) Required(context.Context, versionstamp.Query) (versionstamp.Result, error) {
	return f.res, f.err
}

// set swaps the answer, which is what an admin's `fugaro update` does to the
// node under a running daemon.
func (f *fakeRequiredCLI) set(res versionstamp.Result) { f.res = res }

// requiredCLI is one node's answer: a stamp at min, read from the database.
func requiredCLI(t *testing.T, min string) versionstamp.Result {
	t.Helper()
	return versionstamp.Result{
		Found:  true,
		Stamp:  versionstamp.Stamp{Schema: versionstamp.SchemaVersion, Min: min, SetAt: time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)},
		Origin: versionstamp.OriginDatabase,
	}
}

// TestDaemonBelowTheStampHoldsUnderAUnit: a unit starts this daemon again after
// any exit, so a stamp above its binary is not an exit — it is the same old
// binary printing the same line for ever. It holds instead: one line naming the
// required release and the brew command, nothing claimed, nothing driven, and
// the node untouched (R7). What ends the hold is the one thing that helps, the
// binary at the unit's path moving on, which is what `fugaro update` does.
func TestDaemonBelowTheStampHoldsUnderAUnit(t *testing.T) {
	f := newDaemonFixture(t)
	binary := stubBinary(t, t.TempDir(), "fugaro", "echo v9.9.9")
	d, out := f.daemon(t, binary)
	d.stamp = &fakeRequiredCLI{res: requiredCLI(t, "9.9.9")}
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
		t.Fatalf("a daemon holding below the required CLI returned %v", err)
	}
	if !strings.Contains(out.String(), "requires fugaro 9.9.9") {
		t.Errorf("the daemon did not name the required release: %q", out.String())
	}
	if !strings.Contains(out.String(), updatecheck.BrewCommand) {
		t.Errorf("the line does not name the upgrade the person has to run: %q", out.String())
	}
	if !strings.Contains(out.String(), "holding instead of exiting") {
		t.Errorf("a daemon under a unit must hold rather than exit on the stamp: %q", out.String())
	}
	if !strings.Contains(out.String(), "exiting cleanly") {
		t.Errorf("the binary at the unit's path moved on and the daemon did not exit for the unit to start it: %q", out.String())
	}
	if after := f.db.Value(f.statePath(t)); !reflect.DeepEqual(before, after) {
		t.Errorf("a daemon below the stamp wrote the state node:\nafter  %v\nbefore %v", after, before)
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Errorf("a daemon below the stamp started %d execution(s)", len(execs))
	}
	state, err := d.lease.Read(context.Background())
	if err != nil {
		t.Fatalf("reading the lease node: %v", err)
	}
	if state.Held || state.Holder != "" {
		t.Errorf("the lease names %q, and a daemon that drives nothing claims nothing", state.Holder)
	}
}

// TestDaemonOnceBelowTheStampExits: --once is one cycle by its own contract,
// and a foreground run with no --binary is nobody's unit — nothing starts it
// again — so both exit 0 below the stamp, exactly as they do when an update is
// due (F9). It is the whole command that is run here, so the exit code is the
// process's own, and the stamp is the real seam the binary would read.
func TestDaemonOnceBelowTheStampExits(t *testing.T) {
	newDaemonFixture(t)
	prevVersion, prevSource := Version, newStampSource
	// The running release is the one below the node: a dev build skips the
	// fence by design (spec, "Dev builds"), so the version is this build's own.
	Version = "0.28.0"
	newStampSource = func(versionstamp.Options) versionstamp.Source {
		return &fakeRequiredCLI{res: requiredCLI(t, "0.29.0")}
	}
	t.Cleanup(func() { Version, newStampSource = prevVersion, prevSource })

	out, _, err := execute(t, "orchestrator", "run", "--once")
	if err != nil {
		t.Fatalf("a --once daemon below the stamp returned %v, and it must exit 0 there", err)
	}
	if !strings.Contains(out, "requires fugaro 0.29.0") {
		t.Errorf("the daemon did not name the required release: %q", out)
	}
	if !strings.Contains(out, updatecheck.BrewCommand) {
		t.Errorf("the line does not name the upgrade the person has to run: %q", out)
	}
	if !strings.Contains(out, "stopping") {
		t.Errorf("a --once run below the stamp must say it is stopping: %q", out)
	}
	if !strings.Contains(out, "nothing was done") {
		t.Errorf("the refusal sentence is the spec's own and it is missing: %q", out)
	}
	if strings.Contains(out, "driving it as") {
		t.Errorf("a daemon below the stamp drove the id: %q", out)
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
		t.Errorf("the lease names %q, and a daemon below the stamp claims nothing", held.Holder)
	}
}

// TestDaemonRaisedMidRunStopsClaiming: an admin's `fugaro update` raises the
// stamp under a daemon that is already driving its id, which is the case the
// one-read-at-start daemon gets wrong. The stamp is read every tick (R7), so the
// next cycle stops it: the lease goes back the way abandon gives it back, so
// the id is not held for the whole TTL by a process that is stopping, and
// nothing is driven after the line.
//
// The local endpoint stops with it. A hold under a unit does not end, and a
// reader on this machine must not go on being answered from a feed no cycle
// refreshes: the registry file is what says so from outside the process.
func TestDaemonRaisedMidRunStopsClaiming(t *testing.T) {
	f := newDaemonFixture(t)
	d, out := f.running(t, "")
	st := &fakeRequiredCLI{}
	d.stamp = st
	getenv, dir := endpointState(t)
	d.registryGetenv, d.registryGOOS = getenv, "linux"
	registry := filepath.Join(dir, "aurora.json")
	d.serve(f.ctx)
	t.Cleanup(func() { d.unserve(context.Background()) })
	if _, err := os.Stat(registry); err != nil {
		t.Fatalf("the endpoint was not served before the stamp rose, so this test would prove nothing: %v", err)
	}
	waits := 0
	d.wake = func(context.Context) error {
		waits++
		if waits == 1 {
			// The admin's update lands while this daemon waits between cycles.
			st.set(requiredCLI(t, "9.9.9"))
			return nil
		}
		f.cancel()
		return context.Canceled
	}

	if err := d.loop(f.ctx); err != nil {
		t.Fatalf("the daemon returned %v where the stamp rising is a clean stop", err)
	}
	if waits != 1 {
		t.Errorf("the daemon waited %d times, want the one wait the stamp rose across: it stopped in the cycle after it was read", waits)
	}
	if !strings.Contains(out.String(), "requires fugaro 9.9.9") {
		t.Errorf("the daemon did not name the stamp that rose under it: %q", out.String())
	}
	if !strings.Contains(out.String(), updatecheck.BrewCommand) {
		t.Errorf("the line does not name the upgrade the person has to run: %q", out.String())
	}
	if !strings.Contains(out.String(), "stopping; the lease is released") {
		t.Errorf("a daemon that stops mid-run must say the id is going back: %q", out.String())
	}
	if _, err := os.Stat(registry); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the daemon still serves a local endpoint after the stamp rose, and a reader would be answered from a feed no cycle refreshes: %v", err)
	}
	held, err := d.lease.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if held.Held || held.Holder != "" {
		t.Errorf("the lease names %q after the stamp rose, and a daemon that stops must give the id back rather than hold it for the whole TTL", held.Holder)
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Errorf("a daemon stopped by the stamp started %d execution(s)", len(execs))
	}
}
