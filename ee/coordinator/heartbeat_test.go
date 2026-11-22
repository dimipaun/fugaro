// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"errors"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The sweep's heartbeat (D3212, console-fixes plan CG5): the document beside
// the views that says the coordinator is awake — when a tick last began, and
// when one last finished without failing.
//
// It is the one document the sweep writes that is a function of the clock
// rather than of its inputs, and it is deliberately beside a repository's view
// and not in it: a view stamped with the time it was computed would be a new
// generation every tick, and a reader caching on the ETag would re-read a
// document that had not changed (sweep.go's own rule). The two stamps are the
// database's own clock and never this process's.
//
// Nothing here contacts Firebase: the database is internal/gcpfake's, whose
// clock a test sets (SetClock) and whose write a test can refuse (Deny).

// atOrJustAfter reports whether a heartbeat stamp is the database's own clock
// at at. The Date header has one-second resolution and rtdb.ServerNow projects
// the server's time forward from the moment the answer arrived, so a stamp is
// at or a fraction of a second after at — never before it, and never far
// enough after to be another tick (internal/rtdb/clock_test.go's near, which
// is unexported there).
func atOrJustAfter(got, at time.Time) bool {
	return !got.Before(at) && got.Before(at.Add(2*time.Second))
}

// TestSweepWritesHeartbeat: a tick leaves the heartbeat at
// coordinator/project/heartbeat saying when it began and when it finished, both
// stamped with the database's clock; there is none before the first tick, which
// is how a reader tells a coordinator whose sweep was never scheduled (the job
// is off by default) from one that is running; and — unlike a view — it moves
// on every tick, which is the whole of what "alive" means.
//
// Hand mutation this fails on: stamping with Sweeper.Now (the test's fixed
// 2026-10-24 12:00, which the fake's clock is not), writing the heartbeat once
// instead of twice (the second tick's started_at does not move), and putting it
// inside the view (the view's ETag moves on a second sweep over unchanged
// inputs, which sweep_test.go's own test pins against).
func TestSweepWritesHeartbeat(t *testing.T) {
	s, f, src := testSweeper(t)
	ctx := testCtx(t)
	// The database's clock is deliberately not Sweeper.Now's own fixed
	// 2026-10-24 12:00 (testSweeper): the stamps are the server's, and a
	// mutation that read this process's clock would land on the other date.
	at := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	f.SetClock(func() time.Time { return at })

	if _, found, err := s.Store.ReadHeartbeat(ctx); err != nil || found {
		t.Fatalf("a heartbeat before any sweep: found=%v err=%v, want none: an installation whose sweep is off has no heartbeat, and that absence is what the health line names the fix from", found, err)
	}

	src.runs, src.prs = testWork()
	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}

	hb, found, err := s.Store.ReadHeartbeat(ctx)
	if err != nil {
		t.Fatalf("reading /%s: %v", coordpath.PathHeartbeat, err)
	}
	if !found {
		t.Fatalf("the sweep wrote no heartbeat at /%s", coordpath.PathHeartbeat)
	}
	if !atOrJustAfter(hb.StartedAt, at) {
		t.Errorf("started_at = %v, want the database's clock at %v: the tick began when the server said it did", hb.StartedAt, at)
	}
	if !atOrJustAfter(hb.OKAt, at) {
		t.Errorf("ok_at = %v, want the database's clock at %v: a tick that reached its end succeeded", hb.OKAt, at)
	}

	// The keys are the wire a strict decoder reads (CG7a's map document), so
	// they are pinned here rather than left to the Go field names.
	var raw map[string]any
	if _, found, err := s.Store.Read(ctx, coordpath.PathHeartbeat, &raw); err != nil || !found {
		t.Fatalf("reading the heartbeat raw: found=%v err=%v", found, err)
	}
	for _, key := range []string{"started_at", "ok_at"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("the heartbeat at /%s carries no %q: %v", coordpath.PathHeartbeat, key, raw)
		}
	}
	if len(raw) != 2 {
		t.Errorf("the heartbeat at /%s carries %v, want started_at and ok_at alone", coordpath.PathHeartbeat, raw)
	}

	// A second tick, later by the server's clock, moves both stamps: the
	// heartbeat is the one document here that a tick over unchanged inputs
	// still moves.
	later := at.Add(15 * time.Minute)
	f.SetClock(func() time.Time { return later })
	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	next, found, err := s.Store.ReadHeartbeat(ctx)
	if err != nil || !found {
		t.Fatalf("the second tick's heartbeat: found=%v err=%v", found, err)
	}
	if !atOrJustAfter(next.StartedAt, later) {
		t.Errorf("started_at after the second tick = %v, want the database's clock at %v: a tick that ran leaves the time it began", next.StartedAt, later)
	}
	if !next.OKAt.After(hb.OKAt) {
		t.Errorf("ok_at after the second tick = %v, want later than %v: a successful tick moves it", next.OKAt, hb.OKAt)
	}
}

// TestSweepStartedAtIsTheTicksBeginning: started_at is the database's clock at
// the tick's first answer and ok_at the clock at its end, so on a tick that
// took any time at all the two differ. A tick that stamped started_at with its
// own end would report every successful tick as having begun when it finished —
// the gap a reader measures between the two stamps would always be zero, which
// is the reading that means "the schedule fired and the tick hung", and a
// health line that shows it about a healthy tick is worse than one that shows
// nothing.
//
// The database's clock moves one second per answer here, as a tick that takes a
// while sees it: the tick's beginning and its end are then two readings of one
// clock rather than one reading written twice.
//
// Hand mutation this fails on: stamping started_at in beat's ok branch as well
// (both stamps land on the tick's last reading, so they are equal).
func TestSweepStartedAtIsTheTicksBeginning(t *testing.T) {
	s, f, src := testSweeper(t)
	ctx := testCtx(t)
	src.runs, src.prs = testWork()
	start := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	answers := 0
	f.SetClock(func() time.Time {
		answers++
		return start.Add(time.Duration(answers-1) * time.Second)
	})

	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	hb, found, err := s.Store.ReadHeartbeat(ctx)
	if err != nil || !found {
		t.Fatalf("the tick's heartbeat: found=%v err=%v", found, err)
	}
	if !atOrJustAfter(hb.StartedAt, start) {
		t.Errorf("started_at = %v, want the database's clock at the tick's first answer (%v): a tick reports when it began and not when it finished", hb.StartedAt, start)
	}
	if !hb.OKAt.After(hb.StartedAt) {
		t.Errorf("ok_at = %v and started_at = %v: the end of a tick is a later reading of the same clock, and equal stamps are what a tick that never began looks like", hb.OKAt, hb.StartedAt)
	}
}

// TestReadHeartbeatRefusesADocumentItCannotRead: the read is typed and strict,
// as ReadRepoView is and for the same reason (ErrUnreadable) — a document this
// build cannot read is one it must not pass off as a heartbeat of zeroes, and
// an absent node is "no heartbeat" rather than that same zero document.
//
// Hand mutation this fails on: decoding with json.Unmarshal instead of
// strictDecode (an unknown field is dropped and the document is shown anyway).
func TestReadHeartbeatRefusesADocumentItCannotRead(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(c)
	ctx := testCtx(t)

	if _, found, err := s.ReadHeartbeat(ctx); err != nil || found {
		t.Fatalf("an absent heartbeat: found=%v err=%v, want none and no error", found, err)
	}

	// A shape a later release could write: an unknown field is a refusal here
	// and not a heartbeat with a field dropped.
	f.Set(coordpath.PathHeartbeat, map[string]any{"started_at": "2026-11-02T09:30:00Z", "later_field": 1})
	if _, _, err := s.ReadHeartbeat(ctx); !errors.Is(err, ErrUnreadable) {
		t.Errorf("reading a heartbeat with a field this build does not know = %v, want ErrUnreadable", err)
	}
}

// TestFailedSweepKeepsOkAt: a tick that fails moves started_at and leaves ok_at
// where it was, which is exactly what "trying and not succeeding" looks like to
// whoever reads the health line — and what a stamp written only at the end of a
// tick could not say at all (a failed tick writes nothing, so the line would
// show a heartbeat frozen at the last success with no sign that the schedule is
// still firing).
//
// The failure is the database's refusal of the view's write, which is the
// sweep's own failure and not a repository's (sweep_test.go's
// TestASweepThatCannotWriteFailsRatherThanReportingSuccess).
//
// Hand mutation this fails on: writing both stamps at the end of the tick
// (started_at does not move on a failed tick), and writing ok_at at the tick's
// start (it moves on a failed tick).
func TestFailedSweepKeepsOkAt(t *testing.T) {
	s, f, src := testSweeper(t)
	ctx := testCtx(t)
	// Not Sweeper.Now's own fixed clock either: only the stamps' source can
	// explain a started_at that moved when the tick began.
	first := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	f.SetClock(func() time.Time { return first })
	src.runs, src.prs = testWork()

	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err != nil {
		t.Fatal(err)
	}
	before, found, err := s.Store.ReadHeartbeat(ctx)
	if err != nil || !found {
		t.Fatalf("the first tick's heartbeat: found=%v err=%v", found, err)
	}
	if !atOrJustAfter(before.OKAt, first) {
		t.Fatalf("ok_at after a successful tick = %v, want the database's clock at %v", before.OKAt, first)
	}

	later := first.Add(time.Hour)
	f.SetClock(func() time.Time { return later })
	f.Deny(PathView(testRepo().Slug))
	if _, err := s.Sweep(ctx, []Repo{testRepo()}); err == nil {
		t.Fatal("a tick whose write was refused returned no error")
	}

	after, found, err := s.Store.ReadHeartbeat(ctx)
	if err != nil || !found {
		t.Fatalf("the heartbeat after the failed tick: found=%v err=%v", found, err)
	}
	if !atOrJustAfter(after.StartedAt, later) {
		t.Errorf("started_at after the failed tick = %v, want the database's clock at %v: the tick began, whatever became of it", after.StartedAt, later)
	}
	if !after.OKAt.Equal(before.OKAt) {
		t.Errorf("ok_at after a failed tick = %v, want %v: nothing has succeeded since the last tick that did", after.OKAt, before.OKAt)
	}
}
