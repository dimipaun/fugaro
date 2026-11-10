package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The handover: the daemon reads the binary the unit names, and exits cleanly
// when it is no longer the one this process runs (O8, T7).

// stubBinary writes an executable at dir/name that behaves as the unit's binary
// does when it is asked for its version. It is the path the unit rendered, and
// nothing here is the test process's own executable: a daemon that read that
// instead would read a file whose --version is not a version at all, which is
// what the exit-on-differs cases below rule out.
func stubBinary(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDaemonRestartsWhenTheUnitBinaryDiffers is T7's upgrade hole: a package
// manager replaced the binary at the stable path the unit names, and the
// daemon that is already running is the old one. It exits cleanly — status 0,
// lease released, nothing launched — so the login unit starts the new binary.
//
// The rule is differs and not newer: a downgrade restarts the daemon too, and
// escalates nothing.
func TestDaemonRestartsWhenTheUnitBinaryDiffers(t *testing.T) {
	for _, tc := range []struct{ name, version string }{
		{"a newer binary", "v9.9.9"},
		{"a downgrade", "v0.1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDaemonFixture(t)
			binary := stubBinary(t, t.TempDir(), "fugaro", "echo "+tc.version)
			d, out := f.running(t, binary)

			if err := d.loop(f.ctx); err != nil {
				t.Fatalf("the daemon returned %v where a moved binary is a clean exit", err)
			}
			if !strings.Contains(out.String(), "exiting cleanly") {
				t.Errorf("the daemon did not say why it stopped: %q", out.String())
			}
			if !strings.Contains(out.String(), binary) {
				t.Errorf("the daemon did not name the path the unit rendered: %q", out.String())
			}
			if !strings.Contains(out.String(), runningVersion) {
				t.Errorf("the daemon did not name the version it runs: %q", out.String())
			}
			held, err := d.lease.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if held.Held || held.Holder != "" {
				t.Errorf("the lease still names %q after the handover, and the next daemon must start at once rather than wait out the TTL", held.Holder)
			}
			if execs := f.run.Executions(); len(execs) != 0 {
				t.Errorf("a handover started %d execution(s)", len(execs))
			}
		})
	}
}

// TestDaemonKeepsRunningWhileTheUnitBinaryIsTheSame: an unchanged binary is no
// handover, however many cycles pass, and the daemon keeps driving the id until
// something stops it. What is pinned is that it never handed over: the cycles it
// waited and the line it did not print. The lease is free at the end because the
// stop this test uses is the cancelled context, which gives the id back like
// every other exit (TestDaemonGivesTheLeaseBackWhenItIsStopped) — waiting for
// the TTL is the very cost that release exists to avoid.
func TestDaemonKeepsRunningWhileTheUnitBinaryIsTheSame(t *testing.T) {
	f := newDaemonFixture(t)
	binary := stubBinary(t, t.TempDir(), "fugaro", "echo "+runningVersion)
	d, out := f.running(t, binary)
	waits := 0
	d.wake = func(context.Context) error {
		waits++
		if waits == 3 {
			f.cancel()
			return context.Canceled
		}
		return nil
	}
	if err := d.loop(f.ctx); err != nil {
		t.Fatalf("the daemon returned %v over cycles that should not stop it", err)
	}
	if waits != 3 {
		t.Fatalf("the daemon waited %d times, want the three cycles this test ended it after", waits)
	}
	if strings.Contains(out.String(), "exiting cleanly") {
		t.Errorf("the daemon handed over although the binary at the unit's path is the one it runs: %q", out.String())
	}
	held, err := d.lease.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if held.Held || held.Holder != "" {
		t.Errorf("the daemon was stopped and the lease still names %q for the whole TTL", held.Holder)
	}
}

// runningVersion is the version the fixture's daemon runs as.
const runningVersion = "v1.2.3"

// TestDaemonSurvivesAnUnreadableUnitBinary: a read that fails, or a version
// this cannot parse, is logged and is never a reason to exit. A broken
// package-manager link must not stop the daemon: a daemon that exited on one
// would leave the id unclaimed with nothing to restart it. The cycles counted
// and the handover line that is absent are what say it kept going; the lease is
// free at the end because the test ends the loop by cancelling its context, and
// that exit gives the id back (TestDaemonGivesTheLeaseBackWhenItIsStopped).
func TestDaemonSurvivesAnUnreadableUnitBinary(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"a binary that fails", "exit 3", "could not be read"},
		{"a binary that prints nothing", "exit 0", "could not be read"},
		{"a binary that prints two lines", "echo v1\necho v2", "could not be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDaemonFixture(t)
			binary := stubBinary(t, t.TempDir(), "fugaro", tc.body)
			d, out := f.running(t, binary)
			waits := 0
			d.wake = func(context.Context) error {
				waits++
				if waits == 2 {
					f.cancel()
					return context.Canceled
				}
				return nil
			}
			if err := d.loop(f.ctx); err != nil {
				t.Fatalf("the daemon returned %v over a binary it could not read", err)
			}
			if waits != 2 {
				t.Fatalf("the daemon waited %d times, want the two cycles this test ended it after", waits)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("the read was not logged: %q", out.String())
			}
			if strings.Contains(out.String(), "exiting cleanly") {
				t.Errorf("the daemon handed over over a binary it could not read: %q", out.String())
			}
			held, err := d.lease.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if held.Held || held.Holder != "" {
				t.Errorf("the daemon was stopped and the lease still names %q for the whole TTL", held.Holder)
			}
		})
	}
}

// TestDaemonSkipsTheVersionCheckWhileALaunchIsInProgress: the check is skipped
// only while a Launch is in progress, never for a run merely in flight. A tick
// that handed over mid-launch would abandon a start whose intent is already on
// the node, and the daemon that took the id over would see a run that may or
// may not exist. Once the launch ends the check runs again and the handover
// happens on the next cycle, whatever else the queue is doing.
func TestDaemonSkipsTheVersionCheckWhileALaunchIsInProgress(t *testing.T) {
	f := newDaemonFixture(t)
	binary := stubBinary(t, t.TempDir(), "fugaro", "echo v9.9.9")
	d, out := f.running(t, binary)
	d.launched.begin()
	waits := 0
	d.wake = func(context.Context) error {
		waits++
		// The launch finishes while the daemon waits. Had the check run on the
		// cycle that just ended, this test would never reach here: the daemon
		// would have exited before waiting at all.
		d.launched.done()
		return nil
	}
	if err := d.loop(f.ctx); err != nil {
		t.Fatalf("the daemon returned %v where a handover is a clean exit", err)
	}
	if waits != 1 {
		t.Fatalf("the daemon waited %d times: the check ran while a launch was in progress", waits)
	}
	if !strings.Contains(out.String(), "exiting cleanly") {
		t.Errorf("the daemon never handed over after the launch ended: %q", out.String())
	}
}

// TestUnitBinaryVersionReadsTheNamedPath: the version is read from the path the
// unit rendered, through the binary's own --version, and anything else is an
// error rather than a version. The exit-on-differs rule is only as good as this
// read: a version it invented would restart the daemon for ever, and a version
// it read from the wrong path would never restart it at all.
func TestUnitBinaryVersionReadsTheNamedPath(t *testing.T) {
	dir := t.TempDir()
	path := stubBinary(t, dir, "fugaro", "echo '  v7.7.7  '")
	got, err := unitBinaryVersion(context.Background(), path)
	if err != nil {
		t.Fatalf("reading the version: %v", err)
	}
	if got != "v7.7.7" {
		t.Errorf("the version read is %q, want the one line the binary printed, trimmed", got)
	}
	moved, err := binaryMoved(context.Background(), path, runningVersion)
	if err != nil || !moved {
		t.Errorf("binaryMoved over a path at %s and a process at %s: %v, %v", got, runningVersion, moved, err)
	}
	if _, err := unitBinaryVersion(context.Background(), filepath.Join(dir, "gone")); err == nil {
		t.Error("a path with no binary at it read as a version")
	}
}

// running is the fixture's daemon after its start-up: the lease taken, the tick
// built, and the buffer it says everything into.
func (f *daemonFixture) running(t *testing.T, binary string) (*orchestratorDaemon, *strings.Builder) {
	t.Helper()
	d, out := f.daemon(t, binary)
	state, err := d.lease.Acquire(f.ctx)
	if err != nil || !state.Held {
		t.Fatalf("taking the lease: %+v, %v", state, err)
	}
	tick, err := d.newTick(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	d.tick = tick
	return d, out
}
