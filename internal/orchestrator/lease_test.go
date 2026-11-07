package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The daemon lease's tests (O3, G9, D1232).
//
// One daemon per orchestrator id, and the fence is the store's if-match: two
// daemons read one empty lease node, both decide the lease is free, and exactly
// one write lands. There is no fourth node and no grant here (the mailbox's M1:
// the lease IS the heartbeat), the lease is a sibling of the state node — a PUT
// of the parent would wipe the children and every beat would move one shared
// ETag — and a daemon that cannot read the node holds no lease at all.
//
// Everything runs on internal/gcpfake's Realtime Database fake: no live cloud,
// no clock (every instant is a value the test sets), no key and no secret.
//
// **The red step that was run and watched fail** (O3's, and the one this file
// exists for): with Acquire rewritten to write after a lost race — re-read the
// ETag and write anyway, which is what "drop the if-match" means in code —
// TestTwoDaemonsOneLease fails with both daemons' claims landing, the second
// overwriting the first and both reporting Held. Restored; see the pull
// request.

const (
	daemonID     = "orchestrator-a"
	winnerHolder = "20261107-220502-1df8"
	loserHolder  = "20261107-220503-2c19"
)

// leaseAt is every instant in this file unless a test moves it, and the clock
// is the test's own function: nothing here reads a real one.
var leaseAt = time.Date(2026, 11, 7, 22, 5, 30, 0, time.UTC)

// TestTwoDaemonsOneLease is the single-writer fence, and O3's headline test.
//
// Two daemons for one orchestrator id read **one empty lease node** — the
// loser's read is the real read, held open until the winner has written, so
// both formed their claim from the same empty node and the same ETag, which is
// the race that matters — and both then try to take it. Exactly one write
// lands: the loser's is refused rtdb.ErrPrecondition, the node still names the
// winner, and the loser holds no lease and acts on nothing. A daemon that
// forced its write past the 412 would put two daemons on one state node.
//
// Mutation: in Acquire, write with no if-match (or retry the write after a lost
// race without re-deciding), and both daemons report Held with the loser's
// claim as the one the node carries.
func TestTwoDaemonsOneLease(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	path := mustPath(t, orchestrator.PathLease)
	now := clockAt(leaseAt)

	read := make(chan struct{})
	hold := make(chan struct{})
	loserDB := &countingCAS{inner: rtdbClient(t, f), afterRead: func() { close(read); <-hold }}
	winnerDB := &countingCAS{inner: rtdbClient(t, f)}

	winner := mustLease(t, winnerDB, winnerHolder, now)
	loser := mustLease(t, loserDB, loserHolder, now)

	type outcome struct {
		state orchestrator.LeaseState
		err   error
	}
	loserDone := make(chan outcome, 1)
	go func() {
		state, err := loser.Acquire(context.Background())
		loserDone <- outcome{state, err}
	}()

	<-read
	won, err := winner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("the winner's acquire: %v", err)
	}
	close(hold)

	var lost outcome
	select {
	case lost = <-loserDone:
	case <-time.After(30 * time.Second):
		t.Fatal("the loser's acquire never finished: a lost race must not spin")
	}

	if !won.Held || won.Holder != winnerHolder {
		t.Fatalf("the winner holds %+v, not the lease it asked for", won)
	}
	if won.At != leaseAt {
		t.Errorf("the winner's claim is stamped %s, not the server time it read (%s)", won.At, leaseAt)
	}
	if lost.err != nil {
		t.Fatalf("the loser's acquire ended in an error rather than a lost race: %v", lost.err)
	}
	if lost.state.Held {
		t.Errorf("both daemons hold the lease of one orchestrator id: %+v", lost.state)
	}
	if lost.state.Holder != winnerHolder {
		t.Errorf("the loser is told the node is held by %q, not by %q", lost.state.Holder, winnerHolder)
	}

	// The loser's acquire saw a free lease — so it really did try to write —
	// and the store refused it. That refusal is the fence, and it is one write:
	// a daemon that lost the race re-reads and re-decides, it does not spin.
	if loserDB.puts != 1 {
		t.Errorf("the loser wrote %d times; a lost race is one refused write and a re-read", loserDB.puts)
	}
	if len(loserDB.errs) != 1 || !errors.Is(loserDB.errs[0], rtdb.ErrPrecondition) {
		t.Errorf("the loser's write was refused with %v, not the store's precondition error", loserDB.errs)
	}
	if !loserDB.absent {
		t.Error("the loser did not read an empty lease node, so the race this test stages was not the race it ran")
	}

	// Nothing moved: the node still carries the winner's own claim.
	doc := nodeObject(t, f.Value(path), "the lease")
	if got := nodeText(t, doc, "holder"); got != winnerHolder {
		t.Errorf("the lease names %q after the race, not the winner: both writes landed", got)
	}
	if got := nodeText(t, doc, "at"); got != "2026-11-07T22:05:30Z" {
		t.Errorf("the lease is stamped %q, not the winner's server time", got)
	}

	// And the loser's next acquire is refused by the node, not by the ETag: the
	// lease is fresh and it is not the loser's, so the loser writes nothing.
	again, err := loser.Acquire(context.Background())
	if err != nil {
		t.Fatalf("the loser's second acquire: %v", err)
	}
	if again.Held || again.Holder != winnerHolder {
		t.Errorf("the loser's second acquire takes a fresh lease that is not its own: %+v", again)
	}
	if loserDB.puts != 1 {
		t.Errorf("the loser wrote %d times in total; a fresh lease it does not hold is never written", loserDB.puts)
	}
	if held, err := loser.Held(context.Background()); err != nil || held {
		t.Errorf("the loser's guard says held=%v (err %v): a daemon that lost the race must act on nothing", held, err)
	}

	// A daemon that exits cleanly hands the lease over: the winner's release is
	// its own to make, and the next acquire finds the node free.
	freed, err := winner.Release(context.Background())
	if err != nil {
		t.Fatalf("the winner's release: %v", err)
	}
	if !freed {
		t.Error("the winner released a lease it held and the release said it did not")
	}
	if _, err := loser.Acquire(context.Background()); err != nil {
		t.Fatalf("the loser's acquire after the hand-over: %v", err)
	}
}

// TestLeaseExpiresWhenTheHolderStopsBeating is the other half of the fence: the
// lease is a heartbeat, so a daemon that dies on a laptop lid does not hold the
// orchestrator id for ever. The fixture is a lease another program wrote, so
// the shape the node carries is a file this package did not produce.
//
// Mutation: take freshness out of Acquire (treat any node that names a holder
// as held) and the takeover at the TTL never happens; stamp the claim from the
// caller's clock instead of the server's and the "too far ahead" case holds the
// lease for ever.
func TestLeaseExpiresWhenTheHolderStopsBeating(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	path := mustPath(t, orchestrator.PathLease)
	f.Set(path, leaseFixture(t))

	seeded := leaseFixtureAt(t)
	clock := seeded
	lease := mustLease(t, &countingCAS{inner: rtdbClient(t, f)}, loserHolder, orchestrator.ServerNow(func() (time.Time, bool) { return clock, true }))

	// One second before the TTL the lease is fresh: the holder is doing the
	// work, and a daemon that took it over here would put two on one state node.
	clock = seeded.Add(orchestrator.LeaseTTL - time.Second)
	state, err := lease.Acquire(context.Background())
	if err != nil {
		t.Fatalf("the acquire before the TTL: %v", err)
	}
	if state.Held || state.Holder != leaseFixtureHolder {
		t.Errorf("a lease one second short of its TTL is %+v: the fence expires early", state)
	}

	// At the TTL the holder has stopped beating — a crashed daemon, a closed
	// laptop — and the lease is this daemon's to take.
	clock = seeded.Add(orchestrator.LeaseTTL)
	taken, err := lease.Acquire(context.Background())
	if err != nil {
		t.Fatalf("the acquire at the TTL: %v", err)
	}
	if !taken.Held || taken.Holder != loserHolder {
		t.Fatalf("a lease at its TTL is %+v: a dead holder keeps the orchestrator id for ever", taken)
	}
	if taken.At != clock {
		t.Errorf("the takeover is stamped %s, not the server time it read (%s)", taken.At, clock)
	}
	if held, err := lease.Held(context.Background()); err != nil || !held {
		t.Errorf("the guard says held=%v (err %v) right after the takeover", held, err)
	}

	// A stamp far enough ahead of this daemon's clock is not honoured either:
	// two clocks that disagree by more than the skew bound cannot hold the
	// fence, so no timestamp pins an orchestrator id.
	f.Set(path, map[string]any{"holder": leaseFixtureHolder, "at": clock.Add(orchestrator.LeaseSkew + time.Second).Format(time.RFC3339)})
	future, err := lease.Acquire(context.Background())
	if err != nil {
		t.Fatalf("the acquire against a stamp in the future: %v", err)
	}
	if !future.Held || future.Holder != loserHolder {
		t.Errorf("a stamp %s ahead of the clock is %+v: a clock far ahead holds the lease for ever", orchestrator.LeaseSkew, future)
	}
}

// TestLeaseBeatKeepsTheHolder is the renewal: a daemon that keeps beating keeps
// the lease, and one whose claim another writer moved is told it lost it rather
// than writing over the new holder.
//
// Mutation: let Beat write without checking the node's holder and a daemon that
// lost the lease steals it back on its next tick.
func TestLeaseBeatKeepsTheHolder(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	path := mustPath(t, orchestrator.PathLease)
	clock := leaseAt
	lease := mustLease(t, &countingCAS{inner: rtdbClient(t, f)}, winnerHolder, orchestrator.ServerNow(func() (time.Time, bool) { return clock, true }))

	if _, err := lease.Acquire(context.Background()); err != nil {
		t.Fatalf("the acquire: %v", err)
	}
	clock = leaseAt.Add(orchestrator.LeaseBeat)
	if err := lease.Beat(context.Background()); err != nil {
		t.Fatalf("the beat: %v", err)
	}
	doc := nodeObject(t, f.Value(path), "the lease")
	if got := nodeText(t, doc, "at"); got != clock.Format(time.RFC3339) {
		t.Errorf("the beat left the claim stamped %q, not %q: the lease would expire under a live holder", got, clock.Format(time.RFC3339))
	}

	// Another daemon takes the node over (the holder's stamp went stale, or the
	// node was written by hand). This daemon's next beat must not take it back.
	other := clock.Add(orchestrator.LeaseTTL * 2)
	f.Set(path, map[string]any{"holder": loserHolder, "at": other.Format(time.RFC3339)})
	clock = other
	if err := lease.Beat(context.Background()); !errors.Is(err, orchestrator.ErrLeaseLost) {
		t.Fatalf("beating a lease another daemon holds ended as %v, not ErrLeaseLost", err)
	}
	if got := nodeText(t, nodeObject(t, f.Value(path), "the lease"), "holder"); got != loserHolder {
		t.Errorf("the beat wrote over the holder the node names (%q): a daemon that lost the lease stole it back", got)
	}
}

// leaseFixtureHolder and leaseFixtureAt are the claim testdata/lease-a.json
// carries: a holder that is not one of this file's daemons, so a test cannot
// pass by reading back something it wrote.
const leaseFixtureHolder = "20261107-101500-beef"

func leaseFixtureAt(t *testing.T) time.Time {
	t.Helper()
	var doc struct {
		At string `json:"at"`
	}
	raw, err := os.ReadFile("testdata/lease-a.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the lease fixture is not JSON: %v", err)
	}
	at, err := time.Parse(time.RFC3339, doc.At)
	if err != nil {
		t.Fatalf("the lease fixture's stamp %q is not a time: %v", doc.At, err)
	}
	return at
}

func leaseFixture(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	raw, err := os.ReadFile("testdata/lease-a.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the lease fixture is not JSON: %v", err)
	}
	if doc["holder"] != leaseFixtureHolder {
		t.Fatalf("the lease fixture names %v, not %q: the file and this test have drifted apart", doc["holder"], leaseFixtureHolder)
	}
	return doc
}

// countingCAS is the store as a test sees it: how many reads and writes a
// daemon made, what the store answered, and a hook that runs inside the first
// read — after the real read answered and before the caller sees it — which is
// how a test makes two daemons hold one ETag while a third writes.
type countingCAS struct {
	inner     *rtdb.Client
	gets      int
	puts      int
	absent    bool
	errs      []error
	afterRead func()
	held      bool
}

func (c *countingCAS) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	etag, found, err := c.inner.GetETag(ctx, path, out)
	c.gets++
	if err == nil && !found {
		c.absent = true
	}
	if c.afterRead != nil && !c.held {
		c.held = true
		c.afterRead()
	}
	return etag, found, err
}

func (c *countingCAS) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	c.puts++
	err := c.inner.PutIfMatch(ctx, path, etag, v)
	c.errs = append(c.errs, err)
	return err
}

// refusingCAS answers every conditional write the way a moved ETag does and
// every read with the real node, so a store that pushed its body again under a
// fresher ETag would write over a document it never read.
type refusingCAS struct {
	inner orchestrator.CAS
	gets  int
	puts  int
}

func (c *refusingCAS) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	c.gets++
	return c.inner.GetETag(ctx, path, out)
}

func (c *refusingCAS) PutIfMatch(context.Context, string, string, any) error {
	c.puts++
	return fmt.Errorf("the ETag moved: %w", rtdb.ErrPrecondition)
}

func clockAt(at time.Time) orchestrator.ServerNow {
	return func() (time.Time, bool) { return at, true }
}

func mustLease(t *testing.T, db orchestrator.CAS, holder string, now orchestrator.ServerNow) *orchestrator.Lease {
	t.Helper()
	lease, err := orchestrator.NewLease(db, daemonID, holder, now)
	if err != nil {
		t.Fatalf("building the lease for %q: %v", holder, err)
	}
	return lease
}

// mustPath is one of PathNode, PathState, PathLease and PathAudit for this
// test's orchestrator id: a path this package builds is never a string a test
// wrote, so a wrong path is a failure here rather than a node nobody read.
func mustPath(t *testing.T, pathOf func(string) (string, error)) string {
	t.Helper()
	path, err := pathOf(daemonID)
	if err != nil {
		t.Fatalf("building the node path: %v", err)
	}
	return path
}

// TestLeaseFailsClosedOnWhatItCannotRead is the same rule the state node
// follows, on the node that decides who may write it: a lease this release
// cannot read is a refusal, never "nobody holds it". Reading a broken node as
// free is how two daemons take one orchestrator id, and a claim with no server
// time behind it is not made at all — a laptop's clock is not a fence.
//
// Mutation: treat an unreadable node as absent (skip the decode, or default
// the holder) and every case below takes the lease over instead of refusing.
func TestLeaseFailsClosedOnWhatItCannotRead(t *testing.T) {
	ctx := context.Background()
	path := mustPath(t, orchestrator.PathLease)

	for _, tc := range []struct {
		name string
		doc  any
	}{
		{"a holder that is not a name", map[string]any{"holder": "", "at": "2026-11-07T22:04:00Z"}},
		{"a field this release does not model", map[string]any{"holder": winnerHolder, "at": "2026-11-07T22:04:00Z", "boot": "x"}},
		{"a value that is not a document", "not a lease"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := gcpfake.NewRTDB(t)
			f.Set(path, tc.doc)
			before := f.Value(path)
			counting := &countingCAS{inner: rtdbClient(t, f)}
			lease := mustLease(t, counting, loserHolder, clockAt(leaseAt))

			if _, err := lease.Acquire(ctx); !errors.Is(err, orchestrator.ErrInvalid) {
				t.Fatalf("acquiring a lease node with %s ended as %v, not %v", tc.name, err, orchestrator.ErrInvalid)
			}
			if counting.puts != 0 {
				t.Errorf("the claim wrote the node %d times over a lease it could not read", counting.puts)
			}
			if !reflect.DeepEqual(f.Value(path), before) {
				t.Error("a refused claim changed the lease node")
			}
		})
	}

	t.Run("no server time, no claim", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		counting := &countingCAS{inner: rtdbClient(t, f)}
		lease := mustLease(t, counting, loserHolder, neverAnswered)
		if _, err := lease.Acquire(ctx); !errors.Is(err, orchestrator.ErrNoServerTime) {
			t.Fatalf("a claim with no server time ended as %v, not ErrNoServerTime", err)
		}
		if counting.puts != 0 {
			t.Error("a claim was written with a clock this package does not have")
		}
	})
}
