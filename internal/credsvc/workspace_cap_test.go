package credsvc

// W7: the workspace's own cap at the layer that takes it — the meter the
// workspace's service reserves against before a call it pays for and settles on
// the response's usage (docs/workspaces/spec.md §5, D1301 as amended
// 2026-11-04).
//
// The meter is what the service's forwarding path calls, and it is deliberately
// the whole of the interface these tests need: the figure lives in the
// workspace's OWN database, one conditional write per reserve and per settle,
// and what a member is told is the advisory headroom the answer carries. The
// tests below hold the four properties the cap rests on — the room is taken
// before the call and released into the figure by the settle, a call that would
// pass the ceiling is refused before it is made, a settle that cannot be written
// refuses the call, and the figure is the workspace's own and no member's — plus
// the two the spec states about the delivery of the number (it arrives on the
// service's answer, and it is written into no member's database) and the front
// door (a member that cannot be resolved as a caller is refused before anything
// is counted).
//
// Every value here is a name or a dollar amount; no literal is, or looks like,
// a credential.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// wsMeterDB is the workspace's own database with a client of it: the one
// database the meter reads the workspace's record from and writes the figure to.
func wsMeterDB(t *testing.T) (*gcpfake.RTDB, *rtdb.Client) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("rtdb.New: %v", err)
	}
	return db, client
}

// wsMicros is a dollar amount of the fixture's, converted the one way the
// project converts money.
func wsMicros(t *testing.T, usd float64) pricing.Micros {
	t.Helper()
	m, err := pricing.FromUSD(usd)
	if err != nil {
		t.Fatalf("pricing.FromUSD(%v): %v", usd, err)
	}
	return m
}

// wsSeedRecord writes the workspace's own record — the node `fugaro update`
// publishes into the workspace's database — with the cap the test is about. It
// is the ONLY place the ceiling comes from: the meter reads it and is given no
// cap of its own, so a test that seeds no record is a workspace that states no
// ceiling.
func wsSeedRecord(t *testing.T, db *gcpfake.RTDB, capUSD float64) {
	t.Helper()
	cap := map[string]any{}
	if capUSD > 0 {
		cap = map[string]any{"monthly_micros": int64(wsMicros(t, capUSD))}
	}
	db.Set(projectpolicy.PathWorkspace(wsWorkspace), map[string]any{
		"version": projectpolicy.WorkspaceVersion,
		"name":    wsWorkspace,
		"members": []any{map[string]any{"project": wsMemberA, "repos": []any{}}, map[string]any{"project": wsMemberB, "repos": []any{}}},
		"cap":     cap,
	})
}

// wsFencedDB is the workspace's own database with a fence around the
// workspace's own tree: the meter may read and write the workspace's record
// (projectpolicy.PathWorkspace) and its meter (budget.PathWorkspaceSpend), and
// every other path is refused and recorded. It is what gives the two tests
// below teeth: the database they hand the meter HOLDS a member's own figure,
// and a meter that counted or wrote it — or merely read it — would have to ask
// for that path, which this refuses and names rather than passing quietly.
type wsFencedDB struct {
	*rtdb.Client
	outside []string
	writes  []string
}

// wsFenced is the workspace's own database, seeded, fenced, and handed to the
// meter in the fence's place.
func wsFenced(t *testing.T) (*gcpfake.RTDB, budget.WorkspaceSpendDB, *wsFencedDB) {
	t.Helper()
	db, client := wsMeterDB(t)
	fenced := &wsFencedDB{Client: client}
	return db, fenced, fenced
}

// inside reports whether a path is one of the workspace's own: its record, or
// its meter for the fixture's month.
func (f *wsFencedDB) inside(path string) bool {
	if path == projectpolicy.PathWorkspace(wsWorkspace) {
		return true
	}
	return strings.HasPrefix(path, budget.PathWorkspaceSpend(wsWorkspace, budget.MonthKey(testNow())))
}

func (f *wsFencedDB) fence(path string) error {
	if f.inside(path) {
		return nil
	}
	f.outside = append(f.outside, path)
	return fmt.Errorf("the meter asked for %s, which is not the workspace's own tree", path)
}

func (f *wsFencedDB) Get(ctx context.Context, path string, out any) (bool, error) {
	if err := f.fence(path); err != nil {
		return false, err
	}
	return f.Client.Get(ctx, path, out)
}

func (f *wsFencedDB) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	if err := f.fence(path); err != nil {
		return "", false, err
	}
	return f.Client.GetETag(ctx, path, out)
}

func (f *wsFencedDB) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	if err := f.fence(path); err != nil {
		return err
	}
	f.writes = append(f.writes, path)
	return f.Client.PutIfMatch(ctx, path, etag, v)
}

// wsMeter is the workspace's meter over one database, with the fixture's clock.
func wsMeter(t *testing.T, client budget.WorkspaceSpendDB) *WorkspaceMeter {
	t.Helper()
	m, err := NewWorkspaceMeter(WorkspaceMeterConfig{
		Name: wsWorkspace,
		DB:   client,
		Now:  testNow,
	})
	if err != nil {
		t.Fatalf("NewWorkspaceMeter: %v", err)
	}
	return m
}

// wsSpendNode is the figure as the workspace's own database holds it.
func wsSpendNode(t *testing.T, db *gcpfake.RTDB) map[string]any {
	t.Helper()
	v, _ := db.Value(budget.PathWorkspaceSpend(wsWorkspace, budget.MonthKey(testNow()))).(map[string]any)
	return v
}

// The red step of W7. With the basis at the proxy, a call served without a
// reservation taken first is a call that can pass the cap: the reserve and the
// settle together are the whole of what makes the workspace's figure true.
func TestWorkspaceCapReservesBeforeTheCallAndSettlesOnItsUsage(t *testing.T) {
	ctx := context.Background()
	db, client := wsMeterDB(t)
	wsSeedRecord(t, db, 2)
	m := wsMeter(t, client)

	worst := wsMicros(t, 1)    // the call's worst case: what the service reserves
	usage := wsMicros(t, 0.25) // what the response says the call cost

	hold, room, err := m.Reserve(ctx, wsMemberA, "call-1", worst)
	if err != nil {
		t.Fatalf("Reserve refused a call of %v against a ceiling of $2: %v", worst.USD(), err)
	}
	if room.Settled != 0 {
		t.Fatalf("the call is in flight and the settled figure is already %v", room.Settled.USD())
	}
	if room.Reserved != worst {
		t.Fatalf("the room counts %v of reservation, want the whole worst case %v", room.Reserved.USD(), worst.USD())
	}
	if room.Headroom != wsMicros(t, 1) {
		t.Fatalf("headroom is %v with $1 reserved out of $2, want $1", room.Headroom.USD())
	}
	if node := wsSpendNode(t, db); node == nil {
		t.Fatal("the reservation is in no node of the workspace's own database")
	}

	after, err := m.Settle(ctx, hold, usage)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if after.Reserved != 0 {
		t.Fatalf("%v is still reserved after the call settled", after.Reserved.USD())
	}
	if after.Settled != usage {
		t.Fatalf("the settled figure is %v, want what the call cost (%v) and not the worst case", after.Settled.USD(), usage.USD())
	}
	if after.Headroom != wsMicros(t, 1.75) {
		t.Fatalf("headroom is %v after a $0.25 call out of $2, want $1.75", after.Headroom.USD())
	}

	// The figure is in the workspace's OWN database, keyed by the month, so a
	// second service reading the same database sees the same number.
	fresh, err := wsMeter(t, client).Room(ctx)
	if err != nil {
		t.Fatalf("Room: %v", err)
	}
	if fresh.Settled != usage {
		t.Fatalf("a fresh service reads %v settled, want %v", fresh.Settled.USD(), usage.USD())
	}
}

func TestWorkspaceCapRefusesTheCallThatWouldExceedIt(t *testing.T) {
	ctx := context.Background()
	db, client := wsMeterDB(t)
	wsSeedRecord(t, db, 2)
	m := wsMeter(t, client)

	hold, _, err := m.Reserve(ctx, wsMemberA, "call-1", wsMicros(t, 1.5))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if _, err := m.Settle(ctx, hold, wsMicros(t, 1.5)); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	hold2, room, err := m.Reserve(ctx, wsMemberB, "call-2", wsMicros(t, 0.6))
	if err == nil {
		t.Fatalf("Reserve admitted a $0.60 call with $0.50 of a $2 ceiling left (hold %+v)", hold2)
	}
	if room.Headroom != wsMicros(t, 0.5) {
		t.Fatalf("the refusal reports %v of headroom, want $0.50", room.Headroom.USD())
	}
	// A refused call takes no room: the figure is the one that refused it.
	node := wsSpendNode(t, db)
	if res, ok := node["reservations"].(map[string]any); ok && len(res) != 0 {
		t.Fatalf("the refused call left %d reservations behind: %v", len(res), res)
	}
	if got, _ := node["members"].(map[string]any)[wsMemberA]; got == nil {
		t.Fatal("the settled figure was lost by the refused call")
	}
}

// A call whose usage cannot be written back is refused rather than served: a
// call whose cost the service cannot record spends against a cap it cannot see.
// The failure is not swallowed and not settled as zero, and the reservation is
// still there for the retry.
func TestFailedSettleRefusesTheCall(t *testing.T) {
	ctx := context.Background()
	db, client := wsMeterDB(t)
	wsSeedRecord(t, db, 2)
	m := wsMeter(t, client)

	hold, _, err := m.Reserve(ctx, wsMemberA, "call-1", wsMicros(t, 1))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	db.DenyNext(1)
	if _, err := m.Settle(ctx, hold, wsMicros(t, 0.25)); err == nil {
		t.Fatal("Settle reported success although the database refused the write")
	}
	room, err := m.Room(ctx)
	if err != nil {
		t.Fatalf("Room: %v", err)
	}
	if room.Settled != 0 {
		t.Fatalf("a settle that failed recorded %v anyway", room.Settled.USD())
	}
	if room.Reserved != wsMicros(t, 1) {
		t.Fatalf("the reservation is %v after a failed settle, want the whole $1 still in flight", room.Reserved.USD())
	}
	// The call is refused, so the retry is the service's next attempt at the
	// same call: it settles the reservation it still holds — until the hold is
	// older than budget.WorkspaceReservationTTL, which is where a service that
	// never retries releases the room instead of holding it to the month's end.
	if _, err := m.Settle(ctx, hold, wsMicros(t, 0.25)); err != nil {
		t.Fatalf("the retried settle was refused: %v", err)
	}
}

// D1300, stated as a test so the docs cannot drift from it: the workspace's cap
// bounds the calls the WORKSPACE's credentials pay for. A member's own runs, its
// own ledger and its own caps are another quantity, and the meter touches none
// of them.
//
// The member's figure is in the database the meter is handed, at the member's
// own run path, and every path outside the workspace's own tree is refused and
// recorded (wsFencedDB): a meter that read the member's ledger would fail here
// by name rather than pass whatever it did.
func TestWorkspaceCapBoundsOnlyTheCredentialsItPaysFor(t *testing.T) {
	ctx := context.Background()
	db, meterDB, fenced := wsFenced(t)
	wsSeedRecord(t, db, 2)
	m := wsMeter(t, meterDB)

	// The member's own spend — its own run's ledger node — in the very database
	// the meter holds. It is the member's figure for the member's own cap, and
	// the workspace's service counts none of it.
	memberRun := budget.PathRun(wsSlug, "20261020-090000-ab12")
	db.Set(memberRun, map[string]any{"spent": int64(wsMicros(t, 90))})

	room, err := m.Room(ctx)
	if err != nil {
		t.Fatalf("Room: %v", err)
	}
	if room.Settled != 0 {
		t.Fatalf("the member's own spend of $90 moved the workspace's figure to %v", room.Settled.USD())
	}
	if room.Headroom != wsMicros(t, 2) {
		t.Fatalf("headroom is %v with a member holding $90 of its own spend, want the whole $2", room.Headroom.USD())
	}

	hold, _, err := m.Reserve(ctx, wsMemberA, "call-1", wsMicros(t, 1))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if _, err := m.Settle(ctx, hold, wsMicros(t, 0.25)); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	room, err = m.Room(ctx)
	if err != nil {
		t.Fatalf("Room: %v", err)
	}
	if room.Settled != wsMicros(t, 0.25) {
		t.Fatalf("the workspace's figure is %v after one $0.25 call, want $0.25", room.Settled.USD())
	}
	// Nothing of the member's was asked for, and the member's own figure is
	// where it was: the meter's whole world is the workspace's own tree.
	if len(fenced.outside) != 0 {
		t.Fatalf("the meter reached outside the workspace's own tree: %v", fenced.outside)
	}
	if len(fenced.writes) == 0 {
		t.Fatal("the meter wrote nothing, so the fence above held nothing about what it writes")
	}
	if got := db.Value(memberRun); got == nil {
		t.Fatalf("the member's own ledger node is gone after the workspace's call: %v", got)
	}
}

// The advisory number reaches the member on the service's own answer and is
// written into no node of the member's database: that is what lets the
// workspace's service hold no write on a member at all (O1's answer).
//
// The member's own ledger is seeded into the database the meter holds, and the
// whole of what the meter wrote is held to the workspace's own meter path
// (wsFencedDB): the delivery is the header, and the only node it moves is the
// workspace's own.
func TestHeadroomIsDeliveredByTheServiceAndWrittenNowhere(t *testing.T) {
	ctx := context.Background()
	db, meterDB, fenced := wsFenced(t)
	wsSeedRecord(t, db, 2)
	m := wsMeter(t, meterDB)
	memberRun := budget.PathRun(wsSlug, "20261020-090000-ab12")
	db.Set(memberRun, map[string]any{"spent": int64(wsMicros(t, 90))})

	hold, _, err := m.Reserve(ctx, wsMemberA, "call-1", wsMicros(t, 1))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	defer func() {
		if _, err := m.Settle(ctx, hold, wsMicros(t, 0.25)); err != nil {
			t.Errorf("Settle: %v", err)
		}
		if got := db.Value(memberRun); got == nil {
			t.Errorf("the member's own ledger node is gone after the reservation")
		}
	}()
	room, err := m.Room(ctx)
	if err != nil {
		t.Fatalf("Room: %v", err)
	}

	h := http.Header{}
	SetWorkspaceHeadroom(h, room)
	if got, want := h.Get(gateway.WorkspaceHeadroomHeader), "1000000"; got != want {
		t.Fatalf("the answer carries %q as %v, want %q: the header the member's gateway reads is the wire", gateway.WorkspaceHeadroomHeader, got, want)
	}

	// The number goes into the workspace's own meter — nowhere of the member's.
	// The member's own ledger is in the database the meter holds (wsFencedDB),
	// so a write of it anywhere but the workspace's own tree is refused and
	// recorded rather than passing quietly.
	if len(fenced.outside) != 0 {
		t.Fatalf("the delivery reached outside the workspace's own tree: %v", fenced.outside)
	}
	for _, p := range fenced.writes {
		if !strings.HasPrefix(p, budget.PathWorkspaceSpend(wsWorkspace, budget.MonthKey(testNow()))) {
			t.Fatalf("the delivery wrote %s, which is not the workspace's own meter", p)
		}
	}
	if got := db.Value(budget.PathWorkspaceSpend(wsWorkspace, budget.MonthKey(testNow()))); got == nil {
		t.Fatal("the reservation is in no node of the workspace's own database")
	}
}

// A member the workspace cannot resolve as a caller is refused at the front
// door, so its calls are never served and never counted: the meter is asked
// nothing, and the workspace's figure stays where it was.
func TestMemberThatCannotBeResolvedIsRefusedAtTheFrontDoor(t *testing.T) {
	ctx := context.Background()
	db, client := wsMeterDB(t)
	wsSeedRecord(t, db, 2)
	f := newWSFixture(t)
	m := wsMeter(t, client)

	// An account no member of the workspace binds.
	if _, err := f.verify(wsMemberA, wsAccountOut); err == nil {
		t.Fatalf("Verify admitted %v, which the workspace's table does not bind", wsAccountOut)
	}
	room, err := m.Room(ctx)
	if err != nil {
		t.Fatalf("Room: %v", err)
	}
	if room.Settled != 0 || room.Reserved != 0 {
		t.Fatalf("a refused caller moved the workspace's figure: settled %v, reserved %v", room.Settled.USD(), room.Reserved.USD())
	}
	if wsSpendNode(t, db) != nil {
		t.Fatal("a refused caller left a node in the workspace's meter")
	}
}

// The clock the meter reads the month from is the one the service was built
// with, and a call reserved in one month settles against that month: the cap it
// was admitted under is the cap it is counted against.
func TestWorkspaceCapSettlesACallAgainstTheMonthItWasReservedIn(t *testing.T) {
	ctx := context.Background()
	db, client := wsMeterDB(t)
	wsSeedRecord(t, db, 2)
	// A call taken at the last seconds of a month and settled in the next one:
	// the boundary is crossed while the call is in flight, which is the only
	// way the two months can differ for a call that is still one.
	now := time.Date(2026, 10, 31, 23, 59, 58, 0, time.UTC)
	reserved := now
	m, err := NewWorkspaceMeter(WorkspaceMeterConfig{Name: wsWorkspace, DB: client, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewWorkspaceMeter: %v", err)
	}

	hold, _, err := m.Reserve(ctx, wsMemberA, "call-1", wsMicros(t, 1))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	now = now.Add(5 * time.Second) // midnight passes into the next month
	if _, err := m.Settle(ctx, hold, wsMicros(t, 0.25)); err != nil {
		t.Fatalf("Settle across the month boundary: %v", err)
	}
	if _, ok := db.Value(budget.PathWorkspaceSpend(wsWorkspace, budget.MonthKey(reserved))).(map[string]any); !ok {
		t.Fatal("the call did not settle against the month it was reserved in")
	}
	if v := db.Value(budget.PathWorkspaceSpend(wsWorkspace, budget.MonthKey(now))); v != nil {
		t.Fatalf("the next month holds %v, and a month starts empty", v)
	}
	room, err := m.Room(ctx)
	if err != nil {
		t.Fatalf("Room: %v", err)
	}
	if room.Settled != 0 || room.Headroom != wsMicros(t, 2) {
		t.Fatalf("the new month reads %v settled and %v of headroom, want 0 and $2", room.Settled.USD(), room.Headroom.USD())
	}
}

// A workspace that states no ceiling is not a workspace with a ceiling of zero:
// every call fits and the answer carries no headroom, because there is none to
// carry.
func TestWorkspaceCapWithNoCeilingAdmitsEveryCall(t *testing.T) {
	ctx := context.Background()
	db, client := wsMeterDB(t)
	wsSeedRecord(t, db, 0)
	m := wsMeter(t, client)

	hold, room, err := m.Reserve(ctx, wsMemberA, "call-1", wsMicros(t, 50))
	if err != nil {
		t.Fatalf("Reserve refused a call although the workspace states no ceiling: %v", err)
	}
	if room.Ceiling.Set {
		t.Fatal("the meter reports a ceiling the workspace does not state")
	}
	h := http.Header{}
	SetWorkspaceHeadroom(h, room)
	if got := h.Get(gateway.WorkspaceHeadroomHeader); got != "" {
		t.Fatalf("the answer carries %q as headroom although the workspace states no ceiling", got)
	}
	if _, err := m.Settle(ctx, hold, wsMicros(t, 50)); err != nil {
		t.Fatalf("Settle: %v", err)
	}
}

// A meter built without the workspace's own database is a service that could
// serve calls it cannot meter, so it is refused where it is built.
func TestNewWorkspaceMeterRefusesAConfigurationItCannotMeterWith(t *testing.T) {
	_, client := wsMeterDB(t)
	for _, tc := range []struct {
		name string
		cfg  WorkspaceMeterConfig
	}{
		{"no database", WorkspaceMeterConfig{Name: wsWorkspace, Now: testNow}},
		{"no workspace", WorkspaceMeterConfig{DB: client, Now: testNow}},
	} {
		if _, err := NewWorkspaceMeter(tc.cfg); err == nil {
			t.Fatalf("%v: NewWorkspaceMeter built a meter anyway", tc.name)
		}
	}
}
