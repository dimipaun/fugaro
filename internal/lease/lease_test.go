package lease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// These tests are the invariant's own: one job, one shepherd. Every one of
// them names the bug it is written for, and every one of them was made to fail
// by planting that bug (see the comment on each).

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func newClient(t *testing.T, f *gcpfake.RTDB) *rtdb.Client {
	t.Helper()
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// at is a clock the fake answers its Date header with, truncated to the
// second (the header has one-second resolution).
func at(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

func holder(name, uid string) Holder { return Holder{Name: name, UID: uid} }

// mustTake takes the seat and fails the test when it cannot.
func mustTake(t *testing.T, db DB, path string, h Holder, o Options) Record {
	t.Helper()
	rec, err := Take(ctx(t), db, path, h, o)
	if err != nil {
		t.Fatalf("Take(%s) = %v, want the seat", h.Name, err)
	}
	return rec
}

// ------------------------------------------------------ the invariant

// TestTwoOrchestratorsNeverActOnOneJob is the plan's sentence, as a test: two
// orchestrators want to shepherd one job, and exactly one of them holds the
// seat. The loser is told, in a typed value it can branch on, and it never
// proceeds.
//
// Hand mutation this fails on: dropping the freshness check in Take (the
// second taker takes the seat over and both believe they hold it), or
// returning the held record instead of the error.
func TestTwoOrchestratorsNeverActOnOneJob(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	db := newClient(t, f)
	path := Path("aurora", "r1")
	local := holder("dimi@example.com (laptop)", "uid-local")
	central := holder("resident orchestrator", "uid-central")

	first := mustTake(t, db, path, local, Options{Mode: "enforce", PR: 42})
	if first.Generation != 1 || first.Free() {
		t.Fatalf("first Take = %+v, want generation 1 held by %s", first, local.Name)
	}
	if first.Mode != "enforce" || first.PR != 42 {
		t.Errorf("the record does not carry the mode and the PR: %+v", first)
	}

	_, err := Take(ctx(t), db, path, central, Options{})
	var he *HeldError
	if !errors.As(err, &he) || !errors.Is(err, ErrHeld) {
		t.Fatalf("the second orchestrator got %v, want a *HeldError (the seat is held and fresh)", err)
	}
	if he.Found == nil || he.Found.HolderUID != local.UID {
		t.Fatalf("the refusal does not name the holder: %+v", he)
	}
	if !strings.Contains(he.Error(), local.Name) || !strings.Contains(he.Error(), "generation 1") {
		t.Errorf("the refusal does not say who holds it and at which generation: %q", he.Error())
	}

	// And the seat is still the first one's: nothing the loser did moved it.
	cur, found, err := Read(ctx(t), db, path)
	if err != nil || !found {
		t.Fatalf("Read = %+v, %v, %v", cur, found, err)
	}
	if cur.HolderUID != local.UID || cur.Generation != 1 {
		t.Fatalf("after the refused takeover the lease is %+v, want %s at generation 1", cur, local.Name)
	}
}

// TestRaceForOneFreeSeatHasExactlyOneWinner is the red step's race: many
// orchestrators call Take on one absent lease at the same time, and exactly
// one of them comes back holding it.
//
// Hand mutation this fails on: writing with an unconditional Put, or ignoring
// rtdb.ErrPrecondition (treating a lost compare-and-set as a win). Both make
// every racer report success, which is two orchestrators on one job.
func TestRaceForOneFreeSeatHasExactlyOneWinner(t *testing.T) {
	const racers = 8
	f := gcpfake.NewRTDB(t)
	db := newClient(t, f)
	path := Path("aurora", "r1")

	var wg sync.WaitGroup
	recs := make([]Record, racers)
	errs := make([]error, racers)
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			recs[i], errs[i] = Take(ctx(t), db, path, holder(fmt.Sprintf("orchestrator %d", i), fmt.Sprintf("uid-%d", i)), Options{})
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
			if recs[i].Generation != 1 {
				t.Errorf("racer %d won with generation %d, want 1", i, recs[i].Generation)
			}
		case errors.Is(err, ErrHeld):
		default:
			t.Errorf("racer %d failed with %v, want nil or ErrHeld", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d orchestrators won one seat, want exactly 1: the compare-and-set is not one", winners)
	}
	cur, found, err := Read(ctx(t), db, path)
	if err != nil || !found || cur.Generation != 1 {
		t.Fatalf("the seat after the race: %+v, %v, %v", cur, found, err)
	}
}

// TestRaceToTakeOverAStaleSeatHasExactlyOneWinner is the same race on a seat
// that is free to take: the generation is what tells the winner's write from
// the losers', and it must move by exactly one, however many raced.
//
// Hand mutation this fails on: reading the ETag a second time just before the
// write (a read-modify-write that is not a compare-and-set), which lets two
// racers both write generation 2.
func TestRaceToTakeOverAStaleSeatHasExactlyOneWinner(t *testing.T) {
	const racers = 8
	base := at(time.Now())
	f := gcpfake.NewRTDB(t)
	f.SetClock(func() time.Time { return base })
	db := newClient(t, f)
	path := Path("aurora", "r1")

	// A lease nobody renewed: the database's clock is well past its grace.
	f.Set(path, Record{Holder: "gone", HolderUID: "uid-gone", Generation: 7, RenewedAt: base.Add(-time.Hour).UnixMilli()})
	f.SetClock(func() time.Time { return base })

	var wg sync.WaitGroup
	errs := make([]error, racers)
	gens := make([]int64, racers)
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec, err := Take(ctx(t), db, path, holder(fmt.Sprintf("orchestrator %d", i), fmt.Sprintf("uid-%d", i)), Options{})
			errs[i], gens[i] = err, rec.Generation
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
			if gens[i] != 8 {
				t.Errorf("racer %d took over with generation %d, want 8 (the stale lease's 7 plus one)", i, gens[i])
			}
		case errors.Is(err, ErrHeld):
		default:
			t.Errorf("racer %d failed with %v, want nil or ErrHeld", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d orchestrators took over one stale seat, want exactly 1", winners)
	}
	cur, _, err := Read(ctx(t), db, path)
	if err != nil || cur.Generation != 8 {
		t.Fatalf("the seat after the takeover race: %+v, %v, want generation 8", cur, err)
	}
}

// TestStaleGenerationIsRefused is the fencing token: after a takeover, the
// old holder's every guarded call is refused, and the refusal names the
// orchestrator that took the seat. This is "what the old holder's next action
// gets", and it is a typed value, not a silent continue.
//
// Hand mutation this fails on: dropping the generation comparison in Renew or
// Release (the old holder keeps renewing a seat it does not have, so both
// orchestrators act on the job).
func TestStaleGenerationIsRefused(t *testing.T) {
	base := at(time.Now())
	f := gcpfake.NewRTDB(t)
	f.SetClock(func() time.Time { return base })
	db := newClient(t, f)
	path := Path("aurora", "r1")

	old := mustTake(t, db, path, holder("dimi (laptop)", "uid-local"), Options{})
	f.SetClock(func() time.Time { return base.Add(DefaultGrace + time.Minute) })
	now := mustTake(t, db, path, holder("resident orchestrator", "uid-central"), Options{})
	if now.Generation != old.Generation+1 {
		t.Fatalf("the takeover wrote generation %d, want %d", now.Generation, old.Generation+1)
	}

	_, err := Renew(ctx(t), db, path, old, Options{})
	var le *LostError
	if !errors.As(err, &le) || !errors.Is(err, ErrLost) {
		t.Fatalf("the old holder's Renew = %v, want a *LostError", err)
	}
	if le.Found == nil || le.Found.HolderUID != "uid-central" {
		t.Fatalf("the refusal does not name the new holder: %+v", le)
	}
	if !strings.Contains(le.Error(), "stop acting on the job") {
		t.Errorf("the refusal does not tell the local orchestrator to stop: %q", le.Error())
	}
	if err := Release(ctx(t), db, path, old, Options{}); !errors.Is(err, ErrLost) {
		t.Errorf("the old holder's Release = %v, want ErrLost: it must not be able to free a seat it does not hold", err)
	}

	// The new holder's own calls still work, and the seat did not move.
	renewed, err := Renew(ctx(t), db, path, now, Options{})
	if err != nil {
		t.Fatalf("the new holder's Renew = %v", err)
	}
	if renewed.Generation != now.Generation {
		t.Errorf("a renewal moved the generation to %d, want %d", renewed.Generation, now.Generation)
	}
	cur, _, err := Read(ctx(t), db, path)
	if err != nil || cur.HolderUID != "uid-central" || cur.Generation != now.Generation {
		t.Fatalf("the seat after the old holder's refused calls: %+v, %v", cur, err)
	}
}

// TestTakeoverIsRefusedWhileTheLeaseIsFresh pins the other half of "one job,
// one shepherd": an orchestrator that wants the job while the holder is alive
// is refused, and the refusal says how long it must wait.
//
// Hand mutation this fails on: inverting the freshness test in Take (every
// orchestrator may take over whenever it likes).
func TestTakeoverIsRefusedWhileTheLeaseIsFresh(t *testing.T) {
	base := at(time.Now())
	f := gcpfake.NewRTDB(t)
	f.SetClock(func() time.Time { return base })
	db := newClient(t, f)
	path := Path("aurora", "r1")
	held := mustTake(t, db, path, holder("dimi (laptop)", "uid-local"), Options{})

	// One second short of the grace: still fresh.
	f.SetClock(func() time.Time { return base.Add(DefaultGrace - time.Second) })
	_, err := Take(ctx(t), db, path, holder("resident orchestrator", "uid-central"), Options{})
	var he *HeldError
	if !errors.As(err, &he) {
		t.Fatalf("a takeover %s into the grace = %v, want a *HeldError", DefaultGrace-time.Second, err)
	}
	if want := held.Expires(DefaultGrace); !he.Until.Equal(want) {
		t.Errorf("the refusal says the lease is fresh until %s, want %s", he.Until, want)
	}

	// One second past it: stale, so the takeover lands.
	f.SetClock(func() time.Time { return base.Add(DefaultGrace + time.Second) })
	taken := mustTake(t, db, path, holder("resident orchestrator", "uid-central"), Options{})
	if taken.Generation != held.Generation+1 {
		t.Errorf("the takeover wrote generation %d, want %d", taken.Generation, held.Generation+1)
	}
}

// TestReleaseFreesTheSeatAndKeepsTheGenerationMoving: a holder that gives the
// job up frees the seat at once, and the fencing token does not restart —
// which is what stops the released holder's generation from being the next
// holder's (D15: "the generation stays monotonic").
//
// Hand mutation this fails on: deleting the node on Release (the next taker
// starts at generation 1, and the old holder's generation is valid again).
func TestReleaseFreesTheSeatAndKeepsTheGenerationMoving(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	db := newClient(t, f)
	path := Path("aurora", "r1")
	held := mustTake(t, db, path, holder("dimi (laptop)", "uid-local"), Options{})

	if err := Release(ctx(t), db, path, held, Options{}); err != nil {
		t.Fatalf("Release = %v", err)
	}
	cur, found, err := Read(ctx(t), db, path)
	if err != nil || !found {
		t.Fatalf("the released lease is %+v, %v, %v: it must stay, with its generation", cur, found, err)
	}
	if !cur.Free() || cur.Holder != "" || cur.Generation != held.Generation+1 {
		t.Fatalf("the released lease is %+v, want a free record at generation %d", cur, held.Generation+1)
	}
	// The released holder is refused, at once and without waiting out a grace.
	if _, err := Renew(ctx(t), db, path, held, Options{}); !errors.Is(err, ErrLost) {
		t.Errorf("the released holder's Renew = %v, want ErrLost", err)
	}
	// And the next taker takes the free seat without waiting for a grace.
	next := mustTake(t, db, path, holder("resident orchestrator", "uid-central"), Options{})
	if next.Generation != held.Generation+2 {
		t.Errorf("the next taker wrote generation %d, want %d: the token must keep moving", next.Generation, held.Generation+2)
	}
}

// ------------------------------------------------------ the database's clock

// TestTheClockIsTheDatabases is the plan's third red step: the clock a lease
// is written and judged with is the database's, by an hour's disagreement with
// this machine's. Two things are asserted, because they fail separately:
//
//   - the stamp the record carries is the database's reading, not the local
//     clock's (a `time.Now()` anywhere in the write path fails this at once);
//   - the outcome — fresh, or stale enough to take over — is the database's
//     answer, and it does not change when the two clocks disagree.
//
// Hand mutation this fails on: replacing db.ServerNow() with time.Now() in
// Take/Renew. The two rows of the table below then disagree with the two rows
// with no skew, which is exactly what a machine an hour out would do to the
// invariant.
func TestTheClockIsTheDatabases(t *testing.T) {
	wall := at(time.Now())
	for _, c := range []struct {
		name string
		skew time.Duration // the database's clock, relative to this machine's
		age  time.Duration // how long ago the lease was renewed, on the database's clock
		free bool          // whether the takeover must be allowed
	}{
		{"no skew, renewed five minutes ago", 0, 5 * time.Minute, false},
		{"no skew, renewed eleven minutes ago", 0, 11 * time.Minute, true},
		{"the database is an hour ahead, renewed five minutes ago", time.Hour, 5 * time.Minute, false},
		{"the database is an hour behind, renewed five minutes ago", -time.Hour, 5 * time.Minute, false},
		{"the database is an hour behind, renewed fifty-five minutes ago", -time.Hour, 55 * time.Minute, true},
		{"the database is an hour ahead, renewed eleven minutes ago", time.Hour, 11 * time.Minute, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := gcpfake.NewRTDB(t)
			f.SetClock(func() time.Time { return wall.Add(c.skew) })
			db := newClient(t, f)
			path := Path("aurora", "r1")
			f.Set(path, Record{
				Holder: "gone", HolderUID: "uid-gone", Generation: 3,
				RenewedAt: wall.Add(c.skew).Add(-c.age).UnixMilli(),
			})

			// A lease taken now is stamped with the database's reading.
			held, err := Take(ctx(t), db, path, holder("resident orchestrator", "uid-central"), Options{})
			if !c.free {
				// The seeded lease is fresh on the database's clock, so this
				// taker must be refused. With the local clock it would be
				// taken over whenever the skew makes the lease look old.
				if err == nil {
					t.Fatalf("Take = %+v, want a refusal: the database's clock says the lease is %s old, which is inside the grace", held, c.age)
				}
				if !errors.Is(err, ErrHeld) {
					t.Fatalf("Take = %v, want ErrHeld", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Take = %v, want the seat: the database's clock says the lease is %s old, which is past the grace", err, c.age)
			}
			if got, want := held.Renewed(), wall.Add(c.skew); got.Sub(want).Abs() > 2*time.Second {
				t.Errorf("the record is stamped %s, want the database's clock (%s): the writer used a clock that is not the database's", got, want)
			}
		})
	}
}

// TestNoClockRefusesToAct: a client that has not been told the database's time
// cannot judge a lease, and it must not guess with the local clock. It fails
// closed (D3).
//
// The stub is deliberate. *rtdb.Client always has a clock by the time a read
// returns — the read's own response carries the Date header ServerNow projects
// from — so the branch this test drives is unreachable through the real
// client, which is exactly why it needs a stub: the guard is cheap, and the
// one thing it must never do is fall back to the local clock.
//
// Hand mutation this fails on: falling back to time.Now() when ServerNow
// answers false.
func TestNoClockRefusesToAct(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	db := noClockDB{newClient(t, f)}
	path := Path("aurora", "r1")
	for _, call := range []struct {
		name string
		do   func() error
	}{
		{"Take", func() error {
			_, err := Take(ctx(t), db, path, holder("dimi (laptop)", "uid-local"), Options{})
			return err
		}},
		{"Renew", func() error {
			_, err := Renew(ctx(t), db, path, Record{Holder: "dimi (laptop)", HolderUID: "uid-local", Generation: 1}, Options{})
			return err
		}},
		{"Release", func() error {
			return Release(ctx(t), db, path, Record{Holder: "dimi (laptop)", HolderUID: "uid-local", Generation: 1}, Options{})
		}},
	} {
		if err := call.do(); !errors.Is(err, ErrNoClock) {
			t.Errorf("%s with no clock = %v, want ErrNoClock", call.name, err)
		}
	}
	if v := f.Value(path); v != nil {
		t.Errorf("a call with no clock wrote the lease anyway: %v", v)
	}
}

// noClockDB is a database that has never told anyone the time.
type noClockDB struct{ DB }

func (noClockDB) ServerNow() (time.Time, bool) { return time.Time{}, false }

// ------------------------------------------------------ the write path

// TestEveryWriteIsConditional pins the compare-and-set twice over: at the wire
// (every write to the lease carries an If-Match header, and nothing but a PUT
// is ever sent) and at the call (the ETag the write carries is the one the
// read it was based on returned). A write without the precondition is the bug
// the whole invariant hangs on — it would clobber a takeover that landed
// between this caller's read and its write, which is two orchestrators acting
// on one job.
//
// Hand mutation this fails on: writing with an unconditional Put (no If-Match
// header on the wire), or with the ETag of a read taken somewhere else.
func TestEveryWriteIsConditional(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	db := newClient(t, f)
	path := Path("aurora", "r1")
	spy := &spyDB{DB: db}
	held := mustTake(t, spy, path, holder("dimi (laptop)", "uid-local"), Options{})
	if _, err := Renew(ctx(t), spy, path, held, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := Release(ctx(t), spy, path, held, Options{}); err != nil {
		t.Fatal(err)
	}

	reads, writes := 0, 0
	for _, r := range f.Requests() {
		if r.Path != "/"+path+".json" {
			continue
		}
		switch r.Method {
		case "GET":
			reads++
			if r.Header.Get("X-Firebase-ETag") != "true" {
				t.Errorf("GET %s did not ask for the node's ETag: the read is not one a compare-and-set can be built on", path)
			}
		case "PUT":
			writes++
			if r.Header.Get("If-Match") == "" {
				t.Errorf("PUT %s carried no If-Match: this write would clobber a takeover that landed since the read", path)
			}
		default:
			t.Errorf("%s %s: this package writes with PUT + If-Match and nothing else", r.Method, path)
		}
	}
	if writes != 3 || reads != 3 {
		t.Fatalf("%d reads and %d writes to the lease, want 3 of each (the take, the renew and the release)", reads, writes)
	}
	// Every write named the ETag of the read immediately before it.
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.writes) != 3 {
		t.Fatalf("the spy saw %d writes, want 3", len(spy.writes))
	}
	for i, w := range spy.writes {
		if w.etag != w.read {
			t.Errorf("write %d carried If-Match %q, and the read before it returned %q: the precondition is not the compare the caller read", i+1, w.etag, w.read)
		}
	}
}

// spyDB records what the package asks the database for, so a test can assert
// the shape of the calls rather than only their effect: which ETag each read
// returned, and which ETag each write was conditioned on.
type spyDB struct {
	DB
	mu     sync.Mutex
	last   string // the ETag of the most recent read
	writes []spyWrite
	calls  []string
}

type spyWrite struct{ read, etag, body string }

func (s *spyDB) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	etag, found, err := s.DB.GetETag(ctx, path, out)
	s.mu.Lock()
	s.last = etag
	s.calls = append(s.calls, "GET "+path+" etag="+etag)
	s.mu.Unlock()
	return etag, found, err
}

func (s *spyDB) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	b, _ := json.Marshal(v)
	s.mu.Lock()
	s.writes = append(s.writes, spyWrite{read: s.last, etag: etag, body: string(b)})
	s.calls = append(s.calls, "PUT "+path+" if-match="+etag+" body="+string(b))
	s.mu.Unlock()
	return s.DB.PutIfMatch(ctx, path, etag, v)
}

// TestTheRecordCarriesNoDeadline is D3 as a property of the bytes: the record
// has no expiry field, and the only time in it is the database's reading of
// the moment of the write. A deadline the writer computed would be a number
// the writer's own clock produced, which is the input D3 removes.
//
// Hand mutation this fails on: adding an `expires_at` (or any other deadline)
// to Record, or writing one the caller passed in.
func TestTheRecordCarriesNoDeadline(t *testing.T) {
	base := at(time.Now())
	f := gcpfake.NewRTDB(t)
	f.SetClock(func() time.Time { return base })
	db := newClient(t, f)
	path := Path("aurora", "r1")
	spy := &spyDB{DB: db}
	mustTake(t, spy, path, holder("dimi (laptop)", "uid-local"), Options{Mode: "observe", PR: 7})

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.calls) != 2 {
		t.Fatalf("the take made %d calls (%v), want a read and a write", len(spy.calls), spy.calls)
	}
	body := spy.calls[1][strings.Index(spy.calls[1], "body=")+len("body="):]
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatalf("the written record is not JSON: %v (%s)", err, body)
	}
	want := map[string]bool{"holder": true, "holder_uid": true, "generation": true, "renewed_at": true, "mode": true, "pr": true}
	for k := range fields {
		if !want[k] {
			t.Errorf("the record carries %q, which the deployed rules refuse ($other: false) and no reader applies: %s", k, body)
		}
	}
	for k := range want {
		if _, ok := fields[k]; !ok && k != "mode" && k != "pr" {
			t.Errorf("the record is missing %q: %s", k, body)
		}
	}
	for _, deadline := range []string{"expires_at", "expiry", "deadline", "valid_until"} {
		if _, ok := fields[deadline]; ok {
			t.Errorf("the record carries %q: the writer stores no deadline it computed itself (D3), and the grace is applied by every reader to renewed_at", deadline)
		}
	}
	// The one time in the record is the database's, to the millisecond.
	var renewed int64
	if err := json.Unmarshal(fields["renewed_at"], &renewed); err != nil {
		t.Fatal(err)
	}
	if d := time.UnixMilli(renewed).Sub(base); d.Abs() > 2*time.Second {
		t.Errorf("renewed_at is %s from the database's clock: it is not the database's reading", d.Round(time.Millisecond))
	}

	// And the shape has nowhere to put one: an unset field with omitempty
	// never reaches the body, so the JSON above cannot be the whole check.
	// The struct is what makes D3 structural rather than a habit — a deadline
	// field is one somebody will fill in.
	source, readErr := os.ReadFile("lease.go")
	if readErr != nil {
		t.Fatal(readErr)
	}
	fieldsSrc := recordStruct(string(source))
	if fieldsSrc == "" {
		t.Fatal("lease.go has no Record struct: this test is looking at nothing")
	}
	// The field names, from the JSON tags, and only from the code: the struct's
	// own prose says the word "deadline" (to explain why there is none), so a
	// scan of the whole block would fail on the comment that states the rule.
	declared := map[string]bool{}
	for line := range strings.Lines(fieldsSrc) {
		code, _, _ := strings.Cut(line, "//")
		if i := strings.Index(code, "`json:\""); i >= 0 {
			name, _, _ := strings.Cut(code[i+len("`json:\""):], "\"")
			declared[strings.TrimSuffix(name, ",omitempty")] = true
		}
	}
	allowed := map[string]bool{"holder": true, "holder_uid": true, "generation": true, "renewed_at": true, "mode": true, "pr": true}
	for name := range declared {
		if !allowed[name] {
			t.Errorf("the Record struct carries a %q field: D3's ruling is that the writer stores no absolute deadline it computed itself, and the shape that cannot break the ruling is a shape with nowhere to put one (the fields the deployed rules allow are the six below)", name)
		}
	}
	for name := range allowed {
		if !declared[name] {
			t.Errorf("the Record struct has no %q field, and the deployed rules require it", name)
		}
	}
}

// recordStruct is the source of lease.go's Record struct, from its `type` line
// to its closing brace, "" when there is none.
func recordStruct(src string) string {
	i := strings.Index(src, "type Record struct {")
	if i < 0 {
		return ""
	}
	j := strings.Index(src[i:], "\n}")
	if j < 0 {
		return ""
	}
	return src[i : i+j]
}

// TestThePackageNeverReadsTheLocalClock is the structural half of D3, and the
// one that cannot be defeated by a case a test forgot: the package's decision
// path has no access to the local clock at all. Every time it uses comes from
// the database (DB.ServerNow, via read).
//
// Hand mutation this fails on: a single time.Now() anywhere in lease.go.
func TestThePackageNeverReadsTheLocalClock(t *testing.T) {
	src, err := os.ReadFile("lease.go")
	if err != nil {
		t.Fatal(err)
	}
	if i := strings.Index(string(src), "time.Now()"); i >= 0 {
		line := 1 + strings.Count(string(src[:i]), "\n")
		t.Errorf("lease.go:%d reads the local clock: a lease judged on the local clock is the skew D3 exists to remove (take the time from DB.ServerNow, which read does)", line)
	}
}

// TestHolderMustBeNamed: a holder is a named identity (D3), never
// host/pid/rand — and the record is read by its name, so an empty one is a
// record nobody can act on.
//
// Hand mutation this fails on: dropping the validation in Holder.validate (a
// nameless or uid-less holder takes a seat the rules then refuse).
func TestHolderMustBeNamed(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	db := newClient(t, f)
	path := Path("aurora", "r1")
	for _, c := range []struct {
		name string
		h    Holder
	}{
		{"no name", holder("", "uid-local")},
		{"no uid", holder("dimi (laptop)", "")},
		{"blank name", holder("   ", "uid-local")},
		{"a name longer than the rules allow", holder(strings.Repeat("x", MaxString+1), "uid-local")},
		{"a name carrying a control character", holder("dimi\x1b[31m", "uid-local")},
	} {
		if _, err := Take(ctx(t), db, path, c.h, Options{}); err == nil {
			t.Errorf("Take with %s = nil, want a refusal", c.name)
		}
	}
	// Nothing was written by any of them.
	if v := f.Value(path); v != nil {
		t.Errorf("a refused holder wrote the lease: %v", v)
	}
}

// TestAHandWrittenNodeIsNotOneToTakeOver: a lease the rules would have refused
// is a node nobody wrote with this code, and taking it over would mean
// inventing a generation. It is refused instead.
//
// Hand mutation this fails on: dropping Record.valid (a generation 0 node is
// taken over and the fencing token starts from a number no rule allows).
func TestAHandWrittenNodeIsNotOneToTakeOver(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	db := newClient(t, f)
	path := Path("aurora", "r1")
	f.Set(path, map[string]any{"holder": "someone", "holder_uid": "uid-someone", "generation": 0, "renewed_at": 1})
	if _, err := Take(ctx(t), db, path, holder("dimi (laptop)", "uid-local"), Options{}); err == nil {
		t.Fatal("a lease at generation 0 was taken over, want a refusal: it is not a record this code wrote")
	}
	if v := f.Value(path); v == nil {
		t.Error("the hand-written node was overwritten by a refused call")
	}
}

// TestPathIsEscapedLikeEveryOtherPath: a slug or a job id carrying the
// characters RTDB forbids in a key must not reach the path unescaped, and an
// empty one must not collapse the path onto another node.
func TestPathIsEscaped(t *testing.T) {
	if got, want := Path("aurora", "r1"), "orchestrators/aurora/r1/lease"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if got, want := Path("a.b/c", "r#1"), "orchestrators/a%2Eb%2Fc/r%231/lease"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	for _, c := range []struct{ slug, job string }{{"", "r1"}, {"aurora", ""}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Path(%q, %q) did not panic: an empty segment collapses the path onto another node", c.slug, c.job)
				}
			}()
			Path(c.slug, c.job)
		}()
	}
}

// TestTheRulesCarryTheLeaseAndTheGraceWeApply holds the package and the
// deployed rules together, the way internal/budget/rules holds its own two
// artifacts together: the numbers and the shape a client applies must be the
// ones the database enforces, or a client would take a seat the database
// refuses (or refuse one it allows). The lease family is the whole point of
// the write path: the database is what makes the compare-and-set safe against
// an orchestrator that is not running this code.
//
// Hand mutation this fails on: moving DefaultGrace or MaxString without the
// template's LeaseGraceMs / MaxString, or dropping the orchestrators family
// from the template (the subtree then has no rules and every lease write is
// denied).
func TestTheRulesCarryTheLeaseAndTheGraceWeApply(t *testing.T) {
	golden, err := os.ReadFile("../budget/rules/testdata/rules.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rules map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(golden, &doc); err != nil {
		t.Fatal(err)
	}
	orch, _ := doc.Rules["orchestrators"].(map[string]any)
	if orch == nil {
		t.Fatal("the generated rules carry no orchestrators family: the lease subtree is not deployed, so no orchestrator can take a seat")
	}
	slug, _ := orch["$slug"].(map[string]any)
	if slug == nil {
		t.Fatal("the orchestrators family has no $slug level")
	}
	job, _ := slug["$job"].(map[string]any)
	if job == nil {
		t.Fatal("the orchestrators family has no $job level")
	}
	leaseRule, _ := job["lease"].(map[string]any)
	if leaseRule == nil {
		t.Fatal("the generated rules carry no lease node: orchestrators/<slug>/<job>/lease is not deployed")
	}
	text := string(golden)
	graceMs := fmt.Sprint(DefaultGrace.Milliseconds())
	if !strings.Contains(text, graceMs) {
		t.Errorf("the generated rules do not carry the grace (%s ms, DefaultGrace): the database would let a takeover of a fresh lease through, or refuse one of a stale lease", graceMs)
	}
	if !strings.Contains(text, fmt.Sprint(MaxString)) {
		t.Errorf("the generated rules do not carry MaxString (%d), the bound this package applies to a holder's name", MaxString)
	}
	// The record's fields are the rules' fields: a field this package writes
	// that the rules refuse would make every write fail, and a field the rules
	// allow that this package does not write is one nobody can use.
	for _, f := range []string{"holder", "holder_uid", "generation", "renewed_at", "mode", "pr"} {
		if !strings.Contains(text, `"`+f+`"`) {
			t.Errorf("the generated rules never name %q, so the record's %s field is refused ($other: false) or unvalidated", f, f)
		}
	}
	for _, f := range []string{"expires_at", "expiry", "deadline", "valid_until"} {
		if strings.Contains(text, `"`+f+`"`) {
			t.Errorf("the generated rules name %q: the record carries no deadline (D3), and neither may the rules", f)
		}
	}
}
