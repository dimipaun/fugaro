package budget_test

// W7: the workspace's own meter at the layer that holds the transaction
// (docs/workspaces/spec.md §5, D1301 as amended 2026-11-04). What these tests
// hold is the property the whole cap rests on: a reservation and a settle are
// each ONE conditional write of the workspace's own node, so two calls at the
// ceiling's boundary cannot both take the last of the room, and the bound the
// spec states as a number is the reservation in flight.
//
// The database is the fake RTDB with a real client of it, so the ETag
// compare-and-set is the client's own: a test that stubbed the write would hold
// nothing about the race.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

const (
	wsMeterName   = "workspace-alpha"
	wsMeterMember = "project-a"
	wsMeterOther  = "project-b"
)

// wsMeterNow is the moment every reservation in this file is taken at: the
// middle of a month, so nothing here is about a boundary by accident.
var wsMeterNow = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// wsMeterDB is the workspace's own database with a client of it.
func wsMeterDB(t *testing.T) (*gcpfake.RTDB, *rtdb.Client) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("rtdb.New: %v", err)
	}
	return db, client
}

// wsMeterCeiling is a ceiling of usd dollars, set.
func wsMeterCeiling(t *testing.T, usd float64) budget.WorkspaceCeiling {
	t.Helper()
	m, err := pricing.FromUSD(usd)
	if err != nil {
		t.Fatalf("pricing.FromUSD(%v): %v", usd, err)
	}
	return budget.WorkspaceCeiling{Monthly: m, Set: true}
}

// wsMeterUSD is an amount of usd dollars.
func wsMeterUSD(t *testing.T, usd float64) pricing.Micros {
	t.Helper()
	m, err := pricing.FromUSD(usd)
	if err != nil {
		t.Fatalf("pricing.FromUSD(%v): %v", usd, err)
	}
	return m
}

// wsMeterReserve is one reservation of this file's workspace.
func wsMeterReserve(t *testing.T, call, member string, usd float64, c budget.WorkspaceCeiling) budget.WorkspaceReserve {
	t.Helper()
	return budget.WorkspaceReserve{
		Name: wsMeterName, Call: call, Member: member,
		Amount: wsMeterUSD(t, usd), Ceiling: c, Now: wsMeterNow,
	}
}

// wsMeterRead is the month's node as the workspace's own database holds it.
func wsMeterRead(t *testing.T, client *rtdb.Client) budget.WorkspaceSpend {
	t.Helper()
	s, found, err := budget.ReadWorkspaceSpend(context.Background(), client, wsMeterName, budget.MonthKey(wsMeterNow))
	if err != nil {
		t.Fatalf("ReadWorkspaceSpend: %v", err)
	}
	if !found {
		t.Fatal("the workspace's meter holds no node for the month")
	}
	return s
}

// racedDB is the workspace's own database with one conditional write lost on
// purpose: the first PutIfMatch lets a competing caller land first, so the write
// that follows carries a stale ETag and the database refuses it exactly as a
// real lost race is refused. The meter must then re-read and decide again on
// what it sees, rather than write the room it had already decided on.
type racedDB struct {
	*rtdb.Client
	db    *gcpfake.RTDB
	path  string
	rival budget.WorkspaceSpend
	once  bool
}

func (r *racedDB) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	if !r.once {
		r.once = true
		r.db.Set(r.path, r.rival)
	}
	return r.Client.PutIfMatch(ctx, path, etag, v)
}

// Two calls at the ceiling's boundary cannot both take the last of the room:
// the reservation is a compare-and-set of the month's node, so a write made
// against a read another caller has invalidated does not land, and the retry
// refuses the call that no longer fits.
func TestWorkspaceCapReservationIsAtomicWithTheWorkspaceDatabase(t *testing.T) {
	ctx := context.Background()
	db, client := wsMeterDB(t)
	c := wsMeterCeiling(t, 2)
	path := budget.PathWorkspaceSpend(wsMeterName, budget.MonthKey(wsMeterNow))

	// The competing caller takes $1.50 of the $2 while this call is between its
	// read and its write.
	rival := budget.WorkspaceSpend{
		Month:   budget.MonthKey(wsMeterNow),
		Members: map[string]budget.Micros{},
		Reservations: map[string]budget.WorkspaceReservation{
			"call-rival": {Member: wsMeterOther, Amount: wsMeterUSD(t, 1.5), At: wsMeterNow.UnixMilli()},
		},
	}
	raced := &racedDB{Client: client, db: db, path: path, rival: rival}

	// $1.50 fits in $2 — and does not fit any more once the rival's $1.50 is
	// there, which is the whole of what the compare-and-set is for.
	_, _, err := budget.ReserveWorkspaceSpend(ctx, raced, wsMeterReserve(t, "call-1", wsMeterMember, 1.5, c))
	if !errors.Is(err, budget.ErrWorkspaceCeilingReached) {
		t.Fatalf("the call that lost the race came back as %v, want the ceiling's refusal: a reservation written against a stale read takes room twice", err)
	}
	if !raced.once {
		t.Fatal("the test never lost a write, so it held nothing about the race")
	}
	s := wsMeterRead(t, client)
	if len(s.Reservations) != 1 {
		t.Fatalf("the month holds %d reservations after the lost race, want the rival's one: %v", len(s.Reservations), s.Reservations)
	}
	if _, ok := s.Reservations["call-rival"]; !ok {
		t.Fatalf("the month holds %v, want the rival's reservation alone", s.Reservations)
	}
}

// The bound is the reservation in flight, stated as a number a test can hold
// rather than as an adjective: the settled figure cannot pass the ceiling except
// through the calls still in flight, and each of those took room the ceiling
// had left.
func TestWorkspaceCapOvershootIsBoundedByTheReservationInFlight(t *testing.T) {
	ctx := context.Background()
	_, client := wsMeterDB(t)
	c := wsMeterCeiling(t, 2)

	hold, room, err := budget.ReserveWorkspaceSpend(ctx, client, wsMeterReserve(t, "call-1", wsMeterMember, 1.5, c))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if room.Settled != 0 || room.Reserved != wsMeterUSD(t, 1.5) {
		t.Fatalf("the room reads %v settled and %v reserved, want 0 and $1.50", room.Settled.USD(), room.Reserved.USD())
	}
	s := wsMeterRead(t, client)
	if got := s.OvershootBound(wsMeterNow); got != wsMeterUSD(t, 1.5) {
		t.Fatalf("the overshoot bound is %v with $1.50 in flight, want $1.50", got.USD())
	}
	if s.Settled()+s.OvershootBound(wsMeterNow) > c.Monthly {
		t.Fatalf("settled %v plus the bound %v is more than the ceiling %v", s.Settled().USD(), s.OvershootBound(wsMeterNow).USD(), c.Monthly.USD())
	}

	// A second call that would take the room past the ceiling is refused, which
	// is what keeps the bound a bound: the settled figure and the calls in
	// flight never together exceed the ceiling.
	if _, _, err := budget.ReserveWorkspaceSpend(ctx, client, wsMeterReserve(t, "call-2", wsMeterOther, 0.6, c)); !errors.Is(err, budget.ErrWorkspaceCeilingReached) {
		t.Fatalf("a $0.60 call with $0.50 left came back as %v, want the ceiling's refusal", err)
	}

	// The settle turns the reservation into money spent, and the bound falls to
	// nothing with it.
	after, err := budget.SettleWorkspaceSpend(ctx, client, hold, budget.WorkspaceSettle{Actual: wsMeterUSD(t, 0.25), Ceiling: c, Now: wsMeterNow})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if after.Settled != wsMeterUSD(t, 0.25) || after.Reserved != 0 {
		t.Fatalf("after the settle the room reads %v settled and %v reserved, want $0.25 and 0", after.Settled.USD(), after.Reserved.USD())
	}
	s = wsMeterRead(t, client)
	if s.OvershootBound(wsMeterNow) != 0 {
		t.Fatalf("the bound is %v with nothing in flight, want 0", s.OvershootBound(wsMeterNow).USD())
	}
}

// A hold whose call can no longer be in flight is released rather than held to
// the month's end: the room comes back to the workspace's cap, OvershootBound
// falls with it, and the node stops carrying it the next time anything writes
// that month. Without it a hold lost to a crash, a restart, a disconnect or a
// settle nothing retried would shrink the cap under a ceiling nobody set.
func TestWorkspaceCapReleasesAHoldWhoseCallCannotStillBeInFlight(t *testing.T) {
	ctx := context.Background()
	_, client := wsMeterDB(t)
	c := wsMeterCeiling(t, 2)

	old := wsMeterReserve(t, "call-old", wsMeterMember, 1, c)
	old.Now = wsMeterNow.Add(-budget.WorkspaceReservationTTL - time.Minute)
	oldHold, _, err := budget.ReserveWorkspaceSpend(ctx, client, old)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	// The hold is in the node as it was written, with the moment it was taken:
	// that moment is what the age is read from.
	if s := wsMeterRead(t, client); len(s.Reservations) != 1 || s.Reservations["call-old"].At != old.Now.UnixMilli() {
		t.Fatalf("the node holds %v, want the hold with the moment it was taken", s.Reservations)
	}

	if _, _, err := budget.ReserveWorkspaceSpend(ctx, client, wsMeterReserve(t, "call-live", wsMeterOther, 0.5, c)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if got := wsMeterRead(t, client).OvershootBound(wsMeterNow); got != wsMeterUSD(t, 0.5) {
		t.Fatalf("the bound is %v with one $1 hold past its age and $0.50 in flight, want $0.50", got.USD())
	}

	// The read the headroom answer is made of releases it too: a service that
	// crashed leaving the hold behind has written nothing since, so the read is
	// the only place the room can come back.
	room, err := budget.ReadWorkspaceRoom(ctx, client, wsMeterName, c, wsMeterNow)
	if err != nil {
		t.Fatalf("ReadWorkspaceRoom: %v", err)
	}
	if room.Reserved != wsMeterUSD(t, 0.5) || room.Headroom != wsMeterUSD(t, 1.5) {
		t.Fatalf("the room reads %v reserved and %v of headroom, want $0.50 and $1.50", room.Reserved.USD(), room.Headroom.USD())
	}

	// A settle arriving after the hold aged out is refused rather than recorded:
	// a usage the meter cannot place is a usage the service cannot record, and
	// the call it belongs to is refused on it.
	if _, err := budget.SettleWorkspaceSpend(ctx, client, oldHold, budget.WorkspaceSettle{Actual: wsMeterUSD(t, 0.25), Ceiling: c, Now: wsMeterNow}); !errors.Is(err, budget.ErrWorkspaceReservationUnknown) {
		t.Fatalf("the settle of a hold past its age came back as %v, want the no-reservation refusal", err)
	}

	// The room it released is admitted again — $1 did not fit while both holds
	// were counted — and the write that takes it drops the aged hold from the
	// node, so it is not carried from month to month.
	if _, _, err := budget.ReserveWorkspaceSpend(ctx, client, wsMeterReserve(t, "call-3", wsMeterMember, 1, c)); err != nil {
		t.Fatalf("a $1 call with $0.50 in flight and a $1 hold left over from a crash was refused: %v", err)
	}
	s := wsMeterRead(t, client)
	if _, ok := s.Reservations["call-old"]; ok {
		t.Fatalf("the aged hold is still in the node after a write: %v", s.Reservations)
	}
	if _, ok := s.Reservations["call-live"]; !ok {
		t.Fatalf("the write dropped a hold that is still in flight: %v", s.Reservations)
	}
}

// A hold still within its age is room taken, and a moment nobody states expires
// nothing: the age is not a guess.
func TestWorkspaceCapKeepsAHoldThatIsStillInsideItsAge(t *testing.T) {
	ctx := context.Background()
	_, client := wsMeterDB(t)
	c := wsMeterCeiling(t, 2)

	just := wsMeterReserve(t, "call-just", wsMeterMember, 1, c)
	just.Now = wsMeterNow.Add(-budget.WorkspaceReservationTTL + time.Second)
	hold, _, err := budget.ReserveWorkspaceSpend(ctx, client, just)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	room, err := budget.ReadWorkspaceRoom(ctx, client, wsMeterName, c, wsMeterNow)
	if err != nil {
		t.Fatalf("ReadWorkspaceRoom: %v", err)
	}
	if room.Reserved != wsMeterUSD(t, 1) {
		t.Fatalf("a hold one second inside its age is %v of room, want the whole $1", room.Reserved.USD())
	}
	if _, err := budget.SettleWorkspaceSpend(ctx, client, hold, budget.WorkspaceSettle{Actual: wsMeterUSD(t, 0.25), Ceiling: c, Now: wsMeterNow}); err != nil {
		t.Fatalf("Settle of a hold inside its age: %v", err)
	}

	// The node as read with no moment at all: nothing is expired, because no
	// caller stated a time to expire it against.
	s := wsMeterRead(t, client)
	if got := s.Live(time.Time{}); len(got.Reservations) != len(s.Reservations) {
		t.Fatalf("an unstated moment released %d holds", len(s.Reservations)-len(got.Reservations))
	}
}

// One call, one reservation: a second reservation for a call that already holds
// one is refused rather than taken, so the same usage cannot be paid for twice.
func TestWorkspaceCapRefusesASecondReservationForOneCall(t *testing.T) {
	ctx := context.Background()
	_, client := wsMeterDB(t)
	c := wsMeterCeiling(t, 2)

	if _, _, err := budget.ReserveWorkspaceSpend(ctx, client, wsMeterReserve(t, "call-1", wsMeterMember, 1, c)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	_, _, err := budget.ReserveWorkspaceSpend(ctx, client, wsMeterReserve(t, "call-1", wsMeterMember, 1, c))
	if !errors.Is(err, budget.ErrWorkspaceCallReserved) {
		t.Fatalf("the second reservation came back as %v, want the call-already-reserved refusal", err)
	}
	if s := wsMeterRead(t, client); s.Reserved() != wsMeterUSD(t, 1) {
		t.Fatalf("the month holds %v reserved, want one reservation of $1", s.Reserved().USD())
	}
}

// A usage whose reservation is not there is a usage the meter cannot place, and
// it is refused rather than recorded: a call whose cost the service cannot
// place spends against a cap it cannot see.
func TestWorkspaceCapRefusesASettleWithNoReservation(t *testing.T) {
	ctx := context.Background()
	_, client := wsMeterDB(t)
	c := wsMeterCeiling(t, 2)

	_, err := budget.SettleWorkspaceSpend(ctx, client, budget.WorkspaceHold{
		Name: wsMeterName, Month: budget.MonthKey(wsMeterNow), Call: "call-1", Member: wsMeterMember, Amount: wsMeterUSD(t, 1),
	}, budget.WorkspaceSettle{Actual: wsMeterUSD(t, 0.25), Ceiling: c, Now: wsMeterNow})
	if !errors.Is(err, budget.ErrWorkspaceReservationUnknown) {
		t.Fatalf("the settle came back as %v, want the no-reservation refusal", err)
	}
}

// A meter node the reader cannot make sense of is refused rather than read as
// room: a negative amount read as headroom would fail open.
func TestWorkspaceCapRefusesAMeterNodeItCannotRead(t *testing.T) {
	ctx := context.Background()
	db, client := wsMeterDB(t)
	c := wsMeterCeiling(t, 2)
	path := budget.PathWorkspaceSpend(wsMeterName, budget.MonthKey(wsMeterNow))

	for _, tc := range []struct {
		name string
		node map[string]any
	}{
		{"a member's spend is negative", map[string]any{"month": budget.MonthKey(wsMeterNow), "members": map[string]any{wsMeterMember: -1}}},
		{"a reservation holds nothing", map[string]any{"month": budget.MonthKey(wsMeterNow), "reservations": map[string]any{"call-1": map[string]any{"member": wsMeterMember, "amount": 0, "at": 1}}}},
		{"the month is not the one its path names", map[string]any{"month": "2026-10", "members": map[string]any{wsMeterMember: 1}}},
	} {
		db.Set(path, tc.node)
		if _, err := budget.ReadWorkspaceRoom(ctx, client, wsMeterName, c, wsMeterNow); !errors.Is(err, budget.ErrWorkspaceSpendUnreadable) {
			t.Fatalf("%s: the read came back as %v, want the unreadable-node refusal", tc.name, err)
		}
		if _, _, err := budget.ReserveWorkspaceSpend(ctx, client, wsMeterReserve(t, "call-2", wsMeterMember, 0.5, c)); !errors.Is(err, budget.ErrWorkspaceSpendUnreadable) {
			t.Fatalf("%s: a reservation against that node came back as %v, want the unreadable-node refusal", tc.name, err)
		}
		db.Set(path, nil)
	}
}

// A month with no node is a month that starts empty, not a refusal: the figure
// is the ceiling's own, and nothing has to be reset at a boundary.
func TestWorkspaceCapReadsAnEmptyMonthAsNoSpend(t *testing.T) {
	ctx := context.Background()
	_, client := wsMeterDB(t)
	c := wsMeterCeiling(t, 2)

	room, err := budget.ReadWorkspaceRoom(ctx, client, wsMeterName, c, wsMeterNow)
	if err != nil {
		t.Fatalf("ReadWorkspaceRoom: %v", err)
	}
	if room.Settled != 0 || room.Reserved != 0 || room.Headroom != c.Monthly {
		t.Fatalf("an empty month reads %v settled, %v reserved and %v of headroom, want 0, 0 and the whole ceiling", room.Settled.USD(), room.Reserved.USD(), room.Headroom.USD())
	}
	if _, found, err := budget.ReadWorkspaceSpend(ctx, client, wsMeterName, budget.MonthKey(wsMeterNow)); err != nil || found {
		t.Fatalf("ReadWorkspaceSpend on an empty month: found=%v err=%v, want not found and no error", found, err)
	}
}

// contendedDB never lets a write land: the node keeps changing under the
// meter's feet.
type contendedDB struct {
	*rtdb.Client
	attempts int
}

func (c *contendedDB) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	c.attempts++
	return c.Client.PutIfMatch(ctx, path, etag+"-stale", v)
}

// A node that keeps changing under the write is a refusal after a bounded
// number of attempts, never an unbounded retry: the caller refuses the call.
func TestWorkspaceCapGivesUpOnAContendedMeter(t *testing.T) {
	ctx := context.Background()
	_, client := wsMeterDB(t)
	c := wsMeterCeiling(t, 2)
	contended := &contendedDB{Client: client}

	_, _, err := budget.ReserveWorkspaceSpend(ctx, contended, wsMeterReserve(t, "call-1", wsMeterMember, 1, c))
	if !errors.Is(err, budget.ErrWorkspaceSpendContention) {
		t.Fatalf("the reservation came back as %v, want the contention refusal", err)
	}
	if contended.attempts != budget.SetAttempts {
		t.Fatalf("the meter wrote %d times, want the bounded %d", contended.attempts, budget.SetAttempts)
	}
}

// The month key is UTC's calendar month, which is the period the workspace's
// cap is stated over.
func TestWorkspaceCapMonthKeyIsTheUTCMonth(t *testing.T) {
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), "2026-11"},
		{time.Date(2026, 11, 30, 23, 59, 59, 0, time.UTC), "2026-11"},
		{time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), "2026-12"},
		// A local time that has already turned the month is still the month
		// before it in UTC, which is the month the cap is stated over.
		{time.Date(2026, 12, 1, 0, 30, 0, 0, time.FixedZone("ahead", 3600)), "2026-11"},
		{time.Date(2026, 11, 30, 23, 30, 0, 0, time.FixedZone("behind", -3600)), "2026-12"},
	} {
		if got := budget.MonthKey(tc.at); got != tc.want {
			t.Fatalf("MonthKey(%v) = %q, want %q", tc.at, got, tc.want)
		}
	}
}
